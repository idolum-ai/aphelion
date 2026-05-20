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
		return prior, false, nil
	}

	state := refreshedContinuationState(prior, reason, now)
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
	if allowAutoApproval {
		if err := r.sendMaterializedContinuationApproval(ctx, key, msg, state, text, "continuation_refresh"); err != nil {
			return state, false, fmt.Errorf("send refreshed continuation approval: %w", err)
		}
	} else if err := r.sendContinuationApprovalPrompt(ctx, key, msg, state, text); err != nil {
		return state, false, fmt.Errorf("send refreshed continuation approval: %w", err)
	}
	return state, true, nil
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

func refreshedContinuationState(prior session.ContinuationState, reason string, now time.Time) session.ContinuationState {
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
	state.HandshakeBlockedReason = ""
	state.UpdatedAt = now
	state.ActionProposal = refreshedContinuationActionProposal(prior, decisionID, reason, now)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, turns, now)
	state.PersonaIntent.UpdatedAt = now
	state.GovernorIntent.UpdatedAt = now
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		state.PersonaIntent.Rationale = trimmed
		state.GovernorIntent.Rationale = trimmed
	} else {
		if strings.TrimSpace(state.PersonaIntent.Rationale) == "" {
			state.PersonaIntent.Rationale = "The previous approval prompt expired before it could be used."
		}
		if strings.TrimSpace(state.GovernorIntent.Rationale) == "" {
			state.GovernorIntent.Rationale = "A fresh bounded lease is required before continuation authority can be granted."
		}
	}
	state.PersonaIntent.Decision = session.ContinuationIntentDecisionContinue
	state.GovernorIntent.Decision = session.ContinuationIntentDecisionContinue
	state.GovernorIntent.Ratified = true
	return session.NormalizeContinuationState(state)
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
