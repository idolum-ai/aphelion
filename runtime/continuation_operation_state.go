//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func continuationStateFromOperationPhaseBundle(opState session.OperationState, phases []session.OperationPhase, promptInput string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	bundleID := operationPhaseBundleID(opState, phases)
	if bundleID == "" {
		bundleID = newContinuationDecisionID()
	}
	bundlePhases := continuationApprovalBundlePhasesFromOperation(opState, phases)
	objective := firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, opState.Summary, summarizeContinuationFallback(promptInput))
	nextStep := operationPhaseBundleSummary(bundlePhases)
	if nextStep == "" {
		nextStep = "Approve multiple named phases, then execute them sequentially with stop gates."
	}
	boundedEffect := operationPhaseBundleBoundedEffect(bundlePhases)
	whyNow := "This durable phase plan has multiple bounded phases that can be approved together without approving hard-stop escalation gates."
	if len(phases) > 0 && strings.TrimSpace(phases[0].WhyNow) != "" {
		whyNow = strings.TrimSpace(phases[0].WhyNow)
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     bundleID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: len(bundlePhases),
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A multi-phase approval bundle is ready for button-backed approval.",
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
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             bundleID,
			Status:         session.ContinuationLeaseStatusPending,
			CurrentPhaseID: firstContinuationBundlePhaseID(bundlePhases),
			Phases:         bundlePhases,
			ExpiresAt:      now.Add(continuationLeaseDefaultTTL),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		UpdatedAt: now,
	}
	action := session.ActionProposal{
		ID:               "aprop-" + bundleID,
		OperationID:      bundleID,
		OperatorTitle:    continuationPlanTitleFromText(nextStep),
		PlanTitle:        continuationPlanTitleFromText(firstNonEmptyContinuation(opState.PhasePlan.Goal, objective, nextStep)),
		Summary:          nextStep,
		WhyNow:           whyNow,
		BoundedEffect:    boundedEffect,
		RiskClass:        strongestPhaseAuthorityClass(bundlePhases),
		AllowedActions:   []string{"execute_approved_bundle_phases_sequentially", "use_existing_authority_only", "update_operation_phase_plan", "report_milestone_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval", "execute_phase_outside_bundle", "skip_stop_gate", "credentials_or_tokens", "external_send_or_contact", "archive_delete_or_mutate_source_data", "deploy_restart_without_explicit_approval"},
		ValidationPlan:   []string{"execute only named bundle phases", "preserve per-phase provenance", "report evidence at meaningful milestones and completion", "stop when a hard gate or out-of-bundle phase is reached"},
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action = applyContinuationLeaseClassBoundaries(action)
	action.PlanHash = actionProposalHash(action)
	state.ActionProposal = session.NormalizeActionProposal(action)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, len(bundlePhases), now)
	return session.NormalizeContinuationState(state)
}

func continuationApprovalBundlePhasesFromOperation(opState session.OperationState, phases []session.OperationPhase) []session.ContinuationApprovalBundlePhase {
	opState = session.NormalizeOperationState(opState)
	planIndexes := make(map[string]int, len(opState.PhasePlan.Phases))
	for i, phase := range opState.PhasePlan.Phases {
		if id := strings.TrimSpace(phase.ID); id != "" {
			planIndexes[id] = i + 1
		}
	}
	out := make([]session.ContinuationApprovalBundlePhase, 0, len(phases))
	for i, phase := range phases {
		phase = normalizeSingleOperationPhase(phase)
		id := operationPhaseProposalID(opState, phase)
		phaseIndex := planIndexes[strings.TrimSpace(phase.ID)]
		if phaseIndex <= 0 {
			phaseIndex = i + 1
		}
		out = append(out, session.ContinuationApprovalBundlePhase{
			ID:               id,
			OperationPhaseID: strings.TrimSpace(phase.ID),
			Index:            phaseIndex,
			OperatorTitle:    firstNonEmptyContinuation(phase.OperatorTitle, phase.PlanTitle, continuationPlanTitleFromText(phase.Summary)),
			PlanTitle:        firstNonEmptyContinuation(phase.PlanTitle, phase.OperatorTitle, continuationPlanTitleFromText(phase.Summary)),
			Summary:          strings.TrimSpace(phase.Summary),
			AuthorityClass:   strings.TrimSpace(phase.AuthorityClass),
			WhyNow:           strings.TrimSpace(phase.WhyNow),
			BoundedEffect:    strings.TrimSpace(phase.BoundedEffect),
			AllowedActions:   append([]string(nil), phase.AllowedActions...),
			ForbiddenActions: append([]string(nil), phase.ForbiddenActions...),
			ValidationPlan:   append([]string(nil), phase.ValidationPlan...),
			Status:           session.ContinuationLeaseStatusPending,
		})
	}
	return out
}

