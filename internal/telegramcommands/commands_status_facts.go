//go:build linux

package telegramcommands

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
)

const statusReadableQuickReadMaxChars = 320

type statusReadableFacts struct {
	View           statusView
	State          string
	ActiveTurns    int
	QueuedChats    int
	QueueDepth     int
	PendingItems   int
	ActionItems    int
	BacklogItems   int
	StaleRunning   int
	HotChats       int
	TotalDurables  int
	ActiveDurables int
	DegradedAgents int
	InactiveAgents int
	CurrentSignal  string
	DeliveryStatus string
}

func normalizeStatusReadableFactsView(view statusView) statusView {
	if view == "" {
		return statusViewChat
	}
	return view
}

func statusReadableFactsFromChat(view statusView, snapshot core.ChatStatusSnapshot) statusReadableFacts {
	actionable, backlog := face.TelegramStatusPendingItemCounts(snapshot.PendingItems)
	return statusReadableFacts{
		View:           normalizeStatusReadableFactsView(view),
		State:          face.TelegramStatusChatState(snapshot),
		ActiveTurns:    len(snapshot.ActiveTurnIDs),
		QueueDepth:     snapshot.QueueDepth,
		PendingItems:   len(snapshot.PendingItems),
		ActionItems:    actionable,
		BacklogItems:   backlog,
		StaleRunning:   len(snapshot.StaleRunningTurns),
		CurrentSignal:  face.TelegramStatusChatCurrentSignal(snapshot),
		DeliveryStatus: strings.TrimSpace(snapshot.DeliveryStatus),
	}
}

func statusReadableFactsFromSystem(view statusView, snapshot core.SystemStatusSnapshot) statusReadableFacts {
	facts := statusReadableFacts{
		View:         normalizeStatusReadableFactsView(view),
		ActiveTurns:  snapshot.ActiveTurnCount,
		QueuedChats:  len(snapshot.QueueDepthByChat),
		PendingItems: len(snapshot.PendingItems),
		StaleRunning: len(snapshot.StaleRunningTurns),
		HotChats:     len(snapshot.HotChats),
	}
	switch view {
	case statusViewHotChats:
		if len(snapshot.HotChats) > 0 {
			facts.State = fmt.Sprintf("%d active or pending chat(s)", len(snapshot.HotChats))
		} else {
			facts.State = "none"
		}
	case statusViewFindChat:
		if len(snapshot.HotChats) > 0 {
			facts.State = "ready"
		} else {
			facts.State = "none"
		}
	default:
		switch {
		case facts.StaleRunning > 0:
			facts.State = "needs_recovery"
		case facts.PendingItems > 0:
			facts.State = "needs_attention"
		case facts.ActiveTurns > 0:
			facts.State = "working"
		case facts.QueuedChats > 0:
			facts.State = "queued"
		default:
			facts.State = "idle"
		}
	}
	return facts
}

func statusReadableFactsFromDurables(snapshot core.DurableAgentsStatusSnapshot) statusReadableFacts {
	facts := statusReadableFacts{
		View:           statusViewDurables,
		TotalDurables:  snapshot.TotalAgents,
		ActiveDurables: snapshot.ActiveAgents,
		DegradedAgents: snapshot.DegradedAgents,
		InactiveAgents: snapshot.InactiveAgents,
	}
	switch {
	case snapshot.DegradedAgents > 0:
		facts.State = "degraded"
	case snapshot.TotalAgents == 0:
		facts.State = "none"
	case snapshot.ActiveAgents > 0:
		facts.State = "active"
	default:
		facts.State = "idle"
	}
	return facts
}

func (f statusReadableFacts) providerInput() string {
	parts := []string{
		"status_view=" + string(normalizeStatusReadableFactsView(f.View)),
		"state=" + strings.TrimSpace(f.State),
		fmt.Sprintf("active_turns=%d", f.ActiveTurns),
		fmt.Sprintf("queued_chats=%d", f.QueuedChats),
		fmt.Sprintf("queue_depth=%d", f.QueueDepth),
		fmt.Sprintf("pending_items=%d", f.PendingItems),
		fmt.Sprintf("action_items=%d", f.ActionItems),
		fmt.Sprintf("backlog_items=%d", f.BacklogItems),
		fmt.Sprintf("stale_running=%d", f.StaleRunning),
		fmt.Sprintf("hot_chats=%d", f.HotChats),
		fmt.Sprintf("total_durables=%d", f.TotalDurables),
		fmt.Sprintf("active_durables=%d", f.ActiveDurables),
		fmt.Sprintf("degraded_agents=%d", f.DegradedAgents),
		fmt.Sprintf("inactive_agents=%d", f.InactiveAgents),
	}
	if signal := strings.TrimSpace(f.CurrentSignal); signal != "" {
		parts = append(parts, "current_signal="+signal)
	}
	if delivery := strings.TrimSpace(f.DeliveryStatus); delivery != "" {
		parts = append(parts, "delivery_status="+delivery)
	}
	return strings.Join(parts, "\n")
}

func compactStatusReadableSummary(summary string) string {
	summary = strings.TrimSpace(strings.Join(strings.Fields(summary), " "))
	if summary == "" {
		return ""
	}
	runes := []rune(summary)
	if len(runes) <= statusReadableQuickReadMaxChars {
		return summary
	}
	return strings.TrimSpace(string(runes[:statusReadableQuickReadMaxChars-1])) + "..."
}
