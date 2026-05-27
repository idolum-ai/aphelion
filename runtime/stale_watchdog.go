//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	defaultStaleTurnThreshold = 3 * time.Minute
	defaultStaleTurnLimit     = 8
	defaultUnmatchedToolGrace = 30 * time.Second
)

type StaleTurnReason string

const (
	StaleTurnReasonLastActivity             StaleTurnReason = "last_activity_stale"
	StaleTurnReasonUnmatchedToolStart       StaleTurnReason = "unmatched_tool_start"
	StaleTurnReasonLastActivityAndToolStart StaleTurnReason = "last_activity_and_unmatched_tool_start"
	StaleTurnReasonUnknown                  StaleTurnReason = "unknown_stale_turn"
)

func (r *Runtime) StartStaleTurnWatchdogLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil {
		return
	}
	if logger == nil {
		logger = log.Printf
	}
	cadence := staleTurnWatchdogCadence(r.staleTurnThreshold)
	r.startStaleTurnWatchdogLoop(ctx, cadence, logger)
}

func (r *Runtime) startStaleTurnWatchdogLoop(ctx context.Context, cadence time.Duration, logger func(string, ...any)) {
	r.startBackgroundLoop("stale_turn_watchdog", func() {
		runPeriodic(ctx, cadence, func(runCtx context.Context) {
			select {
			case <-runCtx.Done():
				return
			default:
			}

			if !r.staleTurnWatchdogEnabled || r.staleTurnThreshold <= 0 || r.staleTurnSweep == nil {
				return
			}
			now := time.Now().UTC()
			if nextAttempt := r.staleWatchdogNextAttemptAt(); !nextAttempt.IsZero() {
				if now.Before(nextAttempt) {
					return
				}
				r.releaseStaleWatchdogLatch()
			}
			cutoff := now.Add(-r.staleTurnThreshold)
			stale, err := r.staleTurnSweep(cutoff, r.staleTurnLimit)
			if err != nil {
				logger("WARN stale turn watchdog sweep failed: %v", err)
				r.reportOperationalIssue(runCtx, "stale_watchdog", err)
				return
			}
			if len(stale) == 0 {
				return
			}
			if !r.staleWatchdogTriggered.CompareAndSwap(false, true) {
				return
			}

			observations := r.staleTurnObservations(stale, now)
			if err := r.recordStaleTurnWatchdogEvent(core.ExecutionEventWatchdogObserved, "observed", observations, map[string]any{
				"stale_count": len(stale),
			}, now); err != nil {
				logger("WARN stale turn watchdog failed to record observation: %v", err)
				r.reportOperationalIssue(runCtx, "stale_watchdog", err)
				r.releaseStaleWatchdogLatch()
				return
			}
			logger("WARN stale turn watchdog detected %d stale running turn(s); threshold=%s", len(stale), r.staleTurnThreshold)
			r.reportOperationalIssue(runCtx, "stale_watchdog", fmt.Errorf("detected %d stale running turn(s); threshold=%s", len(stale), r.staleTurnThreshold))
			if r.interruptStaleTurnRuns == nil {
				nextRetryAt := r.defaultStaleWatchdogRetryAt(time.Now().UTC())
				if err := r.recordStaleTurnWatchdogEvent(core.ExecutionEventWatchdogFailed, "failed", observations, map[string]any{
					"reason":          "scoped_interrupt_persister_unavailable",
					"next_attempt_at": nextRetryAt.Format(time.RFC3339Nano),
				}, time.Now().UTC()); err != nil {
					logger("WARN stale turn watchdog failed to record missing interrupt persister: %v", err)
					r.reportOperationalIssue(runCtx, "stale_watchdog", err)
				}
				r.scheduleStaleWatchdogRetry(nextRetryAt)
				return
			}
			cancelledIDs := r.cancelActiveTurnRuns(stale)
			r.waitForCancelledTurnRuns(cancelledIDs, 250*time.Millisecond)
			interrupted, interruptErr := r.interruptStaleTurnRuns(turnRunIDs(stale), "stale turn watchdog interrupted scoped turn")
			if interruptErr != nil {
				logger("WARN stale turn watchdog failed to interrupt running turns: %v", interruptErr)
				r.reportOperationalIssue(runCtx, "stale_watchdog", interruptErr)
				nextRetryAt := r.defaultStaleWatchdogRetryAt(time.Now().UTC())
				_ = r.recordStaleTurnWatchdogEvent(core.ExecutionEventWatchdogFailed, "failed", observations, map[string]any{
					"error":           trimError(interruptErr.Error()),
					"phase":           "interrupt_scoped_turn_runs",
					"next_attempt_at": nextRetryAt.Format(time.RFC3339Nano),
				}, time.Now().UTC())
				r.scheduleStaleWatchdogRetry(nextRetryAt)
				return
			}
			if len(interrupted) == 0 {
				if err := r.recordStaleTurnWatchdogEvent(core.ExecutionEventWatchdogRecoverySuppressed, "suppressed", observations, map[string]any{
					"reason": "stale_rows_already_terminal",
				}, time.Now().UTC()); err != nil {
					logger("WARN stale turn watchdog failed to record terminal-row suppression: %v", err)
					r.reportOperationalIssue(runCtx, "stale_watchdog", err)
				}
				r.releaseStaleWatchdogLatch()
				return
			}
			if err := r.recordStaleTurnWatchdogEvent(core.ExecutionEventWatchdogRecovered, "recovered", observations, map[string]any{
				"reason":              "scoped_turn_recovery",
				"interrupted_count":   len(interrupted),
				"interrupted_run_ids": turnRunIDs(interrupted),
				"cancelled_run_ids":   cancelledIDs,
			}, time.Now().UTC()); err != nil {
				logger("WARN stale turn watchdog failed to record scoped recovery: %v", err)
				r.reportOperationalIssue(runCtx, "stale_watchdog", err)
				nextRetryAt := r.defaultStaleWatchdogRetryAt(time.Now().UTC())
				_ = r.recordStaleTurnWatchdogEvent(core.ExecutionEventWatchdogFailed, "failed", observations, map[string]any{
					"error":             trimError(err.Error()),
					"phase":             "record_scoped_recovery",
					"interrupted_count": len(interrupted),
					"next_attempt_at":   nextRetryAt.Format(time.RFC3339Nano),
				}, time.Now().UTC())
				r.scheduleStaleWatchdogRetry(nextRetryAt)
				return
			}
			r.releaseStaleWatchdogLatch()
		})
	})
}

