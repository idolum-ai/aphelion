//go:build linux

package decision

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Kind string

const (
	KindInterrupt         Kind = "interrupt"
	KindStopWord          Kind = "stop_word"
	KindProposalApproval  Kind = "proposal_approval"
	KindArtifactRetention Kind = "artifact_retention"
	KindMemoryDelegation  Kind = "memory_delegation"
	KindSnapshotRestore   Kind = "snapshot_restore"
)

const resolvedDecisionArchiveLimit = 128

// WaitIndefinitely disables the broker timeout and waits until the decision is resolved or the request context is canceled.
const WaitIndefinitely time.Duration = -1

type Choice struct {
	ID    string
	Label string
}

type Request struct {
	Kind          Kind
	ChatID        int64
	SenderID      int64
	MessageID     int64
	Prompt        string
	Details       string
	Choices       []Choice
	DefaultChoice string
	Timeout       time.Duration
}

type Delivery struct {
	MessageID int64
}

type Result struct {
	DecisionID string
	Choice     string
	Delivery   Delivery
	TimedOut   bool
}

type PendingDecision struct {
	ID string
	Request
}

type Notifier func(context.Context, PendingDecision) (Delivery, error)

type DurableDecision struct {
	Pending  PendingDecision
	Seq      uint64
	OwnerKey string
	Delivery Delivery
}

type DurableStore interface {
	LoadPending(ctx context.Context) ([]DurableDecision, error)
	UpsertPending(ctx context.Context, pending DurableDecision) error
	DeletePending(ctx context.Context, id string) error
	DetachByOwner(ctx context.Context, ownerKey string) (int, error)
	DetachAll(ctx context.Context) (int, error)
}

type Broker struct {
	mu            sync.Mutex
	nextID        uint64
	notifier      Notifier
	pending       map[string]*pendingDecision
	byOwner       map[string]string
	resolved      map[string]PendingDecision
	resolvedOrder []string
	durable       DurableStore
	observer      Observer
	loaded        bool
}

type pendingDecision struct {
	request  PendingDecision
	delivery Delivery
	resultCh chan string
	ownerKey string
	seq      uint64
}

type EventType string

const (
	EventTypeOpened   EventType = "opened"
	EventTypeResolved EventType = "resolved"
	EventTypeExpired  EventType = "expired"
	EventTypeDetached EventType = "detached"
)

type Event struct {
	Type      EventType
	Decision  PendingDecision
	OwnerKey  string
	Seq       uint64
	Choice    string
	TimedOut  bool
	Reason    string
	CreatedAt time.Time
}

type Observer func(context.Context, Event)

type BrokerOption func(*Broker)

func WithDurableStore(store DurableStore) BrokerOption {
	return func(b *Broker) {
		b.durable = store
	}
}

func WithObserver(observer Observer) BrokerOption {
	return func(b *Broker) {
		b.observer = observer
	}
}

func NewBroker(notifier Notifier, opts ...BrokerOption) *Broker {
	b := &Broker{
		notifier: notifier,
		pending:  make(map[string]*pendingDecision),
		byOwner:  make(map[string]string),
		resolved: make(map[string]PendingDecision),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	if b.durable == nil {
		b.loaded = true
	}
	return b
}

func (b *Broker) Load(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("decision broker is nil")
	}

	b.mu.Lock()
	if b.loaded {
		b.mu.Unlock()
		return nil
	}
	store := b.durable
	b.mu.Unlock()
	if store == nil {
		b.mu.Lock()
		b.loaded = true
		b.mu.Unlock()
		return nil
	}

	persisted, err := store.LoadPending(ctx)
	if err != nil {
		return fmt.Errorf("load pending decisions: %w", err)
	}

	loadedPending := make(map[string]*pendingDecision, len(persisted))
	maxSeq := uint64(0)
	for _, row := range persisted {
		id := strings.TrimSpace(row.Pending.ID)
		if id == "" {
			continue
		}
		req := normalizeRequest(row.Pending.Request)
		if len(req.Choices) == 0 || !containsChoice(req.Choices, req.DefaultChoice) {
			continue
		}
		ownerKey := strings.TrimSpace(row.OwnerKey)
		if ownerKey == "" {
			ownerKey = decisionOwnerKey(req)
		}
		seq := row.Seq
		if seq == 0 {
			if parsed, ok := parseBase36(id); ok {
				seq = parsed
			}
		}
		pending := &pendingDecision{
			request: PendingDecision{
				ID:      id,
				Request: req,
			},
			delivery: row.Delivery,
			resultCh: make(chan string, 1),
			ownerKey: ownerKey,
			seq:      seq,
		}
		loadedPending[id] = pending
		if seq > maxSeq {
			maxSeq = seq
		}
	}

	loadedByOwner := make(map[string]string, len(loadedPending))
	for id, pending := range loadedPending {
		if pending.ownerKey == "" {
			continue
		}
		existingID, ok := loadedByOwner[pending.ownerKey]
		if !ok {
			loadedByOwner[pending.ownerKey] = id
			continue
		}
		existing := loadedPending[existingID]
		if existing == nil || existing.seq < pending.seq {
			loadedByOwner[pending.ownerKey] = id
		}
	}

	staleIDs := make([]string, 0)
	for id, pending := range loadedPending {
		if pending.ownerKey == "" {
			continue
		}
		if loadedByOwner[pending.ownerKey] != id {
			delete(loadedPending, id)
			staleIDs = append(staleIDs, id)
		}
	}

	b.mu.Lock()
	if b.loaded {
		b.mu.Unlock()
		return nil
	}
	b.pending = loadedPending
	b.byOwner = loadedByOwner
	if maxSeq > 0 {
		atomic.StoreUint64(&b.nextID, maxSeq)
	}
	b.loaded = true
	b.mu.Unlock()

	for _, id := range staleIDs {
		_ = store.DeletePending(ctx, id)
	}
	return nil
}

