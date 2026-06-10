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

func (r *Runtime) RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error) {
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	return r.RefreshContinuationProposalForKey(ctx, key, reason)
}

func (r *Runtime) RefreshContinuationProposalForKey(ctx context.Context, key session.SessionKey, reason string) (session.ContinuationState, bool, error) {
	return r.refreshContinuationProposal(ctx, key, reason, "expired_callback", true)
}

func (r *Runtime) refreshContinuationProposal(ctx context.Context, key session.SessionKey, reason string, refreshedFrom string, allowAutoApproval bool) (session.ContinuationState, bool, error) {
	if r == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime continuation refresh dependencies are unavailable")
	}
	unlock := r.lockSession(key)
	defer unlock()
	return r.refreshContinuationProposalLocked(ctx, key, reason, refreshedFrom, allowAutoApproval)
}

func (r *Runtime) refreshContinuationProposalLocked(ctx context.Context, key session.SessionKey, reason string, refreshedFrom string, allowAutoApproval bool) (session.ContinuationState, bool, error) {
	if r == nil || r.store == nil || r.outbound == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime continuation refresh dependencies are unavailable")
	}
	if _, ok := r.continuationApprovalPromptSender(); !ok {
		return session.ContinuationState{}, false, fmt.Errorf("runtime outbound does not support inline continuation prompts")
	}
	prior, err := r.store.ContinuationState(key)
	if err != nil {
		return session.ContinuationState{}, false, err
	}
	prior = session.NormalizeContinuationState(prior)
	now := time.Now().UTC()
	if continuationStateHasFreshPendingLease(prior, now) {
		if !allowAutoApproval {
			prior = manualOnlyContinuationRefreshState(prior, now)
			barrier, err := r.clearApprovalWindowForManualRetryBarrier(key, prior, refreshedFrom, now)
			if err != nil {
				return session.ContinuationState{}, false, fmt.Errorf("clear approval window for manual retry: %w", err)
			}
			if err := r.store.UpdateContinuationState(key, prior); err != nil {
				return session.ContinuationState{}, false, fmt.Errorf("persist manual-only continuation proposal: %w", err)
			}
			if barrier.Revoked() {
				msg := continuationPromptInboundForKey(key, "continuation manual retry", core.InboundOriginTurnAuthorization, "")
				text := manualRetryBarrierPromptText(r.renderContinuationPrompt(ctx, key, msg, prior), barrier)
				if err := r.sendContinuationApprovalPrompt(ctx, key, msg, prior, text); err != nil {
					return prior, false, fmt.Errorf("send refreshed continuation approval: %w", err)
				}
				return prior, true, nil
			}
		}
		return prior, false, nil
	}

	state := refreshedContinuationState(prior, reason, refreshedFrom, now)
	barrier := manualRetryBarrierResult{}
	if !allowAutoApproval {
		state = manualOnlyContinuationRefreshState(state, now)
		var err error
		barrier, err = r.clearApprovalWindowForManualRetryBarrier(key, state, refreshedFrom, now)
		if err != nil {
			return session.ContinuationState{}, false, fmt.Errorf("clear approval window for manual retry: %w", err)
		}
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return session.ContinuationState{}, false, fmt.Errorf("persist refreshed continuation proposal: %w", err)
	}
	payload := continuationExecutionPayload(state)
	payload["refreshed_from"] = firstNonEmptyContinuation(refreshedFrom, "continuation_refresh")
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		payload["refresh_reason"] = trimmed
	}
	payload["prior_proposal_id"] = strings.TrimSpace(prior.ActionProposal.ID)
	payload["prior_lease_id"] = strings.TrimSpace(prior.ContinuationLease.ID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)

	msg := continuationPromptInboundForKey(key, "continuation proposal refresh", core.InboundOriginTurnAuthorization, "")
	text := r.renderContinuationPrompt(ctx, key, msg, state)
	if !allowAutoApproval {
		text = manualRetryBarrierPromptText(text, barrier)
	}
	if allowAutoApproval {
		if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, text, "continuation_refresh"); err != nil {
			return state, false, fmt.Errorf("send refreshed continuation approval: %w", err)
		}
	} else if err := r.sendContinuationApprovalPrompt(ctx, key, msg, state, text); err != nil {
		return state, false, fmt.Errorf("send refreshed continuation approval: %w", err)
	}
	return state, true, nil
}

func manualRetryBarrierPromptText(text string, barrier manualRetryBarrierResult) string {
	text = strings.TrimSpace(text)
	if !barrier.Revoked() {
		return text
	}
	notice := "Approval window paused for this retry; approve this one step manually."
	if text == "" {
		return notice
	}
	return text + "\n\n" + notice
}

