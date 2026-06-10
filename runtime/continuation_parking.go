//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	restartParkSourceShutdown = "shutdown"
	restartParkSourceStartup  = "startup_recovery"
)

type RestartParkResult struct {
	TurnRunsInterrupted          int
	ContinuationsParked          int
	PendingContinuationsParked   int
	ApprovedContinuationsParked  int
	AlreadyParkedContinuations   int
	SkippedContinuations         int
	ExpiredApprovedContinuations int
}

type RestartResumeResult struct {
	PendingContinuationsReoffered int
	ApprovedContinuationsResumed  int
	ApprovedContinuationsExpired  int
	ContinuationsFailed           int
}

func (r RestartResumeResult) total() int {
	return r.PendingContinuationsReoffered + r.ApprovedContinuationsResumed + r.ApprovedContinuationsExpired + r.ContinuationsFailed
}

func (r RestartResumeResult) summary() string {
	if r.total() == 0 {
		return ""
	}
	parts := make([]string, 0, 4)
	if r.PendingContinuationsReoffered > 0 {
		parts = append(parts, fmt.Sprintf("reoffered=%d", r.PendingContinuationsReoffered))
	}
	if r.ApprovedContinuationsResumed > 0 {
		parts = append(parts, fmt.Sprintf("approved_reoffered=%d", r.ApprovedContinuationsResumed))
	}
	if r.ApprovedContinuationsExpired > 0 {
		parts = append(parts, fmt.Sprintf("expired_reoffered=%d", r.ApprovedContinuationsExpired))
	}
	if r.ContinuationsFailed > 0 {
		parts = append(parts, fmt.Sprintf("failed=%d", r.ContinuationsFailed))
	}
	return "parked_continuations: " + strings.Join(parts, " ")
}

func (r *Runtime) ParkActiveWorkForRestart(ctx context.Context, source string) (RestartParkResult, error) {
	if r == nil || r.store == nil {
		return RestartParkResult{}, nil
	}
	return parkActiveWorkForRestart(ctx, r.store, source, time.Now().UTC(), r.interruptRunningTurnRuns, func(key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time) {
		r.recordExecutionEvent(key, eventType, stage, status, payload, createdAt)
	})
}

func ParkStoreActiveWorkForRestart(ctx context.Context, store *session.SQLiteStore, source string, now time.Time) (RestartParkResult, error) {
	if store == nil {
		return RestartParkResult{}, nil
	}
	return parkActiveWorkForRestart(ctx, store, source, now, store.InterruptRunningTurnRuns, func(key session.SessionKey, eventType string, stage string, status string, payload map[string]any, createdAt time.Time) {
		_ = appendRestartParkingExecutionEvent(store, key, eventType, stage, status, payload, createdAt)
	})
}

func parkActiveWorkForRestart(
	ctx context.Context,
	store *session.SQLiteStore,
	source string,
	now time.Time,
	interruptRunningTurnRuns func() ([]session.TurnRun, error),
	recordExecutionEvent func(session.SessionKey, string, string, string, map[string]any, time.Time),
) (RestartParkResult, error) {
	result := RestartParkResult{}
	if store == nil {
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	source = normalizeRestartParkSource(source)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	maintenanceKey := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}

	if interruptRunningTurnRuns != nil {
		interrupted, err := interruptRunningTurnRuns()
		if err != nil {
			return result, fmt.Errorf("interrupt running turn runs for restart: %w", err)
		}
		result.TurnRunsInterrupted = len(interrupted)
		if len(interrupted) > 0 {
			recordRestartParkingExecutionEvent(recordExecutionEvent, maintenanceKey, core.ExecutionEventRecoveryDetected, "recovery", "detected", map[string]any{
				"interrupted_count": len(interrupted),
				"restart_source":    source,
				"phase":             "shutdown_park",
			}, now)
		}
	}

	records, err := store.ContinuationStates()
	if err != nil {
		return result, fmt.Errorf("load continuation states for restart parking: %w", err)
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		prior := session.NormalizeContinuationState(record.State)
		parked, mode, ok := parkContinuationStateForRestart(prior, source, now)
		if !ok {
			result.SkippedContinuations++
			continue
		}
		if continuationStateRestartParked(prior) {
			result.AlreadyParkedContinuations++
			continue
		}
		if err := store.UpdateContinuationState(record.Key, parked); err != nil {
			return result, fmt.Errorf("park continuation chat_id=%d: %w", record.Key.ChatID, err)
		}
		payload := continuationExecutionPayload(parked)
		payload["restart_source"] = source
		payload["prior_status"] = string(prior.Status)
		payload["prior_proposal_id"] = strings.TrimSpace(prior.ActionProposal.ID)
		payload["prior_lease_id"] = strings.TrimSpace(prior.ContinuationLease.ID)
		payload["parking_mode"] = mode
		recordRestartParkingExecutionEvent(recordExecutionEvent, record.Key, core.ExecutionEventContinuationParked, "continuation", "parked", payload, now)

		result.ContinuationsParked++
		switch mode {
		case "approved":
			result.ApprovedContinuationsParked++
		case "expired_approved":
			result.PendingContinuationsParked++
			result.ExpiredApprovedContinuations++
		default:
			result.PendingContinuationsParked++
		}
	}
	return result, nil
}

