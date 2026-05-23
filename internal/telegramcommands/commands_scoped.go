//go:build linux

package telegramcommands

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

func renderStatusCommand(ctx context.Context, router commandRouter, msg core.InboundMessage, personaEffort string, governorEffort string) (string, [][]telegram.InlineButton, error) {
	if msg.TelegramThreadID <= 0 {
		return renderStatusView(ctx, router, msg.ChatID, msg.SenderID, statusViewChat, msg.ChatID, personaEffort, governorEffort)
	}
	scoped, ok := router.(commandScopedStatusRouter)
	if !ok {
		return renderStatusView(ctx, router, msg.ChatID, msg.SenderID, statusViewChat, msg.ChatID, personaEffort, governorEffort)
	}
	chat, err := scoped.StatusChatForMessage(msg)
	if err != nil {
		return "", nil, err
	}
	rawText := face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
	summary := statusReadableSummaryText(ctx, router, statusViewChat, rawText)
	text := telegramThreadDisplayPrefixForMessage(msg) + renderStatusChatOperatorView(chat, personaEffort, governorEffort, false, summary)
	text = humanizeTelegramTelemetryText(text)
	rows := statusKeyboardRows(statusViewChat, msg.ChatID, msg.ChatID, router.CanRestart(msg.SenderID), core.SystemStatusSnapshot{}, false)
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

func memoryFocusForCommand(router commandRouter, msg core.InboundMessage) (core.MemoryFocus, bool) {
	if msg.TelegramThreadID > 0 {
		if scoped, ok := router.(commandScopedMemoryRouter); ok {
			return scoped.MemoryFocusForMessage(msg)
		}
	}
	return router.MemoryFocus(msg.ChatID)
}
