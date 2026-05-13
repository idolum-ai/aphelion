//go:build linux

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
)

const debugCallbackPrefix = "debug:"

type debugView string

const (
	debugViewMore debugView = "more"
)

func renderDebugSnapshot(ctx context.Context, router commandRouter, chatID int64, senderID int64, personaEffort string, governorEffort string) (string, string, error) {
	chat, err := router.StatusChat(chatID)
	if err != nil {
		return "", "", err
	}
	var (
		system   *core.SystemStatusSnapshot
		durables *core.DurableAgentsStatusSnapshot
	)
	if router.CanRestart(senderID) {
		sys, err := router.StatusSystem(senderID)
		if err != nil {
			return "", "", err
		}
		system = &sys
		durs, err := router.StatusDurables(senderID)
		if err != nil {
			return "", "", err
		}
		durables = &durs
	}
	full := face.RenderTelegramDebug(chat, system, durables, personaEffort, governorEffort)
	full = strings.TrimSpace(full)
	summary := strings.TrimSpace(router.StatusReadableSummary(ctx, "debug", full))
	summary = groundDebugReadableSummary(summary, chat, system)
	if summary == "" {
		summary = composeDebugReadableSummary(chat, system)
	}
	quick := ""
	if summary != "" {
		quick = "quick_read " + summary
		full = "quick_read " + summary + "\n\n" + full
	}
	quick = humanizeTelegramTelemetryText(quick)
	full = humanizeTelegramTelemetryText(full)
	return quick, full, nil
}

func groundDebugReadableSummary(summary string, chat core.ChatStatusSnapshot, system *core.SystemStatusSnapshot) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	state := debugChatState(chat)
	lower := strings.ToLower(summary)
	inconsistent := false
	for _, candidate := range []string{"idle", "working", "blocked", "queued", "failed", "interrupted"} {
		if strings.Contains(lower, candidate) && candidate != state {
			inconsistent = true
			break
		}
	}
	if strings.Contains(lower, "completed successfully") || strings.HasPrefix(lower, "done") || strings.HasPrefix(lower, "all set") {
		latestStatus := strings.ToLower(strings.TrimSpace(debugLatestTurnStatus(chat)))
		if latestStatus != "" && latestStatus != "completed" {
			inconsistent = true
		}
	}
	if !inconsistent {
		return summary
	}
	return composeDebugReadableSummary(chat, system)
}

func composeDebugReadableSummary(chat core.ChatStatusSnapshot, system *core.SystemStatusSnapshot) string {
	state := debugChatState(chat)
	parts := []string{fmt.Sprintf("Chat %d is %s", chat.ChatID, state)}
	latestStatus := strings.ToLower(strings.TrimSpace(debugLatestTurnStatus(chat)))
	if latestStatus != "" {
		parts = append(parts, "latest turn is "+latestStatus)
	}
	if tool := strings.TrimSpace(debugLatestTurnTool(chat)); tool != "" {
		parts = append(parts, "last tool "+tool)
	}
	if pending := len(chat.PendingItems); pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending item(s)", pending))
	}
	if system != nil {
		if pending := len(system.PendingItems); pending > 0 {
			parts = append(parts, fmt.Sprintf("system has %d pending item(s)", pending))
		}
	}
	return strings.Join(parts, "; ") + "."
}

func debugChatState(chat core.ChatStatusSnapshot) string {
	if len(chat.ActiveTurnIDs) > 0 || strings.TrimSpace(chat.TurnPhase) != "" {
		return "working"
	}
	latestStatus := strings.ToLower(strings.TrimSpace(debugLatestTurnStatus(chat)))
	if latestStatus == "running" {
		return "working"
	}
	for _, item := range chat.PendingItems {
		switch item.Kind {
		case core.PendingItemKindDecision, core.PendingItemKindContinuation:
			return "blocked"
		}
	}
	if strings.EqualFold(strings.TrimSpace(chat.OperationStatus), "blocked") {
		return "blocked"
	}
	switch latestStatus {
	case "interrupted":
		return "interrupted"
	case "failed":
		return "failed"
	}
	if chat.QueueDepth > 0 {
		return "queued"
	}
	return "idle"
}

func debugLatestTurnStatus(chat core.ChatStatusSnapshot) string {
	if chat.LatestTurnRun == nil {
		return ""
	}
	return strings.TrimSpace(chat.LatestTurnRun.Status)
}

func debugLatestTurnTool(chat core.ChatStatusSnapshot) string {
	if chat.LatestTurnRun == nil {
		return ""
	}
	return strings.TrimSpace(chat.LatestTurnRun.LastToolName)
}

func encodeDebugCallbackData(view debugView) string {
	switch view {
	case debugViewMore:
		return debugCallbackPrefix + "more"
	default:
		return debugCallbackPrefix + "more"
	}
}

func decodeDebugCallbackData(data string) (debugView, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, debugCallbackPrefix) {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, debugCallbackPrefix))
	switch payload {
	case "more":
		return debugViewMore, true
	default:
		return "", false
	}
}

func deliverDebugCallbackView(ctx context.Context, sender commandCallbackSender, chatID int64, messageID int64, text string) error {
	if sender == nil {
		return nil
	}
	chunks := splitStatusTextChunks(text, statusMessageChunkLimit)
	if len(chunks) == 0 {
		chunks = []string{humanizeTelegramTelemetryText("debug_scope=chat\nsummary unavailable")}
	}
	first := chunks[0]
	if messageID != 0 {
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, first); err != nil {
			return err
		}
	} else {
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   first,
		}); err != nil {
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
