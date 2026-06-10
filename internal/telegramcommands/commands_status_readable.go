//go:build linux

package telegramcommands

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/core"
	"strings"
)

func statusReadableSummaryText(ctx context.Context, router commandRouter, facts statusReadableFacts) string {
	facts.View = normalizeStatusReadableFactsView(facts.View)
	if router == nil || !statusViewSupportsReadableSummary(facts.View) {
		return ""
	}
	statusText := facts.providerInput()
	summary := strings.TrimSpace(router.StatusReadableSummary(ctx, string(facts.View), statusText))
	summary = groundStatusReadableSummary(facts, summary)
	if summary == "" {
		summary = composeStatusReadableSummary(facts)
	}
	summary = appendStatusOperationEvidenceSummary(summary, statusOperationEvidenceSummary(facts.OperationEvidence))
	return compactStatusReadableSummary(summary)
}

func compactStatusDisplayLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	lastBlank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if len(out) == 0 || lastBlank {
				continue
			}
			out = append(out, "")
			lastBlank = true
			continue
		}
		out = append(out, line)
		lastBlank = false
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func groundStatusReadableSummary(facts statusReadableFacts, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	expectedState := normalizeReadableState(strings.TrimSpace(facts.State))
	detectedState := strings.TrimSpace(detectSummaryStateWord(summary))
	if expectedState != "" && detectedState != "" && expectedState != detectedState {
		return ""
	}
	lower := strings.ToLower(summary)
	if facts.PendingItems > 0 && (strings.Contains(lower, "no pending") || strings.Contains(lower, "0 pending")) {
		return ""
	}
	if summaryClaimsHumanVisibleDelivery(summary) && strings.TrimSpace(facts.DeliveryStatus) != "" {
		return ""
	}
	return summary
}

func composeStatusReadableSummary(facts statusReadableFacts) string {
	switch normalizeStatusReadableFactsView(facts.View) {
	case statusViewChat, statusViewPending, statusViewChatTarget:
		summary := fmt.Sprintf("Chat is %s; action items %d; backlog items %d; signal %s.", statusSummaryStateDisplay(facts.State), facts.ActionItems, facts.BacklogItems, firstNonEmptyStatusSummary(facts.CurrentSignal, "unknown"))
		if evidence := statusOperationEvidenceSummary(facts.OperationEvidence); evidence != "" {
			summary += " " + evidence
		}
		return summary
	case statusViewSystem:
		return fmt.Sprintf("System has %d active turn(s), %d queued chat(s), and %d pending item(s).", facts.ActiveTurns, facts.QueuedChats, facts.PendingItems)
	case statusViewHotChats:
		return fmt.Sprintf("Hot chats listed: %d.", facts.HotChats)
	case statusViewDurables:
		return fmt.Sprintf("Durables total %d; active %d; degraded %d; inactive %d.", facts.TotalDurables, facts.ActiveDurables, facts.DegradedAgents, facts.InactiveAgents)
	default:
		return ""
	}
}

func normalizeReadableState(state string) string {
	switch strings.TrimSpace(state) {
	case "needs attention":
		return "needs_attention"
	case "needs recovery":
		return "needs_recovery"
	default:
		return strings.ReplaceAll(strings.TrimSpace(state), " ", "_")
	}
}

func statusSummaryStateDisplay(state string) string {
	switch strings.TrimSpace(state) {
	case "needs_recovery":
		return "needs recovery"
	default:
		return strings.TrimSpace(state)
	}
}

func detectSummaryStateWord(summary string) string {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return ""
	}
	for _, state := range []string{"needs_recovery", "needs_attention", "idle", "working", "blocked", "queued", "failed", "interrupted"} {
		if strings.Contains(lower, state) {
			return state
		}
	}
	if strings.Contains(lower, "needs recovery") {
		return "needs_recovery"
	}
	if strings.Contains(lower, "needs attention") {
		return "needs_attention"
	}
	return ""
}

func summaryClaimsHumanVisibleDelivery(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	for _, phrase := range []string{
		"human saw",
		"user saw",
		"you saw",
		"was shown",
		"shown to",
		"displayed to",
		"read it",
		"read the message",
		"received it",
		"delivered to the user",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func firstNonEmptyStatusSummary(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func statusViewSupportsReadableSummary(view statusView) bool {
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget, statusViewSystem, statusViewHotChats, statusViewDurables:
		return true
	default:
		return false
	}
}

func statusOperationEvidenceSummary(statuses []core.OperationEvidenceStatus) string {
	if len(statuses) == 0 {
		return ""
	}
	satisfied := 0
	mismatched := 0
	pending := 0
	var reason string
	for _, status := range statuses {
		if status.Satisfied {
			satisfied++
			continue
		}
		if status.Status == "completed" {
			mismatched++
			if reason == "" {
				reason = strings.TrimSpace(status.Reason)
			}
			continue
		}
		pending++
	}
	if mismatched > 0 {
		if reason != "" {
			return fmt.Sprintf("Operation evidence mismatch %d/%d: %s.", mismatched, len(statuses), reason)
		}
		return fmt.Sprintf("Operation evidence mismatch %d/%d.", mismatched, len(statuses))
	}
	if satisfied > 0 && pending == 0 {
		return fmt.Sprintf("Operation evidence satisfied %d/%d.", satisfied, len(statuses))
	}
	if pending > 0 {
		return fmt.Sprintf("Operation evidence pending %d/%d.", pending, len(statuses))
	}
	return ""
}

func appendStatusOperationEvidenceSummary(summary string, evidence string) string {
	summary = strings.TrimSpace(summary)
	evidence = strings.TrimSpace(evidence)
	if summary == "" || evidence == "" {
		return firstNonEmptyStatusSummary(summary, evidence)
	}
	if strings.Contains(strings.ToLower(summary), "operation evidence") {
		return summary
	}
	reserved := len([]rune(evidence)) + 1
	budget := statusReadableQuickReadMaxChars - reserved
	if budget < 80 {
		budget = 80
	}
	return strings.TrimSpace(compactStatusReadableSummaryLimit(summary, budget) + " " + evidence)
}
