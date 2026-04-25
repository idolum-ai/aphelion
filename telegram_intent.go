//go:build linux

package main

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/idolum-ai/aphelion/core"
)

type memoryFocusRouter interface {
	MemoryFocus(chatID int64) (core.MemoryFocus, bool)
}

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
	_ = router
	return msg
}

func rewriteMemoryFocusInbound(msg core.InboundMessage, router memoryFocusRouter) core.InboundMessage {
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
	focus, ok := router.MemoryFocus(msg.ChatID)
	if !ok || !focus.Active() {
		return msg
	}
	lines := []string{
		"MEMORY_FOCUS_CONTEXT",
		fmt.Sprintf("source=%s", strings.TrimSpace(string(focus.Source))),
		fmt.Sprintf("label=%s", strings.TrimSpace(focus.Label)),
		fmt.Sprintf("query=%s", strings.TrimSpace(focus.Query)),
	}
	if excerpt := strings.TrimSpace(focus.Excerpt); excerpt != "" {
		lines = append(lines, fmt.Sprintf("excerpt=%s", excerpt))
	}
	lines = append(lines, "Use this memory focus as the active topic unless the user explicitly pivots.", "", raw)
	msg.Text = strings.Join(lines, "\n")
	return msg
}
