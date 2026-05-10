//go:build linux

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	durableAgentsCallbackPrefix = "agents:"
	staleAgentsCallbackText     = "This durable-agent action is no longer active. Run /agents again."
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
	lines := []string{
		"Durable Agents",
		"",
	}
	rows := make([][]telegram.InlineButton, 0, len(agents)+1)
	if len(agents) == 0 {
		lines = append(lines, "No durable agents are currently configured.")
		rows = append(rows, []telegram.InlineButton{
			{Text: "Refresh", CallbackData: encodeDurableAgentsRefreshCallbackData()},
		})
		return strings.Join(lines, "\n"), rows
	}
	for _, agent := range agents {
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
		lines = append(lines, fmt.Sprintf("- %s (%s)", agentID, strings.Join(parts, " | ")))
		rows = append(rows, []telegram.InlineButton{
			{Text: "Chat", CallbackData: encodeDurableAgentsStartCallbackData(agentID)},
		})
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "Refresh", CallbackData: encodeDurableAgentsRefreshCallbackData()},
	})
	return strings.Join(lines, "\n"), rows
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
