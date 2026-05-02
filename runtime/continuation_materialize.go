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
	if r == nil || r.store == nil || r.outbound == nil || msg.ChatID == 0 || msg.Origin == core.InboundOriginTurnAuthorization {
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
		continuationApprovalButtonRows(continuationCallbackID(state)),
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
	actionProposal.PlanHash = actionProposalHash(actionProposal)
	return session.NormalizeActionProposal(actionProposal)
}

func (r *Runtime) syncOperationProposalStatusFromContinuation(key session.SessionKey, state session.ContinuationState, status session.ProposalStatus) {
	if r == nil || r.store == nil || status == "" {
		return
	}
	state = session.NormalizeContinuationState(state)
	opID := strings.TrimSpace(state.ActionProposal.OperationID)
	if opID == "" {
		return
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return
	}
	opState = session.NormalizeOperationState(opState)
	if strings.TrimSpace(opState.Proposal.ID) != opID || opState.Proposal.Status != session.ProposalStatusPending {
		return
	}
	opState.Proposal.Status = status
	opState.Proposal.UpdatedAt = time.Now().UTC()
	if status == session.ProposalStatusApproved {
		opState.Status = session.OperationStatusActive
	} else if status == session.ProposalStatusDenied {
		opState.Status = session.OperationStatusBlocked
	}
	_ = r.store.UpdateOperationState(key, opState)
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
