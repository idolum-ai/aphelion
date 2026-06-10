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

const continuationOperationalStateNote = "operational continuation_state remains authoritative"

const continuationLeaseDefaultTTL = 30 * time.Minute

const (
	continuationActionApproveLease         = "approve_lease"
	continuationActionApproveBundleAll     = "approve_bundle_all"
	continuationActionApproveBundleCurrent = "approve_bundle_current"
	continuationActionContinueOnce         = "continue_once"
	continuationActionAskEdit              = "ask_edit"
	continuationActionStopPark             = "stop_park"
	continuationActionResumeEdge           = "resume_edge"
	continuationActionAskNextLease         = "ask_next_lease"
	continuationActionStatusOnly           = "status_only"
	continuationActionStop                 = "stop"
)

func (r *Runtime) offerContinuationApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, result *turn.Result) error {
	if r == nil {
		return nil
	}
	unlock := r.lockSession(key)
	defer unlock()
	return r.offerContinuationApprovalLocked(ctx, key, msg, promptInput, result)
}

func (r *Runtime) offerContinuationApprovalLocked(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, result *turn.Result) error {
	if r == nil || r.outbound == nil || r.store == nil {
		return nil
	}
	priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return fmt.Errorf("read prior continuation state: %w", err)
	}

	consensus := r.buildContinuationConsensus(key, result)
	objective, nextStep := summarizeContinuationPlan(consensus.PlanState, consensus.OperationState, promptInput)

	state := session.ContinuationState{
		Kind:                   session.TurnAuthorizationKindContinuation,
		Status:                 session.ContinuationStatusIdle,
		Objective:              objective,
		StageSummary:           nextStep,
		RemainingTurns:         0,
		PersonaIntent:          consensus.PersonaIntent,
		GovernorIntent:         consensus.GovernorIntent,
		HandshakeBlockedReason: consensus.BlockedReason,
		UpdatedAt:              time.Now().UTC(),
	}
	eligible := consensus.eligible()
	quietClose := continuationConsensusShouldCloseQuietly(consensus)
	missingTypedWork := eligible && !continuationConsensusHasTypedRemainingWork(consensus)
	if quietClose {
		state.HandshakeBlockedReason = ""
	}
	if eligible && !quietClose && !missingTypedWork {
		state.Status = session.ContinuationStatusPending
		state.DecisionID = newContinuationDecisionID()
		state.RemainingTurns = 1
		state.ActionProposal = buildContinuationActionProposal(state.DecisionID, consensus, objective, nextStep, time.Now().UTC())
		state.ContinuationLease = buildContinuationLease(state.ActionProposal, state.RemainingTurns, time.Now().UTC())
		if _, blocked, err := r.blockInvalidContinuationAuthorityContract(ctx, key, msg, state, "organic_continuation", time.Now().UTC(), true); blocked || err != nil {
			return err
		}
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return fmt.Errorf("persist continuation state: %w", err)
	}
	if state.Status != session.ContinuationStatusPending {
		payload := continuationExecutionPayload(state)
		payload["reason"] = strings.TrimSpace(consensus.BlockedReason)
		notify := shouldNotifyContinuationBlocked(priorState, priorExists, consensus)
		if quietClose {
			payload["reason"] = "operation_completed"
			payload["user_visible"] = false
			payload["prior_active"] = priorExists && session.NormalizeContinuationState(priorState).Active()
			r.recordExecutionEvent(key, core.ExecutionEventContinuationConsumed, "continuation", "closed", payload, time.Now().UTC())
			return nil
		}
		if missingTypedWork {
			payload["reason"] = "no_typed_remaining_work"
			payload["user_visible"] = false
			payload["prior_active"] = priorExists && session.NormalizeContinuationState(priorState).Active()
			r.recordExecutionEvent(key, core.ExecutionEventContinuationConsumed, "continuation", "closed", payload, time.Now().UTC())
			return nil
		}
		payload["user_visible"] = notify
		payload["prior_active"] = priorExists && session.NormalizeContinuationState(priorState).Active()
		if repaired, err := r.repairOrganicContinuationHandshakeCompileBlock(ctx, key, msg, consensus, state, strings.TrimSpace(consensus.BlockedReason), time.Now().UTC()); err != nil {
			return err
		} else if repaired {
			return nil
		}
		if _, ok := continuationCompileRepairForReason(consensus.BlockedReason); !ok && strings.TrimSpace(consensus.BlockedReason) != "" {
			r.recordContinuationCompileUnknownReason(key, consensus.OperationState, state, consensus.BlockedReason, "organic_continuation", notify, time.Now().UTC())
		}
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", payload, time.Now().UTC())
		if notify {
			if err := r.sendContinuationBlockedNotice(ctx, key, msg, state); err != nil {
				return err
			}
		}
		return nil
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", continuationExecutionPayload(state), time.Now().UTC())
	if approved, err := r.maybeAutoApproveContinuationOfferLocked(ctx, key, msg, state, "organic_continuation"); approved || err != nil {
		return err
	}

	return r.sendContinuationApprovalPrompt(ctx, key, msg, state, r.renderContinuationPrompt(ctx, key, msg, state))
}
