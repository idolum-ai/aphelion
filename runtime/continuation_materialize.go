//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
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
	if handledRecovery, continueMaterialization, err := r.materializePendingRecoveryApprovalNextActionLocked(ctx, key, msg, time.Now().UTC()); err != nil {
		return false, err
	} else if handledRecovery && !continueMaterialization {
		return true, nil
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
		if attached, err := r.attachOperationPhaseRecoveryContract(key, msg, phase, state, now); err != nil {
			return true, err
		} else {
			state = attached
		}
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
		if attached, err := r.attachOperationPhaseRecoveryContract(key, msg, phase, state, now); err != nil {
			return true, err
		} else {
			state = attached
		}
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
	ContractID            string            `json:"contract_id"`
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

func (r *Runtime) materializePendingRecoveryApprovalNextActionLocked(ctx context.Context, key session.SessionKey, msg core.InboundMessage, now time.Time) (bool, bool, error) {
	if r == nil || r.store == nil {
		return false, false, nil
	}
	actor, ok := r.recoveryApprovalMaterializationActor(msg)
	if !ok {
		return false, false, nil
	}
	tools := r.toolsForPrincipal(actor, key)
	if tools == nil {
		return false, false, nil
	}
	actions, err := r.store.OpenNextActionsBySessionOperation(key, session.NextActionBlockedNeedsAuthority, "request_approval", "continuation_lease_request", 100)
	if err != nil {
		return false, false, err
	}
	var deferredConflicts []recoveryApprovalDeferredConflict
	for _, action := range actions {
		consumable, invalid := recoveryApprovalNextActionConsumable(action)
		if invalid {
			if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
				RecordID:    action.RecordID,
				Key:         key,
				Owner:       "runtime",
				SubjectKind: action.SubjectKind,
				SubjectRef:  action.SubjectRef,
				Reason:      "invalid_recovery_handoff",
				ResolvedAt:  now,
			}); err != nil {
				return false, false, fmt.Errorf("resolve invalid recovery approval handoff %s: %w", action.RecordID, err)
			}
			continue
		}
		if !consumable {
			continue
		}
		if _, err := tools.Execute(ctx, "request_approval", json.RawMessage(action.OperationInputJSON)); err != nil {
			var conflict toolpkg.RequestApprovalContinuationConflictError
			if !errors.As(err, &conflict) {
				return false, false, fmt.Errorf("materialize recovery approval handoff %s: %w", action.RecordID, err)
			}
			retry, handled, handleErr := r.adjudicateRecoveryApprovalContinuationConflictLocked(ctx, key, msg, action, conflict, now)
			if handleErr != nil {
				return false, false, handleErr
			}
			if !handled {
				return false, false, fmt.Errorf("materialize recovery approval handoff %s: %w", action.RecordID, err)
			}
			if !retry {
				deferredConflicts = append(deferredConflicts, recoveryApprovalDeferredConflict{Action: action, Conflict: conflict})
				continue
			}
			if _, err := tools.Execute(ctx, "request_approval", json.RawMessage(action.OperationInputJSON)); err != nil {
				return false, false, fmt.Errorf("materialize recovery approval handoff %s after adjudication: %w", action.RecordID, err)
			}
		}
		if err := r.resolveDeferredRecoveryApprovalConflicts(key, deferredConflicts, action.RecordID, "superseded_by_later_recovery_handoff", now); err != nil {
			return false, false, err
		}
		if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
			RecordID:    action.RecordID,
			Key:         key,
			Owner:       "runtime",
			SubjectKind: action.SubjectKind,
			SubjectRef:  action.SubjectRef,
			Reason:      "recovery_handoff_materialized",
			ResolvedAt:  now,
		}); err != nil {
			return false, false, fmt.Errorf("resolve recovery approval handoff %s: %w", action.RecordID, err)
		}
		return true, true, nil
	}
	if len(deferredConflicts) > 0 {
		selected := newestRecoveryApprovalDeferredConflict(deferredConflicts)
		if err := r.resolveDeferredRecoveryApprovalConflicts(key, deferredConflicts, selected.Action.RecordID, "superseded_by_newer_blocker", now); err != nil {
			return false, false, err
		}
		if err := r.emitRecoveryApprovalContinuationConflictBlocker(key, selected.Action, selected.Conflict, now); err != nil {
			return false, false, err
		}
		return true, false, nil
	}
	return false, false, nil
}

