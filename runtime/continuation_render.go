//go:build linux

package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func (r *Runtime) sendContinuationApprovalPrompt(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, text string) error {
	if _, blocked, err := r.blockInvalidContinuationAuthorityContract(ctx, key, msg, state, "approval_prompt", time.Now().UTC(), false); blocked || err != nil {
		return err
	}
	sender, ok := r.continuationApprovalPromptSender()
	if !ok {
		return nil
	}
	messageID, err := sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), text),
		continuationApprovalButtonRows(state),
		nil,
	)
	if err != nil {
		return fmt.Errorf("send continuation approval: %w", err)
	}
	if messageID > 0 && r.store != nil {
		state.DecisionMessageID = messageID
		if err := r.store.UpdateContinuationState(key, session.NormalizeContinuationState(state)); err != nil {
			return fmt.Errorf("record continuation approval message: %w", err)
		}
		if msg.TelegramThreadID > 0 {
			if err := r.store.RecordTelegramCallbackMessageThread(msg.ChatID, messageID, msg.TelegramThreadID, "continuation", time.Now().UTC()); err != nil {
				return fmt.Errorf("record continuation callback thread: %w", err)
			}
		}
	}
	return nil
}

func (r *Runtime) continuationApprovalPromptSender() (interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}, bool) {
	if r == nil || r.outbound == nil {
		return nil, false
	}
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	return sender, ok
}
