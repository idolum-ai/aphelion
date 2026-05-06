//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func (r *Runtime) materializePendingOperationProposalApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, _ *turn.Result) (bool, error) {
	if r == nil || r.store == nil || r.outbound == nil || msg.ChatID == 0 {
		return false, nil
	}
	if _, ok := r.continuationApprovalPromptSender(); !ok {
		return false, nil
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return false, nil
	}
	opState = session.NormalizeOperationState(opState)
	now := time.Now().UTC()
	priorContinuation, priorContinuationExists, _ := r.store.ContinuationStateIfExists(key)
	opState = operationStateWithNonCurrentInProgressPhasesCleared(opState, now)
	opState = operationStateWithInactiveCurrentPhaseLeaseCleared(opState, priorContinuation, priorContinuationExists, now)
	if priorContinuationExists {
		var repaired bool
		opState, repaired = r.repairInvalidPendingPhaseApproval(ctx, key, msg, opState, priorContinuation, now)
		if repaired {
			priorContinuation, priorContinuationExists, _ = r.store.ContinuationStateIfExists(key)
		}
	}
	if pendingOperationPlanLeaseNeedsButton(opState.PlanLease) {
		now := time.Now().UTC()
		priorState, priorExists, _ := r.store.ContinuationStateIfExists(key)
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPlanLeaseMatchesContinuation(opState.PlanLease, priorState) {
			return true, nil
		}

		state := continuationStateFromOperationPlanLease(opState, opState.PlanLease, promptInput, now)
		opState = operationStateWithMaterializedPlanLease(opState, state, now)
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist operation plan lease state: %w", err)
		}
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return false, fmt.Errorf("persist operation plan lease continuation state: %w", err)
		}
		payload := continuationExecutionPayload(state)
		payload["materialized_from"] = "operation_plan_lease"
		payload["plan_lease_id"] = strings.TrimSpace(opState.PlanLease.ID)
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		if err := r.sendMaterializedContinuationApproval(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_plan_lease"); err != nil {
			return false, fmt.Errorf("send operation plan lease continuation approval: %w", err)
		}
		return true, nil
	}
	if lease, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC()); ok {
		now := time.Now().UTC()
		opState.PlanLease = lease
		priorState, priorExists, _ := r.store.ContinuationStateIfExists(key)
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPlanLeaseMatchesContinuation(opState.PlanLease, priorState) {
			return true, nil
		}

		state := continuationStateFromOperationPlanLease(opState, opState.PlanLease, promptInput, now)
		opState = operationStateWithMaterializedPlanLease(opState, state, now)
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist synthesized operation plan lease state: %w", err)
		}
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return false, fmt.Errorf("persist synthesized operation plan lease continuation state: %w", err)
		}
		payload := continuationExecutionPayload(state)
		payload["materialized_from"] = "operation_plan_lease"
		payload["plan_lease_id"] = strings.TrimSpace(opState.PlanLease.ID)
		payload["synthesized_from_phase_plan"] = true
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		if err := r.sendMaterializedContinuationApproval(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_plan_lease"); err != nil {
			return false, fmt.Errorf("send synthesized operation plan lease continuation approval: %w", err)
		}
		return true, nil
	}
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
		if err := r.sendMaterializedContinuationApproval(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_bundle"); err != nil {
			return false, fmt.Errorf("send operation phase bundle continuation approval: %w", err)
		}
		return true, nil
	}
	if phase, ok := nextOperationPhaseForApproval(opState.PhasePlan); ok {
		now := time.Now().UTC()
		if reason := operationPhaseApprovalBlockedReason(phase); reason != "" {
			r.recordAndSendBlockedOperationPhaseApproval(ctx, key, msg, opState, phase, reason, now)
			return true, nil
		}
		if operationPhaseIsPlanningOnlyApproval(phase) {
			r.recordPlanningOnlyOperationPhaseBlocked(key, opState, phase, now)
			return true, nil
		}
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
		if err := r.sendMaterializedContinuationApproval(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_plan"); err != nil {
			return false, fmt.Errorf("send operation phase continuation approval: %w", err)
		}
		return true, nil
	}
	proposal := opState.Proposal
	if !pendingOperationProposalNeedsButton(proposal) {
		if operationPhasePlanHasBlockingInProgress(opState.PhasePlan) {
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

	now = time.Now().UTC()
	state := continuationStateFromOperationProposal(opState, promptInput, now)
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return false, fmt.Errorf("persist operation proposal continuation state: %w", err)
	}
	payload := continuationExecutionPayload(state)
	payload["materialized_from"] = "operation_proposal"
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
	if err := r.sendMaterializedContinuationApproval(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_proposal"); err != nil {
		return false, fmt.Errorf("send operation proposal continuation approval: %w", err)
	}
	return true, nil
}

func (r *Runtime) sendMaterializedContinuationApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, text string, source string) error {
	if approved, err := r.maybeAutoApproveContinuationOffer(ctx, key, msg, state, source); approved || err != nil {
		return err
	}
	return r.sendContinuationApprovalPrompt(ctx, key, msg, state, text)
}

func (r *Runtime) repairInvalidPendingPhaseApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, state session.ContinuationState, now time.Time) (session.OperationState, bool) {
	repairedOpState, repaired, err := r.repairInvalidPendingPhaseApprovalState(ctx, key, msg.ChatID, opState, state, now, true, "materialization_repair")
	if err != nil {
		return session.NormalizeOperationState(opState), false
	}
	return repairedOpState, repaired
}

func (r *Runtime) repairInvalidPendingContinuationApprovals(ctx context.Context, now time.Time) (int, error) {
	if r == nil || r.store == nil {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	records, err := r.store.ContinuationStates()
	if err != nil {
		return 0, fmt.Errorf("load continuation states for approval repair: %w", err)
	}
	repaired := 0
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return repaired, err
		}
		state := session.NormalizeContinuationState(record.State)
		if state.Status != session.ContinuationStatusPending {
			continue
		}
		opState, err := r.store.OperationState(record.Key)
		if err != nil {
			return repaired, fmt.Errorf("load operation state chat_id=%d: %w", record.Key.ChatID, err)
		}
		_, ok, err := r.repairInvalidPendingPhaseApprovalState(ctx, record.Key, record.Key.ChatID, opState, state, now, true, "startup_repair")
		if err != nil {
			return repaired, err
		}
		if ok {
			repaired++
		}
	}
	return repaired, nil
}

