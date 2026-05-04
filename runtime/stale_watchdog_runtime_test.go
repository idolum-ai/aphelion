//go:build linux

package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

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
