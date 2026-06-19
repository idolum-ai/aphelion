//go:build linux

package session

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

const (
	AuthorityWorkActionReadOnly       = "read_only"
	AuthorityWorkActionWorkspaceWrite = "workspace_write"
	AuthorityWorkActionCommit         = "commit"
	AuthorityWorkActionDeploy         = "deploy"
)

const AuthorityClassLocalSecretMetadataReadLiveConfigRead = "local_secret_metadata_read_live_config_read"

type AuthorityContract struct {
	Key                    string
	LeaseClass             ContinuationLeaseClass
	WorkAction             string
	AllowedActions         []string
	ForbiddenActions       []string
	ValidationPlan         []string
	AutoApprovalAllowed    bool
	RequiresInlineApproval bool
	ExternalEffectsAllowed bool
}

func AuthorityContractFor(riskClass string, allowedActions []string, boundedEffect string) (AuthorityContract, bool) {
	_ = boundedEffect
	if contract, ok := AuthorityContractForToken(riskClass); ok {
		return contract, true
	}
	claim, ok := AuthorityInterpretationClaimFor(riskClass, allowedActions, "")
	if !ok {
		return AuthorityContract{}, false
	}
	return AuthorityContractForToken(claim.AuthorityClass)
}

func AuthorityInterpretationClaimFor(riskClass string, allowedActions []string, boundedEffect string) (core.InterpretationClaim, bool) {
	_ = boundedEffect
	if contract, ok := AuthorityContractForToken(riskClass); ok {
		return authorityInterpretationClaim(contract.Key, "risk_class"), true
	}
	if key := authorityClassFromStructuredValues(append([]string{riskClass}, allowedActions...)); key != "" {
		return authorityInterpretationClaim(key, "structured_authority_fields"), true
	}
	return core.InterpretationClaim{}, false
}

func authorityInterpretationClaim(authorityClass string, source string) core.InterpretationClaim {
	return core.NormalizeInterpretationClaim(core.InterpretationClaim{
		Intent:             "authority_classification",
		AuthorityClass:     strings.TrimSpace(authorityClass),
		Confidence:         "high",
		Source:             source,
		ProposedNextAction: "validate_against_authority_contract",
	})
}

func authorityClassFromStructuredValues(values []string) string {
	tokens := authorityStructuredTokenSet(values)
	for _, group := range authorityClassificationPriority() {
		for _, token := range group.Tokens {
			if _, ok := tokens[token]; ok {
				return group.Key
			}
		}
	}
	return ""
}

type authorityClassificationGroup struct {
	Key    string
	Tokens []string
}

func authorityClassificationPriority() []authorityClassificationGroup {
	return []authorityClassificationGroup{
		{Key: "deploy", Tokens: []string{"deploy", "live_deploy", "run_deploy", "system_change", "restart", "service_restart", "restart_aphelion_service", "systemctl_restart", "install_user_service", "make_install_user_service", "run_verify_deploy"}},
		{Key: "capability_grant", Tokens: []string{"capability_grant", "capability_acquisition", "grant_capability", "grant_set", "capability_authority", "capability_access_check", "grant_or_revoke_capability", "capability_revoke"}},
		{Key: "external_account_action", Tokens: []string{"external_account_action", "external_account_pr_create", "github_pr_create", "github_pr_open", "github_pr_update", "github_pr_metadata_update", "pull_request_create", "pull_request_open", "pull_request_update", "pull_request_metadata_update", "open_pull_request", "create_github_pr", "update_pull_request_title", "update_pull_request_body"}},
		{Key: "child_wake", Tokens: []string{"child_wake", "durable_child_wake", "selected_child_wake", "durable_agent_wake"}},
		{Key: AuthorityClassLocalSecretMetadataReadLiveConfigRead, Tokens: []string{
			AuthorityClassLocalSecretMetadataReadLiveConfigRead,
			"local_secret_metadata_read",
			"secret_metadata_read",
			"live_config_read",
			"config_metadata_read",
			"token_file_metadata",
			"metadata_read",
		}},
		{Key: "private_data_intake", Tokens: []string{
			"private_data_intake",
			"resource_owner_data_intake",
			"resource_owner_profile_intake",
			"private_profile_intake",
			"profile_evaluation_rubric",
			"cv_ingestion",
			"mailbox_content",
			"read_mailbox_contents",
			"run_mailbox_adapter_query",
			"run_configured_mailbox_adapter_query_once",
			"read_only_mailbox_smoke",
			"email_read",
			"mailbox_read",
			"external_account_email_read",
			"external_account_email_read_public_web_read",
			"external_account",
			"public_web_read",
			"private_material_processing",
			"rank_private_material",
			"scout_public_opportunities",
		}},
		{Key: "data_access", Tokens: []string{"data_access", "file_access", "read_file", "read_image", "consume_attachment", "artifact_read", "network_access", "external_account_auth_status", "external_account_status_check", "read_only_auth_status_check", "credential_state_check", "credential_metadata", "credential_metadata_check", "token_health_check", "run_external_account_auth_status_or_identity_check"}},
		{Key: "commit", Tokens: []string{"commit", "git_commit", "git_commit_validated_slices", "repo_history_mutation", "workspace_commit", "workspace_commit_then_repo_write_bounded", "git_push", "push_remote"}},
		{Key: "workspace_write", Tokens: []string{"workspace_write", "workspace", "code", "code_change", "code_changes", "repo_edit", "edit", "edit_files", "patch", "run_tests", "test", "tests", "focused_tests", "git_diff_check"}},
		{Key: "read_only_review", Tokens: []string{"read_only", "read_only_review", "status_check", "inspect_readonly_state", "read_only_child_adapter_environment_inspection"}},
	}
}

