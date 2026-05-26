//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/telegram"
)

func handleTailnetSurfaceCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, token string) (bool, error) {
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
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	rendered, rows := renderTailnetSurfaceDetail(surface)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "tailnet.surface.edit", err)
		return true, err
	}
	return true, nil
}

func handleTailnetGrantCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, token string) (bool, error) {
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
	bindings, err := router.TailnetGrantBindings(senderID)
	if err != nil {
		return true, err
	}
	binding, ok := resolveTailnetGrantCallbackToken(bindings, token)
	if !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	rendered, rows := renderTailnetGrantDetail(binding)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "tailnet.grant.edit", err)
		return true, err
	}
	return true, nil
}
