//go:build linux

package decision

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBrokerRequestResolvesChoice(t *testing.T) {
	t.Parallel()

	var broker *Broker
	broker = NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		go func() {
			broker.Resolve(pending.ID, "queue")
		}()
		return Delivery{MessageID: 77}, nil
	})

	result, err := broker.Request(context.Background(), Request{
		Kind:          KindInterrupt,
		ChatID:        7,
		Prompt:        "Still working. What next?",
		Choices:       []Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}},
		DefaultChoice: "queue",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("Request() err = %v", err)
	}
	if result.Choice != "queue" {
		t.Fatalf("choice = %q, want queue", result.Choice)
	}
	if result.Delivery.MessageID != 77 {
		t.Fatalf("delivery = %+v, want message id 77", result.Delivery)
	}
	if result.TimedOut {
		t.Fatal("TimedOut = true, want false")
	}
}

func TestBrokerAutoResolverSkipsNotifierAndEmitsResolved(t *testing.T) {
	t.Parallel()

	var notified bool
	var observed Event
	broker := NewBroker(func(_ context.Context, _ PendingDecision) (Delivery, error) {
		notified = true
		return Delivery{MessageID: 77}, nil
	}, WithAutoResolver(func(_ context.Context, pending PendingDecision) (AutoResolution, error) {
		if pending.Kind != KindProposalApproval {
			return AutoResolution{}, nil
		}
		return AutoResolution{Choice: "approve", Reason: "auto_approved:test"}, nil
	}), WithObserver(func(_ context.Context, event Event) {
		observed = event
	}))

	result, err := broker.Request(context.Background(), Request{
		Kind:          KindProposalApproval,
		ChatID:        7,
		Prompt:        "Approve?",
		Choices:       []Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice: "deny",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("Request() err = %v", err)
	}
	if result.Choice != "approve" || result.Delivery.MessageID != 0 || notified {
		t.Fatalf("result=%#v notified=%v, want auto approve without notifier delivery", result, notified)
	}
	if observed.Type != EventTypeResolved || observed.Choice != "approve" || observed.Reason != "auto_approved:test" {
		t.Fatalf("observed = %#v, want resolved auto-approved event", observed)
	}
}

func TestBrokerAutoResolverSupersedesOlderPendingDecision(t *testing.T) {
	store := newBrokerMemoryDurableStore()
	pendingSeen := make(chan PendingDecision, 1)
	firstResult := make(chan Result, 1)
	firstErr := make(chan error, 1)
	autoApprove := false
	var eventsMu sync.Mutex
	var events []Event

	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 77}, nil
	}, WithDurableStore(store), WithAutoResolver(func(_ context.Context, pending PendingDecision) (AutoResolution, error) {
		if autoApprove && pending.Kind == KindProposalApproval {
			return AutoResolution{Choice: "approve", Reason: "auto_approved:test"}, nil
		}
		return AutoResolution{}, nil
	}), WithObserver(func(_ context.Context, event Event) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, event)
	}))

	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        7,
			SenderID:      1001,
			Prompt:        "Old approval prompt",
			Choices:       []Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		if err != nil {
			firstErr <- err
			return
		}
		firstResult <- result
	}()

	var old PendingDecision
	select {
	case old = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("old request did not become pending")
	}
	if !store.has(old.ID) {
		t.Fatalf("durable store missing old pending decision %q", old.ID)
	}
	autoApprove = true
	result, err := broker.Request(context.Background(), Request{
		Kind:          KindProposalApproval,
		ChatID:        7,
		SenderID:      1001,
		Prompt:        "New auto-approved prompt",
		Choices:       []Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
		DefaultChoice: "deny",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("auto Request() err = %v", err)
	}
	if result.Choice != "approve" {
		t.Fatalf("auto result = %#v, want approve", result)
	}

	select {
	case err := <-firstErr:
		t.Fatalf("first Request() err = %v", err)
	case oldResult := <-firstResult:
		if oldResult.Choice != "deny" {
			t.Fatalf("old result = %#v, want default deny after supersession", oldResult)
		}
	case <-time.After(time.Second):
		t.Fatal("old pending decision was not released after auto-resolve")
	}
	if store.has(old.ID) {
		t.Fatalf("durable store still has old pending decision %q after auto-resolve", old.ID)
	}
	if _, ok := broker.Peek(old.ID); ok {
		t.Fatalf("broker still exposes old pending decision %q", old.ID)
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	var sawDetach, sawResolve bool
	for _, event := range events {
		if event.Decision.ID == old.ID && event.Type == EventTypeDetached && event.Reason == "superseded_by_auto_resolve" {
			sawDetach = true
		}
		if event.Type == EventTypeResolved && event.Choice == "approve" && event.Reason == "auto_approved:test" {
			sawResolve = true
		}
	}
	if !sawDetach || !sawResolve {
		t.Fatalf("events = %#v, want old detach and new auto-resolve", events)
	}
}

func TestBrokerRequestFallsBackToDefaultChoiceOnTimeout(t *testing.T) {
	t.Parallel()

	broker := NewBroker(func(_ context.Context, _ PendingDecision) (Delivery, error) {
		return Delivery{MessageID: 33}, nil
	})

	result, err := broker.Request(context.Background(), Request{
		Kind:          KindProposalApproval,
		ChatID:        7,
		Prompt:        "Confirm command?",
		Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
		DefaultChoice: "deny",
		Timeout:       10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Request() err = %v", err)
	}
	if result.Choice != "deny" {
		t.Fatalf("choice = %q, want deny", result.Choice)
	}
	if !result.TimedOut {
		t.Fatal("TimedOut = false, want true")
	}
	if strings.TrimSpace(result.DecisionID) == "" {
		t.Fatal("DecisionID empty, want generated decision id")
	}
}

func TestBrokerRequestReturnsNotifierError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	broker := NewBroker(func(_ context.Context, _ PendingDecision) (Delivery, error) {
		return Delivery{}, wantErr
	})

	_, err := broker.Request(context.Background(), Request{
		Kind:          KindProposalApproval,
		ChatID:        7,
		Prompt:        "Confirm command?",
		Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
		DefaultChoice: "deny",
		Timeout:       time.Second,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Request() err = %v, want %v", err, wantErr)
	}
}

func TestEncodeDecodeCallbackDataRoundTrip(t *testing.T) {
	t.Parallel()

	data := EncodeCallbackData("abc123", "approve")
	id, choice, ok := DecodeCallbackData(data)
	if !ok {
		t.Fatalf("DecodeCallbackData(%q) ok = false, want true", data)
	}
	if id != "abc123" || choice != "approve" {
		t.Fatalf("DecodeCallbackData(%q) = (%q, %q), want (abc123, approve)", data, id, choice)
	}
}

func TestBrokerRequestWaitsIndefinitelyUntilResolved(t *testing.T) {
	t.Parallel()

	pendingSeen := make(chan PendingDecision, 1)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 44}, nil
	})

	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        7,
			Prompt:        "Confirm command?",
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
		t.Fatal("broker did not publish a pending decision")
	}

	select {
	case result := <-resultCh:
		t.Fatalf("Request() returned early with %+v, want it to keep waiting", result)
	case err := <-errCh:
		t.Fatalf("Request() err = %v, want it to keep waiting", err)
	case <-time.After(25 * time.Millisecond):
	}

	if !broker.Resolve(pending.ID, "approve") {
		t.Fatal("Resolve() = false, want true")
	}

	select {
	case err := <-errCh:
		t.Fatalf("Request() err = %v, want nil", err)
	case result := <-resultCh:
		if strings.TrimSpace(result.DecisionID) == "" {
			t.Fatal("DecisionID empty, want generated decision id")
		}
		if result.Choice != "approve" {
			t.Fatalf("choice = %q, want approve", result.Choice)
		}
		if result.TimedOut {
			t.Fatal("TimedOut = true, want false")
		}
	case <-time.After(time.Second):
		t.Fatal("Request() did not resolve after approval")
	}
}