func (b *Broker) ensureLoaded(ctx context.Context) error {
	if b == nil {
		return fmt.Errorf("decision broker is nil")
	}
	b.mu.Lock()
	loaded := b.loaded
	b.mu.Unlock()
	if loaded {
		return nil
	}
	return b.Load(ctx)
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
		resultCh: make(chan string, 1),
		ownerKey: ownerKey,
		seq:      decisionSeq,
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
	if b == nil {
		return false
	}
	if err := b.ensureLoaded(context.Background()); err != nil {
		return false
	}
	id = strings.TrimSpace(id)
	choice = strings.TrimSpace(choice)
	if id == "" || choice == "" {
		return false
	}

	b.mu.Lock()
	pending := b.pending[id]
	b.mu.Unlock()
	if pending == nil {
		return false
	}
	if !containsChoice(pending.request.Choices, choice) {
		return false
	}
	b.mu.Lock()
	select {
	case pending.resultCh <- choice:
		b.archiveResolvedDecisionLocked(pending.request)
		b.mu.Unlock()
		_ = b.clearWithContext(context.Background(), id)
		b.emitEvent(context.Background(), pending, EventTypeResolved, choice, false, "callback")
		return true
	default:
		b.mu.Unlock()
		return false
	}
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

func (b *Broker) DetachByOwner(ctx context.Context, ownerKey string) (int, error) {
	if b == nil {
		return 0, fmt.Errorf("decision broker is nil")
	}
	if err := b.ensureLoaded(ctx); err != nil {
		return 0, err
	}
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return 0, nil
	}

	detached := make([]*pendingDecision, 0, 1)
	b.mu.Lock()
	for id, pending := range b.pending {
		if pending == nil || pending.ownerKey != ownerKey {
			continue
		}
		delete(b.pending, id)
		detached = append(detached, pending)
	}
	delete(b.byOwner, ownerKey)
	store := b.durable
	b.mu.Unlock()

	for _, pending := range detached {
		b.emitEvent(ctx, pending, EventTypeDetached, strings.TrimSpace(pending.request.DefaultChoice), false, "owner_detach")
		resolveDefaultChoice(pending)
	}
	if store == nil {
		return len(detached), nil
	}
	removed, err := store.DetachByOwner(ctx, ownerKey)
	if err != nil {
		return len(detached), err
	}
	if removed > len(detached) {
		return removed, nil
	}
	return len(detached), nil
}

func (b *Broker) DetachAll(ctx context.Context) (int, error) {
	if b == nil {
		return 0, fmt.Errorf("decision broker is nil")
	}
	if err := b.ensureLoaded(ctx); err != nil {
		return 0, err
	}

	detached := make([]*pendingDecision, 0, len(b.pending))
	b.mu.Lock()
	for id, pending := range b.pending {
		if pending == nil {
			continue
		}
		delete(b.pending, id)
		detached = append(detached, pending)
	}
	b.byOwner = make(map[string]string)
	store := b.durable
	b.mu.Unlock()

	for _, pending := range detached {
		b.emitEvent(ctx, pending, EventTypeDetached, strings.TrimSpace(pending.request.DefaultChoice), false, "detach_all")
		resolveDefaultChoice(pending)
	}
	if store == nil {
		return len(detached), nil
	}
	removed, err := store.DetachAll(ctx)
	if err != nil {
		return len(detached), err
	}
	if removed > len(detached) {
		return removed, nil
	}
	return len(detached), nil
}

func (b *Broker) upsertPending(ctx context.Context, pending *pendingDecision) error {
	if b == nil || pending == nil {
		return nil
	}
	b.mu.Lock()
	store := b.durable
	b.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.UpsertPending(ctx, DurableDecision{
		Pending:  pending.request,
		Seq:      pending.seq,
		OwnerKey: pending.ownerKey,
		Delivery: pending.delivery,
	})
}

func (b *Broker) clearWithContext(ctx context.Context, id string) error {
	if b == nil {
		return nil
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	b.mu.Lock()
	pending, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
		if pending.ownerKey != "" {
			if ownerID, exists := b.byOwner[pending.ownerKey]; exists && ownerID == id {
				delete(b.byOwner, pending.ownerKey)
			}
		}
	}
	store := b.durable
	b.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.DeletePending(ctx, id)
}