func authorityStructuredTokenSet(values []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, value := range values {
		for _, token := range authorityStructuredTokens(value) {
			out[token] = struct{}{}
		}
	}
	return out
}

func authorityStructuredTokens(value string) []string {
	normalized := normalizeAuthorityMatchText(value)
	if normalized == "" {
		return nil
	}
	return []string{normalized}
}

func normalizeAuthorityMatchText(text string) string {
	text = normalizeEnumValue(text)
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		",", "_",
		":", "_",
		"(", "_",
		")", "_",
		"[", "_",
		"]", "_",
		"{", "_",
		"}", "_",
		"|", "_",
	)
	text = replacer.Replace(text)
	for strings.Contains(text, "__") {
		text = strings.ReplaceAll(text, "__", "_")
	}
	return strings.Trim(text, "_")
}

func AuthorityContractForToken(token string) (AuthorityContract, bool) {
	key := normalizeEnumValue(token)
	switch key {
	case AuthorityClassLocalSecretMetadataReadLiveConfigRead:
		return AuthorityContract{
			Key:        AuthorityClassLocalSecretMetadataReadLiveConfigRead,
			LeaseClass: ContinuationLeaseClassDataAccess,
			WorkAction: AuthorityWorkActionReadOnly,
			AllowedActions: []string{
				AuthorityWorkActionReadOnly,
				"inspect_live_config_metadata",
				"inspect_token_file_metadata",
				"inspect_secret_path_metadata",
				"run_metadata_only_preflight",
				"report_metadata_preflight_evidence",
			},
			ForbiddenActions: []string{
				"read_token_contents",
				"telegram_api_call",
				"poll_updates",
				"patch_live_config",
				"restart_service",
				"deploy_or_enable_systemd",
				"send_group_message",
				"read_group_history",
				"email_read",
				"cv_ingestion",
				"public_web_search",
				"private_material_processing",
				"git_push",
			},
			ValidationPlan: []string{
				"verify only metadata paths and config route markers were inspected",
				"verify no token contents, Telegram API calls, config patches, restart, deploy, or group messages occurred",
				"report evidence and stop before any live effect",
			},
			AutoApprovalAllowed:    true,
			RequiresInlineApproval: true,
		}, true
	case "data_access", "file_access", "read_file", "read_image", "consume_attachment", "artifact_read", "network_access", "external_account_auth_status", "external_account_status_check", "read_only_auth_status_check", "credential_state_check", "credential_metadata", "credential_metadata_check", "token_health_check", "run_external_account_auth_status_or_identity_check":
		return AuthorityContract{
			Key:        "data_access",
			LeaseClass: ContinuationLeaseClassDataAccess,
			WorkAction: AuthorityWorkActionReadOnly,
			AllowedActions: []string{
				AuthorityWorkActionReadOnly,
				"request_data_access",
				"read_approved_resource",
				"report_data_access_result",
			},
			ForbiddenActions: []string{
				"silent_data_ingestion",
				"read_unapproved_resource",
				"broad_filesystem_scan",
				"persist_data_without_approval",
				"external_account_access_without_grant",
			},
			ValidationPlan: []string{
				"record resource descriptor, transform, retention, and access result",
				"verify no data was consumed before approval",
			},
			AutoApprovalAllowed:    true,
			RequiresInlineApproval: true,
		}, true
	case "private_data_intake", "resource_owner_data_intake", "resource_owner_profile_intake", "private_profile_intake", "profile_evaluation_rubric", "cv_ingestion", "mailbox_content", "read_mailbox_contents", "run_mailbox_adapter_query", "run_configured_mailbox_adapter_query_once", "read_only_mailbox_smoke", "email_read", "mailbox_read", "external_account_email_read", "external_account_email_read_public_web_read", "external_account", "public_web_read", "private_material_processing", "rank_private_material", "scout_public_opportunities":
		return AuthorityContract{
			Key:        "private_data_intake",
			LeaseClass: ContinuationLeaseClassDataAccess,
			WorkAction: AuthorityWorkActionReadOnly,
			AllowedActions: []string{
				AuthorityWorkActionReadOnly,
				"request_data_access",
				"read_approved_resource",
				"process_approved_private_data",
				"report_data_access_result",
			},
			ForbiddenActions: []string{
				"silent_data_ingestion",
				"read_unapproved_resource",
				"external_account_access_without_grant",
				"email_read_without_grant",
				"cv_ingestion_without_consent",
				"public_contact",
				"application_submission",
			},
			ValidationPlan: []string{
				"verify explicit opt-in or resource descriptor before data intake",
				"record resource descriptor, transform, retention, and access result",
				"stop before external account access or public contact unless separately granted",
			},
			AutoApprovalAllowed:    false,
			RequiresInlineApproval: true,
		}, true
	case "read_only", "read_only_review", "status_check", "inspect_readonly_state":
		return AuthorityContract{
			Key:        "read_only_review",
			LeaseClass: ContinuationLeaseClassLocalWorkspace,
			WorkAction: AuthorityWorkActionReadOnly,
			AllowedActions: []string{
				AuthorityWorkActionReadOnly,
				"read_only_review",
				"status_check",
				"inspect_readonly_state",
				"report_evidence",
			},
			ForbiddenActions: []string{
				"workspace_write",
				"commit",
				"deploy",
				"restart_service",
				"external_effect_without_separate_grant",
			},
			ValidationPlan: []string{
				"report read-only evidence without mutating workspace or live service state",
			},
			AutoApprovalAllowed:    true,
			RequiresInlineApproval: true,
		}, true
	case "workspace_write", "workspace", "code", "code_change", "code_changes", "edit", "edit_files", "patch", "run_tests", "test", "tests":
		return AuthorityContract{
			Key:        "workspace_write",
			LeaseClass: ContinuationLeaseClassLocalWorkspace,
			WorkAction: AuthorityWorkActionWorkspaceWrite,
			AllowedActions: []string{
				AuthorityWorkActionWorkspaceWrite,
				"edit_files",
				"run_tests",
				"git_diff_check",
				"report_evidence",
			},
			ForbiddenActions: []string{
				"commit",
				"deploy",
				"restart_service",
				"git_push",
				"external_effect_without_separate_grant",
			},
			ValidationPlan: []string{
				"show changed files, tests, diff check, and residual risk before asking for broader authority",
			},
			AutoApprovalAllowed:    true,
			RequiresInlineApproval: true,
		}, true
	case "commit", "git_commit", "git_commit_validated_slices", "repo_history_mutation", "git_push", "push_remote":
		return AuthorityContract{
			Key:        "commit",
			LeaseClass: ContinuationLeaseClassLocalWorkspace,
			WorkAction: AuthorityWorkActionCommit,
			AllowedActions: []string{
				AuthorityWorkActionCommit,
				"git_commit",
				"repo_history_mutation",
				"git_diff_check",
				"report_commit_evidence",
			},
			ForbiddenActions: []string{
				"deploy",
				"restart_service",
				"external_effect_without_separate_grant",
			},
			ValidationPlan: []string{
				"verify tests and diff before commit",
				"report commit hashes; push only when git_push is explicitly in the typed allowed actions",
			},
			AutoApprovalAllowed:    true,
			RequiresInlineApproval: true,
		}, true
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart", "restart_aphelion_service", "systemctl_restart", "install_user_service", "make_install_user_service", "run_verify_deploy":
		return AuthorityContract{
			Key:        "deploy",
			LeaseClass: ContinuationLeaseClassDeployRestart,
			WorkAction: AuthorityWorkActionDeploy,
			AllowedActions: []string{
				AuthorityWorkActionDeploy,
				"git_status",
				"review_intended_diff",
				"git_commit_intended_changes",
				"make_build",
				"install_user_service",
				"restart_aphelion_service",
				"run_verify_deploy",
				"prepare_release_handoff",
				"run_explicit_release_step",
				"post_restart_verification",
				"report_release_result",
			},
			ForbiddenActions: []string{
				"deploy_without_handoff",
				"restart_without_recovery_artifact",
				"unbounded_restart_loop",
				"skip_post_deploy_verification",
				"push_or_commit_outside_release_lease",
				"commit_unrelated_changes",
				"skip_build_or_tests_before_restart",
			},
			ValidationPlan: []string{
				"review git status and intended diff before staging",
				"commit only intended repo changes and record the commit hash",
				"build, install the user service, restart the user service, and run verify-deploy",
				"record pre-action git/service state, handoff, post-action status, journal/smoke evidence, and rollback/residual risk",
			},
			AutoApprovalAllowed:    false,
			RequiresInlineApproval: true,
			ExternalEffectsAllowed: true,
		}, true
	case "child_wake", "durable_child_wake", "selected_child_wake", "durable_agent_wake":
		return AuthorityContract{
			Key:        "child_wake",
			LeaseClass: ContinuationLeaseClassChildWake,
			AllowedActions: []string{
				"request_child_wake",
				"wake_named_child",
				"report_child_wake_result",
			},
			ForbiddenActions: []string{
				"wake_unnamed_child",
				"change_child_policy_without_approval",
				"grant_child_capability_without_capability_authority",
				"unbounded_child_wake_loop",
			},
			ValidationPlan: []string{
				"record child agent id, wake count, parent message, and final child state",
			},
			AutoApprovalAllowed:    false,
			RequiresInlineApproval: true,
		}, true
	case "capability_grant", "capability_acquisition", "grant_capability", "grant_set", "capability_authority":
		return AuthorityContract{
			Key:        "capability_grant",
			LeaseClass: ContinuationLeaseClassCapabilityGrant,
			AllowedActions: []string{
				"prepare_capability_request",
				"review_capability_scope",
				"capability_access_check",
				"report_capability_decision",
			},
			ForbiddenActions: []string{
				"treat_lease_as_capability_grant",
				"grant_without_capability_authority",
				"invoke_without_active_capability_grant",
				"broaden_capability_target_silently",
			},
			ValidationPlan: []string{
				"show request id, target resource, allowed actions, and active grant/access-check evidence before invocation",
			},
			AutoApprovalAllowed:    false,
			RequiresInlineApproval: true,
		}, true
	case "external_account_action", "external_account_pr_create", "github_pr_create", "github_pr_open", "github_pr_update", "github_pr_metadata_update", "pull_request_create", "pull_request_open", "pull_request_update", "pull_request_metadata_update", "open_pull_request", "create_github_pr", "update_pull_request_title", "update_pull_request_body":
		return AuthorityContract{
			Key:        "external_account_action",
			LeaseClass: ContinuationLeaseClassCapabilityGrant,
			WorkAction: AuthorityWorkActionReadOnly,
			AllowedActions: []string{
				AuthorityWorkActionReadOnly,
				"capability_access_check",
				"invoke_active_capability_grant",
				"report_capability_result",
			},
			ForbiddenActions: []string{
				"invoke_without_active_capability_grant",
				"broaden_capability_target_silently",
				"credential_token_output",
				"commit",
				"git_commit",
				"repo_history_mutation",
				"git_push",
				"deploy",
				"restart_service",
				"external_account_effect_outside_grant",
			},
			ValidationPlan: []string{
				"show request id, target resource, allowed actions, and active grant/access-check evidence before invocation",
				"perform only the named external-account effect covered by the active grant",
				"report external-account mutation evidence without printing credentials or tokens",
			},
			AutoApprovalAllowed:    false,
			RequiresInlineApproval: true,
			ExternalEffectsAllowed: true,
		}, true
	default:
		return AuthorityContract{}, false
	}
}

func ApplyAuthorityContractToActionProposal(proposal ActionProposal) ActionProposal {
	proposal = ReconcileActionProposalAuthority(SanitizeActionProposalAuthority(NormalizeActionProposal(proposal)))
	compilation := CompileActionProposalAuthorityContract(proposal)
	if strings.TrimSpace(compilation.Contract.Key) == "" {
		return proposal
	}
	proposal.AllowedActions = append(proposal.AllowedActions, compilation.Contract.AllowedActions...)
	proposal.ForbiddenActions = append(proposal.ForbiddenActions, compilation.Contract.ForbiddenActions...)
	proposal.ValidationPlan = append(proposal.ValidationPlan, compilation.Contract.ValidationPlan...)
	if !compilation.Contract.AutoApprovalAllowed {
		autoApproveEligible := false
		proposal.AutoApproveEligible = &autoApproveEligible
	}
	return SanitizeActionProposalAuthority(NormalizeActionProposal(proposal))
}