func (r *Runtime) repairInvalidPendingPhaseApprovalState(ctx context.Context, key session.SessionKey, chatID int64, opState session.OperationState, state session.ContinuationState, now time.Time, notify bool, surface string) (session.OperationState, bool, error) {
	if r == nil || r.store == nil {
		return session.NormalizeOperationState(opState), false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending {
		return opState, false, nil
	}
	reason := continuationApprovalBundleInvalidReason(opState.PhasePlan, state.ApprovalBundle)
	if reason == "" {
		return opState, false, nil
	}
	state.Status = session.ContinuationStatusRevoked
	state.ActionProposal.Status = session.ProposalStatusSuperseded
	state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
	state.ContinuationLease.UpdatedAt = now
	state.ApprovalBundle.Status = session.ContinuationLeaseStatusRevoked
	for i := range state.ApprovalBundle.Phases {
		state.ApprovalBundle.Phases[i].Status = session.ContinuationLeaseStatusRevoked
	}
	state.ApprovalBundle.UpdatedAt = now
	state.UpdatedAt = now
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return opState, false, fmt.Errorf("revoke invalid pending continuation chat_id=%d: %w", key.ChatID, err)
	}

	opState = operationStateWithInvalidApprovalCleared(opState, state, now)
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return opState, false, fmt.Errorf("clear invalid pending operation approval chat_id=%d: %w", key.ChatID, err)
	}

	surface = strings.TrimSpace(surface)
	if surface == "" {
		surface = "materialization_repair"
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", map[string]any{
		"adjudication_kind": "continuation_approval",
		"surface":           surface,
		"subject_id":        strings.TrimSpace(state.DecisionID),
		"operator_label":    "Invalid continuation approval repaired",
		"visible_action":    "repair_invalid_pending_approval",
		"decision":          "revoked_invalid_pending_approval",
		"findings": []core.RuntimeFinding{{
			Kind:             "invalid_pending_approval",
			EvidenceStatus:   "detected_from_phase_contract",
			Detail:           reason,
			RequiredBehavior: "Do not execute old approval buttons; re-adjudicate the next eligible action.",
		}},
	}, now)
	if notify && r.outbound != nil && chatID != 0 {
		_, _ = r.outbound.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   "Stopped stale approval.\n\nI will create a fresh narrower proposal for the next eligible action.",
		})
	}
	return opState, true, nil
}

func operationStateWithInvalidApprovalCleared(opState session.OperationState, state session.ContinuationState, now time.Time) session.OperationState {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	actionOpID := strings.TrimSpace(state.ActionProposal.OperationID)
	actionID := strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-")
	decisionID := strings.TrimSpace(state.DecisionID)
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	if opState.Proposal.Status == session.ProposalStatusPending {
		proposalID := strings.TrimSpace(opState.Proposal.ID)
		if proposalID != "" && (proposalID == actionOpID || proposalID == actionID || proposalID == decisionID) {
			opState.Proposal.Status = session.ProposalStatusSuperseded
			opState.Proposal.UpdatedAt = now
		}
	}
	if opState.PlanLease.Status == session.PlanLeaseStatusProposed || opState.PlanLease.Status == session.PlanLeaseStatusActive || opState.PlanLease.Status == session.PlanLeaseStatusApproved {
		planID := strings.TrimSpace(opState.PlanLease.ID)
		if planID != "" && (planID == actionOpID || planID == actionID || planID == decisionID) {
			opState.PlanLease.Status = session.PlanLeaseStatusRevoked
			opState.PlanLease.UpdatedAt = now
		}
	}
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	bundleIDs := make(map[string]struct{}, len(bundle.Phases))
	for _, phase := range bundle.Phases {
		if id := strings.TrimSpace(phase.OperationPhaseID); id != "" {
			bundleIDs[id] = struct{}{}
		}
	}
	for i := range opState.PhasePlan.Phases {
		phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
		_, inBundle := bundleIDs[phaseID]
		leaseMatches := leaseID != "" && strings.TrimSpace(opState.PhasePlan.Phases[i].LeaseID) == leaseID
		if !inBundle && !leaseMatches {
			continue
		}
		if opState.PhasePlan.Phases[i].Status == session.PlanStatusInProgress {
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		}
		opState.PhasePlan.Phases[i].LeaseID = ""
	}
	opState.Status = session.OperationStatusBlocked
	opState.Stage = "phase_approval_adjudicated"
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}

func continuationApprovalBundleInvalidReason(plan session.OperationPhasePlan, bundle session.ContinuationApprovalBundle) string {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if len(bundle.Phases) == 0 {
		return ""
	}
	phaseByID := make(map[string]session.OperationPhase, len(plan.Phases))
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if id := strings.TrimSpace(phase.ID); id != "" {
			phaseByID[id] = phase
		}
	}
	family := ""
	for _, bundlePhase := range bundle.Phases {
		phaseID := strings.TrimSpace(bundlePhase.OperationPhaseID)
		phase, ok := phaseByID[phaseID]
		if !ok {
			phase = session.OperationPhase{
				ID:               phaseID,
				Summary:          bundlePhase.Summary,
				AuthorityClass:   bundlePhase.AuthorityClass,
				WhyNow:           bundlePhase.WhyNow,
				BoundedEffect:    bundlePhase.BoundedEffect,
				AllowedActions:   append([]string(nil), bundlePhase.AllowedActions...),
				ForbiddenActions: append([]string(nil), bundlePhase.ForbiddenActions...),
				ValidationPlan:   append([]string(nil), bundlePhase.ValidationPlan...),
				Status:           session.PlanStatusPending,
			}
		}
		if reason := operationPhaseApprovalExcludedReason(plan, phase); reason != "" {
			return reason
		}
		if reason := operationPhaseApprovalBlockedReason(phase); reason != "" {
			return reason
		}
		phaseFamily := operationPhaseApprovalFamily(phase)
		if family == "" {
			family = phaseFamily
		} else if family != phaseFamily {
			return "mixed authority classes require separate approvals"
		}
		if operationPhaseRequiresFreshApprovalGate(phase) && len(bundle.Phases) > 1 {
			return "fresh approval gate cannot be bundled"
		}
	}
	return ""
}

func pendingOperationProposalNeedsButton(proposal session.OperationProposal) bool {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	return proposal.Active() && proposal.Status == session.ProposalStatusPending && strings.TrimSpace(proposal.ID) != "" && strings.TrimSpace(proposal.Summary) != ""
}

func pendingOperationPlanLeaseNeedsButton(lease session.OperationPlanLease) bool {
	lease = session.NormalizeOperationState(session.OperationState{PlanLease: lease}).PlanLease
	if !lease.Active() || lease.Status != session.PlanLeaseStatusProposed || strings.TrimSpace(lease.ID) == "" {
		return false
	}
	return strings.TrimSpace(lease.Summary) != "" ||
		strings.TrimSpace(lease.Objective) != "" ||
		len(lease.Lanes) > 0 ||
		len(lease.AllowedActions) > 0 ||
		len(lease.ValidationGates) > 0
}

const operationPlanBudgetMaxLanes = 6

