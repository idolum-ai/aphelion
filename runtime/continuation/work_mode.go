//go:build linux

package continuation

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

type WorkMode string

const (
	WorkModeReadOnly       WorkMode = "read_only"
	WorkModeWorkspaceWrite WorkMode = "workspace_write"
	WorkModeCommit         WorkMode = "commit"
	WorkModeDeploy         WorkMode = "deploy"
)

func WorkModeForState(state session.ContinuationState) WorkMode {
	state = session.NormalizeContinuationState(state)
	if phase, ok := CurrentBundlePhase(state.ApprovalBundle); ok {
		if mode := WorkModeFromStructuredAuthority(phase.AuthorityClass); mode != "" {
			return mode
		}
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	mode := StrongestWorkMode(
		WorkModeFromStructuredAuthority(proposal.RiskClass),
		WorkModeFromStructuredAuthorityList(proposal.AllowedActions),
		WorkModeFromStructuredAuthorityList(state.ContinuationLease.AllowedActions),
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

func LeaseAccessCheck(state session.ContinuationState, mode WorkMode, now time.Time) session.ContinuationLeaseAccessDecision {
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
	requestedRank := WorkModeRank(mode)
	if requestedRank <= 0 {
		decision.Reason = "work_mode_required"
		return decision
	}
	if WorkModeForbiddenByLease(state, mode) {
		decision.Reason = "action_forbidden"
		return decision
	}
	if AllowedWorkModeRank(state) >= requestedRank {
		decision.Allowed = true
		decision.Reason = "allowed_by_structured_authority"
		return decision
	}
	return decision
}

func AllowedWorkModeRank(state session.ContinuationState) int {
	state = session.NormalizeContinuationState(state)
	mode := WorkMode("")
	if phase, ok := CurrentBundlePhase(state.ApprovalBundle); ok {
		mode = StrongestWorkMode(mode, WorkModeFromStructuredAuthority(phase.AuthorityClass))
		mode = StrongestWorkMode(mode, WorkModeFromStructuredAuthorityList(phase.AllowedActions))
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	mode = StrongestWorkMode(mode, WorkModeFromStructuredAuthority(proposal.RiskClass))
	mode = StrongestWorkMode(mode, WorkModeFromStructuredAuthorityList(proposal.AllowedActions))
	mode = StrongestWorkMode(mode, WorkModeFromStructuredAuthorityList(state.ContinuationLease.AllowedActions))
	return WorkModeRank(mode)
}

func WorkModeForbiddenByLease(state session.ContinuationState, mode WorkMode) bool {
	state = session.NormalizeContinuationState(state)
	requestedRank := WorkModeRank(mode)
	if requestedRank <= 0 {
		return false
	}
	for _, forbidden := range forbiddenWorkModeActions(state) {
		forbiddenMode := workModeFromBroadForbiddenAuthority(forbidden)
		forbiddenRank := WorkModeRank(forbiddenMode)
		if forbiddenRank > 0 && requestedRank >= forbiddenRank {
			return true
		}
		if normalizeWorkModeAuthorityToken(forbidden) == normalizeWorkModeAuthorityToken(string(mode)) {
			return true
		}
	}
	return false
}

func forbiddenWorkModeActions(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	out := make([]string, 0, len(state.ActionProposal.ForbiddenActions)+len(state.ContinuationLease.ForbiddenActions)+8)
	out = append(out, state.ActionProposal.ForbiddenActions...)
	out = append(out, state.ContinuationLease.ForbiddenActions...)
	if phase, ok := CurrentBundlePhase(state.ApprovalBundle); ok {
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

func WorkModeFromStructuredAuthorityList(values []string) WorkMode {
	mode := WorkMode("")
	for _, value := range values {
		mode = StrongestWorkMode(mode, WorkModeFromStructuredAuthority(value))
	}
	return mode
}

func WorkModeFromStructuredAuthority(value string) WorkMode {
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

func StrongestWorkMode(modes ...WorkMode) WorkMode {
	strongest := WorkMode("")
	strongestRank := 0
	for _, mode := range modes {
		rank := WorkModeRank(mode)
		if rank > strongestRank {
			strongest = mode
			strongestRank = rank
		}
	}
	return strongest
}

func WorkModeRank(mode WorkMode) int {
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

func CurrentBundlePhase(bundle session.ContinuationApprovalBundle) (session.ContinuationApprovalBundlePhase, bool) {
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if len(bundle.Phases) == 0 {
		return session.ContinuationApprovalBundlePhase{}, false
	}
	currentID := strings.TrimSpace(bundle.CurrentPhaseID)
	if currentID != "" {
		for _, phase := range bundle.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				return phase, true
			}
		}
	}
	for _, phase := range bundle.Phases {
		if phase.Status == session.ContinuationLeaseStatusActive || phase.Status == session.ContinuationLeaseStatusPending || phase.Status == "" {
			return phase, true
		}
	}
	return bundle.Phases[0], true
}
