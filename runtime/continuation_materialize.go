//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func (r *Runtime) MaterializeRequestedApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string) (bool, error) {
	return r.materializePendingOperationProposalApproval(ctx, key, msg, promptInput, nil)
}

func continuationApprovalAlreadyOffered(state session.ContinuationState, store *session.SQLiteStore, key session.SessionKey) (bool, error) {
	if store == nil {
		return false, nil
	}
	state = session.NormalizeContinuationState(state)
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	proposalID := strings.TrimSpace(state.ActionProposal.ID)
	decisionID := strings.TrimSpace(state.DecisionID)
	afterSeq := int64(0)
	for {
		events, err := store.ExecutionEventsBySession(key, afterSeq, 200)
		if err != nil {
			return false, err
		}
		if len(events) == 0 {
			return false, nil
		}
		for _, event := range events {
			afterSeq = event.Seq
			if strings.TrimSpace(event.EventType) != core.ExecutionEventContinuationOffered || strings.TrimSpace(event.Status) != "delivered" {
				continue
			}
			payload := map[string]any{}
			if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
				continue
			}
			if continuationPayloadString(payload, "delivery_status") != "delivered" {
				continue
			}
			if leaseID != "" && continuationPayloadString(payload, "lease_id") == leaseID {
				return true, nil
			}
			if proposalID != "" && continuationPayloadString(payload, "proposal_id") == proposalID {
				return true, nil
			}
			if decisionID != "" && continuationPayloadString(payload, "decision_id") == decisionID {
				return true, nil
			}
		}
		if len(events) < 200 {
			return false, nil
		}
	}
}

func continuationPayloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func (r *Runtime) sendAndRecordContinuationOfferLocked(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, text string, source string, payload map[string]any, at time.Time) error {
	if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, text, source); err != nil {
		return err
	}
	if payload == nil {
		payload = continuationExecutionPayload(state)
	}
	payload["delivery_status"] = "delivered"
	if _, err := r.appendExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "delivered", payload, at); err != nil {
		return fmt.Errorf("record delivered continuation offer: %w", err)
	}
	return nil
}

func (r *Runtime) materializePendingOperationProposalApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, result *turn.Result) (bool, error) {
	if r == nil {
		return false, nil
	}
	unlock := r.lockSession(key)
	defer unlock()
	return r.materializePendingOperationProposalApprovalLocked(ctx, key, msg, promptInput, result)
}

