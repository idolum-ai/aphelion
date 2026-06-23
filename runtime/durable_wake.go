//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

const durableWakeInferenceUnavailableSignal = "Inference backend is unavailable."
const durableWakeInferenceUnavailableFallback = durableWakeInferenceUnavailableSignal + " This turn did not complete. You can /stop to cancel current work and try again."
const durableWakeAwakeLockStaleAfter = 30 * time.Minute
const durableWakePollParallelism = 3
const durableWakePollAgentTimeout = 30 * time.Minute

type durableWakeGovernorContextBuilder func(
	agent core.DurableAgent,
	policy core.DurableAgentLivePolicy,
	msg core.InboundMessage,
	pendingParentConversation []core.DurableAgentConversationMessage,
) string

type durableWakeTurnPlan struct {
	Channel              string
	AuditChannel         string
	Key                  session.SessionKey
	Inbound              core.InboundMessage
	TaskPacketID         string
	SessionChatType      string
	SessionUserName      string
	PromptContextErrHint string
	PolicyReason         string
	PersistenceErrCtx    turnCommitErrorContext
	SendErrCtx           string
	RecordErrCtx         string
	GovernorContext      durableWakeGovernorContextBuilder
	Finalize             func(turnSummary string) error
	FinalizeFailure      func(turnSummary string, cause error) error
}

type durableWakeIngressAdapter interface {
	Name() string
	Supports(agent core.DurableAgent) bool
	Prepare(ctx context.Context, runtime *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error)
}

func defaultDurableWakeIngressAdapters() []durableWakeIngressAdapter {
	return []durableWakeIngressAdapter{
		newScheduledReviewDurableWakeAdapter(),
		newCodexAppServerWakeAdapter(),
		newGenericExternalChannelWakeAdapter(),
		newDurableParentConversationWakeAdapter(),
	}
}

func (r *Runtime) durableWakeAdapterForAgent(agent core.DurableAgent) durableWakeIngressAdapter {
	if r == nil {
		return nil
	}
	for _, adapter := range r.durableWakeAdapters {
		if adapter == nil {
			continue
		}
		if adapter.Supports(agent) {
			return adapter
		}
	}
	return nil
}

func (r *Runtime) pollDurableWakeAgents(ctx context.Context, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	agents, err := r.store.ListDurableAgents()
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		return nil
	}
	workerCount := durableWakePollParallelism
	if workerCount <= 0 {
		workerCount = 1
	}
	if workerCount > len(agents) {
		workerCount = len(agents)
	}
	jobs := make(chan core.DurableAgent)
	errCh := make(chan string, len(agents))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for agent := range jobs {
				agentCtx, cancel := context.WithTimeout(ctx, durableWakePollAgentTimeout)
				if err := r.pollDurableWakeAgent(agentCtx, agent, now); err != nil {
					errCh <- fmt.Sprintf("%s: %v", agent.AgentID, err)
				}
				cancel()
			}
		}()
	}
	for _, agent := range agents {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(errCh)
			return nil
		case jobs <- agent:
		}
	}
	close(jobs)
	wg.Wait()
	close(errCh)

	var errs []string
	for errText := range errCh {
		if strings.TrimSpace(errText) != "" {
			errs = append(errs, errText)
		}
	}
	sort.Strings(errs)
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (r *Runtime) pollDurableWakeAgent(ctx context.Context, agent core.DurableAgent, now time.Time) error {
	if r == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	adapter := r.durableWakeAdapterForAgent(agent)
	if adapter == nil {
		return nil
	}
	if r.shouldRunDurableWakeInChild(agent) {
		if err := r.pollDurableAgentWakeViaChild(ctx, agent, now); err != nil {
			if handled, handleErr := r.recordOrSuppressScheduledReviewWakeFailure(agent, err, now); handled {
				if handleErr != nil {
					return handleErr
				}
				return r.deliverDurableReviewEventsForAgent(ctx, agent)
			}
			return err
		}
		return r.deliverDurableReviewEventsForAgent(ctx, agent)
	}
	if err := r.runDurableAgentChildWakeLoaded(ctx, agent, now); err != nil {
		if handled, handleErr := r.recordOrSuppressScheduledReviewWakeFailure(agent, err, now); handled {
			if handleErr != nil {
				return handleErr
			}
			return r.deliverDurableReviewEventsForAgent(ctx, agent)
		}
		return err
	}
	return r.deliverDurableReviewEventsForAgent(ctx, agent)
}

