//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

func renderStatusView(ctx context.Context, router commandRouter, currentChatID int64, senderID int64, view statusView, targetChatID int64, personaEffort string, governorEffort string) (string, [][]telegram.InlineButton, error) {
	if router == nil {
		return "", nil, fmt.Errorf("status router is unavailable")
	}
	isAdmin := router.CanRestart(senderID)
	if view == "" {
		view = statusViewChat
	}
	if targetChatID == 0 {
		targetChatID = currentChatID
	}

	var (
		text         string
		systemStatus core.SystemStatusSnapshot
		systemLoaded bool
	)

	switch view {
	case statusViewChat:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromChat(view, chat))
		text = renderStatusChatOperatorView(chat, personaEffort, governorEffort, false, summary)
	case statusViewPending:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromChat(view, chat))
		text = renderStatusChatOperatorView(chat, personaEffort, governorEffort, true, summary)
	case statusViewChatTarget:
		chat, err := router.StatusChat(targetChatID)
		if err != nil {
			return "", nil, err
		}
		summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromChat(view, chat))
		text = renderStatusChatOperatorView(chat, personaEffort, governorEffort, false, summary)
	case statusViewSystem:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusSystem(senderID)
		if err != nil {
			return "", nil, err
		}
		systemStatus = status
		systemLoaded = true
		summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromSystem(view, status))
		text = renderReadableStatusOperatorView(face.RenderTelegramStatusSystemOperatorCard(status, personaEffort, governorEffort), summary)
	case statusViewHotChats:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusSystem(senderID)
		if err != nil {
			return "", nil, err
		}
		systemStatus = status
		systemLoaded = true
		summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromSystem(view, status))
		text = renderReadableStatusOperatorView(face.RenderTelegramStatusHotChatsOperatorCard(status), summary)
	case statusViewFindChat:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusSystem(senderID)
		if err != nil {
			return "", nil, err
		}
		systemStatus = status
		systemLoaded = true
		text = face.RenderTelegramStatusFindChatOperatorCard(status)
	case statusViewDurables:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusDurables(senderID)
		if err != nil {
			return "", nil, err
		}
		summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromDurables(status))
		text = renderReadableStatusOperatorView(face.RenderTelegramStatusDurablesOperatorCard(status), summary)
	default:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		view = statusViewChat
		summary := statusReadableSummaryText(ctx, router, statusReadableFactsFromChat(view, chat))
		text = renderStatusChatOperatorView(chat, personaEffort, governorEffort, false, summary)
	}
	text = humanizeTelegramTelemetryText(text)
	rows := statusKeyboardRows(view, currentChatID, targetChatID, isAdmin, systemStatus, systemLoaded, false)
	return text, rows, nil
}

func renderStatusChatOperatorView(chat core.ChatStatusSnapshot, personaEffort string, governorEffort string, pendingOnly bool, quickRead string) string {
	text := strings.TrimSpace(face.RenderTelegramStatusChatOperatorCard(chat, personaEffort, governorEffort, pendingOnly))
	quickRead = strings.TrimSpace(quickRead)
	if quickRead == "" {
		return text
	}
	return "Quick Read: " + quickRead + "\n\n" + text
}

func renderReadableStatusOperatorView(text string, quickRead string) string {
	text = strings.TrimSpace(text)
	quickRead = strings.TrimSpace(quickRead)
	if quickRead == "" {
		return text
	}
	return "Quick Read: " + quickRead + "\n\n" + text
}
