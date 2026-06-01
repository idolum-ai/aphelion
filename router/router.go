//go:build linux

package router

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

// Router maps inbound messages to sessions and enforces per-session turn serialization.
type Router struct {
	agent core.AgentFunc

	mu           sync.Mutex
	locks        map[string]*sync.Mutex
	queues       map[string][]core.InboundMessage
	sessions     map[string]*core.SessionState
	active       map[string]activeTurn
	sessionChats map[string]map[int64]struct{}
	ingressSeq   map[string]int64
	nextID       uint64
	logger       routerLogger
	onEvent      core.RouterEventHandler
}

type activeTurn struct {
	id     uint64
	cancel context.CancelFunc
}

// NewRouter constructs a Router using fn for each routed turn.
func NewRouter(fn core.AgentFunc) *Router {
	return &Router{
		agent:        fn,
		locks:        make(map[string]*sync.Mutex),
		queues:       make(map[string][]core.InboundMessage),
		sessions:     make(map[string]*core.SessionState),
		active:       make(map[string]activeTurn),
		sessionChats: make(map[string]map[int64]struct{}),
		ingressSeq:   make(map[string]int64),
		logger:       defaultRouterLogger(),
	}
}

func (r *Router) SetEventHandler(handler core.RouterEventHandler) {
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
func (r *Router) Route(ctx context.Context, msg core.InboundMessage) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return
		}
	}
	if msg.IngressQueuedAt.IsZero() {
		msg.IngressQueuedAt = time.Now().UTC()
	}
	sessionID, lock, session := r.resolveSession(msg)
	msg = r.assignIngressSeq(sessionID, msg)
	r.emitRouterEvent(ctx, msg, sessionID, core.ExecutionEventIngressAccepted, 0, 0, 0, false)

	lockStart := time.Now()
	if !lock.TryLock() {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return
			}
		}
		queued := r.enqueue(sessionID, msg)
		r.emitRouterEvent(ctx, msg, sessionID, core.ExecutionEventIngressQueued, queued, 0, 0, false)
		r.logger.Debug("session busy; queued message", "chat_id", msg.ChatID, "message_id", msg.MessageID, "queued_count", queued)
		return
	}
	routerLockWait := time.Since(lockStart)
	defer lock.Unlock()

	current := msg
	r.emitRouterEvent(ctx, current, sessionID, core.ExecutionEventIngressSelected, 0, 0, routerLockWait, true)
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

		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return
			}
		}
		next, drained, ok := r.dequeueCompacted(sessionID)
		if !ok {
			return
		}
		r.emitRouterEvent(ctx, next, sessionID, core.ExecutionEventIngressCompacted, 0, drained, 0, false)
		r.logger.Debug("processing compacted queued messages", "chat_id", next.ChatID, "message_id", next.MessageID, "drained_count", drained)
		current = next
		r.emitRouterEvent(ctx, current, sessionID, core.ExecutionEventIngressSelected, 0, 0, 0, true)
	}
}

func (r *Router) StatusForMessage(msg core.InboundMessage) core.SessionStatus {
	sessionID := routeSessionID(msg)

	r.mu.Lock()
	defer r.mu.Unlock()

	queue := r.queues[sessionID]
	_, active := r.active[sessionID]
	return core.SessionStatus{
		Active:     active,
		Queued:     len(queue) > 0,
		QueueDepth: len(queue),
	}
}

func (r *Router) Status(chatID int64) core.SessionStatus {
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

	return core.SessionStatus{
		Active:     active,
		Queued:     queueDepth > 0,
		QueueDepth: queueDepth,
	}
}

func (r *Router) Snapshot() core.RouterStatusSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	observedAt := time.Now().UTC()
	snapshot := core.RouterStatusSnapshot{
		ActiveTurnsByChat: make(map[int64][]uint64, len(r.active)),
		QueueDepthByChat:  make(map[int64]int, len(r.queues)),
	}
	for sessionID, active := range r.active {
		if active.id == 0 {
			continue
		}
		for _, chatID := range r.chatsForSessionLocked(sessionID) {
			snapshot.ActiveTurnsByChat[chatID] = append(snapshot.ActiveTurnsByChat[chatID], active.id)
			snapshot.TotalActiveTurns++
		}
	}
	for sessionID, queue := range r.queues {
		if len(queue) <= 0 {
			continue
		}
		for _, chatID := range r.chatsForSessionLocked(sessionID) {
			snapshot.QueueDepthByChat[chatID] += len(queue)
			snapshot.TotalQueuedMessages += len(queue)
			if depth := snapshot.QueueDepthByChat[chatID]; depth > snapshot.MaxQueueDepth {
				snapshot.MaxQueueDepth = depth
				snapshot.MaxQueueDepthChatID = chatID
			}
		}
		for _, msg := range queue {
			if msg.IngressQueuedAt.IsZero() {
				continue
			}
			queuedAt := msg.IngressQueuedAt.UTC()
			if snapshot.OldestQueuedAt.IsZero() || queuedAt.Before(snapshot.OldestQueuedAt) {
				snapshot.OldestQueuedAt = queuedAt
				snapshot.OldestQueuedAge = nonNegativeDuration(observedAt.Sub(queuedAt))
				snapshot.OldestQueuedChatID = firstSnapshotChatID(r.chatsForSessionLocked(sessionID), msg.ChatID)
			}
		}
	}
	return snapshot
}

