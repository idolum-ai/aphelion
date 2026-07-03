//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type operationPromptContext struct {
	State          session.OperationState
	Suppressed     bool
	SuppressedLine string
}

func (r *Runtime) operationPromptContextForTurn(key session.SessionKey, msg core.InboundMessage, opState session.OperationState, contState session.ContinuationState, now time.Time, surface string) operationPromptContext {
	opState = session.NormalizeOperationState(opState)
	out := operationPromptContext{State: opState}
	if !operationPromptContextCanShed(opState, contState) {
		return out
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	decision := r.operationRecoveryCandidateArbitration(key, msg, opState, now.UTC())
	if decision.Live {
		return out
	}
	surface = strings.TrimSpace(surface)
	if surface != "" {
		r.recordSuppressedRecoveryCandidate(key, opState, decision, surface, now.UTC())
	}
	out.State = session.OperationState{}
	out.Suppressed = true
	out.SuppressedLine = renderSuppressedOperationPromptContextLine(opState, decision, surface)
	return out
}

func operationPromptContextCanShed(opState session.OperationState, contState session.ContinuationState) bool {
	opState = session.NormalizeOperationState(opState)
	if !operationStateRecoverableForBudgetRecovery(opState) {
		return false
	}
	contState = session.NormalizeContinuationState(contState)
	if contState.Status != session.ContinuationStatusRevoked {
		return false
	}
	if contState.ActionProposal.Status == session.ProposalStatusSuperseded ||
		contState.ContinuationLease.Status == session.ContinuationLeaseStatusRevoked ||
		contState.RemainingTurns == 0 {
		return true
	}
	return false
}

func renderSuppressedOperationPromptContextLine(opState session.OperationState, decision recoveryCandidateArbitration, surface string) string {
	parts := []string{
		"operation_state_suppressed=true",
		"reason=" + strings.TrimSpace(decision.Reason),
	}
	if surface = strings.TrimSpace(surface); surface != "" {
		parts = append(parts, "surface="+surface)
	}
	if id := strings.TrimSpace(opState.ID); id != "" {
		parts = append(parts, "operation_id="+id)
	}
	if status := strings.TrimSpace(string(opState.Status)); status != "" {
		parts = append(parts, "operation_status="+status)
	}
	if working := compactEvidenceText(decision.WorkingObjective, 120); working != "" {
		parts = append(parts, fmt.Sprintf("working_objective=%q", working))
	}
	if candidate := compactEvidenceText(decision.CandidateObjective, 120); candidate != "" {
		parts = append(parts, fmt.Sprintf("candidate_objective=%q", candidate))
	}
	return strings.Join(parts, " ")
}
