//go:build linux

package telegramcommands

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	telegramThreadCallbackPrefix      = "thread_absorb:"
	telegramThreadSummaryCallbackData = "thread_summary"
	telegramThreadsPageSize           = 6
)

var telegramThreadPrefixPattern = regexp.MustCompile(`(?is)^\(\s*thread\s+([0-9]+)\s*\)\s*`)

type commandThreadRouter interface {
	CreateTelegramThread(ctx context.Context, msg core.InboundMessage) (session.TelegramThread, error)
	StartTelegramThreadTarget(ctx context.Context, msg core.InboundMessage, text string) (core.InboundMessage, session.TelegramThread, error)
	RecordTelegramThreadGuideMessage(chatID int64, threadID int64, messageID int64) error
	TargetTelegramThreadMessage(ctx context.Context, msg core.InboundMessage, threadID int64, text string) (core.InboundMessage, session.TelegramThread, error)
	TelegramThread(chatID int64, threadID int64) (session.TelegramThread, bool, error)
	TelegramThreadForReplyMessage(chatID int64, replyMessageID int64) (session.TelegramThread, bool, error)
	TelegramThreads(chatID int64) ([]session.TelegramThread, error)
	QueueTelegramThreadSummary(ctx context.Context, msg core.InboundMessage) (string, error)
	AbsorbTelegramThread(ctx context.Context, chatID int64, senderID int64, threadID int64) (string, error)
}

type commandThreadCallbackRecorder interface {
	RecordTelegramThreadCallbackMessage(chatID int64, threadID int64, messageID int64, surface string) error
}

type telegramThreadUserError string

func (e telegramThreadUserError) Error() string {
	return string(e)
}

func handleTelegramThreadCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, command string) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return sendTelegramThreadText(ctx, sender, msg, "Thread controls are unavailable.")
	}
	switch command {
	case "thread":
		text := strings.TrimSpace(telegramCommandArgs(msg.Text))
		if text == "" {
			thread, err := threadRouter.CreateTelegramThread(ctx, msg)
			if err != nil {
				if isTelegramThreadUserError(err) {
					return sendTelegramThreadText(ctx, sender, msg, err.Error())
				}
				return true, err
			}
			return sendTelegramThreadGuide(ctx, sender, threadRouter, msg, thread)
		}
		return false, nil
	case "threads":
		view := telegramThreadsViewFromArgs(telegramCommandArgs(msg.Text))
		threads, err := threadRouter.TelegramThreads(msg.ChatID)
		if err != nil {
			return true, err
		}
		return sendTelegramThreadsPanel(ctx, sender, msg, threads, view)
	case "absorb":
		threadID, ok := parseTelegramThreadIDArg(telegramCommandArgs(msg.Text))
		if !ok {
			threads, err := threadRouter.TelegramThreads(msg.ChatID)
			if err != nil {
				return true, err
			}
			return sendTelegramThreadsPanel(ctx, sender, msg, threads, telegramPageViewList)
		}
		threadID, err := resolveTelegramThreadTargetID(threadRouter, msg.ChatID, threadID)
		if err != nil {
			return true, err
		}
		text, err := threadRouter.AbsorbTelegramThread(ctx, msg.ChatID, msg.SenderID, threadID)
		if err != nil {
			if isTelegramThreadUserError(err) {
				return sendTelegramThreadText(ctx, sender, msg, err.Error())
			}
			return true, err
		}
		return sendTelegramThreadText(ctx, sender, msg, text)
	default:
		return false, nil
	}
}

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

func recordTelegramThreadCallbackMessage(router commandRouter, chatID int64, threadID int64, messageID int64, surface string) error {
	if threadID <= 0 || messageID <= 0 {
		return nil
	}
	recorder, ok := router.(commandThreadCallbackRecorder)
	if !ok {
		return nil
	}
	return recorder.RecordTelegramThreadCallbackMessage(chatID, threadID, messageID, surface)
}

func resolveTelegramThreadStartCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (core.InboundMessage, bool, bool, error) {
	if msg.TelegramThreadID > 0 || strings.TrimSpace(msg.DurableAgentID) != "" {
		return msg, false, false, nil
	}
	command, ok := parseTelegramCommand(msg.Text)
	if !ok || command != "thread" {
		return msg, false, false, nil
	}
	text := strings.TrimSpace(telegramCommandArgs(msg.Text))
	if text == "" {
		return msg, false, false, nil
	}
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		_, err := sendTelegramThreadText(ctx, sender, msg, "Thread controls are unavailable.")
		return msg, false, true, err
	}
	routed, _, err := threadRouter.StartTelegramThreadTarget(ctx, msg, text)
	if err != nil {
		if isTelegramThreadUserError(err) {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, err.Error())
			return msg, false, true, sendErr
		}
		return msg, false, true, err
	}
	return routed, true, false, nil
}

func resolveTelegramThreadPrefix(ctx context.Context, sender commandSender, router commandThreadRouter, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	threadID, text, ok := parseTelegramThreadPrefix(msg.Text)
	if !ok {
		return msg, false, nil
	}
	threadID, err := resolveTelegramThreadTargetID(router, msg.ChatID, threadID)
	if err != nil {
		return msg, true, err
	}
	if strings.TrimSpace(text) == "" {
		_, err := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Add a message after `(thread %d)`.", threadID))
		return msg, true, err
	}
	if command, ok := parseTelegramCommand(text); ok {
		if !telegramThreadLaneCommand(command) {
			_, err := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("/%s is a global command. Run it without `(thread %d)`.", command, threadID))
			return msg, true, err
		}
		thread, ok, err := router.TelegramThread(msg.ChatID, threadID)
		if err != nil {
			return msg, true, err
		}
		if !ok {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
			return msg, true, sendErr
		}
		if !thread.Open() {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", threadID))
			return msg, true, sendErr
		}
		routed := msg
		routed.TelegramThreadID = threadID
		routed.Text = text
		return routed, false, nil
	}
	routed, _, err := router.TargetTelegramThreadMessage(ctx, msg, threadID, text)
	if err != nil {
		if isTelegramThreadUserError(err) {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, err.Error())
			return msg, true, sendErr
		}
		return msg, true, err
	}
	return routed, false, nil
}

func telegramThreadLaneCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "status", "stop", "new", "detach", "memory":
		return true
	default:
		return false
	}
}

func resolveTelegramThreadCommandTarget(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, command string) (core.InboundMessage, bool, error) {
	if msg.TelegramThreadID > 0 || !telegramThreadLaneCommand(command) || msg.ReplyTo == nil || *msg.ReplyTo <= 0 {
		return msg, false, nil
	}
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return msg, false, nil
	}
	thread, ok, err := threadRouter.TelegramThreadForReplyMessage(msg.ChatID, *msg.ReplyTo)
	if err != nil {
		return msg, true, err
	}
	if !ok {
		return msg, false, nil
	}
	if !thread.Open() {
		_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", thread.ThreadID))
		return msg, true, sendErr
	}
	routed := msg
	routed.TelegramThreadID = thread.ThreadID
	return routed, false, nil
}

func resolveTelegramThreadReply(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	if msg.TelegramThreadID > 0 || msg.ReplyTo == nil || *msg.ReplyTo <= 0 || strings.TrimSpace(msg.DurableAgentID) != "" {
		return msg, false, nil
	}
	if _, ok := parseTelegramCommand(msg.Text); ok {
		return msg, false, nil
	}
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return msg, false, nil
	}
	thread, ok, err := threadRouter.TelegramThreadForReplyMessage(msg.ChatID, *msg.ReplyTo)
	if err != nil {
		return msg, true, err
	}
	if !ok {
		return msg, false, nil
	}
	if !thread.Open() {
		_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", thread.ThreadID))
		return msg, true, sendErr
	}
	routed := msg
	routed.TelegramThreadID = thread.ThreadID
	return routed, false, nil
}

func parseTelegramThreadPrefix(text string) (int64, string, bool) {
	matches := telegramThreadPrefixPattern.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 2 {
		return 0, "", false
	}
	threadID, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil || threadID <= 0 {
		return 0, "", false
	}
	rest := telegramThreadPrefixPattern.ReplaceAllString(strings.TrimSpace(text), "")
	return threadID, strings.TrimSpace(rest), true
}

func parseTelegramThreadIDArg(raw string) (int64, bool) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return 0, false
	}
	threadID, err := strconv.ParseInt(fields[0], 10, 64)
	return threadID, err == nil && threadID > 0
}

func sendTelegramThreadText(ctx context.Context, sender commandSender, msg core.InboundMessage, text string) (bool, error) {
	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    strings.TrimSpace(text),
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	return true, err
}

func renderTelegramThreadGuide(threadID int64) string {
	return fmt.Sprintf("Thread %d created.\n\nSend work here with:\n(thread %d) create the inbox child\n\nYou can also reply to side-thread messages. Main chat remains thread 0. Close this thread with /absorb %d.", threadID, threadID, threadID)
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
	b.WriteString("Default chat is thread 0. Start a side thread with `/thread <message>`. Reply to side-thread messages or use `(thread N) <message>`. Close one with `/absorb N`.\n")
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
		rows = append(rows, []telegram.InlineButton{{
			Text:         fmt.Sprintf("Absorb %d", operatorID),
			CallbackData: encodeTelegramThreadAbsorbCallback(thread.ThreadID),
		}})
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

func encodeTelegramThreadAbsorbCallback(threadID int64) string {
	if threadID <= 0 {
		return ""
	}
	return telegramThreadCallbackPrefix + strconv.FormatInt(threadID, 10)
}

func decodeTelegramThreadAbsorbCallback(data string) (int64, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, telegramThreadCallbackPrefix) {
		return 0, false
	}
	threadID, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(trimmed, telegramThreadCallbackPrefix)), 10, 64)
	return threadID, err == nil && threadID > 0
}

func decodeTelegramThreadSummaryCallback(data string) bool {
	return strings.TrimSpace(data) == telegramThreadSummaryCallbackData
}

func handleTelegramThreadSummaryCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread controls are unavailable.")
	}
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	messageID := callbackMessageID(cb)
	if chatID == 0 || senderID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	text, err := threadRouter.QueueTelegramThreadSummary(ctx, core.InboundMessage{
		ChatID:          chatID,
		SenderID:        senderID,
		MessageID:       messageID,
		IngressSurface:  telegramThreadSummaryIngressSurface,
		IngressUpdateID: cb.UpdateID,
		Text:            "/threads summarize",
	})
	if err != nil {
		if isTelegramThreadUserError(err) {
			text = err.Error()
		} else {
			return true, err
		}
	}
	if strings.TrimSpace(text) == "" {
		text = "Summary queued."
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	return true, nil
}

func handleTelegramThreadCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, threadID int64) (bool, error) {
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return true, sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Thread controls are unavailable.")
	}
	chatID := callbackChatID(cb)
	senderID := callbackSenderID(cb)
	messageID := callbackMessageID(cb)
	if chatID == 0 || senderID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Absorbing."); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}
	text, err := threadRouter.AbsorbTelegramThread(ctx, chatID, senderID, threadID)
	if err != nil {
		if isTelegramThreadUserError(err) {
			text = err.Error()
		} else {
			return true, err
		}
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		return true, err
	}
	return true, nil
}

func isTelegramThreadUserError(err error) bool {
	var userErr telegramThreadUserError
	return errors.As(err, &userErr)
}
