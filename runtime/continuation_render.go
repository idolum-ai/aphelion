//go:build linux

package runtime

import (
	"context"
	"fmt"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func (r *Runtime) sendContinuationApprovalPrompt(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, text string) error {
	sender, ok := r.continuationApprovalPromptSender()
	if !ok {
		return nil
	}
	_, err := sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		text,
		continuationApprovalButtonRows(state),
		nil,
	)
	if err != nil {
		return fmt.Errorf("send continuation approval: %w", err)
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
