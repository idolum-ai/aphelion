//go:build linux

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/stoplabels"
	"github.com/idolum-ai/aphelion/session"
)

const continuationCallbackPrefix = core.ContinuationCallbackPrefix
const staleContinuationCallbackText = "This continuation prompt is no longer active. Use the newest prompt."
const continuationCallbackFailureText = "Continuation action failed. Check /health diagnose for details."

const (
	continuationActionApproveLease = "approve_lease"
	continuationActionContinueOnce = "continue_once"
	continuationActionAskEdit      = "ask_edit"
	continuationActionStop         = "stop"
	continuationActionStopPark     = "stop_park"
	continuationActionResumeEdge   = "resume_edge"
	continuationActionAskNextLease = "ask_next_lease"
	continuationActionStatusOnly   = "status_only"
)

func encodeContinuationCallbackData(decisionID string, action string) string {
	decisionID = strings.TrimSpace(decisionID)
	action = normalizeContinuationCallbackAction(action)
	if action == "" {
		return ""
	}
	return core.EncodeContinuationCallbackData(decisionID, action)
}

func decodeContinuationCallbackData(data string) (decisionID string, action string, ok bool) {
	decisionID, action, ok = core.DecodeContinuationCallbackData(data)
	if !ok {
		return "", "", false
	}
	action = normalizeContinuationCallbackAction(action)
	if action == "" {
		return "", "", false
	}
	return decisionID, action, true
}

func normalizeContinuationCallbackAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case continuationActionApproveLease, "approve-lease":
		return continuationActionApproveLease
	case continuationActionContinueOnce, "continue-once":
		return continuationActionContinueOnce
	case continuationActionAskEdit, "ask-edit", "edit":
		return continuationActionAskEdit
	case continuationActionStop:
		return continuationActionStop
	case continuationActionStopPark, "stop-park", "park":
		return continuationActionStopPark
	case continuationActionResumeEdge, "resume-edge", "resume":
		return continuationActionResumeEdge
	case continuationActionAskNextLease, "ask-next-lease", "next_lease", "next-lease":
		return continuationActionAskNextLease
	case continuationActionStatusOnly, "status-only", "status":
		return continuationActionStatusOnly
	default:
		return ""
	}
}

func continuationCallbackMatchesState(state session.ContinuationState, decisionID string, action string) bool {
	state = session.NormalizeContinuationState(state)
	decisionID = strings.TrimSpace(decisionID)
	action = normalizeContinuationCallbackAction(action)
	if decisionID == "" || action == "" {
		return false
	}
	if !continuationCallbackIDMatchesState(state, decisionID) {
		return false
	}
	switch action {
	case continuationActionApproveLease, continuationActionContinueOnce:
		return state.Status == session.ContinuationStatusPending && state.RemainingTurns > 0
	case continuationActionAskEdit:
		return state.Status == session.ContinuationStatusPending
	case continuationActionStop, continuationActionStopPark:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved || continuationCallbackStateExpired(state)
	case continuationActionResumeEdge:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	case continuationActionAskNextLease, continuationActionStatusOnly:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved || continuationCallbackStateExpired(state)
	default:
		return false
	}
}

func continuationCallbackStateExpired(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return state.ActionProposal.Status == session.ProposalStatusExpired ||
		state.ContinuationLease.Status == session.ContinuationLeaseStatusExpired
}

func continuationCallbackIDMatchesState(state session.ContinuationState, decisionID string) bool {
	state = session.NormalizeContinuationState(state)
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" {
		return false
	}
	ids := []string{
		strings.TrimSpace(state.DecisionID),
		strings.TrimSpace(state.ActionProposal.ID),
		strings.TrimSpace(state.ContinuationLease.ID),
		strings.TrimSpace(state.ContinuationLease.ProposalID),
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if decisionID == id || decisionID == core.ContinuationCallbackAlias(id) {
			return true
		}
	}
	return false
}

