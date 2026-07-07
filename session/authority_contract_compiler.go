//go:build linux

package session

import (
	"strings"
	"unicode"
)

type AuthorityContractCompilationStatus string

const (
	AuthorityContractCompilationStatusValid   AuthorityContractCompilationStatus = "valid"
	AuthorityContractCompilationStatusInvalid AuthorityContractCompilationStatus = "invalid"
)

const authorityClauseBoundaryToken = "\x00authority_clause_boundary"

type AuthorityContradictionSeverity string

const (
	AuthorityContradictionSeverityInvalid AuthorityContradictionSeverity = "invalid"
)

const (
	AuthorityContradictionReasonProposalRequiresForbiddenExternalEffect = "proposal_requires_external_effect_but_contract_forbids_external_effect"
	AuthorityContradictionReasonProposalRequiresForbiddenGitPush        = "proposal_requires_forbidden_git_push"
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
	proposal = ReconcileActionProposalAuthority(proposal)
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
	compilation.Contradictions = append(proposalGitPushContradictions(proposal), proposalExternalEffectContradictions(proposal, compilation)...)
	compilation.Contradictions = append(compilation.Contradictions, authorityContractContradictions(compilation.AllowedActions, compilation.ForbiddenActions)...)
	if len(compilation.Contradictions) > 0 {
		compilation.Status = AuthorityContractCompilationStatusInvalid
		compilation.SuggestedRepair = "request_fresh_narrower_proposal"
	}
	return normalizeAuthorityContractCompilation(compilation)
}

