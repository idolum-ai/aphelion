//go:build linux

package decision

import (
	"context"
	"sync"
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

type AutoResolution struct {
	Choice string
	Reason string
}

type AutoResolver func(context.Context, PendingDecision) (AutoResolution, error)

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
	autoResolver  AutoResolver
	loaded        bool
}

type pendingDecision struct {
	request      PendingDecision
	delivery     Delivery
	resultCh     chan string
	ownerKey     string
	exclusiveKey string
	seq          uint64
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

func WithAutoResolver(resolver AutoResolver) BrokerOption {
	return func(b *Broker) {
		b.autoResolver = resolver
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