type recoveryApprovalDeferredConflict struct {
	Action   session.NextActionRecord
	Conflict toolpkg.RequestApprovalContinuationConflictError
}

func (r *Runtime) adjudicateRecoveryApprovalContinuationConflictLocked(ctx context.Context, key session.SessionKey, msg core.InboundMessage, action session.NextActionRecord, conflict toolpkg.RequestApprovalContinuationConflictError, now time.Time) (bool, bool, error) {
	if r == nil || r.store == nil {
		return false, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state, ok, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, false, fmt.Errorf("read continuation conflict state: %w", err)
	}
	if !ok {
		r.recordRecoveryApprovalConflictAdjudication(key, action, conflict, "missing_current_state", "retry_without_conflict", now)
		return true, true, nil
	}
	state = session.NormalizeContinuationState(state)
	if !recoveryApprovalConflictMatchesCurrentState(state, conflict) {
		r.recordRecoveryApprovalConflictAdjudication(key, action, conflict, "current_state_changed", "retry_without_conflict", now)
		return true, true, nil
	}
	if state.Status == session.ContinuationStatusPending &&
		state.ContinuationLease.Status == session.ContinuationLeaseStatusPending &&
		recoveryApprovalHandoffCanSupersedePending(action, state) {
		repaired := continuationStateWithSupersededProjection(state, now)
		repaired.HandshakeBlockedReason = "superseded_by_recovery_approval_handoff"
		if err := r.store.UpdateContinuationState(key, repaired); err != nil {
			return false, false, fmt.Errorf("supersede conflicting pending continuation %s: %w", strings.TrimSpace(state.ContinuationLease.ID), err)
		}
		r.recordRecoveryApprovalConflictAdjudication(key, action, conflict, "stale_pending_superseded", "retry_request_approval", now)
		if _, err := r.store.RecordNextAction(session.NextActionInput{
			Key:                key,
			Owner:              "continuation",
			State:              session.NextActionSuperseded,
			SubjectKind:        "continuation_approval",
			SubjectRef:         firstNonEmptyContinuation(state.ContinuationLease.ID, state.DecisionID, state.ActionProposal.ID),
			CausalRefs:         recoveryApprovalConflictCausalRefs(action, state, conflict),
			NextAction:         "retire the stale pending continuation approval and materialize the requested recovery approval",
			RetryPolicy:        "do_not_use_superseded_prompt",
			OperatorProjection: "A stale pending approval was superseded by a newer typed recovery handoff.",
			CreatedAt:          now,
		}); err != nil {
			return false, false, fmt.Errorf("record superseded recovery approval conflict: %w", err)
		}
		chatID := msg.ChatID
		if chatID == 0 {
			chatID = key.ChatID
		}
		if chatID != 0 {
			r.retireStaleContinuationApprovalCards(ctx, key, chatID, continuationCallbackThreadIDForMessage(key, msg), 0, "superseded_by_recovery_handoff", now)
		}
		return true, true, nil
	}
	return false, true, nil
}

