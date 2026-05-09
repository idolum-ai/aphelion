//go:build linux

package runtime

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func renderOperationProposalMaterializedPromptFallback(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if continuationActionIsPlanLeaseApproval(state) {
		return renderPlanBudgetPromptFallback(state)
	}
	if continuationRequiresEscalatedOperatorApproval(state) {
		return renderEscalatedOperatorApprovalPromptFallback(state)
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	title := continuationApprovalPromptTitle(state)
	if title == "" {
		title = "bounded continuation"
	}
	lines := []string{"Approval: " + title}
	if why := continuationPromptCompactLine(proposal.WhyNow, 220); why != "" {
		lines = append(lines, "", "Why now:", why)
	}
	if scope := continuationApprovalPromptScope(state); scope != "" {
		lines = append(lines, "", "Scope:", scope)
	}
	if included := continuationApprovalPromptIncludedLines(state); len(included) > 0 {
		lines = append(lines, "", "This covers:")
		for _, line := range included {
			lines = append(lines, "- "+line)
		}
	}
	if stops := continuationApprovalPromptStops(state); len(stops) > 0 {
		lines = append(lines, "", "Stops before:", strings.Join(stops, ", "))
	}
	if state.RemainingTurns > 0 {
		turnLabel := "turn"
		if state.RemainingTurns != 1 {
			turnLabel = "turns"
		}
		lines = append(lines, "", fmt.Sprintf("Approve %d bounded %s?", state.RemainingTurns, turnLabel))
	}
	return strings.Join(lines, "\n")
}

func continuationRequiresEscalatedOperatorApproval(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return state.ActionProposal.AutoApproveEligible != nil && !*state.ActionProposal.AutoApproveEligible
}

func renderEscalatedOperatorApprovalPromptFallback(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	title := continuationApprovalPromptTitle(state)
	if title == "" {
		title = "sensitive bounded action"
	}
	lines := []string{"Approval: " + title}
	if why := continuationPromptCompactLine(firstNonEmptyContinuation(proposal.WhyNow, state.GovernorIntent.Rationale), 220); why != "" {
		lines = append(lines, "", "Why I'm asking:", why)
	}
	if scope := continuationApprovalPromptScope(state); scope != "" {
		lines = append(lines, "", "I'll do:", scope)
	}
	if included := continuationEscalatedApprovalAllowedLines(state); len(included) > 0 {
		lines = append(lines, "", "This can use:")
		for _, line := range included {
			lines = append(lines, "- "+line)
		}
	}
	if stops := continuationApprovalPromptStops(state); len(stops) > 0 {
		lines = append(lines, "", "Stops before:", strings.Join(stops, ", "))
	}
	lines = append(lines, "", "Approve this step?")
	return strings.Join(lines, "\n")
}

func continuationEscalatedApprovalAllowedLines(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	values := append([]string(nil), state.ActionProposal.AllowedActions...)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		values = append(values, phase.AllowedActions...)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = strings.ReplaceAll(value, "_", " ")
		value = strings.ReplaceAll(value, "-", " ")
		value = continuationPromptCompactLine(value, 96)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func continuationApprovalPromptTitle(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if summary := continuationPromptCompactLine(phase.Summary, 96); summary != "" {
			return summary
		}
	}
	for _, candidate := range []string{state.ActionProposal.Summary, state.StageSummary, state.Objective} {
		candidate = strings.TrimSpace(candidate)
		if idx := strings.Index(candidate, ":"); strings.HasPrefix(strings.ToLower(candidate), "approve stages ") && idx >= 0 && idx+1 < len(candidate) {
			candidate = strings.TrimSpace(candidate[idx+1:])
		}
		if title := continuationPromptCompactLine(candidate, 96); title != "" {
			return title
		}
	}
	return ""
}

func continuationApprovalPromptScope(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if scope := continuationPromptCompactLine(phase.BoundedEffect, 240); scope != "" {
			return scope
		}
	}
	if scope := continuationPromptCompactLine(state.ActionProposal.BoundedEffect, 260); scope != "" {
		return scope
	}
	return continuationPromptCompactLine(state.GovernorIntent.Constraints, 260)
}