func staleTurnWatchdogCadence(threshold time.Duration) time.Duration {
	if threshold <= 0 {
		return time.Minute
	}
	cadence := threshold / 4
	if cadence < 15*time.Second {
		return 15 * time.Second
	}
	if cadence > 2*time.Minute {
		return 2 * time.Minute
	}
	return cadence
}

func (r *Runtime) unmatchedToolStaleThreshold() time.Duration {
	if r == nil {
		return defaultStaleTurnThreshold
	}
	threshold := r.staleTurnThreshold
	if threshold <= 0 {
		threshold = defaultStaleTurnThreshold
	}
	if r.cfg != nil && r.cfg.Agent.ToolTimeout > 0 {
		toolThreshold := time.Duration(r.cfg.Agent.ToolTimeout)*time.Second + defaultUnmatchedToolGrace
		if toolThreshold > threshold {
			threshold = toolThreshold
		}
	}
	return threshold
}

type StaleTurnObservation struct {
	RunID              int64
	ChatID             int64
	RunKind            session.TurnRunKind
	LastActivityAt     time.Time
	LastToolName       string
	UnmatchedToolStart bool
	Age                time.Duration
	Reason             StaleTurnReason
}

func (r *Runtime) staleTurnObservations(runs []session.TurnRun, now time.Time) []StaleTurnObservation {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := make([]StaleTurnObservation, 0, len(runs))
	for _, run := range runs {
		out = append(out, staleTurnObservation(run, r.staleTurnThreshold, now))
	}
	return out
}