func (r *Runtime) emitRecoveryApprovalContinuationConflictBlocker(key session.SessionKey, action session.NextActionRecord, conflict toolpkg.RequestApprovalContinuationConflictError, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state, ok, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return fmt.Errorf("read blocked continuation conflict state: %w", err)
	}
	if !ok {
		state = session.ContinuationState{}
	} else {
		state = session.NormalizeContinuationState(state)
	}
	if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
		RecordID:    action.RecordID,
		Key:         key,
		Owner:       "runtime",
		SubjectKind: action.SubjectKind,
		SubjectRef:  action.SubjectRef,
		Reason:      "blocked_by_live_continuation_authority",
		ResolvedAt:  now,
	}); err != nil {
		return fmt.Errorf("resolve blocked recovery approval handoff %s: %w", action.RecordID, err)
	}
	r.recordRecoveryApprovalConflictAdjudication(key, action, conflict, "live_authority_blocks_replacement", "operator_intervention_required", now)
	if _, err := r.store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "continuation",
		State:              session.NextActionWaitingForOperator,
		SubjectKind:        "continuation_approval_conflict",
		SubjectRef:         strings.TrimSpace(action.RecordID),
		CausalRefs:         recoveryApprovalConflictCausalRefs(action, state, conflict),
		NextAction:         "finish, stop, or revoke the active continuation before requesting this recovery approval",
		RequiredAuthority:  string(conflict.RequestedLeaseClass),
		ResourceBlocker:    "live_continuation_conflict",
		RetryPolicy:        "operator_must_resolve_existing_authority",
		OperatorProjection: "The requested recovery approval conflicts with a live continuation lease, so Aphelion did not replace it automatically.",
		CreatedAt:          now,
	}); err != nil {
		return fmt.Errorf("record live continuation conflict next action: %w", err)
	}
	return nil
}

func (r *Runtime) resolveDeferredRecoveryApprovalConflicts(key session.SessionKey, conflicts []recoveryApprovalDeferredConflict, keepRecordID string, reason string, now time.Time) error {
	if r == nil || r.store == nil || len(conflicts) == 0 {
		return nil
	}
	keepRecordID = strings.TrimSpace(keepRecordID)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, conflict := range conflicts {
		action := conflict.Action
		if strings.TrimSpace(action.RecordID) == "" || strings.TrimSpace(action.RecordID) == keepRecordID {
			continue
		}
		if err := r.store.ResolveNextAction(session.NextActionResolutionInput{
			RecordID:    action.RecordID,
			Key:         key,
			Owner:       "runtime",
			SubjectKind: action.SubjectKind,
			SubjectRef:  action.SubjectRef,
			Reason:      reason,
			ResolvedAt:  now,
		}); err != nil {
			return fmt.Errorf("resolve deferred recovery approval handoff %s: %w", action.RecordID, err)
		}
		r.recordRecoveryApprovalConflictAdjudication(key, action, conflict.Conflict, "deferred_conflict_superseded", reason, now)
	}
	return nil
}

func newestRecoveryApprovalDeferredConflict(conflicts []recoveryApprovalDeferredConflict) recoveryApprovalDeferredConflict {
	if len(conflicts) == 0 {
		return recoveryApprovalDeferredConflict{}
	}
	selected := conflicts[0]
	for _, candidate := range conflicts[1:] {
		if recoveryApprovalActionNewer(candidate.Action, selected.Action) {
			selected = candidate
		}
	}
	return selected
}

func recoveryApprovalActionNewer(left session.NextActionRecord, right session.NextActionRecord) bool {
	if left.CreatedAt.IsZero() && right.CreatedAt.IsZero() {
		return strings.TrimSpace(left.RecordID) > strings.TrimSpace(right.RecordID)
	}
	if left.CreatedAt.IsZero() {
		return false
	}
	if right.CreatedAt.IsZero() {
		return true
	}
	leftAt := left.CreatedAt.UTC()
	rightAt := right.CreatedAt.UTC()
	if leftAt.Equal(rightAt) {
		return strings.TrimSpace(left.RecordID) > strings.TrimSpace(right.RecordID)
	}
	return leftAt.After(rightAt)
}

func recoveryApprovalHandoffCanSupersedePending(action session.NextActionRecord, state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if action.CreatedAt.IsZero() || state.UpdatedAt.IsZero() {
		return true
	}
	return !action.CreatedAt.UTC().Before(state.UpdatedAt.UTC())
}