func TestBrokerObserverReceivesOpenedAndResolvedEvents(t *testing.T) {
	t.Parallel()

	var (
		eventsMu sync.Mutex
		events   []Event
		broker   *Broker
	)
	broker = NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		go broker.Resolve(pending.ID, "queue")
		return Delivery{MessageID: 50}, nil
	}, WithObserver(func(_ context.Context, event Event) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}))

	result, err := broker.Request(context.Background(), Request{
		Kind:          KindInterrupt,
		ChatID:        77,
		SenderID:      1001,
		Prompt:        "Still working. What next?",
		Choices:       []Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}},
		DefaultChoice: "queue",
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("Request() err = %v", err)
	}
	if strings.TrimSpace(result.DecisionID) == "" {
		t.Fatal("DecisionID empty")
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	if len(events) < 2 {
		t.Fatalf("events len = %d, want >= 2", len(events))
	}
	if events[0].Type != EventTypeOpened {
		t.Fatalf("events[0].Type = %q, want opened", events[0].Type)
	}
	if events[0].Decision.ID != result.DecisionID {
		t.Fatalf("opened decision id = %q, want %q", events[0].Decision.ID, result.DecisionID)
	}
	resolvedFound := false
	for _, event := range events {
		if event.Type != EventTypeResolved {
			continue
		}
		resolvedFound = true
		if event.Choice != "queue" {
			t.Fatalf("resolved choice = %q, want queue", event.Choice)
		}
		if event.Reason != "callback" {
			t.Fatalf("resolved reason = %q, want callback", event.Reason)
		}
	}
	if !resolvedFound {
		t.Fatalf("events = %#v, want resolved event", events)
	}
}

