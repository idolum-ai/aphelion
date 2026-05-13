//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func continuationStateFromOperationPlanLease(opState session.OperationState, lease session.OperationPlanLease, promptInput string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	lease = session.NormalizeOperationPlanLease(lease)
	decisionID := operationPlanLeaseProposalID(lease)
	if decisionID == "" {
		decisionID = newContinuationDecisionID()
	}
	turns := lease.RemainingTurns
	if turns <= 0 {
		turns = lease.TurnBudget
	}
	if turns <= 0 {
		turns = len(lease.Lanes)
	}
	if turns <= 0 {
		turns = 1
	}
	objective := firstNonEmptyContinuation(lease.Objective, opState.Objective, opState.Summary, lease.Summary, summarizeContinuationFallback(promptInput))
	nextStep := firstNonEmptyContinuation(lease.Summary, lease.Objective, "Approve a bounded plan lease.")
	boundedEffect := operationPlanLeaseBoundedEffect(lease)
	whyNow := "This broad plan needs a button-backed bounded envelope before the runtime can execute multiple leased lanes."
	if opState.Stage != "" {
		whyNow = "Operation stage " + strings.TrimSpace(opState.Stage) + " requires a button-backed bounded plan lease."
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: turns,
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A bounded plan lease is ready for explicit approval.",
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
	if phases := operationPlanLeasePhasesFromOperation(opState, lease); len(phases) > 0 {
		bundlePhases := continuationApprovalBundlePhasesFromOperation(opState, phases)
		state.ApprovalBundle = session.ContinuationApprovalBundle{
			ID:             decisionID,
			Status:         session.ContinuationLeaseStatusPending,
			CurrentPhaseID: firstContinuationBundlePhaseID(bundlePhases),
			Phases:         bundlePhases,
			ExpiresAt:      now.Add(continuationLeaseDefaultTTL),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	action := session.ActionProposal{
		ID:               "aprop-" + decisionID,
		OperationID:      strings.TrimSpace(lease.ID),
		MissionID:        strings.TrimSpace(lease.MissionID),
		OperatorTitle:    firstNonEmptyContinuation(lease.OperatorTitle, lease.PlanTitle, continuationPlanTitleFromText(nextStep), continuationPlanTitleFromText(objective)),
		PlanTitle:        firstNonEmptyContinuation(lease.PlanTitle, lease.OperatorTitle, continuationPlanTitleFromText(objective), continuationPlanTitleFromText(nextStep)),
		Summary:          nextStep,
		WhyNow:           whyNow,
		BoundedEffect:    boundedEffect,
		RiskClass:        "plan_lease",
		AllowedActions:   operationPlanLeaseAllowedActions(lease),
		ForbiddenActions: operationPlanLeaseForbiddenActions(lease),
		ValidationPlan:   operationPlanLeaseValidationPlan(lease),
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if operationPlanLeaseContainsEscalatedLane(opState, lease) {
		value := false
		action.AutoApproveEligible = &value
	}
	action.PlanHash = actionProposalHash(action)
	state.ActionProposal = session.NormalizeActionProposal(action)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, turns, now)
	return session.NormalizeContinuationState(state)
}

func operationPlanLeaseContainsEscalatedLane(opState session.OperationState, lease session.OperationPlanLease) bool {
	for _, phase := range operationPlanLeasePhasesFromOperation(opState, lease) {
		if operationPhaseApprovalGate(phase).Level == operationGateLevelEscalatedOperatorApproval {
			return true
		}
		if operationPhaseApprovalKindFor(phase) == operationPhaseApprovalFresh && operationPhaseFreshGateCanJoinPlanBudget(phase) {
			return true
		}
	}
	return false
}

func operationPlanLeasePhasesFromOperation(opState session.OperationState, lease session.OperationPlanLease) []session.OperationPhase {
	opState = session.NormalizeOperationState(opState)
	lease = session.NormalizeOperationPlanLease(lease)
	if len(opState.PhasePlan.Phases) == 0 || len(lease.CoveredPhaseIDs) == 0 {
		return nil
	}
	covered := make(map[string]struct{}, len(lease.CoveredPhaseIDs))
	for _, id := range lease.CoveredPhaseIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			covered[trimmed] = struct{}{}
		}
	}
	out := make([]session.OperationPhase, 0, len(covered))
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if _, ok := covered[strings.TrimSpace(phase.ID)]; !ok {
			continue
		}
		if operationPhaseRequiresFreshApprovalGate(phase) && !operationPhaseFreshGateCanJoinPlanBudget(phase) {
			continue
		}
		out = append(out, phase)
	}
	return out
}

