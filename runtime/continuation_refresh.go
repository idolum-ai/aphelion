//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func (r *Runtime) RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error) {
	if r == nil || r.store == nil || r.outbound == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime continuation refresh dependencies are unavailable")
	}
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	if !ok {
		return session.ContinuationState{}, false, fmt.Errorf("runtime outbound does not support inline continuation prompts")
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
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
	payload["refreshed_from"] = "expired_callback"
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		payload["refresh_reason"] = trimmed
	}
	payload["prior_proposal_id"] = strings.TrimSpace(prior.ActionProposal.ID)
	payload["prior_lease_id"] = strings.TrimSpace(prior.ContinuationLease.ID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)

	msg := core.InboundMessage{ChatID: chatID, Origin: core.InboundOriginTurnAuthorization, Text: "continuation proposal refresh"}
	if _, err := sender.SendInlineKeyboard(ctx, chatID, r.renderContinuationPrompt(ctx, key, msg, state), continuationApprovalButtonRows(state), nil); err != nil {
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
	if strings.TrimSpace(state.PersonaIntent.Rationale) == "" {
		state.PersonaIntent.Rationale = "The previous approval prompt expired before it could be used."
	}
	if strings.TrimSpace(state.GovernorIntent.Rationale) == "" {
		state.GovernorIntent.Rationale = "A fresh bounded lease is required before continuation authority can be granted."
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
	if strings.TrimSpace(proposal.WhyNow) == "" {
		proposal.WhyNow = firstNonEmptyContinuation(reason, "The prior approval prompt expired before approval.")
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
	proposal.PlanHash = actionProposalHash(proposal)
	return session.NormalizeActionProposal(proposal)
}