func TestBrokerObserverReceivesExpiredEvent(t *testing.T) {
	t.Parallel()

	var (
		eventsMu sync.Mutex
		events   []Event
	)
	broker := NewBroker(func(_ context.Context, _ PendingDecision) (Delivery, error) {
		return Delivery{MessageID: 33}, nil
	}, WithObserver(func(_ context.Context, event Event) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}))

	result, err := broker.Request(context.Background(), Request{
		Kind:          KindProposalApproval,
		ChatID:        7,
		Prompt:        "Confirm command?",
		Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
		DefaultChoice: "deny",
		Timeout:       10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Request() err = %v", err)
	}
	if !result.TimedOut {
		t.Fatal("TimedOut = false, want true")
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	expiredFound := false
	for _, event := range events {
		if event.Type != EventTypeExpired {
			continue
		}
		expiredFound = true
		if !event.TimedOut {
			t.Fatal("expired event TimedOut = false, want true")
		}
		if event.Reason != "timeout" {
			t.Fatalf("expired reason = %q, want timeout", event.Reason)
		}
	}
	if !expiredFound {
		t.Fatalf("events = %#v, want expired event", events)
	}
}

func TestBrokerResolvedEventEmitsAfterOperationalClear(t *testing.T) {
	t.Parallel()

	store := newBrokerMemoryDurableStore()
	pendingSeen := make(chan PendingDecision, 1)
	resolvedHasPending := make(chan bool, 1)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 5150}, nil
	}, WithDurableStore(store), WithObserver(func(_ context.Context, event Event) {
		if event.Type != EventTypeResolved {
			return
		}
		resolvedHasPending <- store.has(event.Decision.ID)
	}))

	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        7,
			SenderID:      1001,
			Prompt:        "Confirm command?",
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
	if !store.has(pending.ID) {
		t.Fatalf("durable store missing pending id=%q before resolve", pending.ID)
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

	select {
	case hasPending := <-resolvedHasPending:
		if hasPending {
			t.Fatal("resolved event observed while durable pending row still existed")
		}
	case <-time.After(time.Second):
		t.Fatal("observer did not receive resolved event")
	}
}

func TestBrokerRequestSupersedesPendingDecisionForSameSender(t *testing.T) {
	t.Parallel()

	pendingSeen := make(chan PendingDecision, 2)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 70}, nil
	})

	firstResultCh := make(chan Result, 1)
	firstErrCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        9,
			SenderID:      42,
			Prompt:        "Confirm first?",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		if err != nil {
			firstErrCh <- err
			return
		}
		firstResultCh <- result
	}()

	var firstPending PendingDecision
	select {
	case firstPending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("first request did not publish a pending decision")
	}

	secondResultCh := make(chan Result, 1)
	secondErrCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        9,
			SenderID:      42,
			Prompt:        "Confirm second?",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		if err != nil {
			secondErrCh <- err
			return
		}
		secondResultCh <- result
	}()

	var secondPending PendingDecision
	select {
	case secondPending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("second request did not publish a pending decision")
	}
	if secondPending.ID == firstPending.ID {
		t.Fatalf("second pending id = %q, want a new decision id", secondPending.ID)
	}

	select {
	case err := <-firstErrCh:
		t.Fatalf("first Request() err = %v, want nil", err)
	case firstResult := <-firstResultCh:
		if firstResult.Choice != "deny" {
			t.Fatalf("first choice = %q, want default deny after supersede", firstResult.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("first Request() did not resolve after second request superseded it")
	}

	if broker.Resolve(firstPending.ID, "approve") {
		t.Fatal("Resolve(first pending) = true, want false after supersede")
	}

	if !broker.Resolve(secondPending.ID, "approve") {
		t.Fatal("Resolve(second pending) = false, want true")
	}
	select {
	case err := <-secondErrCh:
		t.Fatalf("second Request() err = %v, want nil", err)
	case secondResult := <-secondResultCh:
		if secondResult.Choice != "approve" {
			t.Fatalf("second choice = %q, want approve", secondResult.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("second Request() did not resolve")
	}
}
