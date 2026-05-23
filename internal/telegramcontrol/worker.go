//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type ingressWorkItem struct {
	parent     context.Context
	msg        core.InboundMessage
	generation uint64
}

type ingressIdentity struct {
	surface  string
	updateID int64
}

type ingressWorker struct {
	sessionID string
	notify    chan struct{}

	mu               sync.Mutex
	queue            []ingressWorkItem
	queuedIngress    map[ingressIdentity]struct{}
	selected         ingressWorkItem
	hasSelected      bool
	generation       uint64
	active           bool
	activeCancel     context.CancelFunc
	activeItem       ingressWorkItem
	hasActiveItem    bool
	chatIDs          map[int64]struct{}
	lastActivity     time.Time
	retirementClosed bool
}

func (w *ingressWorker) enqueue(ctx context.Context, msg core.InboundMessage) error {
	for {
		w.mu.Lock()
		if w.retirementClosed {
			w.mu.Unlock()
			return errIngressWorkerRetired
		}
		if len(w.queue) < ingressSequencerBuffer {
			if identity, ok := ingressIdentityForMessage(msg); ok && w.hasIngressLocked(identity) {
				w.mu.Unlock()
				return nil
			}
			if msg.ChatID != 0 {
				w.chatIDs[msg.ChatID] = struct{}{}
			}
			item := ingressWorkItem{
				parent:     ctx,
				msg:        msg,
				generation: w.generation,
			}
			w.queue = append(w.queue, item)
			if identity, ok := item.ingressIdentity(); ok {
				w.queuedIngress[identity] = struct{}{}
			}
			w.lastActivity = time.Now().UTC()
			w.signalLocked()
			w.mu.Unlock()
			return nil
		}
		w.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return fmt.Errorf("ingress queue for %s is full", strings.TrimSpace(w.sessionID))
		}
	}
}

func (w *ingressWorker) next(idleTTL time.Duration) (ingressWorkItem, bool) {
	if idleTTL <= 0 {
		idleTTL = ingressSequencerIdleTTL
	}
	for {
		w.mu.Lock()
		if w.retirementClosed {
			w.mu.Unlock()
			return ingressWorkItem{}, false
		}
		if len(w.queue) > 0 {
			item := w.queue[0]
			copy(w.queue, w.queue[1:])
			w.queue[len(w.queue)-1] = ingressWorkItem{}
			w.queue = w.queue[:len(w.queue)-1]
			if identity, ok := item.ingressIdentity(); ok {
				delete(w.queuedIngress, identity)
			}
			w.selected = item
			w.hasSelected = true
			w.lastActivity = time.Now().UTC()
			w.mu.Unlock()
			return item, true
		}
		w.mu.Unlock()

		timer := time.NewTimer(idleTTL)
		select {
		case <-w.notify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			return ingressWorkItem{}, false
		}
	}
}

func (w *ingressWorker) activate(item ingressWorkItem, cancel context.CancelFunc) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.retirementClosed || item.generation != w.generation {
		w.clearSelectedLocked(item)
		return false
	}
	w.active = true
	w.activeCancel = cancel
	w.activeItem = item
	w.hasActiveItem = true
	w.clearSelectedLocked(item)
	w.lastActivity = time.Now().UTC()
	return true
}

func (w *ingressWorker) deactivate() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active = false
	w.activeCancel = nil
	w.activeItem = ingressWorkItem{}
	w.hasActiveItem = false
	w.lastActivity = time.Now().UTC()
}

func (w *ingressWorker) stop() (bool, int, []core.InboundMessage) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.generation++
	dropped := len(w.queue)
	droppedMessages := make([]core.InboundMessage, 0, dropped+2)
	if w.hasSelected {
		dropped++
		droppedMessages = append(droppedMessages, w.selected.msg)
	}
	if w.hasActiveItem {
		droppedMessages = append(droppedMessages, w.activeItem.msg)
	}
	for i := range w.queue {
		droppedMessages = append(droppedMessages, w.queue[i].msg)
		w.queue[i] = ingressWorkItem{}
	}
	w.queue = nil
	w.queuedIngress = make(map[ingressIdentity]struct{})
	active := w.active
	if w.activeCancel != nil {
		w.activeCancel()
	}
	w.lastActivity = time.Now().UTC()
	w.signalLocked()
	return active, dropped, droppedMessages
}

func (w *ingressWorker) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.retirementClosed {
		return
	}
	w.retirementClosed = true
	w.generation++
	for i := range w.queue {
		w.queue[i] = ingressWorkItem{}
	}
	w.queue = nil
	w.queuedIngress = make(map[ingressIdentity]struct{})
	w.selected = ingressWorkItem{}
	w.hasSelected = false
	w.activeItem = ingressWorkItem{}
	w.hasActiveItem = false
	if w.activeCancel != nil {
		w.activeCancel()
	}
	w.signalLocked()
}

func (w *ingressWorker) retired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.retirementClosed
}

func (w *ingressWorker) belongsToChat(chatID int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if chatID == 0 {
		return false
	}
	_, ok := w.chatIDs[chatID]
	return ok
}

func (w *ingressWorker) status() (bool, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active, len(w.queue)
}

func (w *ingressWorker) queueDepthByChat() map[int64]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 {
		return nil
	}
	out := make(map[int64]int, len(w.chatIDs))
	for _, item := range w.queue {
		if item.generation != w.generation || item.msg.ChatID == 0 {
			continue
		}
		out[item.msg.ChatID]++
	}
	return out
}

func (w *ingressWorker) shouldRetire(idleTTL time.Duration) bool {
	if idleTTL <= 0 {
		idleTTL = ingressSequencerIdleTTL
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.retirementClosed || w.active || len(w.queue) > 0 {
		return false
	}
	return time.Since(w.lastActivity) >= idleTTL
}

func (w *ingressWorker) signalLocked() {
	select {
	case w.notify <- struct{}{}:
	default:
	}
}

func (w *ingressWorker) hasIngressLocked(identity ingressIdentity) bool {
	if _, ok := w.queuedIngress[identity]; ok {
		return true
	}
	if w.hasSelected {
		if selected, ok := w.selected.ingressIdentity(); ok && selected == identity {
			return true
		}
	}
	if w.hasActiveItem {
		if active, ok := w.activeItem.ingressIdentity(); ok && active == identity {
			return true
		}
	}
	return false
}

func (w *ingressWorker) clearSelectedLocked(item ingressWorkItem) {
	if !w.hasSelected {
		return
	}
	if w.selected.generation != item.generation {
		return
	}
	if selected, ok := w.selected.ingressIdentity(); ok {
		if itemIdentity, itemOK := item.ingressIdentity(); !itemOK || selected != itemIdentity {
			return
		}
	}
	w.selected = ingressWorkItem{}
	w.hasSelected = false
}

func (item ingressWorkItem) ingressIdentity() (ingressIdentity, bool) {
	return ingressIdentityForMessage(item.msg)
}

func ingressIdentityForMessage(msg core.InboundMessage) (ingressIdentity, bool) {
	surface := strings.TrimSpace(msg.IngressSurface)
	if surface == "" || msg.IngressUpdateID <= 0 {
		return ingressIdentity{}, false
	}
	return ingressIdentity{surface: surface, updateID: msg.IngressUpdateID}, true
}

func IngressIdentityForMessage(msg core.InboundMessage) (string, int64, bool) {
	identity, ok := ingressIdentityForMessage(msg)
	if !ok {
		return "", 0, false
	}
	return identity.surface, identity.updateID, true
}
