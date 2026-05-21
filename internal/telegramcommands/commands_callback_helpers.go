//go:build linux

package telegramcommands

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegrampresentation"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type commandInlineKeyboardClearer interface {
	EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type commandStreamControlRouter interface {
	MarkStreamControlStopping(streamID string, chatID int64) bool
}

type telegramCallbackErrorRecorder interface {
	RecordTelegramCallbackError(chatID int64, callbackKind string, err error)
}

type telegramCallbackThreadResolver interface {
	TelegramThreadForReplyMessage(chatID int64, replyMessageID int64) (session.TelegramThread, bool, error)
}

func telegramCallbackTargetMessage(router commandRouter, cb telegram.CallbackQuery) (core.InboundMessage, error) {
	msg := core.InboundMessage{}
	if cb.Message != nil {
		msg.MessageID = cb.Message.MessageID
		if cb.Message.Chat != nil {
			msg.ChatID = cb.Message.Chat.ID
		}
	}
	if cb.From != nil {
		msg.SenderID = cb.From.ID
	}
	if msg.ChatID == 0 || msg.MessageID <= 0 {
		return msg, nil
	}
	resolver, ok := router.(telegramCallbackThreadResolver)
	if !ok {
		return msg, nil
	}
	thread, found, err := resolver.TelegramThreadForReplyMessage(msg.ChatID, msg.MessageID)
	if err != nil || !found {
		return msg, err
	}
	msg.TelegramThreadID = thread.ThreadID
	if thread.DisplaySlot > 0 {
		msg.OriginDetail = telegrampresentation.OriginDetailForDisplaySlot(thread.DisplaySlot)
	}
	return msg, nil
}

func recordTelegramCallbackError(router commandRouter, chatID int64, callbackKind string, err error) {
	if err == nil {
		return
	}
	if recorder, ok := router.(telegramCallbackErrorRecorder); ok {
		recorder.RecordTelegramCallbackError(chatID, callbackKind, err)
	}
}

func editCallbackMessageClearingInlineKeyboard(ctx context.Context, sender commandCallbackSender, chatID int64, messageID int64, text string) error {
	if clearer, ok := sender.(commandInlineKeyboardClearer); ok {
		return clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, messageID, text, "")
	}
	return sender.EditMessageText(ctx, chatID, messageID, text, "")
}