func continuationApprovalPromptIncludedLines(state session.ContinuationState) []string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) < 2 {
		return nil
	}
	lines := make([]string, 0, minStatusInt(len(bundle.Phases), 4))
	for _, phase := range bundle.Phases {
		summary := continuationPromptCompactLine(phase.Summary, 110)
		if summary == "" {
			continue
		}
		if phase.Index > 0 {
			summary = fmt.Sprintf("phase %d: %s", phase.Index, summary)
		}
		lines = append(lines, summary)
		if len(lines) >= 4 {
			break
		}
	}
	return lines
}

func continuationApprovalPromptStops(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		value = planBudgetHumanStop(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range state.ActionProposal.ForbiddenActions {
		add(value)
	}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		for _, value := range phase.ForbiddenActions {
			add(value)
		}
	}
	if len(out) == 0 {
		out = []string{"anything outside scope", "hard gates"}
	}
	out = prioritizePlanBudgetStops(out)
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

func continuationPromptCompactLine(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	return truncatePreview(value, limit)
}

func renderPlanBudgetPromptFallback(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	title := planBudgetPromptTitle(state, proposal)
	lines := []string{"Plan: " + title}
	if goal := firstNonEmptyContinuation(state.Objective, proposal.Summary); goal != "" && goal != title {
		lines = append(lines, "", "Goal: "+continuationPromptCompactLine(goal, 220))
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Budget: up to %d %s", state.RemainingTurns, continuationTurnWord(state.RemainingTurns)))
	}
	if included := planBudgetIncludedLines(state); len(included) > 0 {
		lines = append(lines, "", "I'll do:")
		for _, line := range included {
			lines = append(lines, "- "+line)
		}
	}
	if stops := planBudgetStopLines(state); len(stops) > 0 {
		lines = append(lines, "", "Stops before: "+strings.Join(stops, ", "))
	}
	if first := planBudgetFirstStep(state); first != "" {
		lines = append(lines, "", "First step: "+first)
	}
	lines = append(lines, "", "Anything outside this plan needs a fresh approval.")
	return strings.Join(lines, "\n")
}

func planBudgetPromptTitle(state session.ContinuationState, proposal session.ActionProposal) string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) > 0 {
		if summary := continuationPromptCompactLine(bundle.Phases[0].Summary, 120); summary != "" {
			return summary
		}
	}
	for _, candidate := range []string{state.Objective, proposal.Summary, state.StageSummary} {
		candidate = strings.TrimSpace(candidate)
		lower := strings.ToLower(candidate)
		switch {
		case strings.HasPrefix(lower, "approve plan budget:"):
			if idx := strings.Index(candidate, " for "); idx >= 0 && idx+5 < len(candidate) {
				candidate = strings.TrimSpace(candidate[idx+5:])
			} else if idx := strings.Index(candidate, ":"); idx >= 0 && idx+1 < len(candidate) {
				candidate = strings.TrimSpace(candidate[idx+1:])
			}
		case lower == "approve plan budget":
			candidate = ""
		}
		if title := continuationPromptCompactLine(candidate, 120); title != "" {
			return title
		}
	}
	return "bounded work"
}

func continuationTurnWord(turns int) string {
	if turns == 1 {
		return "turn"
	}
	return "turns"
}

func planBudgetIncludedLines(state session.ContinuationState) []string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) == 0 {
		return nil
	}
	lines := make([]string, 0, len(bundle.Phases))
	for _, phase := range bundle.Phases {
		label := fmt.Sprintf("Step %d", phase.Index)
		if summary := strings.TrimSpace(phase.Summary); summary != "" {
			label += ": " + summary
		}
		if authority := strings.TrimSpace(phase.AuthorityClass); authority != "" {
			if human := planBudgetHumanAuthority(authority); human != "" {
				label += " (" + human + ")"
			}
		}
		lines = append(lines, label)
	}
	return lines
}

