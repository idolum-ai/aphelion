//go:build linux

package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

type activeTurnRun struct {
	cancel   context.CancelFunc
	done     chan struct{}
	doneOnce sync.Once
}

func (r *Runtime) registerActiveTurn(runID int64, cancel context.CancelFunc) {
	if r == nil || runID <= 0 || cancel == nil {
		return
	}
	r.activeTurnMu.Lock()
	defer r.activeTurnMu.Unlock()
	if r.activeTurnCancels == nil {
		r.activeTurnCancels = make(map[int64]*activeTurnRun)
	}
	if previous := r.activeTurnCancels[runID]; previous != nil {
		previous.doneOnce.Do(func() { close(previous.done) })
	}
	r.activeTurnCancels[runID] = &activeTurnRun{cancel: cancel, done: make(chan struct{})}
}

func (r *Runtime) unregisterActiveTurn(runID int64) {
	if r == nil || runID <= 0 {
		return
	}
	r.activeTurnMu.Lock()
	entry := r.activeTurnCancels[runID]
	delete(r.activeTurnCancels, runID)
	r.activeTurnMu.Unlock()
	if entry != nil {
		entry.doneOnce.Do(func() { close(entry.done) })
	}
}

func (r *Runtime) CancelActiveTurnRun(runID int64) bool {
	if r == nil || runID <= 0 {
		return false
	}
	r.activeTurnMu.Lock()
	entry := r.activeTurnCancels[runID]
	r.activeTurnMu.Unlock()
	if entry == nil || entry.cancel == nil {
		return false
	}
	entry.cancel()
	return true
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
		entry := r.activeTurnCancels[run.ID]
		if entry == nil || entry.cancel == nil {
			continue
		}
		cancels = append(cancels, entry.cancel)
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
	for _, done := range r.activeTurnDoneChannels(ids) {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		select {
		case <-done:
		case <-time.After(remaining):
			return
		}
	}
}

func (r *Runtime) activeTurnDoneChannels(ids []int64) []<-chan struct{} {
	if r == nil || len(ids) == 0 {
		return nil
	}
	r.activeTurnMu.Lock()
	defer r.activeTurnMu.Unlock()
	done := make([]<-chan struct{}, 0, len(ids))
	for _, id := range ids {
		if entry := r.activeTurnCancels[id]; entry != nil && entry.done != nil {
			done = append(done, entry.done)
		}
	}
	return done
}

func (r *Runtime) hasActiveTurnRuns(ids []int64) bool {
	if r == nil || len(ids) == 0 {
		return false
	}
	r.activeTurnMu.Lock()
	defer r.activeTurnMu.Unlock()
	for _, id := range ids {
		if entry := r.activeTurnCancels[id]; entry != nil {
			return true
		}
	}
	return false
}