func (r *Runtime) materializePendingOperationProposalApprovalLocked(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, _ *turn.Result) (bool, error) {
	if r == nil || r.store == nil || r.outbound == nil || msg.ChatID == 0 {
		return false, nil
	}
	if _, ok := r.continuationApprovalPromptSender(); !ok {
		return false, nil
	}
	if _, err := r.materializePendingRecoveryApprovalNextActionLocked(ctx, key, msg, time.Now().UTC()); err != nil {
		return false, err
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return false, nil
	}
	opState = session.NormalizeOperationState(opState)
	now := time.Now().UTC()
	priorContinuation, priorContinuationExists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, fmt.Errorf("read prior continuation state: %w", err)
	}
	opState, staleRepaired, err := r.repairStaleContinuationDerivedOrganicProposalState(ctx, key, msg.ChatID, opState, priorContinuation, priorContinuationExists, now, true, "materialization_repair")
	if err != nil {
		return false, err
	}
	if staleRepaired {
		return false, nil
	}
	if priorContinuationExists {
		var completed bool
		opState, completed = operationStateWithConsumedWorkContinuationPhaseCompleted(opState, priorContinuation, now)
		if completed {
			if err := r.store.UpdateOperationState(key, opState); err != nil {
				return false, fmt.Errorf("persist completed consumed operation phase: %w", err)
			}
		}
	}
	if repairedState, repaired := operationStateWithCompletedPhaseDuplicatesReconciled(opState, now); repaired {
		opState = repairedState
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist reconciled completed operation phase duplicates: %w", err)
		}
	}
	if repairedState, repaired := operationStateWithStalePlanLeaseCleared(opState, now); repaired {
		opState = repairedState
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist cleared stale operation plan lease: %w", err)
		}
	}
	if repairedState, repaired := operationStateWithCompletedPhasePlanClosed(opState, now); repaired {
		opState = repairedState
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist completed operation phase plan closure: %w", err)
		}
	}
	opState = operationStateWithNonCurrentInProgressPhasesCleared(opState, now)
	opState = operationStateWithInactiveCurrentPhaseLeaseCleared(opState, priorContinuation, priorContinuationExists, now)
	if priorContinuationExists {
		var repaired bool
		opState, repaired = r.repairInvalidPendingPhaseApproval(ctx, key, msg, opState, priorContinuation, now)
		if repaired {
			priorContinuation, priorContinuationExists, err = r.store.ContinuationStateIfExists(key)
			if err != nil {
				return false, fmt.Errorf("read repaired continuation state: %w", err)
			}
		}
	}
	if repaired, ok, err := r.repairTerminalContinuationProjection(ctx, key, msg, opState, priorContinuation, priorContinuationExists, now, "materialization_repair", true); err != nil {
		return false, err
	} else if ok {
		priorContinuation = repaired
		priorContinuationExists = true
	}
	if repaired, ok, err := r.repairSupersededContinuationProjection(ctx, key, msg, opState, priorContinuation, priorContinuationExists, now, "materialization_repair"); err != nil {
		return false, err
	} else if ok {
		priorContinuation = repaired
		priorContinuationExists = true
	}
	if operationStatusIsTerminal(opState.Status) {
		return false, nil
	}
	if viability := r.operationContinuationCandidateViability(key, opState, now); !viability.Live {
		r.recordSuppressedOperationContinuationCandidate(key, opState, viability, now)
		return false, nil
	}
	if phase, ok := nextOperationPhaseForApproval(opState); ok && len(phase.RequiredCapabilityGrants) > 0 {
		now := time.Now().UTC()
		if reason := operationPhaseApprovalBlockedReason(phase); reason != "" {
			if repairedState, repaired := operationStateWithApprovalBoundaryDeliberationPlan(opState, phase, reason, now); repaired {
				if err := r.store.UpdateOperationState(key, repairedState); err != nil {
					return false, fmt.Errorf("persist required-capability approval-boundary deliberation plan: %w", err)
				}
				opState = repairedState
			} else {
				r.recordAndSendBlockedOperationPhaseApproval(ctx, key, msg, opState, phase, reason, now)
				return true, nil
			}
		}
		if operationPhaseIsPlanningOnlyApproval(phase) {
			r.recordPlanningOnlyOperationPhaseBlocked(key, opState, phase, now)
			return true, nil
		}
		priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
		if err != nil {
			return false, fmt.Errorf("read required-capability phase continuation state: %w", err)
		}
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPhaseMatchesContinuation(opState, phase, priorState) {
			return true, nil
		}
		state := continuationStateFromOperationPhase(opState, phase, promptInput, now)
		if updatedOpState, blocked, err := r.blockInvalidMaterializedContinuationAuthority(ctx, key, msg, opState, state, "operation_phase_required_capability", now); err != nil || blocked {
			return true, err
		} else {
			opState = updatedOpState
		}
		opState = operationStateWithMaterializedPhaseLease(opState, phase.ID, state, now)
		if reused, err := r.consumeActiveContinuationLeaseForMaterializedState(ctx, key, msg, opState, state, "operation_phase_required_capability", now); err != nil || reused {
			return true, err
		}
		if err := r.store.UpdateOperationState(key, opState); err != nil {
			return false, fmt.Errorf("persist required-capability operation phase lease state: %w", err)
		}
		if err := r.store.UpdateContinuationState(key, state); err != nil {
			return false, fmt.Errorf("persist required-capability operation phase continuation state: %w", err)
		}
		payload := continuationExecutionPayload(state)
		payload["materialized_from"] = "operation_phase_required_capability"
		payload["phase_plan_id"] = strings.TrimSpace(opState.PhasePlan.ID)
		payload["phase_id"] = strings.TrimSpace(phase.ID)
		r.recordContinuationBundleNarrowing(key, opState, []session.OperationPhase{phase}, state, "operation_phase_required_capability", now)
		if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_required_capability", payload, now); err != nil {
			return false, fmt.Errorf("send required-capability operation phase continuation approval: %w", err)
		}
		return true, nil
	}

	if pendingOperationPlanLeaseNeedsButton(opState.PlanLease) {
		now := time.Now().UTC()
		priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
		if err != nil {
			return false, fmt.Errorf("read plan-lease continuation state: %w", err)
		}
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPlanLeaseMatchesContinuation(opState.PlanLease, priorState) {
			return true, nil
		}

		state := continuationStateFromOperationPlanLease(opState, opState.PlanLease, promptInput, now)
		if updatedOpState, blocked, err := r.blockInvalidMaterializedContinuationAuthority(ctx, key, msg, opState, state, "operation_plan_lease", now); err != nil || blocked {
			return true, err
		} else {
			opState = updatedOpState
		}
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
		r.recordContinuationBundleNarrowing(key, opState, operationPlanLeasePhasesFromOperation(opState, opState.PlanLease), state, "operation_plan_lease", now)
		if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_plan_lease", payload, now); err != nil {
			return false, fmt.Errorf("send operation plan lease continuation approval: %w", err)
		}
		return true, nil
	}
	if opState.Stage != "approval_request" {
		if lease, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC()); ok {
			now := time.Now().UTC()
			opState.PlanLease = lease
			priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
			if err != nil {
				return false, fmt.Errorf("read synthesized plan-lease continuation state: %w", err)
			}
			priorState = session.NormalizeContinuationState(priorState)
			if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPlanLeaseMatchesContinuation(opState.PlanLease, priorState) {
				return true, nil
			}

			state := continuationStateFromOperationPlanLease(opState, opState.PlanLease, promptInput, now)
			if updatedOpState, blocked, err := r.blockInvalidMaterializedContinuationAuthority(ctx, key, msg, opState, state, "operation_plan_lease", now); err != nil || blocked {
				return true, err
			} else {
				opState = updatedOpState
			}
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
			r.recordContinuationBundleNarrowing(key, opState, operationPlanLeasePhasesFromOperation(opState, opState.PlanLease), state, "operation_plan_lease", now)
			if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_plan_lease", payload, now); err != nil {
				return false, fmt.Errorf("send synthesized operation plan lease continuation approval: %w", err)
			}
			return true, nil
		}
	}
	if bundle, ok := nextOperationPhaseBundleForApproval(opState); ok {
		now := time.Now().UTC()
		priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
		if err != nil {
			return false, fmt.Errorf("read phase-bundle continuation state: %w", err)
		}
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPhaseBundleMatchesContinuation(opState, bundle, priorState) {
			return true, nil
		}

		state := continuationStateFromOperationPhaseBundle(opState, bundle, promptInput, now)
		if updatedOpState, blocked, err := r.blockInvalidMaterializedContinuationAuthority(ctx, key, msg, opState, state, "operation_phase_bundle", now); err != nil || blocked {
			return true, err
		} else {
			opState = updatedOpState
		}
		opState = operationStateWithMaterializedPhaseBundleLease(opState, bundle, state, now)
		if reused, err := r.consumeActiveContinuationLeaseForMaterializedState(ctx, key, msg, opState, state, "operation_phase_bundle", now); err != nil || reused {
			return true, err
		}
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
		if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_bundle", payload, now); err != nil {
			return false, fmt.Errorf("send operation phase bundle continuation approval: %w", err)
		}
		return true, nil
	}
	if phase, ok := nextOperationPhaseForApproval(opState); ok {
		now := time.Now().UTC()
		if reason := operationPhaseApprovalBlockedReason(phase); reason != "" {
			if repairedState, repaired := operationStateWithApprovalBoundaryDeliberationPlan(opState, phase, reason, now); repaired {
				if err := r.store.UpdateOperationState(key, repairedState); err != nil {
					return false, fmt.Errorf("persist approval-boundary deliberation plan: %w", err)
				}
				opState = repairedState
				var refreshed bool
				phase, refreshed = nextOperationPhaseForApproval(opState)
				if !refreshed {
					return true, nil
				}
			} else {
				r.recordAndSendBlockedOperationPhaseApproval(ctx, key, msg, opState, phase, reason, now)
				return true, nil
			}
		}
		if operationPhaseIsPlanningOnlyApproval(phase) {
			r.recordPlanningOnlyOperationPhaseBlocked(key, opState, phase, now)
			return true, nil
		}
		priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
		if err != nil {
			return false, fmt.Errorf("read phase continuation state: %w", err)
		}
		priorState = session.NormalizeContinuationState(priorState)
		if priorExists && continuationStateHasFreshPendingLease(priorState, now) && operationPhaseMatchesContinuation(opState, phase, priorState) {
			return true, nil
		}

		state := continuationStateFromOperationPhase(opState, phase, promptInput, now)
		if updatedOpState, blocked, err := r.blockInvalidMaterializedContinuationAuthority(ctx, key, msg, opState, state, "operation_phase_plan", now); err != nil || blocked {
			return true, err
		} else {
			opState = updatedOpState
		}
		opState = operationStateWithMaterializedPhaseLease(opState, phase.ID, state, now)
		if reused, err := r.consumeActiveContinuationLeaseForMaterializedState(ctx, key, msg, opState, state, "operation_phase_plan", now); err != nil || reused {
			return true, err
		}
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
		r.recordContinuationBundleNarrowing(key, opState, []session.OperationPhase{phase}, state, "operation_phase_plan", now)
		if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_plan", payload, now); err != nil {
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
	priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, fmt.Errorf("read proposal continuation state: %w", err)
	}
	priorState = session.NormalizeContinuationState(priorState)
	if priorExists && priorState.Status == session.ContinuationStatusPending && operationProposalMatchesContinuation(proposal, priorState) {
		alreadyOffered, err := continuationApprovalAlreadyOffered(priorState, r.store, key)
		if err != nil {
			return false, fmt.Errorf("read delivered continuation offers: %w", err)
		}
		if alreadyOffered {
			return true, nil
		}
		payload := continuationExecutionPayload(priorState)
		payload["materialized_from"] = "operation_proposal_existing_continuation"
		if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, priorState, renderOperationProposalMaterializedPromptFallback(priorState), "operation_proposal_existing_continuation", payload, now); err != nil {
			return false, fmt.Errorf("send existing operation proposal continuation approval: %w", err)
		}
		return true, nil
	}

	now = time.Now().UTC()
	state := continuationStateFromOperationProposal(opState, promptInput, now)
	if updatedOpState, blocked, err := r.blockInvalidMaterializedContinuationAuthority(ctx, key, msg, opState, state, "operation_proposal", now); err != nil || blocked {
		return true, err
	} else {
		opState = updatedOpState
	}
	if reused, err := r.consumeActiveContinuationLeaseForMaterializedState(ctx, key, msg, opState, state, "operation_proposal", now); err != nil || reused {
		return true, err
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return false, fmt.Errorf("persist operation proposal continuation state: %w", err)
	}
	payload := continuationExecutionPayload(state)
	payload["materialized_from"] = "operation_proposal"
	if err := r.sendAndRecordContinuationOfferLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_proposal", payload, now); err != nil {
		return false, fmt.Errorf("send operation proposal continuation approval: %w", err)
	}
	return true, nil
}

