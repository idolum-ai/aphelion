//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	approvalWindowCallbackPrefix         = "aw:"
	approvalWindowActionEnable15         = "enable15"
	approvalWindowActionEnable15Compound = "enable15_compound"
	approvalWindowActionDouble           = "double"
	approvalWindowActionCancel           = "cancel"
	approvalWindowActionClose            = "close"
	approvalWindowCallbackStale          = "This approval control is no longer available."
	approvalWindowCallbackDuration       = 15 * time.Minute
)

func ApprovalWindowOfferRows(offerID string) [][]telegram.InlineButton {
	return ApprovalWindowOfferRowsForDuration(offerID, approvalWindowCallbackDuration)
}

func ApprovalWindowOfferRowsForDuration(offerID string, duration time.Duration) [][]telegram.InlineButton {
	offerID = strings.TrimSpace(offerID)
	if offerID == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: approvalWindowEnableLabel(duration), CallbackData: encodeApprovalWindowCallbackData(offerID, approvalWindowActionEnable15)},
		{Text: "Close", CallbackData: encodeApprovalWindowCallbackData(offerID, approvalWindowActionClose)},
	}}
}

func ApprovalWindowEmbeddedOfferRows(offer session.ApprovalWindowOffer) [][]telegram.InlineButton {
	return ApprovalWindowEmbeddedOfferRowsForDuration(offer, approvalWindowCallbackDuration)
}

