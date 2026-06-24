//go:build linux

package runtime

import (
	"context"
	"encoding/json"
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
const durableWakeAttemptLeaseDuration = durableWakePollAgentTimeout

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
	attemptID := durableWakeAttemptID(agent.AgentID, taskPacketID, now)
	leaseOwner := durableWakeAttemptOwner(agent.AgentID, attemptID)
	leaseClaimedAt := time.Now().UTC()
	if err := r.recordDurableWakeTaskPacket(key, agent, plan, taskPacketID, now.UTC()); err != nil {
		wrappedErr := fmt.Errorf("record durable wake task packet: %w", err)
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	claimedPacket, err := r.store.ClaimChildTaskAttempt(session.ChildTaskAttemptClaimInput{
		PacketID:       taskPacketID,
		AttemptID:      attemptID,
		LeaseOwner:     leaseOwner,
		AgentID:        strings.TrimSpace(agent.AgentID),
		Key:            key,
		ClaimedAt:      leaseClaimedAt,
		LeaseExpiresAt: leaseClaimedAt.Add(durableWakeAttemptLeaseDuration),
	})
	if err != nil {
		wrappedErr := fmt.Errorf("claim durable wake child task attempt: %w", err)
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	r.recordExecutionEvent(key, core.ExecutionEventDurableWakeStarted, "durable", "started", map[string]any{
		"agent_id":         strings.TrimSpace(agent.AgentID),
		"channel":          firstNonEmpty(strings.TrimSpace(plan.Channel), "durable_wake"),
		"audit_channel":    strings.TrimSpace(plan.AuditChannel),
		"task_packet_id":   taskPacketID,
		"attempt_id":       attemptID,
		"lease_owner":      claimedPacket.LeaseOwner,
		"lease_generation": claimedPacket.LeaseGeneration,
		"lease_expires_at": claimedPacket.LeaseExpiresAt.Format(time.RFC3339Nano),
	}, now.UTC())

	unlock := r.lockSession(key)
	defer unlock()
	defer r.clearChatTurnPhase(key.ChatID)

	acquired, err := r.tryMarkDurableAgentWakeAwake(agent.AgentID, plan.Inbound.MessageID)
	if err != nil {
		wrappedErr := fmt.Errorf("mark durable wake agent awake: %w", err)
		if _, resultErr := r.recordDurableWakeChildTaskResult(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, session.ChildTaskResultFailed, "", wrappedErr, time.Now().UTC()); resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", wrappedErr, resultErr)
		}
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}
	if !acquired {
		releasedAt := time.Now().UTC()
		if releaseErr := r.releaseDurableWakeChildTaskAttempt(claimedPacket, releasedAt); releaseErr != nil {
			return fmt.Errorf("durable wake agent already awake (and failed to release claimed child task lease: %w)", releaseErr)
		}
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeSkipped, "durable", "skipped", map[string]any{
			"agent_id":          strings.TrimSpace(agent.AgentID),
			"task_packet_id":    taskPacketID,
			"attempt_id":        attemptID,
			"lease_generation":  claimedPacket.LeaseGeneration,
			"lease_released_at": releasedAt.Format(time.RFC3339Nano),
			"reason":            "already_awake",
		}, time.Now().UTC())
		alreadyAwakeErr := fmt.Errorf("durable wake agent already awake")
		if finalizeErr := finalizeDurableWakeFailure(plan, "", alreadyAwakeErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", alreadyAwakeErr, finalizeErr)
		}
		return nil
	}
	defer func() {
		if dormantErr := r.markDurableAgentDormant(agent.AgentID); dormantErr != nil {
			log.Printf("WARN durable wake agent dormant state update failed agent_id=%s err=%v", agent.AgentID, dormantErr)
		}
	}()
	if err := r.ensureDurableAgentPolicyOffered(agent); err != nil {
		wrappedErr := fmt.Errorf("record durable wake offered policy: %w", err)
		if _, resultErr := r.recordDurableWakeChildTaskResult(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, session.ChildTaskResultFailed, "", wrappedErr, time.Now().UTC()); resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", wrappedErr, resultErr)
		}
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}

	pendingParentConversation, err := r.pendingDurableAgentParentConversation(agent.AgentID, 3)
	if err != nil {
		wrappedErr := fmt.Errorf("load durable wake parent conversation: %w", err)
		if _, resultErr := r.recordDurableWakeChildTaskResult(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, session.ChildTaskResultFailed, "", wrappedErr, time.Now().UTC()); resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", wrappedErr, resultErr)
		}
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", wrappedErr, finalizeErr)
		}
		return wrappedErr
	}

	turnResult, turnSummary, err := r.runDurableWakeConversation(ctx, agent, scope, key, plan, pendingParentConversation)
	if err != nil {
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "failed", map[string]any{
			"agent_id":       strings.TrimSpace(agent.AgentID),
			"task_packet_id": taskPacketID,
			"attempt_id":     attemptID,
			"error":          trimError(err.Error()),
		}, time.Now().UTC())
		if _, resultErr := r.recordDurableWakeChildTaskResult(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, session.ChildTaskResultFailed, turnSummary, err, time.Now().UTC()); resultErr != nil {
			return fmt.Errorf("run durable wake turn: %w (and failed to record child task result: %v)", err, resultErr)
		}
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
			"agent_id":       strings.TrimSpace(agent.AgentID),
			"task_packet_id": taskPacketID,
			"attempt_id":     attemptID,
			"error":          trimError(inferenceErr.Error()),
		}, time.Now().UTC())
		if _, resultErr := r.recordDurableWakeChildTaskResult(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, session.ChildTaskResultFailed, turnSummary, inferenceErr, time.Now().UTC()); resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", inferenceErr, resultErr)
		}
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
			"agent_id":       strings.TrimSpace(agent.AgentID),
			"task_packet_id": taskPacketID,
			"attempt_id":     attemptID,
			"error":          trimError(err.Error()),
		}, time.Now().UTC())
		wrappedErr := fmt.Errorf("record durable wake applied policy: %w", err)
		if _, resultErr := r.recordDurableWakeChildTaskResult(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, session.ChildTaskResultFailed, turnSummary, wrappedErr, time.Now().UTC()); resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", wrappedErr, resultErr)
		}
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
	resultStatus, _ := durableWakeChildTaskStatusFromSummary(turnSummary)
	result, err := r.recordDurableWakeChildTaskResult(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, resultStatus, turnSummary, nil, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record durable wake child task result: %w", err)
	}
	r.recordExecutionEvent(key, core.ExecutionEventDurableWakeCompleted, "durable", "completed", map[string]any{
		"agent_id":         strings.TrimSpace(agent.AgentID),
		"summary":          truncatePreview(strings.TrimSpace(turnSummary), 220),
		"task_packet_id":   taskPacketID,
		"attempt_id":       attemptID,
		"lease_generation": result.LeaseGeneration,
		"typed_result_id":  result.ResultID,
		"typed_status":     string(result.Status),
		"next_action":      "review child result or continue the bounded task",
	}, time.Now().UTC())
	return nil
}

