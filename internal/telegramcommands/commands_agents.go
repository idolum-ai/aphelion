//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	durableAgentsCallbackPrefix = "agents:"
	staleAgentsCallbackText     = "This durable-agent action is no longer active. Run /agents again."
	durableAgentsPageSize       = 5
)

type durableAgentsCallbackAction string

const (
	durableAgentsCallbackRefresh durableAgentsCallbackAction = "refresh"
	durableAgentsCallbackStart   durableAgentsCallbackAction = "start"
)

func encodeDurableAgentsRefreshCallbackData() string {
	return durableAgentsCallbackPrefix + string(durableAgentsCallbackRefresh)
}

func encodeDurableAgentsStartCallbackData(agentID string) string {
	return durableAgentsCallbackPrefix + string(durableAgentsCallbackStart) + ":" + strings.TrimSpace(agentID)
}

func decodeDurableAgentsCallbackData(data string) (durableAgentsCallbackAction, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, durableAgentsCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, durableAgentsCallbackPrefix))
	switch {
	case payload == string(durableAgentsCallbackRefresh):
		return durableAgentsCallbackRefresh, "", true
	case strings.HasPrefix(payload, string(durableAgentsCallbackStart)+":"):
		agentID := strings.TrimSpace(strings.TrimPrefix(payload, string(durableAgentsCallbackStart)+":"))
		if agentID == "" {
			return "", "", false
		}
		return durableAgentsCallbackStart, agentID, true
	default:
		return "", "", false
	}
}

func renderDurableAgentsCommand(agents []core.DurableAgentStatusSnapshot) (string, [][]telegram.InlineButton) {
	return renderDurableAgentsCommandPage(agents, 1)
}

func renderDurableAgentsCommandPage(agents []core.DurableAgentStatusSnapshot, page int) (string, [][]telegram.InlineButton) {
	visible, info := telegramPageItems(agents, page, durableAgentsPageSize)
	details := make([]string, 0, len(visible))
	evidence := make([]string, 0, len(visible))
	rows := make([][]telegram.InlineButton, 0, len(visible)+2)
	if len(agents) == 0 {
		details = append(details, "No durable agents are currently configured.")
		rows = append(rows, []telegram.InlineButton{
			{Text: "Refresh", CallbackData: encodeDurableAgentsRefreshCallbackData()},
		})
		return renderTelegramCompactPanel(face.OperatorPanel{
			Title:   "Durable Agents",
			State:   "none configured",
			Why:     "Durable children only appear here after they are declared in governed configuration or state.",
			Next:    "Refresh after adding a child, or use the durable wizard from a normal admin request.",
			Details: details,
		}, false), rows
	}
	for i, agent := range visible {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			continue
		}
		channel := firstNonEmpty(strings.TrimSpace(agent.ChannelKind), "-")
		status := firstNonEmpty(strings.TrimSpace(agent.Status), "-")
		health := firstNonEmpty(strings.TrimSpace(agent.Health), "-")
		parts := []string{channel, status, health}
		if mode := strings.TrimSpace(agent.TailnetMode); mode != "" {
			parts = append(parts, "tailnet:"+mode)
		}
		line := fmt.Sprintf("%d. %s (%s)", info.Start+i+1, agentID, strings.Join(parts, " | "))
		if reason := strings.TrimSpace(agent.ChildRuntimeBlockedReason); reason != "" {
			line += "; blocked: " + truncateOperatorLine(reason, 120)
		}
		if !agent.LastWakeAt.IsZero() {
			line += "; last wake " + agent.LastWakeAt.UTC().Format("2006-01-02 15:04Z")
		}
		details = append(details, line)
		if agent.PolicyVersion > 0 {
			evidence = append(evidence, fmt.Sprintf("%s policy version %d", agentID, agent.PolicyVersion))
		}
		if strings.TrimSpace(agent.EnrollmentStatus) != "" {
			evidence = append(evidence, fmt.Sprintf("%s enrollment: %s", agentID, strings.TrimSpace(agent.EnrollmentStatus)))
		}
		rows = append(rows, []telegram.InlineButton{
			{Text: fmt.Sprintf("Chat %d", info.Start+i+1), CallbackData: encodeDurableAgentsStartCallbackData(agentID)},
		})
	}
	rows = append(rows, telegramPageNavigationRows(info, telegramPageSurfaceAgents, telegramPageViewList)...)
	rows = append(rows, []telegram.InlineButton{
		{Text: "Refresh", CallbackData: encodeDurableAgentsRefreshCallbackData()},
	})
	state := fmt.Sprintf("%d configured", len(agents))
	if info.PageCount > 1 {
		state = fmt.Sprintf("%d configured; page %d of %d", len(agents), info.Page, info.PageCount)
	}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Durable Agents",
		State:    state,
		Why:      "Each child keeps its own bounded state and can only act through declared grants and policy.",
		Next:     "Tap a numbered Chat button for a bounded parent-child check-in, or refresh after policy changes.",
		Details:  details,
		Evidence: evidence,
	}, durableAgentsPageSize, 2), rows
}

func handleDurableAgentsCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action durableAgentsCallbackAction, agentID string) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	senderID := int64(0)
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
		}
	}
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleAgentsCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Durable-agent controls are admin only."); err != nil {
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

	switch action {
	case durableAgentsCallbackRefresh:
		agents, err := router.DurableAgentsList(senderID)
		if err != nil {
			return true, err
		}
		rendered, rows := renderDurableAgentsCommand(agents)
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
			return true, err
		}
		return true, nil
	case durableAgentsCallbackStart:
		note, err := router.StartDurableAgentConversation(ctx, chatID, senderID, strings.TrimSpace(agentID))
		if err != nil {
			return true, err
		}
		if strings.TrimSpace(note) == "" {
			note = fmt.Sprintf("Started background conversation with durable agent %s.", strings.TrimSpace(agentID))
		}
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID:  chatID,
			Text:    strings.TrimSpace(note),
			ReplyTo: replyToMessageID(messageID),
		}); err != nil {
			return true, err
		}
		return true, nil
	default:
		return true, nil
	}
}
