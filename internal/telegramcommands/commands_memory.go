//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

type memoryReviewSource = core.MemoryReviewSource
type memoryReviewSnapshot = core.MemoryReviewSnapshot
type memoryReviewItem = core.MemoryReviewItem

const (
	memoryReviewSourceSession memoryReviewSource = core.MemoryReviewSourceSessionRecent
	memoryReviewSourceShared  memoryReviewSource = core.MemoryReviewSourceSemanticShared
	memoryReviewSourceLocal   memoryReviewSource = core.MemoryReviewSourceSemanticLocal
	memoryCallbackPrefix                         = "memory:"
	staleMemoryCallbackText                      = "This memory action is no longer active. Run /memory again."
)

type memoryReviewCallbackAction string

const (
	memoryReviewActionView    memoryReviewCallbackAction = "view"
	memoryReviewActionRefresh memoryReviewCallbackAction = "refresh"
	memoryReviewActionFocus   memoryReviewCallbackAction = "focus"
	memoryReviewActionClear   memoryReviewCallbackAction = "clear"
)

func encodeMemoryReviewCallbackData(action memoryReviewCallbackAction, source memoryReviewSource, index int) string {
	sourceToken := memoryReviewSourceToken(source)
	switch action {
	case memoryReviewActionFocus:
		return fmt.Sprintf("%s%s:%s:%d", memoryCallbackPrefix, action, sourceToken, index)
	case memoryReviewActionView, memoryReviewActionRefresh, memoryReviewActionClear:
		return fmt.Sprintf("%s%s:%s", memoryCallbackPrefix, action, sourceToken)
	default:
		return ""
	}
}

func decodeMemoryReviewCallbackData(data string) (memoryReviewCallbackAction, memoryReviewSource, int, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, memoryCallbackPrefix) {
		return "", memoryReviewSourceSession, 0, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, memoryCallbackPrefix))
	if payload == "" {
		return "", memoryReviewSourceSession, 0, false
	}
	parts := strings.Split(payload, ":")
	if len(parts) < 2 {
		return "", memoryReviewSourceSession, 0, false
	}
	action := memoryReviewCallbackAction(strings.ToLower(strings.TrimSpace(parts[0])))
	source, ok := decodeMemoryReviewSourceToken(parts[1])
	if !ok {
		return "", memoryReviewSourceSession, 0, false
	}
	switch action {
	case memoryReviewActionView, memoryReviewActionRefresh, memoryReviewActionClear:
		return action, source, 0, true
	case memoryReviewActionFocus:
		if len(parts) != 3 {
			return "", memoryReviewSourceSession, 0, false
		}
		index, err := strconv.Atoi(strings.TrimSpace(parts[2]))
		if err != nil || index <= 0 {
			return "", memoryReviewSourceSession, 0, false
		}
		return action, source, index, true
	default:
		return "", memoryReviewSourceSession, 0, false
	}
}

func memoryReviewSourceToken(source memoryReviewSource) string {
	switch core.NormalizeMemoryReviewSource(string(source)) {
	case memoryReviewSourceShared:
		return "shared"
	case memoryReviewSourceLocal:
		return "local"
	default:
		return "session"
	}
}

func decodeMemoryReviewSourceToken(token string) (memoryReviewSource, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "session":
		return memoryReviewSourceSession, true
	case "shared":
		return memoryReviewSourceShared, true
	case "local":
		return memoryReviewSourceLocal, true
	default:
		return memoryReviewSourceSession, false
	}
}

