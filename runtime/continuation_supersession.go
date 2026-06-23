//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const continuationSupersededByOperationReason = "superseded_by_operation_state"

func (r *Runtime) repairSupersededContinuationProjection(
	ctx context.Context,
	key session.SessionKey,
	msg core.InboundMessage,
	opState session.OperationState,
	state session.ContinuationState,
	stateExists bool,
	now time.Time,
	surface string,
) (session.ContinuationState, bool, error) {
	if r == nil || r.store == nil || !stateExists {
		return session.NormalizeContinuationState(state), false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending && state.Status != session.ContinuationStatusApproved {
		return state, false, nil
	}
	reason := continuationProjectionSupersededReason(opState, state)
	if reason == "" {
		return state, false, nil
	}
	repaired := continuationStateWithSupersededProjection(state, now)
	if err := r.store.UpdateContinuationState(key, repaired); err != nil {
		return state, false, fmt.Errorf("revoke superseded continuation projection chat_id=%d: %w", key.ChatID, err)
	}
	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = "continuation_projection_repair"
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", map[string]any{
		"adjudication_kind": "continuation_approval",
		"surface":           surface,
		"subject_id":        firstNonEmptyContinuation(state.DecisionID, state.ActionProposal.OperationID, state.ActionProposal.ID),
		"operator_label":    "Superseded continuation approval repaired",
		"visible_action":    "retire_superseded_continuation",
		"decision":          "revoked_superseded_continuation",
		"reason_code":       continuationSupersededByOperationReason,
		"reason":            reason,
		"operation_id":      strings.TrimSpace(opState.ID),
		"evidence_refs":     continuationRepairEvidenceRefs(opState, repaired),
		"findings": []core.RuntimeFinding{{
			Kind:             "superseded_continuation_projection",
			EvidenceStatus:   "detected_from_current_operation_state",
			Detail:           reason,
			RequiredBehavior: "Do not execute old approval buttons after the durable operation or phase projection changes.",
		}},
	}, now)
	if _, err := r.store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "continuation",
		State:              session.NextActionSuperseded,
		SubjectKind:        "phase",
		SubjectRef:         firstNonEmptyContinuation(state.ApprovalBundle.CurrentPhaseID, state.ActionProposal.ID),
		CausalRefs:         continuationRepairEvidenceRefs(opState, repaired),
		NextAction:         "retire the stale continuation and use the current operation state",
		RetryPolicy:        "do_not_execute_superseded_projection",
		OperatorProjection: "The prior approval was superseded by newer operation state and must not execute.",
		CreatedAt:          now,
	}); err != nil {
		return state, false, fmt.Errorf("record superseded continuation next action: %w", err)
	}
	chatID := msg.ChatID
	if chatID == 0 {
		chatID = key.ChatID
	}
	if chatID != 0 {
		r.retireStaleContinuationApprovalCards(ctx, key, chatID, continuationCallbackThreadIDForMessage(key, msg), 0, continuationSupersededByOperationReason, now)
	}
	return repaired, true, nil
}

func (r *Runtime) repairTerminalContinuationProjection(
	ctx context.Context,
	key session.SessionKey,
	msg core.InboundMessage,
	opState session.OperationState,
	state session.ContinuationState,
	stateExists bool,
	now time.Time,
	surface string,
	notify bool,
) (session.ContinuationState, bool, error) {
	if r == nil || r.store == nil || !stateExists {
		return session.NormalizeContinuationState(state), false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending && state.Status != session.ContinuationStatusApproved {
		return state, false, nil
	}
	if !operationStatusIsTerminal(opState.Status) {
		return state, false, nil
	}
	chatID := msg.ChatID
	if chatID == 0 {
		chatID = key.ChatID
	}
	reason := "operation " + string(opState.Status)
	if _, ok, err := r.repairStaleCompletedContinuationApprovalState(ctx, key, chatID, opState, state, reason, now, notify, surface); err != nil {
		return state, false, err
	} else if !ok {
		return state, false, nil
	}
	if chatID != 0 {
		r.retireStaleContinuationApprovalCards(ctx, key, chatID, continuationCallbackThreadIDForMessage(key, msg), 0, "stale_completed_operation", now)
	}
	repaired, err := r.store.ContinuationState(key)
	if err != nil {
		return state, false, fmt.Errorf("read repaired terminal continuation chat_id=%d: %w", key.ChatID, err)
	}
	return session.NormalizeContinuationState(repaired), true, nil
}

func continuationStateWithSupersededProjection(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	state.Status = session.ContinuationStatusRevoked
	state.RemainingTurns = 0
	state.HandshakeBlockedReason = continuationSupersededByOperationReason
	if state.ActionProposal.Active() {
		state.ActionProposal.Status = session.ProposalStatusSuperseded
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.RevokedAt = now
		state.ContinuationLease.UpdatedAt = now
	}
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusRevoked
		state.ApprovalBundle.RevokedAt = now
		state.ApprovalBundle.UpdatedAt = now
		for i := range state.ApprovalBundle.Phases {
			state.ApprovalBundle.Phases[i].Status = session.ContinuationLeaseStatusRevoked
		}
	}
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationProjectionSupersededReason(opState session.OperationState, state session.ContinuationState) string {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	if !opState.Active() || operationStatusIsTerminal(opState.Status) || !operationStateHasConcreteContinuationProjection(opState) {
		return ""
	}
	if reason := continuationApprovalBundleProjectionMismatch(opState, state.ApprovalBundle); reason != "" {
		return reason
	}
	if state.VerificationTarget != nil {
		if want := strings.TrimSpace(state.VerificationTarget.OperationID); want != "" &&
			strings.TrimSpace(opState.ID) != "" &&
			want != strings.TrimSpace(opState.ID) &&
			!operationStateHasContinuationSubjectID(opState, want) {
			return "verification target operation changed"
		}
		if want := strings.TrimSpace(state.VerificationTarget.PhaseID); want != "" && !operationStateHasPhaseID(opState, want) {
			return "verification target phase missing from current operation"
		}
		return ""
	}
	actionOperationID := strings.TrimSpace(state.ActionProposal.OperationID)
	if actionOperationID == "" {
		return ""
	}
	if actionOperationID == strings.TrimSpace(opState.ID) {
		return ""
	}
	if opState.Proposal.Active() && operationProposalMatchesContinuation(opState.Proposal, state) {
		return ""
	}
	if opState.PlanLease.Active() && operationPlanLeaseMatchesContinuation(opState.PlanLease, state) {
		return ""
	}
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if operationPhaseApprovalExcludedReason(opState.PhasePlan, phase) != "" {
			continue
		}
		if operationPhaseMatchesContinuation(opState, phase, state) {
			return ""
		}
	}
	if state.ApprovalBundle.Active() {
		if phases, ok := operationPhasesForApprovalBundleIfCurrent(opState, state.ApprovalBundle); ok && operationPhaseBundleMatchesContinuation(opState, phases, state) {
			return ""
		}
	}
	return "continuation no longer matches current operation state"
}

