//go:build linux

package decision

import (
	"context"
	"fmt"
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
)

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
	Choice   string
	Delivery Delivery
	TimedOut bool
}

type PendingDecision struct {
	ID string
	Request
}

type Notifier func(context.Context, PendingDecision) (Delivery, error)

type Broker struct {
	mu       sync.Mutex
	nextID   uint64
	notifier Notifier
	pending  map[string]*pendingDecision
	byOwner  map[string]string
}

type pendingDecision struct {
	request  PendingDecision
	delivery Delivery
	resultCh chan string
	ownerKey string
}

func NewBroker(notifier Notifier) *Broker {
	return &Broker{
		notifier: notifier,
		pending:  make(map[string]*pendingDecision),
		byOwner:  make(map[string]string),
	}
}

func (b *Broker) Request(ctx context.Context, req Request) (Result, error) {
	if b == nil {
		return Result{}, fmt.Errorf("decision broker is nil")
	}
	if len(req.Choices) == 0 {
		return Result{}, fmt.Errorf("decision choices are required")
	}
	if !containsChoice(req.Choices, req.DefaultChoice) {
		return Result{}, fmt.Errorf("default choice %q is not present", req.DefaultChoice)
	}

	decisionID := b.nextDecisionID()
	normalized := normalizeRequest(req)
	ownerKey := decisionOwnerKey(normalized)
	pending := &pendingDecision{
		request: PendingDecision{
			ID:      decisionID,
			Request: normalized,
		},
		resultCh: make(chan string, 1),
		ownerKey: ownerKey,
	}

	b.mu.Lock()
	b.supersedeOwnerLocked(ownerKey)
	b.pending[decisionID] = pending
	if ownerKey != "" {
		b.byOwner[ownerKey] = decisionID
	}
	b.mu.Unlock()

	if b.notifier != nil {
		delivery, err := b.notifier(ctx, pending.request)
		if err != nil {
			b.clear(decisionID)
			return Result{}, err
		}
		pending.delivery = delivery
	}

	timeout := pending.request.Timeout
	if timeout < 0 {
		select {
		case choice := <-pending.resultCh:
			b.clear(decisionID)
			return Result{Choice: choice, Delivery: pending.delivery}, nil
		case <-ctx.Done():
			b.clear(decisionID)
			return Result{}, ctx.Err()
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case choice := <-pending.resultCh:
		b.clear(decisionID)
		return Result{Choice: choice, Delivery: pending.delivery}, nil
	case <-timer.C:
		b.clear(decisionID)
		return Result{Choice: pending.request.DefaultChoice, Delivery: pending.delivery, TimedOut: true}, nil
	case <-ctx.Done():
		b.clear(decisionID)
		return Result{}, ctx.Err()
	}
}

func (b *Broker) Resolve(id string, choice string) bool {
	if b == nil {
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
	select {
	case pending.resultCh <- choice:
		return true
	default:
		return false
	}
}

func (b *Broker) clear(id string) {
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
	b.mu.Unlock()
}

func (b *Broker) supersedeOwnerLocked(ownerKey string) {
	if ownerKey == "" {
		return
	}
	existingID, ok := b.byOwner[ownerKey]
	if !ok {
		return
	}
	existing := b.pending[existingID]
	if existing != nil {
		// Resolve the previous pending decision to its default so only one remains active per owner.
		select {
		case existing.resultCh <- existing.request.DefaultChoice:
		default:
		}
		delete(b.pending, existingID)
	}
	delete(b.byOwner, ownerKey)
}

func (b *Broker) nextDecisionID() string {
	next := atomic.AddUint64(&b.nextID, 1)
	return strconvBase36(next)
}

func decisionOwnerKey(req Request) string {
	if req.SenderID != 0 {
		return fmt.Sprintf("sender:%d", req.SenderID)
	}
	if req.ChatID != 0 {
		return fmt.Sprintf("chat:%d", req.ChatID)
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
