//go:build linux

package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/idolum-ai/aphelion/core"
)

var durableWizardAddressPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

func rewriteDurableRelayIntent(msg core.InboundMessage) core.InboundMessage {
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
	agentID, body, ok := parseDurableRelayIntent(raw)
	if !ok {
		return msg
	}
	msg.DurableAgentID = agentID
	msg.Text = body
	if strings.TrimSpace(msg.Text) == "" && len(msg.Artifacts) == 0 {
		msg.Text = "continue"
	}
	return msg
}

func parseDurableRelayIntent(text string) (agentID string, body string, ok bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(trimmed), "agent:") {
		return "", "", false
	}
	rest := strings.TrimSpace(trimmed[len("agent:"):])
	if rest == "" {
		return "", "", false
	}
	first, remaining, found := splitFirstToken(rest)
	if !found {
		return "", "", false
	}
	first = strings.TrimSpace(first)
	if !isValidDurableRelayAgentID(first) {
		return "", "", false
	}
	return first, strings.TrimSpace(remaining), true
}

func splitFirstToken(text string) (first string, remaining string, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", false
	}
	if idx := strings.IndexFunc(text, unicode.IsSpace); idx >= 0 {
		return text[:idx], strings.TrimSpace(text[idx:]), true
	}
	return text, "", true
}

func isValidDurableRelayAgentID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

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
	if !looksLikeDurableWizardIntent(raw) {
		return msg
	}

	email := firstDurableWizardEmail(raw)
	msg.Text = durableWizardInstructionText(raw, email)
	return msg
}

func looksLikeDurableWizardIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}

	hasInboxSignal := strings.Contains(lower, "email") ||
		strings.Contains(lower, "inbox") ||
		strings.Contains(lower, "gmail") ||
		strings.Contains(lower, "imap") ||
		strings.Contains(lower, "mailbox")
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
	if hasDurableSignal && (hasInboxSignal ||
		strings.Contains(lower, "durable agent") ||
		strings.Contains(lower, "durable child")) {
		return true
	}

	return hasInboxSignal && (strings.Contains(lower, "your own") || strings.Contains(lower, "idolum"))
}

func firstDurableWizardEmail(text string) string {
	match := durableWizardAddressPattern.FindString(text)
	return strings.TrimSpace(match)
}

func durableWizardInstructionText(original string, email string) string {
	lines := []string{
		"Start the durable-child setup wizard now.",
		"Default to the inbox profile (email adapter) unless the user explicitly asks for another channel profile.",
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
