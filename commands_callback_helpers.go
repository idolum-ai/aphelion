//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
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

type continuationProposalRefresher interface {
	RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error)
}

func recordTelegramCallbackError(router commandRouter, chatID int64, callbackKind string, err error) {
	if err == nil {
		return
	}
	if recorder, ok := router.(telegramCallbackErrorRecorder); ok {
		recorder.RecordTelegramCallbackError(chatID, callbackKind, err)
	}
}

func refreshContinuationProposal(ctx context.Context, router commandRouter, chatID int64, reason string) (session.ContinuationState, bool, error) {
	refresher, ok := router.(continuationProposalRefresher)
	if !ok {
		return session.ContinuationState{}, false, nil
	}
	return refresher.RefreshContinuationProposal(ctx, chatID, reason)
}

func answerContinuationCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, cb telegram.CallbackQuery, callbackKind string, text string) {
	err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text)
	if err == nil || telegram.IsStaleCallbackQueryError(err) {
		return
	}
	recordTelegramCallbackError(router, chatID, callbackKind+".answer", err)
	log.Printf("WARN continuation callback answer failed chat_id=%d callback_id=%s kind=%s err=%v", chatID, strings.TrimSpace(cb.ID), strings.TrimSpace(callbackKind), err)
}

func editContinuationCallbackMessage(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string) {
	if messageID == 0 {
		return
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		recordTelegramCallbackError(router, chatID, callbackKind+".edit", err)
		log.Printf("WARN continuation callback message update failed chat_id=%d message_id=%d kind=%s err=%v", chatID, messageID, strings.TrimSpace(callbackKind), err)
	}
}

func editContinuationCallbackMessageWithInlineKeyboard(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string, rows [][]telegram.InlineButton) {
	if messageID == 0 {
		return
	}
	if len(rows) == 0 {
		editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, callbackKind, text)
		return
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, callbackKind+".edit", err)
		log.Printf("WARN continuation callback message update failed chat_id=%d message_id=%d kind=%s err=%v", chatID, messageID, strings.TrimSpace(callbackKind), err)
	}
}

func triggerContinuationAfterCallback(sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, state session.ContinuationState) {
	go func() {
		triggerCtx, cancel := newTurnContext(context.Background(), turnTimeout)
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				err := fmt.Errorf("continuation trigger panic: %v", recovered)
				recordTelegramCallbackError(router, chatID, callbackKind, err)
				log.Printf("WARN continuation trigger callback panicked chat_id=%d kind=%s err=%v", chatID, strings.TrimSpace(callbackKind), err)
				editContinuationCallbackMessage(triggerCtx, sender, router, chatID, messageID, callbackKind, renderContinuationCallbackError(state, err))
			}
		}()
		if err := router.TriggerContinuation(triggerCtx, chatID); err != nil {
			recordTelegramCallbackError(router, chatID, callbackKind, err)
			log.Printf("WARN continuation trigger callback failed chat_id=%d kind=%s err=%v", chatID, strings.TrimSpace(callbackKind), err)
			editContinuationCallbackMessage(triggerCtx, sender, router, chatID, messageID, callbackKind, renderContinuationCallbackError(state, err))
		}
	}()
}

func editCallbackMessageClearingInlineKeyboard(ctx context.Context, sender commandCallbackSender, chatID int64, messageID int64, text string) error {
	if clearer, ok := sender.(commandInlineKeyboardClearer); ok {
		return clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, messageID, text, "")
	}
	return sender.EditMessageText(ctx, chatID, messageID, text, "")
}

func handleTelegramCommandCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	if sender == nil || router == nil {
		return false, nil
	}
	if streamID, action, ok := core.DecodeStreamControlCallbackData(cb.Data); ok {
		return handleStreamControlCallback(ctx, sender, router, cb, streamID, action)
	}
	if runID, action, ok := core.DecodeDeliberationControlCallbackData(cb.Data); ok {
		return handleDeliberationControlCallback(ctx, sender, router, cb, runID, action)
	}
	if command, ok := decodeCommandMenuCallbackData(cb.Data); ok {
		return handleCommandMenuCallback(ctx, sender, router, cb, command)
	}
	if surface, action, ok := decodeAutoCallbackData(cb.Data); ok {
		return handleAutoCallback(ctx, sender, router, cb, surface, action)
	}
	if action, ok := decodeHealthCallbackData(cb.Data); ok {
		return handleHealthCallback(ctx, sender, router, cb, action)
	}
	if action, token, ok := decodeTailnetRevokeTokenCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		senderID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		if cb.From != nil {
			senderID = cb.From.ID
		}
		if chatID == 0 || messageID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		if !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Tailnet controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		surfaces, err := router.TailnetSurfaces(senderID)
		if err != nil {
			return true, err
		}
		surfaceID, ok := resolveTailnetSurfaceCallbackToken(surfaces, token)
		if !ok {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		if action == tailnetRevokeCallbackAsk {
			rendered, rows := renderTailnetRevokeTokenConfirmation(surfaceID)
			if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
				return true, err
			}
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		if action == tailnetRevokeCallbackCancel {
			if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeCanceled(surfaceID)); err != nil {
				return true, err
			}
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		surface, found, err := router.RevokeTailnetSurface(ctx, senderID, surfaceID, "telegram tailnet revoke confirmation")
		if err != nil {
			return true, err
		}
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeResult(surfaceID, surface, found)); err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if action, surfaceID, ok := decodeTailnetRevokeCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		senderID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		if cb.From != nil {
			senderID = cb.From.ID
		}
		if chatID == 0 || messageID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		if !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Tailnet controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		if action == tailnetRevokeCallbackCancel {
			if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeCanceled(surfaceID)); err != nil {
				return true, err
			}
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		surface, found, err := router.RevokeTailnetSurface(ctx, senderID, surfaceID, "telegram tailnet revoke confirmation")
		if err != nil {
			return true, err
		}
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeResult(surfaceID, surface, found)); err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if action, ok := decodeTailnetCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		senderID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		if cb.From != nil {
			senderID = cb.From.ID
		}
		if (action != tailnetCallbackRefresh && action != tailnetCallbackSurfaces && action != tailnetCallbackGrants) || chatID == 0 || messageID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminStatusOnlyText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		var rendered string
		var rows [][]telegram.InlineButton
		if action == tailnetCallbackSurfaces {
			surfaces, err := router.TailnetSurfaces(senderID)
			if err != nil {
				return true, err
			}
			rendered, rows = renderTailnetSurfacesCommand(surfaces)
		} else if action == tailnetCallbackGrants {
			bindings, err := router.TailnetGrantBindings(senderID)
			if err != nil {
				return true, err
			}
			rendered, rows = renderTailnetGrantBindingsCommand(bindings)
		} else {
			snapshot, err := router.TailnetStatus(ctx, senderID)
			if err != nil {
				return true, err
			}
			rendered, rows = renderTailnetCommand(snapshot)
		}
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
			return true, err
		}
		return true, nil
	}
	if view, targetChatID, ok := decodeStatusCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		senderID := int64(0)
		if cb.From != nil {
			senderID = cb.From.ID
		}
		if statusViewRequiresAdmin(view, chatID, targetChatID) && !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminStatusOnlyText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		personaEffort, governorEffort := router.CurrentEfforts()
		rendered, rows, err := renderStatusView(ctx, router, chatID, senderID, view, targetChatID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		if chatID == 0 {
			chatID = targetChatID
		}
		if chatID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if err := deliverStatusCallbackView(ctx, sender, chatID, messageID, rendered, rows); err != nil {
			return true, err
		}
		return true, nil
	}
	if action, token, ok := decodeMissionCallbackData(cb.Data); ok {
		return handleMissionCallback(ctx, sender, router, cb, action, token)
	}
	if proposalID, action, ok := decodeActionProposalCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		senderID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		if cb.From != nil {
			senderID = cb.From.ID
		}
		missionID := missionIDFromActionProposalID(proposalID)
		if missionID == "" || chatID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleActionProposalCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		proposal, err := router.MissionActionProposal(ctx, chatID, senderID, missionID)
		if err != nil || strings.TrimSpace(proposal.ID) != proposalID {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleActionProposalCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		mission, changed, err := router.ApplyMissionActionProposalDecision(ctx, chatID, senderID, missionID, action)
		if err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		if messageID != 0 {
			if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderActionProposalDecision(proposal, mission, action, changed)); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	if decisionID, action, ok := decodeContinuationCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil && cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
			messageID = cb.Message.MessageID
		}
		state, err := router.ContinuationState(chatID)
		if err != nil {
			return true, err
		}
		if !continuationCallbackMatchesState(state, decisionID, action) {
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.stale", staleContinuationCallbackText)
			return true, nil
		}
		var text string
		switch action {
		case continuationActionApproveLease, continuationActionContinueOnce:
			approverID := int64(0)
			if cb.From != nil {
				approverID = cb.From.ID
			}
			state, err := router.ApproveContinuation(chatID, approverID)
			if err != nil {
				if errors.Is(err, core.ErrContinuationExpired) {
					refreshedState, refreshed, refreshErr := refreshContinuationProposal(ctx, router, chatID, "expired approval callback")
					if refreshErr != nil {
						recordTelegramCallbackError(router, chatID, "continuation.refresh", refreshErr)
						log.Printf("WARN continuation refresh callback failed chat_id=%d err=%v", chatID, refreshErr)
					} else if refreshed {
						answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", "That continuation lease expired, so I sent a fresh approval prompt.")
						editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", renderContinuationRefreshedDecision(refreshedState))
						return true, nil
					} else if refreshedState.Status == session.ContinuationStatusPending {
						answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", "A fresh continuation prompt is already active. Use the newest prompt.")
						editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", renderContinuationRefreshAlreadyActiveDecision(refreshedState))
						return true, nil
					}
				}
				recordTelegramCallbackError(router, chatID, "continuation.approve", err)
				log.Printf("WARN continuation approve callback failed chat_id=%d approver_id=%d err=%v", chatID, approverID, err)
				answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", continuationCallbackErrorText(err))
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", renderContinuationCallbackError(state, err))
				return true, nil
			}
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", "")
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", text)
			triggerContinuationAfterCallback(sender, router, chatID, messageID, "continuation.trigger", state)
			return true, nil
		case continuationActionResumeEdge:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.resume", "")
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.resume", text)
			if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
				triggerContinuationAfterCallback(sender, router, chatID, messageID, "continuation.resume", state)
			}
			return true, nil
		case continuationActionAskEdit:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.ask_edit", "")
			if _, err := router.StopContinuation(chatID); err != nil {
				recordTelegramCallbackError(router, chatID, "continuation.ask_edit", err)
				log.Printf("WARN continuation ask-edit callback failed chat_id=%d err=%v", chatID, err)
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.ask_edit", renderContinuationCallbackError(state, err))
				return true, nil
			}
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.ask_edit", text)
			return true, nil
		case continuationActionStop, continuationActionStopPark:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.stop", "")
			stopped, err := router.StopContinuation(chatID)
			if err != nil {
				recordTelegramCallbackError(router, chatID, "continuation.stop", err)
				log.Printf("WARN continuation stop callback failed chat_id=%d err=%v", chatID, err)
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stop", renderContinuationCallbackError(state, err))
				return true, nil
			}
			if action == continuationActionStopPark {
				text = "Continuation parked. " + face.RenderTelegramStop(stopped)
			} else {
				text = face.RenderTelegramStop(stopped)
			}
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stop", text)
			return true, nil
		case continuationActionAskNextLease:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.refresh", "")
			refreshedState, refreshed, refreshErr := refreshContinuationProposal(ctx, router, chatID, "operator requested next lease")
			if refreshErr != nil {
				recordTelegramCallbackError(router, chatID, "continuation.refresh", refreshErr)
				log.Printf("WARN continuation refresh callback failed chat_id=%d err=%v", chatID, refreshErr)
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.refresh", renderContinuationCallbackError(state, refreshErr))
				return true, nil
			}
			if refreshed {
				text = renderContinuationRefreshedDecision(refreshedState)
			} else if refreshedState.Status == session.ContinuationStatusPending {
				text = renderContinuationRefreshAlreadyActiveDecision(refreshedState)
			} else {
				text = renderContinuationDecision(state, action)
			}
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.refresh", text)
			return true, nil
		case continuationActionStatusOnly:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.status", "")
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessageWithInlineKeyboard(ctx, sender, router, chatID, messageID, "continuation.status", text, runtimepkg.ContinuationApprovalButtonRows(state))
			return true, nil
		default:
			return true, nil
		}
	}
	if action, step, option, ok := decodeDurableWizardCallbackData(cb.Data); ok {
		return handleDurableWizardCallback(ctx, sender, router, cb, action, step, option)
	}
	if action, agentID, ok := decodeDurableAgentsCallbackData(cb.Data); ok {
		return handleDurableAgentsCallback(ctx, sender, router, cb, action, agentID)
	}
	if action, source, index, ok := decodeMemoryReviewCallbackData(cb.Data); ok {
		return handleMemoryReviewCallback(ctx, sender, router, cb, action, source, index)
	}
	if action, slot, value, ok := decodeModelCallbackData(cb.Data); ok {
		return handleModelCallback(ctx, sender, router, cb, action, slot, value)
	}
	kind, value, ok := decodeRecipeCallbackData(cb.Data)
	if !ok {
		return false, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}

	var (
		text string
		err  error
	)
	switch kind {
	case "persona_model":
		var selected string
		selected, err = router.SetPersonaModel(value)
		if err == nil {
			text = face.RenderTelegramSetPersonaModel(selected)
		}
	case "governor_effort":
		var selected string
		selected, err = router.SetGovernorEffort(value)
		if err == nil {
			text = face.RenderTelegramSetGovernorEffort(selected)
		}
	default:
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if cb.Message != nil && cb.Message.Chat != nil && cb.Message.MessageID != 0 {
		if editErr := editCallbackMessageClearingInlineKeyboard(ctx, sender, cb.Message.Chat.ID, cb.Message.MessageID, text); editErr != nil {
			return true, editErr
		}
	}
	return true, nil
}
