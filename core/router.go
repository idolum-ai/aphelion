//go:build linux

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	queues   map[int64][]InboundMessage
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
		queues:   make(map[int64][]InboundMessage),
		sessions: make(map[int64]*SessionState),
		active:   make(map[int64]activeTurn),
		logger:   defaultRouterLogger(),
	}
}

// Route routes msg to its session. If a turn is active for the session, the message
// is queued. When queued messages exist after a turn completes, they are compacted
// into a single follow-up input so the next turn has full queue context.
func (r *Router) Route(ctx context.Context, msg InboundMessage) {
	lock, session := r.resolveSession(msg.ChatID)

	if !lock.TryLock() {
		queued := r.enqueue(msg.ChatID, msg)
		r.logger.Debug("session busy; queued message", "chat_id", msg.ChatID, "message_id", msg.MessageID, "queued_count", queued)
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

		next, drained, ok := r.dequeueCompacted(current.ChatID)
		if !ok {
			return
		}
		r.logger.Debug("processing compacted queued messages", "chat_id", next.ChatID, "message_id", next.MessageID, "drained_count", drained)
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
		Queued: len(queue) > 0,
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
	if queue := r.queues[chatID]; len(queue) > 0 {
		delete(r.queues, chatID)
		result.QueuedDropped = true
	}
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	return result
}

func (r *Router) resolveSession(chatID int64) (*sync.Mutex, *SessionState) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lock := r.locks[chatID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[chatID] = lock
	}

	session := r.sessions[chatID]
	if session == nil {
		session = &SessionState{ChatID: chatID}
		r.sessions[chatID] = session
	}

	return lock, session
}

func (r *Router) enqueue(chatID int64, msg InboundMessage) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	queue := r.queues[chatID]
	queue = append(queue, msg)
	r.queues[chatID] = queue
	return len(queue)
}

func (r *Router) dequeueCompacted(chatID int64) (InboundMessage, int, bool) {
	r.mu.Lock()
	queue := r.queues[chatID]
	if len(queue) == 0 {
		r.mu.Unlock()
		return InboundMessage{}, 0, false
	}
	delete(r.queues, chatID)
	r.mu.Unlock()
	return compactQueuedMessages(queue), len(queue), true
}

func compactQueuedMessages(queue []InboundMessage) InboundMessage {
	if len(queue) == 0 {
		return InboundMessage{}
	}
	if len(queue) == 1 {
		return queue[0]
	}

	latest := queue[len(queue)-1]
	compacted := latest
	compacted.Artifacts = latest.Artifacts

	texts := make([]string, 0, len(queue))
	for i, msg := range queue {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			text = "(no text)"
		}
		texts = append(texts, fmt.Sprintf("%d. %s", i+1, text))
	}
	compacted.Text = strings.Join([]string{
		fmt.Sprintf("Merged %d queued follow-up messages (oldest to newest):", len(queue)),
		strings.Join(texts, "\n"),
		"",
		"Prioritize the newest message when instructions conflict.",
	}, "\n")
	return compacted
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