type recoveryApprovalHandoffInput struct {
	Action                string            `json:"action"`
	LeaseClass            string            `json:"lease_class"`
	Principal             string            `json:"principal"`
	AllowedActions        []string          `json:"allowed_actions"`
	Constraints           map[string]string `json:"constraints"`
	Tool                  string            `json:"tool"`
	ToolAction            string            `json:"tool_action"`
	GrantID               string            `json:"grant_id"`
	GrantTargetResource   string            `json:"grant_target_resource"`
	Resource              string            `json:"resource"`
	RequestInstanceID     string            `json:"request_instance_id"`
	AgentID               string            `json:"agent_id"`
	RecoveryContract      string            `json:"recovery_contract"`
	RecoveryOperationKind string            `json:"recovery_operation_kind"`
}

func (r *Runtime) materializePendingRecoveryApprovalNextActionLocked(ctx context.Context, key session.SessionKey, msg core.InboundMessage, now time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	actor, ok := r.recoveryApprovalMaterializationActor(msg)
	if !ok {
		return false, nil
	}
	tools := r.toolsForPrincipal(actor, key)
	if tools == nil {
		return false, nil
	}
	actions, err := r.store.OpenNextActionsBySessionOperation(key, session.NextActionBlockedNeedsAuthority, "request_approval", "continuation_lease_request", 20)
	if err != nil {
		return false, err
	}
	for _, action := range actions {
		if !recoveryApprovalNextActionConsumable(action) {
			continue
		}
		if _, err := tools.Execute(ctx, "request_approval", json.RawMessage(action.OperationInputJSON)); err != nil {
			return false, fmt.Errorf("materialize recovery approval handoff %s: %w", action.RecordID, err)
		}
		if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
			Key:         key,
			Owner:       "runtime",
			SubjectKind: action.SubjectKind,
			SubjectRef:  action.SubjectRef,
			Reason:      "recovery_handoff_materialized",
			ResolvedAt:  now,
		}); err != nil {
			return false, fmt.Errorf("resolve recovery approval handoff %s: %w", action.RecordID, err)
		}
		return true, nil
	}
	return false, nil
}

