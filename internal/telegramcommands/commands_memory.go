//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
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
	contextCallbackPrefix                        = "context:"
	staleMemoryCallbackText                      = "This memory action is no longer active. Run /memory again."
)

type memoryReviewCallbackAction string

const (
	memoryReviewActionView    memoryReviewCallbackAction = "view"
	memoryReviewActionRefresh memoryReviewCallbackAction = "refresh"
	memoryReviewActionAsk     memoryReviewCallbackAction = "ask"
)

func encodeMemoryReviewCallbackData(action memoryReviewCallbackAction, source memoryReviewSource, index int) string {
	sourceToken := memoryReviewSourceToken(source)
	switch action {
	case memoryReviewActionView, memoryReviewActionRefresh, memoryReviewActionAsk:
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
	parts := strings.Split(payload, ":")
	if len(parts) != 2 {
		return "", memoryReviewSourceSession, 0, false
	}
	action := memoryReviewCallbackAction(strings.ToLower(strings.TrimSpace(parts[0])))
	source, ok := decodeMemoryReviewSourceToken(parts[1])
	if !ok {
		return "", memoryReviewSourceSession, 0, false
	}
	switch action {
	case memoryReviewActionView, memoryReviewActionRefresh, memoryReviewActionAsk:
		return action, source, 0, true
	default:
		return "", memoryReviewSourceSession, 0, false
	}
}

func encodeContextCallbackData(action memoryReviewCallbackAction) string {
	switch action {
	case memoryReviewActionRefresh, memoryReviewActionAsk:
		return contextCallbackPrefix + string(action)
	default:
		return ""
	}
}

func decodeContextCallbackData(data string) (memoryReviewCallbackAction, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, contextCallbackPrefix) {
		return "", false
	}
	action := memoryReviewCallbackAction(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, contextCallbackPrefix))))
	switch action {
	case memoryReviewActionRefresh, memoryReviewActionAsk:
		return action, true
	default:
		return "", false
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

func renderMemoryReviewPanel(snapshot memoryReviewSnapshot) (string, [][]telegram.InlineButton) {
	snapshot.Source = core.NormalizeMemoryReviewSource(string(snapshot.Source))
	if snapshot.Source == "" {
		snapshot.Source = memoryReviewSourceSession
	}
	state := "read-only memory state"
	if snapshot.Stats.Partial {
		state = "partial read-only memory state"
	}
	details := []string{
		"Durable stores: " + memoryStoreCountSummary(snapshot.Stats.StoreCounts),
		"Semantic recall: shared " + fmt.Sprint(snapshot.Stats.SemanticSharedCount) + "; local " + fmt.Sprint(snapshot.Stats.SemanticLocalCount),
		"Session context items: " + fmt.Sprint(snapshot.Stats.SessionRecentCount),
		"Writes: none",
	}
	if len(snapshot.Stats.Missing) > 0 {
		details = append(details, "Missing: "+strings.Join(snapshot.Stats.Missing, ", "))
	}
	if len(snapshot.Items) == 0 {
		details = append(details, "Recall preview: no high-signal items for this source.")
	} else {
		details = append(details, "Recall preview:")
		for idx, item := range snapshot.Items {
			details = append(details, fmt.Sprintf("%d. %s — %s", idx+1, firstNonEmpty(item.Label, item.ID), truncateOperatorLine(item.Excerpt, 160)))
		}
	}
	evidence := []string{
		"Source: " + memoryReviewSourceDisplay(snapshot.Source),
		"Query seed: " + firstNonEmpty(strings.TrimSpace(snapshot.Query), "-"),
		"Writes: none",
	}
	rows := [][]telegram.InlineButton{{
		{Text: "Session", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionView, memoryReviewSourceSession, 0)},
		{Text: "Shared", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionView, memoryReviewSourceShared, 0)},
		{Text: "Local", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionView, memoryReviewSourceLocal, 0)},
	}, {
		{Text: "Ask Me", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionAsk, snapshot.Source, 0)},
		{Text: "Refresh", CallbackData: encodeMemoryReviewCallbackData(memoryReviewActionRefresh, snapshot.Source, 0)},
	}}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Memory",
		State:    state,
		Why:      "Durable memory is signed-off claims; semantic memory is recall material, not fact.",
		Next:     "Read this snapshot, refresh it, or tap Ask Me for clarifying questions.",
		Details:  details,
		Evidence: evidence,
	}, 8, 3), rows
}

