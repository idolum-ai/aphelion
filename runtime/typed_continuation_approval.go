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
	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, nil, err
	}
	state = session.NormalizeContinuationState(state)
	if !exists || state.Status != session.ContinuationStatusPending || state.RemainingTurns <= 0 {
		return false, nil, nil
	}
	approved, err := r.ApproveContinuation(msg.ChatID, actor.TelegramUserID)
	if err != nil {
		if errors.Is(err, core.ErrContinuationExpired) {
			if _, _, refreshErr := r.RefreshContinuationProposal(ctx, msg.ChatID, "expired typed approval"); refreshErr != nil {
				return true, nil, refreshErr
			}
			return true, &core.TurnResult{Text: "The prior approval expired; I sent a fresh approval prompt."}, nil
		}
		return true, nil, err
	}
	if approved.Status == session.ContinuationStatusApproved && approved.RemainingTurns > 0 {
		if err := r.TriggerContinuation(ctx, msg.ChatID); err != nil {
			return true, nil, err
		}
	}
	return true, &core.TurnResult{Text: "Approved continuation."}, nil
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
