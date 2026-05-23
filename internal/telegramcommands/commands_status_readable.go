//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/face"
)

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
	details := make([]string, 0, 16)
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget:
		details = append(details, renderReadableChatStatusLines(rawText, view == statusViewPending)...)
	case statusViewSystem:
		details = append(details, renderReadableSystemStatusLines(rawText)...)
	case statusViewHotChats:
		details = append(details, renderReadableHotChatsStatusLines(rawText)...)
	case statusViewFindChat:
		details = append(details, renderReadableFindChatStatusLines(rawText)...)
	case statusViewDurables:
		details = append(details, renderReadableDurablesStatusLines(rawText)...)
	default:
		details = append(details, renderReadableGenericStatusLines(rawText)...)
	}
	if hint := statusDetailsHint(view); hint != "" {
		details = append(details, hint)
	}
	panel := face.RenderOperatorPanel(face.OperatorPanel{
		Title:    statusViewTitle(view),
		State:    statusViewState(view, rawText),
		Why:      statusViewWhy(view, rawText),
		Next:     statusViewNext(view, rawText),
		Details:  details,
		Evidence: statusViewEvidence(view),
	})
	quickRead = strings.TrimSpace(quickRead)
	if quickRead == "" {
		return panel
	}
	return "Quick Read: " + quickRead + "\n\n" + panel
}

func statusViewTitle(view statusView) string {
	switch view {
	case statusViewSystem:
		return "System Status"
	case statusViewHotChats:
		return "Hot Chats"
	case statusViewFindChat:
		return "Find Chat"
	case statusViewDurables:
		return "Durable Agents"
	case statusViewPending:
		return "Pending Status"
	case statusViewChatTarget:
		return "Chat Status"
	default:
		return "Chat Status"
	}
}

func statusViewState(view statusView, rawText string) string {
	switch view {
	case statusViewSystem:
		active, _ := parseStatusSummaryIntToken(rawText, "active_turns")
		queued, _ := parseStatusSummaryIntToken(rawText, "queued_chats")
		pending, _ := parseStatusSummaryIntToken(rawText, "pending_items")
		stale, _ := parseStatusSummaryIntToken(rawText, "stale_running")
		switch {
		case stale > 0:
			return "needs recovery"
		case pending > 0:
			return "needs attention"
		case active > 0:
			return "working"
		case queued > 0:
			return "queued"
		default:
			return "idle"
		}
	case statusViewHotChats:
		if hot, ok := parseStatusSummaryIntToken(rawText, "hot_chats"); ok && hot > 0 {
			return fmt.Sprintf("%d active or pending chat(s)", hot)
		}
		return "none"
	case statusViewFindChat:
		if strings.Contains(rawText, "No active or pending chats") {
			return "none"
		}
		return "ready"
	case statusViewDurables:
		degraded, _ := parseStatusSummaryIntToken(rawText, "degraded")
		active, _ := parseStatusSummaryIntToken(rawText, "active")
		total, _ := parseStatusSummaryIntToken(rawText, "total")
		switch {
		case degraded > 0:
			return fmt.Sprintf("%d degraded durable agent(s)", degraded)
		case total == 0:
			return "none"
		case active > 0:
			return fmt.Sprintf("%d active durable agent(s)", active)
		default:
			return "idle"
		}
	default:
		return statusSummaryStateDisplay(firstNonEmptyStatusSummary(statusSummaryStateToken(rawText), "unknown"))
	}
}

func statusViewWhy(view statusView, rawText string) string {
	switch view {
	case statusViewSystem:
		pending := firstNonEmptyStatusSummary(statusSummaryToken(rawText, "pending_items"), "0")
		active := firstNonEmptyStatusSummary(statusSummaryToken(rawText, "active_turns"), "0")
		queued := firstNonEmptyStatusSummary(statusSummaryToken(rawText, "queued_chats"), "0")
		return fmt.Sprintf("system projection has %s active turn(s), %s queued chat(s), and %s pending item(s)", active, queued, pending)
	case statusViewHotChats:
		return "these chats have active work, queue depth, or pending operator items"
	case statusViewFindChat:
		return "admin chat drilldown starts from currently active or pending chats"
	case statusViewDurables:
		return "durable children are subordinate runtimes and need visible policy, wake, grant, and enrollment posture"
	default:
		return "chat status is a projection over typed runtime state and TES-backed evidence"
	}
}