func recordRestartParkingExecutionEvent(
	recordExecutionEvent func(session.SessionKey, string, string, string, map[string]any, time.Time),
	key session.SessionKey,
	eventType string,
	stage string,
	status string,
	payload map[string]any,
	createdAt time.Time,
) {
	if recordExecutionEvent == nil {
		return
	}
	recordExecutionEvent(key, eventType, stage, status, payload, createdAt)
}

func appendRestartParkingExecutionEvent(
	store *session.SQLiteStore,
	key session.SessionKey,
	eventType string,
	stage string,
	status string,
	payload map[string]any,
	createdAt time.Time,
) error {
	if store == nil {
		return nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal restart parking event payload: %w", err)
	}
	_, err = store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   strings.TrimSpace(eventType),
		Stage:       strings.TrimSpace(stage),
		Status:      strings.TrimSpace(status),
		PayloadJSON: string(raw),
		CreatedAt:   createdAt.UTC(),
	})
	return err
}

func (r *Runtime) resumeRestartParkedContinuations(ctx context.Context, now time.Time) (RestartResumeResult, error) {
	result := RestartResumeResult{}
	if r == nil || r.store == nil {
		return result, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	records, err := r.store.ContinuationStates()
	if err != nil {
		return result, fmt.Errorf("load parked continuation states: %w", err)
	}
	var joined error
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		state := session.NormalizeContinuationState(record.State)
		if !continuationStateRestartParked(state) {
			continue
		}
		switch state.Status {
		case session.ContinuationStatusPending:
			if err := r.reofferRestartParkedContinuation(ctx, record.Key, state, now); err != nil {
				result.ContinuationsFailed++
				r.recordRestartParkedResumeFailure(record.Key, state, err, now)
				joined = errors.Join(joined, err)
				continue
			}
			result.PendingContinuationsReoffered++
		case session.ContinuationStatusApproved:
			reason := "Restart/deploy parked this approved lease; confirm again after startup before execution resumes."
			if continuationLeaseExpired(state, now) || state.ApprovedBy <= 0 {
				reason = "Restart/deploy parked this approved lease, but the lease was no longer valid at startup; approve the fresh lease to resume."
			}
			refreshed := restartParkPendingContinuationState(state, reason, restartParkSourceStartup, now)
			if err := r.store.UpdateContinuationState(record.Key, refreshed); err != nil {
				result.ContinuationsFailed++
				wrapped := fmt.Errorf("refresh parked approved continuation chat_id=%d: %w", record.Key.ChatID, err)
				r.recordRestartParkedResumeFailure(record.Key, state, wrapped, now)
				joined = errors.Join(joined, wrapped)
				continue
			}
			if err := r.reofferRestartParkedContinuation(ctx, record.Key, refreshed, now); err != nil {
				result.ContinuationsFailed++
				r.recordRestartParkedResumeFailure(record.Key, refreshed, err, now)
				joined = errors.Join(joined, err)
				continue
			}
			if continuationLeaseExpired(state, now) || state.ApprovedBy <= 0 {
				result.ApprovedContinuationsExpired++
			} else {
				result.ApprovedContinuationsResumed++
			}
		default:
			result.ContinuationsFailed++
			err := fmt.Errorf("parked continuation chat_id=%d has unsupported status %q", record.Key.ChatID, state.Status)
			r.recordRestartParkedResumeFailure(record.Key, state, err, now)
			joined = errors.Join(joined, err)
		}
	}
	return result, joined
}