func operationPlanLeaseFromPhasePlan(opState session.OperationState, now time.Time) (session.OperationPlanLease, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	if opState.PlanLease.Active() || len(opState.PhasePlan.Phases) == 0 {
		return session.OperationPlanLease{}, false
	}
	if operationPhasePlanHasBlockingInProgress(opState.PhasePlan) {
		return session.OperationPlanLease{}, false
	}
	start := operationPhasePlanStartIndex(opState.PhasePlan)
	pendingCount := operationPhasePlanPendingCountFrom(opState.PhasePlan, start)
	phases := make([]session.OperationPhase, 0, operationPlanBudgetMaxLanes)
	stoppedAtGate := ""
	for i := start; i < len(opState.PhasePlan.Phases) && len(phases) < operationPlanBudgetMaxLanes; i++ {
		phase := normalizeSingleOperationPhase(opState.PhasePlan.Phases[i])
		if operationPhasePlanPhaseIsStaleInProgress(opState.PhasePlan, phase) {
			continue
		}
		if operationPhaseApprovalExcludedReason(opState.PhasePlan, phase) != "" {
			continue
		}
		if phase.Status == session.PlanStatusCompleted {
			continue
		}
		if !operationPhaseNeedsApproval(phase) {
			continue
		}
		if reason := operationPhaseApprovalBlockedReason(phase); reason != "" {
			stoppedAtGate = reason
			break
		}
		if operationPhaseRequiresFreshApprovalGate(phase) {
			stoppedAtGate = operationPhaseStopGateLabel(phase)
			break
		}
		phases = append(phases, phase)
	}
	if len(phases) == 0 {
		return session.OperationPlanLease{}, false
	}
	if pendingCount < 2 && !operationPhaseIsPlanningOnlyApproval(phases[0]) {
		return session.OperationPlanLease{}, false
	}
	lease := session.OperationPlanLease{
		ID:               operationPhasePlanLeaseID(opState, phases),
		Summary:          operationPhasePlanLeaseSummary(opState, phases),
		Objective:        firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, opState.Summary, "Continue the approved operation plan."),
		OperationID:      strings.TrimSpace(opState.ID),
		Status:           session.PlanLeaseStatusProposed,
		TurnBudget:       operationPhasePlanLeaseTurnBudget(phases),
		CoveredPhaseIDs:  operationPhaseIDs(phases),
		Lanes:            operationPlanLeaseLanesFromPhases(phases),
		AllowedActions:   []string{"execute_plan_budget_lanes", "use_existing_authority_only", "update_operation_phase_plan", "report_evidence"},
		ForbiddenActions: []string{"work_outside_plan_budget", "silent_escalation", "skip_stop_gate"},
		ValidationGates:  []string{"report evidence after each turn", "stop if the next action is outside the disclosed plan budget"},
		ExitConditions:   []string{"turn budget is spent", "covered phases are complete", "a stop condition appears", "operator pauses or revokes"},
		ExpiresAt:        now.Add(12 * time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if stoppedAtGate != "" {
		lease.HardInterrupts = []string{stoppedAtGate}
	}
	lease = session.NormalizeOperationPlanLease(lease)
	return lease, true
}

func operationPhasePlanStartIndex(plan session.OperationPhasePlan) int {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for i, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				return i
			}
		}
	}
	for i, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusPending || phase.Status == "" {
			return i
		}
	}
	return 0
}

func operationPhasePlanPendingCountFrom(plan session.OperationPhasePlan, start int) int {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if start < 0 {
		start = 0
	}
	count := 0
	for i := start; i < len(plan.Phases); i++ {
		if operationPhaseNeedsApproval(plan.Phases[i]) {
			count++
		}
	}
	return count
}

func operationPhasePlanHasBlockingInProgress(plan session.OperationPhasePlan) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if len(plan.Phases) == 0 {
		return false
	}
	currentID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentID != "" {
		for _, phase := range plan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if strings.TrimSpace(phase.ID) != currentID {
				continue
			}
			return phase.Status == session.PlanStatusInProgress
		}
	}
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusInProgress {
			return true
		}
	}
	return false
}

func operationPhasePlanPhaseIsStaleInProgress(plan session.OperationPhasePlan, phase session.OperationPhase) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	phase = normalizeSingleOperationPhase(phase)
	currentID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentID == "" {
		return false
	}
	return phase.Status == session.PlanStatusInProgress && strings.TrimSpace(phase.ID) != currentID
}

func operationStateWithNonCurrentInProgressPhasesCleared(opState session.OperationState, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID)
	if currentID == "" {
		return opState
	}
	currentStatus := session.PlanStatus("")
	for _, phase := range opState.PhasePlan.Phases {
		if strings.TrimSpace(phase.ID) == currentID {
			currentStatus = phase.Status
			break
		}
	}
	if currentStatus == session.PlanStatusInProgress {
		return opState
	}
	changed := false
	for i := range opState.PhasePlan.Phases {
		if strings.TrimSpace(opState.PhasePlan.Phases[i].ID) == currentID {
			continue
		}
		if opState.PhasePlan.Phases[i].Status != session.PlanStatusInProgress {
			continue
		}
		opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		opState.PhasePlan.Phases[i].LeaseID = ""
		changed = true
	}
	if changed {
		opState.PhasePlan.UpdatedAt = now
		opState.UpdatedAt = now
	}
	return session.NormalizeOperationState(opState)
}

func operationStateWithInactiveCurrentPhaseLeaseCleared(opState session.OperationState, cont session.ContinuationState, contExists bool, now time.Time) session.OperationState {
	if !contExists {
		return session.NormalizeOperationState(opState)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	cont = session.NormalizeContinuationState(cont)
	leaseID := strings.TrimSpace(cont.ContinuationLease.ID)
	if leaseID == "" || !continuationStateLeaseInactiveForPhaseRecovery(cont) {
		return opState
	}
	currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID)
	if currentID == "" {
		return opState
	}
	changed := false
	for i := range opState.PhasePlan.Phases {
		phase := opState.PhasePlan.Phases[i]
		if strings.TrimSpace(phase.ID) != currentID || strings.TrimSpace(phase.LeaseID) != leaseID || phase.Status != session.PlanStatusInProgress {
			continue
		}
		opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		opState.PhasePlan.Phases[i].LeaseID = ""
		changed = true
		break
	}
	if !changed {
		return opState
	}
	if opState.Proposal.Status == session.ProposalStatusApproved && operationProposalMatchesContinuation(opState.Proposal, cont) {
		opState.Proposal.Status = session.ProposalStatusSuperseded
		opState.Proposal.UpdatedAt = now
	}
	opState.Status = session.OperationStatusBlocked
	opState.Stage = firstNonEmptyContinuation(strings.TrimSpace(opState.Stage), "phase_approval_recovered_from_inactive_lease")
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}

func continuationStateLeaseInactiveForPhaseRecovery(cont session.ContinuationState) bool {
	cont = session.NormalizeContinuationState(cont)
	switch cont.Status {
	case session.ContinuationStatusRevoked:
		return true
	}
	switch cont.ContinuationLease.Status {
	case session.ContinuationLeaseStatusRevoked, session.ContinuationLeaseStatusExpired:
		return true
	default:
		return false
	}
}

