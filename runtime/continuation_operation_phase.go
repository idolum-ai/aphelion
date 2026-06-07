//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func continuationStateFromOperationPhase(opState session.OperationState, phase session.OperationPhase, promptInput string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	gate := operationPhaseApprovalGate(phase)
	decisionID := operationPhaseProposalID(opState, phase)
	if decisionID == "" {
		decisionID = newContinuationDecisionID()
	}
	objective := firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, opState.Summary, phase.Summary, summarizeContinuationFallback(promptInput))
	nextStep := firstNonEmptyContinuation(phase.Summary, phase.BoundedEffect, opState.Stage, "Take the next approved phase, then report evidence.")
	boundedEffect := firstNonEmptyContinuation(phase.BoundedEffect, "Execute this phase only, update the durable phase plan, and stop after the evidence report.")
	whyNow := firstNonEmptyContinuation(phase.WhyNow, "This durable phase plan has a pending phase that needs explicit approval before execution.")
	deployPhase := operationPhaseIsDeployRestartPhase(phase)
	if deployPhase {
		nextStep = firstNonEmptyContinuation(phase.Summary, "Commit, build, install, restart, and verify the service.")
		boundedEffect = deployPhaseBoundedEffect(boundedEffect)
		whyNow = firstNonEmptyContinuation(phase.WhyNow, "Deploy/restart authority is a hard gate and needs explicit operator approval.")
	}
	personaRationale := "A durable phase-plan lease is ready for button-backed approval."
	if gate.Level == operationGateLevelEscalatedOperatorApproval {
		personaRationale = "An escalated operator approval is required before this sensitive bounded phase can run."
	}
	if deployPhase {
		personaRationale = "A deploy/restart phase requires explicit operator approval before it can run."
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: 1,
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  personaRationale,
			NextStep:   nextStep,
			Confidence: "high",
			UpdatedAt:  now,
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:    session.ContinuationIntentDecisionContinue,
			Rationale:   whyNow,
			NextStep:    nextStep,
			Constraints: boundedEffect,
			Confidence:  "high",
			Ratified:    true,
			UpdatedAt:   now,
		},
		UpdatedAt: now,
	}
	riskClass := firstNonEmptyContinuation(phase.AuthorityClass, "continuation")
	if gate.Level == operationGateLevelEscalatedOperatorApproval && gate.ReasonCode != "" {
		riskClass = gate.ReasonCode
	}
	if deployPhase && riskClass == "continuation" {
		riskClass = "deploy"
	}
	riskClass = operationPhaseRiskClassForContinuationAction(phase, riskClass)
	action := session.ActionProposal{
		ID:               "aprop-" + decisionID,
		OperationID:      decisionID,
		OperatorTitle:    firstNonEmptyContinuation(phase.OperatorTitle, phase.PlanTitle, continuationPlanTitleFromText(nextStep), continuationPlanTitleFromText(objective)),
		PlanTitle:        firstNonEmptyContinuation(phase.PlanTitle, phase.OperatorTitle, continuationPlanTitleFromText(objective), continuationPlanTitleFromText(nextStep)),
		Summary:          nextStep,
		WhyNow:           whyNow,
		BoundedEffect:    boundedEffect,
		RiskClass:        riskClass,
		AllowedActions:   append([]string(nil), phase.AllowedActions...),
		ForbiddenActions: append([]string(nil), phase.ForbiddenActions...),
		ValidationPlan:   append([]string(nil), phase.ValidationPlan...),
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if gate.Level == operationGateLevelEscalatedOperatorApproval || phase.AutoApproveEligible != nil {
		value := gate.AutoApproveEligible
		action.AutoApproveEligible = &value
	}
	if deployPhase {
		action = applyDeployPhaseContract(action)
		value := false
		action.AutoApproveEligible = &value
	}
	if len(action.AllowedActions) == 0 {
		action.AllowedActions = []string{"execute_phase_once", "use_existing_authority_only", "update_operation_phase_plan", "report_evidence"}
	}
	if len(action.ForbiddenActions) == 0 {
		action.ForbiddenActions = []string{"expand_authority_without_new_approval", "exceed_phase_bounded_effect", "skip_phase_plan_update", "silent_continuation_past_report"}
	}
	if len(action.ValidationPlan) == 0 {
		action.ValidationPlan = []string{"verify the action stays within the phase bounded effect", "update operation phase status and report evidence"}
	}
	action = applyContinuationLeaseClassBoundaries(action)
	action.PlanHash = actionProposalHash(action)
	state.ActionProposal = session.NormalizeActionProposal(action)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	state.ContinuationLease.RequiredCapabilityGrants = append([]session.CapabilityGrantSpec(nil), phase.RequiredCapabilityGrants...)
	return session.NormalizeContinuationState(state)
}

func operationPhaseIsDeployRestartPhase(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect) == session.ContinuationLeaseClassDeployRestart {
		return true
	}
	return operationPhasePlanBudgetHardStopReason(phase) == "deploy/restart"
}

