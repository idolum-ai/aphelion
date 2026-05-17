//go:build linux

package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestStartStaleTurnWatchdogLoopTriggersInterruptAndHookOnce(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	rt.staleTurnLimit = 5
	var sweepCalls atomic.Int32
	var interruptCalls atomic.Int32
	var hookCalls atomic.Int32
	rt.staleTurnSweep = func(cutoff time.Time, limit int) ([]session.TurnRun, error) {
		sweepCalls.Add(1)
		_ = cutoff
		_ = limit
		return []session.TurnRun{{ID: 88, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	rt.interruptRunningTurnRuns = func() ([]session.TurnRun, error) {
		interruptCalls.Add(1)
		return []session.TurnRun{{ID: 88, ChatID: 7, Status: session.TurnRunStatusInterrupted}}, nil
	}
	rt.SetStaleTurnWatchdogHook(func(runs []session.TurnRun) {
		if len(runs) == 0 || runs[0].ID != 88 {
			t.Fatalf("hook runs = %#v, want stale run 88", runs)
		}
		hookCalls.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(90 * time.Millisecond)

	if got := sweepCalls.Load(); got < 2 {
		t.Fatalf("sweepCalls = %d, want >= 2", got)
	}
	if got := interruptCalls.Load(); got != 1 {
		t.Fatalf("interruptCalls = %d, want 1", got)
	}
	if got := hookCalls.Load(); got != 1 {
		t.Fatalf("hookCalls = %d, want 1", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{
		core.ExecutionEventWatchdogObserved,
		core.ExecutionEventWatchdogRestartRequested,
	}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogObserved) || !hasExecutionEvent(events, core.ExecutionEventWatchdogRestartRequested) {
		t.Fatalf("watchdog events = %#v, want observed and restart requested", events)
	}
}

func TestStartStaleTurnWatchdogLoopDoesNotRestartWhenInterruptFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	rt.staleTurnLimit = 5
	var hookCalls atomic.Int32
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		return []session.TurnRun{{ID: 89, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	rt.interruptRunningTurnRuns = func() ([]session.TurnRun, error) {
		return nil, context.Canceled
	}
	rt.SetStaleTurnWatchdogHook(func([]session.TurnRun) {
		hookCalls.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	if got := hookCalls.Load(); got != 0 {
		t.Fatalf("hookCalls = %d, want 0 when interruption persistence fails", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{
		core.ExecutionEventWatchdogObserved,
		core.ExecutionEventWatchdogRestartRequested,
		core.ExecutionEventWatchdogFailed,
	}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogObserved) || !hasExecutionEvent(events, core.ExecutionEventWatchdogFailed) {
		t.Fatalf("watchdog events = %#v, want observed and failed", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventWatchdogRestartRequested) {
		t.Fatalf("watchdog events = %#v, did not want restart requested", events)
	}
	assertStaleWatchdogRetryScheduled(t, rt)
}

func TestStartStaleTurnWatchdogLoopSuppressesRestartDuringCooldown(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 42, UserID: 0, Scope: heartbeatScopeRef()}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType: core.ExecutionEventWatchdogRestartRequested,
		Stage:     "watchdog",
		Status:    "restart_requested",
		CreatedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	rt.staleTurnRestartCooldown = time.Hour
	rt.staleTurnMaxRestarts = 1
	var interruptCalls atomic.Int32
	var hookCalls atomic.Int32
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		return []session.TurnRun{{ID: 90, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	rt.interruptRunningTurnRuns = func() ([]session.TurnRun, error) {
		interruptCalls.Add(1)
		return []session.TurnRun{{ID: 90, ChatID: 7, Status: session.TurnRunStatusInterrupted}}, nil
	}
	rt.SetStaleTurnWatchdogHook(func([]session.TurnRun) {
		hookCalls.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	if got := interruptCalls.Load(); got != 0 {
		t.Fatalf("interruptCalls = %d, want 0 when restart is suppressed", got)
	}
	if got := hookCalls.Load(); got != 0 {
		t.Fatalf("hookCalls = %d, want 0 during cooldown", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{core.ExecutionEventWatchdogRestartSuppressed}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes(suppressed) err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogRestartSuppressed) {
		t.Fatalf("watchdog suppressed events = %#v, want suppression", events)
	}
	assertStaleWatchdogRetryScheduled(t, rt)
}

func TestStartStaleTurnWatchdogLoopRetriesWhenHookUnavailable(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		return []session.TurnRun{{ID: 92, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	var interruptCalls atomic.Int32
	rt.interruptRunningTurnRuns = func() ([]session.TurnRun, error) {
		interruptCalls.Add(1)
		return []session.TurnRun{{ID: 92, ChatID: 7, Status: session.TurnRunStatusInterrupted}}, nil
	}
	rt.SetStaleTurnWatchdogHook(nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	if got := interruptCalls.Load(); got != 0 {
		t.Fatalf("interruptCalls = %d, want 0 when restart hook is unavailable", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{core.ExecutionEventWatchdogRestartSuppressed}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes(suppressed) err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogRestartSuppressed) {
		t.Fatalf("watchdog suppressed events = %#v, want hook-unavailable suppression", events)
	}
	assertStaleWatchdogRetryScheduled(t, rt)
}

func TestStartStaleTurnWatchdogLoopRetriesWhenInterruptPersisterUnavailable(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		return []session.TurnRun{{ID: 93, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	var hookCalls atomic.Int32
	rt.interruptRunningTurnRuns = nil
	rt.SetStaleTurnWatchdogHook(func([]session.TurnRun) {
		hookCalls.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	if got := hookCalls.Load(); got != 0 {
		t.Fatalf("hookCalls = %d, want 0 when interrupt persister is unavailable", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{core.ExecutionEventWatchdogFailed}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes(failed) err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogFailed) {
		t.Fatalf("watchdog failed events = %#v, want interrupt-persister failure", events)
	}
	assertStaleWatchdogRetryScheduled(t, rt)
}

func TestStartStaleTurnWatchdogLoopReleasesWhenRowsAlreadyTerminal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	var sweepCalls atomic.Int32
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		if sweepCalls.Add(1) == 1 {
			return []session.TurnRun{{ID: 94, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
		}
		return nil, nil
	}
	var hookCalls atomic.Int32
	rt.interruptRunningTurnRuns = func() ([]session.TurnRun, error) {
		return nil, nil
	}
	rt.SetStaleTurnWatchdogHook(func([]session.TurnRun) {
		hookCalls.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	if got := hookCalls.Load(); got != 0 {
		t.Fatalf("hookCalls = %d, want 0 when stale rows are already terminal", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{core.ExecutionEventWatchdogRestartSuppressed}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes(suppressed) err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogRestartSuppressed) {
		t.Fatalf("watchdog suppressed events = %#v, want terminal-row suppression", events)
	}
	assertStaleWatchdogReleased(t, rt)
}

func TestStartStaleTurnWatchdogLoopRetriesWhenRestartRequestEventFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		return []session.TurnRun{{ID: 95, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	rt.interruptRunningTurnRuns = func() ([]session.TurnRun, error) {
		_ = store.Close()
		return []session.TurnRun{{ID: 95, ChatID: 7, Status: session.TurnRunStatusInterrupted}}, nil
	}
	var hookCalls atomic.Int32
	rt.SetStaleTurnWatchdogHook(func([]session.TurnRun) {
		hookCalls.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	if got := hookCalls.Load(); got != 0 {
		t.Fatalf("hookCalls = %d, want 0 when restart-request event cannot be written", got)
	}
	assertStaleWatchdogRetryScheduled(t, rt)
}

func TestStaleTurnWatchdogRestartDecisionHonorsConfiguredBudget(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 42, UserID: 0, Scope: heartbeatScopeRef()}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType: core.ExecutionEventWatchdogRestartRequested,
		Stage:     "watchdog",
		Status:    "restart_requested",
		CreatedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
	}

	rt.staleTurnRestartCooldown = time.Hour
	rt.staleTurnMaxRestarts = 2
	allowed, reason, attempts, _, nextAttemptAt, err := rt.staleTurnWatchdogRestartDecision(time.Now().UTC())
	if err != nil {
		t.Fatalf("staleTurnWatchdogRestartDecision() err = %v", err)
	}
	if !allowed || reason != "" || attempts != 1 || !nextAttemptAt.IsZero() {
		t.Fatalf("decision = allowed %t reason %q attempts %d next %s, want allowed with one attempt remaining", allowed, reason, attempts, nextAttemptAt)
	}
}

func TestStartStaleTurnWatchdogLoopSkipsWhenDisabled(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnWatchdogEnabled = false
	rt.staleTurnThreshold = 50 * time.Millisecond
	var sweepCalls atomic.Int32
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		sweepCalls.Add(1)
		return []session.TurnRun{{ID: 91, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(50 * time.Millisecond)
	if got := sweepCalls.Load(); got != 0 {
		t.Fatalf("sweepCalls = %d, want 0 when watchdog disabled", got)
	}
}

func TestStartStaleTurnWatchdogLoopContinuesOnSweepError(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	var sweepCalls atomic.Int32
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		sweepCalls.Add(1)
		return nil, context.DeadlineExceeded
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(70 * time.Millisecond)
	if got := sweepCalls.Load(); got < 2 {
		t.Fatalf("sweepCalls = %d, want >= 2 despite errors", got)
	}
}

func TestStaleTurnWatchdogCadence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		threshold time.Duration
		want      time.Duration
	}{
		{name: "negative defaults to minute", threshold: -time.Second, want: time.Minute},
		{name: "tiny floors at 15s", threshold: 10 * time.Second, want: 15 * time.Second},
		{name: "quarter duration", threshold: 4 * time.Minute, want: time.Minute},
		{name: "caps at 2m", threshold: 20 * time.Minute, want: 2 * time.Minute},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := staleTurnWatchdogCadence(tc.threshold)
			if got != tc.want {
				t.Fatalf("staleTurnWatchdogCadence(%s) = %s, want %s", tc.threshold, got, tc.want)
			}
		})
	}
}

func TestStaleWatchdogLatchHelpers(t *testing.T) {
	t.Parallel()

	rt := &Runtime{staleTurnThreshold: time.Minute}
	rt.scheduleStaleWatchdogRetry(time.Time{})
	assertStaleWatchdogRetryScheduled(t, rt)

	rt.releaseStaleWatchdogLatch()
	assertStaleWatchdogReleased(t, rt)
}

func assertStaleWatchdogRetryScheduled(t *testing.T, rt *Runtime) {
	t.Helper()

	if !rt.staleWatchdogTriggered.Load() {
		t.Fatal("staleWatchdogTriggered = false, want true while retry is scheduled")
	}
	if rt.staleWatchdogNextAttemptAt().IsZero() {
		t.Fatal("staleWatchdogNextAttemptAt() is zero, want scheduled retry")
	}
}

func assertStaleWatchdogReleased(t *testing.T, rt *Runtime) {
	t.Helper()

	if rt.staleWatchdogTriggered.Load() {
		t.Fatal("staleWatchdogTriggered = true, want released")
	}
	if !rt.staleWatchdogNextAttemptAt().IsZero() {
		t.Fatalf("staleWatchdogNextAttemptAt() = %s, want zero", rt.staleWatchdogNextAttemptAt())
	}
}
