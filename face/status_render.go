//go:build linux

package face

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func RenderTelegramStatusChat(snapshot core.ChatStatusSnapshot, personaEffort string, governorEffort string, pendingOnly bool) string {
	lines := []string{
		fmt.Sprintf("status_scope=chat chat_id=%d generated_at=%s", snapshot.ChatID, formatStatusTime(snapshot.GeneratedAt)),
	}
	if pendingOnly {
		lines = append(lines, fmt.Sprintf("summary pending_items=%d", len(snapshot.PendingItems)))
	} else {
		state := "idle"
		if len(snapshot.ActiveTurnIDs) > 0 {
			state = "working"
		}
		lines = append(lines, fmt.Sprintf("summary state=%s active_turns=%d queue_depth=%d pending_items=%d", state, len(snapshot.ActiveTurnIDs), snapshot.QueueDepth, len(snapshot.PendingItems)))
		if snapshot.LatestTurnRun != nil {
			latest := snapshot.LatestTurnRun
			latestLine := fmt.Sprintf("latest_turn status=%s kind=%s last_activity=%s", latest.Status, latest.Kind, formatStatusTime(latest.LastActivityAt))
			if latest.LastToolName != "" {
				latestLine += " last_tool=" + latest.LastToolName
			}
			if latest.ErrorText != "" {
				latestLine += " error=" + quoteStatusField(truncateStatusField(latest.ErrorText, 120))
			}
			lines = append(lines, latestLine)
		}
		if snapshot.Continuation != nil {
			cont := snapshot.Continuation
			line := fmt.Sprintf("continuation status=%s remaining_turns=%d", cont.Status, cont.RemainingTurns)
			if cont.DecisionID != "" {
				line += " decision_id=" + cont.DecisionID
			}
			if cont.ApprovedBy > 0 {
				line += fmt.Sprintf(" approved_by=%d", cont.ApprovedBy)
			}
			lines = append(lines, line)
		}
		lines = append(lines, fmt.Sprintf("watchdog triggered=%t stale_threshold=%s stale_limit=%d", snapshot.RestartHealth.WatchdogTriggered, snapshot.RestartHealth.StaleTurnThreshold, snapshot.RestartHealth.StaleTurnLimit))
	}
	lines = append(lines, renderPendingItemBlock(snapshot.PendingItems, 12)...)
	lines = append(lines,
		fmt.Sprintf("effort persona=%s governor=%s", strings.TrimSpace(personaEffort), strings.TrimSpace(governorEffort)),
	)
	return strings.Join(lines, "\n")
}

