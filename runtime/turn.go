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
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

const maxReviewEventsPerTurn = 10

func (r *Runtime) HandleInbound(ctx context.Context, msg core.InboundMessage) (result *core.TurnResult, err error) {
	actor, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok {
		return nil, ErrPrincipalDenied
	}
	stopTyping := r.startChatActionLoop(ctx, msg.ChatID, "typing")
	defer stopTyping()

	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0}
	unlock := r.lockSession(key)
	defer unlock()

	tools := r.toolsForPrincipal(actor, key)
	sess, err := r.store.Load(key)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return nil, fmt.Errorf("resolve principal scope: %w", err)
	}
	now := time.Now().UTC()
	prepared, err := r.prepareInboundTurn(ctx, scope, msg)
	if err != nil {
		return nil, err
	}
	facePolicy := decideInteractiveFacePolicy(sess, prepared.LedgerText)
	useMaterialFloor := shouldUseMaterialFloorContract(r.faceBackend, facePolicy)
	exec := r.executionForTurn(prepared)
	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		return nil, fmt.Errorf("load workspace prompt context: %w", err)
	}
	hiddenInputs := r.assembleInteractiveHiddenInputs(ctx, scope, now, prepared.LedgerText)
	governorAwareness := r.withHiddenInputAwareness(r.governorRuntimeAwareness(scope, session.TurnRunKindInteractive, "telegram", exec), hiddenInputs)
	if useMaterialFloor {
		governorAwareness.ArtifactMode = "floor"
	}
	baseGovernorAwareness := governorAwareness
	sess.ChatType = "dm"
	sess.UserName = msg.SenderName
	brokerage := turnBrokerage{}
	extraUsage := core.TokenUsage{}
	currentFaceModel := r.currentFaceRenderer()
	requestFaceNote := func(mode string, awareness prompt.RuntimeAwareness) (string, core.TokenUsage, error) {
		proposer, ok := currentFaceModel.(face.Proposer)
		if !ok || r.faceBackend == face.BackendFloorFallback {
			return "", core.TokenUsage{}, nil
		}
		proposal, proposalErr := proposer.Propose(ctx, face.ProposalRequest{
			GovernorName:    prompt.DefaultGovernorName,
			FaceName:        face.DefaultFaceName,
			Channel:         "telegram",
			Mode:            mode,
			PrincipalRole:   string(actor.Role),
			WorkspaceRoot:   faceWorkspaceRoot(scope),
			LatestUserInput: prepared.LedgerText,
			Runtime:         awareness,
		})
		if proposalErr != nil {
			return "", core.TokenUsage{}, proposalErr
		}
		return strings.TrimSpace(proposal), consumeFaceUsage(currentFaceModel), nil
	}
	if facePolicy.Brokerage {
		faceProposalAwareness := baseGovernorAwareness
		faceProposalAwareness.ArtifactMode = "scene"
		proposal, usage, proposalErr := requestFaceNote("brokerage", r.withBrokerageAwareness(faceProposalAwareness, turnBrokerage{Active: true, Mode: "brokerage"}))
		if proposalErr != nil {
			log.Printf("WARN idolum brokerage proposal failed backend=%s err=%v", r.faceBackend, proposalErr)
			facePolicy.Brokerage = false
			facePolicy.Proposal = true
		} else {
			brokerage.IdolumNote = proposal
			brokerage.Active = brokerage.IdolumNote != ""
			brokerage.Mode = brokerageModeName(brokerage.Active, "brokerage")
			brokerage.SuggestedTurnMode = parseBrokerageMode(proposal)
			extraUsage = addTokenUsage(extraUsage, usage)
		}
	}
	if !brokerage.Active && facePolicy.Proposal {
		faceProposalAwareness := baseGovernorAwareness
		faceProposalAwareness.ArtifactMode = "scene"
		proposal, usage, proposalErr := requestFaceNote("proposal", faceProposalAwareness)
		if proposalErr != nil {
			log.Printf("WARN idolum proposal failed backend=%s err=%v", r.faceBackend, proposalErr)
		} else {
			brokerage.IdolumNote = proposal
			brokerage.Active = brokerage.IdolumNote != ""
			brokerage.Mode = brokerageModeName(brokerage.Active, "proposal")
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
	if brokerage.Active && brokerage.Mode == "brokerage" && strings.TrimSpace(brokerage.IdolumNote) != "" {
		updated, usage, ratifyErr := r.ratifyTurnBrokerage(ctx, exec, systemBlocks, history, prepared.UserText, brokerage)
		extraUsage = addTokenUsage(extraUsage, usage)
		if ratifyErr != nil {
			log.Printf("WARN turn brokerage ratification failed backend=%s err=%v; rerunning plain proposal path", exec.Backend, ratifyErr)
			brokerage.Ratification = ""
			brokerage.SignalJudgment = ""
			brokerage.RatificationRecord = ""
			brokerage.RatifiedSteps = nil
			brokerage.RatifiedTurnMode = ""
			proposal, proposalUsage, proposalErr := requestFaceNote("proposal", baseGovernorAwareness)
			if proposalErr != nil {
				log.Printf("WARN idolum proposal rerun failed backend=%s err=%v; preserving brokerage framing", r.faceBackend, proposalErr)
			} else {
				brokerage.IdolumNote = proposal
				brokerage.Active = brokerage.IdolumNote != ""
				brokerage.Mode = brokerageModeName(brokerage.Active, "proposal")
				brokerage.SuggestedTurnMode = ""
				brokerage.Ratification = ""
				brokerage.SignalJudgment = ""
				brokerage.RatificationRecord = ""
				brokerage.RatifiedSteps = nil
				extraUsage = addTokenUsage(extraUsage, proposalUsage)
			}
		} else {
			brokerage = updated
		}
		governorAwareness = r.withBrokerageAwareness(governorAwareness, brokerage)
		governorPrompt.Runtime = governorAwareness
		systemBlocks = prompt.BuildGovernorPromptBlocks(governorPrompt)
		systemPrompt = prompt.RenderSystemBlocks(systemBlocks)
		sess.SystemPrompt = systemPrompt
	}
	progress := r.newToolProgressReporter(msg)
	monitor := r.startTurnMonitor(key, session.TurnRunKindInteractive, prepared.LedgerText, progress)
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

	sess.TurnCount++
	materialFloor, floorText, _ := governorMaterialArtifact(result.Text, useMaterialFloor)
	floorMetadataState := hiddenInputs.Metadata()
	floorMetadataState.Artifacts = append(floorMetadataState.Artifacts, prepared.ArtifactRefs...)
	floorMetadata := encodeFloorMetadata(floorMetadataState)
	replyWithVoice := r.shouldReplyWithVoice(prepared.InboundWasVoice)
	fallbackOpts := face.FallbackOptions{Channel: "telegram", Voice: replyWithVoice}
	replyText := face.SerializeFloorFallback(materialFloor, floorText, fallbackOpts)
	outboundID := int64(0)
	outboundType := ""
	streamedReply := false
	faceRendered := false
	faceAwareness := r.governorRuntimeAwareness(scope, session.TurnRunKindInteractive, "telegram", exec)
	faceAwareness = r.withBrokerageAwareness(faceAwareness, brokerage)
	faceAwareness.ArtifactMode = "scene"
	faceAwareness.DeliveryMode = "text"
	faceAwareness.StreamReply = false
	if replyWithVoice {
		faceAwareness.DeliveryMode = "voice"
	} else if facePolicy.Render {
		faceAwareness.DeliveryMode = "idolum_render"
	}
	if r.faceBackend != face.BackendFloorFallback && currentFaceModel != nil {
		renderReq := face.RenderRequest{
			GovernorName:    prompt.DefaultGovernorName,
			FaceName:        face.DefaultFaceName,
			Channel:         "telegram",
			PrincipalRole:   string(actor.Role),
			WorkspaceRoot:   faceWorkspaceRoot(scope),
			FloorText:       floorText,
			MaterialFloor:   materialFloor,
			LatestUserInput: prepared.LedgerText,
			Runtime:         faceAwareness,
		}
		renderHeuristicText := floorText
		if useMaterialFloor {
			renderHeuristicText = materialFloorHeuristicText(materialFloor, floorText)
		}
		shouldRender := shouldRenderIdolumReply(facePolicy, prepared.LedgerText, renderHeuristicText, result.ToolLog, outHistory[len(input):])
		if !shouldRender && !replyWithVoice {
			faceAwareness.DeliveryMode = "floor_fallback"
			renderReq.Runtime = faceAwareness
		}

		if shouldRender && !replyWithVoice {
			if streamer, ok := currentFaceModel.(face.StreamRenderer); ok {
				editor := r.newStreamEditor(msg)
				if editor != nil {
					faceAwareness.DeliveryMode = "stream"
					faceAwareness.StreamReply = true
					renderReq.Runtime = faceAwareness
					renderedReply, streamErr := streamer.RenderStream(ctx, renderReq, func(chunk string) error {
						return editor.OnChunk(ctx, chunk)
					})
					if streamErr != nil {
						editor.Abort(ctx)
						log.Printf("WARN face stream render failed backend=%s err=%v; falling back to non-stream render", r.faceBackend, streamErr)
					} else {
						faceRendered = true
						replyText = strings.TrimSpace(renderedReply)
						if replyText == "" {
							replyText = face.SerializeFloorFallback(materialFloor, floorText, fallbackOpts)
						}
						extraUsage = addTokenUsage(extraUsage, consumeFaceUsage(currentFaceModel))
						outboundID, err = editor.Finish(ctx)
						if err != nil {
							return result, fmt.Errorf("finish streamed reply: %w", err)
						}
						if outboundID != 0 {
							outboundType = "streaming"
							streamedReply = true
						}
					}
				}
			}
		}

		if shouldRender && !faceRendered {
			if !replyWithVoice {
				faceAwareness.DeliveryMode = "idolum_render"
				faceAwareness.StreamReply = false
				renderReq.Runtime = faceAwareness
			}
			renderedReply, renderErr := currentFaceModel.Render(ctx, renderReq)
			if renderErr != nil {
				log.Printf("WARN face render failed backend=%s err=%v; using floor_fallback serializer", r.faceBackend, renderErr)
			} else {
				replyText = strings.TrimSpace(renderedReply)
				if replyText == "" {
					replyText = face.SerializeFloorFallback(materialFloor, floorText, fallbackOpts)
				}
				extraUsage = addTokenUsage(extraUsage, consumeFaceUsage(currentFaceModel))
			}
		}
	}
	result.TokenUsage = addTokenUsage(result.TokenUsage, extraUsage)

	newMessages, err := session.NewMessagesForTurn(prepared.LedgerText, outHistory[len(input):], sess.TurnCount)
	if err != nil {
		return nil, fmt.Errorf("convert new messages: %w", err)
	}
	newMessages = replaceLastAssistantWithSceneText(newMessages, replyText)
	newMessages = setLastAssistantFloor(newMessages, floorText)
	newMessages = setLastAssistantFloorMetadata(newMessages, floorMetadata)
	sess.LastFloorText = floorText
	sess.LastFloorMetadata = floorMetadata

	if err := r.store.Save(sess, newMessages, result.TokenUsage); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	if !streamedReply {
		outboundID, outboundType, err = r.sendReply(ctx, msg, replyText, prepared.InboundWasVoice)
		if err != nil {
			return result, fmt.Errorf("send outbound reply: %w", err)
		}
	}
	if err := r.store.RecordOutbound(key, sess.TurnCount, outboundID, outboundType); err != nil {
		return result, fmt.Errorf("record outbound reply: %w", err)
	}

	if shouldGenerateReviewEvent(actor, key) {
		if err := r.enqueueReviewEventsForTurn(actor, msg, sess.TurnCount, prepared.LedgerText, replyText, result.ToolLog); err != nil {
			return result, fmt.Errorf("enqueue review events: %w", err)
		}
	}

	if actor.Role == principal.RoleAdmin {
		if err := r.deliverReviewEvents(ctx, key, sess); err != nil {
			return result, fmt.Errorf("deliver review events: %w", err)
		}
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
	if trimmed == "" {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].Content = trimmed
			messages[i].ContentChars = len(trimmed)
			return messages
		}
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
			TargetAdminChatID: adminChatID,
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

	return fmt.Sprintf(
		"[Review Digest]\nsource_chat=%d source_user=%d source_role=%s turns=%s\n\n%s",
		event.SourceChatID,
		event.SourceUserID,
		event.SourceRole,
		turnRange,
		strings.TrimSpace(event.Summary),
	)
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
