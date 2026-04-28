//go:build linux

package main

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

const tailnetCallbackPrefix = "tailnet:"
const tailnetCallbackRefresh = "refresh"

func renderTailnetCommand(snapshot core.TailnetStatusSnapshot) (string, [][]telegram.InlineButton) {
	lines := []string{"Tailnet"}
	status := firstTailnetNonEmpty(snapshot.Status, "unknown")
	lines = append(lines, fmt.Sprintf("Status: %s", status))
	lines = append(lines, fmt.Sprintf("Enabled: %t", snapshot.Enabled))
	lines = append(lines, "Backend: "+firstTailnetNonEmpty(snapshot.Backend, "-"))
	if node := firstTailnetNonEmpty(snapshot.DNSName, snapshot.HostName); node != "" {
		lines = append(lines, "Node: "+node)
	}
	if tailnet := strings.TrimSpace(snapshot.TailnetName); tailnet != "" {
		lines = append(lines, "Tailnet: "+tailnet)
	}
	if len(snapshot.TailscaleIPs) > 0 {
		lines = append(lines, "IPs: "+strings.Join(snapshot.TailscaleIPs, ", "))
	}
	if len(snapshot.Tags) > 0 {
		lines = append(lines, "Tags: "+strings.Join(snapshot.Tags, ", "))
	}
	if snapshot.NetcheckAvailable && strings.TrimSpace(snapshot.NetcheckSummary) != "" {
		lines = append(lines, "Netcheck: "+truncateTailnetLine(snapshot.NetcheckSummary, 180))
	}
	if snapshot.Parent != nil {
		parent := snapshot.Parent
		lines = append(lines, "", "Parent tsnet:")
		lines = append(lines, fmt.Sprintf("- enabled=%t running=%t", parent.Enabled, parent.Running))
		if host := strings.TrimSpace(parent.Hostname); host != "" {
			lines = append(lines, "- hostname: "+host)
		}
		if listen := strings.TrimSpace(parent.ListenAddr); listen != "" {
			lines = append(lines, "- listen: "+listen)
		}
		if magic := strings.TrimSpace(parent.MagicDNSURL); magic != "" {
			lines = append(lines, "- private URL: "+magic)
		}
		if errText := strings.TrimSpace(parent.LastError); errText != "" {
			lines = append(lines, "- error: "+truncateTailnetLine(errText, 220))
		}
	}
	if summary := strings.TrimSpace(snapshot.Summary); summary != "" {
		lines = append(lines, "", "Summary:", "- "+truncateTailnetLine(summary, 220))
	}
	lines = append(lines, "", "Issues:")
	if len(snapshot.Issues) == 0 {
		lines = append(lines, "- none")
	} else {
		limit := len(snapshot.Issues)
		if limit > 6 {
			limit = 6
		}
		for i := 0; i < limit; i++ {
			issue := snapshot.Issues[i]
			lines = append(lines, fmt.Sprintf("- %s/%s: %s", firstTailnetNonEmpty(issue.Severity, "unknown"), firstTailnetNonEmpty(issue.Code, "issue"), truncateTailnetLine(issue.Summary, 220)))
		}
		if len(snapshot.Issues) > limit {
			lines = append(lines, fmt.Sprintf("- %d more issue(s) omitted", len(snapshot.Issues)-limit))
		}
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Refresh", CallbackData: encodeTailnetCallbackData(tailnetCallbackRefresh)},
	}}
	return strings.Join(compactStatusDisplayLines(lines), "\n"), rows
}

func encodeTailnetCallbackData(action string) string {
	return tailnetCallbackPrefix + strings.TrimSpace(action)
}

func decodeTailnetCallbackData(data string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, tailnetCallbackPrefix) {
		return "", false
	}
	action := strings.TrimSpace(strings.TrimPrefix(trimmed, tailnetCallbackPrefix))
	switch action {
	case tailnetCallbackRefresh:
		return action, true
	default:
		return "", false
	}
}

func firstTailnetNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func truncateTailnetLine(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	if max <= 3 {
		return text[:max]
	}
	return strings.TrimSpace(text[:max-3]) + "..."
}