func (r *Runtime) deliverDurableReviewEventsForAgent(ctx context.Context, agent core.DurableAgent) error {
	if r == nil || r.store == nil || r.outbound == nil {
		return nil
	}
	chatID := agent.ReviewTargetChatID
	if chatID <= 0 {
		return nil
	}
	key := session.SessionKey{
		ChatID: chatID,
		Scope:  telegramDMScopeRef(chatID),
	}
	unlock := r.lockSession(key)
	defer unlock()

	sess, err := r.store.Load(key)
	if err != nil {
		return fmt.Errorf("load durable review target session: %w", err)
	}
	applySessionScope(sess, key)
	if strings.TrimSpace(sess.ChatType) == "" {
		sess.ChatType = "dm"
	}
	return r.deliverReviewEvents(ctx, key, sess)
}

func (r *Runtime) runDurableAgentChildWakeLoaded(ctx context.Context, agent core.DurableAgent, now time.Time) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("durable child wake runtime is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	adapter := r.durableWakeAdapterForAgent(agent)
	if adapter == nil {
		return fmt.Errorf(
			"durable agent %q channel %q has no wake ingress adapter",
			strings.TrimSpace(agent.AgentID),
			strings.TrimSpace(agent.ChannelKind),
		)
	}
	if err := r.preflightDurableWakeAgent(agent, now.UTC()); err != nil {
		if handled, handleErr := r.recordDurableWakeChildRuntimeBlock(agent, err, now.UTC()); handled {
			return handleErr
		}
		return err
	}
	plan, err := adapter.Prepare(ctx, r, agent, now.UTC())
	if err != nil {
		return fmt.Errorf("prepare durable wake via %s: %w", strings.TrimSpace(adapter.Name()), err)
	}
	if plan == nil {
		return nil
	}
	return r.runDurableWakeTurn(ctx, agent, *plan, now.UTC())
}

func (r *Runtime) runDurableWakeTurn(ctx context.Context, agent core.DurableAgent, plan durableWakeTurnPlan, now time.Time) error {
	scope, err := r.scopeForDurableAgent(agent)
	if err != nil {
		wrappedErr := fmt.Errorf("resolve durable wake scope: %w", err)
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	if len(agent.LocalStorageRoots) == 0 {
		agent.LocalStorageRoots = []string{scope.WorkingRoot, scope.SharedMemoryRoot}
	}

	key := plan.Key
	if key.ChatID == 0 {
		key.ChatID = plan.Inbound.ChatID
	}
	if key.Scope.Kind == "" {
		key.Scope = durableAgentScopeRef(agent)
	}
	taskPacketID := strings.TrimSpace(plan.TaskPacketID)
	if taskPacketID == "" {
		taskPacketID = durableWakeTaskPacketID(agent.AgentID, plan.Inbound.MessageID, now)
	}
	r.recordExecutionEvent(key, core.ExecutionEventDurableWakeStarted, "durable", "started", map[string]any{
		"agent_id":       strings.TrimSpace(agent.AgentID),
		"channel":        firstNonEmpty(strings.TrimSpace(plan.Channel), "durable_wake"),
		"audit_channel":  strings.TrimSpace(plan.AuditChannel),
		"task_packet_id": taskPacketID,
	}, now.UTC())

	unlock := r.lockSession(key)
	defer unlock()
	defer r.clearChatTurnPhase(key.ChatID)

	acquired, err := r.tryMarkDurableAgentWakeAwake(agent.AgentID, plan.Inbound.MessageID)
	if err != nil {
		wrappedErr := fmt.Errorf("mark durable wake agent awake: %w", err)
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	if !acquired {
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeSkipped, "durable", "skipped", map[string]any{
			"agent_id": strings.TrimSpace(agent.AgentID),
			"reason":   "already_awake",
		}, time.Now().UTC())
		alreadyAwakeErr := fmt.Errorf("durable wake agent already awake")
		if finalizeErr := finalizeDurableWakeFailure(plan, "", alreadyAwakeErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", alreadyAwakeErr, finalizeErr)
		}
		return nil
	}
	if err := r.ensureDurableAgentPolicyOffered(agent); err != nil {
		wrappedErr := fmt.Errorf("record durable wake offered policy: %w", err)
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	defer func() {
		if dormantErr := r.markDurableAgentDormant(agent.AgentID); dormantErr != nil {
			log.Printf("WARN durable wake agent dormant state update failed agent_id=%s err=%v", agent.AgentID, dormantErr)
		}
	}()

	pendingParentConversation, err := r.pendingDurableAgentParentConversation(agent.AgentID, 3)
	if err != nil {
		return fmt.Errorf("load durable wake parent conversation: %w", err)
	}

	turnResult, turnSummary, err := r.runDurableWakeConversation(ctx, agent, scope, key, plan, pendingParentConversation)
	if err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "failed", map[string]any{
			"agent_id": strings.TrimSpace(agent.AgentID),
			"error":    trimError(err.Error()),
		}, time.Now().UTC())
		if markErr := r.markDurableAgentPolicyApplyFailure(agent, err); markErr != nil {
			return fmt.Errorf("run durable wake turn: %w (and failed to record apply failure: %v)", err, markErr)
		}
		wrappedErr := fmt.Errorf("run durable wake turn: %w", err)
		if finalizeErr := finalizeDurableWakeFailure(plan, turnSummary, wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	if durableTurnInferenceUnavailable(turnResult, turnSummary) {
		inferenceErr := fmt.Errorf("durable wake inference unavailable")
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "failed", map[string]any{
			"agent_id": strings.TrimSpace(agent.AgentID),
			"error":    trimError(inferenceErr.Error()),
		}, time.Now().UTC())
		if markErr := r.markDurableAgentPolicyApplyFailure(agent, inferenceErr); markErr != nil {
			return fmt.Errorf("%w (and failed to record apply failure: %v)", inferenceErr, markErr)
		}
		if finalizeErr := finalizeDurableWakeFailure(plan, turnSummary, inferenceErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", inferenceErr, finalizeErr)
		}
		return inferenceErr
	}
	if err := r.markDurableAgentPolicyApplied(agent); err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "failed", map[string]any{
			"agent_id": strings.TrimSpace(agent.AgentID),
			"error":    trimError(err.Error()),
		}, time.Now().UTC())
		wrappedErr := fmt.Errorf("record durable wake applied policy: %w", err)
		if finalizeErr := finalizeDurableWakeFailure(plan, turnSummary, wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	if plan.Finalize != nil {
		if err := plan.Finalize(turnSummary); err != nil {
			return err
		}
	}
	if len(pendingParentConversation) > 0 {
		if ackErr := r.acknowledgeDurableAgentParentConversation(agent.AgentID, pendingParentConversation, now); ackErr == nil {
			if count, countErr := r.store.DurableAgentReviewEventCountSince(agent.AgentID, now); countErr != nil || count == 0 {
				_ = r.queueDurableAgentParentConversationAck(agent, pendingParentConversation, turnSummary, now)
			}
		}
	}
	r.recordExecutionEvent(key, core.ExecutionEventDurableWakeCompleted, "durable", "completed", map[string]any{
		"agent_id":        strings.TrimSpace(agent.AgentID),
		"summary":         truncatePreview(strings.TrimSpace(turnSummary), 220),
		"task_packet_id":  taskPacketID,
		"typed_result_id": durableWakeResultID(agent.AgentID, taskPacketID),
		"next_action":     "review child result or continue the bounded task",
	}, time.Now().UTC())
	if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
		Key:         key,
		Owner:       "durable_wake",
		SubjectKind: "task_packet",
		SubjectRef:  taskPacketID,
		Reason:      "durable_wake_completed",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("resolve durable wake next action: %w", err)
	}
	return nil
}