func operationPhasePlanLeaseID(opState session.OperationState, phases []session.OperationPhase) string {
	base := firstNonEmptyContinuation(opState.ID, opState.PhasePlan.ID, "operation")
	firstID := "plan"
	lastID := "budget"
	if len(phases) > 0 {
		firstID = firstNonEmptyContinuation(phases[0].ID, phases[0].Summary, "first")
		lastID = firstNonEmptyContinuation(phases[len(phases)-1].ID, phases[len(phases)-1].Summary, "last")
	}
	id := sanitizeOperationPhaseProposalID("plan-budget-" + base + "-" + firstID + "-to-" + lastID)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func operationPhasePlanLeaseSummary(opState session.OperationState, phases []session.OperationPhase) string {
	if len(phases) == 0 {
		return "Approve plan budget"
	}
	goal := firstNonEmptyContinuation(opState.PhasePlan.Goal, opState.Objective, opState.Summary)
	firstIndex := operationPhaseIndex(opState.PhasePlan, phases[0].ID)
	lastIndex := operationPhaseIndex(opState.PhasePlan, phases[len(phases)-1].ID)
	if firstIndex <= 0 {
		firstIndex = 1
	}
	if lastIndex <= 0 {
		lastIndex = firstIndex + len(phases) - 1
	}
	label := fmt.Sprintf("Approve plan budget: phases %d-%d", firstIndex, lastIndex)
	if firstIndex == lastIndex {
		label = fmt.Sprintf("Approve plan budget: phase %d", firstIndex)
	}
	if goal != "" {
		label += " for " + goal
	}
	return label
}

func operationPhaseIndex(plan session.OperationPhasePlan, phaseID string) int {
	phaseID = strings.TrimSpace(phaseID)
	for i, phase := range plan.Phases {
		if strings.TrimSpace(phase.ID) == phaseID {
			return i + 1
		}
	}
	return 0
}

func operationPhasePlanLeaseTurnBudget(phases []session.OperationPhase) int {
	turns := 0
	for range phases {
		turns++
	}
	if turns <= 0 {
		return 1
	}
	return turns
}

func operationPhaseIDs(phases []session.OperationPhase) []string {
	out := make([]string, 0, len(phases))
	for _, phase := range phases {
		if id := strings.TrimSpace(phase.ID); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func operationPlanLeaseLanesFromPhases(phases []session.OperationPhase) []session.OperationPlanLeaseLane {
	lanes := make([]session.OperationPlanLeaseLane, 0, len(phases))
	for _, phase := range phases {
		phase = normalizeSingleOperationPhase(phase)
		lanes = append(lanes, session.OperationPlanLeaseLane{
			ID:               strings.TrimSpace(phase.ID),
			Summary:          strings.TrimSpace(phase.Summary),
			AuthorityClass:   strings.TrimSpace(phase.AuthorityClass),
			ExpectedTurns:    1,
			AllowedActions:   append([]string(nil), phase.AllowedActions...),
			ForbiddenActions: append([]string(nil), phase.ForbiddenActions...),
		})
	}
	return lanes
}

func operationPhaseStopGateLabel(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	return firstNonEmptyContinuation(phase.AuthorityClass, phase.Summary, "fresh approval gate")
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

func operationPlanLeaseMatchesContinuation(lease session.OperationPlanLease, state session.ContinuationState) bool {
	lease = session.NormalizeOperationState(session.OperationState{PlanLease: lease}).PlanLease
	state = session.NormalizeContinuationState(state)
	leaseID := strings.TrimSpace(lease.ID)
	if leaseID == "" {
		return false
	}
	return strings.TrimSpace(state.ActionProposal.OperationID) == leaseID ||
		strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-") == operationPlanLeaseProposalID(lease) ||
		strings.TrimSpace(state.DecisionID) == operationPlanLeaseProposalID(lease) ||
		strings.TrimSpace(state.ContinuationLease.ID) == "lease-"+operationPlanLeaseProposalID(lease)
}

func nextOperationPhaseForApproval(plan session.OperationPhasePlan) (session.OperationPhase, bool) {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}, false
	}
	if operationPhasePlanHasBlockingInProgress(plan) {
		return session.OperationPhase{}, false
	}
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for _, phase := range plan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if strings.TrimSpace(phase.ID) == currentID && operationPhaseNeedsApproval(phase) && operationPhaseApprovalExcludedReason(plan, phase) == "" {
				return phase, true
			}
		}
	}
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if operationPhasePlanPhaseIsStaleInProgress(plan, phase) {
			continue
		}
		if operationPhaseApprovalExcludedReason(plan, phase) != "" {
			continue
		}
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
	if operationPhasePlanHasBlockingInProgress(plan) {
		return nil, false
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
		if operationPhasePlanPhaseIsStaleInProgress(plan, phase) {
			continue
		}
		if operationPhaseApprovalExcludedReason(plan, phase) != "" {
			continue
		}
		if phase.Status == session.PlanStatusCompleted {
			continue
		}
		if !operationPhaseNeedsApproval(phase) {
			break
		}
		if operationPhaseApprovalBlockedReason(phase) != "" {
			break
		}
		if operationPhaseIsPlanningOnlyApproval(phase) {
			break
		}
		if operationPhaseRequiresFreshApprovalGate(phase) {
			break
		}
		if !operationPhaseBundleCanAdd(bundle, phase) {
			break
		}
		bundle = append(bundle, phase)
	}
	if len(bundle) < 2 && start > 0 {
		bundle = bundle[:0]
		for i := 0; i < len(plan.Phases) && len(bundle) < operationApprovalBundleMaxPhases; i++ {
			phase := normalizeSingleOperationPhase(plan.Phases[i])
			if operationPhasePlanPhaseIsStaleInProgress(plan, phase) {
				continue
			}
			if operationPhaseApprovalExcludedReason(plan, phase) != "" {
				continue
			}
			if phase.Status == session.PlanStatusCompleted {
				continue
			}
			if !operationPhaseNeedsApproval(phase) || operationPhaseApprovalBlockedReason(phase) != "" || operationPhaseRequiresFreshApprovalGate(phase) {
				break
			}
			if operationPhaseIsPlanningOnlyApproval(phase) {
				break
			}
			if !operationPhaseBundleCanAdd(bundle, phase) {
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

func operationPhaseBundleCanAdd(bundle []session.OperationPhase, phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if len(bundle) == 0 {
		return true
	}
	want := operationPhaseApprovalFamily(bundle[0])
	got := operationPhaseApprovalFamily(phase)
	if want == "" || got == "" {
		return want == got
	}
	return want == got
}

func operationPhaseApprovalFamily(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	class := session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect)
	switch class {
	case session.ContinuationLeaseClassLocalWorkspace:
		return "local_workspace"
	case session.ContinuationLeaseClassDataAccess:
		return "data_access"
	case session.ContinuationLeaseClassChildWake:
		return "child_wake"
	case session.ContinuationLeaseClassCapabilityGrant:
		return "capability_grant"
	case session.ContinuationLeaseClassDeployRestart:
		return "deploy_restart"
	default:
		return ""
	}
}

func operationPhaseApprovalExcludedReason(plan session.OperationPhasePlan, phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	if operationPhasePlanPhaseIsStaleInProgress(plan, phase) {
		return "stale non-current in-progress phase"
	}
	if phase.Status == session.PlanStatusCompleted {
		return "completed phase"
	}
	if phase.StaleAuthority || operationPhaseReasonCodeIsStaleAuthority(phase.BlockedReasonCode) {
		return "superseded or stale phase"
	}
	text := operationPhaseApprovalText(phase)
	switch {
	case strings.Contains(text, "superseded prior"),
		strings.Contains(text, "superseded phase"),
		strings.Contains(text, "stale phase"),
		strings.Contains(text, "old lease"),
		strings.Contains(text, "old authority"),
		strings.Contains(text, "no authority from this stale phase"),
		strings.Contains(text, "must not be used"),
		strings.Contains(text, "should not be used"):
		return "superseded or stale phase"
	default:
		return ""
	}
}

func operationPhaseApprovalBlockedReason(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	if reason := operationPhaseTypedBlockedReason(phase); reason != "" {
		return reason
	}
	text := operationPhaseApprovalText(phase)
	switch {
	case strings.Contains(text, "no opt in") ||
		strings.Contains(text, "no opt-in") ||
		strings.Contains(text, "not opted in") ||
		strings.Contains(text, "missing opt in") ||
		strings.Contains(text, "missing opt-in"):
		return "waiting for explicit opt-in"
	case strings.Contains(text, "wait for her explicit opt in") ||
		strings.Contains(text, "wait for her explicit opt-in") ||
		strings.Contains(text, "wait for explicit opt in") ||
		strings.Contains(text, "wait for explicit opt-in"):
		return "waiting for explicit opt-in"
	case strings.Contains(text, "blocked:") && (strings.Contains(text, "consent") || strings.Contains(text, "opt in") || strings.Contains(text, "opt-in")):
		return "blocked on consent"
	case strings.Contains(text, "no consent") ||
		strings.Contains(text, "without consent") ||
		strings.Contains(text, "consent has not been observed") ||
		strings.Contains(text, "consent not observed"):
		return "waiting for explicit consent"
	default:
		return ""
	}
}

func operationPhaseTypedBlockedReason(phase session.OperationPhase) string {
	if phase.RequiresOptIn {
		return "waiting for explicit opt-in"
	}
	if phase.RequiresConsent {
		return "waiting for explicit consent"
	}
	code := normalizeOperationPhaseReasonCode(phase.BlockedReasonCode)
	switch code {
	case "":
		return ""
	case "waiting_for_opt_in", "requires_opt_in", "missing_opt_in", "no_opt_in", "opt_in_required":
		return "waiting for explicit opt-in"
	case "waiting_for_consent", "requires_consent", "missing_consent", "no_consent", "consent_required":
		return "waiting for explicit consent"
	case "blocked_on_consent", "consent_blocked":
		return "blocked on consent"
	case "stale_authority", "superseded", "superseded_phase", "stale_phase":
		return ""
	default:
		return "blocked: " + code
	}
}

func operationPhaseReasonCodeIsStaleAuthority(code string) bool {
	switch normalizeOperationPhaseReasonCode(code) {
	case "stale_authority", "superseded", "superseded_phase", "stale_phase", "old_authority", "old_lease":
		return true
	default:
		return false
	}
}

func normalizeOperationPhaseReasonCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", ".", "_")
	code = replacer.Replace(code)
	for strings.Contains(code, "__") {
		code = strings.ReplaceAll(code, "__", "_")
	}
	return strings.Trim(code, "_")
}

func operationPhaseApprovalText(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	parts := []string{
		phase.ID,
		phase.Summary,
		phase.AuthorityClass,
		phase.WhyNow,
		phase.BoundedEffect,
	}
	parts = append(parts, phase.AllowedActions...)
	parts = append(parts, phase.ForbiddenActions...)
	parts = append(parts, phase.ValidationPlan...)
	return strings.ToLower(strings.TrimSpace(strings.Join(parts, " ")))
}

func operationPhaseRequiresFreshApprovalGate(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if class := session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect); class != "" && class != session.ContinuationLeaseClassLocalWorkspace {
		return true
	}
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

func operationPhaseIsPlanningOnlyApproval(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if !operationPhaseNeedsApproval(phase) {
		return false
	}
	if len(phase.AllowedActions) > 0 {
		hasPlanningAction := false
		hasConcreteAction := false
		for _, action := range phase.AllowedActions {
			if operationPhaseActionIsPlanningOnly(action) {
				hasPlanningAction = true
				continue
			}
			if operationPhaseActionIsConcrete(action) {
				hasConcreteAction = true
			}
		}
		if hasPlanningAction && !hasConcreteAction {
			return true
		}
	}
	text := strings.ToLower(strings.TrimSpace(strings.Join(operationPhasePlanningTextParts(phase), " ")))
	if text == "" {
		return false
	}
	if operationPhaseTextIsPlanningOnly(text) && !operationPhaseTextHasConcreteExecution(text) {
		return true
	}
	return false
}

func operationPhasePlanningTextParts(phase session.OperationPhase) []string {
	parts := []string{phase.Summary, phase.WhyNow, phase.BoundedEffect}
	parts = append(parts, phase.ValidationPlan...)
	return parts
}

func operationPhaseActionIsPlanningOnly(action string) bool {
	value := strings.ToLower(strings.TrimSpace(action))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "draft_plan", "draft_repair_plan", "draft_repair_phases", "make_plan", "make_a_plan", "plan", "planning", "propose_plan", "propose_repair_plan", "propose_repair_phases", "update_operation_phase_plan":
		return true
	default:
		return strings.Contains(value, "draft") && strings.Contains(value, "plan") ||
			strings.Contains(value, "propose") && strings.Contains(value, "phase") ||
			strings.Contains(value, "make") && strings.Contains(value, "plan")
	}
}

func operationPhaseActionIsConcrete(action string) bool {
	value := strings.ToLower(strings.TrimSpace(action))
	if value == "" {
		return false
	}
	if workModeFromStructuredAuthority(value) != "" {
		return true
	}
	for _, token := range []string{
		"inspect", "read", "review", "edit", "patch", "write_file", "run_test", "test", "build", "install", "commit", "deploy", "restart", "migrate", "repair", "execute", "verify", "smoke",
	} {
		if strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func operationPhaseTextIsPlanningOnly(text string) bool {
	patterns := []string{
		"make a plan",
		"make plan",
		"draft a plan",
		"draft plan",
		"draft repair plan",
		"draft repair phases",
		"repair planning",
		"planning phase",
		"propose a plan",
		"propose repair phases",
		"turn child diagnostic failures into explicit repair phases",
		"turn failures into explicit repair phases",
		"turn findings into phases",
		"turn issues into phases",
		"convert findings into phases",
		"convert issues into phases",
	}
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return strings.HasPrefix(text, "plan ") || strings.HasPrefix(text, "draft ")
}

func operationPhaseTextHasConcreteExecution(text string) bool {
	for _, token := range []string{
		"edit files", "patch", "run tests", "go test", "build", "install", "commit", "deploy", "restart", "migrate", "repair state", "write artifact", "verify", "smoke test", "inspect state",
	} {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func (r *Runtime) recordPlanningOnlyOperationPhaseBlocked(key session.SessionKey, opState session.OperationState, phase session.OperationPhase, now time.Time) {
	if r == nil {
		return
	}
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", map[string]any{
		"blocked_reason": "planning_only_phase_requires_plan_lease",
		"phase_plan_id":  strings.TrimSpace(opState.PhasePlan.ID),
		"phase_id":       strings.TrimSpace(phase.ID),
		"phase_summary":  strings.TrimSpace(phase.Summary),
		"operation_id":   strings.TrimSpace(opState.ID),
	}, now)
}

func (r *Runtime) recordAndSendBlockedOperationPhaseApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, phase session.OperationPhase, reason string, now time.Time) {
	if r == nil {
		return
	}
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "approval is blocked"
	}
	payload := map[string]any{
		"adjudication_kind": "continuation_approval",
		"surface":           "phase_materialization",
		"subject_id":        strings.TrimSpace(phase.ID),
		"operator_label":    "Continuation approval blocked",
		"visible_action":    "blocked_status",
		"phase_plan_id":     strings.TrimSpace(opState.PhasePlan.ID),
		"phase_id":          strings.TrimSpace(phase.ID),
		"phase_summary":     strings.TrimSpace(phase.Summary),
		"operation_id":      strings.TrimSpace(opState.ID),
		"decision":          "blocked",
		"findings": []core.RuntimeFinding{{
			Kind:             "approval_blocked",
			EvidenceStatus:   "declared_by_phase_contract",
			Detail:           reason,
			RequiredBehavior: "Do not show approval buttons until a fresh eligible proposal exists.",
		}},
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", payload, now)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", map[string]any{
		"blocked_reason": reason,
		"phase_plan_id":  strings.TrimSpace(opState.PhasePlan.ID),
		"phase_id":       strings.TrimSpace(phase.ID),
		"phase_summary":  strings.TrimSpace(phase.Summary),
		"operation_id":   strings.TrimSpace(opState.ID),
	}, now)
	if r.outbound == nil || msg.ChatID == 0 {
		return
	}
	replyTo := msg.MessageID
	var replyToPtr *int64
	if replyTo != 0 {
		replyToPtr = &replyTo
	}
	_, _ = r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    renderOperationPhaseApprovalBlockedStatus(opState, phase, reason),
		ReplyTo: replyToPtr,
	})
}