func (r *Runtime) recoveryApprovalMaterializationActor(msg core.InboundMessage) (principal.Principal, bool) {
	if r == nil || r.resolver == nil || msg.SenderID == 0 {
		return principal.Principal{}, false
	}
	actor, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
	if !ok {
		return principal.Principal{}, false
	}
	return actor, true
}

func recoveryApprovalNextActionConsumable(action session.NextActionRecord) bool {
	if action.State != session.NextActionBlockedNeedsAuthority {
		return false
	}
	if strings.TrimSpace(action.SubjectKind) != "continuation_lease_request" {
		return false
	}
	if strings.TrimSpace(action.ResourceBlocker) != "missing_continuation_lease" {
		return false
	}
	if strings.TrimSpace(action.OperationTool) != "request_approval" || strings.TrimSpace(action.OperationKind) != "continuation_lease_request" {
		return false
	}
	if strings.TrimSpace(action.OperationInputJSON) == "" {
		return false
	}
	var input recoveryApprovalHandoffInput
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &input); err != nil {
		return false
	}
	return recoveryApprovalHandoffInputConsumable(input)
}

func recoveryApprovalHandoffInputConsumable(input recoveryApprovalHandoffInput) bool {
	if strings.TrimSpace(input.RecoveryContract) != "aphelion.recovery_handoff.v1" ||
		strings.TrimSpace(input.RecoveryOperationKind) != "continuation_lease_request" {
		return false
	}
	if strings.TrimSpace(input.Action) != "request_continuation_lease" ||
		strings.TrimSpace(input.Principal) == "" ||
		strings.TrimSpace(input.RequestInstanceID) == "" {
		return false
	}
	leaseClass := session.NormalizeContinuationLeaseClass(session.ContinuationLeaseClass(input.LeaseClass))
	if leaseClass == "" || len(normalizeRecoveryApprovalActions(input.AllowedActions)) == 0 {
		return false
	}
	constraints := normalizeRecoveryApprovalConstraints(input.Constraints)
	toolName := strings.TrimSpace(input.Tool)
	toolAction := strings.ToLower(strings.TrimSpace(input.ToolAction))
	switch leaseClass {
	case session.ContinuationLeaseClassChildWake:
		agentID := strings.TrimSpace(input.AgentID)
		return toolName == "durable_agent" &&
			toolAction == "wake_once" &&
			agentID != "" &&
			recoveryApprovalActionsContain(input.AllowedActions, "wake_named_child") &&
			recoveryApprovalConstraintMatches(constraints, "agent_id", agentID)
	case session.ContinuationLeaseClassDataAccess, session.ContinuationLeaseClassLocalWorkspace:
		grantID := strings.TrimSpace(input.GrantID)
		grantTarget := strings.TrimSpace(input.GrantTargetResource)
		resource := strings.TrimSpace(input.Resource)
		if grantID == "" || grantTarget == "" || resource == "" || toolName == "" || toolAction == "" {
			return false
		}
		required := map[string]string{
			"grant_id":              grantID,
			"grant_target_resource": grantTarget,
			"target_resource":       grantTarget,
			"resource":              resource,
			"tool":                  toolName,
			"tool_action":           toolAction,
		}
		for key, want := range required {
			if !recoveryApprovalConstraintMatches(constraints, key, want) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

func normalizeRecoveryApprovalActions(actions []string) []string {
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		action = strings.ToLower(strings.TrimSpace(action))
		if action != "" {
			out = append(out, action)
		}
	}
	return out
}

func recoveryApprovalActionsContain(actions []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, action := range normalizeRecoveryApprovalActions(actions) {
		if action == want {
			return true
		}
	}
	return false
}

func normalizeRecoveryApprovalConstraints(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func recoveryApprovalConstraintMatches(constraints map[string]string, key string, want string) bool {
	want = strings.TrimSpace(want)
	return want != "" && strings.TrimSpace(constraints[strings.TrimSpace(key)]) == want
}

const operationPlanBudgetMaxLanes = 6

type operationPhaseApprovalKind string

const (
	operationPhaseApprovalNone       operationPhaseApprovalKind = "none"
	operationPhaseApprovalPlanBudget operationPhaseApprovalKind = "plan_budget"
	operationPhaseApprovalFresh      operationPhaseApprovalKind = "fresh"
	operationPhaseApprovalBlocked    operationPhaseApprovalKind = "blocked"
)

const operationApprovalBundleMaxPhases = 6

type operationBlockedApprovalKind string

const (
	operationBlockedApprovalUnknown operationBlockedApprovalKind = ""
	operationBlockedApprovalOptIn   operationBlockedApprovalKind = "opt_in"
	operationBlockedApprovalConsent operationBlockedApprovalKind = "consent"
)