func operationPhaseBundleID(opState session.OperationState, phases []session.OperationPhase) string {
	opState = session.NormalizeOperationState(opState)
	if len(phases) == 0 {
		return ""
	}
	base := firstNonEmptyContinuation(opState.ID, opState.PhasePlan.ID, "operation")
	firstID := firstNonEmptyContinuation(phases[0].ID, phases[0].Summary, "first")
	lastID := firstNonEmptyContinuation(phases[len(phases)-1].ID, phases[len(phases)-1].Summary, "last")
	id := sanitizeOperationPhaseProposalID("bundle-" + base + "-" + firstID + "-to-" + lastID)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func operationPhaseBundleSummary(phases []session.ContinuationApprovalBundlePhase) string {
	if len(phases) == 0 {
		return ""
	}
	first := phases[0].Index
	last := phases[len(phases)-1].Index
	parts := make([]string, 0, len(phases))
	for _, phase := range phases {
		if summary := strings.TrimSpace(phase.Summary); summary != "" {
			parts = append(parts, summary)
		}
	}
	prefix := fmt.Sprintf("Approve stages %d–%d", first, last)
	if len(parts) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(parts, " → ")
}

func operationPhaseBundleBoundedEffect(phases []session.ContinuationApprovalBundlePhase) string {
	parts := make([]string, 0, len(phases)+1)
	for _, phase := range phases {
		label := fmt.Sprintf("phase %d", phase.Index)
		if summary := strings.TrimSpace(phase.Summary); summary != "" {
			label += " " + summary
		}
		if effect := strings.TrimSpace(phase.BoundedEffect); effect != "" {
			parts = append(parts, label+": "+effect)
		} else {
			parts = append(parts, label)
		}
	}
	parts = append(parts, "Stop before any phase not named in this bundle or any hard escalation gate.")
	return strings.Join(parts, " | ")
}

func firstContinuationBundlePhaseID(phases []session.ContinuationApprovalBundlePhase) string {
	for _, phase := range phases {
		if id := strings.TrimSpace(phase.ID); id != "" {
			return id
		}
	}
	return ""
}

func strongestPhaseAuthorityClass(phases []session.ContinuationApprovalBundlePhase) string {
	best := "continuation_bundle"
	for _, phase := range phases {
		mode := workModeFromStructuredAuthority(phase.AuthorityClass)
		switch mode {
		case WorkModeDeploy:
			return "deploy"
		case WorkModeWorkspaceWrite:
			best = "workspace_write"
		case WorkModeReadOnly:
			if best == "continuation_bundle" {
				best = "read_only_review"
			}
		}
	}
	return best
}

func continuationStateFromOperationProposal(opState session.OperationState, promptInput string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	proposal := opState.Proposal
	decisionID := strings.TrimSpace(proposal.ID)
	if decisionID == "" {
		decisionID = newContinuationDecisionID()
	}
	objective := firstNonEmptyContinuation(opState.Objective, opState.Summary, proposal.Summary, summarizeContinuationFallback(promptInput))
	nextStep := firstNonEmptyContinuation(proposal.Summary, proposal.BoundedEffect, opState.Stage, "Take one approved bounded step, then report evidence.")
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: 1,
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A bounded lease is ready for button-backed approval.",
			NextStep:   nextStep,
			Confidence: "high",
			UpdatedAt:  now,
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:    session.ContinuationIntentDecisionContinue,
			Rationale:   firstNonEmptyContinuation(proposal.WhyNow, "The proposal is pending and needs explicit approval before execution."),
			NextStep:    nextStep,
			Constraints: firstNonEmptyContinuation(proposal.BoundedEffect, "Stay inside the bounded proposal and stop after the evidence report."),
			Confidence:  "high",
			Ratified:    true,
			UpdatedAt:   now,
		},
		UpdatedAt: now,
	}
	state.ActionProposal = actionProposalFromOperationProposal(opState, proposal, decisionID, now)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	return session.NormalizeContinuationState(state)
}

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
	actions = append(actions, lease.AllowedActions...)
	for _, lane := range lease.Lanes {
		actions = append(actions, lane.AllowedActions...)
	}
	return session.NormalizeActionProposal(session.ActionProposal{AllowedActions: actions}).AllowedActions
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
	return session.NormalizeContinuationState(state)
}

