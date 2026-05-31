//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func continuationCallbackID(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if id := strings.TrimSpace(state.ActionProposal.ID); id != "" {
		return id
	}
	if id := strings.TrimSpace(state.ContinuationLease.ProposalID); id != "" {
		return id
	}
	if id := strings.TrimSpace(state.ContinuationLease.ID); id != "" {
		return id
	}
	return strings.TrimSpace(state.DecisionID)
}

func continuationApprovalButtonRows(state session.ContinuationState) [][]telegram.InlineButton {
	state = session.NormalizeContinuationState(state)
	decisionID := continuationCallbackID(state)
	if decisionID == "" {
		return nil
	}
	if continuationButtonStateExpired(state) {
		return [][]telegram.InlineButton{
			{
				{Text: "Refresh", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskNextLease)},
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if continuationButtonStateIsPlanLease(state) && state.Status == session.ContinuationStatusApproved {
		return [][]telegram.InlineButton{
			{
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
		return [][]telegram.InlineButton{
			{
				{Text: "Run", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionResumeEdge)},
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Pause", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if state.Status == session.ContinuationStatusPending {
		if continuationStateHasApprovalBundle(state) {
			return [][]telegram.InlineButton{
				{
					{Text: "Approve all", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionApproveBundleAll)},
					{Text: "Approve current", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionApproveBundleCurrent)},
				},
				{
					{Text: "Details", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
					{Text: "Change", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskEdit)},
				},
				{
					{Text: "Pause", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
					{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
				},
			}
		}
		return [][]telegram.InlineButton{
			{
				{Text: "Start", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionApproveLease)},
				{Text: "Details", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Change", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskEdit)},
				{Text: "Pause", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
			},
			{
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	return [][]telegram.InlineButton{
		{
			{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
		},
	}
}

func ContinuationApprovalButtonRows(state session.ContinuationState) [][]telegram.InlineButton {
	return continuationApprovalButtonRows(state)
}

func continuationButtonStateExpired(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return state.ActionProposal.Status == session.ProposalStatusExpired ||
		state.ContinuationLease.Status == session.ContinuationLeaseStatusExpired
}

func continuationButtonStateIsPlanLease(state session.ContinuationState) bool {
	return continuationActionIsPlanLeaseApproval(state)
}

func continuationActionIsPlanLeaseApproval(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return strings.TrimSpace(state.ActionProposal.RiskClass) == "plan_lease" ||
		actionListContains(state.ActionProposal.AllowedActions, "approve_operation_plan_lease") ||
		actionListContains(state.ContinuationLease.AllowedActions, "approve_operation_plan_lease")
}

func continuationApprovalButtonSubject(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	candidates := []string{
		state.ActionProposal.Summary,
		state.StageSummary,
		state.ActionProposal.OperationID,
		state.DecisionID,
		state.ContinuationLease.ProposalID,
		state.ActionProposal.ID,
	}
	for _, candidate := range candidates {
		if subject := compactContinuationPhaseSubject(candidate); subject != "" {
			return subject
		}
	}
	return ""
}

func encodeContinuationCallbackData(decisionID string, action string) string {
	return core.EncodeContinuationCallbackData(decisionID, action)
}

func continuationStateHasApprovalBundle(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return strings.TrimSpace(state.ActionProposal.RiskClass) == "approval_bundle" && state.ApprovalBundle.Active() && len(state.ApprovalBundle.Phases) > 0
}
