//go:build linux

package telegramcontrol

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const (
	ingressSequencerBuffer  = 256
	ingressSequencerIdleTTL = 5 * time.Minute
)

var errIngressWorkerRetired = errors.New("ingress worker retired")

type IngressSequencer struct {
	router      *core.Router
	turnTimeout time.Duration
	idleTTL     time.Duration

	mu          sync.Mutex
	workers     map[string]*ingressWorker
	dropHandler func([]core.InboundMessage)
}

func NewIngressSequencer(router *core.Router, turnTimeout time.Duration) *IngressSequencer {
	if router == nil {
		return nil
	}
	return &IngressSequencer{
		router:      router,
		turnTimeout: turnTimeout,
		idleTTL:     ingressSequencerIdleTTL,
		workers:     make(map[string]*ingressWorker),
	}
}

func (s *IngressSequencer) SetDropHandler(handler func([]core.InboundMessage)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropHandler = handler
}

func (s *IngressSequencer) Enqueue(parent context.Context, msg core.InboundMessage) error {
	if s == nil || s.router == nil {
		return nil
	}
	if parent == nil {
		parent = context.Background()
	}
	if msg.IngressQueuedAt.IsZero() {
		msg.IngressQueuedAt = time.Now().UTC()
	}
	sessionID := core.SessionIDForInboundMessage(msg)
	for {
		worker := s.workerFor(sessionID)
		if err := worker.enqueue(parent, msg); err == nil {
			return nil
		} else if !errors.Is(err, errIngressWorkerRetired) {
			return err
		}
	}
}

func (s *IngressSequencer) Stop(chatID int64) core.StopResult {
	if s == nil || chatID == 0 {
		return core.StopResult{}
	}
	return s.stopMatching(func(worker *ingressWorker) bool {
		return worker.belongsToChat(chatID)
	})
}

func (s *IngressSequencer) StopForMessage(msg core.InboundMessage) core.StopResult {
	if s == nil {
		return core.StopResult{}
	}
	sessionID := core.SessionIDForInboundMessage(msg)
	return s.stopMatching(func(worker *ingressWorker) bool {
		return worker.sessionID == sessionID
	})
}

func (s *IngressSequencer) Status(chatID int64) core.SessionStatus {
	status := core.SessionStatus{}
	if s == nil || chatID == 0 {
		return status
	}
	s.mu.Lock()
	workers := make([]*ingressWorker, 0, len(s.workers))
	for _, worker := range s.workers {
		workers = append(workers, worker)
	}
	s.mu.Unlock()
	for _, worker := range workers {
		if !worker.belongsToChat(chatID) {
			continue
		}
		active, depth := worker.status()
		status.Active = status.Active || active || depth > 0
		status.QueueDepth += depth
	}
	status.Queued = status.QueueDepth > 0
	return status
}

func (s *IngressSequencer) StatusForMessage(msg core.InboundMessage) core.SessionStatus {
	if s == nil {
		return core.SessionStatus{}
	}
	sessionID := core.SessionIDForInboundMessage(msg)
	s.mu.Lock()
	worker := s.workers[sessionID]
	s.mu.Unlock()
	if worker == nil {
		return core.SessionStatus{}
	}
	active, depth := worker.status()
	return core.SessionStatus{
		Active:     active || depth > 0,
		Queued:     depth > 0,
		QueueDepth: depth,
	}
}

func (s *IngressSequencer) Snapshot() core.RouterStatusSnapshot {
	snapshot := core.RouterStatusSnapshot{
		QueueDepthByChat: make(map[int64]int),
	}
	if s == nil {
		return snapshot
	}
	s.mu.Lock()
	workers := make([]*ingressWorker, 0, len(s.workers))
	for _, worker := range s.workers {
		workers = append(workers, worker)
	}
	s.mu.Unlock()
	for _, worker := range workers {
		depthByChat := worker.queueDepthByChat()
		for chatID, depth := range depthByChat {
			snapshot.QueueDepthByChat[chatID] += depth
		}
	}
	return snapshot
}

func (s *IngressSequencer) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	workers := make([]*ingressWorker, 0, len(s.workers))
	for sessionID, worker := range s.workers {
		workers = append(workers, worker)
		delete(s.workers, sessionID)
	}
	s.mu.Unlock()
	for _, worker := range workers {
		worker.close()
	}
}

func (s *IngressSequencer) workerFor(sessionID string) *ingressWorker {
	s.mu.Lock()
	defer s.mu.Unlock()
	if worker, ok := s.workers[sessionID]; ok && worker != nil && !worker.retired() {
		return worker
	}
	worker := &ingressWorker{
		sessionID:     sessionID,
		notify:        make(chan struct{}, 1),
		queuedIngress: make(map[ingressIdentity]struct{}),
		chatIDs:       make(map[int64]struct{}),
		lastActivity:  time.Now().UTC(),
	}
	s.workers[sessionID] = worker
	go s.runWorker(worker)
	return worker
}

func (s *IngressSequencer) stopMatching(match func(worker *ingressWorker) bool) core.StopResult {
	result := core.StopResult{}
	var droppedMessages []core.InboundMessage
	s.mu.Lock()
	workers := make([]*ingressWorker, 0, len(s.workers))
	for _, worker := range s.workers {
		if worker != nil && match(worker) {
			workers = append(workers, worker)
		}
	}
	s.mu.Unlock()

	for _, worker := range workers {
		active, dropped, messages := worker.stop()
		result.ActiveCanceled = result.ActiveCanceled || active
		result.QueuedDropped = result.QueuedDropped || dropped > 0
		droppedMessages = append(droppedMessages, messages...)
	}
	s.notifyDroppedIngress(droppedMessages)
	return result
}

func (s *IngressSequencer) runWorker(worker *ingressWorker) {
	for {
		item, ok := worker.next(s.idleTTL)
		if !ok {
			if s.retireWorker(worker) {
				return
			}
			continue
		}
		turnCtx, cancel := newIngressTurnContext(item.parent, s.turnTimeout)
		if !worker.activate(item, cancel) {
			cancel()
			continue
		}
		if err := turnCtx.Err(); err == nil {
			s.router.Route(turnCtx, item.msg)
		}
		cancel()
		worker.deactivate()
	}
}

func (s *IngressSequencer) retireWorker(worker *ingressWorker) bool {
	if s == nil || worker == nil {
		return true
	}
	if !worker.shouldRetire(s.idleTTL) {
		return false
	}
	s.mu.Lock()
	if current := s.workers[worker.sessionID]; current == worker {
		delete(s.workers, worker.sessionID)
	}
	s.mu.Unlock()
	worker.close()
	return true
}

func newIngressTurnContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout > 0 {
		return context.WithTimeout(parent, timeout)
	}
	return context.WithCancel(parent)
}

func (s *IngressSequencer) notifyDroppedIngress(messages []core.InboundMessage) {
	if s == nil || len(messages) == 0 {
		return
	}
	s.mu.Lock()
	handler := s.dropHandler
	s.mu.Unlock()
	if handler != nil {
		handler(messages)
	}
}
