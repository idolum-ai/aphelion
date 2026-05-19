//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
)

func renderDebugSnapshot(ctx context.Context, router commandRouter, chatID int64, senderID int64, personaEffort string, governorEffort string) (string, string, error) {
	projection, err := renderDebugSnapshotProjection(ctx, router, chatID, senderID, personaEffort, governorEffort)
	if err != nil {
		return "", "", err
	}
	full := strings.TrimSpace(joinDebugSectionTexts(projection.Sections))
	if strings.TrimSpace(projection.QuickText) != "" {
		full = projection.QuickText + "\n\n" + full
	}
	return projection.QuickText, strings.TrimSpace(full), nil
}

type debugSnapshotProjection struct {
	QuickText string
	Sections  []face.TelegramDebugSection
}

func renderDebugSnapshotProjection(ctx context.Context, router commandRouter, chatID int64, senderID int64, personaEffort string, governorEffort string) (debugSnapshotProjection, error) {
	chat, err := router.StatusChat(chatID)
	if err != nil {
		return debugSnapshotProjection{}, err
	}
	var (
		system   *core.SystemStatusSnapshot
		durables *core.DurableAgentsStatusSnapshot
	)
	if router.CanRestart(senderID) {
		sys, err := router.StatusSystem(senderID)
		if err != nil {
			return debugSnapshotProjection{}, err
		}
		system = &sys
		durs, err := router.StatusDurables(senderID)
		if err != nil {
			return debugSnapshotProjection{}, err
		}
		durables = &durs
	}
	sections := face.RenderTelegramDebugSections(chat, system, durables, personaEffort, governorEffort)
	full := strings.TrimSpace(joinDebugSectionTexts(sections))
	summary := strings.TrimSpace(router.StatusReadableSummary(ctx, "trace", full))
	summary = groundDebugReadableSummary(summary, chat, system)
	if summary == "" {
		summary = composeDebugReadableSummary(chat, system)
	}
	quick := ""
	if summary != "" {
		quick = "quick_read " + summary
	}
	quick = humanizeTelegramTelemetryText(quick)
	for i := range sections {
		sections[i].Text = humanizeTelegramTelemetryText(sections[i].Text)
	}
	return debugSnapshotProjection{QuickText: quick, Sections: sections}, nil
}

func joinDebugSectionTexts(sections []face.TelegramDebugSection) string {
	texts := make([]string, 0, len(sections))
	for _, section := range sections {
		text := strings.TrimSpace(section.Text)
		if text == "" {
			continue
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n\n")
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
