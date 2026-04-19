//go:build linux

package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

var durableWizardEmailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

func rewriteDurableWizardIntent(msg core.InboundMessage, router commandRouter) core.InboundMessage {
	if router == nil {
		return msg
	}
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return msg
	}
	raw := strings.TrimSpace(msg.Text)
	if raw == "" {
		return msg
	}
	if _, ok := parseTelegramCommand(raw); ok {
		return msg
	}
	if !router.CanRestart(msg.SenderID) {
		return msg
	}
	if !looksLikeDurableEmailWizardIntent(raw) {
		return msg
	}

	email := firstDurableWizardEmail(raw)
	msg.Text = durableWizardInstructionText(raw, email)
	return msg
}

func looksLikeDurableEmailWizardIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	hasEmail := strings.Contains(lower, "email") ||
		strings.Contains(lower, "inbox") ||
		strings.Contains(lower, "gmail") ||
		strings.Contains(lower, "imap") ||
		strings.Contains(lower, "mailbox")
	if !hasEmail {
		return false
	}

	hasAction := strings.Contains(lower, "create") ||
		strings.Contains(lower, "make") ||
		strings.Contains(lower, "setup") ||
		strings.Contains(lower, "set up") ||
		strings.Contains(lower, "give you") ||
		strings.Contains(lower, "own")
	if !hasAction {
		return false
	}

	hasDurableSignal := strings.Contains(lower, "durable") ||
		strings.Contains(lower, "agent") ||
		strings.Contains(lower, "bot") ||
		strings.Contains(lower, "child")
	if hasDurableSignal {
		return true
	}

	return strings.Contains(lower, "your own") || strings.Contains(lower, "idolum")
}

func firstDurableWizardEmail(text string) string {
	match := durableWizardEmailPattern.FindString(text)
	return strings.TrimSpace(match)
}

func durableWizardInstructionText(original string, email string) string {
	lines := []string{
		"Start the email durable-agent setup wizard now.",
		"Use ONLY the durable_agent tool for this workflow (wizard_start, wizard_answer, wizard_show, wizard_finalize, connection_test, activate, list).",
		"Do NOT use exec, go run, or any command that may start a Telegram poller.",
		"Ask one concise setup question at a time for missing wizard fields.",
		"When answers are complete, summarize the final charter/policy and ask for explicit confirmation before finalize.",
		"After confirmation: wizard_finalize, connection_test, and activate only when connection_test reports status ok.",
		fmt.Sprintf("Original user request: %s", strings.TrimSpace(original)),
	}
	if email != "" {
		lines = append(lines, fmt.Sprintf("Known value: address=%s account=%s", email, email))
	}
	return strings.Join(lines, "\n")
}