func (r *Runtime) reofferRestartParkedContinuation(ctx context.Context, key session.SessionKey, state session.ContinuationState, now time.Time) error {
	if key.ChatID == 0 {
		return fmt.Errorf("reoffer parked continuation: chat id is empty")
	}
	state = session.NormalizeContinuationState(state)
	if !continuationStateHasFreshPendingLease(state, now) {
		state = restartParkPendingContinuationState(state, "Restart/deploy parked this lease long enough for it to expire; approve this fresh lease to continue.", restartParkSourceStartup, now)
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return fmt.Errorf("refresh stale parked continuation: %w", err)
		}
	}
	offerPayload := continuationExecutionPayload(state)
	offerPayload["refreshed_from"] = "restart_reoffer"
	offerPayload["resume_mode"] = "reoffer_pending"
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", offerPayload, now)

	msg := continuationPromptInboundForKey(key, "restart parked continuation resume", core.InboundOriginTurnAuthorization, "")
	text := r.renderContinuationPrompt(ctx, key, msg, state)
	if reason := continuationVisibleParkedReason(state); reason != "" && !strings.Contains(text, reason) {
		text = strings.TrimSpace(text) + "\n\nRestart note:\n" + reason
	}
	if err := r.sendContinuationApprovalPrompt(ctx, key, msg, state, text); err != nil {
		return fmt.Errorf("send parked continuation prompt: %w", err)
	}
	resumed := clearRestartParkedContinuation(state, now)
	if err := r.store.UpdateContinuationState(key, resumed); err != nil {
		return fmt.Errorf("clear parked continuation marker: %w", err)
	}
	payload := continuationExecutionPayload(resumed)
	payload["delivery_sent"] = true
	payload["resume_mode"] = "reoffer_pending"
	r.recordExecutionEvent(key, core.ExecutionEventContinuationResumed, "continuation", "resumed", payload, now)
	return nil
}

func recordRestartResumeSummaryPayload(result RestartResumeResult) map[string]any {
	return map[string]any{
		"pending_reoffered":  result.PendingContinuationsReoffered,
		"approved_reoffered": result.ApprovedContinuationsResumed,
		"approved_expired":   result.ApprovedContinuationsExpired,
		"failed":             result.ContinuationsFailed,
	}
}

func (r *Runtime) recordRestartParkedResumeFailure(key session.SessionKey, state session.ContinuationState, err error, now time.Time) {
	if r == nil || err == nil {
		return
	}
	payload := continuationExecutionPayload(state)
	payload["error"] = trimError(err.Error())
	r.recordExecutionEvent(key, core.ExecutionEventContinuationResumed, "continuation", "failed", payload, now)
}

func parkContinuationStateForRestart(prior session.ContinuationState, source string, now time.Time) (session.ContinuationState, string, bool) {
	prior = session.NormalizeContinuationState(prior)
	switch prior.Status {
	case session.ContinuationStatusPending:
		return restartParkPendingContinuationState(prior, "Restart/deploy parked this pending lease; approve the fresh lease after startup to continue.", source, now), "pending", true
	case session.ContinuationStatusApproved:
		if prior.ApprovedBy <= 0 || continuationLeaseExpired(prior, now) {
			return restartParkPendingContinuationState(prior, "Restart/deploy found the approved lease stale; approve a fresh lease after startup to continue.", source, now), "expired_approved", true
		}
		return restartParkApprovedContinuationState(prior, source, now), "approved", true
	default:
		return prior, "", false
	}
}

func restartParkPendingContinuationState(prior session.ContinuationState, reason string, source string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state := refreshedContinuationState(prior, reason, "restart_reoffer", now)
	state = markRestartParkedContinuation(state, reason, source, now)
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		state.ActionProposal.WhyNow = continuationVisibleRefreshReason(trimmed, "restart_reoffer", prior)
		state.ActionProposal.UpdatedAt = now
		state.ActionProposal.PlanHash = actionProposalHash(state.ActionProposal)
		state.ContinuationLease.PlanHash = state.ActionProposal.PlanHash
		state.ContinuationLease.UpdatedAt = now
	}
	return session.NormalizeContinuationState(state)
}

func continuationVisibleParkedReason(state session.ContinuationState) string {
	reason := strings.TrimSpace(state.ParkedReason)
	if reason == "" {
		return ""
	}
	return continuationVisibleRefreshReason(reason, "restart_reoffer", state)
}

