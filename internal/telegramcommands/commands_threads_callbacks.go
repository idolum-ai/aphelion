//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func recordTelegramThreadCallbackMessage(router commandRouter, chatID int64, threadID int64, messageID int64, surface string) error {
	if threadID <= 0 || messageID <= 0 {
		return nil
	}
	recorder, ok := router.(commandThreadCallbackRecorder)
	if !ok {
		return nil
	}
	return recorder.RecordTelegramThreadCallbackMessage(chatID, threadID, messageID, surface)
}
func encodeTelegramThreadPromoteCallback(threadID int64) string {
	if threadID <= 0 {
		return ""
	}
	return telegramThreadPromoteCallbackPrefix + strconv.FormatInt(threadID, 10)
}
func decodeTelegramThreadPromoteCallback(data string) (int64, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, telegramThreadPromoteCallbackPrefix) {
		return 0, false
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, telegramThreadPromoteCallbackPrefix)), 10, 64)
	return threadID, err == nil && threadID > 0
}
func encodeTelegramThreadPromotionReadyCallback(handoffID string) string {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return ""
	}
	return telegramThreadPromotionReadyPrefix + handoffID
}
func encodeTelegramThreadPromotionCancelCallback(handoffID string) string {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return ""
	}
	return telegramThreadPromotionCancelPrefix + handoffID
}
func encodeTelegramThreadPromotionRefreshCallback(handoffID string) string {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return ""
	}
	return telegramThreadPromotionRefreshPrefix + handoffID
}
func decodeTelegramThreadPromotionActionCallback(data string) (string, string, bool) {
	trimmed := strings.TrimSpace(data)
	for _, candidate := range []struct{ prefix, action string }{
		{telegramThreadPromotionReadyPrefix, "ready"},
		{telegramThreadPromotionCancelPrefix, "cancel"},
		{telegramThreadPromotionRefreshPrefix, "refresh"},
	} {
		if strings.HasPrefix(trimmed, candidate.prefix) {
			handoffID := strings.TrimSpace(strings.TrimPrefix(trimmed, candidate.prefix))
			return candidate.action, handoffID, handoffID != ""
		}
	}
	return "", "", false
}
func encodeTelegramThreadAbsorbCallback(threadID int64) string {
	if threadID <= 0 {
		return ""
	}
	return telegramThreadCallbackPrefix + strconv.FormatInt(threadID, 10)
}
func decodeTelegramThreadAbsorbCallback(data string) (int64, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, telegramThreadCallbackPrefix) {
		return 0, false
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, telegramThreadCallbackPrefix)), 10, 64)
	return threadID, err == nil && threadID > 0
}
func decodeTelegramThreadSummaryCallback(data string) bool {
	return strings.TrimSpace(data) == telegramThreadSummaryCallbackData
}
func handleTelegramThreadSummaryCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread controls are unavailable.")
	}
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	messageID := callbackMessageID(cb)
	if chatID == 0 || senderID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	text, err := threadRouter.QueueTelegramThreadSummary(ctx, core.InboundMessage{
		ChatID:          chatID,
		SenderID:        senderID,
		MessageID:       messageID,
		IngressSurface:  telegramThreadSummaryIngressSurface,
		IngressUpdateID: cb.UpdateID,
		Text:            "/threads summarize",
	})
	if err != nil {
		if isTelegramThreadUserError(err) {
			text = err.Error()
		} else {
			return true, err
		}
	}
	if strings.TrimSpace(text) == "" {
		text = "Summary queued."
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	return true, nil
}
func handleTelegramThreadPromoteCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, threadID int64) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread controls are unavailable.")
	}
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	messageID := callbackMessageID(cb)
	if chatID == 0 || senderID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Promote is admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Drafting promotion."); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}
	if err := recordTelegramThreadCallbackMessage(router, chatID, threadID, messageID, "thread_promote"); err != nil {
		return true, err
	}
	text, err := threadRouter.PromoteTelegramThread(ctx, chatID, senderID, threadID)
	if err != nil {
		if isTelegramThreadUserError(err) {
			text = err.Error()
		} else {
			return true, err
		}
	}
	if handoffID := telegramThreadPromotionHandoffIDFromText(text); handoffID != "" {
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", telegramThreadPromotionDraftRows(handoffID)); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		return true, err
	}
	return true, nil
}
func handleTelegramThreadPromotionActionCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string, handoffID string) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread promotion controls are unavailable.")
	}
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	messageID := callbackMessageID(cb)
	if chatID == 0 || senderID == 0 || messageID == 0 || strings.TrimSpace(handoffID) == "" {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Promote is admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	ack := "Updating promotion."
	surface := "thread_promotion_" + action
	switch action {
	case "ready":
		ack = "Marking promotion ready."
	case "cancel":
		ack = "Cancelling promotion."
	case "refresh":
		ack = "Refreshing promotion package."
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ack); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	if threadID := telegramThreadPromotionThreadIDFromHandoffID(handoffID); threadID > 0 {
		if err := recordTelegramThreadCallbackMessage(router, chatID, threadID, messageID, surface); err != nil {
			return true, err
		}
	}
	var text string
	var err error
	switch action {
	case "ready":
		text, err = threadRouter.PrepareTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
	case "cancel":
		text, err = threadRouter.CancelTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
	case "refresh":
		text, err = threadRouter.SupersedeTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
	default:
		return true, nil
	}
	if err != nil {
		if isTelegramThreadUserError(err) {
			text = err.Error()
		} else {
			return true, err
		}
	}
	if action == "refresh" {
		if refreshedID := telegramThreadPromotionLastHandoffIDFromText(text); refreshedID != "" {
			if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", telegramThreadPromotionDraftRows(refreshedID)); err != nil {
				return true, err
			}
			return true, nil
		}
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		return true, err
	}
	return true, nil
}
func handleTelegramThreadCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, threadID int64) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread controls are unavailable.")
	}
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	messageID := callbackMessageID(cb)
	if chatID == 0 || senderID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Absorbing."); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}
	text, err := threadRouter.AbsorbTelegramThread(ctx, chatID, senderID, threadID)
	if err != nil {
		if isTelegramThreadUserError(err) {
			text = err.Error()
		} else {
			return true, err
		}
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		return true, err
	}
	return true, nil
}
func telegramThreadPromotionDraftRows(handoffID string) [][]telegram.InlineButton {
	handoffID = strings.TrimSpace(handoffID)
	if handoffID == "" {
		return nil
	}
	return [][]telegram.InlineButton{
		{
			{Text: "Ready", CallbackData: encodeTelegramThreadPromotionReadyCallback(handoffID)},
			{Text: "Refresh", CallbackData: encodeTelegramThreadPromotionRefreshCallback(handoffID)},
			{Text: "Cancel", CallbackData: encodeTelegramThreadPromotionCancelCallback(handoffID)},
		},
	}
}
func telegramThreadPromotionHandoffIDFromText(text string) string {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Handoff:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Handoff:"))
		}
	}
	return ""
}
func telegramThreadPromotionLastHandoffIDFromText(text string) string {
	last := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Handoff:") {
			last = strings.TrimSpace(strings.TrimPrefix(line, "Handoff:"))
		}
	}
	return last
}
func telegramThreadPromotionThreadIDFromHandoffID(handoffID string) int64 {
	parts := strings.Split(strings.TrimSpace(handoffID), ":")
	if len(parts) < 4 || parts[0] != "thread-promotion" {
		return 0
	}
	threadID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || threadID <= 0 {
		return 0
	}
	return threadID
}
func isTelegramThreadUserError(err error) bool {
	var userErr telegramThreadUserError
	return errors.As(err, &userErr)
}
