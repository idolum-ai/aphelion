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
	statusViewDurables   statusView = "durables"
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
		rawText := face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
		summary := statusReadableSummaryText(ctx, router, view, rawText)
		text = renderStatusChatOperatorView(chat, personaEffort, governorEffort, false, summary)
	case statusViewPending:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		rawText := face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, true)
		summary := statusReadableSummaryText(ctx, router, view, rawText)
		text = renderStatusChatOperatorView(chat, personaEffort, governorEffort, true, summary)
	case statusViewChatTarget:
		chat, err := router.StatusChat(targetChatID)
		if err != nil {
			return "", nil, err
		}
		rawText := face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
		summary := statusReadableSummaryText(ctx, router, view, rawText)
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
	case statusViewDurables:
		if !isAdmin {
			return "", nil, fmt.Errorf("admin status view denied")
		}
		status, err := router.StatusDurables(senderID)
		if err != nil {
			return "", nil, err
		}
		text = face.RenderTelegramStatusDurables(status)
	default:
		chat, err := router.StatusChat(currentChatID)
		if err != nil {
			return "", nil, err
		}
		view = statusViewChat
		rawText := face.RenderTelegramStatusChat(chat, personaEffort, governorEffort, false)
		summary := statusReadableSummaryText(ctx, router, view, rawText)
		text = renderStatusChatOperatorView(chat, personaEffort, governorEffort, false, summary)
	}
	if view != statusViewChat && view != statusViewPending && view != statusViewChatTarget {
		summary := statusReadableSummaryText(ctx, router, view, text)
		text = renderReadableStatusView(view, text, summary)
	}
	text = humanizeTelegramTelemetryText(text)
	rows := statusKeyboardRows(view, currentChatID, targetChatID, isAdmin, systemStatus, systemLoaded)
	return text, rows, nil
}

func renderStatusChatOperatorView(chat core.ChatStatusSnapshot, personaEffort string, governorEffort string, pendingOnly bool, quickRead string) string {
	text := strings.TrimSpace(face.RenderTelegramStatusChatOperatorCard(chat, personaEffort, governorEffort, pendingOnly))
	quickRead = strings.TrimSpace(quickRead)
	if quickRead == "" {
		return text
	}
	return "quick_read " + quickRead + "\n\n" + text
}

func appendStatusReadableSummary(ctx context.Context, router commandRouter, view statusView, text string) string {
	summary := statusReadableSummaryText(ctx, router, view, text)
	if summary == "" {
		return text
	}
	return "quick_read " + summary + "\n\n" + text
}

func statusReadableSummaryText(ctx context.Context, router commandRouter, view statusView, text string) string {
	if router == nil || !statusViewSupportsReadableSummary(view) {
		return ""
	}
	summary := strings.TrimSpace(router.StatusReadableSummary(ctx, string(view), text))
	summary = groundStatusReadableSummary(view, summary, text)
	if summary == "" {
		summary = composeStatusReadableSummary(view, text)
	}
	return summary
}

func renderReadableStatusView(view statusView, rawText string, quickRead string) string {
	rawText = strings.TrimSpace(rawText)
	lines := make([]string, 0, 16)
	if quickRead = strings.TrimSpace(quickRead); quickRead != "" {
		lines = append(lines, "quick_read "+quickRead, "")
	}
	if scope := statusRawLine(rawText, "status_scope="); scope != "" {
		lines = append(lines, scope)
	}
	if summary := statusRawLine(rawText, "summary "); summary != "" {
		lines = append(lines, summary)
	}

	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget:
		lines = append(lines, renderReadableChatStatusLines(rawText, view == statusViewPending)...)
	case statusViewSystem:
		lines = append(lines, renderReadableSystemStatusLines(rawText)...)
	case statusViewHotChats:
		lines = append(lines, renderReadableHotChatsStatusLines(rawText)...)
	case statusViewFindChat:
		lines = append(lines, renderReadableFindChatStatusLines(rawText)...)
	case statusViewDurables:
		lines = append(lines, renderReadableDurablesStatusLines(rawText)...)
	default:
		lines = append(lines, renderReadableGenericStatusLines(rawText)...)
	}
	if hint := statusDetailsHint(view); hint != "" {
		lines = append(lines, "", hint)
	}
	return strings.Join(compactStatusDisplayLines(lines), "\n")
}

