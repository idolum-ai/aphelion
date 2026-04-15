//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const maxReviewEventsPerTurn = 10

func (r *Runtime) HandleInbound(ctx context.Context, msg core.InboundMessage) (result *core.TurnResult, err error) {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return r.handleDurableTelegramGroupInbound(ctx, msg)
	}
	actor, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok {
		return nil, ErrPrincipalDenied
	}
	stopTyping := r.startChatActionLoop(ctx, msg.ChatID, "typing")
	defer stopTyping()

	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}
	unlock := r.lockSession(key)
	defer unlock()

	tools := r.toolsForPrincipal(actor, key)
	sess, err := r.store.Load(key)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	applySessionScope(sess, key)

	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return nil, fmt.Errorf("resolve principal scope: %w", err)
	}
	now := time.Now().UTC()
	prepared, err := r.prepareInboundTurn(ctx, scope, msg)
	if err != nil {
		return nil, err
	}
	audit := newTurnAuditRecorder(key, "telegram", string(actor.Role), prepared.LedgerText)
	defer r.emitTurnAudit(audit)
	facePolicy := pipeline.DecideInteractiveFacePolicy(sess, prepared.LedgerText)
	useMaterialFloor := pipeline.ShouldUseMaterialFloorContract(string(r.faceBackend), facePolicy)
	exec := r.executionForTurn(prepared)
	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		return nil, fmt.Errorf("load workspace prompt context: %w", err)
	}
	hiddenInputs := r.assembleInteractiveHiddenInputs(ctx, scope, now, prepared.LedgerText)
	governorAwareness := r.withOperationAwareness(r.withPlanAwareness(r.withHiddenInputAwareness(r.governorRuntimeAwareness(scope, session.TurnRunKindInteractive, "telegram", exec), hiddenInputs), sess.PlanState), sess.OperationState)
	if useMaterialFloor {
		governorAwareness.ArtifactMode = "floor"
	}
	baseGovernorAwareness := governorAwareness
	sess.ChatType = "dm"
	sess.UserName = msg.SenderName
	brokerage := turnBrokerage{}
	extraUsage := core.TokenUsage{}
	currentFaceModel := r.currentFaceRenderer()
	requestFaceNote := func(mode string, awareness prompt.RuntimeAwareness, priorProposal string, feedback string) (string, core.TokenUsage, error) {
		proposer, ok := currentFaceModel.(face.Proposer)
		if !ok || r.faceBackend == face.BackendFloorFallback {
			return "", core.TokenUsage{}, nil
		}
		proposal, proposalErr := proposer.Propose(ctx, face.ProposalRequest{
			GovernorName:      prompt.DefaultGovernorName,
			FaceName:          face.DefaultFaceName,
			Channel:           "telegram",
			Mode:              mode,
			PrincipalRole:     string(actor.Role),
			WorkspaceRoot:     faceWorkspaceRoot(scope),
			LatestUserInput:   prepared.LedgerText,
			PriorProposal:     priorProposal,
			BrokerageFeedback: feedback,
			Runtime:           awareness,
		})
		if proposalErr != nil {
			return "", core.TokenUsage{}, proposalErr
		}
		return strings.TrimSpace(proposal), consumeFaceUsage(currentFaceModel), nil
	}
	if facePolicy.Proposal {
		faceProposalAwareness := baseGovernorAwareness
		faceProposalAwareness.ArtifactMode = "scene"
		proposal, usage, proposalErr := requestFaceNote("proposal", faceProposalAwareness, "", "")
		if proposalErr != nil {
			log.Printf("WARN idolum proposal failed backend=%s err=%v", r.faceBackend, proposalErr)
		} else {
			brokerage.IdolumNote = proposal
			brokerage.Active = brokerage.IdolumNote != ""
			if suggestedContract := pipeline.ParseExecutionContract(proposal); suggestedContract != nil {
				brokerage.Phase = brokeragePhaseName(brokerage.Active, "brokerage")
				brokerage.SuggestedExecutionContract = suggestedContract
			} else {
				brokerage.Phase = brokeragePhaseName(brokerage.Active, "proposal")
			}
			extraUsage = addTokenUsage(extraUsage, usage)
		}
	}
	governorAwareness = r.withBrokerageAwareness(governorAwareness, brokerage)
	governorPrompt := prompt.GovernorRequest{
		GovernorName:    prompt.DefaultGovernorName,
		GovernorBackend: exec.Backend,
		PrincipalRole:   string(actor.Role),
		WorkspaceRoot:   scope.WorkingRoot,
		ToolManifest:    toolManifest(tools),
		Workspace:       promptContext,
		Runtime:         governorAwareness,
	}
	systemBlocks := prompt.BuildGovernorPromptBlocks(governorPrompt)
	systemPrompt := prompt.RenderSystemBlocks(systemBlocks)
	sess.SystemPrompt = systemPrompt

	sess, history, err := r.maybeCompactSession(ctx, key, sess, systemBlocks, prepared.UserText, brokerage.IdolumNote)
	if err != nil {
		return nil, fmt.Errorf("maybe compact session: %w", err)
	}
	if brokerage.Active && brokerage.Phase == "brokerage" && strings.TrimSpace(brokerage.IdolumNote) != "" {
		updated, usage := r.convergeTurnBrokerage(ctx, exec, baseGovernorAwareness, systemBlocks, history, prepared.UserText, brokerage, requestFaceNote, audit)
		extraUsage = addTokenUsage(extraUsage, usage)
		brokerage = updated
		if brokerage.Phase == "brokerage" && brokerage.Ratification == "accept" {
			sess.PlanState = maybeSeedPlanFromBrokerage(sess.PlanState, brokerage)
		}
		governorAwareness = r.withOperationAwareness(r.withPlanAwareness(r.withBrokerageAwareness(governorAwareness, brokerage), sess.PlanState), sess.OperationState)
		governorPrompt.Runtime = governorAwareness
		systemBlocks = prompt.BuildGovernorPromptBlocks(governorPrompt)
		systemPrompt = prompt.RenderSystemBlocks(systemBlocks)
		sess.SystemPrompt = systemPrompt
	}
	progress := r.newToolProgressReporter(msg, sess.PlanState, audit)
	monitor := r.startTurnMonitor(key, session.TurnRunKindInteractive, prepared.LedgerText, progress, audit)
	defer monitor.Finish(ctx, err)
	tools = monitor.observeTools(tools)

	input := make([]agent.Message, 0, len(history)+2)
	if systemPrompt != "" {
		input = append(input, agent.Message{Role: "system", Content: systemPrompt, SystemBlocks: systemBlocks})
	}
	if advisory := brokerageContextForGovernor(brokerage); advisory != "" {
		input = append(input, agent.Message{Role: "system", Content: advisory})
	}
	input = append(input, history...)
	input = append(input, agent.Message{Role: "user", Content: prepared.UserText, Media: prepared.AgentMedia})

	result, outHistory, err := agent.RunTurn(ctx, exec.Provider, tools, &agent.Budget{
		Max:     r.cfg.Agent.MaxIterations,
		Caution: 0.7,
		Warning: 0.9,
	}, r.reasoningOptionsForRun(session.TurnRunKindInteractive), input)
	if err != nil {
		return nil, fmt.Errorf("run turn: %w", err)
	}

	if len(outHistory) < len(input) {
		return nil, fmt.Errorf("invalid turn output: history shrank from %d to %d", len(input), len(outHistory))
	}

	result.Text, result.Media = extractOutboundReplyMedia(scope, result.Text, result.Media)
	audit.RecordGovernorReply(result.Text, result.Media)
	sess.TurnCount++
	mediaOnlyReply := len(result.Media) > 0 && strings.TrimSpace(result.Text) == ""
	materialFloor := core.MaterialPacket{}
	floorText := ""
	if !mediaOnlyReply {
		materialFloor, floorText, _ = pipeline.BuildFloorFromGovernor(result.Text, useMaterialFloor)
	}
	floorMetadataState := hiddenInputs.Metadata()
	floorMetadataState.Artifacts = append(floorMetadataState.Artifacts, prepared.ArtifactRefs...)
	floorMetadata := encodeFloorMetadata(floorMetadataState)
	replyWithVoice := r.shouldReplyWithVoice(prepared.InboundWasVoice) && len(result.Media) == 0
	fallbackOpts := face.FallbackOptions{Channel: "telegram", Voice: replyWithVoice}
	replyText := ""
	if !mediaOnlyReply {
		replyText = face.SerializeFloorFallback(materialFloor, floorText, fallbackOpts)
	}
	streamedReply := false
	if operationState, operationErr := r.store.OperationState(key); operationErr == nil {
		sess.OperationState = mergeSessionOperationState(sess.OperationState, operationState)
	} else {
		return nil, fmt.Errorf("load operation state before save: %w", operationErr)
	}
	faceAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindInteractive, "telegram", exec)
	faceAwareness = r.withOperationAwareness(r.withBrokerageAwareness(faceAwareness, brokerage), sess.OperationState)
	renderResult, renderErr := r.renderTurnReply(turnRenderInput{
		Ctx:              ctx,
		Scope:            scope,
		Msg:              msg,
		Channel:          "telegram",
		PrincipalRole:    string(actor.Role),
		OutHistory:       outHistory,
		HistoryInputLen:  len(input),
		Result:           result,
		FacePolicy:       facePolicy,
		UseMaterialFloor: useMaterialFloor,
		MediaOnlyReply:   mediaOnlyReply,
		ReplyText:        replyText,
		FloorText:        floorText,
		MaterialFloor:    materialFloor,
		FallbackOpts:     fallbackOpts,
		FaceAwareness:    faceAwareness,
		CurrentFaceModel: currentFaceModel,
		ReplyWithVoice:   replyWithVoice,
		AllowStream:      true,
		PromptInput:      prepared.LedgerText,
		Audit:            audit,
	})
	if renderErr != nil {
		return result, renderErr
	}
	replyText = renderResult.ReplyText
	streamedReply = renderResult.StreamedReply
	extraUsage = addTokenUsage(extraUsage, renderResult.Usage)
	result.TokenUsage = addTokenUsage(result.TokenUsage, extraUsage)

	commit, err := r.commitTurn(ctx, turnCommitInput{
		Key:             key,
		Sess:            sess,
		Prepared:        prepared,
		OutHistory:      outHistory,
		HistoryInputLen: len(input),
		Result:          result,
		FloorText:       floorText,
		FloorMetadata:   floorMetadata,
		ReplyText:       replyText,
		StreamedReply:   streamedReply,
		OutboundID:      renderResult.OutboundID,
		OutboundType:    renderResult.OutboundType,
		RecordOutbound:  true,
		SendReply: func(_ context.Context) (int64, string, error) {
			outID, outType, sendErr := r.sendReply(ctx, msg, replyText, result.Media, prepared.InboundWasVoice)
			if sendErr != nil {
				return 0, "", fmt.Errorf("send outbound reply: %w", sendErr)
			}
			return outID, outType, nil
		},
		Audit: audit,
		PostCommitHooks: []func() error{
			func() error {
				if !shouldGenerateReviewEvent(actor, key) {
					return nil
				}
				return r.enqueueReviewEventsForTurn(actor, msg, sess.TurnCount, prepared.LedgerText, replyText, result.ToolLog)
			},
			func() error {
				if actor.Role != principal.RoleAdmin {
					return nil
				}
				return r.deliverReviewEvents(ctx, key, sess)
			},
		},
		ErrorConvertMessages: "convert new messages",
		ErrorLoadPlanState:   "load plan state before save",
		ErrorLoadOperation:   "load operation state before save",
		ErrorSaveSession:     "save session",
		ErrorRecordOutbound:  "record outbound reply",
	})
	if err != nil {
		if commit.Committed {
			return result, err
		}
		return nil, err
	}
	return result, nil
}