func statusViewNext(view statusView, rawText string) string {
	switch view {
	case statusViewSystem:
		if pending, ok := parseStatusSummaryIntToken(rawText, "pending_items"); ok && pending > 0 {
			return "open pending items or run /health diagnose for repair guidance"
		}
		return "use /health trace for raw execution evidence or /health diagnose for a read-only diagnosis"
	case statusViewHotChats:
		return "select Find Chat to inspect one chat, or open /health trace for raw evidence"
	case statusViewFindChat:
		return "choose a chat button to inspect that chat's status"
	case statusViewDurables:
		if degraded, ok := parseStatusSummaryIntToken(rawText, "degraded"); ok && degraded > 0 {
			return "inspect the degraded child with durable-agent health or run /health diagnose"
		}
		return "refresh after child wake, policy, grant, or Tailnet changes"
	default:
		return "use /health trace for raw execution evidence or /health diagnose for a read-only diagnosis"
	}
}

func statusViewEvidence(view statusView) []string {
	switch view {
	case statusViewDurables:
		return []string{"Source: durable-agent registry, runtime state, policy state, and TES projections."}
	case statusViewSystem, statusViewHotChats, statusViewFindChat:
		return []string{"Source: system status projection with TES-preferred runtime evidence."}
	default:
		return []string{"Source: chat status projection with TES-preferred runtime evidence."}
	}
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
		return "Use /health trace for the full execution trace and source attribution."
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

func renderStatusSourceAttribution(view statusView) string {
	lines := []string{"source_attribution:"}
	switch view {
	case statusViewChat, statusViewPending, statusViewChatTarget:
		lines = append(lines,
			"- field=summary_state class=projection",
			"- field=latest_turn class=projection preferred=canonical:execution_events.turn",
			"- field=operation_plan_hidden_inputs class=projection preferred=canonical:execution_events.turn_sidecars fallback=operational_current_state_store:status_state_json",
			"- field=tool_authority_lifecycle class=projection preferred=canonical:execution_events.tool_authority note=distinguishes_install_audit_probe_register",
			"- field=delivery class=projection preferred=canonical:execution_events.delivery fallback=operational_current_state_store:status_state_json note=transport_ledger_only",
		)
	case statusViewSystem, statusViewHotChats, statusViewFindChat:
		lines = append(lines,
			"- field=active_turns_queue_depth class=projection preferred=canonical:execution_events.router fallback=operational_current_state_store:router_runtime",
			"- field=latest_turns class=projection preferred=canonical:execution_events.turn",
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
		return fmt.Sprintf("Chat is %s; action items %s; backlog items %s; signal %s.", statusSummaryStateDisplay(state), actionValue, backlogValue, signal)
	case statusViewSystem:
		active := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "active_turns"), "0")
		queued := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "queued_chats"), "0")
		pending := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "pending_items"), "0")
		return fmt.Sprintf("System has %s active turn(s), %s queued chat(s), and %s pending item(s).", active, queued, pending)
	case statusViewHotChats:
		hot := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "hot_chats"), "0")
		return fmt.Sprintf("Hot chats listed: %s.", hot)
	case statusViewDurables:
		total := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "total"), "0")
		active := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "active"), "0")
		degraded := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "degraded"), "0")
		inactive := firstNonEmptyStatusSummary(statusSummaryToken(statusText, "inactive"), "0")
		return fmt.Sprintf("Durables total %s; active %s; degraded %s; inactive %s.", total, active, degraded, inactive)
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
