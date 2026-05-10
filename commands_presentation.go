//go:build linux

package main

import "strings"

func truncateOperatorLine(text string, max int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return strings.TrimSpace(text[:max-3]) + "..."
}

func operatorBoolLabel(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