func staleTurnObservation(run session.TurnRun, threshold time.Duration, now time.Time) StaleTurnObservation {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lastActivity := run.LastActivityAt
	age := time.Duration(0)
	if !lastActivity.IsZero() {
		age = now.UTC().Sub(lastActivity.UTC())
	}
	activityStale := threshold > 0 && age >= threshold
	unmatchedTool := run.ToolCallsStarted > run.ToolCallsFinished
	reason := StaleTurnReasonUnknown
	if activityStale && unmatchedTool {
		reason = StaleTurnReasonLastActivityAndToolStart
	} else if activityStale {
		reason = StaleTurnReasonLastActivity
	} else if unmatchedTool {
		reason = StaleTurnReasonUnmatchedToolStart
	}
	return StaleTurnObservation{
		RunID:              run.ID,
		ChatID:             run.ChatID,
		RunKind:            run.Kind,
		LastActivityAt:     lastActivity,
		LastToolName:       run.LastToolName,
		UnmatchedToolStart: unmatchedTool,
		Age:                age,
		Reason:             reason,
	}
}

func (r *Runtime) staleWatchdogNextAttemptAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	unixNano := r.staleWatchdogNextAttempt.Load()
	if unixNano <= 0 {
		return time.Time{}
	}
	return time.Unix(0, unixNano).UTC()
}

func (r *Runtime) releaseStaleWatchdogLatch() {
	if r == nil {
		return
	}
	r.staleWatchdogNextAttempt.Store(0)
	r.staleWatchdogTriggered.Store(false)
}

func (r *Runtime) defaultStaleWatchdogRetryAt(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	threshold := defaultStaleTurnThreshold
	if r != nil {
		threshold = r.staleTurnThreshold
	}
	delay := staleTurnWatchdogCadence(threshold)
	if delay <= 0 {
		delay = time.Minute
	}
	return now.UTC().Add(delay)
}

func (r *Runtime) scheduleStaleWatchdogRetry(nextAttemptAt time.Time) {
	if r == nil {
		return
	}
	if nextAttemptAt.IsZero() {
		nextAttemptAt = r.defaultStaleWatchdogRetryAt(time.Now().UTC())
	}
	r.staleWatchdogTriggered.Store(true)
	r.staleWatchdogNextAttempt.Store(nextAttemptAt.UTC().UnixNano())
}

func (r *Runtime) recordStaleTurnWatchdogEvent(eventType string, status string, observations []StaleTurnObservation, extra map[string]any, createdAt time.Time) error {
	if r == nil {
		return nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	payload := map[string]any{
		"stale_run_ids": staleObservationRunIDs(observations),
		"stale_count":   len(observations),
		"observations":  staleObservationPayloads(observations),
		"threshold":     r.staleTurnThreshold.String(),
		"limit":         r.staleTurnLimit,
	}
	for key, value := range extra {
		payload[key] = value
	}
	_, err := r.appendExecutionEvent(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}, eventType, "watchdog", status, payload, createdAt.UTC())
	if err != nil {
		return fmt.Errorf("record stale turn watchdog event %s: %w", eventType, err)
	}
	return nil
}

func staleObservationRunIDs(observations []StaleTurnObservation) []int64 {
	ids := make([]int64, 0, len(observations))
	for _, observation := range observations {
		if observation.RunID > 0 {
			ids = append(ids, observation.RunID)
		}
	}
	return ids
}

func staleObservationPayloads(observations []StaleTurnObservation) []map[string]any {
	payloads := make([]map[string]any, 0, len(observations))
	for _, observation := range observations {
		payload := map[string]any{
			"run_id":               observation.RunID,
			"chat_id":              observation.ChatID,
			"run_kind":             string(observation.RunKind),
			"reason":               string(observation.Reason),
			"age_ms":               observation.Age.Milliseconds(),
			"unmatched_tool_start": observation.UnmatchedToolStart,
		}
		if !observation.LastActivityAt.IsZero() {
			payload["last_activity_at"] = observation.LastActivityAt.UTC().Format(time.RFC3339Nano)
		}
		if observation.LastToolName != "" {
			payload["last_tool_name"] = observation.LastToolName
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func turnRunIDs(runs []session.TurnRun) []int64 {
	ids := make([]int64, 0, len(runs))
	for _, run := range runs {
		if run.ID > 0 {
			ids = append(ids, run.ID)
		}
	}
	return ids
}