func recoveryApprovalConflictMatchesCurrentState(state session.ContinuationState, conflict toolpkg.RequestApprovalContinuationConflictError) bool {
	state = session.NormalizeContinuationState(state)
	if want := strings.TrimSpace(conflict.ExistingLeaseID); want != "" && strings.TrimSpace(state.ContinuationLease.ID) != want {
		return false
	}
	if conflict.ExistingLeaseClass != "" && state.ContinuationLease.LeaseClass != conflict.ExistingLeaseClass {
		return false
	}
	if conflict.ExistingStatus != "" && state.Status != conflict.ExistingStatus {
		return false
	}
	if conflict.ExistingLeaseStatus != "" && state.ContinuationLease.Status != conflict.ExistingLeaseStatus {
		return false
	}
	return true
}

func (r *Runtime) recordRecoveryApprovalConflictAdjudication(key session.SessionKey, action session.NextActionRecord, conflict toolpkg.RequestApprovalContinuationConflictError, decision string, outcome string, now time.Time) {
	if r == nil {
		return
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationAdjudicated, "continuation", "adjudicated", map[string]any{
		"adjudication_kind":     "recovery_approval_materialization",
		"surface":               "recovery_approval_handoff",
		"decision":              strings.TrimSpace(decision),
		"outcome":               strings.TrimSpace(outcome),
		"next_action_record_id": strings.TrimSpace(action.RecordID),
		"existing_lease_id":     strings.TrimSpace(conflict.ExistingLeaseID),
		"existing_lease_class":  string(conflict.ExistingLeaseClass),
		"existing_status":       string(conflict.ExistingStatus),
		"existing_lease_status": string(conflict.ExistingLeaseStatus),
		"requested_lease_id":    strings.TrimSpace(conflict.RequestedLeaseID),
		"requested_lease_class": string(conflict.RequestedLeaseClass),
		"request_instance_id":   strings.TrimSpace(conflict.RequestInstanceID),
	}, now)
}

func recoveryApprovalConflictCausalRefs(action session.NextActionRecord, state session.ContinuationState, conflict toolpkg.RequestApprovalContinuationConflictError) []string {
	refs := []string{}
	if id := strings.TrimSpace(action.RecordID); id != "" {
		refs = append(refs, "next_action:"+id)
	}
	if id := strings.TrimSpace(conflict.ExistingLeaseID); id != "" {
		refs = append(refs, "continuation_lease:"+id)
	}
	if id := strings.TrimSpace(conflict.RequestedLeaseID); id != "" {
		refs = append(refs, "requested_continuation_lease:"+id)
	}
	if id := strings.TrimSpace(state.DecisionID); id != "" {
		refs = append(refs, "decision:"+id)
	}
	return refs
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

func recoveryApprovalNextActionConsumable(action session.NextActionRecord) (bool, bool) {
	if action.State != session.NextActionBlockedNeedsAuthority {
		return false, false
	}
	if strings.TrimSpace(action.SubjectKind) != "continuation_lease_request" {
		return false, false
	}
	if strings.TrimSpace(action.ResourceBlocker) != "missing_continuation_lease" {
		return false, false
	}
	if strings.TrimSpace(action.OperationTool) != "request_approval" || strings.TrimSpace(action.OperationKind) != "continuation_lease_request" {
		return false, false
	}
	if strings.TrimSpace(action.OperationInputJSON) == "" {
		return false, true
	}
	if err := toolpkg.ValidateRecoveryTransitionRecord(action); err != nil {
		return false, true
	}
	var input recoveryApprovalHandoffInput
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &input); err != nil {
		return false, true
	}
	consumable := recoveryApprovalHandoffInputConsumable(input)
	return consumable, !consumable
}

func recoveryApprovalHandoffInputConsumable(input recoveryApprovalHandoffInput) bool {
	if strings.TrimSpace(input.RecoveryContract) != "aphelion.recovery_handoff.v1" ||
		strings.TrimSpace(input.RecoveryOperationKind) != "continuation_lease_request" {
		return false
	}
	if strings.TrimSpace(input.Action) != "request_continuation_lease" ||
		strings.TrimSpace(input.ContractID) == "" {
		return false
	}
	return true
}

