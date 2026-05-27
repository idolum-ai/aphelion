//go:build linux

package runtime

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

func (r *Runtime) BeginShutdown() {
	if r == nil {
		return
	}
	if !r.shuttingDown.CompareAndSwap(false, true) {
		return
	}

	// Give any in-flight startup recovery a bounded window to finish so its
	// SQLite writes don't race the rest of the shutdown sequence. If the
	// deadline elapses we log and continue — the recovery goroutine still
	// gets ctx cancellation from its own caller's context.
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer recoveryCancel()
	if err := r.WaitForStartupRecovery(recoveryCtx); err != nil {
		log.Printf("WARN startup recovery did not drain before shutdown deadline: %v", err)
	}

	loopCtx, loopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer loopCancel()
	if err := r.WaitForBackgroundLoops(loopCtx); err != nil {
		log.Printf("WARN background loops did not drain before shutdown deadline: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := r.ParkActiveWorkForRestart(ctx, restartParkSourceShutdown); err != nil {
		if r.expectedShutdownNoise(ctx, err) {
			log.Printf("INFO suppressing expected shutdown restart parking failure err=%v", err)
			return
		}
		log.Printf("WARN restart parking failed during shutdown: %v", err)
	}
}

func (r *Runtime) isShuttingDown() bool {
	return r != nil && r.shuttingDown.Load()
}

func (r *Runtime) expectedShutdownNoise(ctx context.Context, err error) bool {
	if r == nil || err == nil || !r.isShuttingDown() {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	if ctx != nil && errors.Is(ctx.Err(), context.Canceled) {
		return true
	}
	detail := strings.ToLower(strings.TrimSpace(err.Error()))
	if detail == "" {
		return false
	}
	return strings.Contains(detail, "sql: database is closed") || strings.Contains(detail, "context canceled")
}

func isRecoveryMemoryFlushTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "context deadline exceeded")
}
