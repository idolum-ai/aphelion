//go:build linux

package main

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcontrol"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

func (c telegramCommandControl) threadController() telegramcontrol.ThreadController {
	controller := telegramcontrol.ThreadController{
		Store:          c.store,
		Rebind:         c.rebindTelegramIngressForMessage,
		RouteAccepted:  c.RouteAccepted,
		StopForMessage: c.StopForMessage,
	}
	if c.rt != nil {
		controller.Promote = c.rt.PromoteTelegramThread
		controller.PreparePromotion = c.rt.PrepareTelegramThreadPromotion
		controller.CancelPromotion = c.rt.CancelTelegramThreadPromotion
		controller.SupersedePromotion = c.rt.SupersedeTelegramThreadPromotion
		controller.Absorb = c.rt.AbsorbTelegramThread
		controller.IsAbsorbUserError = runtime.IsTelegramThreadUserError
	}
	return controller
}

func (c telegramCommandControl) CreateTelegramThread(ctx context.Context, msg core.InboundMessage) (session.TelegramThread, error) {
	return c.threadController().CreateTelegramThread(ctx, msg)
}

func (c telegramCommandControl) RecordTelegramThreadGuideMessage(chatID int64, threadID int64, messageID int64) error {
	return c.threadController().RecordTelegramThreadGuideMessage(chatID, threadID, messageID)
}

func (c telegramCommandControl) RecordTelegramThreadCallbackMessage(chatID int64, threadID int64, messageID int64, surface string) error {
	return c.threadController().RecordTelegramThreadCallbackMessage(chatID, threadID, messageID, surface)
}

func (c telegramCommandControl) ClearTelegramThreadCallbackMessage(chatID int64, messageID int64, surface string) error {
	return c.threadController().ClearTelegramThreadCallbackMessage(chatID, messageID, surface)
}

func (c telegramCommandControl) RecordTelegramThreadReminderMessage(chatID int64, threadID int64, messageID int64, summary string, summaryKind string, sourceLastActivityAt time.Time, createdBySenderID int64) error {
	return c.threadController().RecordTelegramThreadReminderMessage(chatID, threadID, messageID, summary, summaryKind, sourceLastActivityAt, createdBySenderID)
}

func (c telegramCommandControl) IgnoreTelegramThreadReminder(ctx context.Context, chatID int64, senderID int64, threadID int64, messageID int64) (string, error) {
	return c.threadController().IgnoreTelegramThreadReminder(ctx, chatID, senderID, threadID, messageID)
}

func (c telegramCommandControl) AbsorbTelegramThreadReminder(ctx context.Context, chatID int64, senderID int64, threadID int64, messageID int64) (string, error) {
	return c.threadController().AbsorbTelegramThreadReminder(ctx, chatID, senderID, threadID, messageID)
}

func (c telegramCommandControl) StartTelegramThreadTarget(ctx context.Context, msg core.InboundMessage, text string) (core.InboundMessage, session.TelegramThread, error) {
	return c.threadController().StartTelegramThreadTarget(ctx, msg, text)
}

func (c telegramCommandControl) TargetTelegramThreadMessage(ctx context.Context, msg core.InboundMessage, threadID int64, text string) (core.InboundMessage, session.TelegramThread, error) {
	return c.threadController().TargetTelegramThreadMessage(ctx, msg, threadID, text)
}

func (c telegramCommandControl) TelegramThreadForReplyMessage(chatID int64, replyMessageID int64) (session.TelegramThread, bool, error) {
	return c.threadController().TelegramThreadForReplyMessage(chatID, replyMessageID)
}

func (c telegramCommandControl) MarkTelegramThreadReminderResumed(chatID int64, replyMessageID int64) error {
	return c.threadController().MarkTelegramThreadReminderResumed(chatID, replyMessageID)
}

func (c telegramCommandControl) TelegramThread(chatID int64, threadID int64) (session.TelegramThread, bool, error) {
	return c.threadController().TelegramThread(chatID, threadID)
}

func (c telegramCommandControl) TelegramThreads(chatID int64) ([]session.TelegramThread, error) {
	return c.threadController().TelegramThreads(chatID)
}

func (c telegramCommandControl) TelegramThreadReminders(chatID int64, status session.TelegramThreadReminderStatus, limit int) ([]session.TelegramThreadReminder, error) {
	return c.threadController().TelegramThreadReminders(chatID, status, limit)
}

func (c telegramCommandControl) QueueTelegramThreadSummary(ctx context.Context, msg core.InboundMessage) (string, error) {
	return c.threadController().QueueTelegramThreadSummary(ctx, msg)
}

func (c telegramCommandControl) PromoteTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (session.TelegramThreadPromotionResult, error) {
	return c.threadController().PromoteTelegramThread(ctx, chatID, senderID, threadID)
}

func (c telegramCommandControl) PrepareTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	return c.threadController().PrepareTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
}

func (c telegramCommandControl) CancelTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	return c.threadController().CancelTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
}

func (c telegramCommandControl) SupersedeTelegramThreadPromotion(ctx context.Context, chatID int64, senderID int64, handoffID string) (session.TelegramThreadPromotionResult, error) {
	return c.threadController().SupersedeTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
}

func (c telegramCommandControl) AbsorbTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (string, error) {
	return c.threadController().AbsorbTelegramThread(ctx, chatID, senderID, threadID)
}
