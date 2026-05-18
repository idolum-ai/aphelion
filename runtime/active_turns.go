//go:build linux

package runtime

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) registerActiveTurn(runID int64, cancel context.CancelFunc) {
	if r == nil || runID <= 0 || cancel == nil {
		return
	}
	r.activeTurnMu.Lock()
	defer r.activeTurnMu.Unlock()
	if r.activeTurnCancels == nil {
		r.activeTurnCancels = make(map[int64]context.CancelFunc)
	}
	r.activeTurnCancels[runID] = cancel
}

func (r *Runtime) unregisterActiveTurn(runID int64) {
	if r == nil || runID <= 0 {
		return
	}
	r.activeTurnMu.Lock()
	defer r.activeTurnMu.Unlock()
	delete(r.activeTurnCancels, runID)
}

func (r *Runtime) cancelActiveTurnRuns(runs []session.TurnRun) []int64 {
	if r == nil || len(runs) == 0 {
		return nil
	}
	cancels := make([]context.CancelFunc, 0, len(runs))
	cancelled := make([]int64, 0, len(runs))
	r.activeTurnMu.Lock()
	for _, run := range runs {
		if run.ID <= 0 {
			continue
		}
		cancel, ok := r.activeTurnCancels[run.ID]
		if !ok || cancel == nil {
			continue
		}
		delete(r.activeTurnCancels, run.ID)
		cancels = append(cancels, cancel)
		cancelled = append(cancelled, run.ID)
	}
	r.activeTurnMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return cancelled
}

func (r *Runtime) waitForCancelledTurnRuns(ids []int64, wait time.Duration) {
	if r == nil || len(ids) == 0 || wait <= 0 {
		return
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if !r.hasActiveTurnRuns(ids) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *Runtime) hasActiveTurnRuns(ids []int64) bool {
	if r == nil || len(ids) == 0 {
		return false
	}
	r.activeTurnMu.Lock()
	defer r.activeTurnMu.Unlock()
	for _, id := range ids {
		if _, ok := r.activeTurnCancels[id]; ok {
			return true
		}
	}
	return false
}