func durableWakeTaskPacketID(agentID string, messageID int64, at time.Time) string {
	seed := strings.Join([]string{strings.TrimSpace(agentID), fmt.Sprintf("%d", messageID), at.UTC().Format(time.RFC3339Nano)}, ":")
	return "child_task:" + session.EffectAttemptCommandHash(seed)[7:23]
}

func durableWakeResultID(agentID string, taskPacketID string) string {
	seed := strings.Join([]string{strings.TrimSpace(agentID), strings.TrimSpace(taskPacketID), "result"}, ":")
	return "child_result:" + session.EffectAttemptCommandHash(seed)[7:23]
}

func durableTurnInferenceUnavailable(result *turn.Result, summary string) bool {
	if result != nil && result.Turn != nil && strings.TrimSpace(result.Turn.ProviderFailure) != "" {
		return true
	}
	return durableWakeInferenceUnavailable(summary)
}

func durableWakeInferenceUnavailable(summary string) bool {
	return strings.TrimSpace(summary) == durableWakeInferenceUnavailableFallback
}

func finalizeDurableWakeFailure(plan durableWakeTurnPlan, turnSummary string, cause error) error {
	if plan.FinalizeFailure == nil {
		return nil
	}
	return plan.FinalizeFailure(turnSummary, cause)
}