func ApprovalWindowEmbeddedOfferRowsForDuration(offer session.ApprovalWindowOffer, duration time.Duration) [][]telegram.InlineButton {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.ID == "" || !offer.ClosedAt.IsZero() || !offer.UsedAt.IsZero() {
		return nil
	}
	// Embedded rows share a card with another authority surface, so they only
	// expose actions that preserve the source card's existing controls.
	return [][]telegram.InlineButton{{
		{Text: approvalWindowEnableLabel(duration), CallbackData: encodeApprovalWindowCallbackData(offer.ID, approvalWindowActionEnable15Compound)},
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
	return ApprovalWindowRowsForOfferForDuration(offer, approvalWindowCallbackDuration)
}

func ApprovalWindowRowsForOfferForDuration(offer session.ApprovalWindowOffer, duration time.Duration) [][]telegram.InlineButton {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.ID == "" || !offer.ClosedAt.IsZero() {
		return nil
	}
	if !offer.UsedAt.IsZero() {
		return nil
	}
	return ApprovalWindowOfferRowsForDuration(offer.ID, duration)
}

func ApprovalWindowRowsForLiveOffer(offer session.ApprovalWindowOffer) [][]telegram.InlineButton {
	return ApprovalWindowRowsForLiveOfferForDuration(offer, approvalWindowCallbackDuration)
}

func ApprovalWindowRowsForLiveOfferForDuration(offer session.ApprovalWindowOffer, duration time.Duration) [][]telegram.InlineButton {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.ID == "" || !offer.ClosedAt.IsZero() {
		return nil
	}
	if !offer.UsedAt.IsZero() {
		if offer.OpenedLeaseID != "" && offer.OpenedOverrideID != "" {
			return ApprovalWindowActiveRows(offer.ID)
		}
		return nil
	}
	return ApprovalWindowOfferRowsForDuration(offer.ID, duration)
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
	return ApprovalWindowRowsForLiveOfferForDuration(offer, approvalWindowDurationFromRouter(router)), nil
}

func approvalWindowDurationFromRouter(router commandRouter) time.Duration {
	if durations, ok := router.(approvalWindowDurationRouter); ok {
		if duration := durations.DefaultApprovalWindowDuration(); duration > 0 {
			return duration
		}
	}
	return approvalWindowCallbackDuration
}

func approvalWindowEnableLabel(duration time.Duration) string {
	if duration <= 0 {
		duration = approvalWindowCallbackDuration
	}
	return "Approve " + approvalWindowLabelDuration(duration)
}

func approvalWindowLabelDuration(duration time.Duration) string {
	if duration <= 0 {
		duration = approvalWindowCallbackDuration
	}
	if duration%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(duration/time.Hour))
	}
	if duration%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(duration/time.Minute))
	}
	return duration.Truncate(time.Second).String()
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
		return "", "", true
	}
	offerID = strings.TrimSpace(offerID)
	action = strings.TrimSpace(action)
	switch action {
	case approvalWindowActionEnable15, approvalWindowActionEnable15Compound, approvalWindowActionDouble, approvalWindowActionCancel, approvalWindowActionClose:
		return offerID, action, true
	default:
		return offerID, "", true
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

	if strings.TrimSpace(offerID) == "" {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), approvalWindowCallbackStale); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}

	approvals, ok := router.(approvalWindowRouter)
	if action == approvalWindowActionClose {
		if ok && strings.TrimSpace(offerID) != "" {
			if err := approvals.CloseApprovalWindowOffer(ctx, offerID, senderID); err != nil {
				if answerErr := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), approvalWindowCallbackErrorAnswer(err)); answerErr != nil && !telegram.IsStaleCallbackQueryError(answerErr) {
					return true, answerErr
				}
				return true, nil
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
	if !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Approval windows are unavailable."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}

	var text string
	var rows [][]telegram.InlineButton
	duration := approvalWindowDurationFromRouter(router)
	switch action {
	case approvalWindowActionEnable15:
		var result core.ApprovalWindowEnableResult
		result, err = approvals.EnableApprovalWindowOfferResult(ctx, offerID, senderID, duration)
		text = result.Text
		if err == nil && result.Active {
			rows = ApprovalWindowActiveRows(offerID)
		}
	case approvalWindowActionEnable15Compound:
		compound, compoundErr := prepareApprovalWindowCompoundDecisionAction(router, targetMsg, offerID, senderID)
		if compoundErr != nil {
			err = compoundErr
			break
		}
		var result core.ApprovalWindowEnableResult
		result, err = approvals.EnableApprovalWindowOfferResult(ctx, offerID, senderID, duration)
		text = result.Text
		if err == nil && result.Active {
			text += compound.note
			if resolveErr := compound.resolve(); resolveErr != nil {
				cancelResult, cancelErr := approvals.CancelApprovalWindowOfferResult(ctx, offerID, senderID)
				if cancelErr != nil || !cancelResult.Canceled {
					err = fmt.Errorf("%w; rollback approval window failed", resolveErr)
				} else {
					err = resolveErr
				}
			} else {
				rows = ApprovalWindowActiveRows(offerID)
			}
		}
	case approvalWindowActionDouble:
		text, err = approvals.DoubleApprovalWindowOffer(ctx, offerID, senderID)
		rows = ApprovalWindowActiveRows(offerID)
	case approvalWindowActionCancel:
		var result core.ApprovalWindowCancelResult
		result, err = approvals.CancelApprovalWindowOfferResult(ctx, offerID, senderID)
		text = result.Text
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
	if action == approvalWindowActionEnable15Compound {
		// The source decision completion path owns updating the original card with
		// the active approval-window controls. Sending here creates duplicate
		// active control cards for the same offer.
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

type approvalWindowOfferLookupRouter interface {
	ApprovalWindowOfferByID(offerID string) (session.ApprovalWindowOffer, bool, error)
}

type approvalWindowDecisionResolverRouter interface {
	PeekDecisionCallback(decisionID string, actor decision.CallbackActor) (decision.PendingDecision, bool)
	ResolveDecisionCallback(decisionID string, choice string, actor decision.CallbackActor) decision.ResolveResult
}

type approvalWindowCompoundDecisionAction struct {
	note    string
	resolve func() error
}

func prepareApprovalWindowCompoundDecisionAction(router commandRouter, targetMsg core.InboundMessage, offerID string, senderID int64) (approvalWindowCompoundDecisionAction, error) {
	lookup, ok := router.(approvalWindowOfferLookupRouter)
	if !ok {
		return approvalWindowCompoundDecisionAction{}, fmt.Errorf("approval window source is unavailable")
	}
	offer, ok, err := lookup.ApprovalWindowOfferByID(offerID)
	if err != nil {
		return approvalWindowCompoundDecisionAction{}, err
	}
	if !ok {
		return approvalWindowCompoundDecisionAction{}, fmt.Errorf("approval window source is no longer active")
	}
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.SourceKind != session.ApprovalWindowOfferSourceDecision {
		return approvalWindowCompoundDecisionAction{}, fmt.Errorf("approval window source is not actionable")
	}
	if offer.SourceDecisionKind != string(decision.KindProposalApproval) {
		return approvalWindowCompoundDecisionAction{}, fmt.Errorf("approval window source is not a proposal approval")
	}
	return prepareApprovalWindowDecisionCompoundAction(router, targetMsg, senderID, offer)
}

func prepareApprovalWindowDecisionCompoundAction(router commandRouter, targetMsg core.InboundMessage, senderID int64, offer session.ApprovalWindowOffer) (approvalWindowCompoundDecisionAction, error) {
	resolver, ok := router.(approvalWindowDecisionResolverRouter)
	if !ok || strings.TrimSpace(offer.SourceID) == "" {
		return approvalWindowCompoundDecisionAction{}, fmt.Errorf("current approval is no longer active; use the newest prompt if approval is still needed")
	}
	actor := decision.CallbackActor{
		TelegramUserID: senderID,
		ChatID:         targetMsg.ChatID,
		MessageID:      targetMsg.MessageID,
	}
	if _, ok := resolver.PeekDecisionCallback(strings.TrimSpace(offer.SourceID), actor); !ok {
		return approvalWindowCompoundDecisionAction{}, fmt.Errorf("current approval is no longer active; use the newest prompt if approval is still needed")
	}
	return approvalWindowCompoundDecisionAction{
		note: "\n\nCurrent approval: approved.",
		resolve: func() error {
			result := resolver.ResolveDecisionCallback(strings.TrimSpace(offer.SourceID), "approve", actor)
			if !result.Resolved {
				return fmt.Errorf("current approval is no longer active; use the newest prompt if approval is still needed")
			}
			return nil
		},
	}, nil
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
