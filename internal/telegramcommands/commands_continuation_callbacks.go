//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type continuationProposalRefresher interface {
	RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error)
}

type scopedContinuationProposalRefresher interface {
	RefreshContinuationProposalForMessage(ctx context.Context, msg core.InboundMessage, reason string) (session.ContinuationState, bool, error)
}

type scopedContinuationRouter interface {
	ContinuationStateForMessage(msg core.InboundMessage) (session.ContinuationState, error)
	ApproveContinuationForMessage(msg core.InboundMessage, approverID int64) (session.ContinuationState, error)
	ApproveContinuationBundleForMessage(msg core.InboundMessage, approverID int64, phaseIDs []string) (session.ContinuationState, error)
	StopContinuationForMessage(msg core.InboundMessage) (core.StopResult, error)
	TriggerContinuationForMessage(ctx context.Context, msg core.InboundMessage) error
}

func refreshContinuationProposal(ctx context.Context, router commandRouter, chatID int64, reason string) (session.ContinuationState, bool, error) {
	refresher, ok := router.(continuationProposalRefresher)
	if !ok {
		return session.ContinuationState{}, false, nil
	}
	return refresher.RefreshContinuationProposal(ctx, chatID, reason)
}

func refreshContinuationProposalForMessage(ctx context.Context, router commandRouter, msg core.InboundMessage, reason string) (session.ContinuationState, bool, error) {
	if scoped, ok := router.(scopedContinuationProposalRefresher); ok && msg.TelegramThreadID > 0 {
		return scoped.RefreshContinuationProposalForMessage(ctx, msg, reason)
	}
	return refreshContinuationProposal(ctx, router, msg.ChatID, reason)
}

func answerContinuationCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, cb telegram.CallbackQuery, callbackKind string, text string) {
	err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text)
	if err == nil || telegram.IsStaleCallbackQueryError(err) {
		return
	}
	recordTelegramCallbackError(router, chatID, callbackKind+".answer", err)
	log.Printf("WARN continuation callback answer failed chat_id=%d callback_id=%s kind=%s err=%v", chatID, strings.TrimSpace(cb.ID), strings.TrimSpace(callbackKind), err)
}

func editContinuationCallbackMessage(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string) error {
	if messageID == 0 {
		return nil
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		recordTelegramCallbackError(router, chatID, callbackKind+".edit", err)
		log.Printf("WARN continuation callback message update failed chat_id=%d message_id=%d kind=%s err=%v", chatID, messageID, strings.TrimSpace(callbackKind), err)
		return err
	}
	return nil
}

func editContinuationCallbackMessageWithInlineKeyboard(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string, rows [][]telegram.InlineButton) error {
	if messageID == 0 {
		return nil
	}
	if len(rows) == 0 {
		return editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, callbackKind, text)
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, callbackKind+".edit", err)
		log.Printf("WARN continuation callback message update failed chat_id=%d message_id=%d kind=%s err=%v", chatID, messageID, strings.TrimSpace(callbackKind), err)
		return err
	}
	return nil
}

func editHandledContinuationCallbackMessage(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string) {
	err := editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, callbackKind, text)
	if err == nil || telegramCallbackMessageNotModifiedError(err) {
		retireTelegramCallbackProjection(router, chatID, messageID, continuationCallbackRetiredSurface)
	}
}

func editHandledContinuationCallbackMessageWithInlineKeyboard(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string, rows [][]telegram.InlineButton) {
	err := editContinuationCallbackMessageWithInlineKeyboard(ctx, sender, router, chatID, messageID, callbackKind, text, rows)
	if err == nil || telegramCallbackMessageNotModifiedError(err) {
		retireTelegramCallbackProjection(router, chatID, messageID, continuationCallbackRetiredSurface)
	}
}

func telegramCallbackMessageNotModifiedError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "message is not modified") || strings.Contains(value, "not modified")
}

func continuationCallbackDisplayText(msg core.InboundMessage, text string) string {
	prefix := telegramThreadDisplayPrefixForMessage(msg)
	if prefix == "" || strings.HasPrefix(text, prefix) {
		return text
	}
	return prefix + text
}