type faceUsageConsumer interface {
	ConsumeLastUsage() core.TokenUsage
}

func consumeFaceUsage(model face.Renderer) core.TokenUsage {
	consumer, ok := model.(faceUsageConsumer)
	if !ok {
		return core.TokenUsage{}
	}
	return consumer.ConsumeLastUsage()
}

func addTokenUsage(dst core.TokenUsage, src core.TokenUsage) core.TokenUsage {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.TotalTokens += src.TotalTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	return dst
}

func replaceLastAssistantWithSceneText(messages []session.Message, sceneText string) []session.Message {
	trimmed := strings.TrimSpace(sceneText)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].Content = trimmed
			messages[i].ContentChars = len(trimmed)
			return messages
		}
	}
	if trimmed == "" {
		return messages
	}

	turnIndex := 0
	if len(messages) > 0 {
		turnIndex = messages[len(messages)-1].TurnIndex
	}
	return append(messages, session.Message{
		Role:         "assistant",
		Content:      trimmed,
		ContentChars: len(trimmed),
		TurnIndex:    turnIndex,
	})
}

func shouldGenerateReviewEvent(actor principal.Principal, key session.SessionKey) bool {
	if actor.Role != principal.RoleAdmin {
		return true
	}
	// Future-compatible hook: subordinate sessions from admin principals still produce digests.
	return key.UserID != 0
}

