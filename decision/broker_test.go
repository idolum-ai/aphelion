//go:build linux

package decision

import (
	"context"
	"errors"
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
