//go:build linux

package runtime

import (
	"context"
	"fmt"
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
	"github.com/idolum-ai/aphelion/turn"
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
	facePolicy := pipeline.DecideInteractiveFacePolicy(prepared.LedgerText)
	useMaterialFloor := pipeline.ShouldUseMaterialFloorContract(facePolicy)
	exec := r.executionForTurn(prepared)
	promptContext, err := r.promptContextForScope(scope, now)
	if err != nil {
		return nil, fmt.Errorf("load workspace prompt context: %w", err)
	}
	hiddenInputs := r.assembleInteractiveHiddenInputs(ctx, scope, now, prepared.LedgerText)
	hiddenInputs.addCoreAll(prepared.ArtifactDecisionInputs)
	baseGovernorAwareness := turn.ApplyOperationAwareness(
		turn.ApplyPlanAwareness(
			turn.ApplyHiddenInputAwareness(r.governorRuntimeAwareness(scope, session.TurnRunKindInteractive, "telegram", exec), hiddenInputs.toTurnAwareness()),
			sess.PlanState,
		),
		sess.OperationState,
	)
	if useMaterialFloor {
		baseGovernorAwareness.ArtifactMode = "floor"
	}
	sess.ChatType = "dm"
	sess.UserName = msg.SenderName
	machine := &turn.Machine{
		Governor: nil,
		Face:     nil,
		Options: turn.Options{
			GovernorName: prompt.DefaultGovernorName,
			FaceName:     face.DefaultFaceName,
			Channel:      "telegram",
			Style:        "observant, high-agency, warm, and emotionally lucid",
		},
		RuntimeAwareness: baseGovernorAwareness,
		PolicyFunc: func(turn.Request) turn.Policy {
			return turn.Policy{
				Brokerage: false,
				Proposal:  facePolicy.Proposal,
				Render:    facePolicy.Render,
				Reason:    "mapped from pipeline interactive face policy",
			}
		},
	}
	coordinator := &interactiveTurnCoordinator{
		runtime:               r,
		actor:                 actor,
		scope:                 scope,
		msg:                   msg,
		key:                   key,
		sess:                  sess,
		prepared:              prepared,
		exec:                  exec,
		facePolicy:            facePolicy,
		useMaterialFloor:      useMaterialFloor,
		governorName:          prompt.DefaultGovernorName,
		faceName:              face.DefaultFaceName,
		channelName:           "telegram",
		principalRole:         string(actor.Role),
		hiddenInputs:          hiddenInputs,
		promptContext:         promptContext,
		tools:                 tools,
		currentFaceModel:      r.currentFaceRenderer(),
		baseGovernorAwareness: baseGovernorAwareness,
		audit:                 audit,
	}
	machine.Governor = coordinator
	machine.Face = coordinator
	machine.Persistence = &turnPersistencePort{
		runtime: r,
		key:     key,
		sess:    sess,
		errCtx: turnCommitErrorContext{
			ConvertMessages: "convert new messages",
			LoadPlanState:   "load plan state before save",
			LoadOperation:   "load operation state before save",
			SaveSession:     "save session",
			RecordOutbound:  "record outbound reply",
		},
		audit: audit,
	}
	machine.Delivery = &turnDeliveryPort{
		runtime:         r,
		key:             key,
		sess:            sess,
		msg:             msg,
		inboundWasVoice: prepared.InboundWasVoice,
		deliver:         true,
		recordOutbound:  true,
		audit:           audit,
		sendErrCtx:      "send outbound reply",
		recordErrCtx:    "record outbound reply",
		hooks: turnCommitHooks{
			QueueReviewEvents: func() error {
				if !shouldGenerateReviewEvent(actor, key) {
					return nil
				}
				return r.enqueueReviewEventsForTurn(
					actor,
					msg,
					sess.TurnCount,
					prepared.LedgerText,
					strings.TrimSpace(coordinator.lastRenderedReply),
					coordinator.getTurnToolLog(),
				)
			},
			DeliverReviewEvents: func() error {
				if actor.Role != principal.RoleAdmin {
					return nil
				}
				return r.deliverReviewEvents(ctx, key, sess)
			},
		},
	}

	turnResult, err := machine.Handle(ctx, turn.Request{
		RunKind:         session.TurnRunKindInteractive,
		SessionKey:      key,
		Inbound:         msg,
		Session:         sess,
		InboundWasVoice: prepared.InboundWasVoice,
		Now:             now,
	})
	if err != nil && (turnResult == nil || !turnResult.Commit.Persisted) {
		return nil, err
	}
	if turnResult == nil || turnResult.Turn == nil {
		return nil, fmt.Errorf("interactive turn did not return a result")
	}
	if err != nil {
		return turnResult.Turn, err
	}
	return turnResult.Turn, nil
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

func toolCapabilities(registry agent.ToolRegistry) prompt.ToolCapabilities {
	if registry == nil {
		return prompt.ToolCapabilities{}
	}
	return prompt.ToolCapabilitiesFromDefs(registry.Definitions())
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