func operationPhaseIsDeployRestartPhase(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect) == session.ContinuationLeaseClassDeployRestart {
		return true
	}
	return operationPhasePlanBudgetHardStopReason(phase) == "deploy/restart"
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

func actionProposalFromOperationProposal(opState session.OperationState, proposal session.OperationProposal, decisionID string, now time.Time) session.ActionProposal {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	proposalID := strings.TrimSpace(proposal.ID)
	actionID := "aprop-" + strings.TrimSpace(decisionID)
	if proposalID != "" {
		actionID = "aprop-" + proposalID
	}
	actionProposal := session.ActionProposal{
		ID:               actionID,
		OperationID:      proposalID,
		OperatorTitle:    firstNonEmptyContinuation(proposal.OperatorTitle, proposal.PlanTitle, continuationPlanTitleFromText(proposal.Summary), continuationPlanTitleFromText(opState.Objective)),
		PlanTitle:        firstNonEmptyContinuation(proposal.PlanTitle, proposal.OperatorTitle, continuationPlanTitleFromText(opState.Objective), continuationPlanTitleFromText(proposal.Summary)),
		Summary:          firstNonEmptyContinuation(proposal.Summary, opState.Stage, opState.Objective),
		WhyNow:           firstNonEmptyContinuation(proposal.WhyNow, "This pending lease requires explicit approval."),
		BoundedEffect:    firstNonEmptyContinuation(proposal.BoundedEffect, "Execute one bounded step under the pending lease, then report evidence."),
		RiskClass:        firstNonEmptyContinuation(proposal.Kind, "continuation"),
		AllowedActions:   []string{"execute_bounded_proposal_once", "use_existing_authority_only", "report_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval", "exceed_bounded_effect", "silent_continuation_past_report"},
		ValidationPlan:   []string{"verify the action stays within the bounded effect", "report evidence and residual risk"},
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	actionProposal = applyOrganicProposalSandbox(actionProposal, opState, proposal)
	actionProposal = applyGoalContinuationSandbox(actionProposal, opState, proposal)
	actionProposal = applyContinuationLeaseClassBoundaries(actionProposal)
	actionProposal.PlanHash = actionProposalHash(actionProposal)
	return session.NormalizeActionProposal(actionProposal)
}

func operationStateWithMaterializedPhaseBundleLease(opState session.OperationState, phases []session.OperationPhase, state session.ContinuationState, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	phaseIDs := make(map[string]struct{}, len(phases))
	for _, phase := range phases {
		phaseIDs[strings.TrimSpace(phase.ID)] = struct{}{}
	}
	opState.Status = session.OperationStatusBlocked
	opState.Stage = "bundle_approval"
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
	firstPhaseID := ""
	for i := range opState.PhasePlan.Phases {
		phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
		if _, ok := phaseIDs[phaseID]; !ok {
			continue
		}
		if firstPhaseID == "" {
			firstPhaseID = phaseID
		}
		opState.PhasePlan.Phases[i].LeaseID = strings.TrimSpace(state.ContinuationLease.ID)
		if opState.PhasePlan.Phases[i].Status == "" {
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		}
	}
	if firstPhaseID != "" {
		opState.PhasePlan.CurrentPhaseID = firstPhaseID
	}
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
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

func (r *Runtime) syncOperationProposalStatusFromContinuation(key session.SessionKey, state session.ContinuationState, status session.ProposalStatus) {
	if r == nil || r.store == nil || status == "" {
		return
	}
	state = session.NormalizeContinuationState(state)
	opID := strings.TrimSpace(state.ActionProposal.OperationID)
	if opID == "" && strings.TrimSpace(state.ActionProposal.ID) == "" && strings.TrimSpace(state.DecisionID) == "" && strings.TrimSpace(state.ContinuationLease.ID) == "" {
		return
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return
	}
	opState = session.NormalizeOperationState(opState)
	planLeaseUpdated := syncOperationPlanLeaseStatusFromContinuation(&opState, state, status)
	updated := planLeaseUpdated
	if syncOperationBundlePhaseStatusFromContinuation(&opState, state, status) {
		updated = true
	}
	if syncOperationPhaseStatusFromContinuation(&opState, state, status) {
		updated = true
	}
	if strings.TrimSpace(opState.Proposal.ID) == opID && opState.Proposal.Status == session.ProposalStatusPending {
		opState.Proposal.Status = status
		opState.Proposal.UpdatedAt = time.Now().UTC()
		updated = true
	}
	if !updated {
		return
	}
	if status == session.ProposalStatusApproved {
		if planLeaseUpdated && continuationActionIsPlanLeaseApproval(state) {
			if state.ApprovalBundle.Active() {
				opState.Status = session.OperationStatusActive
				opState.Stage = "plan_lease_active"
			} else {
				opState.Status = session.OperationStatusBlocked
				opState.Stage = "plan_lease_approved"
			}
		} else {
			opState.Status = session.OperationStatusActive
		}
	} else if status == session.ProposalStatusDenied || status == session.ProposalStatusExpired || status == session.ProposalStatusSuperseded {
		opState.Status = session.OperationStatusBlocked
	}
	opState.UpdatedAt = time.Now().UTC()
	_ = r.store.UpdateOperationState(key, opState)
}

func syncOperationPlanLeaseStatusFromContinuation(opState *session.OperationState, state session.ContinuationState, status session.ProposalStatus) bool {
	if opState == nil {
		return false
	}
	*opState = session.NormalizeOperationState(*opState)
	state = session.NormalizeContinuationState(state)
	if !continuationActionIsPlanLeaseApproval(state) {
		return false
	}
	leaseID := strings.TrimSpace(state.ActionProposal.OperationID)
	if leaseID == "" {
		leaseID = strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-plan-lease-")
	}
	if leaseID == "" || strings.TrimSpace(opState.PlanLease.ID) != leaseID {
		return false
	}
	now := time.Now().UTC()
	switch status {
	case session.ProposalStatusApproved:
		if state.ApprovalBundle.Active() {
			opState.PlanLease.Status = session.PlanLeaseStatusActive
		} else {
			opState.PlanLease.Status = session.PlanLeaseStatusApproved
		}
		opState.PlanLease.ApprovedBy = firstNonZeroInt64(state.ContinuationLease.ApprovedBy, state.ApprovedBy)
		if !state.ContinuationLease.ApprovedAt.IsZero() {
			opState.PlanLease.ApprovedAt = state.ContinuationLease.ApprovedAt.UTC()
		} else {
			opState.PlanLease.ApprovedAt = now
		}
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = session.ProposalStatusApproved
			opState.Proposal.UpdatedAt = now
		}
	case session.ProposalStatusDenied:
		opState.PlanLease.Status = session.PlanLeaseStatusRevoked
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = session.ProposalStatusDenied
			opState.Proposal.UpdatedAt = now
		}
	case session.ProposalStatusExpired, session.ProposalStatusSuperseded:
		opState.PlanLease.Status = session.PlanLeaseStatusExpired
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = status
			opState.Proposal.UpdatedAt = now
		}
	}
	opState.PlanLease.UpdatedAt = now
	opState.UpdatedAt = now
	*opState = session.NormalizeOperationState(*opState)
	return true
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func syncOperationBundlePhaseStatusFromContinuation(opState *session.OperationState, state session.ContinuationState, status session.ProposalStatus) bool {
	if opState == nil {
		return false
	}
	*opState = session.NormalizeOperationState(*opState)
	state = session.NormalizeContinuationState(state)
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if strings.TrimSpace(bundle.ID) == "" || len(bundle.Phases) == 0 {
		return false
	}
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	currentPhaseID := strings.TrimSpace(bundle.CurrentPhaseID)
	if currentPhaseID == "" {
		currentPhaseID = firstContinuationBundlePhaseID(bundle.Phases)
	}
	bundleIDs := make(map[string]session.ContinuationApprovalBundlePhase, len(bundle.Phases))
	for _, phase := range bundle.Phases {
		if id := strings.TrimSpace(phase.OperationPhaseID); id != "" {
			bundleIDs[id] = phase
		}
	}
	if len(bundleIDs) == 0 {
		return false
	}
	updated := false
	for i := range opState.PhasePlan.Phases {
		phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
		bundlePhase, ok := bundleIDs[phaseID]
		if !ok {
			continue
		}
		switch status {
		case session.ProposalStatusApproved:
			opState.PhasePlan.Phases[i].LeaseID = leaseID
			if strings.TrimSpace(bundlePhase.ID) == currentPhaseID || currentPhaseID == "" {
				opState.PhasePlan.Phases[i].Status = session.PlanStatusInProgress
				opState.PhasePlan.CurrentPhaseID = phaseID
			} else if opState.PhasePlan.Phases[i].Status == "" {
				opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
			}
		case session.ProposalStatusDenied, session.ProposalStatusExpired, session.ProposalStatusSuperseded:
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
			opState.PhasePlan.Phases[i].LeaseID = ""
			if opState.PhasePlan.CurrentPhaseID == "" {
				opState.PhasePlan.CurrentPhaseID = phaseID
			}
		}
		updated = true
	}
	if updated {
		opState.PhasePlan.UpdatedAt = time.Now().UTC()
		*opState = session.NormalizeOperationState(*opState)
	}
	return updated
}

