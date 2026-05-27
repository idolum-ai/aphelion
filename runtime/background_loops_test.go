//go:build linux

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func TestWaitForBackgroundLoopsReturnsImmediatelyWhenIdle(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := rt.WaitForBackgroundLoops(ctx); err != nil {
		t.Fatalf("WaitForBackgroundLoops err = %v, want nil when no loops are running", err)
	}
}

func TestWaitForBackgroundLoopsHonorsCtxDeadlineWhenLoopRunning(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	loopCtx, cancelLoop := context.WithCancel(context.Background())
	defer cancelLoop()
	rt.startBackgroundLoop("test", func() {
		<-loopCtx.Done()
	})

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancelWait()
	err := rt.WaitForBackgroundLoops(waitCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForBackgroundLoops err = %v, want deadline while loop is running", err)
	}

	cancelLoop()
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := rt.WaitForBackgroundLoops(drainCtx); err != nil {
		t.Fatalf("WaitForBackgroundLoops after cancel err = %v, want nil", err)
	}
}

func TestRuntimeBackgroundLoopStartersDrainAfterContextCancel(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Every = "1m"
	cfg.Cron.Enabled = true
	cfg.Cron.Jobs = []config.CronJobConfig{{
		ID:      "background-loop-test",
		Every:   "1m",
		Prompt:  "test cron prompt",
		Enabled: true,
	}}
	cfg.Nocturne.Enabled = true
	cfg.Nocturne.CheckEvery = "1m"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	rt.StartIdleExpiryLoop(ctx, func(string, ...any) {})
	rt.StartStaleTurnWatchdogLoop(ctx, func(string, ...any) {})
	rt.StartHeartbeatLoop(ctx, func(string, ...any) {})
	rt.StartDurableWakeLoop(ctx, func(string, ...any) {})
	rt.StartCronLoop(ctx, func(string, ...any) {})
	rt.StartNocturneLoop(ctx, func(string, ...any) {})

	runningCtx, cancelRunning := context.WithTimeout(context.Background(), 25*time.Millisecond)
	err = rt.WaitForBackgroundLoops(runningCtx)
	cancelRunning()
	if !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("WaitForBackgroundLoops before cancel err = %v, want deadline while runtime loops are running", err)
	}

	cancel()
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := rt.WaitForBackgroundLoops(drainCtx); err != nil {
		t.Fatalf("WaitForBackgroundLoops after runtime ctx cancel err = %v, want nil", err)
	}
}
