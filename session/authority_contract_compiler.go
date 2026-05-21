//go:build linux

package session

import "strings"

type AuthorityContractCompilationStatus string

const (
	AuthorityContractCompilationStatusValid   AuthorityContractCompilationStatus = "valid"
	AuthorityContractCompilationStatusInvalid AuthorityContractCompilationStatus = "invalid"
)

type AuthorityContradictionSeverity string

const (
	AuthorityContradictionSeverityInvalid AuthorityContradictionSeverity = "invalid"
)

type AuthorityContradiction struct {
	AllowedAction   string                         `json:"allowed_action,omitempty"`
	ForbiddenAction string                         `json:"forbidden_action,omitempty"`
	WorkAction      string                         `json:"work_action,omitempty"`
	Reason          string                         `json:"reason,omitempty"`
	Severity        AuthorityContradictionSeverity `json:"severity,omitempty"`
}

type AuthorityContractCompilation struct {
	Status           AuthorityContractCompilationStatus `json:"status,omitempty"`
	Contract         AuthorityContract                  `json:"contract,omitempty"`
	WorkAction       string                             `json:"work_action,omitempty"`
	AllowedActions   []string                           `json:"allowed_actions,omitempty"`
	ForbiddenActions []string                           `json:"forbidden_actions,omitempty"`
	Contradictions   []AuthorityContradiction           `json:"contradictions,omitempty"`
	SuggestedRepair  string                             `json:"suggested_repair,omitempty"`
}

func CompileActionProposalAuthorityContract(proposal ActionProposal) AuthorityContractCompilation {
	riskClass := normalizeEnumValue(proposal.RiskClass)
	boundedEffect := strings.TrimSpace(proposal.BoundedEffect)
	allowedActions := normalizeActionStringSlice(proposal.AllowedActions)
	forbiddenActions := normalizeActionStringSlice(proposal.ForbiddenActions)
	compilation := AuthorityContractCompilation{
		Status:           AuthorityContractCompilationStatusValid,
		AllowedActions:   append([]string(nil), allowedActions...),
		ForbiddenActions: append([]string(nil), forbiddenActions...),
	}
	if contract, ok := authorityContractForCompilation(riskClass, allowedActions, forbiddenActions, boundedEffect); ok {
		compilation.Contract = contract
		compilation.WorkAction = strings.TrimSpace(contract.WorkAction)
		compilation.AllowedActions = normalizeActionStringSlice(append(compilation.AllowedActions, contract.AllowedActions...))
		compilation.ForbiddenActions = normalizeActionStringSlice(append(compilation.ForbiddenActions, contract.ForbiddenActions...))
	} else {
		compilation.WorkAction = strongestAuthorityWorkActionForAllowedActions(compilation.AllowedActions)
	}
	compilation.Contradictions = authorityContractContradictions(compilation.AllowedActions, compilation.ForbiddenActions)
	if len(compilation.Contradictions) > 0 {
		compilation.Status = AuthorityContractCompilationStatusInvalid
		compilation.SuggestedRepair = "request_fresh_narrower_proposal"
	}
	return normalizeAuthorityContractCompilation(compilation)
}

func authorityContractForCompilation(riskClass string, allowedActions []string, forbiddenActions []string, boundedEffect string) (AuthorityContract, bool) {
	if authorityActionsInclude(allowedActions, "execute_in_approved_user_sandbox") {
		return AuthorityContract{}, false
	}
	if normalizeAuthorityMatchText(riskClass) == "system_change" && authorityForbiddenIncludesBroadDeployRestart(forbiddenActions) && authorityWorkActionRank(strongestAuthorityWorkActionForAllowedActions(allowedActions)) < authorityWorkActionRank(AuthorityWorkActionDeploy) {
		return AuthorityContract{}, false
	}
	return AuthorityContractFor(riskClass, allowedActions, boundedEffect)
}

func authorityForbiddenIncludesBroadDeployRestart(actions []string) bool {
	for _, action := range actions {
		switch normalizeAuthorityMatchText(action) {
		case "deploy", "restart", "restart_service", "service_restart", "live_deploy", "run_deploy", "system_change":
			return true
		}
	}
	return false
}

func CompileContinuationAuthorityContract(state ContinuationState) AuthorityContractCompilation {
	proposal := state.ActionProposal
	proposal.AllowedActions = append(append([]string(nil), proposal.AllowedActions...), state.ContinuationLease.AllowedActions...)
	proposal.ForbiddenActions = append(append([]string(nil), proposal.ForbiddenActions...), state.ContinuationLease.ForbiddenActions...)
	if phase, ok := CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok {
		proposal.AllowedActions = append(proposal.AllowedActions, phase.AllowedActions...)
		proposal.ForbiddenActions = append(proposal.ForbiddenActions, phase.ForbiddenActions...)
		if strings.TrimSpace(proposal.RiskClass) == "" {
			proposal.RiskClass = strings.TrimSpace(phase.AuthorityClass)
		}
	}
	return CompileActionProposalAuthorityContract(proposal)
}

