//go:build linux

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

func TestTelegramDecisionDurableStoreRoundTrip(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "decision-durable.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	durable := newTelegramDecisionDurableStore(store)
	if durable == nil {
		t.Fatal("newTelegramDecisionDurableStore() = nil, want non-nil")
	}

	pending := decision.DurableDecision{
		Pending: decision.PendingDecision{
			ID: "decision-1",
			Request: decision.Request{
				Kind:          decision.KindProposalApproval,
				ChatID:        7,
				SenderID:      42,
				MessageID:     91,
				Prompt:        "Approve this proposal?",
				Details:       "Install one dependency.",
				Choices:       []decision.Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}},
				DefaultChoice: "deny",
				Timeout:       decision.WaitIndefinitely,
			},
		},
		Seq:      12,
		OwnerKey: "chat:7:sender:42",
		Delivery: decision.Delivery{MessageID: 5001},
	}

	if err := durable.UpsertPending(context.Background(), pending); err != nil {
		t.Fatalf("UpsertPending() err = %v", err)
	}

	loaded, err := durable.LoadPending(context.Background())
	if err != nil {
		t.Fatalf("LoadPending() err = %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded len = %d, want 1", len(loaded))
	}
	got := loaded[0]
	if got.Pending.ID != pending.Pending.ID {
		t.Fatalf("id = %q, want %q", got.Pending.ID, pending.Pending.ID)
	}
	if got.Seq != pending.Seq {
		t.Fatalf("seq = %d, want %d", got.Seq, pending.Seq)
	}
	if got.OwnerKey != pending.OwnerKey {
		t.Fatalf("owner = %q, want %q", got.OwnerKey, pending.OwnerKey)
	}
	if got.Delivery.MessageID != pending.Delivery.MessageID {
		t.Fatalf("delivery message id = %d, want %d", got.Delivery.MessageID, pending.Delivery.MessageID)
	}
	if got.Pending.Timeout != pending.Pending.Timeout {
		t.Fatalf("timeout = %v, want %v", got.Pending.Timeout, pending.Pending.Timeout)
	}
	if len(got.Pending.Choices) != 2 {
		t.Fatalf("choices len = %d, want 2", len(got.Pending.Choices))
	}

	if err := durable.DeletePending(context.Background(), pending.Pending.ID); err != nil {
		t.Fatalf("DeletePending() err = %v", err)
	}
	loaded, err = durable.LoadPending(context.Background())
	if err != nil {
		t.Fatalf("LoadPending(after delete) err = %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("loaded len after delete = %d, want 0", len(loaded))
	}
}
