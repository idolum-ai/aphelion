//go:build linux

package runtime

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/internal/stoplabels"
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
	title := continuationApprovalPromptTitle(state)
	if title == "" {
		title = "bounded continuation"
	}
	action := continuationApprovalPromptScope(state)
	if action == "" {
		action = title
	}
	card := newContinuationApprovalPromptCard("Approve", title, state.RemainingTurns)
	card.addSection("Scope", action)
	card.addListSection("Covers", continuationApprovalPromptIncludedLines(state))
	card.addListSectionWithLimit("Requires", continuationRequiredCapabilityLines(state), 4, 220)
	card.addListSection("Stops before", continuationApprovalPromptStops(state))
	return card.String()
}

type continuationApprovalPromptCard struct {
	heading  string
	sections []string
}

func newContinuationApprovalPromptCard(action string, title string, turns int) *continuationApprovalPromptCard {
	action = strings.TrimSpace(action)
	if action == "" {
		action = "Approve"
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "bounded continuation"
	}
	heading := action + ":\n" + title
	if turns > 0 {
		heading += fmt.Sprintf("\n\nBudget:\nup to %d %s", turns, continuationTurnWord(turns))
	}
	return &continuationApprovalPromptCard{heading: heading}
}

func (c *continuationApprovalPromptCard) addSection(label string, value string) {
	if c == nil {
		return
	}
	label = strings.TrimSpace(label)
	value = continuationPromptCompactLine(value, 360)
	if label == "" || value == "" {
		return
	}
	c.sections = append(c.sections, label+":\n"+value)
}

func (c *continuationApprovalPromptCard) addListSection(label string, values []string) {
	c.addListSectionWithLimit(label, values, 6, 140)
}

func (c *continuationApprovalPromptCard) addListSectionWithLimit(label string, values []string, limit int, itemLimit int) {
	if c == nil {
		return
	}
	label = strings.TrimSpace(label)
	items := continuationPromptBulletItems(values, limit, itemLimit)
	if label == "" || len(items) == 0 {
		return
	}
	c.sections = append(c.sections, label+":\n- "+strings.Join(items, "\n- "))
}

func (c *continuationApprovalPromptCard) String() string {
	if c == nil {
		return ""
	}
	parts := []string{strings.TrimSpace(c.heading)}
	for _, section := range c.sections {
		if section = strings.TrimSpace(section); section != "" {
			parts = append(parts, section)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func continuationPromptBulletItems(values []string, limit int, itemLimit int) []string {
	if limit <= 0 {
		limit = len(values)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, minStatusInt(len(values), limit))
	for _, value := range values {
		value = continuationPromptCompactLine(value, itemLimit)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func continuationRequiresEscalatedOperatorApproval(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return state.ActionProposal.AutoApproveEligible != nil && !*state.ActionProposal.AutoApproveEligible
}

func renderEscalatedOperatorApprovalPromptFallback(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	title := continuationApprovalPromptTitle(state)
	if title == "" {
		title = "sensitive bounded action"
	}
	action := continuationApprovalPromptScope(state)
	if action == "" {
		action = title
	}
	card := newContinuationApprovalPromptCard("Approve", title, state.RemainingTurns)
	card.addSection("Scope", action)
	card.addListSection("Can use", continuationEscalatedApprovalAllowedLines(state))
	card.addListSectionWithLimit("Requires", continuationRequiredCapabilityLines(state), 4, 220)
	card.addListSection("Stops before", continuationApprovalPromptStops(state))
	return card.String()
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

func continuationRequiredCapabilityLines(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	specs := append([]session.CapabilityGrantSpec(nil), state.ContinuationLease.RequiredCapabilityGrants...)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		specs = append(specs, phase.RequiredCapabilityGrants...)
	}
	if len(specs) == 0 {
		return nil
	}
	lines := make([]string, 0, minStatusInt(len(specs), 4))
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		spec = session.NormalizeCapabilityGrantSpec(spec)
		line := capabilityGrantSpecPromptLine(spec)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		lines = append(lines, line)
		if len(lines) >= 4 {
			break
		}
	}
	return lines
}

func capabilityGrantSpecPromptLine(spec session.CapabilityGrantSpec) string {
	spec = session.NormalizeCapabilityGrantSpec(spec)
	parts := make([]string, 0, 5)
	if spec.Kind != "" {
		parts = append(parts, strings.TrimSpace(string(spec.Kind)))
	}
	if spec.TargetResource != "" {
		parts = append(parts, spec.TargetResource)
	}
	if len(spec.AllowedActions) > 0 {
		parts = append(parts, "actions "+strings.Join(spec.AllowedActions, ", "))
	}
	if spec.GrantedTo != "" {
		parts = append(parts, "for "+spec.GrantedTo)
	}
	if spec.RequestID != "" {
		parts = append(parts, "request "+spec.RequestID)
	}
	if len(parts) == 0 {
		return ""
	}
	return continuationPromptCompactLine(strings.Join(parts, " · "), 180)
}

func continuationApprovalPromptStops(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	values := append([]string(nil), state.ActionProposal.ForbiddenActions...)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		values = append(values, phase.ForbiddenActions...)
	}
	return stoplabels.LabelsForContinuationState(state, values, stoplabels.Options{
		Defaults: []string{"anything outside scope", "hard gates"},
		Limit:    4,
	})
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
	card := newContinuationApprovalPromptCard("Approve plan", title, state.RemainingTurns)
	if first := planBudgetFirstStep(state); first != "" {
		card.addSection("First step", continuationPromptCompactLine(first, 160))
	}
	card.addListSection("Covers", planBudgetIncludedLines(state))
	card.addListSectionWithLimit("Requires", continuationRequiredCapabilityLines(state), 4, 220)
	card.addListSection("Stops before", planBudgetStopLines(state))
	card.addSection("Fresh approval", "Anything outside this plan needs a fresh approval.")
	return card.String()
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
	case "repo_publication", "remote_repo_mutation", "git_push", "push_remote":
		return "remote publication"
	case "public_web_read", "public_profile_metadata_read", "public_account_content_read":
		return "public read"
	case "external_account_auth_status", "external_account_status_check", "read_only_auth_status_check", "credential_metadata", "token_health_check":
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
	state = session.NormalizeContinuationState(state)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	return stoplabels.LabelsForContinuationState(state, proposal.ForbiddenActions, stoplabels.Options{
		Defaults: []string{"anything outside scope", "hard gates", "deploy/restart", "policy or permission changes", "mailbox access or mutation"},
		Limit:    6,
	})
}

func planBudgetFirstStep(state session.ContinuationState) string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if phase, ok := currentContinuationBundlePhase(bundle); ok {
		return strings.TrimSpace(phase.Summary)
	}
	return strings.TrimSpace(state.StageSummary)
}
