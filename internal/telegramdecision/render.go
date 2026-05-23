//go:build linux

package telegramdecision

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/decisionprojection"
	"github.com/idolum-ai/aphelion/telegram"
)

func approvedDecisionConfirmationLabel(kind decision.Kind) string {
	switch kind {
	case decision.KindProposalApproval:
		return "Proposal"
	case decision.KindMemoryDelegation:
		return "Memory delegation"
	case decision.KindSnapshotRestore:
		return "Snapshot restore"
	case decision.KindArtifactRetention:
		return "Artifact retention"
	default:
		return "Approval"
	}
}

func inlineButtonRows(pending decision.PendingDecision) [][]telegram.InlineButton {
	return inlineButtonRowsExpanded(pending, false)
}

func inlineButtonRowsExpanded(pending decision.PendingDecision, expanded bool) [][]telegram.InlineButton {
	if len(pending.Choices) == 0 {
		return nil
	}
	rows := make([][]telegram.InlineButton, 0, 2)
	if strings.TrimSpace(pending.Details) != "" {
		label := "Expand details"
		action := "expand"
		if expanded {
			label = "Hide details"
			action = "collapse"
		}
		rows = append(rows, []telegram.InlineButton{{
			Text:         label,
			CallbackData: decision.EncodeCallbackData(pending.ID, action),
		}})
	}
	row := make([]telegram.InlineButton, 0, len(pending.Choices))
	for _, choice := range orderedDecisionChoices(pending.Choices) {
		row = append(row, telegram.InlineButton{
			Text:         strings.TrimSpace(choice.Label),
			CallbackData: decision.EncodeCallbackData(pending.ID, choice.ID),
		})
	}
	rows = append(rows, row)
	return rows
}

func orderedDecisionChoices(choices []decision.Choice) []decision.Choice {
	out := append([]decision.Choice(nil), choices...)
	if len(out) != 2 {
		return out
	}
	leftID := strings.ToLower(strings.TrimSpace(out[0].ID))
	rightID := strings.ToLower(strings.TrimSpace(out[1].ID))
	if isAffirmativeChoiceID(leftID) && isNegativeChoiceID(rightID) {
		out[0], out[1] = out[1], out[0]
	}
	return out
}

func isNegativeChoiceID(id string) bool {
	switch id {
	case "deny", "stop", "cancel", "reject", "abort":
		return true
	default:
		return false
	}
}

func isAffirmativeChoiceID(id string) bool {
	switch id {
	case "approve", "continue", "queue", "allow", "accept", "yes":
		return true
	default:
		return false
	}
}

func renderPendingDecisionSummary(pending decision.PendingDecision) string {
	prompt := strings.TrimSpace(pending.Prompt)
	summary := strings.TrimSpace(summarizePendingDecision(pending))
	if summary == "" {
		return renderPendingDecisionExpanded(pending)
	}
	if pending.Kind == decision.KindProposalApproval {
		return summary
	}
	if prompt == "" {
		return summary
	}
	return strings.TrimSpace(prompt + "\n\n" + summary)
}

func renderPendingDecisionExpanded(pending decision.PendingDecision) string {
	text := strings.TrimSpace(pending.Prompt)
	if details := strings.TrimSpace(pending.Details); details != "" {
		if text != "" {
			text += "\n\n"
		}
		text += details
	}
	return strings.TrimSpace(text)
}

func summarizePendingDecision(pending decision.PendingDecision) string {
	details := strings.TrimSpace(pending.Details)
	if details == "" {
		return ""
	}
	switch pending.Kind {
	case decision.KindProposalApproval:
		return summarizeProposalApprovalDetails(details)
	case decision.KindArtifactRetention:
		return summarizeArtifactRetentionDetails(details)
	case decision.KindMemoryDelegation:
		return summarizeMemoryDelegationDetails(details)
	case decision.KindSnapshotRestore:
		return summarizeSnapshotRestoreDetails(details)
	default:
		return summarizeGenericDecisionDetails(details)
	}
}

func summarizeProposalApprovalDetails(details string) string {
	return decisionprojection.ProposalApprovalSummary(details)
}

func summarizeArtifactRetentionDetails(details string) string {
	sections := splitDecisionSections(details)
	artifacts := strings.TrimSpace(sections["artifacts"])
	items := []string{}
	for _, line := range strings.Split(artifacts, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if line != "" {
			items = append(items, line)
		}
	}
	if len(items) == 0 {
		return compactSentence(details)
	}
	preview := items[0]
	if len(items) > 1 {
		preview += fmt.Sprintf(" +%d more", len(items)-1)
	}
	return strings.TrimSpace(strings.Join([]string{
		"Choose how long to keep the inbound artifact.",
		"Artifact: " + preview,
		"Use Expand details to inspect the full artifact list.",
	}, "\n"))
}

func summarizeMemoryDelegationDetails(details string) string {
	sections := splitDecisionSections(details)
	agent := firstNonEmpty(sections["agent"])
	why := firstNonEmpty(sections["why now"])
	items := firstNonEmpty(sections["items"])
	itemPreview := ""
	if items != "" {
		for _, line := range strings.Split(items, "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			if line != "" {
				itemPreview = line
				break
			}
		}
	}
	lines := make([]string, 0, 4)
	if agent != "" {
		lines = append(lines, "Memory delegation for "+agent+".")
	} else {
		lines = append(lines, "Memory delegation request.")
	}
	if itemPreview != "" {
		lines = append(lines, "Item: "+compactSentence(itemPreview))
	}
	if why != "" {
		lines = append(lines, compactSentence("Why now: "+why))
	}
	lines = append(lines, "Use Expand details to inspect all delegated items.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func summarizeSnapshotRestoreDetails(details string) string {
	sections := splitDecisionSections(details)
	agent := firstNonEmpty(sections["agent"])
	snapshot := firstNonEmpty(sections["snapshot"])
	reason := firstNonEmpty(sections["reason"])
	lines := make([]string, 0, 4)
	if agent != "" {
		lines = append(lines, "Snapshot restore for "+agent+".")
	} else {
		lines = append(lines, "Durable child snapshot restore request.")
	}
	if snapshot != "" {
		lines = append(lines, "Snapshot: "+snapshot)
	}
	if reason != "" {
		lines = append(lines, compactSentence("Reason: "+reason))
	}
	lines = append(lines, "Use Expand details to inspect restore metadata.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func summarizeGenericDecisionDetails(details string) string {
	return compactSentence(details)
}

func splitDecisionSections(details string) map[string]string {
	out := map[string]string{}
	lines := strings.Split(strings.TrimSpace(details), "\n")
	current := "summary"
	buf := []string{}
	flush := func() {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" && strings.TrimSpace(out[current]) == "" {
			out[current] = text
		}
		buf = buf[:0]
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			if len(buf) > 0 {
				buf = append(buf, "")
			}
			continue
		}
		if strings.HasSuffix(line, ":") {
			flush()
			current = strings.ToLower(strings.TrimSuffix(line, ":"))
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func compactSentence(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	if len(text) <= 220 {
		return text
	}
	cut := text[:220]
	if idx := strings.LastIndex(cut, " "); idx > 80 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut) + "…"
}

func truncateDecisionSummaryText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" || limit <= 0 || len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	cut := text[:limit-3]
	if idx := strings.LastIndex(cut, " "); idx > 40 {
		cut = cut[:idx]
	}
	return strings.TrimSpace(cut) + "..."
}