func renderOperationPhaseApprovalBlockedStatus(opState session.OperationState, phase session.OperationPhase, reason string) string {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	title := firstNonEmptyContinuation(phase.Summary, opState.PhasePlan.Goal, opState.Objective, "Next phase")
	lines := []string{"Blocked: " + truncatePreview(title, 96)}
	if reason = strings.TrimSpace(reason); reason != "" {
		lines = append(lines, "", "Why now:", truncatePreview(reason, 180))
	}
	if next := operationBlockedApprovalNextStep(reason); next != "" {
		lines = append(lines, "", "Next:", next)
	}
	return strings.Join(lines, "\n")
}

func operationBlockedApprovalNextStep(reason string) string {
	lower := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.Contains(lower, "opt-in"), strings.Contains(lower, "opt in"), strings.Contains(lower, "consent"):
		return "Wait for explicit opt-in/consent, then create a fresh narrower proposal."
	default:
		return "Create a fresh narrower proposal before continuing."
	}
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

func continuationStateFromOperationPlanLease(opState session.OperationState, lease session.OperationPlanLease, promptInput string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	lease = session.NormalizeOperationPlanLease(lease)
	decisionID := operationPlanLeaseProposalID(lease)
	if decisionID == "" {
		decisionID = newContinuationDecisionID()
	}
	turns := lease.RemainingTurns
	if turns <= 0 {
		turns = lease.TurnBudget
	}
	if turns <= 0 {
		turns = len(lease.Lanes)
	}
	if turns <= 0 {
		turns = 1
	}
	objective := firstNonEmptyContinuation(lease.Objective, opState.Objective, opState.Summary, lease.Summary, summarizeContinuationFallback(promptInput))
	nextStep := firstNonEmptyContinuation(lease.Summary, lease.Objective, "Approve a bounded plan lease.")
	boundedEffect := operationPlanLeaseBoundedEffect(lease)
	whyNow := "This broad plan needs a button-backed bounded envelope before Aphelion can execute multiple leased lanes."
	if opState.Stage != "" {
		whyNow = "Operation stage " + strings.TrimSpace(opState.Stage) + " requires a button-backed bounded plan lease."
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: turns,
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A bounded plan lease is ready for explicit approval.",
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
	if phases := operationPlanLeasePhasesFromOperation(opState, lease); len(phases) > 0 {
		bundlePhases := continuationApprovalBundlePhasesFromOperation(opState, phases)
		state.ApprovalBundle = session.ContinuationApprovalBundle{
			ID:             decisionID,
			Status:         session.ContinuationLeaseStatusPending,
			CurrentPhaseID: firstContinuationBundlePhaseID(bundlePhases),
			Phases:         bundlePhases,
			ExpiresAt:      now.Add(continuationLeaseDefaultTTL),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	action := session.ActionProposal{
		ID:               "aprop-" + decisionID,
		OperationID:      strings.TrimSpace(lease.ID),
		MissionID:        strings.TrimSpace(lease.MissionID),
		Summary:          nextStep,
		WhyNow:           whyNow,
		BoundedEffect:    boundedEffect,
		RiskClass:        "plan_lease",
		AllowedActions:   operationPlanLeaseAllowedActions(lease),
		ForbiddenActions: operationPlanLeaseForbiddenActions(lease),
		ValidationPlan:   operationPlanLeaseValidationPlan(lease),
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	state.ActionProposal = session.NormalizeActionProposal(action)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, turns, now)
	return session.NormalizeContinuationState(state)
}

func operationPlanLeasePhasesFromOperation(opState session.OperationState, lease session.OperationPlanLease) []session.OperationPhase {
	opState = session.NormalizeOperationState(opState)
	lease = session.NormalizeOperationPlanLease(lease)
	if len(opState.PhasePlan.Phases) == 0 || len(lease.CoveredPhaseIDs) == 0 {
		return nil
	}
	covered := make(map[string]struct{}, len(lease.CoveredPhaseIDs))
	for _, id := range lease.CoveredPhaseIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			covered[trimmed] = struct{}{}
		}
	}
	out := make([]session.OperationPhase, 0, len(covered))
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if _, ok := covered[strings.TrimSpace(phase.ID)]; !ok {
			continue
		}
		if operationPhaseRequiresFreshApprovalGate(phase) {
			continue
		}
		out = append(out, phase)
	}
	return out
}