func ReconcileActionProposalAuthority(proposal ActionProposal) ActionProposal {
	proposal.AllowedActions = normalizeActionStringSlice(proposal.AllowedActions)
	proposal.ForbiddenActions = normalizeActionStringSlice(proposal.ForbiddenActions)
	if actionProposalRequiresGitPush(proposal) && !actionProposalForbidsGitPush(proposal.ForbiddenActions) {
		proposal.AllowedActions = normalizeActionStringSlice(append(proposal.AllowedActions, "git_push"))
	}
	return proposal
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
	state = NormalizeContinuationState(state)
	if ContinuationConstraintsAreDiscoveredEffect(state.ContinuationLease.Constraints) {
		return compileDiscoveredEffectContinuationAuthorityContract(state)
	}
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

func compileDiscoveredEffectContinuationAuthorityContract(state ContinuationState) AuthorityContractCompilation {
	state = NormalizeContinuationState(state)
	allowed := append([]string(nil), state.ActionProposal.AllowedActions...)
	allowed = append(allowed, state.ContinuationLease.AllowedActions...)
	forbidden := append([]string(nil), state.ActionProposal.ForbiddenActions...)
	forbidden = append(forbidden, state.ContinuationLease.ForbiddenActions...)
	if phase, ok := CurrentContinuationApprovalBundlePhase(state.ApprovalBundle); ok {
		allowed = append(allowed, phase.AllowedActions...)
		forbidden = append(forbidden, phase.ForbiddenActions...)
	}
	constraints := normalizeRecoveryStringMap(state.ContinuationLease.Constraints)
	effectAction := strings.TrimSpace(constraints["effect_action"])
	if effectAction != "" {
		allowed = append(allowed, effectAction)
	}
	compilation := AuthorityContractCompilation{
		Status: AuthorityContractCompilationStatusValid,
		Contract: AuthorityContract{
			Key:        ContinuationRecoveryContractKindDiscoveredEffect,
			LeaseClass: ContinuationLeaseClassDataAccess,
			WorkAction: AuthorityWorkActionReadOnly,
			AllowedActions: []string{
				AuthorityWorkActionReadOnly,
				"read_approved_resource",
				"report_data_access_result",
				"report_evidence",
			},
			ForbiddenActions: []string{
				"workspace_write",
				"edit_files",
				"commit",
				"git_commit",
				"repo_history_mutation",
				"repo_publication",
				"git_push",
				"push_remote",
				"github_pr_create",
				"github_pr_update",
				"deploy",
				"restart_service",
				"credential_token_output",
				"external_effect_outside_bounded_effect",
			},
			ValidationPlan: []string{
				"run only the discovered exact command once",
				"record the typed result or blocker before any further retry",
			},
			AutoApprovalAllowed:    true,
			RequiresInlineApproval: true,
			ExternalEffectsAllowed: true,
		},
		WorkAction:       AuthorityWorkActionReadOnly,
		AllowedActions:   normalizeActionStringSlice(allowed),
		ForbiddenActions: normalizeActionStringSlice(forbidden),
	}
	compilation.AllowedActions = normalizeActionStringSlice(append(compilation.AllowedActions, compilation.Contract.AllowedActions...))
	compilation.ForbiddenActions = normalizeActionStringSlice(append(compilation.ForbiddenActions, compilation.Contract.ForbiddenActions...))
	compilation.Contradictions = append(compilation.Contradictions, authorityContractContradictions(compilation.AllowedActions, compilation.ForbiddenActions)...)
	if strings.TrimSpace(constraints["command"]) == "" ||
		strings.TrimSpace(constraints["command_hash"]) == "" ||
		strings.TrimSpace(constraints["effect_kind"]) != "network_or_external_contact" ||
		effectAction == "" {
		compilation.Contradictions = append(compilation.Contradictions, AuthorityContradiction{
			AllowedAction:   effectAction,
			ForbiddenAction: "incomplete_discovered_effect_contract",
			WorkAction:      AuthorityWorkActionReadOnly,
			Reason:          "discovered_effect_contract_incomplete",
			Severity:        AuthorityContradictionSeverityInvalid,
		})
	}
	if len(compilation.Contradictions) > 0 {
		compilation.Status = AuthorityContractCompilationStatusInvalid
		compilation.SuggestedRepair = "request_fresh_narrower_proposal"
	}
	return normalizeAuthorityContractCompilation(compilation)
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
	scopedGitPushAllowed := authorityActionsHaveScopedGitPushAllowance(allowed)
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
			if authorityScopedGitPushExclusionAllowed(allowedAction, forbiddenAction, scopedGitPushAllowed) {
				continue
			}
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

func proposalGitPushContradictions(proposal ActionProposal) []AuthorityContradiction {
	if !actionProposalRequiresGitPush(proposal) {
		return nil
	}
	forbidden := firstActionProposalGitPushForbiddenAction(proposal.ForbiddenActions)
	if forbidden == "" {
		return nil
	}
	if authorityForbiddenGitPushIsScopeExclusion(forbidden) && actionProposalHasScopedGitPushAllowance(proposal) {
		return nil
	}
	return []AuthorityContradiction{authorityContradiction("git_push", forbidden, AuthorityWorkActionCommit, AuthorityContradictionReasonProposalRequiresForbiddenGitPush)}
}

func proposalExternalEffectContradictions(proposal ActionProposal, compilation AuthorityContractCompilation) []AuthorityContradiction {
	allowed := firstActionProposalExternalEffectAllowedAction(proposal)
	if allowed == "" {
		return nil
	}
	forbidden := firstAuthorityExternalEffectForbiddenAction(compilation.ForbiddenActions)
	if compilation.Contract.ExternalEffectsAllowed && forbidden == "" {
		return nil
	}
	if forbidden == "" {
		forbidden = "external_effect_without_separate_grant"
	}
	return []AuthorityContradiction{authorityContradiction(allowed, forbidden, compilation.WorkAction, AuthorityContradictionReasonProposalRequiresForbiddenExternalEffect)}
}

func authorityScopedGitPushExclusionAllowed(allowedAction string, forbiddenAction string, scopedGitPushAllowed bool) bool {
	if !scopedGitPushAllowed || !authorityForbiddenGitPushIsScopeExclusion(forbiddenAction) {
		return false
	}
	if !authorityTokenImpliesGitPush(allowedAction) {
		return false
	}
	return normalizeAuthorityMatchText(allowedAction) != normalizeAuthorityMatchText(forbiddenAction)
}

func actionProposalHasScopedGitPushAllowance(proposal ActionProposal) bool {
	if authorityActionsHaveScopedGitPushAllowance(proposal.AllowedActions) {
		return true
	}
	return authorityTextImpliesScopedGitPush(actionProposalPositiveAuthorityText(proposal))
}

func authorityActionsHaveScopedGitPushAllowance(actions []string) bool {
	for _, action := range actions {
		if authorityTokenImpliesScopedGitPush(action) {
			return true
		}
	}
	return false
}

func authorityForbiddenGitPushIsScopeExclusion(value string) bool {
	token := normalizeAuthorityMatchText(value)
	if token == "" || !authorityTokenImpliesGitPush(value) {
		return false
	}
	hasExclusion := false
	for _, marker := range []string{"another", "other", "different", "unrelated", "unapproved", "outside", "elsewhere"} {
		if strings.Contains(token, marker) {
			hasExclusion = true
			break
		}
	}
	if !hasExclusion {
		return false
	}
	for _, marker := range []string{"branch", "repo", "repository", "remote", "origin", "upstream"} {
		if strings.Contains(token, marker) {
			return true
		}
	}
	return false
}

func authorityTokenImpliesScopedGitPush(value string) bool {
	token := normalizeAuthorityMatchText(value)
	if token == "" || !authorityTokenImpliesGitPush(value) {
		return false
	}
	if token == "git_push" || token == "push_remote" || token == "push_branch" || token == "push_branches" || token == "push_to_remote" {
		return false
	}
	for _, marker := range []string{"origin_", "origin/", "branch", "main", "pr_branch", "current_branch", "fork", "upstream"} {
		if strings.Contains(token, strings.ReplaceAll(marker, "/", "_")) || strings.Contains(strings.ToLower(strings.TrimSpace(value)), marker) {
			return true
		}
	}
	return false
}

func authorityTextImpliesScopedGitPush(text string) bool {
	tokens := authorityTextTokens(text)
	for i, token := range tokens {
		if token == "git" && i+1 < len(tokens) && isAuthorityPushVerb(tokens[i+1]) {
			if authorityPushMentionNegated(tokens, i) || authorityPushMentionNegated(tokens, i+1) {
				continue
			}
			start := authorityClauseBoundedWindowStart(tokens, i, 8)
			end := authorityClauseBoundedWindowEnd(tokens, i+1, 8)
			if authorityWindowHasPushScopeMarker(tokens, start, end) && !authorityWindowHasPushScopeExclusion(tokens, start, end) {
				return true
			}
			continue
		}
		if !isAuthorityPushVerb(token) || authorityPushMentionNegated(tokens, i) {
			continue
		}
		start := authorityClauseBoundedWindowStart(tokens, i, 8)
		end := authorityClauseBoundedWindowEnd(tokens, i, 8)
		if !authorityWindowHasGitPushContext(tokens, start, end, i) {
			continue
		}
		if authorityWindowHasPushScopeMarker(tokens, start, end) && !authorityWindowHasPushScopeExclusion(tokens, start, end) {
			return true
		}
	}
	return false
}

func authorityWindowHasGitPushContext(tokens []string, start int, end int, pushIndex int) bool {
	for i := start; i < end; i++ {
		if i == pushIndex {
			continue
		}
		if isAuthorityGitPushContextToken(tokens[i]) {
			return true
		}
	}
	return false
}

func authorityWindowHasPushScopeMarker(tokens []string, start int, end int) bool {
	for i := start; i < end; i++ {
		switch tokens[i] {
		case "origin", "branch", "main", "fork", "upstream":
			return true
		}
	}
	return false
}

func authorityWindowHasPushScopeExclusion(tokens []string, start int, end int) bool {
	for i := start; i < end; i++ {
		switch tokens[i] {
		case "another", "other", "different", "unrelated", "unapproved", "outside", "elsewhere":
			return true
		}
	}
	return false
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
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart", "restart_aphelion_service", "systemctl_restart", "install_user_service", "make_install_user_service", "run_verify_deploy":
		return AuthorityWorkActionDeploy
	case "commit", "git_commit", "repo_history_mutation", "git_commit_validated_slices", "workspace_commit", "workspace_commit_then_repo_write_bounded", "git_push", "push_remote", "repo_publication", "remote_repo_mutation":
		return AuthorityWorkActionCommit
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "repo_edit", "edit", "edit_files", "patch", "patch_code", "run_tests", "test", "tests", "focused_tests", "git_diff_check", "edit_repo_code", "run_go_tests", "git_status", "git_diff":
		return AuthorityWorkActionWorkspaceWrite
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state", "inspect_code", "draft_contract", "report_evidence":
		return AuthorityWorkActionReadOnly
	default:
		switch {
		case strings.HasPrefix(token, "deploy"), strings.HasPrefix(token, "live_deploy"), strings.HasPrefix(token, "run_deploy"), strings.HasPrefix(token, "system_change"), strings.HasPrefix(token, "restart"), strings.HasPrefix(token, "service_restart"):
			return AuthorityWorkActionDeploy
		case strings.HasPrefix(token, "commit"), strings.HasPrefix(token, "git_commit"), strings.HasPrefix(token, "repo_history_mutation"), strings.HasPrefix(token, "workspace_commit"), strings.HasPrefix(token, "git_push"), strings.HasPrefix(token, "push_remote"), strings.HasPrefix(token, "repo_publication"), strings.HasPrefix(token, "remote_repo_mutation"):
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

func firstActionProposalExternalEffectAllowedAction(proposal ActionProposal) string {
	for _, action := range proposal.AllowedActions {
		if authorityTokenImpliesExternalEffect(action) {
			return strings.TrimSpace(action)
		}
	}
	return ""
}

func actionProposalRequiresGitPush(proposal ActionProposal) bool {
	for _, action := range proposal.AllowedActions {
		if authorityTokenImpliesGitPush(action) {
			return true
		}
	}
	return authorityTextImpliesGitPush(actionProposalPositiveAuthorityText(proposal))
}

func actionProposalPositiveAuthorityText(proposal ActionProposal) string {
	parts := []string{
		proposal.OperatorTitle,
		proposal.PlanTitle,
		proposal.Summary,
		proposal.WhyNow,
	}
	if effect := positiveBoundedEffectAuthorityText(proposal.BoundedEffect); effect != "" {
		parts = append(parts, effect)
	}
	return strings.Join(parts, "\n")
}

func positiveBoundedEffectAuthorityText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || boundedEffectLineIsNegativeAuthorityProjection(trimmed) {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, "\n")
}

func boundedEffectLineIsNegativeAuthorityProjection(line string) bool {
	normalized := normalizeAuthorityMatchText(strings.TrimSuffix(line, ":"))
	switch {
	case normalized == "forbidden",
		normalized == "forbidden_actions",
		normalized == "forbid",
		normalized == "forbids",
		normalized == "stop",
		normalized == "stop_conditions",
		normalized == "must_not",
		normalized == "do_not",
		strings.HasPrefix(normalized, "forbidden_"),
		strings.HasPrefix(normalized, "stop_"),
		strings.HasPrefix(normalized, "must_not_"),
		strings.HasPrefix(normalized, "do_not_"),
		strings.HasPrefix(normalized, "without_"):
		return true
	default:
		return false
	}
}

func actionProposalForbidsGitPush(actions []string) bool {
	return firstActionProposalGitPushForbiddenAction(actions) != ""
}

func firstActionProposalGitPushForbiddenAction(actions []string) string {
	for _, action := range actions {
		if authorityTokenImpliesGitPush(action) {
			return strings.TrimSpace(action)
		}
	}
	return ""
}

func firstAuthorityExternalEffectForbiddenAction(actions []string) string {
	for _, action := range actions {
		if authorityTokenForbidsExternalEffect(action) {
			return strings.TrimSpace(action)
		}
	}
	return ""
}

func authorityTokenForbidsExternalEffect(value string) bool {
	token := normalizeAuthorityMatchText(value)
	switch token {
	case "external_effect_without_separate_grant", "external_effect_without_approval", "external_contact_without_grant", "network_contact_without_grant":
		return true
	default:
		return false
	}
}

func authorityTokenImpliesExternalEffect(value string) bool {
	token := normalizeAuthorityMatchText(value)
	switch token {
	case AuthorityClassExternalRead, "external_network_read", "network_read", "network_access_read", "external_network_contact", "network_contact", "fetch", "fetch_remote", "git_fetch", "git_fetch_read_refs", "git_ls_remote", "ls_remote":
		return true
	}
	return strings.HasPrefix(token, "git_fetch") ||
		strings.HasPrefix(token, "fetch_remote") ||
		strings.HasPrefix(token, "fetch_origin") ||
		strings.HasPrefix(token, "git_ls_remote") ||
		strings.HasPrefix(token, "ls_remote")
}

func authorityTokenImpliesGitPush(value string) bool {
	token := normalizeAuthorityMatchText(value)
	switch token {
	case "git_push", "push_remote", "push_branch", "push_branches", "push_to_origin", "push_to_remote", "push_main_to_origin", "push_current_branch", "push_existing_commit", "git_push_to_pr_branch", "push_pr_branch":
		return true
	}
	if strings.HasPrefix(token, "git_push") || strings.HasPrefix(token, "push_remote") {
		return true
	}
	if strings.Contains(token, "push") {
		for _, marker := range []string{"origin", "remote", "branch", "repo", "repository", "github", "upstream"} {
			if strings.Contains(token, marker) {
				return true
			}
		}
	}
	return false
}

func authorityTextImpliesGitPush(text string) bool {
	tokens := authorityTextTokens(text)
	for i, token := range tokens {
		if token == "git" && i+1 < len(tokens) && isAuthorityPushVerb(tokens[i+1]) {
			if authorityPushMentionNegated(tokens, i) || authorityPushMentionNegated(tokens, i+1) {
				continue
			}
			return true
		}
		if !isAuthorityPushVerb(token) {
			continue
		}
		if authorityPushMentionNegated(tokens, i) {
			continue
		}
		start := authorityClauseBoundedWindowStart(tokens, i, 8)
		end := authorityClauseBoundedWindowEnd(tokens, i, 8)
		for j := start; j < end; j++ {
			if j == i {
				continue
			}
			if isAuthorityGitPushContextToken(tokens[j]) {
				return true
			}
		}
	}
	return false
}

func authorityPushMentionNegated(tokens []string, idx int) bool {
	start := authorityClauseBoundedWindowStart(tokens, idx, 4)
	for i := start; i < idx; i++ {
		switch tokens[i] {
		case "no", "not", "without", "never", "avoid", "avoids", "forbid", "forbidden", "forbids", "prohibit", "prohibited", "deny", "denies":
			return true
		}
	}
	return false
}

func authorityClauseBoundedWindowStart(tokens []string, idx int, width int) int {
	if width < 0 {
		width = 0
	}
	start := idx - width
	if start < 0 {
		start = 0
	}
	for i := idx - 1; i >= start; i-- {
		if tokens[i] == authorityClauseBoundaryToken {
			return i + 1
		}
	}
	return start
}

func authorityClauseBoundedWindowEnd(tokens []string, idx int, width int) int {
	if width < 0 {
		width = 0
	}
	end := idx + width + 1
	if end > len(tokens) {
		end = len(tokens)
	}
	for i := idx + 1; i < end; i++ {
		if tokens[i] == authorityClauseBoundaryToken {
			return i
		}
	}
	return end
}

func authorityTextTokens(text string) []string {
	out := []string{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		out = append(out, b.String())
		b.Reset()
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
		switch r {
		case '.', ';', ':', '!', '?', '\n', '\r':
			if len(out) == 0 || out[len(out)-1] != authorityClauseBoundaryToken {
				out = append(out, authorityClauseBoundaryToken)
			}
		}
	}
	flush()
	return out
}

func isAuthorityPushVerb(token string) bool {
	switch token {
	case "push", "pushes", "pushed", "pushing":
		return true
	default:
		return false
	}
}

func isAuthorityGitPushContextToken(token string) bool {
	switch token {
	case "git", "repo", "repos", "repository", "repositories", "origin", "remote", "remotes", "branch", "branches", "github", "upstream":
		return true
	default:
		return false
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
