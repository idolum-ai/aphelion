//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/turn"
)

func (r *Runtime) materializePendingOperationProposalApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, _ *turn.Result) (bool, error) {
	if r == nil || r.store == nil || r.outbound == nil || msg.ChatID == 0 {
		return false, nil
	}
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	if !ok {
		return false, nil
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return false, nil
	}
	opState = session.NormalizeOperationState(opState)
	if bundle, ok := nextOperationPhaseBundleForApproval(opState.PhasePlan); ok {
		now := time.Now().UTC()
		priorState, priorExists, _ := r.store.ContinuationStateIfExists(key)
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPhaseBundleMatchesContinuation(opState, bundle, priorState) {
			return true, nil
		}

		state := continuationStateFromOperationPhaseBundle(opState, bundle, promptInput, now)
		opState = operationStateWithMaterializedPhaseBundleLease(opState, bundle, state, now)
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist operation phase bundle lease state: %w", err)
		}
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return false, fmt.Errorf("persist operation phase bundle continuation state: %w", err)
		}
		payload := continuationExecutionPayload(state)
		payload["materialized_from"] = "operation_phase_bundle"
		payload["phase_plan_id"] = strings.TrimSpace(opState.PhasePlan.ID)
		payload["bundle_phase_count"] = len(bundle)
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		_, err = sender.SendInlineKeyboard(
			ctx,
			msg.ChatID,
			renderOperationProposalMaterializedPromptFallback(state),
			continuationApprovalButtonRows(state),
			nil,
		)
		if err != nil {
			return false, fmt.Errorf("send operation phase bundle continuation approval: %w", err)
		}
		return true, nil
	}
	if phase, ok := nextOperationPhaseForApproval(opState.PhasePlan); ok {
		now := time.Now().UTC()
		priorState, priorExists, _ := r.store.ContinuationStateIfExists(key)
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPhaseMatchesContinuation(opState, phase, priorState) {
			return true, nil
		}

		state := continuationStateFromOperationPhase(opState, phase, promptInput, now)
		opState = operationStateWithMaterializedPhaseLease(opState, phase.ID, state, now)
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist operation phase lease state: %w", err)
		}
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return false, fmt.Errorf("persist operation phase continuation state: %w", err)
		}
		payload := continuationExecutionPayload(state)
		payload["materialized_from"] = "operation_phase_plan"
		payload["phase_plan_id"] = strings.TrimSpace(opState.PhasePlan.ID)
		payload["phase_id"] = strings.TrimSpace(phase.ID)
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		_, err = sender.SendInlineKeyboard(
			ctx,
			msg.ChatID,
			renderOperationProposalMaterializedPromptFallback(state),
			continuationApprovalButtonRows(state),
			nil,
		)
		if err != nil {
			return false, fmt.Errorf("send operation phase continuation approval: %w", err)
		}
		return true, nil
	}
	proposal := opState.Proposal
	if !pendingOperationProposalNeedsButton(proposal) {
		if operationPhasePlanOwnsContinuation(opState.PhasePlan) {
			return true, nil
		}
		return false, nil
	}
	if operationPhasePlanOwnsContinuation(opState.PhasePlan) && operationProposalBelongsToPhasePlan(opState, proposal) {
		return true, nil
	}
	priorState, priorExists, _ := r.store.ContinuationStateIfExists(key)
	priorState = session.NormalizeContinuationState(priorState)
	if priorExists && priorState.Status == session.ContinuationStatusPending && operationProposalMatchesContinuation(proposal, priorState) {
		return true, nil
	}

	now := time.Now().UTC()
	state := continuationStateFromOperationProposal(opState, promptInput, now)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return false, fmt.Errorf("persist operation proposal continuation state: %w", err)
	}
	payload := continuationExecutionPayload(state)
	payload["materialized_from"] = "operation_proposal"
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
	_, err = sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		renderOperationProposalMaterializedPromptFallback(state),
		continuationApprovalButtonRows(state),
		nil,
	)
	if err != nil {
		return false, fmt.Errorf("send operation proposal continuation approval: %w", err)
	}
	return true, nil
}

func pendingOperationProposalNeedsButton(proposal session.OperationProposal) bool {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	return proposal.Active() && proposal.Status == session.ProposalStatusPending && strings.TrimSpace(proposal.ID) != "" && strings.TrimSpace(proposal.Summary) != ""
}

func operationProposalMatchesContinuation(proposal session.OperationProposal, state session.ContinuationState) bool {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	state = session.NormalizeContinuationState(state)
	proposalID := strings.TrimSpace(proposal.ID)
	if proposalID == "" {
		return false
	}
	return strings.TrimSpace(state.ActionProposal.OperationID) == proposalID || strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-") == proposalID || strings.TrimSpace(state.DecisionID) == proposalID
}

