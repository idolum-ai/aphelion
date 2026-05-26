//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/telegram"
)

const stalePageCallbackText = "This paged view is no longer active. Run the command again."

func handleTelegramPageCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, req telegramPageRequest) (bool, error) {
	chatID := callbackChatID(cb)
	messageID := callbackMessageID(cb)
	senderID := callbackSenderID(cb)
	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), stalePageCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	switch req.Surface {
	case telegramPageSurfaceThreads:
		return handleTelegramThreadsPageCallback(ctx, sender, router, cb, chatID, messageID, req.View, req.Page)
	case telegramPageSurfaceAgents:
		return handleDurableAgentsPageCallback(ctx, sender, router, cb, chatID, messageID, senderID, req.View, req.Page)
	case telegramPageSurfaceHealth:
		return handleHealthTracePageCallback(ctx, sender, router, cb, chatID, messageID, senderID, req.Page)
	case telegramPageSurfaceTailnet:
		return handleTailnetPageCallback(ctx, sender, router, cb, chatID, messageID, senderID, req.View, req.Page)
	default:
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), stalePageCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
}

func handleTelegramThreadsPageCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, view string, page int) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread controls are unavailable.")
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	threads, err := threadRouter.TelegramThreads(chatID)
	if err != nil {
		return true, err
	}
	rendered, rows := renderTelegramThreadsPanel(threads, view, page)
	if len(rows) == 0 {
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, rendered); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		return true, err
	}
	return true, nil
}

func handleDurableAgentsPageCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, view string, page int) (bool, error) {
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Durable-agent controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	agents, err := router.DurableAgentsList(senderID)
	if err != nil {
		return true, err
	}
	rendered, rows := renderDurableAgentsCommandViewPage(agents, view, page)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "agents.page.edit", err)
		return true, err
	}
	return true, nil
}

func handleTailnetPageCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, view string, page int) (bool, error) {
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Tailnet controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	var rendered string
	var rows [][]telegram.InlineButton
	switch view {
	case telegramPageViewSurfaces:
		surfaces, err := router.TailnetSurfaces(senderID)
		if err != nil {
			return true, err
		}
		rendered, rows = renderTailnetSurfacesCommandPage(surfaces, page)
	case telegramPageViewGrants:
		bindings, err := router.TailnetGrantBindings(senderID)
		if err != nil {
			return true, err
		}
		rendered, rows = renderTailnetGrantBindingsCommandPage(bindings, page)
	default:
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), stalePageCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "tailnet.page.edit", err)
		return true, err
	}
	return true, nil
}

func handleHealthTracePageCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, page int) (bool, error) {
	if err := answerHealthCallback(ctx, sender, cb, ""); err != nil {
		return true, err
	}
	personaEffort, governorEffort := router.CurrentEfforts()
	projection, err := renderDebugSnapshotProjection(ctx, router, chatID, senderID, personaEffort, governorEffort)
	if err != nil {
		return true, err
	}
	rendered, rows := renderHealthTracePage(projection, page)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		return true, err
	}
	return true, nil
}