func renderReadableChatStatusLines(rawText string, pendingOnly bool) []string {
	lines := []string{"current:"}
	if pendingOnly {
		lines = append(lines, renderStatusBlock(rawText, "pending_items:", 12)...)
		if effort := statusRawLine(rawText, "effort "); effort != "" {
			lines = append(lines, "- "+effort)
		}
		return lines
	}
	for _, prefix := range []string{
		"current_signal=",
		"turn_phase ",
		"latest_turn ",
		"operation ",
		"plan_step ",
		"plan_progress ",
		"delivery ",
		"continuation ",
		"hidden_inputs ",
		"detached_work ",
		"watchdog ",
		"effort ",
	} {
		if line := statusRawLine(rawText, prefix); line != "" {
			lines = append(lines, "- "+line)
		}
	}
	if len(lines) == 1 {
		lines = append(lines, "- state="+firstNonEmptyStatusSummary(statusSummaryStateToken(rawText), "unknown"))
	}
	if block := renderStatusBlock(rawText, "pending_items:", 5); len(block) > 0 {
		lines = append(lines, block...)
	}
	return lines
}

func renderReadableSystemStatusLines(rawText string) []string {
	lines := []string{"current:"}
	for _, prefix := range []string{"active_chat_ids=", "watchdog ", "effort "} {
		if line := statusRawLine(rawText, prefix); line != "" {
			lines = append(lines, "- "+line)
		}
	}
	lines = append(lines, statusRawLines(rawText, "queue ", 5, "- ")...)
	if block := renderStatusBlock(rawText, "hot_chats:", 5); len(block) > 0 {
		lines = append(lines, block...)
	}
	if block := renderStatusBlock(rawText, "pending_items:", 8); len(block) > 0 {
		lines = append(lines, block...)
	}
	if block := renderStatusBlock(rawText, "tailnet:", 6); len(block) > 0 {
		lines = append(lines, block...)
	}
	if len(lines) == 1 {
		lines = append(lines, "- state=idle")
	}
	return lines
}

func renderReadableHotChatsStatusLines(rawText string) []string {
	lines := []string{"hot_chats:"}
	lines = append(lines, statusNumberedRawLines(rawText, 12)...)
	if len(lines) == 1 {
		lines = append(lines, statusPlainRawLines(rawText, 4)...)
	}
	return lines
}

func renderReadableFindChatStatusLines(rawText string) []string {
	lines := []string{"find_chat:"}
	lines = append(lines, statusPlainRawLines(rawText, 2)...)
	lines = append(lines, statusNumberedRawLines(rawText, 12)...)
	return lines
}

func renderReadableDurablesStatusLines(rawText string) []string {
	lines := []string{"agents:"}
	for _, line := range statusRawBlockEntries(rawText, "agents:", 10) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- id=") ||
			strings.Contains(trimmed, "health=degraded") ||
			strings.Contains(trimmed, "apply_error=") ||
			strings.Contains(trimmed, "child_runtime_blocked=") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 1 {
		lines = append(lines, "- none")
	}
	return lines
}

func renderReadableGenericStatusLines(rawText string) []string {
	lines := []string{"current:"}
	lines = append(lines, statusPlainRawLines(rawText, 8)...)
	if len(lines) == 1 {
		lines = append(lines, "- status=unavailable")
	}
	return lines
}

func statusDetailsHint(view statusView) string {
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget, statusViewSystem, statusViewHotChats, statusViewDurables:
		return "details: /debug has the full execution trace and source attribution."
	default:
		return ""
	}
}

func statusRawLine(rawText string, prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return ""
	}
	for _, line := range strings.Split(rawText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return trimmed
		}
	}
	return ""
}

func statusRawLines(rawText string, prefix string, max int, outputPrefix string) []string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	out := make([]string, 0, max)
	for _, line := range strings.Split(rawText, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		out = append(out, outputPrefix+trimmed)
		if max > 0 && len(out) >= max {
			return out
		}
	}
	return out
}

func renderStatusBlock(rawText string, header string, maxEntries int) []string {
	entries := statusRawBlockEntries(rawText, header, maxEntries)
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries)+1)
	out = append(out, strings.TrimSpace(header))
	out = append(out, entries...)
	return out
}

