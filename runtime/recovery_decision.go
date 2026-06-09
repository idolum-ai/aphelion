//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type recoveryDecisionAction string

const (
	recoveryDecisionContinueUnderActiveLease recoveryDecisionAction = "continue_under_active_lease"
	recoveryDecisionRepairAndRetry           recoveryDecisionAction = "repair_and_retry"
	recoveryDecisionRescopeRequest           recoveryDecisionAction = "rescope_request"
	recoveryDecisionPark                     recoveryDecisionAction = "park"
	recoveryDecisionAskBoundedApproval       recoveryDecisionAction = "ask_for_bounded_approval"
)

type recoveryDecision struct {
	Action             recoveryDecisionAction
	Reason             string
	InterruptionKind   string
	InterruptionReason string
	OperationStatus    string
	OperationStage     string
	OperationObjective string
	ContinuationStatus string
	LeaseStatus        string
	LeaseID            string
	AllowedActions     []string
	ForbiddenActions   []string
}

func (d recoveryDecision) activeAction() bool {
	return d.Action != ""
}

func (d recoveryDecision) payload() map[string]any {
	payload := map[string]any{
		"recovery_action":     string(d.Action),
		"decision_reason":     strings.TrimSpace(d.Reason),
		"interruption_kind":   strings.TrimSpace(d.InterruptionKind),
		"interruption_reason": strings.TrimSpace(d.InterruptionReason),
	}
	if d.OperationStatus != "" {
		payload["operation_status"] = d.OperationStatus
	}
	if d.OperationStage != "" {
		payload["operation_stage"] = d.OperationStage
	}
	if d.OperationObjective != "" {
		payload["operation_objective"] = truncatePreview(d.OperationObjective, 220)
	}
	if d.ContinuationStatus != "" {
		payload["continuation_status"] = d.ContinuationStatus
	}
	if d.LeaseStatus != "" {
		payload["lease_status"] = d.LeaseStatus
	}
	if d.LeaseID != "" {
		payload["lease_id"] = d.LeaseID
	}
	if len(d.AllowedActions) > 0 {
		payload["allowed_actions"] = d.AllowedActions
	}
	if len(d.ForbiddenActions) > 0 {
		payload["forbidden_actions"] = d.ForbiddenActions
	}
	return payload
}

func (r *Runtime) recoveryDecisionForInterruption(key session.SessionKey, interruptionKind string, interruptionReason string, now time.Time) recoveryDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	decision := recoveryDecision{
		Action:             recoveryDecisionPark,
		Reason:             "durable_state_unavailable",
		InterruptionKind:   strings.TrimSpace(interruptionKind),
		InterruptionReason: strings.TrimSpace(interruptionReason),
	}
	if r == nil || r.store == nil {
		return decision
	}

	opState, opExists, _ := r.recoveryOperationState(key)
	contState, contExists, _ := r.recoveryContinuationState(key)
	if opExists {
		decision.OperationStatus = string(opState.Status)
		decision.OperationStage = strings.TrimSpace(opState.Stage)
		decision.OperationObjective = strings.TrimSpace(opState.Objective)
	}
	if contExists {
		decision.ContinuationStatus = string(contState.Status)
		decision.LeaseStatus = string(contState.ContinuationLease.Status)
		decision.LeaseID = strings.TrimSpace(contState.ContinuationLease.ID)
		decision.AllowedActions = append([]string(nil), contState.ContinuationLease.AllowedActions...)
		decision.ForbiddenActions = append([]string(nil), contState.ContinuationLease.ForbiddenActions...)
	}

	leaseActive := contExists &&
		contState.Status == session.ContinuationStatusApproved &&
		contState.ContinuationLease.ActiveAt(now)

	switch {
	case opExists && opState.Status == session.OperationStatusCompleted:
		decision.Action = recoveryDecisionPark
		decision.Reason = "operation_already_completed"
	case opExists && opState.Status == session.OperationStatusFailed:
		decision.Action = recoveryDecisionRescopeRequest
		decision.Reason = "operation_failed_requires_review"
	case opExists && opState.Status == session.OperationStatusBlocked:
		decision.Action = recoveryDecisionRescopeRequest
		decision.Reason = "operation_blocked_requires_repair"
	case opExists && opState.Status == session.OperationStatusActive && leaseActive:
		decision.Action = recoveryDecisionContinueUnderActiveLease
		decision.Reason = "active_operation_and_active_lease"
	case opExists && opState.Status == session.OperationStatusActive && contExists:
		decision.Action = recoveryDecisionRepairAndRetry
		decision.Reason = "active_operation_without_runnable_lease"
	case opExists && opState.Status == session.OperationStatusActive:
		decision.Action = recoveryDecisionAskBoundedApproval
		decision.Reason = "active_operation_needs_bounded_approval"
	case opExists && strings.TrimSpace(opState.Objective) != "":
		decision.Action = recoveryDecisionAskBoundedApproval
		decision.Reason = "known_objective_needs_bounded_approval"
	default:
		decision.Action = recoveryDecisionPark
		decision.Reason = "no_recoverable_operation"
	}
	return decision
}

