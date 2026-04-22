//go:build linux

package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

	mu           sync.Mutex
	locks        map[string]*sync.Mutex
	queues       map[string][]InboundMessage
	sessions     map[string]*SessionState
	active       map[string]activeTurn
	sessionChats map[string]map[int64]struct{}
	ingressSeq   map[string]int64
	nextID       uint64
	logger       routerLogger
	onEvent      RouterEventHandler
}

type activeTurn struct {
	id     uint64
	cancel context.CancelFunc
}

type SessionStatus struct {
	Active bool
	Queued bool
	// QueueDepth is the count of queued follow-up messages for this chat.
	QueueDepth int
	// Diagnostics includes optional status details from higher-level runtime layers.
	Diagnostics []string
}

type StopResult struct {
	ActiveCanceled      bool
	QueuedDropped       bool
	ContinuationRevoked bool
}

type DetachResult struct {
	ActiveCanceled           bool
	QueuedDropped            bool
	ContinuationRevoked      bool
	PendingDecisionsDetached int
}

type NewSessionResult struct {
	ActiveCanceled           bool
	QueuedDropped            bool
	ContinuationRevoked      bool
	PendingDecisionsDetached int
	ContextCleared           bool
}

// NewRouter constructs a Router using fn for each routed turn.
func NewRouter(fn AgentFunc) *Router {
	return &Router{
		agent:        fn,
		locks:        make(map[string]*sync.Mutex),
		queues:       make(map[string][]InboundMessage),
		sessions:     make(map[string]*SessionState),
		active:       make(map[string]activeTurn),
		sessionChats: make(map[string]map[int64]struct{}),
		ingressSeq:   make(map[string]int64),
		logger:       defaultRouterLogger(),
	}
}

func (r *Router) SetEventHandler(handler RouterEventHandler) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onEvent = handler
}

// Route routes msg to its session. If a turn is active for the session, the message
// is queued. When queued messages exist after a turn completes, they are compacted
// into a single follow-up input so the next turn has full queue context.
func (r *Router) Route(ctx context.Context, msg InboundMessage) {
	sessionID, lock, session := r.resolveSession(msg)
	msg = r.assignIngressSeq(sessionID, msg)
	r.emitRouterEvent(ctx, msg, sessionID, ExecutionEventIngressAccepted, 0, 0)

	if !lock.TryLock() {
		queued := r.enqueue(sessionID, msg)
		r.emitRouterEvent(ctx, msg, sessionID, ExecutionEventIngressQueued, queued, 0)
		r.logger.Debug("session busy; queued message", "chat_id", msg.ChatID, "message_id", msg.MessageID, "queued_count", queued)
		return
	}
	defer lock.Unlock()

	current := msg
	r.emitRouterEvent(ctx, current, sessionID, ExecutionEventIngressSelected, 0, 0)
	for {
		turnCtx, cancel := context.WithCancel(ctx)
		activeID := r.markActive(sessionID, current.ChatID, cancel)

		_, err := r.agent(turnCtx, session, current)
		cancel()
		r.clearActive(sessionID, activeID)

		if err != nil {
			if errors.Is(err, context.Canceled) {
				r.logger.Debug("agent turn canceled", "chat_id", current.ChatID, "message_id", current.MessageID)
			} else {
				r.logger.Error("agent turn failed", "chat_id", current.ChatID, "message_id", current.MessageID, "error", err)
			}
		}

		next, drained, ok := r.dequeueCompacted(sessionID)
		if !ok {
			return
		}
		r.emitRouterEvent(ctx, next, sessionID, ExecutionEventIngressCompacted, 0, drained)
		r.logger.Debug("processing compacted queued messages", "chat_id", next.ChatID, "message_id", next.MessageID, "drained_count", drained)
		current = next
		r.emitRouterEvent(ctx, current, sessionID, ExecutionEventIngressSelected, 0, 0)
	}
}