func operationPlanLeaseProposalID(lease session.OperationPlanLease) string {
	lease = session.NormalizeOperationPlanLease(lease)
	base := firstNonEmptyContinuation(lease.OperationID, lease.ID, lease.Summary, "plan-lease")
	id := sanitizeOperationPhaseProposalID("plan-lease-" + base)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func operationPlanLeaseBoundedEffect(lease session.OperationPlanLease) string {
	lease = session.NormalizeOperationPlanLease(lease)
	parts := []string{"Work inside this approved plan budget only; stop for hard gates or anything outside the disclosed lanes."}
	if lease.TurnBudget > 0 {
		parts = append(parts, fmt.Sprintf("turn_budget=%d", lease.TurnBudget))
	}
	if lease.RemainingTurns > 0 {
		parts = append(parts, fmt.Sprintf("remaining_turns=%d", lease.RemainingTurns))
	}
	for _, lane := range lease.Lanes {
		label := firstNonEmptyContinuation(lane.ID, lane.Summary, "lane")
		detail := strings.TrimSpace(label)
		if authority := strings.TrimSpace(lane.AuthorityClass); authority != "" {
			detail += " " + authority
		}
		if lane.ExpectedTurns > 0 {
			detail += fmt.Sprintf(" %d turn(s)", lane.ExpectedTurns)
		}
		if summary := strings.TrimSpace(lane.Summary); summary != "" && summary != label {
			detail += ": " + summary
		}
		parts = append(parts, "lane "+detail)
	}
	if len(lease.ValidationGates) > 0 {
		parts = append(parts, "validation gates: "+strings.Join(lease.ValidationGates, "; "))
	}
	if len(lease.ExitConditions) > 0 {
		parts = append(parts, "exit conditions: "+strings.Join(lease.ExitConditions, "; "))
	}
	return strings.Join(parts, " | ")
}

func operationPlanLeaseAllowedActions(lease session.OperationPlanLease) []string {
	lease = session.NormalizeOperationPlanLease(lease)
	actions := []string{
		"approve_operation_plan_lease",
		"record_plan_lease_approval",
		"use_plan_lease_as_bounded_envelope",
		"require_separate_capability_grant_for_external_effects",
		"report_plan_lease_evidence_digest",
	}
	actions = append(actions, lease.AllowedActions...)
	for _, lane := range lease.Lanes {
		actions = append(actions, lane.AllowedActions...)
	}
	return session.NormalizeActionProposal(session.ActionProposal{AllowedActions: actions}).AllowedActions
}

func operationPlanLeaseForbiddenActions(lease session.OperationPlanLease) []string {
	lease = session.NormalizeOperationPlanLease(lease)
	actions := []string{
		"treat_plan_lease_as_capability_grant",
		"activate_unapproved_autonomous_work",
		"bypass_lane_authority",
		"bypass_hard_interrupt",
		"deploy_or_restart_without_parking",
		"grant_or_revoke_capability",
		"mailbox_access_without_separate_grant",
		"external_effect_without_separate_grant",
	}
	actions = append(actions, lease.ForbiddenActions...)
	actions = append(actions, lease.HardInterrupts...)
	for _, lane := range lease.Lanes {
		actions = append(actions, lane.ForbiddenActions...)
	}
	return session.NormalizeActionProposal(session.ActionProposal{ForbiddenActions: actions}).ForbiddenActions
}

func operationPlanLeaseValidationPlan(lease session.OperationPlanLease) []string {
	lease = session.NormalizeOperationPlanLease(lease)
	plan := []string{
		"verify every leased lane declares authority_class and expected_turns",
		"stop and ask for a separate grant at any hard interrupt",
		"do not treat plan approval as tool, capability, deploy, or restart authority",
		"record evidence digest before proposing follow-up lease",
	}
	plan = append(plan, lease.ValidationGates...)
	plan = append(plan, lease.ExitConditions...)
	return session.NormalizeActionProposal(session.ActionProposal{ValidationPlan: plan}).ValidationPlan
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
	action = applyContinuationLeaseClassBoundaries(action)
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
	actionProposal = applyContinuationLeaseClassBoundaries(actionProposal)
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

func operationStateWithMaterializedPlanLease(opState session.OperationState, state session.ContinuationState, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	opState.Status = session.OperationStatusBlocked
	opState.Stage = "plan_lease_approval"
	opState.Proposal = session.OperationProposal{
		ID:            strings.TrimSpace(state.ActionProposal.OperationID),
		Kind:          strings.TrimSpace(state.ActionProposal.RiskClass),
		Summary:       strings.TrimSpace(state.ActionProposal.Summary),
		WhyNow:        strings.TrimSpace(state.ActionProposal.WhyNow),
		BoundedEffect: strings.TrimSpace(state.ActionProposal.BoundedEffect),
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	if opState.PlanLease.Status == "" {
		opState.PlanLease.Status = session.PlanLeaseStatusProposed
	}
	covered := make(map[string]struct{}, len(opState.PlanLease.CoveredPhaseIDs))
	for _, id := range opState.PlanLease.CoveredPhaseIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			covered[trimmed] = struct{}{}
		}
	}
	if len(covered) > 0 {
		firstCovered := ""
		for i := range opState.PhasePlan.Phases {
			phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
			if _, ok := covered[phaseID]; !ok {
				continue
			}
			opState.PhasePlan.Phases[i].LeaseID = strings.TrimSpace(state.ContinuationLease.ID)
			if firstCovered == "" {
				firstCovered = phaseID
			}
		}
		if firstCovered != "" {
			opState.PhasePlan.CurrentPhaseID = firstCovered
			opState.PhasePlan.UpdatedAt = now
		}
	}
	opState.PlanLease.UpdatedAt = now
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
	planLeaseUpdated := syncOperationPlanLeaseStatusFromContinuation(&opState, state, status)
	updated := planLeaseUpdated
	if syncOperationBundlePhaseStatusFromContinuation(&opState, state, status) {
		updated = true
	}
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
		if planLeaseUpdated && continuationActionIsPlanLeaseApproval(state) {
			if state.ApprovalBundle.Active() {
				opState.Status = session.OperationStatusActive
				opState.Stage = "plan_lease_active"
			} else {
				opState.Status = session.OperationStatusBlocked
				opState.Stage = "plan_lease_approved"
			}
		} else {
			opState.Status = session.OperationStatusActive
		}
	} else if status == session.ProposalStatusDenied || status == session.ProposalStatusExpired || status == session.ProposalStatusSuperseded {
		opState.Status = session.OperationStatusBlocked
	}
	opState.UpdatedAt = time.Now().UTC()
	_ = r.store.UpdateOperationState(key, opState)
}

