//go:build linux

package runtime

import (
	"context"
	"database/sql"
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
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

const durableWakeInferenceUnavailableSignal = "Inference backend is unavailable."
const durableWakeInferenceUnavailableFallback = durableWakeInferenceUnavailableSignal + " This turn did not complete. You can /stop to cancel current work and try again."
const durableWakeAwakeLockStaleAfter = 30 * time.Minute
const durableWakePollParallelism = 3
const durableWakePollAgentTimeout = 30 * time.Minute
const durableWakeAttemptLeaseDuration = durableWakePollAgentTimeout
const durableWakeProgressFinishTimeout = 5 * time.Second

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
	WakeClaimID          string
	SessionChatType      string
	SessionUserName      string
	ParentMessageIDs     []string
	PromptContextErrHint string
	PolicyReason         string
	PersistenceErrCtx    turnCommitErrorContext
	SendErrCtx           string
	RecordErrCtx         string
	GovernorContext      durableWakeGovernorContextBuilder
	Finalize             func(turnSummary string) error
	FinalizeFailure      func(turnSummary string, cause error) error
	OutcomeIntents       func(status session.ChildTaskResultStatus, turnSummary string, cause error, now time.Time) []session.ChildTaskOutcomeIntentInput
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
	if err := r.processPendingDurableWakeOutcomeIntents(ctx, 100); err != nil {
		log.Printf("WARN durable wake pending outcome intent sweep failed err=%v", err)
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
	key := plan.Key
	if key.ChatID == 0 {
		key.ChatID = plan.Inbound.ChatID
	}
	if key.Scope.Kind == "" {
		key.Scope = durableAgentScopeRef(agent)
	}
	var progress *toolProgressReporter
	if durableWakeProgressEnabled(agent, plan) {
		progress = r.newDurableWakeProgressReporter(key, plan.Inbound, agent)
	}
	durableWakeProgressSurface(ctx, progress, "Starting child wake")
	defer func() {
		finishDurableWakeProgress(progress)
	}()
	taskPacketID := strings.TrimSpace(plan.TaskPacketID)
	if taskPacketID == "" {
		taskPacketID = durableWakeTaskPacketID(agent.AgentID, plan.Inbound.MessageID, now)
	}
	attemptID := durableWakeAttemptID(agent.AgentID, taskPacketID, now)
	leaseOwner := durableWakeAttemptOwner(agent.AgentID, attemptID)
	leaseClaimedAt := time.Now().UTC()
	if err := r.recordDurableWakeTaskPacket(key, agent, plan, taskPacketID, now.UTC()); err != nil {
		wrappedErr := fmt.Errorf("record durable wake task packet: %w", err)
		failure := core.NewDurableAgentWakeFailureError(core.DurableAgentWakeFailureTaskPacketManifestRecord, agent.AgentID, plan.ParentMessageIDs, wrappedErr)
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake manifest record failed")
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", failure, finalizeErr)
		}
		return failure
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
		failure := core.NewDurableAgentWakeFailureError(core.DurableAgentWakeFailureTaskAttemptClaim, agent.AgentID, plan.ParentMessageIDs, wrappedErr)
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake claim failed")
		if finalizeErr := finalizeDurableWakeFailure(plan, "", wrappedErr); finalizeErr != nil {
			return fmt.Errorf("%w (and failed to record wake failure: %v)", failure, finalizeErr)
		}
		return failure
	}
	durableWakeProgressSurface(ctx, progress, "Child wake claimed")
	scope, err := r.scopeForDurableAgent(agent)
	if err != nil {
		wrappedErr := fmt.Errorf("resolve durable wake scope: %w", err)
		failure := core.NewDurableAgentWakeFailureError(core.DurableAgentWakeFailureScopeSetup, agent.AgentID, plan.ParentMessageIDs, wrappedErr)
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake scope setup failed")
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "failed", map[string]any{
			"agent_id":       strings.TrimSpace(agent.AgentID),
			"task_packet_id": taskPacketID,
			"attempt_id":     attemptID,
			"error":          trimError(wrappedErr.Error()),
		}, time.Now().UTC())
		resultAt := time.Now().UTC()
		_, resultErr := r.recordDurableWakeFailureOutcome(key, agent, plan, taskPacketID, attemptID, claimedPacket, nil, "", wrappedErr, resultAt)
		if resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", failure, resultErr)
		}
		return failure
	}
	if len(agent.LocalStorageRoots) == 0 {
		agent.LocalStorageRoots = []string{scope.WorkingRoot, scope.SharedMemoryRoot}
	}
	turnCtx, stopLeaseKeeper := r.startDurableWakeAttemptLeaseKeeper(ctx, key, claimedPacket)
	defer func() {
		if stopErr := stopLeaseKeeper(); stopErr != nil {
			log.Printf("WARN durable wake child task lease keeper stopped with error packet_id=%s attempt_id=%s err=%v", taskPacketID, attemptID, stopErr)
		}
	}()
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
	durableWakeProgressSurface(ctx, progress, "Child wake started")

	unlock := r.lockSession(key)
	defer unlock()
	defer r.clearChatTurnPhase(key.ChatID)

	acquired, err := r.tryMarkDurableAgentWakeAwake(agent.AgentID, plan.Inbound.MessageID)
	if err != nil {
		wrappedErr := fmt.Errorf("mark durable wake agent awake: %w", err)
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake awake marker failed")
		resultAt := time.Now().UTC()
		_, resultErr := r.recordDurableWakeFailureOutcome(key, agent, plan, taskPacketID, attemptID, claimedPacket, nil, "", wrappedErr, resultAt)
		if resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", wrappedErr, resultErr)
		}
		return wrappedErr
	}
	if !acquired {
		releasedAt := time.Now().UTC()
		if releaseErr := r.releaseDurableWakeChildTaskAttempt(claimedPacket, releasedAt); releaseErr != nil {
			durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake release failed")
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
		durableWakeProgressDone(ctx, progress, agent, "skipped", "Child already running")
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
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake policy offer failed")
		resultAt := time.Now().UTC()
		_, resultErr := r.recordDurableWakeFailureOutcome(key, agent, plan, taskPacketID, attemptID, claimedPacket, nil, "", wrappedErr, resultAt)
		if resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", wrappedErr, resultErr)
		}
		return wrappedErr
	}

	pendingParentConversation, err := r.pendingDurableAgentParentConversation(agent.AgentID, 3)
	if len(plan.ParentMessageIDs) > 0 {
		pendingParentConversation, err = r.pendingDurableAgentParentConversationByIDs(agent.AgentID, plan.ParentMessageIDs)
	}
	if err != nil {
		wrappedErr := fmt.Errorf("load durable wake parent conversation: %w", err)
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake parent conversation load failed")
		resultAt := time.Now().UTC()
		_, resultErr := r.recordDurableWakeFailureOutcome(key, agent, plan, taskPacketID, attemptID, claimedPacket, nil, "", wrappedErr, resultAt)
		if resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", wrappedErr, resultErr)
		}
		return wrappedErr
	}

	childTurnCtx := durableWakeChildContext(turnCtx)
	childTurnCtx = toolpkg.WithExecutionAuthorityAdmission(childTurnCtx, durableWakeChildTaskAuthorityAdmission(key, agent, claimedPacket))
	durableWakeProgressSurface(ctx, progress, "Child is running")
	turnResult, turnSummary, err := r.runDurableWakeConversation(childTurnCtx, agent, scope, key, plan, pendingParentConversation, progress)
	if err != nil {
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake failed")
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "failed", map[string]any{
			"agent_id":       strings.TrimSpace(agent.AgentID),
			"task_packet_id": taskPacketID,
			"attempt_id":     attemptID,
			"error":          trimError(err.Error()),
		}, time.Now().UTC())
		resultAt := time.Now().UTC()
		_, resultErr := r.recordDurableWakeFailureOutcome(key, agent, plan, taskPacketID, attemptID, claimedPacket, pendingParentConversation, turnSummary, err, resultAt)
		if resultErr != nil {
			return fmt.Errorf("run durable wake turn: %w (and failed to record child task result: %v)", err, resultErr)
		}
		return fmt.Errorf("run durable wake turn: %w", err)
	}
	if durableTurnInferenceUnavailable(turnResult, turnSummary) {
		inferenceErr := fmt.Errorf("durable wake inference unavailable")
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake inference unavailable")
		r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "failed", map[string]any{
			"agent_id":       strings.TrimSpace(agent.AgentID),
			"task_packet_id": taskPacketID,
			"attempt_id":     attemptID,
			"error":          trimError(inferenceErr.Error()),
		}, time.Now().UTC())
		resultAt := time.Now().UTC()
		_, resultErr := r.recordDurableWakeFailureOutcome(key, agent, plan, taskPacketID, attemptID, claimedPacket, pendingParentConversation, turnSummary, inferenceErr, resultAt)
		if resultErr != nil {
			return fmt.Errorf("%w (and failed to record child task result: %v)", inferenceErr, resultErr)
		}
		return inferenceErr
	}
	outcome := r.compileDurableWakeChildOutcome(agent, turnSummary, turnResult)
	resultStatus := outcome.Status
	turnSummary = outcome.Summary
	if leaseErr := stopLeaseKeeper(); leaseErr != nil {
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake task lease lost")
		return fmt.Errorf("durable wake child task lease lost before outcome commit: %w", leaseErr)
	}
	outcomeIntents := durableWakeOutcomeIntentInputs(agent, plan, pendingParentConversation, resultStatus, turnSummary, nil, time.Now().UTC())
	result, err := r.recordDurableWakeChildTaskResultWithIntents(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, resultStatus, turnSummary, nil, time.Now().UTC(), outcomeIntents)
	if err != nil {
		durableWakeProgressDone(ctx, progress, agent, "failed", "Child wake outcome commit failed")
		return fmt.Errorf("record durable wake child task result: %w", err)
	}
	r.applyDurableWakeOutcomeIntentsOrWarn(context.Background(), agent, plan, result)
	r.applyDurableWakeNonDurableFinalizer(agent, plan, resultStatus, turnSummary, nil)
	durableWakeProgressDone(ctx, progress, agent, durableWakeProgressDoneStatus(result.Status), durableWakeProgressResultSurface(result))
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

