//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

const operationApprovalBoundaryPlanPhasePrefix = "phase-clarify-approval-boundary"

func operationStateWithApprovalBoundaryDeliberationPlan(opState session.OperationState, blockedPhase session.OperationPhase, reason string, now time.Time) (session.OperationState, bool) {
	opState = session.NormalizeOperationState(opState)
	blockedPhase = normalizeSingleOperationPhase(blockedPhase)
	if !operationPhaseApprovalBlockCanEnterDeliberation(opState, blockedPhase, reason) {
		return opState, false
	}

	firstPhase := operationApprovalBoundaryDeliberationFirstPhase(opState, blockedPhase, reason)
	phases := make([]session.OperationPhase, 0, len(opState.PhasePlan.Phases)+1)
	inserted := false
	blockedID := strings.TrimSpace(blockedPhase.ID)
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if strings.TrimSpace(phase.ID) == blockedID && !inserted {
			phases = append(phases, firstPhase)
			inserted = true
			phase.BlockedReasonCode = ""
			phase.RequiresApproval = true
			phase.Status = session.PlanStatusPending
		}
		phases = append(phases, phase)
	}
	if !inserted {
		phases = append([]session.OperationPhase{firstPhase}, phases...)
	}

	if strings.TrimSpace(opState.PhasePlan.ID) == "" {
		opState.PhasePlan.ID = operationApprovalBoundaryPlanID(opState)
	}
	if strings.TrimSpace(opState.PhasePlan.Goal) == "" {
		opState.PhasePlan.Goal = firstNonEmptyContinuation(opState.Objective, blockedPhase.Summary, "Clarify approval boundary and continue safely")
	}
	opState.PhasePlan.CurrentPhaseID = firstPhase.ID
	opState.PhasePlan.Phases = phases
	opState.PhasePlan.UpdatedAt = now
	if opState.Status == "" || opState.Status == session.OperationStatusBlocked {
		opState.Status = session.OperationStatusActive
	}
	opState.Stage = "approval_request"
	opState.Summary = operationApprovalBoundaryDeliberationSummary(blockedPhase, reason)
	return session.NormalizeOperationState(opState), true
}

func operationPhaseApprovalBlockCanEnterDeliberation(opState session.OperationState, phase session.OperationPhase, reason string) bool {
	phase = normalizeSingleOperationPhase(phase)
	reason = normalizeOperationPhaseReasonCode(reason)
	kind, ok := continuationCompileRepairForReason(reason)
	if !ok || kind != continuationCompileRepairApprovalBoundary {
		return false
	}
	return operationCompileRepairAnchorCanEnter(opState, phase, kind)
}

func operationApprovalBoundaryDeliberationFirstPhase(opState session.OperationState, blockedPhase session.OperationPhase, reason string) session.OperationPhase {
	blockedTitle := firstNonEmptyContinuation(blockedPhase.Summary, opState.Objective, "the requested work")
	return session.OperationPhase{
		ID:             operationApprovalBoundaryFirstPhaseID(blockedPhase),
		Summary:        "Clarify approval boundary for “" + truncatePreview(blockedTitle, 96) + "”",
		Status:         session.PlanStatusPending,
		AuthorityClass: "read_only_review",
		WhyNow:         "Internal deliberation received an unclear approval-boundary blocker and split the work into a safe first slice plus later pending work.",
		BoundedEffect:  "Inspect only the current request, operation state, and relevant local/code context; name the resource, action, and stopping point for a narrower execution phase. Does not execute the blocked work.",
		AllowedActions: []string{
			"inspect_request_context",
			"inspect_operation_state",
			"outline_boundary",
			"propose_normal_phase_plan",
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

func operationApprovalBoundaryFirstPhaseID(blockedPhase session.OperationPhase) string {
	base := normalizeOperationPhaseReasonCode(firstNonEmptyContinuation(blockedPhase.ID, blockedPhase.Summary, "phase"))
	if base == "" {
		return operationApprovalBoundaryPlanPhasePrefix
	}
	id := operationApprovalBoundaryPlanPhasePrefix + "-for-" + base
	if len(id) <= 96 {
		return id
	}
	return strings.TrimRight(id[:96], "-_")
}

func operationApprovalBoundaryPlanID(opState session.OperationState) string {
	base := normalizeOperationPhaseReasonCode(firstNonEmptyContinuation(opState.ID, opState.Objective, "operation"))
	if base == "" {
		base = "operation"
	}
	return base + "-approval-boundary-plan"
}

func operationApprovalBoundaryDeliberationSummary(blockedPhase session.OperationPhase, reason string) string {
	title := firstNonEmptyContinuation(blockedPhase.Summary, blockedPhase.ID, "the requested work")
	return "Unclear approval-boundary blocker routed through deliberation for “" + truncatePreview(title, 96) + "”: " + strings.TrimSpace(reason) + ". The next approval is a normal read-only first slice; the original work remains pending later approval."
}
