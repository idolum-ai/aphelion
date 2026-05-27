//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"
)

func TestWaitForStartupRecoveryReturnsImmediatelyWhenIdle(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	if err := rt.WaitForStartupRecovery(ctx); err != nil {
		t.Fatalf("WaitForStartupRecovery err = %v, want nil when no recovery in flight", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("WaitForStartupRecovery blocked %v with no recovery pending", elapsed)
	}
}

func TestWaitForStartupRecoveryDrainsCompletedGoroutine(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	rt.startupRecoveryWG.Add(1)
	go func() {
		defer rt.startupRecoveryWG.Done()
		time.Sleep(50 * time.Millisecond)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.WaitForStartupRecovery(ctx); err != nil {
		t.Fatalf("WaitForStartupRecovery err = %v, want nil after goroutine returns", err)
	}
}

func TestWaitForStartupRecoveryHonorsCtxDeadlineWhenStuck(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	rt.startupRecoveryWG.Add(1)
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
		rt.startupRecoveryWG.Wait()
	})
	go func() {
		defer rt.startupRecoveryWG.Done()
		<-release
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := rt.WaitForStartupRecovery(ctx)
	if err == nil {
		t.Fatalf("WaitForStartupRecovery err = nil, want deadline error while goroutine is stuck")
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("WaitForStartupRecovery returned in %v, expected to wait for the deadline", elapsed)
	}
}

