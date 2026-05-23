//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func sendTelegramThreadsPanel(ctx context.Context, sender commandSender, msg core.InboundMessage, threads []session.TelegramThread, view string) (bool, error) {
	rendered, rows := renderTelegramThreadsPanel(threads, view, 1)
	if len(rows) > 0 {
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	}
	return sendTelegramThreadText(ctx, sender, msg, rendered)
}
func sendTelegramThreadGuide(ctx context.Context, sender commandSender, router commandThreadRouter, msg core.InboundMessage, thread session.TelegramThread) (bool, error) {
	operatorID := telegramThreadOperatorID(thread)
	rendered := renderTelegramThreadGuide(operatorID)
	rows := [][]telegram.InlineButton{{
		{Text: fmt.Sprintf("Promote %d", operatorID), CallbackData: encodeTelegramThreadPromoteCallback(thread.ThreadID)},
		{Text: fmt.Sprintf("Absorb %d", operatorID), CallbackData: encodeTelegramThreadAbsorbCallback(thread.ThreadID)},
	}}
	messageID, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID))
	if err != nil {
		return true, err
	}
	if err := router.RecordTelegramThreadGuideMessage(msg.ChatID, thread.ThreadID, messageID); err != nil {
		return true, err
	}
	return true, nil
}
func renderTelegramThreadGuide(threadID int64) string {
	return fmt.Sprintf("Thread %d created.\n\nSend work here with:\n(thread %d) create the inbox child\n\nYou can also reply to side-thread messages. Main chat remains thread 0. Promote this thread into a draft durable handoff with Promote %d, or close it with /absorb %d.", threadID, threadID, threadID, threadID)
}
func renderTelegramThreadsHelp(threads []session.TelegramThread) string {
	rendered, _ := renderTelegramThreadsPanel(threads, telegramPageViewList, 1)
	return rendered
}
func renderTelegramThreadsPanel(threads []session.TelegramThread, view string, page int) (string, [][]telegram.InlineButton) {
	view = normalizeTelegramThreadsView(view)
	allThreads := append([]session.TelegramThread(nil), threads...)
	threads = filterTelegramThreadsForView(threads, view)
	visible, info := telegramPageItems(threads, page, telegramThreadsPageSize)
	var b strings.Builder
	if view == telegramPageViewNonOpen {
		b.WriteString("Threads — non-open\n")
	} else {
		b.WriteString("Threads — open\n")
	}
	b.WriteString("Default chat is thread 0. Start a side thread with `/thread <message>`. Reply to side-thread messages or use `(thread N) <message>`. Promote one into a draft handoff with Promote, or close one with `/absorb N`.\n")
	if len(threads) == 0 {
		if view == telegramPageViewNonOpen {
			b.WriteString("\nNo non-open side threads.")
		} else {
			b.WriteString("\nNo open side threads.")
		}
		return b.String(), nil
	}
	if info.PageCount > 1 {
		fmt.Fprintf(&b, "\nPage %d of %d. Showing %d-%d of %d.\n", info.Page, info.PageCount, info.Start+1, info.End, info.Total)
	}
	b.WriteString("\n")
	for _, thread := range visible {
		status := strings.TrimSpace(string(thread.Status))
		if status == "" {
			status = "unknown"
		}
		label := telegramThreadDisplayLabel(thread)
		fmt.Fprintf(&b, "- %s: %s", label, status)
		if preview := compactThreadPreview(thread.CreatedText); preview != "" {
			fmt.Fprintf(&b, " - %s", preview)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), telegramThreadsRowsPage(threads, allThreads, view, info)
}
func compactThreadPreview(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= 64 {
		return text
	}
	return strings.TrimSpace(string(runes[:61])) + "..."
}
func telegramThreadsRows(threads []session.TelegramThread) [][]telegram.InlineButton {
	threads = filterTelegramThreadsForView(threads, telegramPageViewList)
	_, info := telegramPageItems(threads, 1, telegramThreadsPageSize)
	return telegramThreadsRowsPage(threads, threads, telegramPageViewList, info)
}
func telegramThreadsRowsPage(threads []session.TelegramThread, allThreads []session.TelegramThread, view string, info telegramPageInfo) [][]telegram.InlineButton {
	var rows [][]telegram.InlineButton
	if telegramThreadsHasOpen(threads) {
		rows = append(rows, []telegram.InlineButton{{
			Text:         "Summarize",
			CallbackData: telegramThreadSummaryCallbackData,
		}})
	}
	for _, thread := range threads[info.Start:info.End] {
		if !thread.Open() {
			continue
		}
		operatorID := telegramThreadOperatorID(thread)
		rows = append(rows, []telegram.InlineButton{
			{Text: fmt.Sprintf("Promote %d", operatorID), CallbackData: encodeTelegramThreadPromoteCallback(thread.ThreadID)},
			{Text: fmt.Sprintf("Absorb %d", operatorID), CallbackData: encodeTelegramThreadAbsorbCallback(thread.ThreadID)},
		})
	}
	if view == telegramPageViewNonOpen {
		rows = append(rows, []telegram.InlineButton{{Text: "Show open", CallbackData: encodeTelegramPageCallbackData(telegramPageSurfaceThreads, telegramPageViewList, 1)}})
	} else if telegramThreadsHasNonOpen(allThreads) {
		rows = append(rows, []telegram.InlineButton{{Text: "Show non-open", CallbackData: encodeTelegramPageCallbackData(telegramPageSurfaceThreads, telegramPageViewNonOpen, 1)}})
	}
	rows = append(rows, telegramPageNavigationRows(info, telegramPageSurfaceThreads, view)...)
	return rows
}
func telegramThreadsHasOpen(threads []session.TelegramThread) bool {
	for _, thread := range threads {
		if thread.Open() {
			return true
		}
	}
	return false
}
func telegramThreadsHasNonOpen(threads []session.TelegramThread) bool {
	for _, thread := range threads {
		if !thread.Open() {
			return true
		}
	}
	return false
}
