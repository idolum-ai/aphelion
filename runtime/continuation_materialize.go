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

func (r *Runtime) MaterializeRequestedApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string) (bool, error) {
	return r.materializePendingOperationProposalApproval(ctx, key, msg, promptInput, nil)
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
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		r.recordContinuationBundleNarrowing(key, opState, []session.OperationPhase{phase}, state, "operation_phase_required_capability", now)
		if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_required_capability"); err != nil {
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
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		r.recordContinuationBundleNarrowing(key, opState, operationPlanLeasePhasesFromOperation(opState, opState.PlanLease), state, "operation_plan_lease", now)
		if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_plan_lease"); err != nil {
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
			r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
			r.recordContinuationBundleNarrowing(key, opState, operationPlanLeasePhasesFromOperation(opState, opState.PlanLease), state, "operation_plan_lease", now)
			if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_plan_lease"); err != nil {
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
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_bundle"); err != nil {
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
		r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
		r.recordContinuationBundleNarrowing(key, opState, []session.OperationPhase{phase}, state, "operation_phase_plan", now)
		if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_phase_plan"); err != nil {
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
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
	if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "operation_proposal"); err != nil {
		return false, fmt.Errorf("send operation proposal continuation approval: %w", err)
	}
	return true, nil
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