func (r *Router) StatusForMessage(msg InboundMessage) SessionStatus {
	sessionID := routeSessionID(msg)

	r.mu.Lock()
	defer r.mu.Unlock()

	queue := r.queues[sessionID]
	_, active := r.active[sessionID]
	return SessionStatus{
		Active:     active,
		Queued:     len(queue) > 0,
		QueueDepth: len(queue),
	}
}

func (r *Router) Status(chatID int64) SessionStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	active := false
	queueDepth := 0
	for sessionID := range r.active {
		if r.sessionBelongsToChatLocked(sessionID, chatID) {
			active = true
			break
		}
	}
	for sessionID, queue := range r.queues {
		if len(queue) == 0 {
			continue
		}
		if r.sessionBelongsToChatLocked(sessionID, chatID) {
			queueDepth += len(queue)
		}
	}

	return SessionStatus{
		Active:     active,
		Queued:     queueDepth > 0,
		QueueDepth: queueDepth,
	}
}

func (r *Router) Snapshot() RouterStatusSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	snapshot := RouterStatusSnapshot{
		ActiveTurnsByChat: make(map[int64][]uint64, len(r.active)),
		QueueDepthByChat:  make(map[int64]int, len(r.queues)),
	}
	for sessionID, active := range r.active {
		if active.id == 0 {
			continue
		}
		for _, chatID := range r.chatsForSessionLocked(sessionID) {
			snapshot.ActiveTurnsByChat[chatID] = append(snapshot.ActiveTurnsByChat[chatID], active.id)
		}
	}
	for sessionID, queue := range r.queues {
		if len(queue) <= 0 {
			continue
		}
		for _, chatID := range r.chatsForSessionLocked(sessionID) {
			snapshot.QueueDepthByChat[chatID] += len(queue)
		}
	}
	return snapshot
}

func (r *Router) StopForMessage(msg InboundMessage) StopResult {
	sessionID := routeSessionID(msg)
	return r.stopMatching(func(candidate string) bool {
		return candidate == sessionID
	})
}

func (r *Router) Stop(chatID int64) StopResult {
	return r.stopMatching(func(sessionID string) bool {
		return r.sessionBelongsToChatLocked(sessionID, chatID)
	})
}

func (r *Router) stopMatching(match func(sessionID string) bool) StopResult {
	var (
		result  StopResult
		cancels []context.CancelFunc
	)

	r.mu.Lock()
	for sessionID, current := range r.active {
		if !match(sessionID) {
			continue
		}
		delete(r.active, sessionID)
		if current.cancel != nil {
			cancels = append(cancels, current.cancel)
		}
		result.ActiveCanceled = true
	}
	for sessionID, queue := range r.queues {
		if len(queue) == 0 || !match(sessionID) {
			continue
		}
		delete(r.queues, sessionID)
		result.QueuedDropped = true
	}
	r.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	return result
}

func (r *Router) resolveSession(msg InboundMessage) (string, *sync.Mutex, *SessionState) {
	sessionID := routeSessionID(msg)

	r.mu.Lock()
	defer r.mu.Unlock()

	lock := r.locks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.locks[sessionID] = lock
	}

	session := r.sessions[sessionID]
	if session == nil {
		session = &SessionState{ChatID: msg.ChatID}
		r.sessions[sessionID] = session
	} else if session.ChatID == 0 && msg.ChatID != 0 {
		session.ChatID = msg.ChatID
	}
	r.trackSessionChatLocked(sessionID, msg.ChatID)

	return sessionID, lock, session
}

func (r *Router) enqueue(sessionID string, msg InboundMessage) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trackSessionChatLocked(sessionID, msg.ChatID)
	queue := r.queues[sessionID]
	queue = append(queue, msg)
	r.queues[sessionID] = queue
	return len(queue)
}

