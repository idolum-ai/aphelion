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

func TestBrokerRequestKeepsPendingDecisionPerDifferentSender(t *testing.T) {
	t.Parallel()

	pendingSeen := make(chan PendingDecision, 2)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 80}, nil
	})

	firstDone := make(chan Result, 1)
	secondDone := make(chan Result, 1)
	go func() {
		result, _ := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        7,
			SenderID:      1001,
			Prompt:        "Sender A",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		firstDone <- result
	}()
	go func() {
		result, _ := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        7,
			SenderID:      1002,
			Prompt:        "Sender B",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		secondDone <- result
	}()

	var firstPending PendingDecision
	var secondPending PendingDecision
	select {
	case firstPending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("did not receive first pending decision")
	}
	select {
	case secondPending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("did not receive second pending decision")
	}
	if firstPending.ID == secondPending.ID {
		t.Fatalf("pending ids are equal: %q", firstPending.ID)
	}

	resolveChoice := func(p PendingDecision) string {
		switch p.SenderID {
		case 1001:
			return "approve"
		case 1002:
			return "deny"
		default:
			t.Fatalf("unexpected sender_id in pending decision: %d", p.SenderID)
			return ""
		}
	}
	if !broker.Resolve(firstPending.ID, resolveChoice(firstPending)) {
		t.Fatal("Resolve(first pending) = false, want true")
	}
	if !broker.Resolve(secondPending.ID, resolveChoice(secondPending)) {
		t.Fatal("Resolve(second pending) = false, want true")
	}

	select {
	case result := <-firstDone:
		if result.Choice != "approve" {
			t.Fatalf("first choice = %q, want approve", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	select {
	case result := <-secondDone:
		if result.Choice != "deny" {
			t.Fatalf("second choice = %q, want deny", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("second request did not finish")
	}
}

func TestBrokerRequestKeepsPendingDecisionForSameSenderInDifferentChats(t *testing.T) {
	t.Parallel()

	pendingSeen := make(chan PendingDecision, 2)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 81}, nil
	})

	firstDone := make(chan Result, 1)
	secondDone := make(chan Result, 1)
	go func() {
		result, _ := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        7,
			SenderID:      42,
			Prompt:        "Chat A",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		firstDone <- result
	}()
	go func() {
		result, _ := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        8,
			SenderID:      42,
			Prompt:        "Chat B",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		secondDone <- result
	}()

	var pendingByChat = map[int64]PendingDecision{}
	for len(pendingByChat) < 2 {
		select {
		case pending := <-pendingSeen:
			pendingByChat[pending.ChatID] = pending
		case <-time.After(time.Second):
			t.Fatal("did not receive both pending decisions")
		}
	}
	firstPending := pendingByChat[7]
	secondPending := pendingByChat[8]
	if firstPending.ID == secondPending.ID {
		t.Fatalf("pending ids are equal: %q", firstPending.ID)
	}

	if !broker.Resolve(firstPending.ID, "approve") {
		t.Fatal("Resolve(first pending) = false, want true")
	}
	if !broker.Resolve(secondPending.ID, "deny") {
		t.Fatal("Resolve(second pending) = false, want true")
	}

	select {
	case result := <-firstDone:
		if result.Choice != "approve" {
			t.Fatalf("first choice = %q, want approve", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not finish")
	}
	select {
	case result := <-secondDone:
		if result.Choice != "deny" {
			t.Fatalf("second choice = %q, want deny", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("second request did not finish")
	}
}

func TestBrokerRequestOlderDeliveryDoesNotSupersedeNewerDecision(t *testing.T) {
	t.Parallel()

	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{}, 1)
	pendingSeen := make(chan PendingDecision, 2)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		if strings.Contains(pending.Prompt, "first") {
			firstStarted <- struct{}{}
			<-releaseFirst
		}
		pendingSeen <- pending
		return Delivery{MessageID: 83}, nil
	})

	firstResultCh := make(chan Result, 1)
	secondResultCh := make(chan Result, 1)
	go func() {
		result, _ := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        9,
			SenderID:      42,
			Prompt:        "first request",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		firstResultCh <- result
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first request did not start before launching second request")
	}

	go func() {
		result, _ := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        9,
			SenderID:      42,
			Prompt:        "second request",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		secondResultCh <- result
	}()

	var secondPending PendingDecision
	select {
	case secondPending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("second request did not publish a pending decision")
	}
	if !strings.Contains(secondPending.Prompt, "second") {
		t.Fatalf("first delivered prompt = %q, want second request to deliver first", secondPending.Prompt)
	}

	close(releaseFirst)

	var firstPending PendingDecision
	select {
	case firstPending = <-pendingSeen:
		if !strings.Contains(firstPending.Prompt, "first") {
			t.Fatalf("second delivered prompt = %q, want first request after release", firstPending.Prompt)
		}
	case <-time.After(50 * time.Millisecond):
		// stale notifier may finish after the request has already been defaulted; that's acceptable
	}

	select {
	case result := <-firstResultCh:
		if result.Choice != "deny" {
			t.Fatalf("first choice = %q, want deny after stale delivery", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not resolve after stale delivery")
	}

	if firstPending.ID != "" {
		if broker.Resolve(firstPending.ID, "approve") {
			t.Fatal("Resolve(first pending) = true, want false after stale delivery")
		}
	}
	if !broker.Resolve(secondPending.ID, "approve") {
		t.Fatal("Resolve(second pending) = false, want true")
	}
	select {
	case result := <-secondResultCh:
		if result.Choice != "approve" {
			t.Fatalf("second choice = %q, want approve", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("second request did not resolve")
	}
}

func TestBrokerRequestNotifierFailureDoesNotSupersedeExistingPendingDecision(t *testing.T) {
	t.Parallel()

	pendingSeen := make(chan PendingDecision, 2)
	notifyCount := 0
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		notifyCount++
		if notifyCount == 2 {
			return Delivery{}, errors.New("send failed")
		}
		pendingSeen <- pending
		return Delivery{MessageID: 82}, nil
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

	_, err := broker.Request(context.Background(), Request{
		Kind:          KindProposalApproval,
		ChatID:        9,
		SenderID:      42,
		Prompt:        "Confirm second?",
		Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
		DefaultChoice: "deny",
		Timeout:       WaitIndefinitely,
	})
	if err == nil || err.Error() != "send failed" {
		t.Fatalf("second Request() err = %v, want send failed", err)
	}

	select {
	case result := <-firstResultCh:
		t.Fatalf("first Request() returned early with %+v, want it to remain pending", result)
	case err := <-firstErrCh:
		t.Fatalf("first Request() err = %v, want it to remain pending", err)
	case <-time.After(25 * time.Millisecond):
	}

	if !broker.Resolve(firstPending.ID, "approve") {
		t.Fatal("Resolve(first pending) = false, want true")
	}
	select {
	case err := <-firstErrCh:
		t.Fatalf("first Request() err = %v, want nil", err)
	case result := <-firstResultCh:
		if result.Choice != "approve" {
			t.Fatalf("first choice = %q, want approve", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("first Request() did not resolve after explicit approval")
	}
}

type brokerMemoryDurableStore struct {
	mu        sync.Mutex
	rows      map[string]DurableDecision
	loadErr   error
	upsertErr error
	deleteErr error
	detachErr error
}

func newBrokerMemoryDurableStore() *brokerMemoryDurableStore {
	return &brokerMemoryDurableStore{
		rows: make(map[string]DurableDecision),
	}
}

func (s *brokerMemoryDurableStore) LoadPending(_ context.Context) ([]DurableDecision, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DurableDecision, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, row)
	}
	return out, nil
}

func (s *brokerMemoryDurableStore) UpsertPending(_ context.Context, pending DurableDecision) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	id := strings.TrimSpace(pending.Pending.ID)
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[id] = pending
	return nil
}

func (s *brokerMemoryDurableStore) DeletePending(_ context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

func (s *brokerMemoryDurableStore) DetachByOwner(_ context.Context, ownerKey string) (int, error) {
	if s.detachErr != nil {
		return 0, s.detachErr
	}
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for id, row := range s.rows {
		if strings.TrimSpace(row.OwnerKey) != ownerKey {
			continue
		}
		delete(s.rows, id)
		count++
	}
	return count, nil
}

func (s *brokerMemoryDurableStore) DetachAll(_ context.Context) (int, error) {
	if s.detachErr != nil {
		return 0, s.detachErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := len(s.rows)
	s.rows = make(map[string]DurableDecision)
	return count, nil
}

func (s *brokerMemoryDurableStore) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.rows[id]
	return ok
}

func (s *brokerMemoryDurableStore) get(id string) (DurableDecision, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.rows[id]
	return value, ok
}

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