func renderMemoryReviewPanel(snapshot memoryReviewSnapshot, focus core.MemoryFocus) (string, [][]telegram.InlineButton) {
	snapshot.Source = core.NormalizeMemoryReviewSource(string(snapshot.Source))
	if snapshot.Source == "" {
		snapshot.Source = memoryReviewSourceSession
	}
	state := fmt.Sprintf("%d candidate(s)", len(snapshot.Items))
	next := "Switch source, refresh, or choose a focus candidate."
	details := make([]string, 0, 4+len(snapshot.Items)*2)
	if focus.Active() {
		state += "; focus active"
		details = append(details, "Active Focus: "+firstNonEmpty(strings.TrimSpace(focus.Label), "selected item"))
		if excerpt := strings.TrimSpace(focus.Excerpt); excerpt != "" {
			details = append(details, "Focus excerpt: "+truncateOperatorLine(excerpt, 220))
		}
		next = "Clear focus, refresh candidates, or keep chatting with the current focus injected as bounded context."
	} else {
		details = append(details, "Active Focus: none")
	}
	if len(snapshot.Items) == 0 {
		details = append(details, "No memory candidates found for this source yet.")
	} else {
		details = append(details, "Candidates:")
		for idx, item := range snapshot.Items {
			details = append(details, fmt.Sprintf("%d. %s", idx+1, firstNonEmpty(strings.TrimSpace(item.Label), item.ID)))
			if excerpt := strings.TrimSpace(item.Excerpt); excerpt != "" {
				details = append(details, truncateOperatorLine(excerpt, 220))
			}
		}
	}
	evidence := []string{
		"Source: " + memoryReviewSourceDisplay(snapshot.Source),
		"Query seed: " + firstNonEmpty(strings.TrimSpace(snapshot.Query), "-"),
	}

	rows := [][]telegram.InlineButton{
		{
			{Text: "Session", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionView, memoryReviewSourceSession, 0)},
			{Text: "Semantic Shared", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionView, memoryReviewSourceShared, 0)},
			{Text: "Semantic Local", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionView, memoryReviewSourceLocal, 0)},
		},
	}
	if len(snapshot.Items) > 0 {
		focusRow := make([]telegram.InlineButton, 0, 3)
		limit := len(snapshot.Items)
		if limit > 3 {
			limit = 3
		}
		for i := 1; i <= limit; i++ {
			focusRow = append(focusRow, telegram.InlineButton{
				Text:         fmt.Sprintf("Focus %d", i),
				CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionFocus, snapshot.Source, i),
			})
		}
		rows = append(rows, focusRow)
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "Clear Focus", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionClear, snapshot.Source, 0)},
		{Text: "Refresh", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionRefresh, snapshot.Source, 0)},
	})
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:    "Memory Review",
		State:    state,
		Why:      "A selected focus is injected into later non-command turns as bounded context.",
		Next:     next,
		Details:  details,
		Evidence: evidence,
	}, false), rows
}

func memoryReviewSourceDisplay(source memoryReviewSource) string {
	switch core.NormalizeMemoryReviewSource(string(source)) {
	case memoryReviewSourceShared:
		return "semantic shared memory"
	case memoryReviewSourceLocal:
		return "semantic local memory"
	default:
		return "recent session"
	}
}

func handleMemoryReviewCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action memoryReviewCallbackAction, source memoryReviewSource, index int) (bool, error) {
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	chatID := targetMsg.ChatID
	messageID := targetMsg.MessageID
	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleMemoryCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}

	source = core.NormalizeMemoryReviewSource(string(source))
	if source == "" {
		source = memoryReviewSourceSession
	}

	switch action {
	case memoryReviewActionClear:
		if targetMsg.TelegramThreadID > 0 {
			if scoped, ok := router.(interface {
				ClearMemoryFocusForMessage(core.InboundMessage) bool
			}); ok {
				scoped.ClearMemoryFocusForMessage(targetMsg)
			} else {
				router.ClearMemoryFocus(chatID)
			}
		} else {
			router.ClearMemoryFocus(chatID)
		}
	case memoryReviewActionFocus:
		snapshot, err := memoryReviewSnapshotForCommand(ctx, router, targetMsg, source)
		if err != nil {
			return true, err
		}
		if index <= 0 || index > len(snapshot.Items) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleMemoryCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		item := snapshot.Items[index-1]
		focus := core.MemoryFocus{
			Source:  snapshot.Source,
			ItemID:  strings.TrimSpace(item.ID),
			Label:   strings.TrimSpace(item.Label),
			Excerpt: strings.TrimSpace(item.Excerpt),
			Query:   strings.TrimSpace(snapshot.Query),
			SetAt:   time.Now().UTC(),
		}
		if targetMsg.TelegramThreadID > 0 {
			if scoped, ok := router.(interface {
				SetMemoryFocusForMessage(core.InboundMessage, core.MemoryFocus)
			}); ok {
				scoped.SetMemoryFocusForMessage(targetMsg, focus)
			} else {
				router.SetMemoryFocus(chatID, focus)
			}
		} else {
			router.SetMemoryFocus(chatID, focus)
		}
	case memoryReviewActionRefresh, memoryReviewActionView:
	default:
		return true, nil
	}

	snapshot, err := memoryReviewSnapshotForCommand(ctx, router, targetMsg, source)
	if err != nil {
		return true, err
	}
	focus, _ := memoryFocusForCommand(router, targetMsg)
	text, rows := renderMemoryReviewPanel(snapshot, focus)
	text = telegramThreadDisplayPrefixForMessage(targetMsg) + text
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
		return true, err
	}
	return true, nil
}
