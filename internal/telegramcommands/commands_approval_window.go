//go:build linux

package telegramcommands

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/telegram"
)

const (
	approvalWindowCallbackPrefix   = "aw:"
	approvalWindowActionEnable15   = "enable15"
	approvalWindowActionDouble     = "double"
	approvalWindowActionCancel     = "cancel"
	approvalWindowActionClose      = "close"
	approvalWindowCallbackStale    = "This approval control is no longer available."
	approvalWindowCallbackDuration = 15 * time.Minute
)

func approvalWindowOfferRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Approve next 15 min", CallbackData: encodeApprovalWindowCallbackData(approvalWindowActionEnable15)},
		{Text: "Close", CallbackData: encodeApprovalWindowCallbackData(approvalWindowActionClose)},
	}}
}

func approvalWindowActiveRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "2x approval time", CallbackData: encodeApprovalWindowCallbackData(approvalWindowActionDouble)},
		{Text: "Cancel approvals", CallbackData: encodeApprovalWindowCallbackData(approvalWindowActionCancel)},
	}}
}

func encodeApprovalWindowCallbackData(action string) string {
	return approvalWindowCallbackPrefix + strings.TrimSpace(action)
}

func decodeApprovalWindowCallbackData(data string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, approvalWindowCallbackPrefix) {
		return "", false
	}
	action := strings.TrimSpace(strings.TrimPrefix(trimmed, approvalWindowCallbackPrefix))
	switch action {
	case approvalWindowActionEnable15, approvalWindowActionDouble, approvalWindowActionCancel, approvalWindowActionClose:
		return action, true
	default:
		return "", false
	}
}

func handleApprovalWindowCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string) (bool, error) {
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	chatID := targetMsg.ChatID
	messageID := targetMsg.MessageID
	if targetMsg.SenderID == 0 && cb.From != nil {
		targetMsg.SenderID = cb.From.ID
	}
	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), approvalWindowCallbackStale); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if action == approvalWindowActionClose {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		text := approvalWindowCallbackClosedText(cb)
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
			return true, err
		}
		return true, nil
	}

	approvals, ok := router.(approvalWindowRouter)
	if !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Approval windows are unavailable."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}

	var text string
	var rows [][]telegram.InlineButton
	switch action {
	case approvalWindowActionEnable15:
		text, err = approvals.EnableApprovalWindowForMessage(ctx, targetMsg, approvalWindowCallbackDuration)
		rows = approvalWindowActiveRows()
	case approvalWindowActionDouble:
		text, err = approvals.DoubleApprovalWindowForMessage(ctx, targetMsg)
		rows = approvalWindowActiveRows()
	case approvalWindowActionCancel:
		text, err = approvals.CancelApprovalWindowForMessage(ctx, targetMsg)
	default:
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), approvalWindowCallbackStale); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err != nil {
		if answerErr := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), approvalWindowCallbackErrorAnswer(err)); answerErr != nil && !telegram.IsStaleCallbackQueryError(answerErr) {
			return true, answerErr
		}
		if editErr := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, continuationCallbackDisplayText(targetMsg, renderApprovalWindowCallbackError(err))); editErr != nil {
			return true, editErr
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	text = continuationCallbackDisplayText(targetMsg, text)
	if len(rows) > 0 {
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		return true, err
	}
	return true, nil
}

func approvalWindowCallbackClosedText(cb telegram.CallbackQuery) string {
	if cb.Message != nil {
		if text := strings.TrimSpace(cb.Message.Text); text != "" {
			return text
		}
	}
	return "Approval controls closed."
}

func approvalWindowCallbackErrorAnswer(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "Approval window action failed."
	}
	if len(msg) > 180 {
		msg = msg[:180]
	}
	return msg
}

func renderApprovalWindowCallbackError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "Unknown error."
	}
	return "Approval window action failed.\n\n" + msg
}