func (r *Runtime) applyDurableWakeNonDurableFinalizer(agent core.DurableAgent, plan durableWakeTurnPlan, status session.ChildTaskResultStatus, turnSummary string, cause error) {
	if plan.OutcomeIntents != nil {
		return
	}
	var err error
	if cause != nil || status == session.ChildTaskResultFailed {
		if plan.FinalizeFailure != nil {
			err = plan.FinalizeFailure(turnSummary, firstNonNilError(cause, fmt.Errorf("durable wake failed")))
		}
	} else if plan.Finalize != nil {
		err = plan.Finalize(turnSummary)
	}
	if err != nil {
		log.Printf("WARN non-durable wake finalizer failed agent_id=%s channel=%s err=%v", agent.AgentID, firstNonEmpty(strings.TrimSpace(plan.Channel), "durable_wake"), err)
	}
}

func (r *Runtime) applyDurableWakeOutcomeIntentsOrWarn(ctx context.Context, agent core.DurableAgent, plan durableWakeTurnPlan, result session.ChildTaskResult) {
	if err := r.applyDurableWakeOutcomeIntents(ctx, agent, plan, result); err != nil {
		log.Printf("WARN durable wake post-outcome intent processing failed agent_id=%s result_id=%s status=%s err=%v", strings.TrimSpace(agent.AgentID), strings.TrimSpace(result.ResultID), result.Status, err)
	}
}