func (b *Broker) activateOwner(ctx context.Context, id string) bool {
	var staleID string
	var stalePending *pendingDecision
	var supersededID string
	var supersededPending *pendingDecision

	b.mu.Lock()
	pending, ok := b.pending[id]
	if !ok {
		b.mu.Unlock()
		return false
	}
	ownerKey := pending.ownerKey
	if ownerKey == "" {
		b.mu.Unlock()
		return false
	}

	existingID, ok := b.byOwner[ownerKey]
	if !ok || existingID == id {
		b.byOwner[ownerKey] = id
		b.mu.Unlock()
		return false
	}
	existing := b.pending[existingID]
	if existing == nil {
		b.byOwner[ownerKey] = id
		b.mu.Unlock()
		return false
	}
	if existing.seq > pending.seq {
		delete(b.pending, id)
		staleID = id
		stalePending = pending
		b.mu.Unlock()
		b.emitEvent(ctx, stalePending, EventTypeDetached, strings.TrimSpace(stalePending.request.DefaultChoice), false, "stale_superseded")
		select {
		case stalePending.resultCh <- stalePending.request.DefaultChoice:
		default:
		}
		_ = b.clearWithContext(ctx, staleID)
		return true
	}
	delete(b.pending, existingID)
	b.byOwner[ownerKey] = id
	supersededID = existingID
	supersededPending = existing
	b.mu.Unlock()
	b.emitEvent(ctx, supersededPending, EventTypeDetached, strings.TrimSpace(supersededPending.request.DefaultChoice), false, "superseded_by_newer")
	select {
	case supersededPending.resultCh <- supersededPending.request.DefaultChoice:
	default:
	}
	_ = b.clearWithContext(ctx, supersededID)
	return false
}

func (b *Broker) nextDecision() (uint64, string) {
	next := atomic.AddUint64(&b.nextID, 1)
	return next, strconvBase36(next)
}

func decisionOwnerKey(req Request) string {
	if req.ChatID != 0 && req.SenderID != 0 {
		return fmt.Sprintf("chat:%d:sender:%d", req.ChatID, req.SenderID)
	}
	if req.ChatID != 0 {
		return fmt.Sprintf("chat:%d", req.ChatID)
	}
	if req.SenderID != 0 {
		return fmt.Sprintf("sender:%d", req.SenderID)
	}
	return ""
}

func normalizeRequest(req Request) Request {
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Details = strings.TrimSpace(req.Details)
	if req.Timeout == 0 {
		req.Timeout = 30 * time.Second
	}
	return req
}

func containsChoice(choices []Choice, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, choice := range choices {
		if strings.TrimSpace(choice.ID) == id {
			return true
		}
	}
	return false
}

func OwnerKey(chatID int64, senderID int64) string {
	return decisionOwnerKey(Request{ChatID: chatID, SenderID: senderID})
}

func (b *Broker) emitEvent(ctx context.Context, pending *pendingDecision, eventType EventType, choice string, timedOut bool, reason string) {
	if b == nil || pending == nil {
		return
	}
	b.mu.Lock()
	observer := b.observer
	b.mu.Unlock()
	if observer == nil {
		return
	}
	event := Event{
		Type:      eventType,
		Decision:  pending.request,
		OwnerKey:  strings.TrimSpace(pending.ownerKey),
		Seq:       pending.seq,
		Choice:    strings.TrimSpace(choice),
		TimedOut:  timedOut,
		Reason:    strings.TrimSpace(reason),
		CreatedAt: time.Now().UTC(),
	}
	observer(ctx, event)
}

func resolveDefaultChoice(pending *pendingDecision) {
	if pending == nil {
		return
	}
	defaultChoice := strings.TrimSpace(pending.request.DefaultChoice)
	if defaultChoice == "" {
		return
	}
	select {
	case pending.resultCh <- defaultChoice:
	default:
	}
}

func EncodeCallbackData(id string, choice string) string {
	return "decision:" + strings.TrimSpace(id) + ":" + strings.TrimSpace(choice)
}

func DecodeCallbackData(data string) (id string, choice string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, "decision:") {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, ":", 3)
	if len(parts) != 3 || strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), true
}

func strconvBase36(v uint64) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if v == 0 {
		return "0"
	}
	var buf [13]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = digits[int(v%36)]
		v /= 36
	}
	return string(buf[i:])
}

func parseBase36(raw string) (uint64, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, false
	}
	var out uint64
	for _, ch := range raw {
		var digit uint64
		switch {
		case ch >= '0' && ch <= '9':
			digit = uint64(ch - '0')
		case ch >= 'a' && ch <= 'z':
			digit = uint64(ch-'a') + 10
		default:
			return 0, false
		}
		if out > (math.MaxUint64-digit)/36 {
			return 0, false
		}
		out = out*36 + digit
	}
	return out, true
}