func planBudgetHumanAuthority(authority string) string {
	switch normalizeOperationPhaseReasonCode(authority) {
	case "read_only_review":
		return "read-only"
	case "workspace_write":
		return "local workspace"
	case "workspace_commit_then_repo_write_bounded", "git_commit", "commit":
		return "local commit"
	case "public_web_read", "public_profile_metadata_read", "public_account_content_read":
		return "public read"
	case "external_account_auth_status", "read_only_auth_status_check", "credential_metadata", "token_health_check":
		return "account status only"
	case "private_data_intake":
		return "private data"
	case "child_wake":
		return "child wake"
	case "capability_grant":
		return "permission change"
	case "deploy", "restart", "system_change":
		return "release action"
	default:
		authority = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(authority, "_", " "), "-", " "))
		return continuationPromptCompactLine(authority, 64)
	}
}

func planBudgetStopLines(state session.ContinuationState) []string {
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	seen := map[string]struct{}{}
	stops := make([]string, 0, len(proposal.ForbiddenActions))
	for _, value := range proposal.ForbiddenActions {
		value = planBudgetHumanStop(value)
		if value != "" {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			stops = append(stops, value)
		}
	}
	if len(stops) == 0 {
		stops = []string{"anything outside scope", "hard gates", "deploy/restart", "policy or permission changes", "mailbox access or mutation"}
	}
	stops = prioritizePlanBudgetStops(stops)
	if len(stops) > 6 {
		stops = stops[:6]
	}
	return stops
}

func prioritizePlanBudgetStops(stops []string) []string {
	if len(stops) == 0 {
		return nil
	}
	priority := []string{
		"anything outside scope",
		"hard gates",
		"deploy/restart",
		"credentials/tokens",
		"external send/contact",
		"archive/delete",
		"policy or permission changes",
		"mailbox access or mutation",
		"external account/effect",
		"spend",
		"public contact/posting",
		"unapproved autonomous work",
	}
	seen := make(map[string]struct{}, len(stops))
	for _, stop := range stops {
		if stop = strings.TrimSpace(stop); stop != "" {
			seen[stop] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	add := func(stop string) {
		if _, ok := seen[stop]; !ok {
			return
		}
		out = append(out, stop)
		delete(seen, stop)
	}
	for _, stop := range priority {
		add(stop)
	}
	for _, stop := range stops {
		add(strings.TrimSpace(stop))
	}
	return out
}

func planBudgetHumanStop(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "credential") || strings.Contains(value, "token"):
		return "credentials/tokens"
	case strings.Contains(value, "mailbox"):
		return "mailbox access or mutation"
	case strings.Contains(value, "deploy") || strings.Contains(value, "restart"):
		return "deploy/restart"
	case strings.Contains(value, "archive") || strings.Contains(value, "delete") || strings.Contains(value, "mutate source"):
		return "archive/delete"
	case strings.Contains(value, "send") || strings.Contains(value, "contact"):
		return "external send/contact"
	case strings.Contains(value, "hard interrupt"):
		return "hard gates"
	case strings.Contains(value, "lane") || strings.Contains(value, "outside") || strings.Contains(value, "scope") || strings.Contains(value, "budget"):
		return "anything outside scope"
	case strings.Contains(value, "policy") || strings.Contains(value, "grant") || strings.Contains(value, "permission"):
		return "policy or permission changes"
	case strings.Contains(value, "external"):
		return "external account/effect"
	case strings.Contains(value, "purchase") || strings.Contains(value, "spend"):
		return "spend"
	case strings.Contains(value, "public"):
		return "public contact/posting"
	case strings.Contains(value, "autonomous"):
		return "unapproved autonomous work"
	default:
		return value
	}
}

func planBudgetFirstStep(state session.ContinuationState) string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if phase, ok := currentContinuationBundlePhase(bundle); ok {
		return strings.TrimSpace(phase.Summary)
	}
	return strings.TrimSpace(state.StageSummary)
}