func (r *Runtime) attachOperationPhaseRecoveryContract(key session.SessionKey, msg core.InboundMessage, phase session.OperationPhase, state session.ContinuationState, now time.Time) (session.ContinuationState, error) {
	state = session.NormalizeContinuationState(state)
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	if lease.LeaseClass != session.ContinuationLeaseClassChildWake {
		return state, nil
	}
	agentID, grantID, targetResource := operationPhaseChildWakeTarget(phase)
	if agentID == "" {
		return state, fmt.Errorf("child_wake operation phase requires exact durable_agent wake_once target")
	}
	principalID := operationPhaseRecoveryPrincipal(msg, key)
	if principalID == "" {
		return state, fmt.Errorf("child_wake operation phase requires operator principal")
	}
	allowed := append([]string(nil), lease.AllowedActions...)
	if !operationPhaseActionContains(allowed, "wake_named_child") {
		allowed = append(allowed, "wake_named_child")
	}
	constraints := map[string]string{}
	for key, value := range lease.Constraints {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			constraints[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	constraints["agent_id"] = agentID
	constraints["principal"] = principalID
	if grantID != "" {
		constraints["grant_id"] = grantID
	}
	if targetResource != "" {
		constraints["grant_target_resource"] = targetResource
		constraints["target_resource"] = targetResource
	}
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "operation-phase-" + strings.TrimSpace(state.DecisionID),
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		Principal:           principalID,
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      allowed,
		Constraints:         constraints,
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             agentID,
		GrantID:             grantID,
		GrantTargetResource: targetResource,
		CreatedAt:           now,
	})
	if err != nil {
		return state, err
	}
	contract, err = r.store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		return state, err
	}
	lease.RecoveryContractID = contract.ContractID
	lease.RetryOperation = contract.RetryOperation
	lease.Constraints = constraints
	lease.AllowedActions = allowed
	lease.PlanHash = contract.ContractHash
	state.ContinuationLease = session.NormalizeContinuationLease(lease)
	state.ActionProposal.PlanHash = contract.ContractHash
	return session.NormalizeContinuationState(state), nil
}

func operationPhaseChildWakeTarget(phase session.OperationPhase) (string, string, string) {
	phase = normalizeSingleOperationPhase(phase)
	for _, grant := range phase.RequiredCapabilityGrants {
		grant = session.NormalizeCapabilityGrantSpec(grant)
		agentID := operationPhaseAgentIDFromTargetResource(grant.TargetResource)
		if agentID == "" {
			agentID = operationPhaseAgentIDFromConstraints(grant.Constraints)
		}
		if agentID != "" {
			return agentID, grant.GrantID, grant.TargetResource
		}
	}
	return "", "", ""
}

func operationPhaseAgentIDFromTargetResource(target string) string {
	target = strings.TrimSpace(target)
	parts := strings.Split(target, ":")
	if len(parts) == 3 && parts[0] == "durable_agent" && parts[2] == "wake_once" && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func operationPhaseAgentIDFromConstraints(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	return operationPhaseFindAgentID(payload)
}

func operationPhaseFindAgentID(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.TrimSpace(key) == "agent_id" {
				if found := operationPhaseFindAgentID(child); found != "" {
					return found
				}
			}
		}
		for _, child := range typed {
			if found := operationPhaseFindAgentID(child); found != "" {
				return found
			}
		}
	case []any:
		if len(typed) == 1 {
			return operationPhaseFindAgentID(typed[0])
		}
	case string:
		return strings.TrimSpace(typed)
	}
	return ""
}

func operationPhaseRecoveryPrincipal(msg core.InboundMessage, key session.SessionKey) string {
	if msg.SenderID != 0 {
		return fmt.Sprintf("telegram:%d", msg.SenderID)
	}
	if key.UserID != 0 {
		return fmt.Sprintf("telegram:%d", key.UserID)
	}
	if key.ChatID != 0 {
		return fmt.Sprintf("telegram:%d", key.ChatID)
	}
	return ""
}

func operationPhaseActionContains(actions []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, action := range actions {
		if strings.ToLower(strings.TrimSpace(action)) == want {
			return true
		}
	}
	return false
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
