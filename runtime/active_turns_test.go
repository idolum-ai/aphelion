//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestCancelActiveTurnRunsKeepsEntryUntilUnregister(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.registerActiveTurn(101, cancel)

	cancelled := rt.cancelActiveTurnRuns([]session.TurnRun{{ID: 101}})
	if len(cancelled) != 1 || cancelled[0] != 101 {
		t.Fatalf("cancelled = %#v, want [101]", cancelled)
	}
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("context was not cancelled")
	}
	if !rt.hasActiveTurnRuns([]int64{101}) {
		t.Fatal("active turn entry removed at cancellation request; want it retained until unregister")
	}

	rt.unregisterActiveTurn(101)
	if rt.hasActiveTurnRuns([]int64{101}) {
		t.Fatal("active turn entry still present after unregister")
	}
}

func TestWaitForCancelledTurnRunsWaitsForUnregister(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.registerActiveTurn(202, cancel)
	_ = rt.cancelActiveTurnRuns([]session.TurnRun{{ID: 202}})

	done := make(chan struct{})
	go func() {
		rt.waitForCancelledTurnRuns([]int64{202}, 500*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("wait returned before active turn unregistered")
	case <-time.After(50 * time.Millisecond):
	}

	rt.unregisterActiveTurn(202)
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("wait did not return after unregister")
	}
	_ = ctx
}

func TestWaitForCancelledTurnRunsTimesOutWhenTurnDoesNotUnregister(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.registerActiveTurn(303, cancel)
	_ = rt.cancelActiveTurnRuns([]session.TurnRun{{ID: 303}})

	started := time.Now()
	rt.waitForCancelledTurnRuns([]int64{303}, 40*time.Millisecond)
	if elapsed := time.Since(started); elapsed < 35*time.Millisecond {
		t.Fatalf("wait returned too early after %s", elapsed)
	}
	if !rt.hasActiveTurnRuns([]int64{303}) {
		t.Fatal("active turn entry removed on timeout; want unregister to own removal")
	}
	_ = ctx
}

func TestCancelActiveTurnRunCancelsRegisteredRun(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt.registerActiveTurn(404, cancel)

	if !rt.CancelActiveTurnRun(404) {
		t.Fatal("CancelActiveTurnRun() = false, want true for active run")
	}
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("context was not cancelled")
	}
	if rt.CancelActiveTurnRun(405) {
		t.Fatal("CancelActiveTurnRun() = true for unknown run, want false")
	}
}