func (r *Runtime) runDurableWakeConversation(
	ctx context.Context,
	agent core.DurableAgent,
	scope sandbox.Scope,
	key session.SessionKey,
	plan durableWakeTurnPlan,
	pendingParentConversation []core.DurableAgentConversationMessage,
) (*turn.Result, string, error) {
	livePolicy := core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	channel := firstNonEmpty(strings.TrimSpace(plan.Channel), "durable_wake")
	assembled, err := r.assembleInteractiveLikeTurn(ctx, interactiveLikeAssemblyInput{
		Scope:                scope,
		Key:                  key,
		Msg:                  plan.Inbound,
		RunKind:              session.TurnRunKindInteractive,
		Channel:              channel,
		PrincipalRole:        "durable_agent",
		AuditChannel:         firstNonEmpty(strings.TrimSpace(plan.AuditChannel), channel),
		PromptContextErrHint: firstNonEmpty(strings.TrimSpace(plan.PromptContextErrHint), "load durable wake prompt context"),
		PolicyReason:         firstNonEmpty(strings.TrimSpace(plan.PolicyReason), "mapped from interactive face policy for durable wake channels"),
	})
	if err != nil {
		return nil, "", err
	}

	now := assembled.Now
	sess := assembled.Sess
	prepared := assembled.Prepared
	facePolicy := assembled.FacePolicy
	useMaterialFloor := assembled.UseMaterialFloor
	exec := assembled.Exec
	promptContext := assembled.PromptContext
	hiddenInputs := assembled.HiddenInputs
	baseGovernorAwareness := assembled.BaseGovernorAwareness
	audit := assembled.Audit
	machine := assembled.Machine
	defer r.emitTurnAudit(audit)

	sess.ChatType = firstNonEmpty(strings.TrimSpace(plan.SessionChatType), strings.TrimSpace(plan.Inbound.ChatType), channel)
	sess.ChatTitle = strings.TrimSpace(plan.Inbound.ChatTitle)
	sess.UserName = firstNonEmpty(strings.TrimSpace(plan.SessionUserName), strings.TrimSpace(plan.Inbound.SenderName), "durable_agent")
	tools := r.toolsForPrincipal(principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: strings.TrimSpace(agent.AgentID)}, key)
	coordinator := &durableGroupTurnCoordinator{
		runtime:                   r,
		registered:                agent,
		livePolicy:                livePolicy,
		scope:                     scope,
		msg:                       plan.Inbound,
		key:                       key,
		sess:                      sess,
		prepared:                  prepared,
		exec:                      exec,
		facePolicy:                facePolicy,
		useMaterialFloor:          useMaterialFloor,
		governorName:              machine.Options.GovernorName,
		faceName:                  machine.Options.FaceName,
		channelName:               channel,
		principalRole:             "durable_agent",
		hiddenInputs:              hiddenInputs,
		promptContext:             promptContext,
		tools:                     tools,
		currentFaceModel:          r.currentFaceRenderer(),
		baseGovernorAwareness:     baseGovernorAwareness,
		audit:                     audit,
		allowStream:               false,
		pendingParentConversation: pendingParentConversation,
		governorContextBuilder:    plan.GovernorContext,
	}
	machine.Governor = coordinator
	machine.Face = coordinator

	errCtx := plan.PersistenceErrCtx
	if errCtx == (turnCommitErrorContext{}) {
		errCtx = turnCommitErrorContext{
			ConvertMessages: "convert durable wake messages",
			LoadPlanState:   "load durable wake plan state before save",
			LoadOperation:   "load durable wake operation state before save",
			SaveSession:     "save durable wake session",
			RecordOutbound:  "record durable wake outbound reply",
		}
	}
	machine.Persistence = &turnPersistencePort{
		runtime:     r,
		key:         key,
		scope:       scope,
		sess:        sess,
		runIDSource: coordinator,
		errCtx:      errCtx,
		audit:       audit,
	}
	machine.Delivery = &turnDeliveryPort{
		runtime:         r,
		key:             key,
		sess:            sess,
		runIDSource:     coordinator,
		msg:             plan.Inbound,
		inboundWasVoice: prepared.InboundWasVoice,
		deliver:         false,
		recordOutbound:  false,
		audit:           audit,
		sendErrCtx:      firstNonEmpty(strings.TrimSpace(plan.SendErrCtx), "send durable wake reply"),
		recordErrCtx:    firstNonEmpty(strings.TrimSpace(plan.RecordErrCtx), "record durable wake outbound reply"),
	}
	turnResult, err := machine.Handle(ctx, turn.Request{
		RunKind:          session.TurnRunKindInteractive,
		SessionKey:       key,
		Inbound:          plan.Inbound,
		InboundWasVoice:  prepared.InboundWasVoice,
		ReplyWithVoice:   r.preparedReplyWithVoice(prepared),
		Session:          sess,
		Now:              now,
		PreparedUserText: prepared.LedgerText,
	})
	if err != nil {
		if turnResult == nil || !turnResult.Commit.Persisted {
			return nil, "", err
		}
	}
	if turnResult == nil || turnResult.Turn == nil {
		return nil, "", fmt.Errorf("durable wake turn did not return a result")
	}
	if err != nil {
		return nil, "", err
	}
	return turnResult, strings.TrimSpace(turnResult.VisibleReply), nil
}
