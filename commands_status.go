//go:build linux

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

const statusCallbackPrefix = "status:"
const staleStatusCallbackText = "This status action is no longer available. Run /status again."
const adminStatusOnlyText = "This status view is available to Telegram admins only."
const statusMessageChunkLimit = 3800

type statusView string

const (
	statusViewChat       statusView = "chat"
	statusViewPending    statusView = "pending"
	statusViewSystem     statusView = "system"
	statusViewHotChats   statusView = "hot"
	statusViewFindChat   statusView = "find"
	statusViewChatTarget statusView = "chat_target"
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
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
	case statusViewPending:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, true)
	case statusViewChatTarget:
		chat, err := router.StatusChat(targetChatID)
		if err != nil {
			return "", nil, err
		}
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
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
		text = face.RenderTelegramStatusSystem(status, personaEffort, governorEffort)
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
		text = face.RenderTelegramStatusHotChats(status)
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
		text = face.RenderTelegramStatusFindChat(status)
	default:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		view = statusViewChat
		text = face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
	}
	text = appendStatusReadableSummary(ctx, router, view, text)
	rows := statusKeyboardRows(view, currentChatID, targetChatID, isAdmin, systemStatus, systemLoaded)
	return text, rows, nil
}

func appendStatusReadableSummary(ctx context.Context, router commandRouter, view statusView, text string) string {
	if router == nil || !statusViewSupportsReadableSummary(view) {
		return text
	}
	summary := strings.TrimSpace(router.StatusReadableSummary(ctx, string(view), text))
	if summary == "" {
		return text
	}
	return "quick_read " + summary + "\n\n" + text
}

func statusViewSupportsReadableSummary(view statusView) bool {
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget, statusViewSystem, statusViewHotChats:
		return true
	default:
		return false
	}
}

func statusKeyboardRows(view statusView, currentChatID int64, targetChatID int64, isAdmin bool, system core.SystemStatusSnapshot, systemLoaded bool) [][]telegram.InlineButton {
	if targetChatID == 0 {
		targetChatID = currentChatID
	}
	activeView := view
	if activeView == "" {
		activeView = statusViewChat
	}

	rows := [][]telegram.InlineButton{
		{
			{Text: "This Chat", CallbackData: encodeStatusCallbackData(statusViewChat, currentChatID)},
			{Text: "Pending Only", CallbackData: encodeStatusCallbackData(statusViewPending, currentChatID)},
			{Text: "Refresh", CallbackData: encodeStatusCallbackData(activeView, targetChatID)},
		},
	}
	if isAdmin {
		rows = append(rows, []telegram.InlineButton{
			{Text: "System Overview", CallbackData: encodeStatusCallbackData(statusViewSystem, 0)},
			{Text: "Hot Chats", CallbackData: encodeStatusCallbackData(statusViewHotChats, 0)},
			{Text: "Find Chat", CallbackData: encodeStatusCallbackData(statusViewFindChat, 0)},
		})
	}
	if isAdmin && systemLoaded && view == statusViewFindChat {
		maxChats := len(system.HotChats)
		if maxChats > 12 {
			maxChats = 12
		}
		for i := 0; i < maxChats; i += 2 {
			row := make([]telegram.InlineButton, 0, 2)
			chatA := system.HotChats[i]
			row = append(row, telegram.InlineButton{
				Text:         fmt.Sprintf("Chat %d", chatA.ChatID),
				CallbackData: encodeStatusCallbackData(statusViewChatTarget, chatA.ChatID),
			})
			if i+1 < maxChats {
				chatB := system.HotChats[i+1]
				row = append(row, telegram.InlineButton{
					Text:         fmt.Sprintf("Chat %d", chatB.ChatID),
					CallbackData: encodeStatusCallbackData(statusViewChatTarget, chatB.ChatID),
				})
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func statusViewRequiresAdmin(view statusView, callbackChatID int64, targetChatID int64) bool {
	switch view {
	case statusViewSystem, statusViewHotChats, statusViewFindChat:
		return true
	case statusViewChatTarget:
		return targetChatID != 0 && (callbackChatID == 0 || targetChatID != callbackChatID)
	default:
		return false
	}
}

func encodeStatusCallbackData(view statusView, chatID int64) string {
	switch view {
	case statusViewChat:
		return statusCallbackPrefix + "chat"
	case statusViewPending:
		return statusCallbackPrefix + "pending"
	case statusViewSystem:
		return statusCallbackPrefix + "system"
	case statusViewHotChats:
		return statusCallbackPrefix + "hot"
	case statusViewFindChat:
		return statusCallbackPrefix + "find"
	case statusViewChatTarget:
		return statusCallbackPrefix + "chat:" + strconv.FormatInt(chatID, 10)
	default:
		return statusCallbackPrefix + "chat"
	}
}

func decodeStatusCallbackData(data string) (statusView, int64, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, statusCallbackPrefix) {
		return "", 0, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, statusCallbackPrefix))
	if payload == "" {
		return "", 0, false
	}
	parts := strings.Split(payload, ":")
	if len(parts) == 1 {
		switch parts[0] {
		case "chat":
			return statusViewChat, 0, true
		case "pending":
			return statusViewPending, 0, true
		case "system":
			return statusViewSystem, 0, true
		case "hot":
			return statusViewHotChats, 0, true
		case "find":
			return statusViewFindChat, 0, true
		default:
			return "", 0, false
		}
	}
	if len(parts) == 2 && parts[0] == "chat" {
		chatID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || chatID == 0 {
			return "", 0, false
		}
		return statusViewChatTarget, chatID, true
	}
	return "", 0, false
}

func deliverStatusCallbackView(ctx context.Context, sender commandCallbackSender, chatID int64, messageID int64, text string, rows [][]telegram.InlineButton) error {
	if sender == nil {
		return nil
	}
	chunks := splitStatusTextChunks(text, statusMessageChunkLimit)
	if len(chunks) == 0 {
		chunks = []string{"status_scope=chat\nsummary unavailable"}
	}
	first := chunks[0]
	if messageID != 0 {
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, first, "", rows); err != nil {
			return err
		}
	} else {
		if _, err := sender.SendInlineKeyboard(ctx, chatID, first, rows, nil); err != nil {
			return err
		}
	}
	for i := 1; i < len(chunks); i++ {
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   chunks[i],
		}); err != nil {
			return err
		}
	}
	return nil
}

func splitStatusTextChunks(text string, limit int) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if limit <= 0 {
		limit = statusMessageChunkLimit
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}
	chunks := make([]string, 0, (len(runes)/limit)+1)
	for len(runes) > 0 {
		if len(runes) <= limit {
			chunk := strings.TrimSpace(string(runes))
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
			break
		}
		cut := limit
		for i := cut; i > cut/2; i-- {
			if runes[i-1] == '\n' {
				cut = i
				break
			}
		}
		chunk := strings.TrimSpace(string(runes[:cut]))
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		runes = runes[cut:]
	}
	return chunks
}