type durableWakeChildValueBoundary struct {
	context.Context
}

func (durableWakeChildValueBoundary) Value(any) any {
	return nil
}

// durableWakeChildContext is an allow-list context boundary: cancellation and
// deadlines cross into the child turn, parent context values do not. Child task
// authority is admitted explicitly after this boundary is constructed.
func durableWakeChildContext(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return durableWakeChildValueBoundary{Context: parent}
}

func finishDurableWakeProgress(progress *toolProgressReporter) {
	if progress == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), durableWakeProgressFinishTimeout)
	defer cancel()
	progress.Finish(ctx)
}

func firstNonNilError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (r *Runtime) startDurableWakeAttemptLeaseKeeper(ctx context.Context, key session.SessionKey, packet session.ChildTaskPacket) (context.Context, func() error) {
	if ctx == nil {
		ctx = context.Background()
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	if r == nil || r.store == nil || strings.TrimSpace(packet.PacketID) == "" {
		return leaseCtx, func() error {
			cancel()
			return nil
		}
	}
	interval := durableWakeAttemptLeaseDuration / 3
	if interval <= 0 {
		interval = time.Minute
	}
	if interval < time.Second {
		interval = time.Second
	}
	errCh := make(chan error, 1)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	var once sync.Once
	heartbeat := func(now time.Time) error {
		_, err := r.store.HeartbeatChildTaskAttempt(session.ChildTaskAttemptHeartbeatInput{
			PacketID:        packet.PacketID,
			AttemptID:       packet.ActiveAttemptID,
			LeaseOwner:      packet.LeaseOwner,
			LeaseGeneration: packet.LeaseGeneration,
			FencingToken:    packet.FencingToken,
			HeartbeatAt:     now,
			LeaseExpiresAt:  now.Add(durableWakeAttemptLeaseDuration),
		})
		if err != nil {
			r.recordExecutionEvent(key, core.ExecutionEventDurableWakeFailed, "durable", "lease_lost", map[string]any{
				"task_packet_id":   packet.PacketID,
				"attempt_id":       packet.ActiveAttemptID,
				"lease_generation": packet.LeaseGeneration,
				"error":            trimError(err.Error()),
			}, now)
		}
		return err
	}
	go func() {
		defer close(doneCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-leaseCtx.Done():
				return
			case now := <-ticker.C:
				if err := heartbeat(now.UTC()); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	stop := func() error {
		var err error
		once.Do(func() {
			close(stopCh)
			<-doneCh
			select {
			case err = <-errCh:
				cancel()
				return
			default:
			}
			cancel()
		})
		return err
	}
	return leaseCtx, stop
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

func durableWakeChildTaskAuthorityAdmission(key session.SessionKey, agent core.DurableAgent, packet session.ChildTaskPacket) session.ExecutionRunAuthority {
	agentID := strings.TrimSpace(agent.AgentID)
	constraints := map[string]string{
		"agent_id":          agentID,
		"packet_id":         strings.TrimSpace(packet.PacketID),
		"task_lease_id":     strings.TrimSpace(packet.TaskLeaseID),
		"attempt_id":        strings.TrimSpace(packet.ActiveAttemptID),
		"lease_owner":       strings.TrimSpace(packet.LeaseOwner),
		"lease_generation":  fmt.Sprintf("%d", packet.LeaseGeneration),
		"fencing_token":     strings.TrimSpace(packet.FencingToken),
		"target_resource":   strings.TrimSpace(packet.TargetResource),
		"required_action":   strings.TrimSpace(packet.RequiredAction),
		"authority_kind":    strings.TrimSpace(packet.AuthorityKind),
		"authority_id":      strings.TrimSpace(packet.AuthorityID),
		"grant_id":          strings.TrimSpace(packet.GrantID),
		"request_id":        strings.TrimSpace(packet.RequestID),
		"child_task_status": string(packet.Status),
	}
	return session.NormalizeExecutionRunAuthority(session.ExecutionRunAuthority{
		SessionID:           session.SessionIDForKey(key),
		ChatID:              key.ChatID,
		UserID:              key.UserID,
		Scope:               key.Scope,
		Principal:           core.DurableAgentPrincipal(agentID),
		PrincipalRole:       string(principal.RoleDurableAgent),
		ExecutionSpecies:    "durable_child_wake",
		LeaseKind:           session.ExecutionAuthorityLeaseKindChildTask,
		LeaseStatus:         string(packet.Status),
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		LeaseAllowedActions: []string{"use_child_task_grants", "invoke_granted_child_tools"},
		LeaseConstraints:    constraints,
		LeaseRemainingTurns: 1,
		LeaseExpiresAt:      packet.LeaseExpiresAt,
		AdmittedAt:          time.Now().UTC(),
	})
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
	if existing, ok, err := r.store.ChildTaskPacket(taskPacketID); err != nil {
		return err
	} else if ok {
		if strings.TrimSpace(existing.AgentID) != strings.TrimSpace(agent.AgentID) {
			return fmt.Errorf("durable wake task packet %s belongs to agent %s, not %s", taskPacketID, existing.AgentID, agent.AgentID)
		}
		return nil
	}
	inputRaw, _ := json.Marshal(map[string]any{
		"channel":         firstNonEmpty(strings.TrimSpace(plan.Channel), "durable_wake"),
		"audit_channel":   strings.TrimSpace(plan.AuditChannel),
		"inbound_id":      plan.Inbound.MessageID,
		"inbound_preview": truncatePreview(strings.TrimSpace(plan.Inbound.Text), 220),
		"wake_claim_id":   strings.TrimSpace(plan.WakeClaimID),
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
	return r.recordDurableWakeChildTaskResultWithIntents(key, agent, taskPacketID, attemptID, leaseOwner, leaseGeneration, fencingToken, status, summary, cause, now, nil)
}

func (r *Runtime) recordDurableWakeFailureOutcome(key session.SessionKey, agent core.DurableAgent, plan durableWakeTurnPlan, taskPacketID string, attemptID string, claimedPacket session.ChildTaskPacket, pending []core.DurableAgentConversationMessage, summary string, cause error, now time.Time) (session.ChildTaskResult, error) {
	intents := durableWakeOutcomeIntentInputs(agent, plan, pending, session.ChildTaskResultFailed, summary, cause, now)
	result, err := r.recordDurableWakeChildTaskResultWithIntents(key, agent, taskPacketID, attemptID, claimedPacket.LeaseOwner, claimedPacket.LeaseGeneration, claimedPacket.FencingToken, session.ChildTaskResultFailed, summary, cause, now, intents)
	if err != nil {
		return session.ChildTaskResult{}, err
	}
	r.applyDurableWakeOutcomeIntentsOrWarn(context.Background(), agent, plan, result)
	// Some legacy adapters still expose callbacks instead of typed outcome intents.
	// Run them after the durable failure outcome is committed, never before it.
	r.applyDurableWakeNonDurableFinalizer(agent, plan, session.ChildTaskResultFailed, summary, cause)
	return result, nil
}

func (r *Runtime) recordDurableWakeChildTaskResultWithIntents(key session.SessionKey, agent core.DurableAgent, taskPacketID string, attemptID string, leaseOwner string, leaseGeneration int64, fencingToken string, status session.ChildTaskResultStatus, summary string, cause error, now time.Time, outcomeIntents []session.ChildTaskOutcomeIntentInput) (session.ChildTaskResult, error) {
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
	if parsedStatus, parsedBlocker := durableWakeChildTaskStatusFromStructuredSummary(summary); parsedStatus != "" {
		if status == "" {
			status = parsedStatus
		}
		blockerKind = parsedBlocker
	} else if parsedStatus, parsedBlocker := durableWakeChildTaskStatusFromSummary(summary); status == "" {
		status = parsedStatus
		blockerKind = parsedBlocker
	} else if status == session.ChildTaskResultBlocked {
		if parsedStatus != session.ChildTaskResultCompleted {
			blockerKind = parsedBlocker
		}
		if blockerKind == "" {
			blockerKind = durableWakeChildBlockerFromSummary(summary)
		}
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
	if input.Status != session.ChildTaskResultCompleted {
		classification := durableWakeChildTaskBlockerClassification(agent, input)
		if classification.Kind != "" {
			input.BlockerKind = classification.Kind
		}
		if classification.State != "" {
			input.NextState = classification.State
		}
	}
	nextAction := durableWakeChildTaskNextActionInput(key, agent, input, taskPacketID, now)
	if intent, ok := durableWakeChildBlockerReviewIntent(agent, input, nextAction, now); ok {
		outcomeIntents = append([]session.ChildTaskOutcomeIntentInput{intent}, outcomeIntents...)
	}
	result, err := r.store.CommitChildTaskOutcome(session.ChildTaskOutcomeCommitInput{
		Result:         input,
		NextAction:     nextAction,
		OutcomeIntents: outcomeIntents,
		ResolvedAt:     now,
	})
	if err != nil {
		return session.ChildTaskResult{}, err
	}
	return result, nil
}

func durableWakeOutcomeIntentInputs(agent core.DurableAgent, plan durableWakeTurnPlan, pending []core.DurableAgentConversationMessage, status session.ChildTaskResultStatus, summary string, cause error, now time.Time) []session.ChildTaskOutcomeIntentInput {
	var intents []session.ChildTaskOutcomeIntentInput
	if plan.OutcomeIntents != nil {
		intents = append(intents, plan.OutcomeIntents(status, summary, cause, now)...)
	}
	if intent, ok := durableWakeExternalChannelStateIntent(agent, plan, status, summary, cause, now); ok {
		intents = append(intents, intent)
	}
	if cause == nil && status != session.ChildTaskResultFailed {
		payloadRaw, _ := json.Marshal(map[string]any{
			"agent_id": strings.TrimSpace(agent.AgentID),
		})
		intents = append(intents, session.ChildTaskOutcomeIntentInput{
			Kind:        session.ChildTaskOutcomeIntentPolicyApplied,
			Sequence:    20,
			PayloadJSON: string(payloadRaw),
			ResultRef:   "durable_policy_applied:" + strings.TrimSpace(agent.AgentID),
			CreatedAt:   now,
		})
	}
	if cause != nil || status == session.ChildTaskResultFailed {
		payloadRaw, _ := json.Marshal(map[string]any{
			"agent_id": strings.TrimSpace(agent.AgentID),
			"error":    errorText(cause),
		})
		intents = append(intents, session.ChildTaskOutcomeIntentInput{
			Kind:        session.ChildTaskOutcomeIntentPolicyApplyFailed,
			Sequence:    20,
			PayloadJSON: string(payloadRaw),
			ResultRef:   "durable_policy_apply_failed:" + strings.TrimSpace(agent.AgentID),
			CreatedAt:   now,
		})
	}
	if durableWakeShouldAcknowledgeParentConversation(status, cause) && len(pending) > 0 {
		payloadRaw, _ := json.Marshal(map[string]any{
			"agent_id":      strings.TrimSpace(agent.AgentID),
			"message_ids":   core.DurableAgentConversationMessageIDs(pending),
			"message_count": len(pending),
			"summary":       truncatePreview(strings.TrimSpace(summary), 500),
		})
		intents = append(intents, session.ChildTaskOutcomeIntentInput{
			Kind:        session.ChildTaskOutcomeIntentParentConversationAck,
			Sequence:    30,
			PayloadJSON: string(payloadRaw),
			ResultRef:   "durable_parent_ack:" + strings.TrimSpace(agent.AgentID),
			CreatedAt:   now,
		})
	}
	return intents
}

func durableWakeShouldAcknowledgeParentConversation(status session.ChildTaskResultStatus, cause error) bool {
	if cause != nil {
		return false
	}
	switch status {
	case session.ChildTaskResultCompleted, session.ChildTaskResultBlocked:
		return true
	default:
		return false
	}
}

type durableWakeChildOutcome struct {
	Status      session.ChildTaskResultStatus
	BlockerKind string
	Summary     string
	Typed       bool
}

func (r *Runtime) compileDurableWakeChildOutcome(agent core.DurableAgent, summary string, result *turn.Result) durableWakeChildOutcome {
	status, blockerKind, typed := durableWakeChildTaskStatusFromTurnResult(result)
	if !typed {
		status, blockerKind = durableWakeChildTaskStatusFromStructuredSummary(summary)
		typed = status != ""
	}
	if status == "" {
		status, blockerKind = durableWakeLegacyReviewStatusFromSummary(summary)
	}
	if status == "" {
		status = session.ChildTaskResultUpdate
		blockerKind = "missing_typed_child_result"
	}
	outcome := durableWakeChildOutcome{
		Status:      status,
		BlockerKind: strings.TrimSpace(blockerKind),
		Summary:     strings.TrimSpace(summary),
		Typed:       typed,
	}
	if outcome.Status == session.ChildTaskResultBlocked &&
		durableWakeSummaryClaimsToolRuntimeNotExecutable(agent, outcome.Summary) &&
		(outcome.BlockerKind == "" || outcome.BlockerKind == "child_reported_blocked") &&
		!durableWakeSummaryHasChildBlocker(outcome.Summary) &&
		!r.durableWakeChildTurnHasRuntimeProbeEvidence(agent, result) {
		outcome.BlockerKind = "tool_runtime_probe_missing"
		outcome.Summary = strings.Join([]string{
			"CHILD_BLOCKER: tool_runtime_probe_missing",
			"EVIDENCE_STATUS: child runtime executability was not proven by a child-side tool call; run a deterministic child-side probe before classifying materialization as failed.",
			outcome.Summary,
		}, "\n")
	}
	return outcome
}

func (r *Runtime) qualifyDurableWakeChildSummaryWithTurnEvidence(agent core.DurableAgent, status session.ChildTaskResultStatus, summary string, result *turn.Result) string {
	outcome := r.compileDurableWakeChildOutcome(agent, summary, result)
	return outcome.Summary
}

func durableWakeSummaryClaimsToolRuntimeNotExecutable(agent core.DurableAgent, summary string) bool {
	text := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(summary),
		strings.TrimSpace(externalChannelAdapter(agent)),
	}, "\n"))
	for _, spec := range durableWakeBlockedChildBlockerSpecs {
		if spec.Kind == "tool_runtime_not_executable" {
			return durableWakeTextMatchesAny(text, spec.Markers)
		}
	}
	return false
}

func durableWakeSummaryHasChildBlocker(summary string) bool {
	return durableWakeChildBlockerFromSummary(summary) != ""
}

func durableWakeChildBlockerFromSummary(summary string) string {
	for _, line := range strings.Split(summary, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(line), "child_blocker:") {
			return normalizeExternalChannelWakeOutcomeReason(strings.TrimSpace(line[len("child_blocker:"):]))
		}
	}
	return ""
}

func (r *Runtime) durableWakeChildTurnHasRuntimeProbeEvidence(agent core.DurableAgent, result *turn.Result) bool {
	if result == nil {
		return false
	}
	if result.Turn != nil && len(result.Turn.ToolLog) > 0 {
		for _, entry := range result.Turn.ToolLog {
			if durableWakeChildRuntimeProbeToolName(agent, entry) {
				return true
			}
		}
		return false
	}
	if r != nil && r.store != nil && result.RunID > 0 {
		if run, err := r.store.TurnRun(result.RunID); err == nil && run != nil {
			return run.ToolCallsFinished > 0 && durableWakeChildRuntimeProbeToolName(agent, run.LastToolName)
		}
	}
	return false
}

func durableWakeChildRuntimeProbeToolName(agent core.DurableAgent, raw string) bool {
	name := strings.TrimSpace(strings.ToLower(raw))
	if name == "" {
		return false
	}
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = strings.TrimSpace(name[:idx])
	}
	switch name {
	case "exec", "shell", "native_exec":
		return true
	case "read_file", "list_dir", "search", "capability_authority":
		return false
	}
	adapterName := strings.TrimSpace(strings.ToLower(externalChannelAdapter(agent)))
	return adapterName != "" && name == adapterName
}

func (r *Runtime) durableWakeChildTurnToolCallsFinished(result *turn.Result) int {
	if result == nil {
		return 0
	}
	if r != nil && r.store != nil && result.RunID > 0 {
		if run, err := r.store.TurnRun(result.RunID); err == nil && run != nil {
			return run.ToolCallsFinished
		}
	}
	if result.Turn != nil && len(result.Turn.ToolLog) > 0 {
		return len(result.Turn.ToolLog)
	}
	return 0
}

func (r *Runtime) applyDurableWakeOutcomeIntents(ctx context.Context, agent core.DurableAgent, plan durableWakeTurnPlan, result session.ChildTaskResult) error {
	if r == nil || r.store == nil || strings.TrimSpace(result.ResultID) == "" {
		return nil
	}
	intents, err := r.store.ChildTaskOutcomeIntentsForResult(result.ResultID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, intent := range intents {
		if intent.Status == session.ChildTaskOutcomeIntentApplied {
			continue
		}
		if !durableWakeOutcomeIntentDue(intent, now) {
			return nil
		}
		if err := r.applyDurableWakeOutcomeIntent(ctx, agent, plan, intent); err != nil {
			return err
		}
	}
	return nil
}

func durableWakeOutcomeIntentDue(intent session.ChildTaskOutcomeIntent, now time.Time) bool {
	switch intent.Status {
	case session.ChildTaskOutcomeIntentPending:
		return true
	case session.ChildTaskOutcomeIntentRetryable:
		return intent.NextAttemptAt.IsZero() || !intent.NextAttemptAt.After(now.UTC())
	case session.ChildTaskOutcomeIntentApplying:
		return !intent.LeaseExpiresAt.IsZero() && !intent.LeaseExpiresAt.After(now.UTC())
	default:
		return false
	}
}

func (r *Runtime) applyDurableWakeOutcomeIntent(ctx context.Context, agent core.DurableAgent, plan durableWakeTurnPlan, intent session.ChildTaskOutcomeIntent) error {
	if r == nil || r.store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	claimed, ok, err := r.store.ClaimChildTaskOutcomeIntent(session.ChildTaskOutcomeIntentClaimInput{
		IntentID:       intent.IntentID,
		LeaseOwner:     "durable_wake_intent:" + strings.TrimSpace(intent.IntentID),
		ClaimedAt:      time.Now().UTC(),
		LeaseExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil || !ok {
		return err
	}
	applyErr := r.executeDurableWakeOutcomeIntent(ctx, agent, plan, claimed)
	now := time.Now().UTC()
	if applyErr != nil {
		retryErr := r.store.RetryChildTaskOutcomeIntent(session.ChildTaskOutcomeIntentRetryInput{
			IntentID:        claimed.IntentID,
			LeaseOwner:      claimed.LeaseOwner,
			LeaseGeneration: claimed.LeaseGeneration,
			FencingToken:    claimed.FencingToken,
			LastError:       trimError(applyErr.Error()),
			AttemptedAt:     now,
			NextAttemptAt:   now.Add(durableWakeOutcomeIntentBackoff(claimed.Attempts + 1)),
			DeadLetter:      claimed.Attempts+1 >= 5,
		})
		if retryErr != nil {
			return retryErr
		}
		return applyErr
	}
	return r.store.CompleteChildTaskOutcomeIntent(session.ChildTaskOutcomeIntentCompletionInput{
		IntentID:        claimed.IntentID,
		LeaseOwner:      claimed.LeaseOwner,
		LeaseGeneration: claimed.LeaseGeneration,
		FencingToken:    claimed.FencingToken,
		CompletedAt:     now,
	})
}

func durableWakeOutcomeIntentBackoff(attempts int) time.Duration {
	if attempts <= 1 {
		return 30 * time.Second
	}
	d := time.Duration(attempts) * time.Minute
	if d > 30*time.Minute {
		return 30 * time.Minute
	}
	return d
}

func (r *Runtime) executeDurableWakeOutcomeIntent(ctx context.Context, agent core.DurableAgent, plan durableWakeTurnPlan, intent session.ChildTaskOutcomeIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch intent.Kind {
	case session.ChildTaskOutcomeIntentGenericFinalize:
		return fmt.Errorf("generic durable wake finalizer is not restart-repairable; adapter must provide typed outcome intents")
	case session.ChildTaskOutcomeIntentPolicyApplied:
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			var payload struct {
				AgentID string `json:"agent_id"`
			}
			_ = json.Unmarshal([]byte(intent.PayloadJSON), &payload)
			agentID = strings.TrimSpace(payload.AgentID)
		}
		if agentID == "" {
			return fmt.Errorf("policy-applied intent missing agent_id")
		}
		loaded, err := r.store.DurableAgent(agentID)
		if err != nil {
			return err
		}
		return r.markDurableAgentPolicyApplied(*loaded)
	case session.ChildTaskOutcomeIntentPolicyApplyFailed:
		var payload struct {
			AgentID string `json:"agent_id"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal([]byte(intent.PayloadJSON), &payload)
		agentID := firstNonEmpty(strings.TrimSpace(agent.AgentID), strings.TrimSpace(payload.AgentID))
		if agentID == "" {
			return fmt.Errorf("policy-apply-failed intent missing agent_id")
		}
		loaded, err := r.store.DurableAgent(agentID)
		if err != nil {
			return err
		}
		return r.markDurableAgentPolicyApplyFailure(*loaded, errors.New(firstNonEmpty(strings.TrimSpace(payload.Error), "durable wake failed")))
	case session.ChildTaskOutcomeIntentParentConversationAck:
		return r.applyDurableWakeParentConversationAckIntent(agent, intent)
	case session.ChildTaskOutcomeIntentScheduledReview:
		return r.applyScheduledReviewOutcomeIntent(intent)
	case session.ChildTaskOutcomeIntentChildBlockerReview:
		return r.applyDurableWakeChildBlockerReviewIntent(agent, intent)
	case session.ChildTaskOutcomeIntentExternalChannelState:
		return r.applyDurableWakeExternalChannelStateIntent(agent, intent)
	default:
		return fmt.Errorf("unsupported durable wake outcome intent kind %s", intent.Kind)
	}
}

func (r *Runtime) applyDurableWakeParentConversationAckIntent(agent core.DurableAgent, intent session.ChildTaskOutcomeIntent) error {
	var payload struct {
		AgentID      string   `json:"agent_id"`
		MessageIDs   []string `json:"message_ids"`
		Summary      string   `json:"summary"`
		MessageCount int      `json:"message_count"`
	}
	if err := json.Unmarshal([]byte(intent.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parse parent conversation ack intent payload: %w", err)
	}
	agentID := firstNonEmpty(strings.TrimSpace(agent.AgentID), strings.TrimSpace(payload.AgentID))
	if agentID == "" {
		return fmt.Errorf("parent conversation ack intent missing agent_id")
	}
	loaded, err := r.store.DurableAgent(agentID)
	if err != nil {
		return fmt.Errorf("load parent conversation ack agent: %w", err)
	}
	messages, err := r.durableAgentConversationMessagesByID(agentID, payload.MessageIDs)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}
	if err := r.acknowledgeDurableAgentParentConversation(agentID, messages, intent.CreatedAt); err != nil {
		return err
	}
	for _, messageID := range payload.MessageIDs {
		messageID = strings.TrimSpace(messageID)
		if messageID == "" || messageID == strings.TrimSpace(intent.PacketID) {
			continue
		}
		if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
			Key:         r.durableAgentExecutionKey(agentID),
			Owner:       "durable_wake",
			SubjectKind: "task_packet",
			SubjectRef:  messageID,
			Reason:      "parent_conversation_acknowledged",
			ResolvedAt:  intent.CreatedAt,
		}); err != nil {
			return err
		}
	}
	if count, countErr := r.store.DurableAgentReviewEventCountSince(agentID, intent.CreatedAt); countErr != nil || count == 0 {
		if err := r.queueDurableAgentParentConversationAck(*loaded, messages, payload.Summary, intent.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) durableAgentConversationMessagesByID(agentID string, messageIDs []string) ([]core.DurableAgentConversationMessage, error) {
	state, err := r.store.DurableAgentState(strings.TrimSpace(agentID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range messageIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 || continuity.Conversation == nil {
		return nil, nil
	}
	var out []core.DurableAgentConversationMessage
	for _, message := range continuity.Conversation.Messages {
		if want[strings.TrimSpace(message.MessageID)] {
			out = append(out, message)
		}
	}
	return out, nil
}

func (r *Runtime) processPendingDurableWakeOutcomeIntents(ctx context.Context, limit int) error {
	if r == nil || r.store == nil {
		return nil
	}
	intents, err := r.store.PendingChildTaskOutcomeIntents(limit)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		var payload struct {
			AgentID string `json:"agent_id"`
		}
		_ = json.Unmarshal([]byte(intent.PayloadJSON), &payload)
		agent, _ := r.store.DurableAgent(strings.TrimSpace(payload.AgentID))
		if agent == nil {
			if err := r.applyDurableWakeOutcomeIntent(ctx, core.DurableAgent{}, durableWakeTurnPlan{}, intent); err != nil {
				log.Printf("WARN durable wake pending intent processing failed intent_id=%s err=%v", intent.IntentID, err)
			}
			continue
		}
		if err := r.applyDurableWakeOutcomeIntent(ctx, *agent, durableWakeTurnPlan{}, intent); err != nil {
			log.Printf("WARN durable wake pending intent processing failed intent_id=%s err=%v", intent.IntentID, err)
		}
	}
	return nil
}

func durableWakeChildTaskStatusFromSummary(summary string) (session.ChildTaskResultStatus, string) {
	if status, blockerKind := durableWakeLegacyReviewStatusFromSummary(summary); status != "" {
		return status, blockerKind
	}
	return session.ChildTaskResultUpdate, "missing_terminal_review_status"
}

func durableWakeLegacyReviewStatusFromSummary(summary string) (session.ChildTaskResultStatus, string) {
	blockerKind := ""
	for _, line := range strings.Split(summary, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "child_blocker:") {
			blockerKind = normalizeExternalChannelWakeOutcomeReason(strings.TrimSpace(line[len("child_blocker:"):]))
			continue
		}
		if !strings.HasPrefix(lower, "review_status:") {
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
			if blockerKind != "" {
				return session.ChildTaskResultBlocked, blockerKind
			}
			return session.ChildTaskResultBlocked, "child_reported_" + strings.ReplaceAll(status, " ", "_")
		case "failed", "failure":
			return session.ChildTaskResultFailed, "child_reported_failed"
		}
	}
	return "", ""
}

func durableWakeChildTaskStatusFromStructuredSummary(summary string) (session.ChildTaskResultStatus, string) {
	for _, line := range strings.Split(summary, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "aphelion_child_result:") {
			continue
		}
		raw := strings.TrimSpace(line[len("APHELION_CHILD_RESULT:"):])
		var payload struct {
			Status      string `json:"status"`
			BlockerKind string `json:"blocker_kind"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return session.ChildTaskResultUpdate, "invalid_child_result_contract"
		}
		switch strings.Trim(strings.ToLower(payload.Status), " .") {
		case "completed", "complete":
			return session.ChildTaskResultCompleted, ""
		case "update":
			return session.ChildTaskResultUpdate, ""
		case "blocked", "needs_review":
			blockerKind := normalizeExternalChannelWakeOutcomeReason(payload.BlockerKind)
			if blockerKind == "" {
				blockerKind = "child_reported_blocked"
			}
			return session.ChildTaskResultBlocked, blockerKind
		case "failed", "failure":
			return session.ChildTaskResultFailed, "child_reported_failed"
		default:
			return session.ChildTaskResultUpdate, "invalid_child_result_status"
		}
	}
	return "", ""
}

func durableWakeChildTaskStatusFromTurnResult(result *turn.Result) (session.ChildTaskResultStatus, string, bool) {
	if result == nil || result.Turn == nil || result.Turn.ChildTaskResult == nil {
		return "", "", false
	}
	return durableWakeChildTaskStatusFromContract(*result.Turn.ChildTaskResult)
}

func durableWakeChildTaskStatusFromContract(contract core.ChildTaskResultContract) (session.ChildTaskResultStatus, string, bool) {
	switch strings.Trim(strings.ToLower(contract.Status), " .") {
	case "completed", "complete":
		return session.ChildTaskResultCompleted, "", true
	case "update":
		return session.ChildTaskResultUpdate, "", true
	case "blocked", "needs_review":
		blockerKind := normalizeExternalChannelWakeOutcomeReason(contract.BlockerKind)
		if blockerKind == "" {
			blockerKind = "child_reported_blocked"
		}
		return session.ChildTaskResultBlocked, blockerKind, true
	case "failed", "failure":
		blockerKind := normalizeExternalChannelWakeOutcomeReason(contract.BlockerKind)
		if blockerKind == "" {
			blockerKind = "child_reported_failed"
		}
		return session.ChildTaskResultFailed, blockerKind, true
	default:
		return session.ChildTaskResultUpdate, "invalid_child_result_contract", true
	}
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
	progress *toolProgressReporter,
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
	promptOperationState := assembled.PromptOperationState
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
		promptOperationState:      promptOperationState,
		audit:                     audit,
		allowStream:               false,
		progress:                  progress,
		progressOwnedByCaller:     progress != nil,
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
