//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func operationPhaseRequiresFreshApprovalGate(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	gate := operationPhaseApprovalGate(phase)
	if gate.Level == operationGateLevelEscalatedOperatorApproval || gate.Level == operationGateLevelHardConsentBlock {
		return true
	}
	if class := session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect); class != "" && class != session.ContinuationLeaseClassLocalWorkspace {
		return true
	}
	return phase.RequiresApproval || operationPhaseHasStructuredCode(phase,
		"deploy",
		"live_deploy",
		"run_deploy",
		"restart",
		"restart_service",
		"service_restart",
		"restart_aphelion_service",
		"systemctl_restart",
		"park_restart",
		"install_user_service",
		"make_install_user_service",
		"reinstall",
		"system_change",
		"policy_apply",
		"grant_or_revoke_capability",
		"capability_grant",
		"capability_revoke",
		"capability_access_check",
		"mailbox_access",
		"mailbox_mutation",
		"mailbox_read",
		"email_read",
		"external_account_email_read",
		"external_account_email_read_public_web_read",
		"mailbox_content",
		"read_mailbox_contents",
		"run_mailbox_adapter_query",
		"run_configured_mailbox_adapter_query_once",
		"read_only_mailbox_smoke",
		"credential_access",
		"credential_metadata",
		"credential_metadata_check",
		"read_credentials_or_tokens",
		"token_health_check",
		"external_account_action",
		"external_account",
		"external_account_auth_status",
		"read_only_auth_status_check",
		"public_account_content_read",
		"public_profile_metadata_read",
		"public_web_read",
		"network_access",
		"data_access",
		"private_data_intake",
		"resource_owner_data_intake",
		"resource_owner_profile_intake",
		"private_profile_intake",
		"profile_evaluation_rubric",
		"cv_ingestion",
		"private_material_processing",
		"rank_private_material",
		"scout_public_opportunities",
		"purchase",
		"spend",
		"public_contact",
		"public_posting",
		"communication",
		"commit",
		"git_commit",
		"repo_history_mutation",
		"workspace_commit_then_repo_write_bounded",
		"push",
		"git_push",
		"push_remote",
	)
}

func operationPhaseIsPlanningOnlyApproval(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if !operationPhaseHasPlanMaterial(phase) {
		return false
	}
	if len(phase.AllowedActions) > 0 {
		hasPlanningAction := false
		hasConcreteAction := false
		for _, action := range phase.AllowedActions {
			if operationPhaseActionIsPlanningOnly(action) {
				hasPlanningAction = true
				continue
			}
			if operationPhaseActionIsConcrete(action) {
				hasConcreteAction = true
			}
		}
		if hasPlanningAction && !hasConcreteAction {
			return true
		}
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join(operationPhasePlanningTextParts(phase), " ")))
	if text == "" {
		return false
	}
	if operationPhaseTextIsPlanningOnly(text) && !operationPhaseTextHasConcreteExecution(text) {
		return true
	}
	return false
}

func operationPhasePlanningTextParts(phase session.OperationPhase) []string {
	parts := []string{phase.Summary, phase.WhyNow, phase.BoundedEffect}
	parts = append(parts, phase.ValidationPlan...)
	return parts
}

func operationPhaseActionIsPlanningOnly(action string) bool {
	value := strings.ToLower(strings.TrimSpace(action))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "draft_plan", "draft_repair_plan", "draft_repair_phases", "make_plan", "make_a_plan", "plan", "planning", "propose_plan", "propose_repair_plan", "propose_repair_phases", "update_operation_phase_plan":
		return true
	default:
		return strings.Contains(value, "draft") && strings.Contains(value, "plan") ||
			strings.Contains(value, "propose") && strings.Contains(value, "phase") ||
			strings.Contains(value, "make") && strings.Contains(value, "plan")
	}
}

func operationPhaseActionIsConcrete(action string) bool {
	value := strings.ToLower(strings.TrimSpace(action))
	if value == "" {
		return false
	}
	if workModeFromStructuredAuthority(value) != "" {
		return true
	}
	for _, token := range []string{
		"inspect", "read", "review", "edit", "patch", "write_file", "run_test", "test", "build", "install", "commit", "deploy", "restart", "migrate", "repair", "execute", "verify", "smoke",
	} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func operationPhaseTextIsPlanningOnly(text string) bool {
	patterns := []string{
		"make a plan",
		"make plan",
		"draft a plan",
		"draft plan",
		"draft repair plan",
		"draft repair phases",
		"repair planning",
		"planning phase",
		"propose a plan",
		"propose repair phases",
		"turn child diagnostic failures into explicit repair phases",
		"turn failures into explicit repair phases",
		"turn findings into phases",
		"turn issues into phases",
		"convert findings into phases",
		"convert issues into phases",
	}
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return strings.HasPrefix(text, "plan ") || strings.HasPrefix(text, "draft ")
}

func operationPhaseTextHasConcreteExecution(text string) bool {
	for _, token := range []string{
		"edit files", "patch", "run tests", "go test", "build", "install", "commit", "deploy", "restart", "migrate", "repair state", "write artifact", "verify", "smoke test", "inspect state",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}