func AuthorityContractCompilationValidForApproval(state ContinuationState) bool {
	return CompileContinuationAuthorityContract(state).Valid()
}

func (c AuthorityContractCompilation) Valid() bool {
	return c.Status == AuthorityContractCompilationStatusValid && len(c.Contradictions) == 0
}

func (c AuthorityContractCompilation) Invalid() bool {
	return !c.Valid()
}

func AuthorityContractCompilationSummary(c AuthorityContractCompilation) string {
	c = normalizeAuthorityContractCompilation(c)
	if len(c.Contradictions) == 0 {
		return "authority contract valid"
	}
	first := c.Contradictions[0]
	parts := []string{"invalid authority contract"}
	if first.AllowedAction != "" {
		parts = append(parts, "allowed_action="+first.AllowedAction)
	}
	if first.ForbiddenAction != "" {
		parts = append(parts, "forbidden_action="+first.ForbiddenAction)
	}
	if first.WorkAction != "" {
		parts = append(parts, "work_action="+first.WorkAction)
	}
	if first.Reason != "" {
		parts = append(parts, "reason="+first.Reason)
	}
	return strings.Join(parts, " ")
}

func CurrentContinuationApprovalBundlePhase(bundle ContinuationApprovalBundle) (ContinuationApprovalBundlePhase, bool) {
	bundle = NormalizeContinuationApprovalBundle(bundle)
	if !bundle.Active() {
		return ContinuationApprovalBundlePhase{}, false
	}
	if strings.TrimSpace(bundle.CurrentPhaseID) != "" {
		for _, phase := range bundle.Phases {
			if strings.TrimSpace(phase.ID) == strings.TrimSpace(bundle.CurrentPhaseID) {
				return phase, true
			}
		}
	}
	for _, phase := range bundle.Phases {
		if phase.Active() {
			return phase, true
		}
	}
	return ContinuationApprovalBundlePhase{}, false
}