func operationPhaseRiskClassForContinuationAction(phase session.OperationPhase, fallback string) string {
	phase = normalizeSingleOperationPhase(phase)
	if operationPhaseHasExternalAccountGrant(phase) && !operationPhaseAllowsLocalRepoMutation(phase) {
		return "external_account_action"
	}
	return strings.TrimSpace(fallback)
}

func operationPhaseHasExternalAccountGrant(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	for _, grant := range phase.RequiredCapabilityGrants {
		if grant.Kind == session.CapabilityKindExternalAccount {
			return true
		}
	}
	return false
}

func operationPhaseAllowsLocalRepoMutation(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	switch workModeFromStructuredAuthorityList(phase.AllowedActions) {
	case WorkModeWorkspaceWrite, WorkModeCommit, WorkModeDeploy:
		return true
	default:
		return false
	}
}

func deployPhaseBoundedEffect(current string) string {
	current = strings.TrimSpace(current)
	required := "Commit only intended repo changes, build the binary, install the user service, restart the user service, and run verify-deploy; stop before push or unrelated changes."
	lower := strings.ToLower(current)
	if strings.Contains(lower, "commit") &&
		strings.Contains(lower, "build") &&
		strings.Contains(lower, "install") &&
		strings.Contains(lower, "restart") &&
		(strings.Contains(lower, "verify-deploy") || strings.Contains(lower, "verify deploy")) {
		return current
	}
	if current == "" {
		return required
	}
	return current + " " + required
}

func applyDeployPhaseContract(action session.ActionProposal) session.ActionProposal {
	action = session.NormalizeActionProposal(action)
	if strings.TrimSpace(action.RiskClass) == "" || strings.TrimSpace(action.RiskClass) == "continuation" {
		action.RiskClass = "deploy"
	}
	action.AllowedActions = append(action.AllowedActions,
		"git_status",
		"review_intended_diff",
		"git_commit_intended_changes",
		"make_build",
		"install_user_service",
		"restart_aphelion_service",
		"run_verify_deploy",
		"prepare_release_handoff",
		"post_restart_verification",
		"report_release_result",
	)
	action.ForbiddenActions = append(action.ForbiddenActions,
		"commit_unrelated_changes",
		"push_remote",
		"deploy_without_handoff",
		"restart_without_recovery_artifact",
		"skip_build_or_tests_before_restart",
		"skip_post_deploy_verification",
		"unbounded_restart_loop",
	)
	action.ValidationPlan = append(action.ValidationPlan,
		"record pre-deploy git status and intended diff",
		"run go test ./..., go vet ./..., and git diff --check before commit",
		"commit only intended changes and record the commit hash",
		"run make build, make install-user-service, and verify-deploy after restart",
	)
	return session.NormalizeActionProposal(action)
}

func operationStateWithMaterializedPhaseLease(opState session.OperationState, phaseID string, state session.ContinuationState, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	phaseID = strings.TrimSpace(phaseID)
	opState.Status = session.OperationStatusBlocked
	opState.Stage = "phase_approval"
	opState.Proposal = session.OperationProposal{
		ID:            strings.TrimSpace(state.ActionProposal.OperationID),
		Kind:          strings.TrimSpace(state.ActionProposal.RiskClass),
		OperatorTitle: strings.TrimSpace(state.ActionProposal.OperatorTitle),
		PlanTitle:     strings.TrimSpace(state.ActionProposal.PlanTitle),
		Summary:       strings.TrimSpace(state.ActionProposal.Summary),
		WhyNow:        strings.TrimSpace(state.ActionProposal.WhyNow),
		BoundedEffect: strings.TrimSpace(state.ActionProposal.BoundedEffect),
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	for i := range opState.PhasePlan.Phases {
		if strings.TrimSpace(opState.PhasePlan.Phases[i].ID) != phaseID {
			continue
		}
		if operationPhaseIsDeployRestartPhase(opState.PhasePlan.Phases[i]) {
			opState.Stage = "deploy_approval"
		}
		opState.PhasePlan.Phases[i].LeaseID = strings.TrimSpace(state.ContinuationLease.ID)
		if opState.PhasePlan.Phases[i].Status == "" {
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		}
		opState.PhasePlan.CurrentPhaseID = opState.PhasePlan.Phases[i].ID
		break
	}
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}

func normalizeSingleOperationPhase(phase session.OperationPhase) session.OperationPhase {
	plan := session.NormalizeOperationState(session.OperationState{PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{phase}}}).PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}
	}
	return plan.Phases[0]
}

func operationPhaseProposalID(opState session.OperationState, phase session.OperationPhase) string {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	base := firstNonEmptyContinuation(opState.ID, opState.PhasePlan.ID, "operation")
	phaseID := firstNonEmptyContinuation(phase.ID, phase.Summary, "phase")
	id := sanitizeOperationPhaseProposalID("phase-" + base + "-" + phaseID)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func sanitizeOperationPhaseProposalID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
