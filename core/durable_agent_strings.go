//go:build linux

package core

import "strings"

func normalizeDurableAgentStringSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeDurableAgentText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func clampDurableAgentField(value string, max int) string {
	normalized := normalizeDurableAgentText(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= max {
		return normalized
	}
	return string(runes[:max])
}

func firstNonEmptyDurableAgent(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeDurableAgentCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return normalizeDurableAgentStringSet(strings.Split(value, ","))
}
