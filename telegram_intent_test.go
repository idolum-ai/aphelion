//go:build linux

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestRewriteDurableWizardIntentLeavesNaturalLanguageUnchanged(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{canRestart: true}
	for _, text := range []string{
		"I want to give you your own address as a durable agent",
		"create a durable child agent for triage work",
	} {
		msg := core.InboundMessage{
			ChatID:     7,
			SenderID:   1001,
			SenderName: "admin",
			Text:       text,
		}
		got := rewriteDurableWizardIntent(msg, router)
		if got.Text != msg.Text {
			t.Fatalf("rewriteDurableWizardIntent(%q) = %q, want unchanged", text, got.Text)
		}
	}
}

func TestRewriteDurableWizardIntentDoesNotRewriteNonAdmin(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{canRestart: false}
	msg := core.InboundMessage{
		ChatID:   7,
		SenderID: 1002,
		Text:     "create a durable email agent for me",
	}

	got := rewriteDurableWizardIntent(msg, router)
	if got.Text != msg.Text {
		t.Fatalf("rewriteDurableWizardIntent() = %q, want unchanged %q", got.Text, msg.Text)
	}
}

func TestRewriteDurableWizardIntentDoesNotRewriteSlashCommand(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{canRestart: true}
	msg := core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/status",
	}

	got := rewriteDurableWizardIntent(msg, router)
	if got.Text != msg.Text {
		t.Fatalf("rewriteDurableWizardIntent() = %q, want unchanged slash command %q", got.Text, msg.Text)
	}
}

func TestParseDurableRelayIntentParsesAgentPrefix(t *testing.T) {
	t.Parallel()

	agentID, body, ok := parseDurableRelayIntent("agent:family-group hello there")
	if !ok {
		t.Fatal("parseDurableRelayIntent() ok = false, want true")
	}
	if agentID != "family-group" {
		t.Fatalf("agentID = %q, want family-group", agentID)
	}
	if body != "hello there" {
		t.Fatalf("body = %q, want %q", body, "hello there")
	}
}

func TestRewriteDurableRelayIntentSetsDurableAgentRouting(t *testing.T) {
	t.Parallel()

	msg := core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "agent:family-group can you summarize this?",
	}
	got := rewriteDurableRelayIntent(msg)
	if got.DurableAgentID != "family-group" {
		t.Fatalf("DurableAgentID = %q, want family-group", got.DurableAgentID)
	}
	if got.Text != "can you summarize this?" {
		t.Fatalf("Text = %q, want rewritten relay body", got.Text)
	}
}

func TestRewriteDurableRelayIntentLeavesSlashCommandUntouched(t *testing.T) {
	t.Parallel()

	msg := core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/status",
	}
	got := rewriteDurableRelayIntent(msg)
	if got.DurableAgentID != "" {
		t.Fatalf("DurableAgentID = %q, want empty", got.DurableAgentID)
	}
	if got.Text != msg.Text {
		t.Fatalf("Text = %q, want unchanged %q", got.Text, msg.Text)
	}
}

func TestShouldAllowUnresolvedPrivateDurableRelayMessage(t *testing.T) {
	t.Parallel()

	if !shouldAllowUnresolvedPrivateDurableRelayMessage(&telegram.Message{
		Chat: &telegram.Chat{Type: "private"},
		Text: "agent:ops-child hello",
	}) {
		t.Fatal("shouldAllowUnresolvedPrivateDurableRelayMessage() = false, want true")
	}
	if shouldAllowUnresolvedPrivateDurableRelayMessage(&telegram.Message{
		Chat: &telegram.Chat{Type: "private"},
		Text: "hello",
	}) {
		t.Fatal("shouldAllowUnresolvedPrivateDurableRelayMessage() = true, want false for normal chat")
	}
}

func TestRewriteMemoryFocusInboundPrefixesFocusContext(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		memoryFocusByChat: map[int64]core.MemoryFocus{
			7: {
				Source:  "session_recent",
				ItemID:  "session:12:user",
				Label:   "turn=12 role=user",
				Excerpt: "Can you investigate alternatives for the architecture?",
				Query:   "investigation thread",
				SetAt:   time.Now().UTC(),
			},
		},
	}
	msg := core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "Let's continue with this topic.",
	}
	got := rewriteMemoryFocusInbound(msg, router)
	if got.Text == msg.Text {
		t.Fatalf("rewriteMemoryFocusInbound() text unchanged = %q, want focus-prefixed text", got.Text)
	}
	for _, needle := range []string{
		"MEMORY_FOCUS_CONTEXT",
		"source=session_recent",
		"label=turn=12 role=user",
		"query=investigation thread",
		"Let's continue with this topic.",
	} {
		if !strings.Contains(got.Text, needle) {
			t.Fatalf("rewritten text = %q, want substring %q", got.Text, needle)
		}
	}
}

func TestRewriteMemoryFocusInboundLeavesSlashCommandUntouched(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		memoryFocusByChat: map[int64]core.MemoryFocus{
			7: {
				Source: "session_recent",
				ItemID: "session:12:user",
				Label:  "turn=12 role=user",
			},
		},
	}
	msg := core.InboundMessage{
		ChatID:   7,
		SenderID: 1001,
		Text:     "/status",
	}
	got := rewriteMemoryFocusInbound(msg, router)
	if got.Text != msg.Text {
		t.Fatalf("rewriteMemoryFocusInbound() = %q, want unchanged slash command %q", got.Text, msg.Text)
	}
}
