//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		b.WriteString("**Absorbed Threads**\n")
	} else {
		b.WriteString("**On Threads**\n")
		b.WriteString("Thread 0 is the main chat.\n\n")
		b.WriteString("Start a side thread with:\n")
		b.WriteString("/thread <message>\n\n")
		b.WriteString("Continue one by replying to its messages, or by saying:\n")
		b.WriteString("(thread N) <message>\n\n")
		b.WriteString("Use **Analyze** to understand what the open threads are doing.\n")
		b.WriteString("Open a thread below to decide whether to **Promote** it or **Absorb** it.\n\n")
		b.WriteString("**Open Threads**\n")
	}
	if len(threads) == 0 {
		if view == telegramPageViewNonOpen {
			b.WriteString("\nNo absorbed side threads.")
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
		label := telegramThreadDisplayLabel(thread)
		if view == telegramPageViewNonOpen {
			status := strings.TrimSpace(string(thread.Status))
			if status == "" {
				status = "unknown"
			}
			fmt.Fprintf(&b, "%s: %s", label, status)
		} else {
			fmt.Fprintf(&b, "%s:", label)
		}
		if preview := compactThreadPreview(thread.CreatedText); preview != "" {
			fmt.Fprintf(&b, " *%s*", preview)
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
			Text:         "Analyze",
			CallbackData: telegramThreadSummaryCallbackData,
		}})
	}
	if view != telegramPageViewNonOpen {
		var threadRow []telegram.InlineButton
		for _, thread := range threads[info.Start:info.End] {
			if !thread.Open() {
				continue
			}
			operatorID := telegramThreadOperatorID(thread)
			threadRow = append(threadRow, telegram.InlineButton{
				Text:         fmt.Sprintf("%d", operatorID),
				CallbackData: encodeTelegramThreadDetailCallback(thread.ThreadID),
			})
			if len(threadRow) == 6 {
				rows = append(rows, threadRow)
				threadRow = nil
			}
		}
		if len(threadRow) > 0 {
			rows = append(rows, threadRow)
		}
	}
	rows = append(rows, telegramPageNavigationRows(info, telegramPageSurfaceThreads, view)...)
	if view == telegramPageViewNonOpen {
		rows = append(rows, []telegram.InlineButton{{Text: "Show open", CallbackData: encodeTelegramPageCallbackData(telegramPageSurfaceThreads, telegramPageViewList, 1)}})
	} else if telegramThreadsHasNonOpen(allThreads) {
		rows = append(rows, []telegram.InlineButton{{Text: "Show absorbed", CallbackData: encodeTelegramPageCallbackData(telegramPageSurfaceThreads, telegramPageViewNonOpen, 1)}})
	}
	return rows
}
func renderTelegramThreadDetail(thread session.TelegramThread) string {
	return renderTelegramThreadDetailAt(thread, time.Now().UTC())
}
func renderTelegramThreadDetailAt(thread session.TelegramThread, now time.Time) string {
	operatorID := telegramThreadOperatorID(thread)
	preview := compactThreadPreview(thread.CreatedText)
	if preview == "" {
		preview = "No opening message recorded."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Thread %d**\n", operatorID)
	fmt.Fprintf(&b, "*%s*\n\n", preview)
	if lastActive := telegramThreadLastActiveAt(thread); !lastActive.IsZero() {
		fmt.Fprintf(&b, "Last active: %s\n", formatTelegramThreadDetailTime(lastActive))
		fmt.Fprintf(&b, "%s\n\n", formatTelegramThreadRelativeTime(lastActive, now))
	} else {
		b.WriteString("Last active: unknown\n\n")
	}
	b.WriteString("**Promote**\n")
	b.WriteString("Turn this thread into a real work item.\n\n")
	b.WriteString("**Absorb**\n")
	b.WriteString("Fold the useful result back into the main chat and remove it from open threads.")
	return b.String()
}
func telegramThreadLastActiveAt(thread session.TelegramThread) time.Time {
	if !thread.LastActivityAt.IsZero() {
		return thread.LastActivityAt.UTC()
	}
	if !thread.CreatedAt.IsZero() {
		return thread.CreatedAt.UTC()
	}
	if !thread.UpdatedAt.IsZero() {
		return thread.UpdatedAt.UTC()
	}
	return time.Time{}
}
func formatTelegramThreadDetailTime(t time.Time) string {
	return t.UTC().Format("Jan 2, 2006, 3:04 PM UTC")
}
func formatTelegramThreadRelativeTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	delta := now.UTC().Sub(t.UTC())
	future := false
	if delta < 0 {
		future = true
		delta = -delta
	}
	if delta < time.Minute {
		if future {
			return "moments from now"
		}
		return "just now"
	}
	value := int64(delta / time.Minute)
	unit := "minute"
	if delta >= 48*time.Hour {
		value = int64(delta / (24 * time.Hour))
		unit = "day"
	} else if delta >= 2*time.Hour {
		value = int64(delta / time.Hour)
		unit = "hour"
	}
	if value != 1 {
		unit += "s"
	}
	if future {
		return fmt.Sprintf("in %d %s", value, unit)
	}
	return fmt.Sprintf("%d %s ago", value, unit)
}
func telegramThreadDetailRows(thread session.TelegramThread) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Promote", CallbackData: encodeTelegramThreadPromoteCallback(thread.ThreadID)},
		{Text: "Absorb", CallbackData: encodeTelegramThreadAbsorbCallback(thread.ThreadID)},
		{Text: "Back", CallbackData: telegramThreadBackCallbackData},
	}}
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
