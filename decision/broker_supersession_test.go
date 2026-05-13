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

func TestBrokerDifferentDecisionKindsDoNotSupersedeSameSender(t *testing.T) {
	t.Parallel()

	pendingSeen := make(chan PendingDecision, 2)
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		pendingSeen <- pending
		return Delivery{MessageID: 90}, nil
	})

	proposalResultCh := make(chan Result, 1)
	proposalErrCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindProposalApproval,
			ChatID:        9,
			SenderID:      42,
			Prompt:        "Approve repo commit?",
			Choices:       []Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
			DefaultChoice: "deny",
			Timeout:       WaitIndefinitely,
		})
		if err != nil {
			proposalErrCh <- err
			return
		}
		proposalResultCh <- result
	}()

	var proposalPending PendingDecision
	select {
	case proposalPending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("proposal request did not publish pending decision")
	}

	interruptResultCh := make(chan Result, 1)
	interruptErrCh := make(chan error, 1)
	go func() {
		result, err := broker.Request(context.Background(), Request{
			Kind:          KindInterrupt,
			ChatID:        9,
			SenderID:      42,
			Prompt:        "Queue unrelated message?",
			Choices:       []Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}},
			DefaultChoice: "queue",
			Timeout:       WaitIndefinitely,
		})
		if err != nil {
			interruptErrCh <- err
			return
		}
		interruptResultCh <- result
	}()

	var interruptPending PendingDecision
	select {
	case interruptPending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("interrupt request did not publish pending decision")
	}
	if interruptPending.ID == proposalPending.ID {
		t.Fatalf("interrupt pending id = %q, want distinct id", interruptPending.ID)
	}

	select {
	case result := <-proposalResultCh:
		t.Fatalf("proposal resolved early with %+v, want it to stay pending while interrupt is active", result)
	case err := <-proposalErrCh:
		t.Fatalf("proposal errored early with %v, want it to stay pending", err)
	case <-time.After(25 * time.Millisecond):
	}
	if _, ok := broker.Peek(proposalPending.ID); !ok {
		t.Fatal("proposal was dropped after unrelated interrupt decision")
	}

	if !broker.Resolve(interruptPending.ID, "queue") {
		t.Fatal("Resolve(interrupt) = false, want true")
	}
	select {
	case err := <-interruptErrCh:
		t.Fatalf("interrupt Request() err = %v, want nil", err)
	case result := <-interruptResultCh:
		if result.Choice != "queue" {
			t.Fatalf("interrupt choice = %q, want queue", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("interrupt Request() did not resolve")
	}

	if !broker.Resolve(proposalPending.ID, "approve") {
		t.Fatal("Resolve(proposal) = false, want true after unrelated interrupt")
	}
	select {
	case err := <-proposalErrCh:
		t.Fatalf("proposal Request() err = %v, want nil", err)
	case result := <-proposalResultCh:
		if result.Choice != "approve" {
			t.Fatalf("proposal choice = %q, want approve", result.Choice)
		}
	case <-time.After(time.Second):
		t.Fatal("proposal Request() did not resolve after approval")
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
	detachedCounts := make(map[string]int)
	var detachedMu sync.Mutex
	broker := NewBroker(func(_ context.Context, pending PendingDecision) (Delivery, error) {
		if strings.Contains(pending.Prompt, "first") {
			firstStarted <- struct{}{}
			<-releaseFirst
		}
		pendingSeen <- pending
		return Delivery{MessageID: 83}, nil
	}, WithObserver(func(_ context.Context, event Event) {
		if event.Type != EventTypeDetached {
			return
		}
		detachedMu.Lock()
		detachedCounts[event.Decision.ID]++
		detachedMu.Unlock()
	}))

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
		detachedMu.Lock()
		gotDetached := detachedCounts[result.DecisionID]
		detachedMu.Unlock()
		if gotDetached != 1 {
			t.Fatalf("detached events for first stale decision = %d, want 1", gotDetached)
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