func firstSnapshotChatID(chatIDs []int64, fallback int64) int64 {
	for _, chatID := range chatIDs {
		if chatID != 0 {
			return chatID
		}
	}
	return fallback
}

func (r *Router) StopForMessage(msg core.InboundMessage) core.StopResult {
	sessionID := routeSessionID(msg)
	return r.stopMatching(func(candidate string) bool {
		return candidate == sessionID
	})
}

func (r *Router) Stop(chatID int64) core.StopResult {
	return r.stopMatching(func(sessionID string) bool {
		return r.sessionBelongsToChatLocked(sessionID, chatID)
	})
}

func (r *Router) stopMatching(match func(sessionID string) bool) core.StopResult {
	var (
		result  core.StopResult
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

func (r *Router) resolveSession(msg core.InboundMessage) (string, *sync.Mutex, *core.SessionState) {
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
		session = &core.SessionState{ChatID: msg.ChatID}
		r.sessions[sessionID] = session
	} else if session.ChatID == 0 && msg.ChatID != 0 {
		session.ChatID = msg.ChatID
	}
	r.trackSessionChatLocked(sessionID, msg.ChatID)

	return sessionID, lock, session
}

func (r *Router) enqueue(sessionID string, msg core.InboundMessage) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trackSessionChatLocked(sessionID, msg.ChatID)
	queue := r.queues[sessionID]
	queue = append(queue, msg)
	r.queues[sessionID] = queue
	return len(queue)
}

func (r *Router) dequeueCompacted(sessionID string) (core.InboundMessage, int, bool) {
	r.mu.Lock()
	queue := r.queues[sessionID]
	if len(queue) == 0 {
		r.mu.Unlock()
		return core.InboundMessage{}, 0, false
	}
	delete(r.queues, sessionID)
	r.mu.Unlock()
	return compactQueuedMessages(queue), len(queue), true
}

func compactQueuedMessages(queue []core.InboundMessage) core.InboundMessage {
	if len(queue) == 0 {
		return core.InboundMessage{}
	}
	if len(queue) == 1 {
		return queue[0]
	}

	latest := queue[len(queue)-1]
	compacted := latest
	compacted.Artifacts = latest.Artifacts
	for _, msg := range queue {
		if msg.IngressQueuedAt.IsZero() {
			continue
		}
		if compacted.IngressQueuedAt.IsZero() || msg.IngressQueuedAt.Before(compacted.IngressQueuedAt) {
			compacted.IngressQueuedAt = msg.IngressQueuedAt
		}
	}

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

func (r *Router) assignIngressSeq(sessionID string, msg core.InboundMessage) core.InboundMessage {
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

func (r *Router) emitRouterEvent(ctx context.Context, msg core.InboundMessage, sessionID string, eventType string, queueDepth int, drainedCount int, routerLockWait time.Duration, routerLockWaitKnown bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	handler := r.onEvent
	r.mu.Unlock()
	if handler == nil {
		return
	}
	createdAt := time.Now().UTC()
	ingressQueueWait, ingressQueueWaitKnown := ingressQueueWaitSince(msg.IngressQueuedAt, createdAt)
	handler(ctx, core.RouterEvent{
		EventType:             strings.TrimSpace(eventType),
		SessionID:             strings.TrimSpace(sessionID),
		ChatID:                msg.ChatID,
		UserID:                msg.SenderID,
		ChatType:              strings.TrimSpace(msg.ChatType),
		DurableAgentID:        strings.TrimSpace(msg.DurableAgentID),
		MessageID:             msg.MessageID,
		IngressSeq:            msg.IngressSeq,
		IngressSurface:        strings.TrimSpace(msg.IngressSurface),
		IngressUpdateID:       msg.IngressUpdateID,
		QueueDepth:            queueDepth,
		DrainedCount:          drainedCount,
		IngressQueueWait:      ingressQueueWait,
		IngressQueueWaitKnown: ingressQueueWaitKnown,
		RouterLockWait:        nonNegativeDuration(routerLockWait),
		RouterLockWaitKnown:   routerLockWaitKnown,
		CreatedAt:             createdAt,
	})
}

func ingressQueueWaitSince(queuedAt time.Time, observedAt time.Time) (time.Duration, bool) {
	if queuedAt.IsZero() || observedAt.IsZero() {
		return 0, false
	}
	return nonNegativeDuration(observedAt.Sub(queuedAt.UTC())), true
}

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func routeSessionID(msg core.InboundMessage) string {
	return core.SessionIDForInboundMessage(msg)
}
