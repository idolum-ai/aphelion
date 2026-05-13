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
