//go:build linux

package telegram

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeDurableGroupMessageIncludesReplyContextAndReplyTo(t *testing.T) {
	now := time.Now().Unix()
	route := durableGroupRoute{agentID: "family-group", respondOn: "mentions"}
	bot := &User{ID: 42, Username: "idolum_bot"}
	msg := &Message{
		MessageID: 70,
		Date:      now,
		Chat:      &Chat{ID: -100300, Type: "supergroup", Title: "Research"},
		From:      &User{ID: 7, Username: "alice"},
		Text:      "Can you expand this?",
		ReplyToMessage: &Message{
			MessageID: 69,
			From:      &User{ID: 42, Username: "idolum_bot"},
			Text:      "The shortlist has three options with tradeoffs.",
		},
	}

	got := normalizeDurableGroupMessage(msg, route, bot)
	if got == nil {
		t.Fatal("normalizeDurableGroupMessage() = nil, want inbound message")
	}
	if got.ReplyTo == nil || *got.ReplyTo != 69 {
		t.Fatalf("ReplyTo = %#v, want 69", got.ReplyTo)
	}
	if !strings.Contains(got.Text, "Can you expand this?") {
		t.Fatalf("text = %q, want user text", got.Text)
	}
	if !strings.Contains(got.Text, "Reply context:") {
		t.Fatalf("text = %q, want reply context section", got.Text)
	}
	if !strings.Contains(got.Text, "idolum_bot: The shortlist has three options with tradeoffs.") {
		t.Fatalf("text = %q, want quoted reply context", got.Text)
	}
}
