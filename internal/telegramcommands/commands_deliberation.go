//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	staleDeliberationCallbackText        = "This deliberation control is no longer active. Use /status for the latest state."
	adminDeliberationDetailsCallbackText = "Details are available to Telegram admins only."
	adminDeliberationDetachCallbackText  = "Detach controls are available to Telegram admins only."
)

type commandRunControlRouter interface {
	StopRun(runID int64, senderID int64) (core.StopResult, bool, error)
	DetachRun(runID int64, senderID int64) (core.DetachResult, bool, error)
}

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

	if action == core.DeliberationControlActionDetails || action == core.DeliberationControlActionSummary {
		if messageID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDeliberationCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if action == core.DeliberationControlActionDetails && !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminDeliberationDetailsCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		updated, _, toggleErr := router.ToggleProgressView(ctx, chatID, senderID, runID, action == core.DeliberationControlActionDetails)
		if toggleErr != nil {
			return true, toggleErr
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		if !updated {
			return true, nil
		}
		return true, nil
	}

	runControl, hasRunControl := router.(commandRunControlRouter)
	if !hasRunControl {
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
	}

	stale := func() (bool, error) {
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

	if action == core.DeliberationControlActionDetach && !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminDeliberationDetachCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}

	var text string
	switch action {
	case core.DeliberationControlActionStop:
		if hasRunControl {
			stopped, ok, stopErr := runControl.StopRun(runID, senderID)
			if stopErr != nil {
				return true, stopErr
			}
			if !ok {
				return stale()
			}
			text = face.RenderTelegramStop(stopped)
		} else {
			text = face.RenderTelegramStop(router.Stop(chatID))
		}
	case core.DeliberationControlActionDetach:
		var detached core.DetachResult
		var detachErr error
		if hasRunControl {
			var ok bool
			detached, ok, detachErr = runControl.DetachRun(runID, senderID)
			if detachErr == nil && !ok {
				return stale()
			}
		} else {
			detached, detachErr = router.Detach(chatID, senderID)
		}
		if detachErr != nil {
			return true, detachErr
		}
		text = face.RenderTelegramDetach(detached)
	default:
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
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
