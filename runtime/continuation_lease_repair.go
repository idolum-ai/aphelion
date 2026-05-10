//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const leaseDenialRepairKind = "lease_action_denied_repair"

type leaseDenialRepairMarker struct {
	Kind            string `json:"kind,omitempty"`
	CauseHash       string `json:"cause_hash,omitempty"`
	PriorLeaseID    string `json:"prior_lease_id,omitempty"`
	PriorProposalID string `json:"prior_proposal_id,omitempty"`
	Action          string `json:"action,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

func (r *Runtime) offerLeaseActionDeniedRepair(ctx context.Context, key session.SessionKey, chatID int64, prior session.ContinuationState, decision session.ContinuationLeaseAccessDecision, now time.Time) {
	if r == nil || r.store == nil || r.outbound == nil || chatID == 0 {
		return
	}
	if _, ok := r.continuationApprovalPromptSender(); !ok {
		return
	}
	reason := strings.TrimSpace(decision.Reason)
	if reason != "action_not_allowed" && reason != "lease_class_requires_explicit_action" {
		return
	}
	action := strings.TrimSpace(decision.Action)
	if action == "" || continuationWorkModeForbiddenByLease(prior, WorkMode(action)) {
		return
	}
	requestedRank := workModeRank(WorkMode(action))
	if requestedRank > workModeRank(WorkModeReadOnly) && continuationAllowedWorkModeRank(prior) < requestedRank {
		return
	}
	causeHash := leaseDenialRepairCauseHash(prior, decision)
	if r.leaseDenialRepairAlreadyOffered(key, causeHash) {
		return
	}
	repaired := continuationStateWithLeaseDenialRepair(prior, decision, now)
	if err := r.store.UpdateContinuationState(key, repaired); err != nil {
		log.Printf("WARN lease denial repair state update failed chat_id=%d err=%v", chatID, err)
		return
	}
	marker := leaseDenialRepairMarkerFor(prior, decision, causeHash)
	payload := leaseDenialRepairPayload(repaired, marker)
	r.recordExecutionEvent(key, core.ExecutionEventRecoveryIssued, "continuation", "lease_denial_repair_offered", payload, now)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
	msg := core.InboundMessage{ChatID: chatID, Origin: core.InboundOriginTurnAuthorization, Text: "lease action denied repair"}
	text := r.renderContinuationPrompt(ctx, key, msg, repaired)
	if err := r.sendContinuationApprovalPrompt(ctx, key, msg, repaired, text); err != nil {
		log.Printf("WARN send lease denial repair prompt failed chat_id=%d err=%v", chatID, err)
	}
}

func (r *Runtime) leaseDenialRepairAlreadyOffered(key session.SessionKey, causeHash string) bool {
	if r == nil || r.store == nil || strings.TrimSpace(causeHash) == "" {
		return true
	}
	events, err := r.store.LatestExecutionEventsBySession(key, 100)
	if err != nil {
		return true
	}
	for _, event := range events {
		if event.EventType != core.ExecutionEventRecoveryIssued {
			continue
		}
		marker, ok := leaseDenialRepairMarkerFromPayload(event.PayloadJSON)
		if ok && marker.Kind == leaseDenialRepairKind && marker.CauseHash == strings.TrimSpace(causeHash) {
			return true
		}
	}
	return false
}

func leaseDenialRepairCauseHash(state session.ContinuationState, decision session.ContinuationLeaseAccessDecision) string {
	state = session.NormalizeContinuationState(state)
	return actionProposalHash(session.ActionProposal{
		ID:            strings.TrimSpace(state.ContinuationLease.ID),
		Summary:       strings.TrimSpace(decision.Action),
		WhyNow:        strings.TrimSpace(decision.Reason),
		BoundedEffect: strings.Join(append(append([]string{}, state.ContinuationLease.AllowedActions...), state.ContinuationLease.ForbiddenActions...), "|"),
		PlanHash:      strings.TrimSpace(state.ActionProposal.PlanHash),
	})
}

func leaseDenialRepairMarkerFor(state session.ContinuationState, decision session.ContinuationLeaseAccessDecision, causeHash string) leaseDenialRepairMarker {
	state = session.NormalizeContinuationState(state)
	return leaseDenialRepairMarker{
		Kind:            leaseDenialRepairKind,
		CauseHash:       strings.TrimSpace(causeHash),
		PriorLeaseID:    strings.TrimSpace(state.ContinuationLease.ID),
		PriorProposalID: strings.TrimSpace(state.ActionProposal.ID),
		Action:          strings.TrimSpace(decision.Action),
		Reason:          strings.TrimSpace(decision.Reason),
	}
}

func leaseDenialRepairPayload(state session.ContinuationState, marker leaseDenialRepairMarker) map[string]any {
	payload := continuationExecutionPayload(state)
	payload["refreshed_from"] = "lease_action_denied"
	payload["lease_denial_repair"] = marker
	payload["lease_action"] = marker.Action
	payload["lease_access_reason"] = marker.Reason
	return payload
}

func leaseDenialRepairMarkerFromPayload(payloadJSON string) (leaseDenialRepairMarker, bool) {
	var payload struct {
		LeaseDenialRepair leaseDenialRepairMarker `json:"lease_denial_repair"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(payloadJSON)), &payload); err != nil {
		return leaseDenialRepairMarker{}, false
	}
	marker := payload.LeaseDenialRepair
	marker.Kind = strings.TrimSpace(marker.Kind)
	marker.CauseHash = strings.TrimSpace(marker.CauseHash)
	if marker.Kind == "" || marker.CauseHash == "" {
		return leaseDenialRepairMarker{}, false
	}
	return marker, true
}

