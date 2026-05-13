//go:build linux

package decision

import (
	"context"
	"testing"
	"time"
)

func TestBrokerRequestPersistsAndClearsDurablePendingDecision(t *testing.T) {
	t.Parallel()

	store := newBrokerMemoryDurableStore()
	pendingSeen := make(chan PendingDecision, 1)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 98}, nil
	}, WithDurableStore(store))

	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        9,
			SenderID:      42,
			Prompt:        "Confirm durable approval?",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	var pending PendingDecision
	select {
	case pending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("request did not publish a pending decision")
	}

	waitFor := func(deadline time.Duration, check func() bool) bool {
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		for {
			if check() {
				return true
			}
			select {
			case <-timer.C:
				return false
			case <-tick.C:
			}
		}
	}

	if !waitFor(100*time.Millisecond, func() bool {
		durable, ok := store.get(pending.ID)
		return ok && durable.Delivery.MessageID == 98
	}) {
		t.Fatalf("durable store does not contain delivered pending decision id=%q", pending.ID)
	}

	if !broker.Resolve(pending.ID, "approve") {
		t.Fatal("Resolve() = false, want true")
	}

	select {
	case err := <-errCh:
		t.Fatalf("Request() err = %v, want nil", err)
	case result := <-resultCh:
		if result.Choice != "approve" {
			t.Fatalf("choice = %q, want approve", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("Request() did not resolve after approval")
	}

	if !waitFor(100*time.Millisecond, func() bool { return !store.has(pending.ID) }) {
		t.Fatalf("durable store still contains id=%q after resolve", pending.ID)
	}
}

func TestBrokerResolveClearsDurableDecisionLoadedAfterRestart(t *testing.T) {
	t.Parallel()

	store := newBrokerMemoryDurableStore()
	store.rows["durable-1"] = DurableDecision{
		Pending: PendingDecision{
			ID: "durable-1",
			Request: Request{
				Kind:          KindProposalApproval,
				ChatID:        7,
				SenderID:      42,
				Prompt:        "Confirm restart-loaded approval?",
				Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
				DefaultChoice: "deny",
				Timeout:       WaitIndefinitely,
			},
		},
		Seq:      9,
		OwnerKey: "chat:7:sender:42",
		Delivery: Delivery{MessageID: 7001},
	}

	broker := NewBroker(nil, WithDurableStore(store))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if _, ok := broker.Peek("durable-1"); !ok {
		t.Fatal("Peek() did not return loaded durable decision")
	}
	if !broker.Resolve("durable-1", "approve") {
		t.Fatal("Resolve() = false, want true for loaded decision")
	}
	if _, ok := broker.Peek("durable-1"); ok {
		t.Fatal("Peek() = true after resolve, want false")
	}
	if store.has("durable-1") {
		t.Fatal("durable store still contains resolved decision")
	}
}

func TestBrokerLoadKeepsOnlyNewestPendingDecisionPerOwner(t *testing.T) {
	t.Parallel()

	store := newBrokerMemoryDurableStore()
	store.rows["older"] = DurableDecision{
		Pending: PendingDecision{
			ID: "older",
			Request: Request{
				Kind:          KindProposalApproval,
				ChatID:        7,
				SenderID:      11,
				Prompt:        "Older request",
				Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
				DefaultChoice: "deny",
				Timeout:       WaitIndefinitely,
			},
		},
		Seq:      10,
		OwnerKey: "chat:7:sender:11",
		Delivery: Delivery{MessageID: 10},
	}
	store.rows["newer"] = DurableDecision{
		Pending: PendingDecision{
			ID: "newer",
			Request: Request{
				Kind:          KindProposalApproval,
				ChatID:        7,
				SenderID:      11,
				Prompt:        "Newer request",
				Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
				DefaultChoice: "deny",
				Timeout:       WaitIndefinitely,
			},
		},
		Seq:      11,
		OwnerKey: "chat:7:sender:11",
		Delivery: Delivery{MessageID: 11},
	}

	broker := NewBroker(nil, WithDurableStore(store))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if _, ok := broker.Peek("older"); ok {
		t.Fatal("Peek(older) = true, want false after supersede on load")
	}
	if _, ok := broker.Peek("newer"); !ok {
		t.Fatal("Peek(newer) = false, want true")
	}
	if store.has("older") {
		t.Fatal("durable store still contains stale older decision")
	}
}

func TestBrokerLoadKeepsPendingDecisionsForDifferentKindsSameOwner(t *testing.T) {
	t.Parallel()

	store := newBrokerMemoryDurableStore()
	store.rows["proposal"] = DurableDecision{
		Pending: PendingDecision{
			ID: "proposal",
			Request: Request{
				Kind:          KindProposalApproval,
				ChatID:        7,
				SenderID:      11,
				Prompt:        "Approve repo commit?",
				Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
				DefaultChoice: "deny",
				Timeout:       WaitIndefinitely,
			},
		},
		Seq:      10,
		OwnerKey: "chat:7:sender:11",
		Delivery: Delivery{MessageID: 10},
	}
	store.rows["interrupt"] = DurableDecision{
		Pending: PendingDecision{
			ID: "interrupt",
			Request: Request{
				Kind:          KindInterrupt,
				ChatID:        7,
				SenderID:      11,
				Prompt:        "Queue unrelated message?",
				Choices:       []Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}},
				DefaultChoice: "queue",
				Timeout:       WaitIndefinitely,
			},
		},
		Seq:      11,
		OwnerKey: "chat:7:sender:11",
		Delivery: Delivery{MessageID: 11},
	}

	broker := NewBroker(nil, WithDurableStore(store))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if _, ok := broker.Peek("proposal"); !ok {
		t.Fatal("Peek(proposal) = false, want proposal retained")
	}
	if _, ok := broker.Peek("interrupt"); !ok {
		t.Fatal("Peek(interrupt) = false, want interrupt retained")
	}
	if !store.has("proposal") || !store.has("interrupt") {
		t.Fatalf("durable store lost decisions: proposal=%v interrupt=%v", store.has("proposal"), store.has("interrupt"))
	}
}