func nextOperationPhaseForApproval(plan session.OperationPhasePlan) (session.OperationPhase, bool) {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}, false
	}
	for _, phase := range plan.Phases {
		if phase.Status == session.PlanStatusInProgress {
			return session.OperationPhase{}, false
		}
	}
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for _, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentID && operationPhaseNeedsApproval(phase) {
				return phase, true
			}
		}
	}
	for _, phase := range plan.Phases {
		if operationPhaseNeedsApproval(phase) {
			return phase, true
		}
	}
	return session.OperationPhase{}, false
}

const operationApprovalBundleMaxPhases = 3

func nextOperationPhaseBundleForApproval(plan session.OperationPhasePlan) ([]session.OperationPhase, bool) {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if len(plan.Phases) < 2 {
		return nil, false
	}
	for _, phase := range plan.Phases {
		if phase.Status == session.PlanStatusInProgress {
			return nil, false
		}
	}
	start := 0
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for i, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				start = i
				break
			}
		}
	}
	bundle := make([]session.OperationPhase, 0, operationApprovalBundleMaxPhases)
	for i := start; i < len(plan.Phases) && len(bundle) < operationApprovalBundleMaxPhases; i++ {
		phase := normalizeSingleOperationPhase(plan.Phases[i])
		if phase.Status == session.PlanStatusCompleted {
			continue
		}
		if !operationPhaseNeedsApproval(phase) {
			break
		}
		if operationPhaseRequiresFreshApprovalGate(phase) {
			break
		}
		bundle = append(bundle, phase)
	}
	if len(bundle) < 2 && start > 0 {
		bundle = bundle[:0]
		for i := 0; i < len(plan.Phases) && len(bundle) < operationApprovalBundleMaxPhases; i++ {
			phase := normalizeSingleOperationPhase(plan.Phases[i])
			if phase.Status == session.PlanStatusCompleted {
				continue
			}
			if !operationPhaseNeedsApproval(phase) || operationPhaseRequiresFreshApprovalGate(phase) {
				break
			}
			bundle = append(bundle, phase)
		}
	}
	if len(bundle) < 2 {
		return nil, false
	}
	return bundle, true
}

func operationPhaseRequiresFreshApprovalGate(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	values := []string{phase.AuthorityClass}
	values = append(values, phase.AllowedActions...)
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		switch value {
		case "deploy", "live_deploy", "run_deploy", "restart", "restart_service", "service_restart", "system_change", "policy_apply", "grant_or_revoke_capability", "capability_grant", "capability_revoke", "mailbox_access", "mailbox_mutation", "credential_access", "read_credentials_or_tokens", "external_account_action", "external_effect", "purchase", "spend", "public_contact", "public_posting", "communication":
			return true
		}
		if strings.Contains(value, "credential") || strings.Contains(value, "token") || strings.Contains(value, "mailbox") || strings.Contains(value, "policy") || strings.Contains(value, "grant") || strings.Contains(value, "purchase") || strings.Contains(value, "spend") || strings.Contains(value, "public_contact") {
			return true
		}
	}
	return false
}

func operationPhasePlanOwnsContinuation(plan session.OperationPhasePlan) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	return len(plan.Phases) > 0
}

func operationProposalBelongsToPhasePlan(opState session.OperationState, proposal session.OperationProposal) bool {
	opState = session.NormalizeOperationState(opState)
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	proposalID := strings.TrimSpace(proposal.ID)
	if proposalID == "" || len(opState.PhasePlan.Phases) == 0 {
		return false
	}
	for _, phase := range opState.PhasePlan.Phases {
		if proposalID == operationPhaseProposalID(opState, phase) {
			return true
		}
	}
	return false
}

func operationPhaseNeedsApproval(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if !phase.Active() {
		return false
	}
	if phase.Status != session.PlanStatusPending {
		return false
	}
	if phase.RequiresApproval {
		return true
	}
	return strings.TrimSpace(phase.Summary) != "" ||
		strings.TrimSpace(phase.AuthorityClass) != "" ||
		strings.TrimSpace(phase.BoundedEffect) != "" ||
		len(phase.AllowedActions) > 0 ||
		len(phase.ForbiddenActions) > 0 ||
		len(phase.ValidationPlan) > 0
}

func operationPhaseMatchesContinuation(opState session.OperationState, phase session.OperationPhase, state session.ContinuationState) bool {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	proposalID := operationPhaseProposalID(opState, phase)
	if proposalID == "" {
		return false
	}
	if strings.TrimSpace(state.ActionProposal.OperationID) == proposalID ||
		strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-") == proposalID ||
		strings.TrimSpace(state.DecisionID) == proposalID {
		return true
	}
	leaseID := strings.TrimSpace(phase.LeaseID)
	return leaseID != "" && leaseID == strings.TrimSpace(state.ContinuationLease.ID)
}

