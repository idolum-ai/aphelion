//go:build linux

package decision

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Request struct {
	Kind           Kind
	ChatID         int64
	SenderID       int64
	MessageID      int64
	OwnerKey       string
	SessionID      string
	ScopeKind      string
	ScopeID        string
	DurableAgentID string
	Prompt         string
	Details        string
	Metadata       map[string]string
	Choices        []Choice
	DefaultChoice  string
	Timeout        time.Duration
}

func (b *Broker) Request(ctx context.Context, req Request) (Result, error) {
	if b == nil {
		return Result{}, fmt.Errorf("decision broker is nil")
	}
	if err := b.ensureLoaded(ctx); err != nil {
		return Result{}, err
	}
	if len(req.Choices) == 0 {
		return Result{}, fmt.Errorf("decision choices are required")
	}
	if !containsChoice(req.Choices, req.DefaultChoice) {
		return Result{}, fmt.Errorf("default choice %q is not present", req.DefaultChoice)
	}

	decisionSeq, decisionID := b.nextDecision()
	normalized := normalizeRequest(req)
	ownerKey := decisionOwnerKey(normalized)
	pending := &pendingDecision{
		request: PendingDecision{
			ID:      decisionID,
			Request: normalized,
		},
		resultCh:     make(chan string, 1),
		ownerKey:     ownerKey,
		exclusiveKey: decisionExclusiveKey(normalized, ownerKey),
		seq:          decisionSeq,
	}
	if b.autoResolver != nil {
		resolution, err := b.autoResolver(ctx, pending.request)
		if err != nil {
			return Result{}, err
		}
		choice := strings.TrimSpace(resolution.Choice)
		if choice != "" {
			if !containsChoice(pending.request.Choices, choice) {
				return Result{}, fmt.Errorf("auto-resolved choice %q is not present", choice)
			}
			reason := strings.TrimSpace(resolution.Reason)
			if reason == "" {
				reason = "auto_approved"
			}
			if err := b.clearSupersededPendingForAutoResolve(ctx, pending); err != nil {
				return Result{}, err
			}
			b.mu.Lock()
			b.archiveResolvedDecisionLocked(pending.request)
			b.mu.Unlock()
			b.emitEvent(ctx, pending, EventTypeResolved, choice, false, reason)
			return Result{DecisionID: pending.request.ID, Choice: choice, Delivery: pending.delivery}, nil
		}
	}

	b.mu.Lock()
	b.pending[decisionID] = pending
	b.mu.Unlock()
	if err := b.upsertPending(ctx, pending); err != nil {
		_ = b.clearWithContext(ctx, decisionID)
		return Result{}, err
	}

	if b.notifier != nil {
		delivery, err := b.notifier(ctx, pending.request)
		if err != nil {
			_ = b.clearWithContext(ctx, decisionID)
			return Result{}, err
		}
		pending.delivery = delivery
		pending.request.Delivery = delivery
		if err := b.upsertPending(ctx, pending); err != nil {
			_ = b.clearWithContext(ctx, decisionID)
			return Result{}, err
		}
	}

	if stale := b.activateOwner(ctx, decisionID); stale {
		// activateOwner already detached and persisted stale cleanup before returning true.
		return Result{DecisionID: pending.request.ID, Choice: pending.request.DefaultChoice, Delivery: pending.delivery}, nil
	}
	b.emitEvent(ctx, pending, EventTypeOpened, "", false, "")

	timeout := pending.request.Timeout
	if timeout < 0 {
		select {
		case choice := <-pending.resultCh:
			_ = b.clearWithContext(ctx, decisionID)
			return Result{DecisionID: pending.request.ID, Choice: choice, Delivery: pending.delivery}, nil
		case <-ctx.Done():
			_ = b.clearWithContext(ctx, decisionID)
			b.emitEvent(ctx, pending, EventTypeDetached, strings.TrimSpace(pending.request.DefaultChoice), false, "context_canceled")
			return Result{}, ctx.Err()
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case choice := <-pending.resultCh:
		_ = b.clearWithContext(ctx, decisionID)
		return Result{DecisionID: pending.request.ID, Choice: choice, Delivery: pending.delivery}, nil
	case <-timer.C:
		_ = b.clearWithContext(ctx, decisionID)
		b.emitEvent(ctx, pending, EventTypeExpired, strings.TrimSpace(pending.request.DefaultChoice), true, "timeout")
		return Result{DecisionID: pending.request.ID, Choice: pending.request.DefaultChoice, Delivery: pending.delivery, TimedOut: true}, nil
	case <-ctx.Done():
		_ = b.clearWithContext(ctx, decisionID)
		b.emitEvent(ctx, pending, EventTypeDetached, strings.TrimSpace(pending.request.DefaultChoice), false, "context_canceled")
		return Result{}, ctx.Err()
	}
}

func (b *Broker) Resolve(id string, choice string) bool {
	return b.resolve(id, choice, CallbackActor{}, false).Resolved
}

func (b *Broker) ResolveCallback(id string, choice string, actor CallbackActor) bool {
	return b.ResolveCallbackDetailed(id, choice, actor).Resolved
}

func (b *Broker) ResolveCallbackDetailed(id string, choice string, actor CallbackActor) ResolveResult {
	return b.resolve(id, choice, actor, true)
}

func (b *Broker) resolve(id string, choice string, actor CallbackActor, requireActor bool) ResolveResult {
	if b == nil {
		return ResolveResult{}
	}
	if err := b.ensureLoaded(context.Background()); err != nil {
		return ResolveResult{}
	}
	id = strings.TrimSpace(id)
	choice = strings.TrimSpace(choice)
	if id == "" || choice == "" {
		return ResolveResult{}
	}

	b.mu.Lock()
	pending := b.pending[id]
	b.mu.Unlock()
	if pending == nil {
		return ResolveResult{}
	}
	if requireActor && !callbackActorMatchesDecision(pending.request, actor) {
		return ResolveResult{Pending: pending.request, LoadedFromDurable: pending.request.LoadedFromDurable}
	}
	if !containsChoice(pending.request.Choices, choice) {
		return ResolveResult{Pending: pending.request, LoadedFromDurable: pending.request.LoadedFromDurable}
	}
	b.mu.Lock()
	select {
	case pending.resultCh <- choice:
		b.archiveResolvedDecisionLocked(pending.request)
		b.mu.Unlock()
		_ = b.clearWithContext(context.Background(), id)
		b.emitEvent(context.Background(), pending, EventTypeResolved, choice, false, "callback")
		return ResolveResult{
			Resolved:          true,
			Pending:           pending.request,
			Choice:            choice,
			LoadedFromDurable: pending.request.LoadedFromDurable,
		}
	default:
		b.mu.Unlock()
		return ResolveResult{Pending: pending.request, LoadedFromDurable: pending.request.LoadedFromDurable}
	}
}

func (b *Broker) PeekCallback(id string, actor CallbackActor) (PendingDecision, bool) {
	pending, ok := b.Peek(id)
	if !ok || !callbackActorMatchesDecision(pending, actor) {
		return PendingDecision{}, false
	}
	return pending, true
}

func (b *Broker) Peek(id string) (PendingDecision, bool) {
	if b == nil {
		return PendingDecision{}, false
	}
	if err := b.ensureLoaded(context.Background()); err != nil {
		return PendingDecision{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return PendingDecision{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	pending := b.pending[id]
	if pending == nil {
		return PendingDecision{}, false
	}
	return pending.request, true
}

func (b *Broker) PeekResolvedCallback(id string, actor CallbackActor) (PendingDecision, bool) {
	pending, ok := b.PeekResolved(id)
	if !ok || !callbackActorMatchesDecision(pending, actor) {
		return PendingDecision{}, false
	}
	return pending, true
}

func (b *Broker) PeekResolved(id string) (PendingDecision, bool) {
	if b == nil {
		return PendingDecision{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return PendingDecision{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	pending, ok := b.resolved[id]
	return pending, ok
}

func callbackActorMatchesDecision(pending PendingDecision, actor CallbackActor) bool {
	if actor.TelegramUserID <= 0 || actor.ChatID == 0 || actor.MessageID <= 0 {
		return false
	}
	if pending.ChatID == 0 || pending.ChatID != actor.ChatID {
		return false
	}
	if pending.SenderID > 0 && pending.SenderID != actor.TelegramUserID {
		return false
	}
	if pending.Delivery.MessageID <= 0 || pending.Delivery.MessageID != actor.MessageID {
		return false
	}
	return true
}

func (b *Broker) archiveResolvedDecisionLocked(pending PendingDecision) {
	id := strings.TrimSpace(pending.ID)
	if id == "" {
		return
	}
	if b.resolved == nil {
		b.resolved = make(map[string]PendingDecision)
	}
	if _, exists := b.resolved[id]; !exists {
		b.resolvedOrder = append(b.resolvedOrder, id)
	}
	b.resolved[id] = pending
	for len(b.resolvedOrder) > resolvedDecisionArchiveLimit {
		oldest := b.resolvedOrder[0]
		b.resolvedOrder = b.resolvedOrder[1:]
		delete(b.resolved, oldest)
	}
}