func TestBrokerDetachByOwnerClearsPendingAndUnblocksWaiters(t *testing.T) {
	t.Parallel()

	store := newBrokerMemoryDurableStore()
	pendingSeen := make(chan PendingDecision, 1)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 901}, nil
	}, WithDurableStore(store))

	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        77,
			SenderID:      88,
			Prompt:        "Detach me",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	var pending PendingDecision
	select {
	case pending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("request did not publish pending decision")
	}
	if _, ok := broker.Peek(pending.ID); !ok {
		t.Fatal("Peek() = false, want pending decision")
	}
	count, err := broker.DetachByOwner(context.Background(), "chat:77:sender:88")
	if err != nil {
		t.Fatalf("DetachByOwner() err = %v", err)
	}
	if count != 1 {
		t.Fatalf("DetachByOwner() count = %d, want 1", count)
	}
	select {
	case err := <-errCh:
		t.Fatalf("Request() err = %v", err)
	case result := <-resultCh:
		if result.Choice != "deny" {
			t.Fatalf("result choice = %q, want default deny", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("Request() did not resolve after detach")
	}
	if _, ok := broker.Peek(pending.ID); ok {
		t.Fatal("Peek() = true after detach, want false")
	}
	if store.has(pending.ID) {
		t.Fatalf("durable store still contains detached id=%q", pending.ID)
	}
}

func TestBrokerDetachAllClearsEverything(t *testing.T) {
	t.Parallel()

	store := newBrokerMemoryDurableStore()
	store.rows["d1"] = DurableDecision{
		Pending: PendingDecision{
			ID: "d1",
			Request: Request{
				Kind:          KindProposalApproval,
				ChatID:        1,
				SenderID:      10,
				Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
				DefaultChoice: "deny",
				Timeout:       WaitIndefinitely,
			},
		},
		Seq:      1,
		OwnerKey: "chat:1:sender:10",
	}
	store.rows["d2"] = DurableDecision{
		Pending: PendingDecision{
			ID: "d2",
			Request: Request{
				Kind:          KindProposalApproval,
				ChatID:        2,
				SenderID:      20,
				Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
				DefaultChoice: "deny",
				Timeout:       WaitIndefinitely,
			},
		},
		Seq:      2,
		OwnerKey: "chat:2:sender:20",
	}

	broker := NewBroker(nil, WithDurableStore(store))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	count, err := broker.DetachAll(context.Background())
	if err != nil {
		t.Fatalf("DetachAll() err = %v", err)
	}
	if count != 2 {
		t.Fatalf("DetachAll() count = %d, want 2", count)
	}
	if _, ok := broker.Peek("d1"); ok {
		t.Fatal("Peek(d1) = true after DetachAll, want false")
	}
	if _, ok := broker.Peek("d2"); ok {
		t.Fatal("Peek(d2) = true after DetachAll, want false")
	}
	if store.has("d1") || store.has("d2") {
		t.Fatalf("durable store rows still present after DetachAll: %#v", store.rows)
	}
}
