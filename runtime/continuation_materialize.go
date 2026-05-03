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
	if operationPhasePlanOwnsContinuation(opState.PhasePlan) {
		return true, nil
	}
	proposal := opState.Proposal
	if !pendingOperationProposalNeedsButton(proposal) {
		return false, nil
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

func operationPhasePlanOwnsContinuation(plan session.OperationPhasePlan) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	return len(plan.Phases) > 0
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
	updated := syncOperationPhaseStatusFromContinuation(&opState, state, status)
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
	lines = append(lines, "", fmt.Sprintf("Approve %d bounded turn(s)?", state.RemainingTurns))
	lines = append(lines, "", "Use the buttons instead of typing approval when possible.")
	return strings.Join(lines, "\n")
}