func (r *Runtime) enqueueReviewEventsForTurn(
	actor principal.Principal,
	msg core.InboundMessage,
	turnIndex int,
	userText string,
	sceneText string,
	toolLog []string,
) error {
	targets := uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)
	if len(targets) == 0 {
		return nil
	}

	summary := session.BuildReviewSummary(session.ReviewSummaryInput{
		SourceChatID: msg.ChatID,
		SourceUserID: msg.SenderID,
		SourceRole:   string(actor.Role),
		SourceScope:  telegramDMScopeRef(msg.ChatID),
		TurnIndex:    turnIndex,
		UserText:     userText,
		SceneText:    sceneText,
		ToolLog:      toolLog,
	}, session.DefaultReviewSummaryMaxChars)

	for _, adminChatID := range targets {
		if err := r.store.EnqueueReviewEvent(session.ReviewEvent{
			SourceChatID:      msg.ChatID,
			SourceUserID:      msg.SenderID,
			SourceRole:        string(actor.Role),
			SourceScope:       telegramDMScopeRef(msg.ChatID),
			TargetAdminChatID: adminChatID,
			TargetScope:       telegramDMScopeRef(adminChatID),
			TurnFrom:          turnIndex,
			TurnTo:            turnIndex,
			Summary:           summary,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) deliverReviewEvents(ctx context.Context, key session.SessionKey, sess *session.Session) error {
	events, err := r.store.PendingReviewEvents(key.ChatID, maxReviewEventsPerTurn)
	if err != nil {
		return err
	}
	for _, event := range events {
		text := formatReviewEventMessage(event)
		msgID, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
			ChatID: key.ChatID,
			Text:   text,
		})
		if err != nil {
			return err
		}
		newMessages := appendAssistantTurn(sess, text, text, "")
		if err := r.store.Save(sess, newMessages, core.TokenUsage{}); err != nil {
			return err
		}
		if err := r.store.RecordOutbound(key, sess.TurnCount, msgID, "review_digest"); err != nil {
			return err
		}
		if err := r.store.MarkReviewDelivered([]int64{event.ID}); err != nil {
			return err
		}
	}
	return nil
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func formatReviewEventMessage(event session.ReviewEvent) string {
	turnRange := "n/a"
	if event.TurnFrom > 0 && event.TurnTo >= event.TurnFrom {
		turnRange = fmt.Sprintf("%d-%d", event.TurnFrom, event.TurnTo)
	} else if event.TurnFrom > 0 {
		turnRange = fmt.Sprintf("%d", event.TurnFrom)
	}
	return face.RenderReviewDigest(face.ReviewDigestNotice{
		SourceChatID: event.SourceChatID,
		SourceUserID: event.SourceUserID,
		SourceRole:   event.SourceRole,
		SourceScope:  formattedReviewEventScope(event),
		SourceAgent:  formattedReviewEventAgent(event),
		ParentScope:  formattedReviewEventParentScope(event),
		TurnRange:    turnRange,
		Summary:      strings.TrimSpace(event.Summary),
	})
}

func formattedReviewEventScope(event session.ReviewEvent) string {
	scope := session.NormalizeScopeRef(event.SourceScope)
	if scope.IsZero() {
		return ""
	}
	return scope.String()
}

func formattedReviewEventAgent(event session.ReviewEvent) string {
	scope := session.NormalizeScopeRef(event.SourceScope)
	return strings.TrimSpace(scope.DurableAgentID)
}

func formattedReviewEventParentScope(event session.ReviewEvent) string {
	scope := session.NormalizeScopeRef(event.SourceScope)
	if scope.ParentScopeKind == "" && scope.ParentScopeID == "" {
		return ""
	}
	parent := session.NormalizeScopeRef(session.ScopeRef{Kind: scope.ParentScopeKind, ID: scope.ParentScopeID})
	if parent.IsZero() {
		return ""
	}
	return parent.String()
}

func toolManifest(registry agent.ToolRegistry) string {
	if registry == nil {
		return ""
	}
	type manifestProvider interface {
		Manifest() string
	}
	if provider, ok := registry.(manifestProvider); ok {
		return provider.Manifest()
	}
	return renderToolManifest(registry.Definitions())
}

func renderToolManifest(defs []agent.ToolDef) string {
	if len(defs) == 0 {
		return ""
	}

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
