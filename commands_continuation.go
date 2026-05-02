//go:build linux

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const continuationCallbackPrefix = core.ContinuationCallbackPrefix
const staleContinuationCallbackText = "This continuation prompt is no longer active. Use the newest prompt."
const continuationCallbackFailureText = "Continuation action failed. Check /doctor for details."

const (
	continuationActionApprove      = "approve"
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
		action = strings.TrimSpace(action)
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
	case continuationActionApprove, "continue":
		return continuationActionApprove
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
	if decisionID == "" || state.DecisionID == "" || action == "" {
		return false
	}
	if !continuationCallbackIDMatchesState(state, decisionID) {
		return false
	}
	switch action {
	case continuationActionApprove, continuationActionApproveLease, continuationActionContinueOnce:
		return state.Status == session.ContinuationStatusPending && state.RemainingTurns > 0
	case continuationActionAskEdit:
		return state.Status == session.ContinuationStatusPending
	case continuationActionStop, continuationActionStopPark:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	case continuationActionResumeEdge:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	case continuationActionAskNextLease, continuationActionStatusOnly:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	default:
		return false
	}
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

func continuationActionApprovesLease(action string) bool {
	switch normalizeContinuationCallbackAction(action) {
	case continuationActionApprove, continuationActionApproveLease, continuationActionContinueOnce:
		return true
	default:
		return false
	}
}

func renderContinuationDecision(state session.ContinuationState, action string) string {
	state = session.NormalizeContinuationState(state)
	switch normalizeContinuationCallbackAction(action) {
	case continuationActionApprove, continuationActionApproveLease:
		return renderContinuationApprovedDecision(state, "Continuation lease approved.")
	case continuationActionContinueOnce:
		return renderContinuationApprovedDecision(state, "Continuing once under the approved lease.")
	case continuationActionAskEdit:
		return "Continuation lease needs edits. I parked this prompt; no continuation was approved or started."
	case continuationActionAskNextLease:
		return renderContinuationEdgeStatus(state, "Next lease needed.")
	case continuationActionStatusOnly:
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
