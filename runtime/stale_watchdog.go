//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

const (
	defaultStaleTurnThreshold = 3 * time.Minute
	defaultStaleTurnLimit     = 8
	defaultUnmatchedToolGrace = 30 * time.Second
)

func (r *Runtime) SetStaleTurnWatchdogHook(hook func(runs []session.TurnRun)) {
	if r == nil {
		return
	}
	r.staleTurnWatchdogHook = hook
}

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
	go runPeriodic(ctx, cadence, func(runCtx context.Context) {
		select {
		case <-runCtx.Done():
			return
		default:
		}

		if r.staleTurnThreshold <= 0 || r.staleTurnSweep == nil {
			return
		}
		cutoff := time.Now().UTC().Add(-r.staleTurnThreshold)
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

		logger("WARN stale turn watchdog detected %d stale running turn(s); threshold=%s", len(stale), r.staleTurnThreshold)
		r.reportOperationalIssue(runCtx, "stale_watchdog", fmt.Errorf("detected %d stale running turn(s); threshold=%s", len(stale), r.staleTurnThreshold))
		interrupted := stale
		if r.interruptRunningTurnRuns != nil {
			runs, interruptErr := r.interruptRunningTurnRuns()
			if interruptErr != nil {
				logger("WARN stale turn watchdog failed to interrupt running turns: %v", interruptErr)
				r.reportOperationalIssue(runCtx, "stale_watchdog", interruptErr)
			} else if len(runs) > 0 {
				interrupted = runs
			}
		}
		if r.staleTurnWatchdogHook != nil {
			r.staleTurnWatchdogHook(interrupted)
		}
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
