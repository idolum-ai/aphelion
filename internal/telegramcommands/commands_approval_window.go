//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
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

func ApprovalWindowOfferRows(offerID string) [][]telegram.InlineButton {
	offerID = strings.TrimSpace(offerID)
	if offerID == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: "Approve 15m", CallbackData: encodeApprovalWindowCallbackData(offerID, approvalWindowActionEnable15)},
		{Text: "Close", CallbackData: encodeApprovalWindowCallbackData(offerID, approvalWindowActionClose)},
	}}
}

func ApprovalWindowEmbeddedOfferRows(offer session.ApprovalWindowOffer) [][]telegram.InlineButton {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.ID == "" || !offer.ClosedAt.IsZero() || !offer.UsedAt.IsZero() {
		return nil
	}
	// Embedded rows share a card with another authority surface, so they only
	// expose actions that preserve the source card's existing controls.
	return [][]telegram.InlineButton{{
		{Text: "Approve 15m", CallbackData: encodeApprovalWindowCallbackData(offer.ID, approvalWindowActionEnable15)},
	}}
}

func ApprovalWindowActiveRows(offerID string) [][]telegram.InlineButton {
	offerID = strings.TrimSpace(offerID)
	if offerID == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: "Double time", CallbackData: encodeApprovalWindowCallbackData(offerID, approvalWindowActionDouble)},
		{Text: "Cancel approvals", CallbackData: encodeApprovalWindowCallbackData(offerID, approvalWindowActionCancel)},
	}}
}

func ApprovalWindowRowsForOffer(offer session.ApprovalWindowOffer) [][]telegram.InlineButton {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.ID == "" || !offer.ClosedAt.IsZero() {
		return nil
	}
	if !offer.UsedAt.IsZero() {
		return ApprovalWindowActiveRows(offer.ID)
	}
	return ApprovalWindowOfferRows(offer.ID)
}

func approvalWindowOfferRowsForSource(ctx context.Context, router commandRouter, msg core.InboundMessage, sourceKind string, sourceID string, sourceDecisionKind string) ([][]telegram.InlineButton, error) {
	approvals, ok := router.(approvalWindowRouter)
	if !ok {
		return nil, nil
	}
	offer, created, err := approvals.CreateApprovalWindowOfferForMessage(ctx, msg, sourceKind, sourceID, sourceDecisionKind)
	if err != nil || !created {
		return nil, err
	}
	return ApprovalWindowRowsForOffer(offer), nil
}

func encodeApprovalWindowCallbackData(offerID string, action string) string {
	return approvalWindowCallbackPrefix + strings.TrimSpace(offerID) + ":" + strings.TrimSpace(action)
}

func decodeApprovalWindowCallbackData(data string) (string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, approvalWindowCallbackPrefix) {
		return "", "", false
	}
	body := strings.TrimSpace(strings.TrimPrefix(trimmed, approvalWindowCallbackPrefix))
	offerID, action, ok := strings.Cut(body, ":")
	if !ok {
		// Legacy no-token callbacks fail closed for authority-bearing actions.
		return "", strings.TrimSpace(body), true
	}
	offerID = strings.TrimSpace(offerID)
	action = strings.TrimSpace(action)
	switch action {
	case approvalWindowActionEnable15, approvalWindowActionDouble, approvalWindowActionCancel, approvalWindowActionClose:
		return offerID, action, offerID != "" || action == approvalWindowActionClose
	default:
		return "", "", false
	}
}

func handleApprovalWindowCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, offerID string, action string) (bool, error) {
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	chatID := targetMsg.ChatID
	messageID := targetMsg.MessageID
	senderID := int64(0)
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if targetMsg.SenderID == 0 {
		targetMsg.SenderID = senderID
	}
	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), approvalWindowCallbackStale); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}

	approvals, ok := router.(approvalWindowRouter)
	if action == approvalWindowActionClose {
		if ok && strings.TrimSpace(offerID) != "" {
			if err := approvals.CloseApprovalWindowOffer(ctx, offerID); err != nil {
				return true, err
			}
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		text := continuationCallbackDisplayText(targetMsg, approvalWindowCallbackClosedText(cb))
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
			return true, err
		}
		return true, nil
	}
	if strings.TrimSpace(offerID) == "" {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), approvalWindowCallbackStale); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
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
		text, err = approvals.EnableApprovalWindowOffer(ctx, offerID, senderID, approvalWindowCallbackDuration)
		rows = ApprovalWindowActiveRows(offerID)
	case approvalWindowActionDouble:
		text, err = approvals.DoubleApprovalWindowOffer(ctx, offerID, senderID)
		rows = ApprovalWindowActiveRows(offerID)
	case approvalWindowActionCancel:
		text, err = approvals.CancelApprovalWindowOffer(ctx, offerID, senderID)
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
	if action == approvalWindowActionEnable15 {
		replyTo := messageID
		if _, err := sender.SendInlineKeyboard(ctx, chatID, text, rows, &replyTo); err != nil {
			return true, err
		}
		return true, nil
	}
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
	if cb.Message != nil && strings.TrimSpace(cb.Message.Text) != "" {
		return strings.TrimSpace(cb.Message.Text)
	}
	return "Approval window offer closed."
}

func approvalWindowCallbackErrorAnswer(err error) string {
	if err == nil {
		return "Approval window unavailable."
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		return "Approval window unavailable."
	}
	if len(text) > 180 {
		text = text[:177] + "..."
	}
	return text
}

func renderApprovalWindowCallbackError(err error) string {
	why := "Approval window unavailable."
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		why = strings.TrimSpace(err.Error())
	}
	return fmt.Sprintf("Approval window was not opened.\n\n%s", why)
}