func RenderTelegramStatusSystem(snapshot core.SystemStatusSnapshot, personaEffort string, governorEffort string) string {
	lines := []string{
		fmt.Sprintf("status_scope=system generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		fmt.Sprintf("summary active_turns=%d active_chats=%d queued_chats=%d pending_items=%d continuations=%d stale_running=%d", snapshot.ActiveTurnCount, len(snapshot.ActiveChatIDs), len(snapshot.QueueDepthByChat), len(snapshot.PendingItems), len(snapshot.Continuations), len(snapshot.StaleRunningTurns)),
		fmt.Sprintf("active_chat_ids=%s", formatInt64List(snapshot.ActiveChatIDs)),
	}
	if len(snapshot.QueueDepthByChat) > 0 {
		queueKeys := make([]int64, 0, len(snapshot.QueueDepthByChat))
		for chatID := range snapshot.QueueDepthByChat {
			queueKeys = append(queueKeys, chatID)
		}
		sort.Slice(queueKeys, func(i, j int) bool { return queueKeys[i] < queueKeys[j] })
		for _, chatID := range queueKeys {
			lines = append(lines, fmt.Sprintf("queue chat_id=%d depth=%d", chatID, snapshot.QueueDepthByChat[chatID]))
		}
	}
	if len(snapshot.HotChats) > 0 {
		lines = append(lines, "hot_chats:")
		max := len(snapshot.HotChats)
		if max > 10 {
			max = 10
		}
		for i := 0; i < max; i++ {
			hot := snapshot.HotChats[i]
			line := fmt.Sprintf("- chat_id=%d pending=%d active_turns=%d queue_depth=%d", hot.ChatID, hot.PendingCount, hot.ActiveTurnCount, hot.QueueDepth)
			if hot.LatestStatus != "" {
				line += " latest=" + hot.LatestStatus
			}
			if !hot.LastActivityAt.IsZero() {
				line += " last_activity=" + formatStatusTime(hot.LastActivityAt)
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, renderPendingItemBlock(snapshot.PendingItems, 20)...)
	lines = append(lines, fmt.Sprintf("watchdog triggered=%t stale_threshold=%s stale_limit=%d", snapshot.RestartHealth.WatchdogTriggered, snapshot.RestartHealth.StaleTurnThreshold, snapshot.RestartHealth.StaleTurnLimit))
	lines = append(lines, fmt.Sprintf("effort persona=%s governor=%s", strings.TrimSpace(personaEffort), strings.TrimSpace(governorEffort)))
	return strings.Join(lines, "\n")
}

func RenderTelegramStatusHotChats(snapshot core.SystemStatusSnapshot) string {
	lines := []string{
		fmt.Sprintf("status_scope=hot_chats generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		fmt.Sprintf("summary hot_chats=%d", len(snapshot.HotChats)),
	}
	if len(snapshot.HotChats) == 0 {
		lines = append(lines, "No active or pending chats right now.")
		return strings.Join(lines, "\n")
	}
	max := len(snapshot.HotChats)
	if max > 30 {
		max = 30
	}
	for i := 0; i < max; i++ {
		hot := snapshot.HotChats[i]
		line := fmt.Sprintf("%d. chat_id=%d pending=%d active_turns=%d queue_depth=%d", i+1, hot.ChatID, hot.PendingCount, hot.ActiveTurnCount, hot.QueueDepth)
		if hot.LatestStatus != "" {
			line += " latest=" + hot.LatestStatus
		}
		if !hot.LastActivityAt.IsZero() {
			line += " last_activity=" + formatStatusTime(hot.LastActivityAt)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func RenderTelegramStatusFindChat(snapshot core.SystemStatusSnapshot) string {
	lines := []string{
		fmt.Sprintf("status_scope=find_chat generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		"Select a chat below to drill into scoped status.",
	}
	if len(snapshot.HotChats) == 0 {
		lines = append(lines, "No active or pending chats found.")
		return strings.Join(lines, "\n")
	}
	max := len(snapshot.HotChats)
	if max > 12 {
		max = 12
	}
	for i := 0; i < max; i++ {
		hot := snapshot.HotChats[i]
		lines = append(lines, fmt.Sprintf("%d. chat_id=%d pending=%d queue_depth=%d", i+1, hot.ChatID, hot.PendingCount, hot.QueueDepth))
	}
	return strings.Join(lines, "\n")
}

func renderPendingItemBlock(items []core.PendingItem, max int) []string {
	lines := []string{"pending_items:"}
	if len(items) == 0 {
		lines = append(lines, "- none")
		return lines
	}
	if max <= 0 || max > len(items) {
		max = len(items)
	}
	for i := 0; i < max; i++ {
		item := items[i]
		line := fmt.Sprintf("- kind=%s chat_id=%d", item.Kind, item.ChatID)
		if id := strings.TrimSpace(item.ID); id != "" {
			line += " id=" + id
		}
		if item.Age > 0 {
			line += " age=" + item.Age.Truncate(time.Second).String()
		}
		if item.Stale {
			line += " stale=true"
		}
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
		}
		lines = append(lines, line)
	}
	if len(items) > max {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(items)-max))
	}
	return lines
}

func formatInt64List(values []int64) string {
	if len(values) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatInt(value, 10))
	}
	return strings.Join(parts, ",")
}

func formatStatusTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}

func quoteStatusField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "\"\""
	}
	value = strings.ReplaceAll(value, `"`, `'`)
	return `"` + value + `"`
}

func truncateStatusField(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
