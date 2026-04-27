//go:build linux

package main

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const staleDeliberationCallbackText = "This deliberation control is no longer active. Use /status for the latest state."

func handleDeliberationControlCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, runID int64, action core.DeliberationControlAction) (bool, error) {
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
	if chatID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDeliberationCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}

	chatStatus, err := router.StatusChat(chatID)
	if err != nil {
		return true, err
	}
	if !deliberationCallbackMatchesActiveRun(chatStatus, runID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDeliberationCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		if messageID != 0 {
			text := staleDeliberationCallbackText
			if cb.Message != nil && strings.TrimSpace(cb.Message.Text) != "" {
				text = cb.Message.Text
			}
			if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
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

	var text string
	switch action {
	case core.DeliberationControlActionStop:
		text = face.RenderTelegramStop(router.Stop(chatID))
	case core.DeliberationControlActionDetach:
		detached, detachErr := router.Detach(chatID, senderID)
		if detachErr != nil {
			return true, detachErr
		}
		text = face.RenderTelegramDetach(detached)
	default:
		return true, nil
	}
	if messageID != 0 {
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
			return true, err
		}
	}
	return true, nil
}

func deliberationCallbackMatchesActiveRun(snapshot core.ChatStatusSnapshot, runID int64) bool {
	if runID <= 0 || snapshot.LatestTurnRun == nil {
		return false
	}
	latest := snapshot.LatestTurnRun
	if latest.ID != runID {
		return false
	}
	return strings.TrimSpace(latest.Status) == string(session.TurnRunStatusRunning)
}