func manualOnlyContinuationRefreshState(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	manualOnly := false
	state.ActionProposal.AutoApproveEligible = &manualOnly
	state.ActionProposal.UpdatedAt = now
	state.ActionProposal.PlanHash = actionProposalHash(state.ActionProposal)
	state.ContinuationLease.PlanHash = state.ActionProposal.PlanHash
	state.ContinuationLease.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationStateHasFreshPendingLease(state session.ContinuationState, now time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending || state.RemainingTurns <= 0 {
		return false
	}
	if state.ActionProposal.Active() && !state.ActionProposal.ExpiresAt.IsZero() && !state.ActionProposal.ExpiresAt.After(now) {
		return false
	}
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	if strings.TrimSpace(lease.ID) == "" && strings.TrimSpace(lease.ProposalID) == "" {
		return true
	}
	return lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now)
}

func refreshedContinuationState(prior session.ContinuationState, reason string, refreshedFrom string, now time.Time) session.ContinuationState {
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
	visibleReason := continuationVisibleRefreshReason(reason, refreshedFrom, prior)
	state.Status = session.ContinuationStatusPending
	state.DecisionID = decisionID
	state.RemainingTurns = turns
	state.ApprovedBy = 0
	state.HandshakeBlockedReason = ""
	state.UpdatedAt = now
	state.ActionProposal = refreshedContinuationActionProposal(prior, decisionID, visibleReason, now)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, turns, now)
	state.PersonaIntent.UpdatedAt = now
	state.GovernorIntent.UpdatedAt = now
	state.PersonaIntent.Rationale = visibleReason
	state.GovernorIntent.Rationale = visibleReason
	state.PersonaIntent.Decision = session.ContinuationIntentDecisionContinue
	state.GovernorIntent.Decision = session.ContinuationIntentDecisionContinue
	state.GovernorIntent.Ratified = true
	return session.NormalizeContinuationState(state)
}

func continuationVisibleRefreshReason(reason string, refreshedFrom string, prior session.ContinuationState) string {
	switch strings.TrimSpace(refreshedFrom) {
	case "work_executor_failure":
		return "I need your approval before retrying this step."
	case "expired_callback":
		return "This step needs a new approval before I continue."
	case "operator_requested_next_lease":
		return "This next step needs your approval before I continue."
	}
	lower := strings.ToLower(strings.TrimSpace(refreshedFrom + " " + reason))
	switch {
	case strings.Contains(lower, "work") && strings.Contains(lower, "fail"):
		return "I need your approval before retrying this step."
	case strings.Contains(lower, "restart") || strings.Contains(lower, "deploy") || strings.Contains(lower, "park"):
		return "The service restarted, so I need your approval before continuing."
	case strings.Contains(lower, "expired") || strings.Contains(lower, "stale"):
		return "This step needs a new approval before I continue."
	}
	prior = session.NormalizeContinuationState(prior)
	if strings.TrimSpace(prior.ActionProposal.WhyNow) != "" || strings.TrimSpace(prior.StageSummary) != "" {
		return "This step needs your approval before I continue."
	}
	return "I need your approval before continuing."
}

func refreshedContinuationActionProposal(prior session.ContinuationState, decisionID string, reason string, now time.Time) session.ActionProposal {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	proposal := session.NormalizeActionProposal(prior.ActionProposal)
	if !proposal.Active() {
		proposal = buildContinuationActionProposal(decisionID, continuationConsensus{PersonaIntent: prior.PersonaIntent, GovernorIntent: prior.GovernorIntent}, prior.Objective, prior.StageSummary, now)
	}
	proposal.ID = "aprop-" + strings.TrimSpace(decisionID)
	proposal.Status = session.ProposalStatusPending
	proposal.ExpiresAt = now.Add(continuationLeaseDefaultTTL)
	proposal.CreatedAt = now
	proposal.UpdatedAt = now
	if strings.TrimSpace(proposal.Summary) == "" {
		proposal.Summary = firstNonEmptyContinuation(prior.StageSummary, prior.Objective, "Continue one bounded turn.")
	}
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		proposal.WhyNow = trimmed
	} else if strings.TrimSpace(proposal.WhyNow) == "" {
		proposal.WhyNow = "The prior approval prompt expired before approval."
	}
	if strings.TrimSpace(proposal.BoundedEffect) == "" {
		proposal.BoundedEffect = "Resume one bounded continuation turn and report the result."
	}
	if len(proposal.AllowedActions) == 0 {
		proposal.AllowedActions = []string{"continue_one_turn", "use_existing_authority_only", "report_evidence"}
	}
	if len(proposal.ForbiddenActions) == 0 {
		proposal.ForbiddenActions = []string{"expand_authority_without_new_approval", "external_effect_outside_bounded_effect", "ignore_stop_or_revocation"}
	}
	if len(proposal.ValidationPlan) == 0 {
		proposal.ValidationPlan = []string{"consume at most the approved continuation turn", "report what changed and what evidence supports it"}
	}
	proposal = applyContinuationLeaseClassBoundaries(proposal)
	proposal.PlanHash = actionProposalHash(proposal)
	return session.NormalizeActionProposal(proposal)
}
