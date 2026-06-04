//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	mediaThreadPickerPageSize = 6
	mediaThreadPickerSurface  = "media_thread_picker"
	mediaThreadPickPrefix     = "mtpick:"
)

type mediaThreadPickerRouter interface {
	TelegramThreads(chatID int64) ([]session.TelegramThread, error)
	StartTelegramThreadTarget(ctx context.Context, msg core.InboundMessage, text string) (core.InboundMessage, session.TelegramThread, error)
	RouteAccepted(ctx context.Context, msg core.InboundMessage) error
	RecordTelegramMediaThreadPicker(chatID int64, pickerMessageID int64, inbound core.InboundMessage) error
	TelegramMediaThreadPicker(chatID int64, pickerMessageID int64) (core.InboundMessage, bool, error)
	MarkTelegramMediaThreadPickerRouted(chatID int64, pickerMessageID int64) error
}

func maybeAskTelegramMediaThreadPicker(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if len(msg.Artifacts) == 0 || msg.TelegramThreadID > 0 || strings.TrimSpace(msg.DurableAgentID) != "" {
		return false, nil
	}
	if msg.ReplyTo != nil && *msg.ReplyTo > 0 {
		return false, nil
	}
	picker, ok := router.(mediaThreadPickerRouter)
	if !ok {
		return false, nil
	}
	threads, err := picker.TelegramThreads(msg.ChatID)
	if err != nil {
		return true, err
	}
	open := make([]session.TelegramThread, 0, len(threads))
	for _, th := range threads {
		if th.Open() {
			open = append(open, th)
		}
	}
	text, rows := renderMediaThreadPicker(msg, open, 0)
	mid, err := sender.SendInlineKeyboard(ctx, msg.ChatID, text, rows, replyToMessageID(msg.MessageID))
	if err != nil {
		return true, err
	}
	if err := picker.RecordTelegramMediaThreadPicker(msg.ChatID, mid, msg); err != nil {
		return true, err
	}
	return true, nil
}

func handleTelegramMediaThreadPickerCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	picker, ok := router.(mediaThreadPickerRouter)
	if !ok {
		return false, nil
	}
	data := strings.TrimSpace(cb.Data)
	if !strings.HasPrefix(data, mediaThreadPickPrefix) {
		return false, nil
	}
	chatID, messageID := callbackChatMessage(cb)
	if chatID == 0 || messageID == 0 {
		return true, sender.AnswerCallbackQuery(ctx, cb.ID, "Missing message context.")
	}
	parts := strings.Split(data, ":")
	if len(parts) < 2 {
		return true, sender.AnswerCallbackQuery(ctx, cb.ID, "Invalid picker action.")
	}
	threads, err := picker.TelegramThreads(chatID)
	if err != nil {
		return true, err
	}
	open := make([]session.TelegramThread, 0, len(threads))
	for _, th := range threads {
		if th.Open() {
			open = append(open, th)
		}
	}
	switch parts[1] {
	case "page":
		page := 0
		if len(parts) > 2 {
			page, _ = strconv.Atoi(parts[2])
		}
		text, rows := renderMediaThreadPicker(core.InboundMessage{}, open, page)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows)
	case "new":
		msg, ok, err := picker.TelegramMediaThreadPicker(chatID, messageID)
		if err != nil {
			return true, err
		}
		if !ok {
			return true, sender.AnswerCallbackQuery(ctx, cb.ID, "Original media message is unavailable.")
		}
		msg.Text = firstNonEmpty(strings.TrimSpace(msg.Text), "Process this media.")
		routed, thread, err := picker.StartTelegramThreadTarget(ctx, msg, msg.Text)
		if err != nil {
			return true, err
		}
		if err := picker.RouteAccepted(ctx, routed); err != nil {
			return true, err
		}
		if err := picker.MarkTelegramMediaThreadPickerRouted(chatID, messageID); err != nil {
			return true, err
		}
		_ = sender.AnswerCallbackQuery(ctx, cb.ID, "Sent to new thread.")
		return true, editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, fmt.Sprintf("Media routed to new thread %d.", thread.DisplaySlot))
	case "thread":
		if len(parts) < 3 {
			return true, sender.AnswerCallbackQuery(ctx, cb.ID, "Invalid thread choice.")
		}
		threadID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || threadID <= 0 {
			return true, sender.AnswerCallbackQuery(ctx, cb.ID, "Invalid thread choice.")
		}
		msg, ok, err := picker.TelegramMediaThreadPicker(chatID, messageID)
		if err != nil {
			return true, err
		}
		if !ok {
			return true, sender.AnswerCallbackQuery(ctx, cb.ID, "Original media message is unavailable.")
		}
		msg.TelegramThreadID = threadID
		if err := picker.RouteAccepted(ctx, msg); err != nil {
			return true, err
		}
		if err := picker.MarkTelegramMediaThreadPickerRouted(chatID, messageID); err != nil {
			return true, err
		}
		_ = sender.AnswerCallbackQuery(ctx, cb.ID, "Media routed.")
		return true, editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, fmt.Sprintf("Media routed to thread %d.", visibleThreadSlot(open, threadID)))
	}
	return true, sender.AnswerCallbackQuery(ctx, cb.ID, "Unknown picker action.")
}

func renderMediaThreadPicker(msg core.InboundMessage, open []session.TelegramThread, page int) (string, [][]telegram.InlineButton) {
	if page < 0 {
		page = 0
	}
	start := page * mediaThreadPickerPageSize
	if start > len(open) {
		page = 0
		start = 0
	}
	end := start + mediaThreadPickerPageSize
	if end > len(open) {
		end = len(open)
	}
	text := "Which open thread should process this media?"
	rows := [][]telegram.InlineButton{}
	for _, th := range open[start:end] {
		label := fmt.Sprintf("Thread %d", th.DisplaySlot)
		if strings.TrimSpace(th.ArchivedDisplayName) != "" {
			label += ": " + compactButtonText(th.ArchivedDisplayName, 32)
		}
		rows = append(rows, []telegram.InlineButton{{Text: label, CallbackData: fmt.Sprintf("%sthread:%d", mediaThreadPickPrefix, th.ThreadID)}})
	}
	nav := []telegram.InlineButton{}
	if page > 0 {
		nav = append(nav, telegram.InlineButton{Text: "◀ Prev", CallbackData: fmt.Sprintf("%spage:%d", mediaThreadPickPrefix, page-1)})
	}
	if end < len(open) {
		nav = append(nav, telegram.InlineButton{Text: "Next ▶", CallbackData: fmt.Sprintf("%spage:%d", mediaThreadPickPrefix, page+1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []telegram.InlineButton{{Text: "Create new thread", CallbackData: mediaThreadPickPrefix + "new"}})
	_ = msg
	return text, rows
}

func visibleThreadSlot(open []session.TelegramThread, threadID int64) int64 {
	for _, th := range open {
		if th.ThreadID == threadID {
			return th.DisplaySlot
		}
	}
	return threadID
}
func compactButtonText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
func callbackChatMessage(cb telegram.CallbackQuery) (int64, int64) {
	if cb.Message == nil || cb.Message.Chat == nil {
		return 0, 0
	}
	return cb.Message.Chat.ID, cb.Message.MessageID
}
