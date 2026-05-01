//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
)

func (r *Runtime) BeginShutdown() {
	if r == nil {
		return
	}
	r.shuttingDown.Store(true)
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