func (r *Runtime) recoveryOperationState(key session.SessionKey) (session.OperationState, bool, error) {
	if r == nil || r.store == nil {
		return session.OperationState{}, false, nil
	}
	if _, opState, exists, err := r.store.PlanAndOperationStateIfExists(key); err != nil {
		return session.OperationState{}, false, err
	} else if exists {
		return session.NormalizeOperationState(opState), true, nil
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return session.OperationState{}, false, err
	}
	opState = session.NormalizeOperationState(opState)
	return opState, opState.Active(), nil
}

func (r *Runtime) recoveryContinuationState(key session.SessionKey) (session.ContinuationState, bool, error) {
	if r == nil || r.store == nil {
		return session.ContinuationState{}, false, nil
	}
	if state, exists, err := r.store.ContinuationStateIfExists(key); err != nil {
		return session.ContinuationState{}, false, err
	} else if exists {
		state = session.NormalizeContinuationState(state)
		return state, recoveryContinuationStatePresent(state), nil
	}
	return session.ContinuationState{}, false, nil
}

func recoveryContinuationStatePresent(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return strings.TrimSpace(string(state.Status)) != "" ||
		strings.TrimSpace(state.DecisionID) != "" ||
		state.DecisionMessageID > 0 ||
		strings.TrimSpace(state.Objective) != "" ||
		strings.TrimSpace(state.StageSummary) != "" ||
		state.RemainingTurns > 0 ||
		state.ApprovedBy > 0 ||
		state.ActionProposal.Active() ||
		strings.TrimSpace(state.ContinuationLease.ID) != "" ||
		strings.TrimSpace(state.ContinuationLease.ProposalID) != "" ||
		state.ApprovalBundle.Active() ||
		strings.TrimSpace(state.HandshakeBlockedReason) != "" ||
		!state.ParkedAt.IsZero() ||
		strings.TrimSpace(state.ParkedReason) != "" ||
		strings.TrimSpace(state.ParkedSource) != ""
}

func (r *Runtime) recordRecoveryDecision(key session.SessionKey, decision recoveryDecision, at time.Time) {
	if r == nil || !decision.activeAction() {
		return
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	r.recordExecutionEvent(key, core.ExecutionEventRecoveryIssued, "recovery", string(decision.Action), decision.payload(), at.UTC())
}

func recoveryDecisionVisibleText(decision recoveryDecision) string {
	if !decision.activeAction() {
		return ""
	}
	allowed := strings.Join(nonEmptyStrings(decision.AllowedActions), ", ")
	if allowed == "" {
		allowed = "the next bounded recovery step"
	}
	switch decision.Action {
	case recoveryDecisionContinueUnderActiveLease:
		return "Saved state still shows this work is approved and in progress. Next action: continue with " + allowed + "; do not mark it complete or start over."
	case recoveryDecisionRepairAndRetry:
		return "Saved state still shows active work, but the retry path needs repair first. Next action: repair the path, then retry only the work still supported by evidence."
	case recoveryDecisionAskBoundedApproval:
		return "Saved state still names the objective, but the next step needs approval. Next action: ask for approval for one clear step."
	case recoveryDecisionRescopeRequest:
		return "Saved state shows a blocker before the next step. Next action: rescope or repair the blocked step and ask again."
	case recoveryDecisionPark:
		return "Saved state does not show a safe next step. Next action: park this and report what evidence is needed to resume."
	default:
		return ""
	}
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