func authorityContractContradictions(allowedActions []string, forbiddenActions []string) []AuthorityContradiction {
	allowed := normalizeActionStringSlice(allowedActions)
	forbidden := normalizeActionStringSlice(forbiddenActions)
	if len(allowed) == 0 || len(forbidden) == 0 {
		return nil
	}
	out := []AuthorityContradiction{}
	for _, allowedAction := range allowed {
		allowedMode := authorityWorkActionForAllowedToken(allowedAction)
		allowedRank := authorityWorkActionRank(allowedMode)
		if allowedRank <= 0 {
			continue
		}
		allowedNormalized := normalizeAuthorityMatchText(allowedAction)
		for _, forbiddenAction := range forbidden {
			forbiddenMode := authorityWorkActionForForbiddenToken(forbiddenAction)
			forbiddenRank := authorityWorkActionRank(forbiddenMode)
			forbiddenNormalized := normalizeAuthorityMatchText(forbiddenAction)
			if forbiddenNormalized != "" && allowedNormalized == forbiddenNormalized {
				out = append(out, authorityContradiction(allowedAction, forbiddenAction, allowedMode, "allowed_action_exactly_forbidden"))
				continue
			}
			if forbiddenRank > 0 && allowedRank >= forbiddenRank {
				out = append(out, authorityContradiction(allowedAction, forbiddenAction, allowedMode, "allowed_action_implies_forbidden_authority"))
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func authorityContradiction(allowedAction string, forbiddenAction string, workAction string, reason string) AuthorityContradiction {
	return AuthorityContradiction{
		AllowedAction:   strings.TrimSpace(allowedAction),
		ForbiddenAction: strings.TrimSpace(forbiddenAction),
		WorkAction:      strings.TrimSpace(workAction),
		Reason:          strings.TrimSpace(reason),
		Severity:        AuthorityContradictionSeverityInvalid,
	}
}

func authorityActionsInclude(actions []string, want string) bool {
	want = normalizeAuthorityMatchText(want)
	if want == "" {
		return false
	}
	for _, action := range actions {
		if normalizeAuthorityMatchText(action) == want {
			return true
		}
	}
	return false
}

func authorityWorkActionForAllowedToken(value string) string {
	token := normalizeAuthorityMatchText(value)
	if contract, ok := AuthorityContractForToken(token); ok && strings.TrimSpace(contract.WorkAction) != "" {
		return strings.TrimSpace(contract.WorkAction)
	}
	switch token {
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart", "restart_aphelion_service", "systemctl_restart", "install_user_service", "make_install_user_service", "run_verify_deploy", "git_push", "push_remote":
		return AuthorityWorkActionDeploy
	case "commit", "git_commit", "repo_history_mutation", "git_commit_validated_slices", "workspace_commit", "workspace_commit_then_repo_write_bounded":
		return AuthorityWorkActionCommit
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "repo_edit", "edit", "edit_files", "patch", "patch_code", "run_tests", "test", "tests", "focused_tests", "git_diff_check", "edit_repo_code", "run_go_tests", "git_status", "git_diff":
		return AuthorityWorkActionWorkspaceWrite
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state", "inspect_code", "draft_contract", "report_evidence":
		return AuthorityWorkActionReadOnly
	default:
		switch {
		case strings.HasPrefix(token, "deploy"), strings.HasPrefix(token, "live_deploy"), strings.HasPrefix(token, "run_deploy"), strings.HasPrefix(token, "system_change"), strings.HasPrefix(token, "restart"), strings.HasPrefix(token, "service_restart"), strings.HasPrefix(token, "git_push"), strings.HasPrefix(token, "push_remote"):
			return AuthorityWorkActionDeploy
		case strings.HasPrefix(token, "commit"), strings.HasPrefix(token, "git_commit"), strings.HasPrefix(token, "repo_history_mutation"), strings.HasPrefix(token, "workspace_commit"):
			return AuthorityWorkActionCommit
		case strings.HasPrefix(token, "workspace_write"), strings.HasPrefix(token, "workspace"), strings.HasPrefix(token, "code_change"), strings.HasPrefix(token, "edit_files"), strings.HasPrefix(token, "patch"), strings.HasPrefix(token, "run_tests"):
			return AuthorityWorkActionWorkspaceWrite
		case strings.HasPrefix(token, "read_only"), strings.HasPrefix(token, "status_check"), strings.HasPrefix(token, "inspect_readonly_state"):
			return AuthorityWorkActionReadOnly
		default:
			return ""
		}
	}
}

func authorityWorkActionForForbiddenToken(value string) string {
	token := normalizeAuthorityMatchText(value)
	switch token {
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart", "restart_aphelion_service", "systemctl_restart", "install_user_service", "make_install_user_service", "run_verify_deploy", "deploy_restart", "restart_deploy", "deploy_or_restart", "restart_or_deploy", "deploy_or_enable_systemd", "deploy_or_enable_service", "deploy_service_restart", "restart_or_service_restart":
		return AuthorityWorkActionDeploy
	case "commit", "git_commit", "repo_history_mutation", "git_commit_validated_slices", "workspace_commit", "workspace_commit_then_repo_write_bounded":
		return AuthorityWorkActionCommit
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "repo_edit", "edit", "edit_files", "patch", "run_tests", "test", "tests", "focused_tests", "git_diff_check":
		return AuthorityWorkActionWorkspaceWrite
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state":
		return AuthorityWorkActionReadOnly
	default:
		return ""
	}
}

func authorityWorkActionRank(action string) int {
	switch strings.TrimSpace(action) {
	case AuthorityWorkActionDeploy:
		return 4
	case AuthorityWorkActionCommit:
		return 3
	case AuthorityWorkActionWorkspaceWrite:
		return 2
	case AuthorityWorkActionReadOnly:
		return 1
	default:
		return 0
	}
}

func strongestAuthorityWorkActionForAllowedActions(actions []string) string {
	strongest := ""
	strongestRank := 0
	for _, action := range actions {
		mode := authorityWorkActionForAllowedToken(action)
		if rank := authorityWorkActionRank(mode); rank > strongestRank {
			strongest = mode
			strongestRank = rank
		}
	}
	return strongest
}

func normalizeAuthorityContractCompilation(c AuthorityContractCompilation) AuthorityContractCompilation {
	c.WorkAction = strings.TrimSpace(c.WorkAction)
	c.AllowedActions = normalizeActionStringSlice(c.AllowedActions)
	c.ForbiddenActions = normalizeActionStringSlice(c.ForbiddenActions)
	if len(c.Contradictions) == 0 {
		c.Contradictions = nil
	}
	for i := range c.Contradictions {
		c.Contradictions[i].AllowedAction = strings.TrimSpace(c.Contradictions[i].AllowedAction)
		c.Contradictions[i].ForbiddenAction = strings.TrimSpace(c.Contradictions[i].ForbiddenAction)
		c.Contradictions[i].WorkAction = strings.TrimSpace(c.Contradictions[i].WorkAction)
		c.Contradictions[i].Reason = strings.TrimSpace(c.Contradictions[i].Reason)
		if c.Contradictions[i].Severity == "" {
			c.Contradictions[i].Severity = AuthorityContradictionSeverityInvalid
		}
	}
	if c.Status == "" {
		c.Status = AuthorityContractCompilationStatusValid
	}
	if len(c.Contradictions) > 0 {
		c.Status = AuthorityContractCompilationStatusInvalid
		if strings.TrimSpace(c.SuggestedRepair) == "" {
			c.SuggestedRepair = "request_fresh_narrower_proposal"
		}
	}
	c.SuggestedRepair = strings.TrimSpace(c.SuggestedRepair)
	return c
}
