//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
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

func clearTelegramThreadCallbackMessage(router commandRouter, chatID int64, messageID int64, surface string) error {
	if chatID == 0 || messageID <= 0 {
		return nil
	}
	recorder, ok := router.(commandThreadCallbackRecorder)
	if !ok {
		return nil
	}
	return recorder.ClearTelegramThreadCallbackMessage(chatID, messageID, surface)
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

const telegramThreadPromotionActionCompactPrefix = "tp:"

func encodeTelegramThreadPromotionReadyCallback(handoffID string) string {
	return encodeTelegramThreadPromotionActionCallback("ready", handoffID)
}
func encodeTelegramThreadPromotionCancelCallback(handoffID string) string {
	return encodeTelegramThreadPromotionActionCallback("cancel", handoffID)
}
func encodeTelegramThreadPromotionRefreshCallback(handoffID string) string {
	return encodeTelegramThreadPromotionActionCallback("refresh", handoffID)
}
func encodeTelegramThreadPromotionActionCallback(action string, handoffID string) string {
	action = strings.TrimSpace(action)
	handoffID = strings.TrimSpace(handoffID)
	if action == "" || handoffID == "" {
		return ""
	}
	if prefix := telegramThreadPromotionActionLegacyPrefix(action); prefix != "" {
		data := prefix + handoffID
		if len(data) <= core.TelegramCallbackDataMaxBytes {
			return data
		}
	}
	if code := telegramThreadPromotionActionCode(action); code != "" {
		if threadID, token, ok := telegramThreadPromotionCompactParts(handoffID); ok {
			data := telegramThreadPromotionActionCompactPrefix + code + ":" + strconv.FormatInt(threadID, 10) + ":" + token
			if len(data) <= core.TelegramCallbackDataMaxBytes {
				return data
			}
		}
	}
	return ""
}
func telegramThreadPromotionActionCode(action string) string {
	switch strings.TrimSpace(action) {
	case "ready":
		return "r"
	case "cancel":
		return "c"
	case "refresh":
		return "f"
	default:
		return ""
	}
}
func telegramThreadPromotionActionFromCode(code string) string {
	switch strings.TrimSpace(code) {
	case "r":
		return "ready"
	case "c":
		return "cancel"
	case "f":
		return "refresh"
	default:
		return ""
	}
}
func telegramThreadPromotionActionLegacyPrefix(action string) string {
	switch strings.TrimSpace(action) {
	case "ready":
		return telegramThreadPromotionReadyPrefix
	case "cancel":
		return telegramThreadPromotionCancelPrefix
	case "refresh":
		return telegramThreadPromotionRefreshPrefix
	default:
		return ""
	}
}
func telegramThreadPromotionCompactParts(handoffID string) (int64, string, bool) {
	parts := strings.Split(strings.TrimSpace(handoffID), ":")
	if len(parts) < 4 || parts[0] != "thread-promotion" {
		return 0, "", false
	}
	threadID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || threadID <= 0 {
		return 0, "", false
	}
	token := strings.TrimSpace(parts[3])
	if token == "" {
		return 0, "", false
	}
	return threadID, token, true
}
func decodeTelegramThreadPromotionActionCallback(data string) (string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if strings.HasPrefix(trimmed, telegramThreadPromotionActionCompactPrefix) {
		parts := strings.Split(strings.TrimPrefix(trimmed, telegramThreadPromotionActionCompactPrefix), ":")
		if len(parts) != 3 {
			return "", "", false
		}
		action := telegramThreadPromotionActionFromCode(parts[0])
		threadID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		token := strings.TrimSpace(parts[2])
		if action == "" || err != nil || threadID <= 0 || token == "" {
			return "", "", false
		}
		return action, "thread-promotion::" + strconv.FormatInt(threadID, 10) + ":" + token, true
	}
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
func encodeTelegramThreadDetailCallback(threadID int64) string {
	if threadID <= 0 {
		return ""
	}
	return telegramThreadDetailCallbackPrefix + strconv.FormatInt(threadID, 10)
}
func decodeTelegramThreadDetailCallback(data string) (int64, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, telegramThreadDetailCallbackPrefix) {
		return 0, false
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, telegramThreadDetailCallbackPrefix)), 10, 64)
	return threadID, err == nil && threadID > 0
}
func decodeTelegramThreadBackCallback(data string) bool {
	return strings.TrimSpace(data) == telegramThreadBackCallbackData
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
		Text:            "/threads analyze",
	})
	if err != nil {
		if isTelegramThreadUserError(err) {
			text = err.Error()
		} else {
			return true, err
		}
	}
	if strings.TrimSpace(text) == "" {
		text = "Analysis queued."
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	return true, nil
}
func handleTelegramThreadDetailCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread controls are unavailable.")
	}
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	messageID := callbackMessageID(cb)
	threadID, ok := decodeTelegramThreadDetailCallback(cb.Data)
	if chatID == 0 || senderID == 0 || messageID == 0 || !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	threads, err := threadRouter.TelegramThreads(chatID)
	if err != nil {
		return true, err
	}
	var selected session.TelegramThread
	found := false
	for _, thread := range threads {
		if thread.ThreadID == threadID && thread.Open() {
			selected = thread
			found = true
			break
		}
	}
	if !found {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread is no longer open."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread opened."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	if err := recordTelegramThreadCallbackMessage(router, chatID, threadID, messageID, "thread_detail"); err != nil {
		return true, err
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, renderTelegramThreadDetail(selected), "", telegramThreadDetailRows(selected)); err != nil {
		return true, err
	}
	return true, nil
}
func handleTelegramThreadBackCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
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
	threads, err := threadRouter.TelegramThreads(chatID)
	if err != nil {
		return true, err
	}
	rendered, rows := renderTelegramThreadsPanel(threads, telegramPageViewList, 1)
	if len(rows) == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "No open threads."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Back to threads."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	if err := clearTelegramThreadCallbackMessage(router, chatID, messageID, "threads_list"); err != nil {
		return true, err
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
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
	result, err := threadRouter.PromoteTelegramThread(ctx, chatID, senderID, threadID)
	if err != nil {
		if isTelegramThreadUserError(err) {
			result.Text = err.Error()
		} else {
			return true, err
		}
	}
	if result.HandoffID != "" && result.Status == session.TelegramThreadPromotionStatusDraft {
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, result.Text, "", telegramThreadPromotionDraftRows(result.HandoffID)); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, result.Text); err != nil {
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
	handoffID = telegramThreadPromotionCallbackHandoffID(chatID, handoffID)
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
	var result session.TelegramThreadPromotionResult
	var err error
	switch action {
	case "ready":
		result, err = threadRouter.PrepareTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
	case "cancel":
		result, err = threadRouter.CancelTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
	case "refresh":
		result, err = threadRouter.SupersedeTelegramThreadPromotion(ctx, chatID, senderID, handoffID)
	default:
		return true, nil
	}
	if err != nil {
		if isTelegramThreadUserError(err) {
			result.Text = err.Error()
		} else {
			return true, err
		}
	}
	if action == "refresh" && result.HandoffID != "" && result.Status == session.TelegramThreadPromotionStatusDraft {
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, result.Text, "", telegramThreadPromotionDraftRows(result.HandoffID)); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, result.Text); err != nil {
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
			{Text: "Cancel", CallbackData: encodeTelegramThreadPromotionCancelCallback(handoffID)},
			{Text: "Ready", CallbackData: encodeTelegramThreadPromotionReadyCallback(handoffID)},
		},
		{
			{Text: "Refresh", CallbackData: encodeTelegramThreadPromotionRefreshCallback(handoffID)},
		},
	}
}
func telegramThreadPromotionCallbackHandoffID(chatID int64, handoffID string) string {
	handoffID = strings.TrimSpace(handoffID)
	parts := strings.Split(handoffID, ":")
	if len(parts) < 4 || parts[0] != "thread-promotion" {
		return handoffID
	}
	if strings.TrimSpace(parts[1]) != "" || chatID == 0 {
		return handoffID
	}
	parts[1] = strconv.FormatInt(chatID, 10)
	return strings.Join(parts, ":")
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
