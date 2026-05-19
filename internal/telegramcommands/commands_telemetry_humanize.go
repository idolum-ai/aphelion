//go:build linux

package telegramcommands

import (
	"regexp"
	"strings"
)

var telemetryLabelOverrides = map[string]string{
	"quick_read":        "Quick Read",
	"status_scope":      "Status Scope",
	"debug_scope":       "Trace Scope",
	"debug_chat":        "Trace Chat",
	"debug_system":      "Trace System",
	"trace_scope":       "Trace Scope",
	"summary":           "Summary",
	"current":           "Current",
	"details":           "Details",
	"sources":           "Sources",
	"operation":         "Operation",
	"watchdog":          "Watchdog",
	"effort":            "Effort",
	"queue":             "Queue",
	"continuation":      "Continuation",
	"policy":            "Policy",
	"capacity":          "Capacity",
	"runtime":           "Runtime",
	"enrollment":        "Enrollment",
	"agents":            "Agents",
	"status":            "Status",
	"why":               "Why",
	"now":               "Now",
	"state":             "State",
	"kind":              "Kind",
	"id":                "ID",
	"phase":             "Phase",
	"stage":             "Stage",
	"step":              "Step",
	"signal":            "Signal",
	"active":            "Active",
	"inactive":          "Inactive",
	"dormant":           "Dormant",
	"degraded":          "Degraded",
	"total":             "Total",
	"pending":           "Pending",
	"count":             "Count",
	"latest":            "Latest",
	"triggered":         "Triggered",
	"channel":           "Channel",
	"health":            "Health",
	"outbound":          "Outbound",
	"drift":             "Drift",
	"can":               "Can",
	"cannot":            "Cannot",
	"uncertain":         "Uncertain",
	"success":           "Success",
	"evidence":          "Evidence",
	"version":           "Version",
	"hash":              "Hash",
	"age":               "Age",
	"error":             "Error",
	"request":           "Request",
	"omitted":           "Omitted",
	"current_signal":    "Current Signal",
	"pending_items":     "Pending Items",
	"needs_attention":   "Needs Attention",
	"last_known_work":   "Last Known Work",
	"auto_approval":     "Auto Approval",
	"backlog":           "Backlog",
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

var telemetryLabelAtLineStartWithColonPattern = regexp.MustCompile(`^(\s*-?\s*)([a-z][a-z0-9_]*):`)
var telemetryLabelAtLineStartWithSpacePattern = regexp.MustCompile(`^(\s*-?\s*)([a-z][a-z0-9_]*)\s+`)
var telemetryPairPattern = regexp.MustCompile(`\b([a-z][a-z0-9_]*)=`)
var telemetryBracketTagLinePattern = regexp.MustCompile(`^\s*\[(/?)([A-Z0-9_]+)\]\s*$`)

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
	line = humanizeTelegramBracketTagLine(line)
	if strings.TrimSpace(line) == "" {
		return line
	}
	line = telemetryLabelAtLineStartWithColonPattern.ReplaceAllStringFunc(line, func(match string) string {
		parts := telemetryLabelAtLineStartWithColonPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		if !shouldHumanizeTelemetryLabel(parts[2]) {
			return match
		}
		return parts[1] + telemetryDisplayLabel(parts[2]) + ":"
	})
	line = telemetryLabelAtLineStartWithSpacePattern.ReplaceAllStringFunc(line, func(match string) string {
		parts := telemetryLabelAtLineStartWithSpacePattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		if !shouldHumanizeTelemetryLabel(parts[2]) {
			return match
		}
		return parts[1] + telemetryDisplayLabel(parts[2]) + ": "
	})
	line = telemetryPairPattern.ReplaceAllStringFunc(line, func(match string) string {
		key := strings.TrimSuffix(match, "=")
		if !shouldHumanizeTelemetryLabel(key) {
			return match
		}
		return telemetryDisplayLabel(key) + ": "
	})
	return line
}

func humanizeTelegramBracketTagLine(line string) string {
	parts := telemetryBracketTagLinePattern.FindStringSubmatch(line)
	if len(parts) != 3 {
		return line
	}
	if parts[1] == "/" {
		return ""
	}
	label := telemetryDisplayLabel(strings.ToLower(strings.TrimSpace(parts[2])))
	if label == "" {
		return line
	}
	return label + ":"
}

func shouldHumanizeTelemetryLabel(key string) bool {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return false
	}
	if _, ok := telemetryLabelOverrides[key]; ok {
		return true
	}
	return strings.Contains(key, "_")
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
	case "ai":
		return "AI"
	case "pdf":
		return "PDF"
	case "llm":
		return "LLM"
	case "imap":
		return "IMAP"
	case "openai":
		return "OpenAI"
	case "utc":
		return "UTC"
	default:
		return strings.ToUpper(word[:1]) + word[1:]
	}
}
