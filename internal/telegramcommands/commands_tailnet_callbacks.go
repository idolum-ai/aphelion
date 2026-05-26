//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/telegram"
)

func handleTailnetRevokeTokenCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string, token string) (bool, error) {
	chatID := callbackChatID(cb)
	messageID := callbackMessageID(cb)
	senderID := callbackSenderID(cb)
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
	surface, ok := resolveTailnetSurfaceCallbackToken(surfaces, token)
	if !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	surfaceID := strings.TrimSpace(surface.SurfaceID)
	if action == tailnetRevokeCallbackAsk {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		rendered, rows := renderTailnetRevokeTokenConfirmation(surfaceID)
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
			recordTelegramCallbackError(router, chatID, "tailnet.revoke.ask.edit", err)
			return true, err
		}
		return true, nil
	}
	return completeTailnetRevokeCallback(ctx, sender, router, cb, chatID, messageID, senderID, action, surfaceID)
}

func completeTailnetRevokeCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, action string, surfaceID string) (bool, error) {
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	if action == tailnetRevokeCallbackCancel {
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeCanceled(surfaceID)); err != nil {
			recordTelegramCallbackError(router, chatID, "tailnet.revoke.cancel.edit", err)
			return true, err
		}
		return true, nil
	}
	surface, found, err := router.RevokeTailnetSurface(ctx, senderID, surfaceID, "telegram tailnet revoke confirmation")
	if err != nil {
		recordTelegramCallbackError(router, chatID, "tailnet.revoke", err)
		return true, err
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeResult(surfaceID, surface, found)); err != nil {
		recordTelegramCallbackError(router, chatID, "tailnet.revoke.edit", err)
		return true, err
	}
	return true, nil
}

func handleTailnetCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string) (bool, error) {
	chatID := callbackChatID(cb)
	messageID := callbackMessageID(cb)
	senderID := callbackSenderID(cb)
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
		rendered, rows = renderTailnetSurfacesCommandPage(surfaces, 1)
	} else if action == tailnetCallbackGrants {
		bindings, err := router.TailnetGrantBindings(senderID)
		if err != nil {
			return true, err
		}
		rendered, rows = renderTailnetGrantBindingsCommandPage(bindings, 1)
	} else {
		snapshot, err := router.TailnetStatus(ctx, senderID)
		if err != nil {
			return true, err
		}
		rendered, rows = renderTailnetCommand(snapshot)
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "tailnet."+action+".edit", err)
		return true, err
	}
	return true, nil
}