func renderContinuationDecision(state session.ContinuationState, action string) string {
	state = session.NormalizeContinuationState(state)
	switch normalizeContinuationCallbackAction(action) {
	case continuationActionApproveLease:
		return renderContinuationApprovedDecision(state, "Continuation lease approved.")
	case continuationActionContinueOnce:
		return renderContinuationApprovedDecision(state, "Continuing once under the approved lease.")
	case continuationActionAskEdit:
		return "Continuation lease needs edits. I parked this prompt; no continuation was approved or started."
	case continuationActionAskNextLease:
		return renderContinuationEdgeStatus(state, "Next lease needed.")
	case continuationActionStatusOnly:
		if state.Status == session.ContinuationStatusPending {
			return renderContinuationScopeDetails(state, "Lease scope details.")
		}
		return renderContinuationEdgeStatus(state, "Continuation status only.")
	case continuationActionResumeEdge:
		if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
			return renderContinuationApprovedDecision(state, "Resuming the approved edge.")
		}
		return renderContinuationEdgeStatus(state, "Resume edge needs an approved lease first.")
	default:
		return renderContinuationEdgeStatus(state, "Continuation decision recorded.")
	}
}

func continuationCallbackErrorText(err error) string {
	switch {
	case errors.Is(err, core.ErrContinuationExpired):
		return "That continuation lease expired before it could be approved."
	case errors.Is(err, core.ErrContinuationNotPending), errors.Is(err, core.ErrContinuationNoTurns), errors.Is(err, core.ErrContinuationStale):
		return staleContinuationCallbackText
	default:
		return continuationCallbackFailureText
	}
}

func renderContinuationCallbackError(state session.ContinuationState, err error) string {
	switch {
	case errors.Is(err, core.ErrContinuationExpired):
		return renderContinuationEdgeStatus(state, "Continuation lease expired before approval.")
	case errors.Is(err, core.ErrContinuationNotPending), errors.Is(err, core.ErrContinuationNoTurns), errors.Is(err, core.ErrContinuationStale):
		return renderContinuationEdgeStatus(state, "Continuation prompt is no longer active.")
	default:
		return renderContinuationEdgeStatus(state, "Continuation action failed.")
	}
}

func renderContinuationRefreshedDecision(state session.ContinuationState) string {
	return renderContinuationEdgeStatus(state, "Continuation lease expired before approval. I sent a fresh approval prompt.")
}

func renderContinuationRefreshAlreadyActiveDecision(state session.ContinuationState) string {
	return renderContinuationEdgeStatus(state, "Continuation lease expired before approval. A fresh approval prompt is already active.")
}

func renderContinuationApprovedDecision(state session.ContinuationState, prefix string) string {
	text := strings.TrimSpace(prefix)
	if text == "" {
		text = "Continuation approved."
	}
	if state.RemainingTurns > 0 {
		text += fmt.Sprintf(" Remaining turns: %d.", state.RemainingTurns)
	}
	if state.StageSummary != "" {
		text += " Next: " + state.StageSummary
	}
	return text
}

func renderContinuationEdgeStatus(state session.ContinuationState, prefix string) string {
	state = session.NormalizeContinuationState(state)
	lines := []string{strings.TrimSpace(prefix)}
	if lines[0] == "" {
		lines[0] = "Continuation edge."
	}
	if state.Status != "" {
		lines = append(lines, "Status: "+string(state.Status))
	}
	if state.Objective != "" {
		lines = append(lines, "Objective: "+state.Objective)
	}
	if state.StageSummary != "" {
		lines = append(lines, "Next: "+state.StageSummary)
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Remaining turns: %d", state.RemainingTurns))
	}
	if state.HandshakeBlockedReason != "" {
		lines = append(lines, "Blocked reason: "+state.HandshakeBlockedReason)
	}
	lines = append(lines, "No new authority was granted by this status view.")
	return strings.Join(lines, "\n")
}