func operationPhaseBundleMatchesContinuation(opState session.OperationState, phases []session.OperationPhase, state session.ContinuationState) bool {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	bundleID := operationPhaseBundleID(opState, phases)
	if bundleID == "" {
		return false
	}
	if strings.TrimSpace(state.ApprovalBundle.ID) == bundleID || strings.TrimSpace(state.ActionProposal.OperationID) == bundleID || strings.TrimSpace(state.DecisionID) == bundleID {
		return true
	}
	return false
}

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
		Summary:          nextStep,
		WhyNow:           whyNow,
		BoundedEffect:    boundedEffect,
		RiskClass:        strongestPhaseAuthorityClass(bundlePhases),
		AllowedActions:   []string{"execute_approved_bundle_phases_sequentially", "use_existing_authority_only", "update_operation_phase_plan", "report_evidence_after_each_phase"},
		ForbiddenActions: []string{"expand_authority_without_new_approval", "execute_phase_outside_bundle", "skip_stop_gate", "silent_continuation_past_report"},
		ValidationPlan:   []string{"execute only named bundle phases", "preserve per-phase provenance", "stop when a hard gate or out-of-bundle phase is reached"},
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
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

func continuationStateFromOperationPhase(opState session.OperationState, phase session.OperationPhase, promptInput string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	decisionID := operationPhaseProposalID(opState, phase)
	if decisionID == "" {
		decisionID = newContinuationDecisionID()
	}
	objective := firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, opState.Summary, phase.Summary, summarizeContinuationFallback(promptInput))
	nextStep := firstNonEmptyContinuation(phase.Summary, phase.BoundedEffect, opState.Stage, "Take the next approved phase, then report evidence.")
	boundedEffect := firstNonEmptyContinuation(phase.BoundedEffect, "Execute this phase only, update the durable phase plan, and stop after the evidence report.")
	whyNow := firstNonEmptyContinuation(phase.WhyNow, "This durable phase plan has a pending phase that needs explicit approval before execution.")
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: 1,
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A durable phase-plan lease is ready for button-backed approval.",
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
	action := session.ActionProposal{
		ID:               "aprop-" + decisionID,
		OperationID:      decisionID,
		Summary:          nextStep,
		WhyNow:           whyNow,
		BoundedEffect:    boundedEffect,
		RiskClass:        firstNonEmptyContinuation(phase.AuthorityClass, "continuation"),
		AllowedActions:   append([]string(nil), phase.AllowedActions...),
		ForbiddenActions: append([]string(nil), phase.ForbiddenActions...),
		ValidationPlan:   append([]string(nil), phase.ValidationPlan...),
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
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
	action.PlanHash = actionProposalHash(action)
	state.ActionProposal = session.NormalizeActionProposal(action)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	return session.NormalizeContinuationState(state)
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
	actionProposal = applyOrganicRalphSandbox(actionProposal, opState, proposal)
	actionProposal = applyGoalContinuationSandbox(actionProposal, opState, proposal)
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
	updated := syncOperationBundlePhaseStatusFromContinuation(&opState, state, status)
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
		opState.Status = session.OperationStatusActive
	} else if status == session.ProposalStatusDenied || status == session.ProposalStatusExpired || status == session.ProposalStatusSuperseded {
		opState.Status = session.OperationStatusBlocked
	}
	opState.UpdatedAt = time.Now().UTC()
	_ = r.store.UpdateOperationState(key, opState)
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

func renderOperationProposalMaterializedPromptFallback(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	lines := []string{"Approval needed."}
	if summary := strings.TrimSpace(proposal.Summary); summary != "" {
		lines = append(lines, "", "Lease:", summary)
	}
	if why := strings.TrimSpace(proposal.WhyNow); why != "" {
		lines = append(lines, "", "Why now:", why)
	}
	if effect := strings.TrimSpace(proposal.BoundedEffect); effect != "" {
		lines = append(lines, "", "Bounded effect:", effect)
	}
	if bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle); len(bundle.Phases) > 0 {
		lines = append(lines, "", "Bundle phases:")
		for _, phase := range bundle.Phases {
			label := fmt.Sprintf("%d", phase.Index)
			if summary := strings.TrimSpace(phase.Summary); summary != "" {
				label += ". " + summary
			}
			if authority := strings.TrimSpace(phase.AuthorityClass); authority != "" {
				label += " [" + authority + "]"
			}
			lines = append(lines, "- "+label)
		}
	}
	lines = append(lines, "", fmt.Sprintf("Approve %d bounded turn(s)?", state.RemainingTurns))
	lines = append(lines, "", "Use the buttons instead of typing approval when possible.")
	return strings.Join(lines, "\n")
}