func renderContextPanel(snapshot core.ContextSnapshot) (string, [][]telegram.InlineButton) {
	chat := snapshot.Chat
	state := "read-only context snapshot"
	if chat.OperationStatus != "" {
		state += "; operation " + chat.OperationStatus
	}
	details := []string{
		"Current lane: " + contextLaneLabel(chat),
		"Active operation: " + firstNonEmpty(chat.OperationSummary, chat.OperationStatus, "none"),
		"Current plan: " + firstNonEmpty(chat.PlanStep, chat.PlanStepStatus, "none"),
	}
	if len(snapshot.Recent) == 0 {
		details = append(details, "Recent relevant context: none surfaced")
	} else {
		for i, item := range snapshot.Recent {
			if i >= 3 {
				break
			}
			details = append(details, fmt.Sprintf("Recent %d: %s", i+1, truncateOperatorLine(item.Excerpt, 180)))
		}
	}
	evidence := []string{"Source: session state + operation/plan sidecars", "Generated: " + snapshot.GeneratedAt.UTC().Format(time.RFC3339), "Writes: none"}
	rows := [][]telegram.InlineButton{{
		{Text: "Ask Me", CallbackData: encodeContextCallbackData(memoryReviewActionAsk)},
		{Text: "Refresh", CallbackData: encodeContextCallbackData(memoryReviewActionRefresh)},
	}}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Context",
		State:    state,
		Why:      "This is the context currently shaping replies in this chat or thread.",
		Next:     "Read it, refresh it, or tap Ask Me for clarifying questions.",
		Details:  details,
		Evidence: evidence,
	}, 8, 3), rows
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

func memoryStoreCountSummary(counts map[string]int) string {
	if len(counts) == 0 {
		return "unavailable"
	}
	stores := []string{"memory", "knowledge", "decisions", "questions", "rhizome", "dreams"}
	parts := make([]string, 0, len(stores))
	for _, store := range stores {
		parts = append(parts, fmt.Sprintf("%s=%d", store, counts[store]))
	}
	return strings.Join(parts, "; ")
}

func contextLaneLabel(chat core.ChatStatusSnapshot) string {
	if strings.TrimSpace(chat.ScopeKind) != "" {
		return firstNonEmpty(strings.TrimSpace(chat.ScopeKind)+" "+strings.TrimSpace(chat.ScopeID), strings.TrimSpace(chat.ScopeKind))
	}
	if chat.ChatID != 0 {
		return "main chat"
	}
	return "unknown"
}

func handleMemoryReviewCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action memoryReviewCallbackAction, source memoryReviewSource, index int) (bool, error) {
	_ = index
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	if targetMsg.ChatID == 0 || targetMsg.MessageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleMemoryCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	source = core.NormalizeMemoryReviewSource(string(source))
	if source == "" {
		source = memoryReviewSourceSession
	}
	if action == memoryReviewActionAsk {
		return queueAskMeCallback(ctx, sender, router, cb, targetMsg, telegramMemoryClarificationIngressSurface, askMePrompt("memory", source))
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	snapshot, err := memoryReviewSnapshotForCommand(ctx, router, targetMsg, source)
	if err != nil {
		return true, err
	}
	text, rows := renderMemoryReviewPanel(snapshot)
	text = telegramThreadDisplayPrefixForMessage(targetMsg) + text
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, targetMsg.ChatID, targetMsg.MessageID, text, "", rows); err != nil {
		return true, err
	}
	return true, nil
}

func handleContextCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action memoryReviewCallbackAction) (bool, error) {
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	if targetMsg.ChatID == 0 || targetMsg.MessageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "This context action is no longer active. Run /context again."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if action == memoryReviewActionAsk {
		return queueAskMeCallback(ctx, sender, router, cb, targetMsg, telegramContextClarificationIngressSurface, askMePrompt("context", memoryReviewSourceSession))
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	snapshot, err := contextSnapshotForCommand(ctx, router, targetMsg)
	if err != nil {
		return true, err
	}
	text, rows := renderContextPanel(snapshot)
	text = telegramThreadDisplayPrefixForMessage(targetMsg) + text
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, targetMsg.ChatID, targetMsg.MessageID, text, "", rows); err != nil {
		return true, err
	}
	return true, nil
}

func queueAskMeCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, targetMsg core.InboundMessage, surface string, prompt string) (bool, error) {
	queued := targetMsg
	queued.Text = prompt
	queued.IngressSurface = strings.TrimSpace(surface)
	queued.IngressUpdateID = cb.UpdateID
	if clarification, ok := router.(interface {
		QueueClarification(context.Context, core.InboundMessage) error
	}); ok {
		if err := clarification.QueueClarification(ctx, queued); err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Clarification queued."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  targetMsg.ChatID,
		Text:    prompt,
		ReplyTo: replyToMessageID(targetMsg.MessageID),
	})
	if err != nil {
		return true, err
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Clarification queued."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	return true, nil
}

func askMePrompt(surface string, source memoryReviewSource) string {
	if surface == "context" {
		return "Ask me concise clarifying questions about the current context you are carrying. Show what you think is shaping this chat or thread, what assumptions may be wrong, and what you need me to clarify. Do not write memory or change state."
	}
	return "Ask me concise clarifying questions about memory. Show what durable-memory assumptions or semantic recalls may need confirmation, label recall as recall rather than fact, and ask what I should confirm, correct, or reject. Do not write memory or change state. Source view: " + memoryReviewSourceDisplay(source) + "."
}