func renderContinuationScopeDetails(state session.ContinuationState, prefix string) string {
	state = session.NormalizeContinuationState(state)
	if continuationStateIsPlanBudget(state) {
		return renderContinuationPlanBudgetDetails(state, prefix)
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	lines := []string{strings.TrimSpace(prefix)}
	if lines[0] == "" {
		lines[0] = "Lease scope details."
	}
	if state.Status != "" {
		lines = append(lines, "Status: "+string(state.Status))
	}
	if state.Objective != "" {
		lines = append(lines, "Objective: "+state.Objective)
	}
	if state.StageSummary != "" {
		lines = append(lines, "Next: "+state.StageSummary)
	}
	if proposal.Summary != "" {
		lines = append(lines, "Proposal: "+proposal.Summary)
	}
	if proposal.WhyNow != "" {
		lines = append(lines, "Why now: "+proposal.WhyNow)
	}
	if proposal.BoundedEffect != "" {
		lines = append(lines, "Bounded effect: "+proposal.BoundedEffect)
	}
	if allowed := firstNonEmptyContinuationCommandList(proposal.AllowedActions, lease.AllowedActions); len(allowed) > 0 {
		lines = append(lines, "Allowed actions: "+strings.Join(allowed, ", "))
	}
	if forbidden := firstNonEmptyContinuationCommandList(proposal.ForbiddenActions, lease.ForbiddenActions); len(forbidden) > 0 {
		lines = append(lines, "Forbidden actions: "+strings.Join(forbidden, ", "))
	}
	if validation := firstNonEmptyContinuationCommandList(proposal.ValidationPlan, lease.ValidationPlan); len(validation) > 0 {
		lines = append(lines, "Validation plan: "+strings.Join(validation, "; "))
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Remaining turns: %d", state.RemainingTurns))
	}
	lines = append(lines, "No new authority was granted by this status view.")
	return strings.Join(lines, "\n")
}

func continuationStateIsPlanBudget(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return strings.TrimSpace(state.ActionProposal.RiskClass) == "plan_lease" ||
		actionListContainsMain(state.ActionProposal.AllowedActions, "approve_operation_plan_lease") ||
		actionListContainsMain(state.ContinuationLease.AllowedActions, "approve_operation_plan_lease")
}

func renderContinuationPlanBudgetDetails(state session.ContinuationState, prefix string) string {
	state = session.NormalizeContinuationState(state)
	lines := []string{strings.TrimSpace(prefix)}
	if lines[0] == "" {
		lines[0] = "Plan budget details."
	}
	if state.Objective != "" {
		lines = append(lines, "Goal: "+state.Objective)
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Budget remaining: %d turn(s)", state.RemainingTurns))
	}
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) > 0 {
		lines = append(lines, "Included:")
		for _, phase := range bundle.Phases {
			label := fmt.Sprintf("- phase %d", phase.Index)
			if summary := strings.TrimSpace(phase.Summary); summary != "" {
				label += ": " + summary
			}
			if authority := strings.TrimSpace(phase.AuthorityClass); authority != "" {
				label += " [" + authority + "]"
			}
			lines = append(lines, label)
		}
	}
	stops := compactContinuationStops(state)
	if len(stops) > 0 {
		lines = append(lines, "Stops for: "+strings.Join(stops, ", "))
	}
	lines = append(lines, "This details view does not change permissions.")
	return strings.Join(lines, "\n")
}

func compactContinuationStops(state session.ContinuationState) []string {
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	values := firstNonEmptyContinuationCommandList(proposal.ForbiddenActions, state.ContinuationLease.ForbiddenActions)
	return stoplabels.LabelsForContinuationState(state, values, stoplabels.Options{
		Defaults: []string{"anything outside scope", "hard gates", "deploy/restart", "policy or permission changes", "mailbox access or mutation"},
		Limit:    5,
	})
}

func actionListContainsMain(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == want {
			return true
		}
	}
	return false
}

func firstNonEmptyContinuationCommandList(values ...[]string) []string {
	for _, list := range values {
		out := make([]string, 0, len(list))
		for _, value := range list {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