func (r *Router) dequeueCompacted(sessionID string) (InboundMessage, int, bool) {
	r.mu.Lock()
	queue := r.queues[sessionID]
	if len(queue) == 0 {
		r.mu.Unlock()
		return InboundMessage{}, 0, false
	}
	delete(r.queues, sessionID)
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

func (r *Router) markActive(sessionID string, chatID int64, cancel context.CancelFunc) uint64 {
	id := atomic.AddUint64(&r.nextID, 1)
	r.mu.Lock()
	r.trackSessionChatLocked(sessionID, chatID)
	r.active[sessionID] = activeTurn{id: id, cancel: cancel}
	r.mu.Unlock()
	return id
}

func (r *Router) clearActive(sessionID string, id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.active[sessionID]
	if !ok || current.id != id {
		return
	}
	delete(r.active, sessionID)
}

func (r *Router) trackSessionChatLocked(sessionID string, chatID int64) {
	if strings.TrimSpace(sessionID) == "" || chatID == 0 {
		return
	}
	chats := r.sessionChats[sessionID]
	if chats == nil {
		chats = make(map[int64]struct{})
		r.sessionChats[sessionID] = chats
	}
	chats[chatID] = struct{}{}
}

func (r *Router) sessionBelongsToChatLocked(sessionID string, chatID int64) bool {
	if chatID == 0 {
		return false
	}
	if chats := r.sessionChats[sessionID]; len(chats) > 0 {
		_, ok := chats[chatID]
		return ok
	}
	sess := r.sessions[sessionID]
	return sess != nil && sess.ChatID == chatID
}

func (r *Router) chatsForSessionLocked(sessionID string) []int64 {
	if chats := r.sessionChats[sessionID]; len(chats) > 0 {
		out := make([]int64, 0, len(chats))
		for chatID := range chats {
			out = append(out, chatID)
		}
		return out
	}
	sess := r.sessions[sessionID]
	if sess == nil || sess.ChatID == 0 {
		return nil
	}
	return []int64{sess.ChatID}
}

func (r *Router) assignIngressSeq(sessionID string, msg InboundMessage) InboundMessage {
	if r == nil || strings.TrimSpace(sessionID) == "" {
		return msg
	}
	if msg.IngressSeq > 0 {
		return msg
	}
	r.mu.Lock()
	next := r.ingressSeq[sessionID] + 1
	r.ingressSeq[sessionID] = next
	r.mu.Unlock()
	msg.IngressSeq = next
	return msg
}

func (r *Router) emitRouterEvent(ctx context.Context, msg InboundMessage, sessionID string, eventType string, queueDepth int, drainedCount int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	handler := r.onEvent
	r.mu.Unlock()
	if handler == nil {
		return
	}
	handler(ctx, RouterEvent{
		EventType:      strings.TrimSpace(eventType),
		SessionID:      strings.TrimSpace(sessionID),
		ChatID:         msg.ChatID,
		UserID:         msg.SenderID,
		ChatType:       strings.TrimSpace(msg.ChatType),
		DurableAgentID: strings.TrimSpace(msg.DurableAgentID),
		MessageID:      msg.MessageID,
		IngressSeq:     msg.IngressSeq,
		QueueDepth:     queueDepth,
		DrainedCount:   drainedCount,
		CreatedAt:      time.Now().UTC(),
	})
}

func routeSessionID(msg InboundMessage) string {
	if agentID := strings.TrimSpace(msg.DurableAgentID); agentID != "" {
		return "durable_agent:" + agentID
	}
	switch strings.ToLower(strings.TrimSpace(msg.ChatType)) {
	case "group", "supergroup", "channel":
		if msg.ChatID != 0 {
			return fmt.Sprintf("telegram_group:%d", msg.ChatID)
		}
	default:
		if msg.ChatID != 0 {
			return fmt.Sprintf("telegram_dm:%d", msg.ChatID)
		}
	}
	return fmt.Sprintf("transport:%d", msg.ChatID)
}

func SessionIDForInboundMessage(msg InboundMessage) string {
	return routeSessionID(msg)
}