func syncOperationPlanLeaseStatusFromContinuation(opState *session.OperationState, state session.ContinuationState, status session.ProposalStatus) bool {
	if opState == nil {
		return false
	}
	*opState = session.NormalizeOperationState(*opState)
	state = session.NormalizeContinuationState(state)
	if !continuationActionIsPlanLeaseApproval(state) {
		return false
	}
	leaseID := strings.TrimSpace(state.ActionProposal.OperationID)
	if leaseID == "" {
		leaseID = strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-plan-lease-")
	}
	if leaseID == "" || strings.TrimSpace(opState.PlanLease.ID) != leaseID {
		return false
	}
	now := time.Now().UTC()
	switch status {
	case session.ProposalStatusApproved:
		if state.ApprovalBundle.Active() {
			opState.PlanLease.Status = session.PlanLeaseStatusActive
		} else {
			opState.PlanLease.Status = session.PlanLeaseStatusApproved
		}
		opState.PlanLease.ApprovedBy = firstNonZeroInt64(state.ContinuationLease.ApprovedBy, state.ApprovedBy)
		if !state.ContinuationLease.ApprovedAt.IsZero() {
			opState.PlanLease.ApprovedAt = state.ContinuationLease.ApprovedAt.UTC()
		} else {
			opState.PlanLease.ApprovedAt = now
		}
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = session.ProposalStatusApproved
			opState.Proposal.UpdatedAt = now
		}
	case session.ProposalStatusDenied:
		opState.PlanLease.Status = session.PlanLeaseStatusRevoked
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = session.ProposalStatusDenied
			opState.Proposal.UpdatedAt = now
		}
	case session.ProposalStatusExpired, session.ProposalStatusSuperseded:
		opState.PlanLease.Status = session.PlanLeaseStatusExpired
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = status
			opState.Proposal.UpdatedAt = now
		}
	}
	opState.PlanLease.UpdatedAt = now
	opState.UpdatedAt = now
	*opState = session.NormalizeOperationState(*opState)
	return true
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
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
	if continuationActionIsPlanLeaseApproval(state) {
		return renderPlanBudgetPromptFallback(state)
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	title := continuationApprovalPromptTitle(state)
	if title == "" {
		title = "bounded continuation"
	}
	lines := []string{"Approval needed: " + title}
	if why := continuationPromptCompactLine(proposal.WhyNow, 220); why != "" {
		lines = append(lines, "", "Why now:", why)
	}
	if scope := continuationApprovalPromptScope(state); scope != "" {
		lines = append(lines, "", "Scope:", scope)
	}
	if included := continuationApprovalPromptIncludedLines(state); len(included) > 0 {
		lines = append(lines, "", "Included:")
		for _, line := range included {
			lines = append(lines, "- "+line)
		}
	}
	if stops := continuationApprovalPromptStops(state); len(stops) > 0 {
		lines = append(lines, "", "Stops:", strings.Join(stops, ", "))
	}
	if state.RemainingTurns > 0 {
		turnLabel := "turn"
		if state.RemainingTurns != 1 {
			turnLabel = "turns"
		}
		lines = append(lines, "", fmt.Sprintf("Approve %d bounded %s?", state.RemainingTurns, turnLabel))
	}
	return strings.Join(lines, "\n")
}