func restartParkApprovedContinuationState(prior session.ContinuationState, source string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state := session.NormalizeContinuationState(prior)
	turns := state.RemainingTurns
	if state.ContinuationLease.RemainingTurns > turns {
		turns = state.ContinuationLease.RemainingTurns
	}
	if turns <= 0 {
		turns = 1
	}
	if strings.TrimSpace(state.DecisionID) == "" {
		state.DecisionID = newContinuationDecisionID()
	}
	if !state.ActionProposal.Active() {
		state.ActionProposal = buildContinuationActionProposal(state.DecisionID, continuationConsensus{PersonaIntent: state.PersonaIntent, GovernorIntent: state.GovernorIntent}, state.Objective, state.StageSummary, now)
	}
	if strings.TrimSpace(state.ActionProposal.ID) == "" {
		state.ActionProposal.ID = "aprop-" + strings.TrimSpace(state.DecisionID)
	}
	state.Status = session.ContinuationStatusApproved
	state.RemainingTurns = turns
	state.ActionProposal.Status = session.ProposalStatusApproved
	state.ActionProposal.ExpiresAt = now.Add(continuationLeaseDefaultTTL)
	state.ActionProposal.UpdatedAt = now
	state.ActionProposal.PlanHash = actionProposalHash(state.ActionProposal)
	if strings.TrimSpace(state.ContinuationLease.ID) == "" && strings.TrimSpace(state.ContinuationLease.ProposalID) == "" {
		state.ContinuationLease = buildContinuationLease(state.ActionProposal, turns, now)
	}
	if strings.TrimSpace(state.ContinuationLease.ID) == "" && strings.TrimSpace(state.ActionProposal.ID) != "" {
		state.ContinuationLease.ID = "lease-" + strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-")
	}
	if strings.TrimSpace(state.ContinuationLease.ProposalID) == "" {
		state.ContinuationLease.ProposalID = strings.TrimSpace(state.ActionProposal.ID)
	}
	state.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	state.ContinuationLease.RemainingTurns = turns
	if state.ContinuationLease.MaxTurns < turns {
		state.ContinuationLease.MaxTurns = turns
	}
	if len(state.ContinuationLease.AllowedActions) == 0 {
		state.ContinuationLease.AllowedActions = append([]string(nil), state.ActionProposal.AllowedActions...)
	}
	if len(state.ContinuationLease.ForbiddenActions) == 0 {
		state.ContinuationLease.ForbiddenActions = append([]string(nil), state.ActionProposal.ForbiddenActions...)
	}
	if len(state.ContinuationLease.ValidationPlan) == 0 {
		state.ContinuationLease.ValidationPlan = append([]string(nil), state.ActionProposal.ValidationPlan...)
	}
	state.ContinuationLease.ApprovedBy = state.ApprovedBy
	if state.ContinuationLease.ApprovedAt.IsZero() {
		state.ContinuationLease.ApprovedAt = now
	}
	state.ContinuationLease.ExpiresAt = now.Add(continuationLeaseDefaultTTL)
	state.ContinuationLease.PlanHash = state.ActionProposal.PlanHash
	state.ContinuationLease.UpdatedAt = now
	state.UpdatedAt = now
	state = markRestartParkedContinuation(state, "Restart/deploy parked this approved lease before execution; it will be resumed after startup.", source, now)
	return session.NormalizeContinuationState(state)
}

func markRestartParkedContinuation(state session.ContinuationState, reason string, source string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state.ParkedAt = now.UTC()
	state.ParkedReason = strings.TrimSpace(reason)
	state.ParkedSource = normalizeRestartParkSource(source)
	state.UpdatedAt = now.UTC()
	return session.NormalizeContinuationState(state)
}

func clearRestartParkedContinuation(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state.ParkedAt = time.Time{}
	state.ParkedReason = ""
	state.ParkedSource = ""
	state.UpdatedAt = now.UTC()
	return session.NormalizeContinuationState(state)
}

func continuationStateRestartParked(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return !state.ParkedAt.IsZero() || strings.TrimSpace(state.ParkedReason) != "" || strings.TrimSpace(state.ParkedSource) != ""
}

func normalizeRestartParkSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return restartParkSourceShutdown
	}
	return source
}
