//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func continuationWorkMode(state session.ContinuationState) WorkMode {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if mode := workModeFromStructuredAuthority(phase.AuthorityClass); mode != "" {
			return mode
		}
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	mode := strongestWorkMode(
		workModeFromStructuredAuthority(proposal.RiskClass),
		workModeFromStructuredAuthorityList(proposal.AllowedActions),
		workModeFromStructuredAuthorityList(state.ContinuationLease.AllowedActions),
	)
	if mode != "" {
		return mode
	}

	lower := strings.ToLower(strings.Join([]string{
		proposal.Summary,
		proposal.BoundedEffect,
		state.StageSummary,
	}, " "))
	switch {
	case strings.Contains(lower, "read_only") || strings.Contains(lower, "status_check"):
		return WorkModeReadOnly
	default:
		return ""
	}
}

func continuationWorkModeAccessCheck(state session.ContinuationState, mode WorkMode, now time.Time) session.ContinuationLeaseAccessDecision {
	state = session.NormalizeContinuationState(state)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	action := strings.TrimSpace(string(mode))
	decision := session.CheckContinuationLeaseAction(state.ContinuationLease, action, now)
	if decision.Allowed {
		return decision
	}
	if decision.Reason != "action_not_allowed" {
		return decision
	}
	requestedRank := workModeRank(mode)
	if requestedRank <= 0 {
		decision.Reason = "work_mode_required"
		return decision
	}
	if continuationWorkModeForbiddenByLease(state, mode) {
		decision.Reason = "action_forbidden"
		return decision
	}
	if continuationAllowedWorkModeRank(state) >= requestedRank {
		decision.Allowed = true
		decision.Reason = "allowed_by_structured_authority"
		return decision
	}
	return decision
}

func continuationAllowedWorkModeRank(state session.ContinuationState) int {
	state = session.NormalizeContinuationState(state)
	mode := WorkMode("")
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		mode = strongestWorkMode(mode, workModeFromStructuredAuthority(phase.AuthorityClass))
		mode = strongestWorkMode(mode, workModeFromStructuredAuthorityList(phase.AllowedActions))
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	mode = strongestWorkMode(mode, workModeFromStructuredAuthority(proposal.RiskClass))
	mode = strongestWorkMode(mode, workModeFromStructuredAuthorityList(proposal.AllowedActions))
	mode = strongestWorkMode(mode, workModeFromStructuredAuthorityList(state.ContinuationLease.AllowedActions))
	return workModeRank(mode)
}

func continuationWorkModeForbiddenByLease(state session.ContinuationState, mode WorkMode) bool {
	state = session.NormalizeContinuationState(state)
	requestedRank := workModeRank(mode)
	if requestedRank <= 0 {
		return false
	}
	for _, forbidden := range continuationForbiddenWorkModeActions(state) {
		forbiddenMode := workModeFromBroadForbiddenAuthority(forbidden)
		forbiddenRank := workModeRank(forbiddenMode)
		if forbiddenRank > 0 && requestedRank >= forbiddenRank {
			return true
		}
		if normalizeWorkModeAuthorityToken(forbidden) == normalizeWorkModeAuthorityToken(string(mode)) {
			return true
		}
	}
	return false
}

func continuationForbiddenWorkModeActions(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	out := make([]string, 0, len(state.ActionProposal.ForbiddenActions)+len(state.ContinuationLease.ForbiddenActions)+8)
	out = append(out, state.ActionProposal.ForbiddenActions...)
	out = append(out, state.ContinuationLease.ForbiddenActions...)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		out = append(out, phase.ForbiddenActions...)
	}
	return out
}

func workModeFromBroadForbiddenAuthority(value string) WorkMode {
	token := normalizeWorkModeAuthorityToken(value)
	switch token {
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart":
		return WorkModeDeploy
	case "commit", "git_commit", "repo_history_mutation":
		return WorkModeCommit
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "edit", "edit_files", "patch", "run_tests", "test", "tests":
		return WorkModeWorkspaceWrite
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state":
		return WorkModeReadOnly
	default:
		return ""
	}
}

func workModeFromStructuredAuthorityList(values []string) WorkMode {
	mode := WorkMode("")
	for _, value := range values {
		mode = strongestWorkMode(mode, workModeFromStructuredAuthority(value))
	}
	return mode
}

func workModeFromStructuredAuthority(value string) WorkMode {
	token := normalizeWorkModeAuthorityToken(value)
	if contract, ok := session.AuthorityContractForToken(token); ok {
		if mode := workModeFromAuthorityContractAction(contract.WorkAction); mode != "" {
			return mode
		}
	}
	switch token {
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart":
		return WorkModeDeploy
	case "commit", "git_commit", "repo_history_mutation":
		return WorkModeCommit
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "edit", "edit_files", "patch", "run_tests", "test", "tests":
		return WorkModeWorkspaceWrite
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state":
		return WorkModeReadOnly
	default:
		switch {
		case strings.HasPrefix(token, "deploy") ||
			strings.HasPrefix(token, "live_deploy") ||
			strings.HasPrefix(token, "run_deploy") ||
			strings.HasPrefix(token, "system_change") ||
			strings.HasPrefix(token, "restart") ||
			strings.HasPrefix(token, "service_restart"):
			return WorkModeDeploy
		case strings.HasPrefix(token, "commit") ||
			strings.HasPrefix(token, "git_commit") ||
			strings.HasPrefix(token, "repo_history_mutation"):
			return WorkModeCommit
		case strings.HasPrefix(token, "workspace_write") ||
			strings.HasPrefix(token, "workspace") ||
			strings.HasPrefix(token, "code_change") ||
			strings.HasPrefix(token, "edit_files") ||
			strings.HasPrefix(token, "patch") ||
			strings.HasPrefix(token, "run_tests"):
			return WorkModeWorkspaceWrite
		case strings.HasPrefix(token, "read_only") ||
			strings.HasPrefix(token, "status_check") ||
			strings.HasPrefix(token, "inspect_readonly_state"):
			return WorkModeReadOnly
		default:
			return ""
		}
	}
}