func operationPlanLeaseProposalID(lease session.OperationPlanLease) string {
	lease = session.NormalizeOperationPlanLease(lease)
	base := firstNonEmptyContinuation(lease.OperationID, lease.ID, lease.Summary, "plan-lease")
	id := sanitizeOperationPhaseProposalID("plan-lease-" + base)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func operationPlanLeaseBoundedEffect(lease session.OperationPlanLease) string {
	lease = session.NormalizeOperationPlanLease(lease)
	parts := []string{"Work inside this approved plan budget only; stop for hard gates or anything outside the disclosed lanes."}
	if lease.TurnBudget > 0 {
		parts = append(parts, fmt.Sprintf("turn_budget=%d", lease.TurnBudget))
	}
	if lease.RemainingTurns > 0 {
		parts = append(parts, fmt.Sprintf("remaining_turns=%d", lease.RemainingTurns))
	}
	for _, lane := range lease.Lanes {
		label := firstNonEmptyContinuation(lane.ID, lane.Summary, "lane")
		detail := strings.TrimSpace(label)
		if authority := strings.TrimSpace(lane.AuthorityClass); authority != "" {
			detail += " " + authority
		}
		if lane.ExpectedTurns > 0 {
			detail += fmt.Sprintf(" %d turn(s)", lane.ExpectedTurns)
		}
		if summary := strings.TrimSpace(lane.Summary); summary != "" && summary != label {
			detail += ": " + summary
		}
		parts = append(parts, "lane "+detail)
	}
	if len(lease.ValidationGates) > 0 {
		parts = append(parts, "validation gates: "+strings.Join(lease.ValidationGates, "; "))
	}
	if len(lease.ExitConditions) > 0 {
		parts = append(parts, "exit conditions: "+strings.Join(lease.ExitConditions, "; "))
	}
	return strings.Join(parts, " | ")
}

func operationPlanLeaseAllowedActions(lease session.OperationPlanLease) []string {
	lease = session.NormalizeOperationPlanLease(lease)
	actions := []string{
		"approve_operation_plan_lease",
		"record_plan_lease_approval",
		"use_plan_lease_as_bounded_envelope",
		"require_separate_capability_grant_for_external_effects",
		"report_plan_lease_evidence_digest",
	}
	actions = append(actions, authorityContractWorkActionForToken(lease.AllowedActions...)...)
	actions = append(actions, lease.AllowedActions...)
	for _, lane := range lease.Lanes {
		actions = append(actions, authorityContractWorkActionForToken(lane.AuthorityClass)...)
		actions = append(actions, authorityContractWorkActionForToken(lane.AllowedActions...)...)
		actions = append(actions, lane.AllowedActions...)
	}
	return session.NormalizeActionProposal(session.ActionProposal{AllowedActions: actions}).AllowedActions
}

func authorityContractWorkActionForToken(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		contract, ok := session.AuthorityContractForToken(value)
		if !ok || strings.TrimSpace(contract.WorkAction) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(contract.WorkAction))
	}
	return out
}

func operationPlanLeaseForbiddenActions(lease session.OperationPlanLease) []string {
	lease = session.NormalizeOperationPlanLease(lease)
	actions := []string{
		"treat_plan_lease_as_capability_grant",
		"activate_unapproved_autonomous_work",
		"bypass_lane_authority",
		"bypass_hard_interrupt",
		"deploy_or_restart_without_parking",
		"grant_or_revoke_capability",
		"mailbox_access_without_separate_grant",
		"external_effect_without_separate_grant",
	}
	actions = append(actions, lease.ForbiddenActions...)
	actions = append(actions, lease.HardInterrupts...)
	for _, lane := range lease.Lanes {
		actions = append(actions, lane.ForbiddenActions...)
	}
	return session.NormalizeActionProposal(session.ActionProposal{ForbiddenActions: actions}).ForbiddenActions
}

func operationPlanLeaseValidationPlan(lease session.OperationPlanLease) []string {
	lease = session.NormalizeOperationPlanLease(lease)
	plan := []string{
		"verify every leased lane declares authority_class and expected_turns",
		"stop and ask for a separate grant at any hard interrupt",
		"do not treat plan approval as tool, capability, deploy, or restart authority",
		"record milestone evidence before proposing follow-up authority",
	}
	plan = append(plan, lease.ValidationGates...)
	plan = append(plan, lease.ExitConditions...)
	return session.NormalizeActionProposal(session.ActionProposal{ValidationPlan: plan}).ValidationPlan
}

func operationStateWithMaterializedPlanLease(opState session.OperationState, state session.ContinuationState, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	opState.Status = session.OperationStatusBlocked
	opState.Stage = "plan_lease_approval"
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
	if opState.PlanLease.Status == "" {
		opState.PlanLease.Status = session.PlanLeaseStatusProposed
	}
	covered := make(map[string]struct{}, len(opState.PlanLease.CoveredPhaseIDs))
	for _, id := range opState.PlanLease.CoveredPhaseIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			covered[trimmed] = struct{}{}
		}
	}
	if len(covered) > 0 {
		firstCovered := ""
		for i := range opState.PhasePlan.Phases {
			phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
			if _, ok := covered[phaseID]; !ok {
				continue
			}
			opState.PhasePlan.Phases[i].LeaseID = strings.TrimSpace(state.ContinuationLease.ID)
			if firstCovered == "" {
				firstCovered = phaseID
			}
		}
		if firstCovered != "" {
			opState.PhasePlan.CurrentPhaseID = firstCovered
			opState.PhasePlan.UpdatedAt = now
		}
	}
	opState.PlanLease.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}