func statusRawBlockEntries(rawText string, header string, maxEntries int) []string {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	lines := strings.Split(rawText, "\n")
	out := make([]string, 0, maxEntries)
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == header {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "  ") {
			break
		}
		out = append(out, trimmed)
		if maxEntries > 0 && len(out) >= maxEntries {
			break
		}
	}
	return out
}

func statusNumberedRawLines(rawText string, max int) []string {
	out := make([]string, 0, max)
	for _, line := range strings.Split(rawText, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		dot := strings.Index(trimmed, ".")
		if dot <= 0 {
			continue
		}
		if _, err := strconv.Atoi(trimmed[:dot]); err != nil {
			continue
		}
		out = append(out, trimmed)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func statusPlainRawLines(rawText string, max int) []string {
	out := make([]string, 0, max)
	for _, line := range strings.Split(rawText, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" ||
			strings.HasPrefix(trimmed, "status_scope=") ||
			strings.HasPrefix(trimmed, "summary ") ||
			strings.HasSuffix(trimmed, ":") {
			continue
		}
		out = append(out, trimmed)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func compactStatusDisplayLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	lastBlank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if len(out) == 0 || lastBlank {
				continue
			}
			out = append(out, "")
			lastBlank = true
			continue
		}
		out = append(out, line)
		lastBlank = false
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func groundStatusReadableSummary(view statusView, summary string, statusText string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	expectedState := strings.TrimSpace(statusSummaryStateToken(statusText))
	detectedState := strings.TrimSpace(detectSummaryStateWord(summary))
	if expectedState != "" && detectedState != "" && expectedState != detectedState {
		return ""
	}
	if pending, ok := parseStatusSummaryIntToken(statusText, "pending_items"); ok {
		lower := strings.ToLower(summary)
		if pending > 0 && (strings.Contains(lower, "no pending") || strings.Contains(lower, "0 pending")) {
			return ""
		}
	}
	if summaryClaimsHumanVisibleDelivery(summary) && statusDeliveryStatusToken(statusText) != "" {
		return ""
	}
	_ = view
	return summary
}

func appendStatusSourceAttribution(view statusView, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	if strings.Contains(strings.ToLower(text), "source_attribution:") {
		return text
	}
	block := renderStatusSourceAttribution(view)
	if block == "" {
		return text
	}
	return text + "\n" + block
}

func renderStatusSourceAttribution(view statusView) string {
	lines := []string{"source_attribution:"}
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget:
		lines = append(lines,
			"- field=summary_state class=projection",
			"- field=latest_turn class=projection preferred=canonical:execution_events.turn fallback=compatibility_fallback:turn_runs",
			"- field=operation_plan_hidden_inputs class=projection preferred=canonical:execution_events.turn_sidecars fallback=operational_current_state_store:status_state_json",
			"- field=tool_authority_lifecycle class=projection preferred=canonical:execution_events.tool_authority note=distinguishes_install_audit_probe_register",
			"- field=delivery class=projection preferred=canonical:execution_events.delivery fallback=operational_current_state_store:status_state_json note=transport_ledger_only",
		)
	case statusViewSystem, statusViewHotChats, statusViewFindChat:
		lines = append(lines,
			"- field=active_turns_queue_depth class=projection preferred=canonical:execution_events.router fallback=operational_current_state_store:router_runtime",
			"- field=latest_turns class=projection preferred=canonical:execution_events.turn fallback=compatibility_fallback:turn_runs",
			"- field=pending_decisions class=projection preferred=operational_current_state_store:pending_decisions fallback=canonical:execution_events.decision",
			"- field=pending_continuations class=projection preferred=operational_current_state_store:continuation_state_json fallback=canonical:execution_events.continuation",
		)
		if view == statusViewSystem {
			lines = append(lines, "- field=tool_authority_lifecycle class=projection preferred=canonical:execution_events.tool_authority note=distinguishes_install_audit_probe_register")
		}
	case statusViewDurables:
		lines = append(lines,
			"- field=durable_identity class=canonical store=session.durable_agents",
			"- field=durable_runtime_posture class=operational_current_state_store preferred=session.durable_agent_state overlay=projection:tes_execution_events",
		)
	default:
		return ""
	}
	return strings.Join(lines, "\n")
}

func composeStatusReadableSummary(view statusView, statusText string) string {
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget:
		state := firstNonEmptyStatusSummary(statusSummaryStateToken(statusText), "unknown")
		pendingValue := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "pending_items"), "0")
		actionValue := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "action_items"), pendingValue)
		backlogValue := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "backlog_items"), "0")
		signal := firstNonEmptyStatusSummary(statusCurrentSignal(statusText), "unknown")
		return fmt.Sprintf("Chat is %s; action items=%s; backlog items=%s; signal=%s.", statusSummaryStateDisplay(state), actionValue, backlogValue, signal)
	case statusViewSystem:
		active := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "active_turns"), "0")
		queued := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "queued_chats"), "0")
		pending := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "pending_items"), "0")
		return fmt.Sprintf("System has active turns=%s; queued chats=%s; pending items=%s.", active, queued, pending)
	case statusViewHotChats:
		hot := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "hot_chats"), "0")
		return fmt.Sprintf("Hot chats listed=%s.", hot)
	case statusViewDurables:
		total := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "total"), "0")
		active := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "active"), "0")
		degraded := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "degraded"), "0")
		inactive := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "inactive"), "0")
		return fmt.Sprintf("Durables total=%s; active=%s; degraded=%s; inactive=%s.", total, active, degraded, inactive)
	default:
		return ""
	}
}

