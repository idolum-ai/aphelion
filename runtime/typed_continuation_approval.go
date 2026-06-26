//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) maybeHandleTypedContinuationApproval(ctx context.Context, msg core.InboundMessage, actor principal.Principal) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || msg.ChatID == 0 || msg.Origin == core.InboundOriginTurnAuthorization {
		return false, nil, nil
	}
	if !isTypedContinuationApprovalText(msg.Text) {
		return false, nil, nil
	}
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramInboundScopeRef(msg)}
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, nil, err
	}
	state = session.NormalizeContinuationState(state)
	if !exists || state.Status != session.ContinuationStatusPending || state.RemainingTurns <= 0 {
		return false, nil, nil
	}
	approved, err := r.ApproveContinuationForKey(key, actor.TelegramUserID)
	if err != nil {
		if errors.Is(err, core.ErrContinuationExpired) {
			if _, _, refreshErr := r.RefreshContinuationProposalForKey(ctx, key, "expired typed approval"); refreshErr != nil {
				return true, nil, refreshErr
			}
			return true, &core.TurnResult{Text: "The prior approval expired; I sent a fresh approval prompt."}, nil
		}
		return true, nil, err
	}
	if approved.Status == session.ContinuationStatusApproved && approved.RemainingTurns > 0 {
		if err := r.TriggerContinuationForKey(ctx, key); err != nil {
			return true, nil, err
		}
	}
	return true, &core.TurnResult{Text: "Approved continuation."}, nil
}

func (r *Runtime) maybeHandleApprovedContinuationRunIntent(ctx context.Context, msg core.InboundMessage, actor principal.Principal) (bool, *core.TurnResult, error) {
	if r == nil || r.store == nil || msg.ChatID == 0 || msg.Origin == core.InboundOriginTurnAuthorization {
		return false, nil, nil
	}
	if actor.Role != principal.RoleAdmin || !isApprovedContinuationRunText(msg.Text) {
		return false, nil, nil
	}
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramInboundScopeRef(msg)}
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, nil, err
	}
	state = session.NormalizeContinuationState(state)
	if !exists || state.Status != session.ContinuationStatusApproved || state.RemainingTurns <= 0 {
		return false, nil, nil
	}
	result, err := r.triggerContinuationLoopWithResult(ctx, key)
	if err != nil {
		return true, nil, err
	}
	if !result.Ran {
		return true, &core.TurnResult{Text: approvedContinuationRunNoopText(result.State)}, nil
	}
	return true, &core.TurnResult{Text: "Running approved continuation."}, nil
}

func isTypedContinuationApprovalText(text string) bool {
	value := strings.ToLower(strings.TrimSpace(text))
	value = strings.Trim(value, ".! \t\r\n")
	switch value {
	case "approve", "approved", "yes approve", "yes approved", "approved yes", "ok approve", "ok approved":
		return true
	default:
		return false
	}
}

func isApprovedContinuationRunText(text string) bool {
	value := normalizeContinuationControlText(text)
	if value == "" || approvedContinuationRunTextNegated(value) {
		return false
	}
	switch value {
	case "continue", "please continue", "continue please",
		"run", "run it", "please run", "run please",
		"resume", "resume it", "please resume", "resume please",
		"proceed", "please proceed", "proceed please",
		"go ahead", "yes continue", "ok continue", "yes run", "ok run":
		return true
	}
	for _, prefix := range []string{
		"continue approved",
		"continue the approved",
		"continue with approved",
		"run approved",
		"run the approved",
		"run with approved",
		"resume approved",
		"resume the approved",
		"resume with approved",
	} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func normalizeContinuationControlText(text string) string {
	value := strings.ToLower(strings.TrimSpace(text))
	replacer := strings.NewReplacer(
		"\t", " ",
		"\n", " ",
		"\r", " ",
		".", " ",
		",", " ",
		"!", " ",
		"?", " ",
		";", " ",
		":", " ",
	)
	return strings.Join(strings.Fields(replacer.Replace(value)), " ")
}

func approvedContinuationRunTextNegated(value string) bool {
	for _, phrase := range []string{
		"do not continue",
		"don't continue",
		"dont continue",
		"do not run",
		"don't run",
		"dont run",
		"do not resume",
		"don't resume",
		"dont resume",
		"do not proceed",
		"don't proceed",
		"dont proceed",
		"not now",
		"no continue",
		"no run",
		"pause",
		"stop",
	} {
		if value == phrase || strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func approvedContinuationRunNoopText(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if state.ContinuationLease.Status == session.ContinuationLeaseStatusExpired ||
		state.ActionProposal.Status == session.ProposalStatusExpired {
		return "The approved continuation expired before it could run."
	}
	if state.Status != session.ContinuationStatusApproved {
		return "No approved continuation is currently runnable."
	}
	if state.RemainingTurns <= 0 || state.ContinuationLease.RemainingTurns <= 0 ||
		state.ContinuationLease.Status == session.ContinuationLeaseStatusConsumed {
		return "The approved continuation has no remaining turns."
	}
	return "The approved continuation is not currently runnable."
}
