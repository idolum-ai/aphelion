//go:build linux

package core

import (
	"strings"
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

func TestDurableAgentContinuityPreservesExplicitConversationMessageID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 13, 13, 0, 0, 123, time.UTC)
	parentMessage := DurableAgentConversationMessage{
		MessageID: "parent-msg-opaque-1",
		Role:      "parent",
		Text:      "Use the parent-provided message identity.",
		CreatedAt: now,
	}
	state := DurableAgentContinuityState{}.WithConversationMessages(parentMessage)
	if state.Conversation == nil || len(state.Conversation.Messages) != 1 {
		t.Fatalf("Conversation = %#v, want one message", state.Conversation)
	}
	if state.Conversation.Messages[0].MessageID != parentMessage.MessageID {
		t.Fatalf("MessageID = %q, want %q", state.Conversation.Messages[0].MessageID, parentMessage.MessageID)
	}

	regeneratedIDs := DurableAgentConversationMessageIDs([]DurableAgentConversationMessage{{
		Role:      parentMessage.Role,
		Text:      parentMessage.Text,
		CreatedAt: parentMessage.CreatedAt,
	}})
	if len(regeneratedIDs) != 1 {
		t.Fatalf("regeneratedIDs len = %d, want 1", len(regeneratedIDs))
	}
	if state.Conversation.Messages[0].MessageID == regeneratedIDs[0] {
		t.Fatalf("MessageID = %q, want preserved opaque id instead of regenerated id", state.Conversation.Messages[0].MessageID)
	}

	raw, err := state.Marshal()
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	parsed, err := ParseDurableAgentContinuityState(raw)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if parsed.Conversation.Messages[0].MessageID != parentMessage.MessageID {
		t.Fatalf("parsed MessageID = %q, want %q", parsed.Conversation.Messages[0].MessageID, parentMessage.MessageID)
	}
	if _, err := parsed.AcknowledgeParentConversationMessageIDs(regeneratedIDs, now.Add(time.Minute)); err == nil {
		t.Fatal("AcknowledgeParentConversationMessageIDs() by regenerated id err = nil, want unknown message_id")
	}
	acknowledged, err := parsed.AcknowledgeParentConversationMessageIDs([]string{parentMessage.MessageID}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("AcknowledgeParentConversationMessageIDs() by preserved id err = %v", err)
	}
	if acknowledged.Conversation.Messages[0].AcknowledgedAt.IsZero() {
		t.Fatal("AcknowledgedAt is zero, want preserved id acknowledgement to update message")
	}

	generated := DurableAgentContinuityState{}.WithConversationMessage("parent", "Generate a deterministic local id.", now)
	if generated.Conversation == nil || len(generated.Conversation.Messages) != 1 {
		t.Fatalf("generated Conversation = %#v, want one message", generated.Conversation)
	}
	if !strings.HasPrefix(generated.Conversation.Messages[0].MessageID, "dcm_") {
		t.Fatalf("generated MessageID = %q, want dcm_ prefix", generated.Conversation.Messages[0].MessageID)
	}
}
