//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
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
		{Text: "Promote", CallbackData: encodeTelegramThreadPromoteCallback(thread.ThreadID)},
		{Text: "Absorb", CallbackData: encodeTelegramThreadAbsorbCallback(thread.ThreadID)},
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
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title: "Thread " + fmt.Sprint(threadID),
		State: "created",
		Why:   "Side threads keep parallel work in separate session lanes while the main chat remains thread 0.",
		Next:  fmt.Sprintf("Reply here or send (thread %d) with the next message. Promote for a durable handoff, or absorb when done.", threadID),
		Details: []string{
			fmt.Sprintf("Example: (thread %d) create the inbox child", threadID),
			"Replies to known side-thread messages stay in this thread.",
			"Promote drafts a durable handoff; Absorb closes the lane.",
		},
		Evidence: []string{"main chat: thread 0"},
	}, false)
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
	details := make([]string, 0, len(visible)+1)
	if view == telegramPageViewNonOpen {
		for _, thread := range visible {
			details = append(details, telegramThreadBoardLine(thread, true))
		}
	} else {
		for _, thread := range visible {
			details = append(details, telegramThreadBoardLine(thread, false))
		}
	}
	if len(threads) == 0 {
		if view == telegramPageViewNonOpen {
			details = append(details, "No absorbed side threads.")
		} else {
			details = append(details, "No open side threads.")
		}
	}
	state := fmt.Sprintf("%d shown; %d total", len(threads), len(allThreads))
	if info.PageCount > 1 {
		state = fmt.Sprintf("Page %d of %d; %d shown; %d total", info.Page, info.PageCount, len(threads), len(allThreads))
	}
	if len(threads) == 0 {
		state = "none"
	}
	panel := face.OperatorPanel{
		Title:    telegramThreadsBoardTitle(view),
		State:    state,
		Why:      "Thread 0 is the main chat; side threads keep simultaneous work in separate session lanes.",
		Next:     telegramThreadsBoardNext(view),
		Details:  details,
		Evidence: []string{"targeting: (thread N) <message>", "reply routing: durable message ledger"},
	}
	return renderTelegramCompactPanelWithLimits(panel, telegramThreadsPageSize, 2), telegramThreadsRowsPage(threads, allThreads, view, info)
}

func telegramThreadsBoardTitle(view string) string {
	if normalizeTelegramThreadsView(view) == telegramPageViewNonOpen {
		return "Absorbed Threads"
	}
	return "Side Threads"
}

func telegramThreadsBoardNext(view string) string {
	if normalizeTelegramThreadsView(view) == telegramPageViewNonOpen {
		return "Switch back to open threads when you need active lanes."
	}
	return "Start with /thread, reply to side-thread messages, or open a thread before promoting or absorbing it."
}

func telegramThreadBoardLine(thread session.TelegramThread, includeStatus bool) string {
	label := telegramThreadDisplayLabel(thread)
	preview := compactThreadPreview(thread.CreatedText)
	if preview == "" {
		preview = "No opening message recorded."
	}
	if includeStatus {
		status := strings.TrimSpace(string(thread.Status))
		if status == "" {
			status = "unknown"
		}
		return fmt.Sprintf("%s: %s; %s", label, status, preview)
	}
	return fmt.Sprintf("%s: %s", label, preview)
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
	state := strings.TrimSpace(string(thread.Status))
	if state == "" {
		state = "unknown"
	}
	details := []string{"Opening message: " + preview}
	if lastActive := telegramThreadLastActiveAt(thread); !lastActive.IsZero() {
		details = append(details,
			"Last active: "+formatTelegramThreadDetailTime(lastActive),
			"Relative time: "+formatTelegramThreadRelativeTime(lastActive, now),
		)
	} else {
		details = append(details, "Last active: unknown")
	}
	eligibility := thread.ReminderEligibility(now, session.DefaultTelegramThreadReminderPolicy())
	details = append(details, fmt.Sprintf("Reminder eligibility: %s", telegramThreadReminderEligibilityLine(eligibility)))
	details = append(details,
		"Promote: draft a durable handoff from this lane.",
		"Absorb: close the lane and record a main-chat note.",
	)
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    fmt.Sprintf("Thread %d", operatorID),
		State:    state,
		Why:      "This side thread has its own transcript, plan, progress, and recovery state.",
		Next:     "Promote it into a durable handoff, absorb it when complete, or go back to the board.",
		Details:  details,
		Evidence: []string{fmt.Sprintf("thread_id: %d", thread.ThreadID), fmt.Sprintf("display_slot: %d", operatorID)},
	}, 6, 2)
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
	return [][]telegram.InlineButton{
		{
			{Text: "Promote", CallbackData: encodeTelegramThreadPromoteCallback(thread.ThreadID)},
			{Text: "Absorb", CallbackData: encodeTelegramThreadAbsorbCallback(thread.ThreadID)},
		},
		{
			{Text: "Back", CallbackData: telegramThreadBackCallbackData},
		},
	}
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

func telegramThreadReminderEligibilityLine(eligibility session.TelegramThreadReminderEligibility) string {
	parts := []string{"reason=" + eligibility.Reason}
	if eligibility.Eligible {
		parts = append(parts, "eligible=true")
	} else {
		parts = append(parts, "eligible=false")
	}
	if eligibility.Age > 0 {
		parts = append(parts, "age="+eligibility.Age.Truncate(time.Minute).String())
	}
	if eligibility.StaleAfter > 0 {
		parts = append(parts, "threshold="+eligibility.StaleAfter.Truncate(time.Minute).String())
	}
	if strings.TrimSpace(eligibility.SummaryKind) != "" {
		parts = append(parts, "summary="+strings.TrimSpace(eligibility.SummaryKind))
	}
	return strings.Join(parts, " ")
}