func workModeFromAuthorityContractAction(action string) WorkMode {
	switch normalizeWorkModeAuthorityToken(action) {
	case session.AuthorityWorkActionDeploy:
		return WorkModeDeploy
	case session.AuthorityWorkActionCommit:
		return WorkModeCommit
	case session.AuthorityWorkActionWorkspaceWrite:
		return WorkModeWorkspaceWrite
	case session.AuthorityWorkActionReadOnly:
		return WorkModeReadOnly
	default:
		return ""
	}
}

func normalizeWorkModeAuthorityToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

func strongestWorkMode(modes ...WorkMode) WorkMode {
	strongest := WorkMode("")
	strongestRank := 0
	for _, mode := range modes {
		rank := workModeRank(mode)
		if rank > strongestRank {
			strongest = mode
			strongestRank = rank
		}
	}
	return strongest
}

func workModeRank(mode WorkMode) int {
	switch mode {
	case WorkModeDeploy:
		return 4
	case WorkModeCommit:
		return 3
	case WorkModeWorkspaceWrite:
		return 2
	case WorkModeReadOnly:
		return 1
	default:
		return 0
	}
}

func workPromptForContinuation(state session.ContinuationState, opState session.OperationState) string {
	state = session.NormalizeContinuationState(state)
	opState = session.NormalizeOperationState(opState)
	lines := []string{
		"Role: You are the bounded work executor for a runtime-approved continuation.",
		"",
		"## Goal",
		"Complete only the approved next step and return evidence the parent runtime can store and summarize.",
		"",
		"## Success Criteria",
		"- Stay within the lease, work mode, repository, and sandbox implied by this request.",
		"- Preserve durable operation context and do not collapse a broad objective into a one-step plan.",
		"- Validate meaningful edits, generated artifacts, service actions, or conclusions with the narrowest relevant check available.",
		"- Report changed files, commands, tests, evidence, residual risk, and any blocked validation.",
		"",
		"## Constraints",
		"- Do not expand authority, credentials, network use, deploy, restart, commit, or external effects beyond this approved lease.",
		"- Do not ask for approval to make a plan. If more work remains, propose concrete bounded next phases or lanes.",
		"",
		"## Stop Rules",
		"- Stop before any action outside the lease or any action whose failure could create irreversible, external, privacy, or credential risk.",
		"- If required evidence or validation is unavailable, report that limitation instead of inventing certainty.",
	}
	currentBundlePhase, hasCurrentBundlePhase := currentContinuationBundlePhase(state.ApprovalBundle)
	if objective := firstNonEmptyContinuation(opState.Objective, state.Objective); objective != "" {
		lines = append(lines, "Objective: "+objective)
	}
	if hasCurrentBundlePhase {
		phaseID := firstNonEmptyContinuation(currentBundlePhase.OperationPhaseID, currentBundlePhase.ID)
		if phaseID != "" {
			lines = append(lines, "Approved bundle phase: "+phaseID)
		}
		if authority := strings.TrimSpace(currentBundlePhase.AuthorityClass); authority != "" {
			lines = append(lines, "Phase authority class: "+authority)
		}
	}
	if summary := firstNonEmptyContinuation(currentBundlePhase.Summary, state.ActionProposal.Summary, state.StageSummary); summary != "" {
		lines = append(lines, "Next step: "+summary)
	}
	if effect := firstNonEmptyContinuation(currentBundlePhase.BoundedEffect, state.ActionProposal.BoundedEffect); effect != "" {
		lines = append(lines, "Bounded effect: "+effect)
	}
	if hasCurrentBundlePhase && len(currentBundlePhase.AllowedActions) > 0 {
		lines = append(lines, "Allowed phase actions: "+strings.Join(currentBundlePhase.AllowedActions, ", "))
	}
	if hasCurrentBundlePhase && len(currentBundlePhase.ForbiddenActions) > 0 {
		lines = append(lines, "Forbidden phase actions: "+strings.Join(currentBundlePhase.ForbiddenActions, ", "))
	}
	if opState.PhasePlan.Active() {
		lines = append(lines, "Durable phase plan: "+firstNonEmptyContinuation(opState.PhasePlan.Goal, opState.PhasePlan.ID))
		if current := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID); current != "" {
			lines = append(lines, "Current phase id: "+current)
		}
		for _, phase := range opState.PhasePlan.Phases {
			label := strings.TrimSpace(phase.ID)
			if summary := strings.TrimSpace(phase.Summary); summary != "" {
				if label == "" {
					label = summary
				} else {
					label += ": " + summary
				}
			}
			if label == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("Phase [%s] %s", phase.Status, label))
		}
	}
	lines = append(lines, "Stop after this bounded step and report changed files, commands, tests, evidence, and remaining risk.")
	return strings.Join(lines, "\n")
}
