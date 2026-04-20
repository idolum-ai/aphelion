//go:build linux

package runtime

import (
	"context"
	"time"
)

const durableWakeLoopCadence = time.Minute

func (r *Runtime) StartDurableWakeLoop(ctx context.Context, logger func(string, ...any)) {
	if r == nil || r.store == nil {
		return
	}
	if logger == nil {
		logger = func(string, ...any) {}
	}
	go runPeriodic(ctx, durableWakeLoopCadence, func(runCtx context.Context) {
		if err := r.pollDurableWakeAgents(runCtx, time.Now().UTC()); err != nil {
			logger("WARN durable wake poll failed: %v", err)
		}
	})
}