func continuationStateWithLeaseDenialRepair(prior session.ContinuationState, decision session.ContinuationLeaseAccessDecision, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	prior = session.NormalizeContinuationState(prior)
	decisionID := newContinuationDecisionID()
	turns := prior.ContinuationLease.MaxTurns
	if turns <= 0 {
		turns = prior.RemainingTurns
	}
	if turns <= 0 {
		turns = 1
	}
	state := prior
	state.Status = session.ContinuationStatusPending
	state.DecisionID = decisionID
	state.RemainingTurns = turns
	state.ApprovedBy = 0
	state.UpdatedAt = now
	state.ActionProposal = session.NormalizeActionProposal(prior.ActionProposal)
	state.ActionProposal.ID = "aprop-" + decisionID
	state.ActionProposal.Status = session.ProposalStatusPending
	state.ActionProposal.ExpiresAt = now.Add(continuationLeaseDefaultTTL)
	state.ActionProposal.CreatedAt = now
	state.ActionProposal.UpdatedAt = now
	state.ActionProposal.WhyNow = "The prior approved lease was blocked before execution by a lease/action shape mismatch; approve this corrected lease to retry the same bounded work."
	state.ActionProposal.AllowedActions = append(state.ActionProposal.AllowedActions, strings.TrimSpace(decision.Action))
	state.ActionProposal = applyContinuationLeaseClassBoundaries(state.ActionProposal)
	state.ActionProposal.PlanHash = actionProposalHash(state.ActionProposal)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, turns, now)
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusPending
		state.ApprovalBundle.ApprovedBy = 0
		state.ApprovalBundle.ApprovedAt = time.Time{}
		state.ApprovalBundle.ConsumedAt = time.Time{}
		state.ApprovalBundle.RevokedAt = time.Time{}
		state.ApprovalBundle.UpdatedAt = now
		if state.ApprovalBundle.CurrentPhaseID == "" {
			state.ApprovalBundle.CurrentPhaseID = firstContinuationBundlePhaseID(state.ApprovalBundle.Phases)
		}
		for i := range state.ApprovalBundle.Phases {
			state.ApprovalBundle.Phases[i].Status = session.ContinuationLeaseStatusPending
		}
	}
	state.PersonaIntent.Decision = session.ContinuationIntentDecisionContinue
	state.PersonaIntent.Rationale = "The previous lease shape was rejected before execution; a corrected lease is ready for approval."
	state.PersonaIntent.UpdatedAt = now
	state.GovernorIntent.Decision = session.ContinuationIntentDecisionContinue
	state.GovernorIntent.Rationale = state.ActionProposal.WhyNow
	state.GovernorIntent.Ratified = true
	state.GovernorIntent.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}
