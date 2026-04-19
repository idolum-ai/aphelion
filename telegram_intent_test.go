//go:build linux

package main

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestRewriteDurableWizardIntentRewritesAdminNaturalLanguage(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{canRestart: true}
	msg := core.InboundMessage{
		ChatID:     7,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I want to give you your own email address: idolum@example.com as a durable agent",
	}

	got := rewriteDurableWizardIntent(msg, router)
	if got.Text == msg.Text {
		t.Fatalf("rewriteDurableWizardIntent() text unchanged = %q, want rewritten wizard instruction", got.Text)
	}
	for _, needle := range []string{
		"Start the email durable-agent setup wizard now.",
		"Use ONLY the durable_agent tool",
		"Do NOT use exec",
		"Original user request:",
		"Known value: address=idolum@example.com account=idolum@example.com",
	} {
		if !strings.Contains(got.Text, needle) {
			t.Fatalf("rewritten text = %q, want substring %q", got.Text, needle)
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
