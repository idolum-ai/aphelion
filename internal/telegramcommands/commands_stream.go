//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

const staleStreamCallbackText = "This stream is no longer active."

func handleStreamControlCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, streamID string, action core.StreamControlAction) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	senderID := int64(0)
	messageText := ""
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		messageText = strings.TrimSpace(cb.Message.Text)
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
		}
	}
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if action != core.StreamControlActionStop || chatID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStreamCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}

	streamRouter, ok := router.(commandStreamControlRouter)
	if !ok || !streamRouter.MarkStreamControlStopping(streamID, chatID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStreamCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		if messageID != 0 && messageText != "" {
			if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, messageText); err != nil {
				return true, err
			}
		}
		return true, nil
	}

	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Stopping stream."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	targetMsg.ChatID = chatID
	targetMsg.MessageID = messageID
	if targetMsg.SenderID == 0 {
		targetMsg.SenderID = senderID
	}
	stopped := stopForCommand(router, targetMsg)
	if messageID == 0 {
		return true, nil
	}
	text := messageText
	if text == "" {
		text = face.RenderTelegramStop(stopped)
	} else if !strings.HasSuffix(text, "Stopping.") && !strings.HasSuffix(text, "Stopped.") {
		text += "\n\nStopping."
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		return true, err
	}
	return true, nil
}