func statusSummaryStateDisplay(state string) string {
	switch strings.TrimSpace(state) {
	case "needs_recovery":
		return "needs recovery"
	default:
		return strings.TrimSpace(state)
	}
}

func statusSummaryStateToken(statusText string) string {
	return statusSummaryToken(statusText, "state")
}

func statusSummaryToken(statusText string, token string) string {
	statusText = strings.TrimSpace(statusText)
	token = strings.TrimSpace(token)
	if statusText == "" || token == "" {
		return ""
	}
	for _, line := range strings.Split(statusText, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "summary ") {
			continue
		}
		fields := strings.Fields(line)
		for _, field := range fields {
			if !strings.Contains(field, "=") {
				continue
			}
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.TrimSpace(parts[0]) == token {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func statusCurrentSignal(statusText string) string {
	for _, line := range strings.Split(strings.TrimSpace(statusText), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "current_signal=") {
			continue
		}
		return strings.TrimSpace(strings.TrimPrefix(line, "current_signal="))
	}
	return ""
}

func statusDeliveryStatusToken(statusText string) string {
	for _, line := range strings.Split(strings.TrimSpace(statusText), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "delivery ") {
			continue
		}
		fields := strings.Fields(line)
		for _, field := range fields {
			if !strings.Contains(field, "=") {
				continue
			}
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 {
				continue
			}
			if strings.TrimSpace(parts[0]) == "status" {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func parseStatusSummaryIntToken(statusText string, token string) (int, bool) {
	raw := statusSummaryToken(statusText, token)
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func detectSummaryStateWord(summary string) string {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return ""
	}
	for _, state := range []string{"needs_recovery", "idle", "working", "blocked", "queued", "failed", "interrupted"} {
		if strings.Contains(lower, state) {
			return state
		}
	}
	if strings.Contains(lower, "needs recovery") {
		return "needs_recovery"
	}
	return ""
}

func summaryClaimsHumanVisibleDelivery(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	for _, phrase := range []string{
		"human saw",
		"user saw",
		"you saw",
		"was shown",
		"shown to",
		"displayed to",
		"read it",
		"read the message",
		"received it",
		"delivered to the user",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func firstNonEmptyStatusSummary(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func statusViewSupportsReadableSummary(view statusView) bool {
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget, statusViewSystem, statusViewHotChats, statusViewDurables:
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
		rows = append(rows, []telegram.InlineButton{
			{Text: "Durables", CallbackData: encodeStatusCallbackData(statusViewDurables, 0)},
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
	case statusViewSystem, statusViewHotChats, statusViewFindChat, statusViewDurables:
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
	case statusViewDurables:
		return statusCallbackPrefix + "durables"
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
		case "durables":
			return statusViewDurables, 0, true
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
		chunks = []string{humanizeTelegramTelemetryText("status_scope=chat\nsummary unavailable")}
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