func continuationApprovalPromptTitle(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if summary := continuationPromptCompactLine(phase.Summary, 96); summary != "" {
			return summary
		}
	}
	for _, candidate := range []string{state.ActionProposal.Summary, state.StageSummary, state.Objective} {
		candidate = strings.TrimSpace(candidate)
		if idx := strings.Index(candidate, ":"); strings.HasPrefix(strings.ToLower(candidate), "approve stages ") && idx >= 0 && idx+1 < len(candidate) {
			candidate = strings.TrimSpace(candidate[idx+1:])
		}
		if title := continuationPromptCompactLine(candidate, 96); title != "" {
			return title
		}
	}
	return ""
}

func continuationApprovalPromptScope(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if scope := continuationPromptCompactLine(phase.BoundedEffect, 240); scope != "" {
			return scope
		}
	}
	if scope := continuationPromptCompactLine(state.ActionProposal.BoundedEffect, 260); scope != "" {
		return scope
	}
	return continuationPromptCompactLine(state.GovernorIntent.Constraints, 260)
}

func continuationApprovalPromptIncludedLines(state session.ContinuationState) []string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) < 2 {
		return nil
	}
	lines := make([]string, 0, minStatusInt(len(bundle.Phases), 4))
	for _, phase := range bundle.Phases {
		summary := continuationPromptCompactLine(phase.Summary, 110)
		if summary == "" {
			continue
		}
		if phase.Index > 0 {
			summary = fmt.Sprintf("phase %d: %s", phase.Index, summary)
		}
		lines = append(lines, summary)
		if len(lines) >= 4 {
			break
		}
	}
	return lines
}

func continuationApprovalPromptStops(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		value = planBudgetHumanStop(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range state.ActionProposal.ForbiddenActions {
		add(value)
	}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		for _, value := range phase.ForbiddenActions {
			add(value)
		}
	}
	if len(out) == 0 {
		out = []string{"anything outside scope", "hard gates"}
	}
	out = prioritizePlanBudgetStops(out)
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

func continuationPromptCompactLine(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	return truncatePreview(value, limit)
}

func renderPlanBudgetPromptFallback(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	lines := []string{"Approve plan budget"}
	if goal := firstNonEmptyContinuation(state.Objective, proposal.Summary); goal != "" {
		lines = append(lines, "", "Goal: "+goal)
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Budget: up to %d turn(s)", state.RemainingTurns))
	}
	if included := planBudgetIncludedLines(state); len(included) > 0 {
		lines = append(lines, "", "Included:")
		for _, line := range included {
			lines = append(lines, "- "+line)
		}
	}
	if stops := planBudgetStopLines(state); len(stops) > 0 {
		lines = append(lines, "", "Stops for: "+strings.Join(stops, ", "))
	}
	if first := planBudgetFirstStep(state); first != "" {
		lines = append(lines, "", "First step: "+first)
	}
	lines = append(lines, "", "Approving this budget does not change tool, account, mailbox, deploy, or policy permissions.")
	lines = append(lines, "Anything outside the disclosed budget needs a fresh approval.")
	return strings.Join(lines, "\n")
}

func planBudgetIncludedLines(state session.ContinuationState) []string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) == 0 {
		return nil
	}
	lines := make([]string, 0, len(bundle.Phases))
	for _, phase := range bundle.Phases {
		label := fmt.Sprintf("phase %d", phase.Index)
		if summary := strings.TrimSpace(phase.Summary); summary != "" {
			label += ": " + summary
		}
		if authority := strings.TrimSpace(phase.AuthorityClass); authority != "" {
			label += " [" + authority + "]"
		}
		lines = append(lines, label)
	}
	return lines
}

func planBudgetStopLines(state session.ContinuationState) []string {
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	seen := map[string]struct{}{}
	stops := make([]string, 0, len(proposal.ForbiddenActions))
	for _, value := range proposal.ForbiddenActions {
		value = planBudgetHumanStop(value)
		if value != "" {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			stops = append(stops, value)
		}
	}
	if len(stops) == 0 {
		stops = []string{"anything outside scope", "hard gates", "deploy/restart", "policy or permission changes", "mailbox access or mutation"}
	}
	stops = prioritizePlanBudgetStops(stops)
	if len(stops) > 5 {
		stops = stops[:5]
	}
	return stops
}

func prioritizePlanBudgetStops(stops []string) []string {
	if len(stops) == 0 {
		return nil
	}
	priority := []string{
		"anything outside scope",
		"hard gates",
		"deploy/restart",
		"policy or permission changes",
		"mailbox access or mutation",
		"credentials/tokens",
		"external account/effect",
		"spend",
		"public contact/posting",
		"unapproved autonomous work",
	}
	seen := make(map[string]struct{}, len(stops))
	for _, stop := range stops {
		if stop = strings.TrimSpace(stop); stop != "" {
			seen[stop] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	add := func(stop string) {
		if _, ok := seen[stop]; !ok {
			return
		}
		out = append(out, stop)
		delete(seen, stop)
	}
	for _, stop := range priority {
		add(stop)
	}
	for _, stop := range stops {
		add(strings.TrimSpace(stop))
	}
	return out
}

func planBudgetHumanStop(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	switch {
	case value == "":
		return ""
	case strings.Contains(value, "credential") || strings.Contains(value, "token"):
		return "credentials/tokens"
	case strings.Contains(value, "mailbox"):
		return "mailbox access or mutation"
	case strings.Contains(value, "deploy") || strings.Contains(value, "restart"):
		return "deploy/restart"
	case strings.Contains(value, "hard interrupt"):
		return "hard gates"
	case strings.Contains(value, "lane") || strings.Contains(value, "outside") || strings.Contains(value, "scope") || strings.Contains(value, "budget"):
		return "anything outside scope"
	case strings.Contains(value, "policy") || strings.Contains(value, "grant") || strings.Contains(value, "permission"):
		return "policy or permission changes"
	case strings.Contains(value, "external"):
		return "external account/effect"
	case strings.Contains(value, "purchase") || strings.Contains(value, "spend"):
		return "spend"
	case strings.Contains(value, "public"):
		return "public contact/posting"
	case strings.Contains(value, "autonomous"):
		return "unapproved autonomous work"
	default:
		return value
	}
}

func planBudgetFirstStep(state session.ContinuationState) string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if phase, ok := currentContinuationBundlePhase(bundle); ok {
		return strings.TrimSpace(phase.Summary)
	}
	return strings.TrimSpace(state.StageSummary)
}
