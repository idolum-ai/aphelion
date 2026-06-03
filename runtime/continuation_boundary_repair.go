//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

const operationApprovalBoundaryRepairPhaseID = "phase-repair-approval-boundary"

func operationStateWithApprovalBoundaryRepairPlan(opState session.OperationState, blockedPhase session.OperationPhase, reason string, now time.Time) (session.OperationState, bool) {
	opState = session.NormalizeOperationState(opState)
	blockedPhase = normalizeSingleOperationPhase(blockedPhase)
	if !operationPhaseApprovalBlockIsAutoRepairable(opState, blockedPhase, reason) {
		return opState, false
	}

	repairPhase := operationApprovalBoundaryRepairPhase(opState, blockedPhase, reason)
	phases := make([]session.OperationPhase, 0, len(opState.PhasePlan.Phases)+1)
	inserted := false
	blockedID := strings.TrimSpace(blockedPhase.ID)
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if strings.TrimSpace(phase.ID) == blockedID && !inserted {
			phases = append(phases, repairPhase)
			inserted = true
			phase.BlockedReasonCode = ""
			phase.RequiresApproval = true
			phase.Status = session.PlanStatusPending
		}
		phases = append(phases, phase)
	}
	if !inserted {
		phases = append([]session.OperationPhase{repairPhase}, phases...)
	}

	if strings.TrimSpace(opState.PhasePlan.ID) == "" {
		opState.PhasePlan.ID = operationApprovalBoundaryRepairPlanID(opState)
	}
	if strings.TrimSpace(opState.PhasePlan.Goal) == "" {
		opState.PhasePlan.Goal = firstNonEmptyContinuation(opState.Objective, blockedPhase.Summary, "Clarify approval boundary and continue safely")
	}
	opState.PhasePlan.CurrentPhaseID = operationApprovalBoundaryRepairPhaseID
	opState.PhasePlan.Phases = phases
	opState.PhasePlan.UpdatedAt = now
	if opState.Status == "" || opState.Status == session.OperationStatusBlocked {
		opState.Status = session.OperationStatusActive
	}
	opState.Stage = "approval_boundary_repair"
	opState.Summary = operationApprovalBoundaryRepairSummary(blockedPhase, reason)
	return session.NormalizeOperationState(opState), true
}

func operationPhaseApprovalBlockIsAutoRepairable(opState session.OperationState, phase session.OperationPhase, reason string) bool {
	phase = normalizeSingleOperationPhase(phase)
	reason = normalizeOperationPhaseReasonCode(reason)
	if !phase.Active() || phase.Status != session.PlanStatusPending {
		return false
	}
	if strings.TrimSpace(phase.ID) == operationApprovalBoundaryRepairPhaseID || strings.HasPrefix(strings.TrimSpace(phase.ID), operationApprovalBoundaryRepairPhaseID+"-") {
		return false
	}
	if operationPhaseApprovalExcludedReason(opState.PhasePlan, phase) != "" {
		return false
	}
	if reason != "waiting_for_a_clearer_approval_boundary" {
		return false
	}
	if phase.RequiresConsent || phase.RequiresOptIn || len(phase.RequiredCapabilityGrants) > 0 {
		return false
	}
	if operationPhaseHasThirdPartyPrivateDataGate(phase) {
		return false
	}
	if operationPhasePlanBudgetHardStopReason(phase) != "" {
		return false
	}
	return true
}

func operationApprovalBoundaryRepairPhase(opState session.OperationState, blockedPhase session.OperationPhase, reason string) session.OperationPhase {
	blockedTitle := firstNonEmptyContinuation(blockedPhase.Summary, opState.Objective, "the requested work")
	return session.OperationPhase{
		ID:             operationApprovalBoundaryRepairPhaseID,
		Summary:        "Clarify approval boundary for “" + truncatePreview(blockedTitle, 96) + "”",
		Status:         session.PlanStatusPending,
		AuthorityClass: "read_only_review",
		WhyNow:         "The previous phase could not be approved because its resource, action, or stopping point was unclear.",
		BoundedEffect:  "Inspect only the current request, operation state, and relevant local/code context; synthesize a narrower execution proposal or phase plan. Does not execute the blocked work.",
		AllowedActions: []string{
			"inspect_request_context",
			"inspect_operation_state",
			"outline_boundary",
			"request_safe_phase_approval",
			"report_approval_boundary",
			"read_only_review",
		},
		ForbiddenActions: []string{
			"execute_blocked_work",
			"external_account_use",
			"private_content_access",
			"credential_or_token_output",
			"deploy",
			"restart_service",
			"destructive_or_irreversible_action",
			"modify_policy_or_grants",
		},
		ValidationPlan: []string{
			"Name the resource, action, and stopping point needed for execution.",
			"Preserve the original requested work as pending later approval.",
			"Confirm no blocked work or external effect occurred during boundary review.",
		},
		RequiresApproval: true,
	}
}

func operationApprovalBoundaryRepairPlanID(opState session.OperationState) string {
	base := normalizeOperationPhaseReasonCode(firstNonEmptyContinuation(opState.ID, opState.Objective, "operation"))
	if base == "" {
		base = "operation"
	}
	return base + "-approval-boundary-repair-plan"
}

func operationApprovalBoundaryRepairSummary(blockedPhase session.OperationPhase, reason string) string {
	title := firstNonEmptyContinuation(blockedPhase.Summary, blockedPhase.ID, "the requested work")
	return "Approval boundary auto-repair prepared for “" + truncatePreview(title, 96) + "”: " + strings.TrimSpace(reason) + ". The next approval is a read-only review phase only; the original work remains pending later approval."
}
