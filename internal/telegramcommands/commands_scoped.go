//go:build linux

package telegramcommands

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func renderStatusCommand(ctx context.Context, router commandRouter, msg core.InboundMessage, personaEffort string, governorEffort string) (string, [][]telegram.InlineButton, error) {
	if msg.TelegramThreadID <= 0 {
		return renderStatusView(ctx, router, msg.ChatID, msg.SenderID, statusViewChat, msg.ChatID, personaEffort, governorEffort)
	}
	return renderThreadStatusView(ctx, router, msg, statusViewChat, personaEffort, governorEffort)
}

func renderThreadStatusView(ctx context.Context, router commandRouter, msg core.InboundMessage, view statusView, personaEffort string, governorEffort string) (string, [][]telegram.InlineButton, error) {
	if view != statusViewPending {
		view = statusViewChat
	}
	scoped, ok := router.(commandScopedStatusRouter)
	if !ok {
		return renderStatusView(ctx, router, msg.ChatID, msg.SenderID, statusViewChat, msg.ChatID, personaEffort, governorEffort)
	}
	chat, err := scoped.StatusChatForMessage(msg)
	if err != nil {
		return "", nil, err
	}
	pendingOnly := view == statusViewPending
	summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromChat(view, chat))
	text := telegramThreadDisplayPrefixForMessage(msg) + renderStatusChatOperatorView(chat, personaEffort, governorEffort, pendingOnly, summary)
	text = humanizeTelegramTelemetryText(text)
	rows := statusKeyboardRows(view, msg.ChatID, msg.ChatID, false, core.SystemStatusSnapshot{}, false, true)
	return text, rows, nil
}

func stopForCommand(router commandRouter, msg core.InboundMessage) core.StopResult {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedSessionRouter); ok {
			return scoped.StopForMessage(msg)
		}
	}
	return router.Stop(msg.ChatID)
}

func newForCommand(router commandRouter, msg core.InboundMessage) (core.NewSessionResult, error) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedSessionRouter); ok {
			return scoped.NewForMessage(msg)
		}
	}
	return router.New(msg.ChatID, msg.SenderID)
}

func detachForCommand(router commandRouter, msg core.InboundMessage) (core.DetachResult, error) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedSessionRouter); ok {
			return scoped.DetachForMessage(msg)
		}
	}
	return router.Detach(msg.ChatID, msg.SenderID)
}

func memoryReviewSnapshotForCommand(ctx context.Context, router commandRouter, msg core.InboundMessage, source memoryReviewSource) (memoryReviewSnapshot, error) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedMemoryRouter); ok {
			return scoped.MemoryReviewSnapshotForMessage(ctx, msg, source)
		}
	}
	return router.MemoryReviewSnapshot(ctx, msg.ChatID, msg.SenderID, source)
}

func contextSnapshotForCommand(ctx context.Context, router commandRouter, msg core.InboundMessage) (core.ContextSnapshot, error) {
	var (
		chat core.ChatStatusSnapshot
		err  error
	)
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedStatusRouter); ok {
			chat, err = scoped.StatusChatForMessage(msg)
		} else {
			chat, err = router.StatusChat(msg.ChatID)
		}
	} else {
		chat, err = router.StatusChat(msg.ChatID)
	}
	if err != nil {
		return core.ContextSnapshot{}, err
	}
	recent, err := memoryReviewSnapshotForCommand(ctx, router, msg, memoryReviewSourceSession)
	if err != nil {
		return core.ContextSnapshot{}, err
	}
	return core.ContextSnapshot{
		GeneratedAt: recent.GeneratedAt,
		Chat:        chat,
		Recent:      recent.Items,
	}, nil
}
