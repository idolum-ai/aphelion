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

func TestStartStaleTurnWatchdogLoopRecoversScopedStaleTurn(t *testing.T) {
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
	rt.staleTurnSweep = func(cutoff time.Time, limit int) ([]session.TurnRun, error) {
		sweepCalls.Add(1)
		_ = cutoff
		_ = limit
		if interruptCalls.Load() > 0 {
			return nil, nil
		}
		return []session.TurnRun{{ID: 88, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	rt.interruptStaleTurnRuns = func(ids []int64, reason string) ([]session.TurnRun, error) {
		interruptCalls.Add(1)
		if len(ids) != 1 || ids[0] != 88 || reason == "" {
			t.Fatalf("interrupt ids=%v reason=%q, want stale run 88 with reason", ids, reason)
		}
		return []session.TurnRun{{ID: 88, ChatID: 7, Status: session.TurnRunStatusInterrupted}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(90 * time.Millisecond)

	if got := sweepCalls.Load(); got < 1 {
		t.Fatalf("sweepCalls = %d, want >= 1", got)
	}
	if got := interruptCalls.Load(); got != 1 {
		t.Fatalf("interruptCalls = %d, want 1", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{
		core.ExecutionEventWatchdogObserved,
		core.ExecutionEventWatchdogRecovered,
	}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogObserved) || !hasExecutionEvent(events, core.ExecutionEventWatchdogRecovered) {
		t.Fatalf("watchdog events = %#v, want observed and recovered", events)
	}
}

func TestStartStaleTurnWatchdogLoopRetriesWhenInterruptFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	rt.staleTurnLimit = 5
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		return []session.TurnRun{{ID: 89, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	rt.interruptStaleTurnRuns = func([]int64, string) ([]session.TurnRun, error) {
		return nil, context.Canceled
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	events, err := store.ExecutionEventsByTypes([]string{
		core.ExecutionEventWatchdogObserved,
		core.ExecutionEventWatchdogFailed,
	}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogObserved) || !hasExecutionEvent(events, core.ExecutionEventWatchdogFailed) {
		t.Fatalf("watchdog events = %#v, want observed and failed", events)
	}
	assertStaleWatchdogRetryScheduled(t, rt)
}

func TestStartStaleTurnWatchdogLoopRecoversAfterPriorWatchdogHistory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 42, UserID: 0, Scope: heartbeatScopeRef()}
	if _, err := store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType: core.ExecutionEventWatchdogFailed,
		Stage:     "watchdog",
		Status:    "failed",
		CreatedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
	}

	rt.staleTurnThreshold = 50 * time.Millisecond
	var interruptCalls atomic.Int32
	rt.staleTurnSweep = func(time.Time, int) ([]session.TurnRun, error) {
		if interruptCalls.Load() > 0 {
			return nil, nil
		}
		return []session.TurnRun{{ID: 90, ChatID: 7, Status: session.TurnRunStatusRunning}}, nil
	}
	rt.interruptStaleTurnRuns = func([]int64, string) ([]session.TurnRun, error) {
		interruptCalls.Add(1)
		return []session.TurnRun{{ID: 90, ChatID: 7, Status: session.TurnRunStatusInterrupted}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	if got := interruptCalls.Load(); got != 1 {
		t.Fatalf("interruptCalls = %d, want scoped recovery despite prior watchdog history", got)
	}
	events, err := store.ExecutionEventsByTypes([]string{core.ExecutionEventWatchdogRecovered, core.ExecutionEventWatchdogRecoverySuppressed}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes(watchdog) err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogRecovered) {
		t.Fatalf("watchdog events = %#v, want scoped recovery", events)
	}
	assertStaleWatchdogReleased(t, rt)
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
	rt.interruptStaleTurnRuns = nil

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

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
	rt.interruptStaleTurnRuns = func([]int64, string) ([]session.TurnRun, error) {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	events, err := store.ExecutionEventsByTypes([]string{core.ExecutionEventWatchdogRecoverySuppressed}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes(suppressed) err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWatchdogRecoverySuppressed) {
		t.Fatalf("watchdog suppressed events = %#v, want terminal-row suppression", events)
	}
	assertStaleWatchdogReleased(t, rt)
}

func TestStartStaleTurnWatchdogLoopRetriesWhenRecoveryEventFails(t *testing.T) {
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
	rt.interruptStaleTurnRuns = func([]int64, string) ([]session.TurnRun, error) {
		_ = store.Close()
		return []session.TurnRun{{ID: 95, ChatID: 7, Status: session.TurnRunStatusInterrupted}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.startStaleTurnWatchdogLoop(ctx, 15*time.Millisecond, func(string, ...any) {})

	time.Sleep(60 * time.Millisecond)

	assertStaleWatchdogRetryScheduled(t, rt)
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
