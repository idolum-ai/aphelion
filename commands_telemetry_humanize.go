//go:build linux

package main

import (
	"regexp"
	"strings"
)

var telemetryLabelOverrides = map[string]string{
	"quick_read":        "Quick Read",
	"status_scope":      "Status Scope",
	"debug_scope":       "Debug Scope",
	"debug_chat":        "Debug Chat",
	"debug_system":      "Debug System",
	"current_signal":    "Current Signal",
	"pending_items":     "Pending Items",
	"pending_counts":    "Pending Counts",
	"latest_turn":       "Latest Turn",
	"latest_turns":      "Latest Turns",
	"latest_request":    "Latest Request",
	"last_activity":     "Last Activity",
	"last_tool":         "Last Tool",
	"last_tool_error":   "Last Tool Error",
	"last_tool_result":  "Last Tool Result",
	"last_tool_preview": "Last Tool Preview",
	"last_exec_command": "Last Exec Command",
	"turn_error":        "Turn Error",
}

var telemetryLabelAtLineStartWithColonPattern = regexp.MustCompile(`^(\s*-?\s*)([a-z][a-z0-9_]*_[a-z0-9_]*):`)
var telemetryLabelAtLineStartWithSpacePattern = regexp.MustCompile(`^(\s*-?\s*)([a-z][a-z0-9_]*_[a-z0-9_]*)\s+`)
var telemetryPairPattern = regexp.MustCompile(`\b([a-z][a-z0-9_]*_[a-z0-9_]*)=`)

func humanizeTelegramTelemetryText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = humanizeTelegramTelemetryLine(lines[i])
	}
	return strings.Join(lines, "\n")
}

func humanizeTelegramTelemetryLine(line string) string {
	if strings.TrimSpace(line) == "" {
		return line
	}
	line = telemetryLabelAtLineStartWithColonPattern.ReplaceAllStringFunc(line, func(match string) string {
		parts := telemetryLabelAtLineStartWithColonPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return parts[1] + telemetryDisplayLabel(parts[2]) + ":"
	})
	line = telemetryLabelAtLineStartWithSpacePattern.ReplaceAllStringFunc(line, func(match string) string {
		parts := telemetryLabelAtLineStartWithSpacePattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		return parts[1] + telemetryDisplayLabel(parts[2]) + ": "
	})
	line = telemetryPairPattern.ReplaceAllStringFunc(line, func(match string) string {
		key := strings.TrimSuffix(match, "=")
		return telemetryDisplayLabel(key) + ": "
	})
	return line
}

func telemetryDisplayLabel(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if override, ok := telemetryLabelOverrides[key]; ok {
		return override
	}
	parts := strings.Split(key, "_")
	for i := range parts {
		parts[i] = telemetryWordDisplay(parts[i])
	}
	return strings.Join(parts, " ")
}

func telemetryWordDisplay(word string) string {
	word = strings.TrimSpace(word)
	if word == "" {
		return ""
	}
	switch strings.ToLower(word) {
	case "id":
		return "ID"
	case "ids":
		return "IDs"
	case "api":
		return "API"
	case "pdf":
		return "PDF"
	case "imap":
		return "IMAP"
	case "utc":
		return "UTC"
	default:
		return strings.ToUpper(word[:1]) + word[1:]
	}
}
