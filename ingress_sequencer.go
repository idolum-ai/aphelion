//go:build linux

package main

import (
	"context"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

const ingressSequencerBuffer = 256

type ingressSequencer struct {
	router      *core.Router
	turnTimeout time.Duration

	mu      sync.Mutex
	workers map[string]chan ingressWorkItem
}

type ingressWorkItem struct {
	parent context.Context
	msg    core.InboundMessage
}

func newIngressSequencer(router *core.Router, turnTimeout time.Duration) *ingressSequencer {
	if router == nil {
		return nil
	}
	return &ingressSequencer{
		router:      router,
		turnTimeout: turnTimeout,
		workers:     make(map[string]chan ingressWorkItem),
	}
}

func (s *ingressSequencer) Enqueue(parent context.Context, msg core.InboundMessage) {
	if s == nil || s.router == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	sessionID := core.SessionIDForInboundMessage(msg)
	worker := s.workerFor(sessionID)
	worker <- ingressWorkItem{
		parent: parent,
		msg:    msg,
	}
}

func (s *ingressSequencer) workerFor(sessionID string) chan ingressWorkItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if worker, ok := s.workers[sessionID]; ok && worker != nil {
		return worker
	}
	worker := make(chan ingressWorkItem, ingressSequencerBuffer)
	s.workers[sessionID] = worker
	go s.runWorker(worker)
	return worker
}

func (s *ingressSequencer) runWorker(worker <-chan ingressWorkItem) {
	for item := range worker {
		turnCtx, cancel := newTurnContext(item.parent, s.turnTimeout)
		s.router.Route(turnCtx, item.msg)
		cancel()
	}
}
