//go:build linux

package session

import "strings"

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
	if contract, ok := AuthorityContractForToken(riskClass); ok {
		return contract, true
	}
	text := normalizeEnumValue(strings.Join(append(append([]string{}, allowedActions...), riskClass, boundedEffect), " "))
	if text == "" {
		return AuthorityContract{}, false
	}
	containsAny := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(text, normalizeEnumValue(value)) {
				return true
			}
		}
		return false
	}
	switch {
	case containsAny("deploy", "restart", "service_restart", "git_push", "push_remote"):
		return mustAuthorityContract("deploy"), true
	case containsAny("capability_grant", "capability_acquisition", "grant_capability", "grant_set", "capability_authority"):
		return mustAuthorityContract("capability_grant"), true
	case containsAny("child_wake", "durable_child_wake", "selected_child_wake", "durable_agent_wake"):
		return mustAuthorityContract("child_wake"), true
	case containsAny("local_secret_metadata_read", "secret_metadata_read", "live_config_read", "config_metadata_read", "token_file_metadata", "metadata_read"):
		return mustAuthorityContract(AuthorityClassLocalSecretMetadataReadLiveConfigRead), true
	case containsAny("private_data_intake", "wife_profile", "cv_ingestion", "email_read", "mailbox_read", "external_account_email_read", "external_account", "public_web_read", "job_processing", "job_ranking", "job_scouting"):
		return mustAuthorityContract("private_data_intake"), true
	case containsAny("data_access", "file_access", "read_image", "read_file", "consume_attachment", "artifact_read", "network_access"):
		return mustAuthorityContract("data_access"), true
	case containsAny("git_commit", "repo_history_mutation", "commit"):
		return mustAuthorityContract("commit"), true
	case containsAny("workspace_write", "repo_edit", "edit_files", "patch", "run_tests", "focused_tests", "git_diff_check"):
		return mustAuthorityContract("workspace_write"), true
	case containsAny("read_only", "read_only_review", "status_check", "inspect_readonly_state"):
		return mustAuthorityContract("read_only_review"), true
	default:
		return AuthorityContract{}, false
	}
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
				"job_processing",
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
	case "data_access", "file_access", "read_file", "read_image", "consume_attachment", "artifact_read", "network_access":
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
	case "private_data_intake", "wife_profile", "profile_scoring_rubric", "cv_ingestion", "email_read", "mailbox_read", "external_account_email_read", "external_account_email_read_public_web_read", "external_account", "public_web_read", "job_processing", "job_ranking", "job_scouting":
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
	case "commit", "git_commit", "repo_history_mutation":
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
				"git_push",
				"deploy",
				"restart_service",
				"external_effect_without_separate_grant",
			},
			ValidationPlan: []string{
				"verify tests and diff before commit",
				"report commit hashes and do not push without separate authority",
			},
			AutoApprovalAllowed:    true,
			RequiresInlineApproval: true,
		}, true
	case "deploy", "live_deploy", "run_deploy", "system_change", "restart", "restart_service", "service_restart":
		return AuthorityContract{
			Key:        "deploy",
			LeaseClass: ContinuationLeaseClassDeployRestart,
			WorkAction: AuthorityWorkActionDeploy,
			AllowedActions: []string{
				AuthorityWorkActionDeploy,
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
			},
			ValidationPlan: []string{
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
			RequiresInlineApproval: true,
		}, true
	default:
		return AuthorityContract{}, false
	}
}

func ApplyAuthorityContractToActionProposal(proposal ActionProposal) ActionProposal {
	proposal = NormalizeActionProposal(proposal)
	contract, ok := AuthorityContractFor(proposal.RiskClass, proposal.AllowedActions, proposal.BoundedEffect)
	if !ok {
		return proposal
	}
	proposal.AllowedActions = append(proposal.AllowedActions, contract.AllowedActions...)
	proposal.ForbiddenActions = append(proposal.ForbiddenActions, contract.ForbiddenActions...)
	proposal.ValidationPlan = append(proposal.ValidationPlan, contract.ValidationPlan...)
	return NormalizeActionProposal(proposal)
}

func mustAuthorityContract(token string) AuthorityContract {
	contract, ok := AuthorityContractForToken(token)
	if !ok {
		return AuthorityContract{}
	}
	return contract
}