func durableWakeTaskPacketID(agentID string, messageID int64, at time.Time) string {
	seed := strings.Join([]string{strings.TrimSpace(agentID), fmt.Sprintf("%d", messageID), at.UTC().Format(time.RFC3339Nano)}, ":")
	return "child_task:" + session.EffectAttemptCommandHash(seed)[7:23]
}

func durableWakeAttemptID(agentID string, taskPacketID string, at time.Time) string {
	seed := strings.Join([]string{strings.TrimSpace(agentID), strings.TrimSpace(taskPacketID), at.UTC().Format(time.RFC3339Nano)}, ":")
	return session.ChildTaskAttemptID(taskPacketID, seed)
}

func durableWakeAttemptOwner(agentID string, attemptID string) string {
	return strings.Join([]string{"durable_wake", strings.TrimSpace(agentID), strings.TrimSpace(attemptID)}, ":")
}

func durableWakeResultID(agentID string, taskPacketID string, attemptID string) string {
	return session.ChildTaskResultID(agentID, taskPacketID, attemptID)
}

func (r *Runtime) recordDurableWakeTaskPacket(key session.SessionKey, agent core.DurableAgent, plan durableWakeTurnPlan, taskPacketID string, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	taskPacketID = strings.TrimSpace(taskPacketID)
	if taskPacketID == "" {
		return nil
	}
	inputRaw, _ := json.Marshal(map[string]any{
		"channel":         firstNonEmpty(strings.TrimSpace(plan.Channel), "durable_wake"),
		"audit_channel":   strings.TrimSpace(plan.AuditChannel),
		"inbound_id":      plan.Inbound.MessageID,
		"inbound_preview": truncatePreview(strings.TrimSpace(plan.Inbound.Text), 220),
	})
	_, err := r.store.RecordChildTaskPacket(session.ChildTaskPacketInput{
		PacketID:  taskPacketID,
		AgentID:   strings.TrimSpace(agent.AgentID),
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: string(inputRaw),
		CreatedAt: now,
	})
	return err
}

func (r *Runtime) releaseDurableWakeChildTaskAttempt(packet session.ChildTaskPacket, releasedAt time.Time) error {
	if r == nil || r.store == nil || strings.TrimSpace(packet.PacketID) == "" {
		return nil
	}
	if releasedAt.IsZero() {
		releasedAt = time.Now().UTC()
	}
	_, err := r.store.ReleaseChildTaskAttempt(session.ChildTaskAttemptReleaseInput{
		PacketID:        packet.PacketID,
		AttemptID:       packet.ActiveAttemptID,
		LeaseOwner:      packet.LeaseOwner,
		LeaseGeneration: packet.LeaseGeneration,
		FencingToken:    packet.FencingToken,
		ReleasedAt:      releasedAt.UTC(),
	})
	return err
}

