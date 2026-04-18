//go:build linux

package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// AgentFunc executes one agent turn for a session.
type AgentFunc func(ctx context.Context, session *SessionState, msg InboundMessage) (*TurnResult, error)

// SessionState is the in-memory state for a chat session.
type SessionState struct {
	ChatID       int64
	Messages     []map[string]interface{}
	SystemPrompt string
}

// Router maps inbound messages to sessions and enforces per-session turn serialization.
type Router struct {
	agent AgentFunc

	mu       sync.Mutex
	locks    map[int64]*sync.Mutex
	queues   map[int64]chan InboundMessage
	sessions map[int64]*SessionState
	active   map[int64]activeTurn
	nextID   uint64
	logger   routerLogger
}

type activeTurn struct {
	id     uint64
	cancel context.CancelFunc
}

type SessionStatus struct {
	Active bool
	Queued bool
	// Diagnostics includes optional status details from higher-level runtime layers.
	Diagnostics []string
}

type StopResult struct {
	ActiveCanceled      bool
	QueuedDropped       bool
	ContinuationRevoked bool
}

// NewRouter constructs a Router using fn for each routed turn.
func NewRouter(fn AgentFunc) *Router {
	return &Router{
		agent:    fn,
		locks:    make(map[int64]*sync.Mutex),
		queues:   make(map[int64]chan InboundMessage),
		sessions: make(map[int64]*SessionState),
		active:   make(map[int64]activeTurn),
		logger:   defaultRouterLogger(),
	}
}

// Route routes msg to its session. If a turn is active for the session, the message
// is queued in a cap-1 latest-wins buffer.
func (r *Router) Route(ctx context.Context, msg InboundMessage) {
	lock, queue, session := r.resolveSession(msg.ChatID)

	if !lock.TryLock() {
		r.enqueueLatest(queue, msg)
		r.logger.Debug("session busy; queued latest message", "chat_id", msg.ChatID, "message_id", msg.MessageID)
		return
	}
	defer lock.Unlock()

	current := msg
	for {
		turnCtx, cancel := context.WithCancel(ctx)
		activeID := r.markActive(current.ChatID, cancel)

		_, err := r.agent(turnCtx, session, current)
		cancel()
		r.clearActive(current.ChatID, activeID)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				r.logger.Debug("agent turn canceled", "chat_id", current.ChatID, "message_id", current.MessageID)
			} else {
				r.logger.Error("agent turn failed", "chat_id", current.ChatID, "message_id", current.MessageID, "error", err)
			}
		}

		next, ok := r.dequeue(queue)
		if !ok {
			return
		}
		r.logger.Debug("processing queued message", "chat_id", next.ChatID, "message_id", next.MessageID)
		current = next
	}
}

func (r *Router) Status(chatID int64) SessionStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	queue := r.queues[chatID]
	_, active := r.active[chatID]
	return SessionStatus{
		Active: active,
		Queued: queue != nil && len(queue) > 0,
	}
}

func (r *Router) Stop(chatID int64) StopResult {
	var result StopResult
	var cancel context.CancelFunc

	r.mu.Lock()
	if current, ok := r.active[chatID]; ok {
		cancel = current.cancel
		delete(r.active, chatID)
		result.ActiveCanceled = true
	}
	if queue := r.queues[chatID]; queue != nil {
		select {
		case <-queue:
			result.QueuedDropped = true
		default:
		}
	}
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return result
}

func (r *Router) resolveSession(chatID int64) (*sync.Mutex, chan InboundMessage, *SessionState) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lock := r.locks[chatID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[chatID] = lock
	}

	queue := r.queues[chatID]
	if queue == nil {
		queue = make(chan InboundMessage, 1)
		r.queues[chatID] = queue
	}

	session := r.sessions[chatID]
	if session == nil {
		session = &SessionState{ChatID: chatID}
		r.sessions[chatID] = session
	}

	return lock, queue, session
}

func (r *Router) enqueueLatest(queue chan InboundMessage, msg InboundMessage) {
	select {
	case <-queue:
	default:
	}

	select {
	case queue <- msg:
	default:
		// Should not happen because we drain first, but keep latest-wins guarantee.
		select {
		case <-queue:
		default:
		}
		queue <- msg
	}
}

func (r *Router) dequeue(queue chan InboundMessage) (InboundMessage, bool) {
	select {
	case msg := <-queue:
		return msg, true
	default:
		return InboundMessage{}, false
	}
}

func (r *Router) markActive(chatID int64, cancel context.CancelFunc) uint64 {
	id := atomic.AddUint64(&r.nextID, 1)
	r.mu.Lock()
	r.active[chatID] = activeTurn{id: id, cancel: cancel}
	r.mu.Unlock()
	return id
}

func (r *Router) clearActive(chatID int64, id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.active[chatID]
	if !ok || current.id != id {
		return
	}
	delete(r.active, chatID)
}
