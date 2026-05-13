//go:build linux

package core

import (
	"testing"
	"time"
)

func TestDurableAgentContinuityConversationLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Add(-2 * time.Minute)
	state := DurableAgentContinuityState{}
	state = state.WithConversationMessage("parent", "Please keep this child focused on brief replies.", now)
	state = state.WithConversationMessage("child", "Understood; I will keep responses concise.", now.Add(time.Minute))

	pending := state.PendingParentConversationMessages(10)
	if len(pending) != 1 {
		t.Fatalf("PendingParentConversationMessages() len = %d, want 1", len(pending))
	}
	if pending[0].Role != "parent" {
		t.Fatalf("pending role = %q, want parent", pending[0].Role)
	}
	if pending[0].MessageID == "" {
		t.Fatal("pending message_id is empty")
	}
	if pending[0].AcknowledgedAt.IsZero() != true {
		t.Fatalf("pending acknowledged_at = %v, want zero", pending[0].AcknowledgedAt)
	}

	raw, err := state.Marshal()
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	parsed, err := ParseDurableAgentContinuityState(raw)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if parsed.Conversation == nil || len(parsed.Conversation.Messages) != 2 {
		t.Fatalf("parsed conversation = %#v, want 2 messages", parsed.Conversation)
	}

	ackAt := now.Add(90 * time.Second)
	parsed, err = parsed.AcknowledgeParentConversationMessageIDs([]string{pending[0].MessageID}, ackAt)
	if err != nil {
		t.Fatalf("AcknowledgeParentConversationMessageIDs() err = %v", err)
	}
	pending = parsed.PendingParentConversationMessages(10)
	if len(pending) != 0 {
		t.Fatalf("PendingParentConversationMessages() after ack len = %d, want 0", len(pending))
	}
	if parsed.Conversation.Messages[1].Role != "parent" {
		t.Fatalf("oldest role = %q, want parent", parsed.Conversation.Messages[1].Role)
	}
	if parsed.Conversation.Messages[1].AcknowledgedAt.IsZero() {
		t.Fatal("parent message AcknowledgedAt is zero, want non-zero")
	}
}

func TestDurableAgentContinuityAcknowledgesOnlyExplicitParentMessageIDs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	state := DurableAgentContinuityState{}
	state = state.WithConversationMessage("parent", "A", now)
	polled := state.PendingParentConversationMessages(10)
	if len(polled) != 1 || polled[0].MessageID == "" {
		t.Fatalf("initial pending = %#v, want one message with id", polled)
	}
	state = state.WithConversationMessage("parent", "B", now.Add(time.Minute))

	updated, err := state.AcknowledgeParentConversationMessageIDs([]string{polled[0].MessageID}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("AcknowledgeParentConversationMessageIDs() err = %v", err)
	}
	pending := updated.PendingParentConversationMessages(10)
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	if pending[0].Text != "B" {
		t.Fatalf("pending text = %q, want B", pending[0].Text)
	}
	for _, message := range updated.Conversation.Messages {
		if message.Text == "A" && message.AcknowledgedAt.IsZero() {
			t.Fatal("message A remains unacknowledged")
		}
		if message.Text == "B" && !message.AcknowledgedAt.IsZero() {
			t.Fatal("message B was acknowledged without being included in ack batch")
		}
	}
}
