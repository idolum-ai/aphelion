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