func (r *Runtime) recordDurableWakeChildTaskResult(key session.SessionKey, agent core.DurableAgent, taskPacketID string, attemptID string, leaseOwner string, leaseGeneration int64, fencingToken string, status session.ChildTaskResultStatus, summary string, cause error, now time.Time) (session.ChildTaskResult, error) {
	if r == nil || r.store == nil {
		return session.ChildTaskResult{}, nil
	}
	taskPacketID = strings.TrimSpace(taskPacketID)
	if taskPacketID == "" {
		return session.ChildTaskResult{}, nil
	}
	attemptID = strings.TrimSpace(attemptID)
	if attemptID == "" {
		attemptID = durableWakeAttemptID(agent.AgentID, taskPacketID, now)
	}
	fencingToken = strings.TrimSpace(fencingToken)
	leaseOwner = strings.TrimSpace(leaseOwner)
	if leaseOwner == "" {
		leaseOwner = durableWakeAttemptOwner(agent.AgentID, attemptID)
	}
	blockerKind := ""
	if parsedStatus, parsedBlocker := durableWakeChildTaskStatusFromSummary(summary); status == "" {
		status = parsedStatus
		blockerKind = parsedBlocker
	} else if parsedStatus != "" && parsedStatus != session.ChildTaskResultCompleted {
		status = parsedStatus
		blockerKind = parsedBlocker
	}
	if cause != nil {
		status = session.ChildTaskResultFailed
		blockerKind = "wake_failed"
	}
	if status == "" {
		status = session.ChildTaskResultCompleted
	}
	errorText := ""
	if cause != nil {
		errorText = trimError(cause.Error())
	}
	input := session.NormalizeChildTaskResultInput(session.ChildTaskResultInput{
		ResultID:        durableWakeResultID(agent.AgentID, taskPacketID, attemptID),
		PacketID:        taskPacketID,
		AttemptID:       attemptID,
		LeaseOwner:      leaseOwner,
		LeaseGeneration: leaseGeneration,
		FencingToken:    fencingToken,
		AgentID:         strings.TrimSpace(agent.AgentID),
		Key:             key,
		Status:          status,
		Summary:         truncatePreview(strings.TrimSpace(summary), 500),
		BlockerKind:     blockerKind,
		ErrorText:       errorText,
		EvidenceRefs:    []string{"task_packet:" + taskPacketID},
		CreatedAt:       now,
	})
	nextAction := durableWakeChildTaskNextActionInput(key, input, taskPacketID, now)
	result, err := r.store.RecordChildTaskResultAndAdvance(input, nextAction, now)
	if err != nil {
		return session.ChildTaskResult{}, err
	}
	return result, nil
}

func durableWakeChildTaskNextActionInput(key session.SessionKey, result session.ChildTaskResultInput, taskPacketID string, now time.Time) *session.NextActionInput {
	if result.Status == session.ChildTaskResultCompleted {
		return nil
	}
	nextAction := "repair the child task blocker before retrying"
	requiredAuthority := ""
	retryPolicy := "retry_after_blocker_resolution"
	if result.Status == session.ChildTaskResultBlocked {
		nextAction = "approve or repair the bounded authority the child task reported as blocked"
		requiredAuthority = result.BlockerKind
	}
	if result.Status == session.ChildTaskResultUpdate {
		nextAction = "continue the bounded child task from the latest reported update"
		retryPolicy = "continue_after_child_update"
	}
	return &session.NextActionInput{
		Key:                key,
		Owner:              "durable_wake",
		State:              result.NextState,
		SubjectKind:        "task_packet",
		SubjectRef:         taskPacketID,
		CausalRefs:         []string{"task_packet:" + taskPacketID, "child_task_attempt:" + result.AttemptID, "child_task_result:" + result.ResultID},
		NextAction:         nextAction,
		RequiredAuthority:  requiredAuthority,
		ResourceBlocker:    result.BlockerKind,
		RetryPolicy:        retryPolicy,
		OperatorProjection: firstNonEmpty(strings.TrimSpace(result.Summary), nextAction),
		CreatedAt:          now,
	}
}

func durableWakeChildTaskStatusFromSummary(summary string) (session.ChildTaskResultStatus, string) {
	for _, line := range strings.Split(summary, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "review_status:") {
			continue
		}
		status := strings.TrimSpace(line[len("review_status:"):])
		status = strings.Trim(strings.ToLower(status), " .")
		switch status {
		case "completed", "complete":
			return session.ChildTaskResultCompleted, ""
		case "update":
			return session.ChildTaskResultUpdate, ""
		case "blocked", "needs_review":
			return session.ChildTaskResultBlocked, "child_reported_" + strings.ReplaceAll(status, " ", "_")
		case "failed", "failure":
			return session.ChildTaskResultFailed, "child_reported_failed"
		}
	}
	return session.ChildTaskResultUpdate, "missing_terminal_review_status"
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