func triggerContinuationAfterCallback(sender commandCallbackSender, router commandRouter, msg core.InboundMessage, messageID int64, callbackKind string, state session.ContinuationState) {
	go func() {
		triggerCtx, cancel := newCommandTurnContext(context.Background())
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				err := fmt.Errorf("continuation trigger panic: %v", recovered)
				recordTelegramCallbackError(router, msg.ChatID, callbackKind, err)
				log.Printf("WARN continuation trigger callback panicked chat_id=%d kind=%s err=%v", msg.ChatID, strings.TrimSpace(callbackKind), err)
				editContinuationCallbackMessage(triggerCtx, sender, router, msg.ChatID, messageID, callbackKind, continuationCallbackDisplayText(msg, renderContinuationCallbackError(state, err)))
			}
		}()
		err := error(nil)
		if scoped, ok := router.(scopedContinuationRouter); ok && msg.TelegramThreadID > 0 {
			err = scoped.TriggerContinuationForMessage(triggerCtx, msg)
		} else {
			err = router.TriggerContinuation(triggerCtx, msg.ChatID)
		}
		if err != nil {
			recordTelegramCallbackError(router, msg.ChatID, callbackKind, err)
			log.Printf("WARN continuation trigger callback failed chat_id=%d kind=%s err=%v", msg.ChatID, strings.TrimSpace(callbackKind), err)
			editContinuationCallbackMessage(triggerCtx, sender, router, msg.ChatID, messageID, callbackKind, continuationCallbackDisplayText(msg, renderContinuationCallbackError(state, err)))
		}
	}()
}

func handleContinuationCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, decisionID string, action string) (bool, error) {
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	chatID := targetMsg.ChatID
	messageID := targetMsg.MessageID
	var state session.ContinuationState
	if scoped, ok := router.(scopedContinuationRouter); ok && targetMsg.TelegramThreadID > 0 {
		state, err = scoped.ContinuationStateForMessage(targetMsg)
	} else {
		state, err = router.ContinuationState(chatID)
	}
	if err != nil {
		return true, err
	}
	if !continuationCallbackMatchesState(state, decisionID, action) {
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.stale", staleContinuationCallbackText)
		editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stale", continuationCallbackDisplayText(targetMsg, staleContinuationCallbackText))
		return true, nil
	}
	if authText := continuationCallbackAuthorizationFailure(router, cb, chatID, messageID, state); authText != "" {
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.unauthorized", authText)
		if authText == staleContinuationCallbackText {
			editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stale", continuationCallbackDisplayText(targetMsg, authText))
		}
		return true, nil
	}
	if action == continuationActionContinueOnce {
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.stale", legacyContinueOnceCallbackText)
		editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stale", continuationCallbackDisplayText(targetMsg, renderContinuationDecision(state, action)))
		return true, nil
	}

	var text string
	switch action {
	case continuationActionApproveLease, continuationActionApproveBundleAll, continuationActionApproveBundleCurrent:
		approverID := int64(0)
		if cb.From != nil {
			approverID = cb.From.ID
		}
		if targetMsg.SenderID == 0 {
			targetMsg.SenderID = approverID
		}
		phaseIDs := continuationCallbackBundlePhaseIDs(state, action)
		if action == continuationActionApproveBundleAll || action == continuationActionApproveBundleCurrent {
			if scoped, ok := router.(scopedContinuationRouter); ok && targetMsg.TelegramThreadID > 0 {
				state, err = scoped.ApproveContinuationBundleForMessage(targetMsg, approverID, phaseIDs)
			} else {
				state, err = router.ApproveContinuationBundle(chatID, approverID, phaseIDs)
			}
		} else if scoped, ok := router.(scopedContinuationRouter); ok && targetMsg.TelegramThreadID > 0 {
			state, err = scoped.ApproveContinuationForMessage(targetMsg, approverID)
		} else {
			state, err = router.ApproveContinuation(chatID, approverID)
		}
		if err != nil {
			if errors.Is(err, core.ErrContinuationExpired) {
				refreshedState, refreshed, refreshErr := refreshContinuationProposalForMessage(ctx, router, targetMsg, "expired approval callback")
				if refreshErr != nil {
					recordTelegramCallbackError(router, chatID, "continuation.refresh", refreshErr)
					log.Printf("WARN continuation refresh callback failed chat_id=%d err=%v", chatID, refreshErr)
				} else if refreshed {
					answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", "That continuation request expired, so I sent a fresh approval prompt.")
					editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", continuationCallbackDisplayText(targetMsg, renderContinuationRefreshedDecision(refreshedState)))
					return true, nil
				} else if refreshedState.Status == session.ContinuationStatusPending {
					answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", "A fresh continuation prompt is already active. Use the newest prompt.")
					editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", continuationCallbackDisplayText(targetMsg, renderContinuationRefreshAlreadyActiveDecision(refreshedState)))
					return true, nil
				}
			}
			recordTelegramCallbackError(router, chatID, "continuation.approve", err)
			log.Printf("WARN continuation approve callback failed chat_id=%d approver_id=%d err=%v", chatID, approverID, err)
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", continuationCallbackErrorText(err))
			editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", continuationCallbackDisplayText(targetMsg, renderContinuationCallbackError(state, err)))
			return true, nil
		}
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", "")
		text = renderContinuationDecision(state, action)
		rows, offerErr := postApprovalWindowOfferRowsForSource(ctx, router, targetMsg, session.ApprovalWindowOfferSourceContinuation, decisionID, "continuation")
		if offerErr != nil {
			return true, offerErr
		}
		if len(rows) > 0 {
			editHandledContinuationCallbackMessageWithInlineKeyboard(ctx, sender, router, chatID, messageID, "continuation.approve", continuationCallbackDisplayText(targetMsg, text), rows)
		} else {
			editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", continuationCallbackDisplayText(targetMsg, text))
		}
		triggerContinuationAfterCallback(sender, router, targetMsg, messageID, "continuation.trigger", state)
		return true, nil
	case continuationActionResumeEdge:
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.resume", "")
		text = renderContinuationDecision(state, action)
		editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.resume", continuationCallbackDisplayText(targetMsg, text))
		if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
			triggerContinuationAfterCallback(sender, router, targetMsg, messageID, "continuation.resume", state)
		}
		return true, nil
	case continuationActionAskEdit:
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.ask_edit", "")
		if scoped, ok := router.(scopedContinuationRouter); ok && targetMsg.TelegramThreadID > 0 {
			_, err = scoped.StopContinuationForMessage(targetMsg)
		} else {
			_, err = router.StopContinuation(chatID)
		}
		if err != nil {
			recordTelegramCallbackError(router, chatID, "continuation.ask_edit", err)
			log.Printf("WARN continuation ask-edit callback failed chat_id=%d err=%v", chatID, err)
			editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.ask_edit", continuationCallbackDisplayText(targetMsg, renderContinuationCallbackError(state, err)))
			return true, nil
		}
		text = renderContinuationDecision(state, action)
		editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.ask_edit", continuationCallbackDisplayText(targetMsg, text))
		return true, nil
	case continuationActionStop, continuationActionStopPark:
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.stop", "")
		var stopped core.StopResult
		if scoped, ok := router.(scopedContinuationRouter); ok && targetMsg.TelegramThreadID > 0 {
			stopped, err = scoped.StopContinuationForMessage(targetMsg)
		} else {
			stopped, err = router.StopContinuation(chatID)
		}
		if err != nil {
			recordTelegramCallbackError(router, chatID, "continuation.stop", err)
			log.Printf("WARN continuation stop callback failed chat_id=%d err=%v", chatID, err)
			editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stop", continuationCallbackDisplayText(targetMsg, renderContinuationCallbackError(state, err)))
			return true, nil
		}
		if action == continuationActionStopPark {
			text = "Continuation parked. " + face.RenderTelegramStop(stopped)
		} else {
			text = face.RenderTelegramStop(stopped)
		}
		editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stop", continuationCallbackDisplayText(targetMsg, text))
		return true, nil
	case continuationActionAskNextLease:
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.refresh", "")
		refreshedState, refreshed, refreshErr := refreshContinuationProposalForMessage(ctx, router, targetMsg, "operator requested next lease")
		if refreshErr != nil {
			recordTelegramCallbackError(router, chatID, "continuation.refresh", refreshErr)
			log.Printf("WARN continuation refresh callback failed chat_id=%d err=%v", chatID, refreshErr)
			editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.refresh", continuationCallbackDisplayText(targetMsg, renderContinuationCallbackError(state, refreshErr)))
			return true, nil
		}
		if refreshed {
			text = renderContinuationRefreshedDecision(refreshedState)
		} else if refreshedState.Status == session.ContinuationStatusPending {
			text = renderContinuationRefreshAlreadyActiveDecision(refreshedState)
		} else {
			text = renderContinuationDecision(state, action)
		}
		editHandledContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.refresh", continuationCallbackDisplayText(targetMsg, text))
		return true, nil
	case continuationActionStatusOnly:
		answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.status", "")
		text = renderContinuationDecision(state, action)
		editContinuationCallbackMessageWithInlineKeyboard(ctx, sender, router, chatID, messageID, "continuation.status", continuationCallbackDisplayText(targetMsg, text), continuationApprovalButtonRows(state))
		return true, nil
	default:
		return true, nil
	}
}

func continuationCallbackBundlePhaseIDs(state session.ContinuationState, action string) []string {
	if action != continuationActionApproveBundleCurrent {
		return nil
	}
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	current := strings.TrimSpace(bundle.CurrentPhaseID)
	if current == "" && len(bundle.Phases) > 0 {
		current = strings.TrimSpace(bundle.Phases[0].ID)
	}
	if current == "" {
		return nil
	}
	return []string{current}
}
