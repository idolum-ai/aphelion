//go:build linux

package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type continuationClassScopeDecision struct {
	Allowed         bool
	FailedDimension string
	Reason          string
}

func (r *Runtime) consumeActiveContinuationLeaseForMaterializedState(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, proposed session.ContinuationState, source string, now time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	prior, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	prior = session.NormalizeContinuationState(prior)
	proposed = session.NormalizeContinuationState(proposed)
	decision := continuationClassScopeDecisionForMaterializedState(prior, proposed, now)
	if !decision.Allowed {
		return false, nil
	}
	adopted := continuationStateWithActiveClassScopedLease(prior, proposed, now)
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return false, err
	}
	if err := r.store.UpdateContinuationState(key, adopted); err != nil {
		return false, err
	}
	r.syncOperationProposalStatusFromContinuation(key, adopted, session.ProposalStatusApproved)
	payload := continuationExecutionPayload(adopted)
	payload["materialized_from"] = strings.TrimSpace(source)
	payload["class_scope_reason"] = strings.TrimSpace(decision.Reason)
	payload["active_lease_id"] = strings.TrimSpace(prior.ContinuationLease.ID)
	payload["proposed_continuation_id"] = strings.TrimSpace(proposed.ActionProposal.ID)
	payload["proposed_decision_id"] = strings.TrimSpace(proposed.DecisionID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationClassScopedConsumption, "continuation", "approved", payload, now)
	if r.outbound != nil && msg.ChatID != 0 {
		text := "Continuing under the active approved lease."
		if label := strings.TrimPrefix(continuationUserFacingPlanLabel(adopted), "Plan: "); label != "" {
			text = "Continuing under the active approved lease: " + label + "."
		}
		text = r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), text)
		if _, err := r.outbound.SendMessage(ctx, core.OutboundMessage{ChatID: msg.ChatID, Text: text}); err != nil {
			return true, err
		}
	}
	return true, nil
}

func continuationClassScopeDecisionForMaterializedState(prior session.ContinuationState, proposed session.ContinuationState, now time.Time) continuationClassScopeDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	prior = session.NormalizeContinuationState(prior)
	proposed = session.NormalizeContinuationState(proposed)
	if prior.Status != session.ContinuationStatusApproved {
		return continuationClassScopeDecision{FailedDimension: "status", Reason: "prior_continuation_not_approved"}
	}
	if !prior.ContinuationLease.ActiveAt(now) {
		return continuationClassScopeDecision{FailedDimension: "lease", Reason: "prior_lease_inactive_or_expired"}
	}
	if continuationActionIsPlanLeaseApproval(proposed) {
		return continuationClassScopeDecision{FailedDimension: "lease_kind", Reason: "plan_lease_requires_explicit_approval_boundary"}
	}
	priorClass := session.NormalizeContinuationLeaseClass(prior.ContinuationLease.LeaseClass)
	proposedClass := session.NormalizeContinuationLeaseClass(proposed.ContinuationLease.LeaseClass)
	if proposedClass == "" {
		proposedClass = session.InferContinuationLeaseClass(proposed.ActionProposal.RiskClass, proposed.ActionProposal.AllowedActions, proposed.ActionProposal.BoundedEffect)
	}
	if priorClass == "" || proposedClass == "" || priorClass != proposedClass {
		return continuationClassScopeDecision{FailedDimension: "lease_class", Reason: "proposed_lease_class_does_not_match_active_lease"}
	}
	if proposalMayExternalEffect(proposed.ActionProposal) && priorClass == session.ContinuationLeaseClassLocalWorkspace {
		return continuationClassScopeDecision{FailedDimension: "external_effect", Reason: "local_workspace_lease_cannot_adopt_external_effect"}
	}
	proposedActions := session.NormalizeActionProposal(proposed.ActionProposal).AllowedActions
	if len(proposedActions) == 0 {
		return continuationClassScopeDecision{FailedDimension: "allowed_actions", Reason: "proposed_allowed_actions_required"}
	}
	for _, action := range proposedActions {
		access := session.CheckContinuationLeaseAction(prior.ContinuationLease, action, now)
		if !access.Allowed {
			return continuationClassScopeDecision{FailedDimension: "allowed_actions", Reason: "proposed_action_not_allowed_by_active_lease:" + strings.TrimSpace(action)}
		}
	}
	if !capabilityGrantSpecsCoveredByActiveLease(continuationRequiredCapabilityGrantSpecs(prior), continuationRequiredCapabilityGrantSpecs(proposed)) {
		return continuationClassScopeDecision{FailedDimension: "capability_grants", Reason: "proposed_required_capability_grants_not_covered_by_active_lease"}
	}
	return continuationClassScopeDecision{Allowed: true, Reason: "proposed_authority_fits_active_lease_class_scope"}
}

func continuationStateWithActiveClassScopedLease(prior session.ContinuationState, proposed session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	prior = session.NormalizeContinuationState(prior)
	proposed = session.NormalizeContinuationState(proposed)
	adopted := proposed
	adopted.Status = session.ContinuationStatusApproved
	adopted.RemainingTurns = prior.RemainingTurns
	adopted.ApprovedBy = prior.ApprovedBy
	adopted.ActionProposal.Status = session.ProposalStatusApproved
	adopted.ActionProposal.UpdatedAt = now.UTC()
	adopted.ContinuationLease = prior.ContinuationLease
	adopted.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	adopted.ContinuationLease.RemainingTurns = prior.ContinuationLease.RemainingTurns
	adopted.ContinuationLease.ApprovedBy = prior.ContinuationLease.ApprovedBy
	adopted.ContinuationLease.ApprovedAt = prior.ContinuationLease.ApprovedAt
	adopted.ContinuationLease.UpdatedAt = now.UTC()
	if adopted.ApprovedBy <= 0 {
		adopted.ApprovedBy = adopted.ContinuationLease.ApprovedBy
	}
	adopted.UpdatedAt = now.UTC()
	return session.NormalizeContinuationState(adopted)
}

func capabilityGrantSpecsCoveredByActiveLease(covered []session.CapabilityGrantSpec, proposed []session.CapabilityGrantSpec) bool {
	covered = session.NormalizeCapabilityGrantSpecs(covered)
	proposed = session.NormalizeCapabilityGrantSpecs(proposed)
	if len(proposed) == 0 {
		return true
	}
	if len(covered) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(covered))
	for _, spec := range covered {
		set[capabilityGrantSpecScopeKey(spec)] = struct{}{}
	}
	for _, spec := range proposed {
		if _, ok := set[capabilityGrantSpecScopeKey(spec)]; !ok {
			return false
		}
	}
	return true
}

func capabilityGrantSpecScopeKey(spec session.CapabilityGrantSpec) string {
	spec = session.NormalizeCapabilityGrantSpec(spec)
	return strings.Join([]string{
		strings.TrimSpace(spec.RequestID),
		strings.TrimSpace(spec.GrantID),
		strings.TrimSpace(string(spec.Kind)),
		strings.TrimSpace(spec.TargetResource),
		strings.TrimSpace(spec.GrantedTo),
		strings.Join(session.NormalizeCapabilityActions(spec.AllowedActions), ","),
	}, "|")
}