func syncOperationPhaseStatusFromContinuation(opState *session.OperationState, state session.ContinuationState, status session.ProposalStatus) bool {
	if opState == nil {
		return false
	}
	*opState = session.NormalizeOperationState(*opState)
	state = session.NormalizeContinuationState(state)
	opID := strings.TrimSpace(state.ActionProposal.OperationID)
	actionID := strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-")
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	updated := false
	for i := range opState.PhasePlan.Phases {
		phase := opState.PhasePlan.Phases[i]
		proposalID := operationPhaseProposalID(*opState, phase)
		if proposalID == "" {
			continue
		}
		matches := opID == proposalID || actionID == proposalID || strings.TrimSpace(state.DecisionID) == proposalID
		if !matches && leaseID != "" {
			matches = strings.TrimSpace(phase.LeaseID) == leaseID
		}
		if !matches {
			continue
		}
		switch status {
		case session.ProposalStatusApproved:
			opState.PhasePlan.Phases[i].Status = session.PlanStatusInProgress
			opState.PhasePlan.Phases[i].LeaseID = leaseID
			opState.PhasePlan.CurrentPhaseID = opState.PhasePlan.Phases[i].ID
		case session.ProposalStatusDenied, session.ProposalStatusExpired, session.ProposalStatusSuperseded:
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
			opState.PhasePlan.Phases[i].LeaseID = ""
			opState.PhasePlan.CurrentPhaseID = opState.PhasePlan.Phases[i].ID
		}
		opState.PhasePlan.UpdatedAt = time.Now().UTC()
		updated = true
		break
	}
	if updated {
		*opState = session.NormalizeOperationState(*opState)
	}
	return updated
}