func continuationApprovalBundleProjectionMismatch(opState session.OperationState, bundle session.ContinuationApprovalBundle) string {
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if !bundle.Active() {
		return ""
	}
	if want := strings.TrimSpace(bundle.OperationID); want != "" && strings.TrimSpace(opState.ID) != "" && want != strings.TrimSpace(opState.ID) {
		return "approval bundle operation changed"
	}
	if want := strings.TrimSpace(bundle.PhasePlanID); want != "" && strings.TrimSpace(opState.PhasePlan.ID) != "" && want != strings.TrimSpace(opState.PhasePlan.ID) {
		return "approval bundle phase plan changed"
	}
	if strings.TrimSpace(bundle.PlanFingerprint) == "" && !continuationApprovalBundleHasPhaseFingerprint(bundle) {
		return ""
	}
	phases, ok := operationPhasesForApprovalBundleIfCurrent(opState, bundle)
	if !ok {
		return "approval bundle phase missing"
	}
	if got := operationPhasePlanFingerprint(opState, phases); got != "" && strings.TrimSpace(bundle.PlanFingerprint) != "" && got != strings.TrimSpace(bundle.PlanFingerprint) {
		return "approval bundle plan fingerprint changed"
	}
	return ""
}

func continuationApprovalBundleHasPhaseFingerprint(bundle session.ContinuationApprovalBundle) bool {
	for _, phase := range bundle.Phases {
		if strings.TrimSpace(phase.PhaseFingerprint) != "" {
			return true
		}
	}
	return false
}

func operationPhasesForApprovalBundleIfCurrent(opState session.OperationState, bundle session.ContinuationApprovalBundle) ([]session.OperationPhase, bool) {
	opState = session.NormalizeOperationState(opState)
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	byID := make(map[string]session.OperationPhase, len(opState.PhasePlan.Phases))
	byIndex := make(map[string]int, len(opState.PhasePlan.Phases))
	for i, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		id := strings.TrimSpace(phase.ID)
		if id == "" {
			id = fmt.Sprintf("phase-%d", i+1)
		}
		byID[id] = phase
		byIndex[id] = i
	}
	out := make([]session.OperationPhase, 0, len(bundle.Phases))
	for _, token := range bundle.Phases {
		token = session.NormalizeContinuationApprovalBundlePhase(token)
		if token.Status == session.ContinuationLeaseStatusDeferred {
			continue
		}
		phaseID := strings.TrimSpace(token.OperationPhaseID)
		phase, ok := byID[phaseID]
		if !ok {
			return nil, false
		}
		if fp := strings.TrimSpace(token.PhaseFingerprint); fp != "" && !continuationApprovalBundlePhaseMatchesOperation(opState, token, phase, byIndex[phaseID]) {
			return nil, false
		}
		out = append(out, phase)
	}
	return out, len(out) > 0
}

func operationStateHasPhaseID(opState session.OperationState, phaseID string) bool {
	phaseID = strings.TrimSpace(phaseID)
	if phaseID == "" {
		return false
	}
	opState = session.NormalizeOperationState(opState)
	for _, phase := range opState.PhasePlan.Phases {
		if strings.TrimSpace(phase.ID) == phaseID {
			return true
		}
	}
	return false
}

func operationStateHasConcreteContinuationProjection(opState session.OperationState) bool {
	opState = session.NormalizeOperationState(opState)
	if opState.Proposal.Active() || opState.PlanLease.Active() || opState.PhasePlan.Active() {
		return true
	}
	return false
}

func operationStateHasContinuationSubjectID(opState session.OperationState, subjectID string) bool {
	subjectID = strings.TrimSpace(subjectID)
	if subjectID == "" {
		return false
	}
	opState = session.NormalizeOperationState(opState)
	if subjectID == strings.TrimSpace(opState.ID) ||
		subjectID == strings.TrimSpace(opState.Proposal.ID) ||
		subjectID == strings.TrimSpace(opState.PlanLease.ID) ||
		subjectID == operationPlanLeaseProposalID(opState.PlanLease) {
		return true
	}
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if subjectID == strings.TrimSpace(phase.ID) || subjectID == strings.TrimSpace(phase.LeaseID) || subjectID == operationPhaseProposalID(opState, phase) {
			return true
		}
	}
	return false
}
