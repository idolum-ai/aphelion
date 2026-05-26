//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

const telegramAgentsAnalyzeIngressSurface = "telegram:callback-work:agents-analyze"

func handleDurableAgentsCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, req durableAgentsCallbackRequest) (bool, error) {
	chatID := callbackChatID(cb)
	messageID := callbackMessageID(cb)
	senderID := callbackSenderID(cb)
	if chatID == 0 || messageID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleAgentsCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Durable-agent controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	switch req.Action {
	case durableAgentsCallbackRefresh, durableAgentsCallbackBack:
		return handleDurableAgentsListCallback(ctx, sender, router, cb, chatID, messageID, senderID, req)
	case durableAgentsCallbackAnalyze:
		return handleDurableAgentsAnalyzeCallback(ctx, sender, router, cb, chatID, messageID, senderID)
	default:
		return handleDurableAgentTargetCallback(ctx, sender, router, cb, chatID, messageID, senderID, req)
	}
}

func handleDurableAgentsListCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, req durableAgentsCallbackRequest) (bool, error) {
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	agents, err := router.DurableAgentsList(senderID)
	if err != nil {
		return true, err
	}
	rendered, rows := renderDurableAgentsCommandViewPage(agents, req.View, req.Page)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "agents."+string(req.Action)+".edit", err)
		return true, err
	}
	return true, nil
}

func handleDurableAgentsAnalyzeCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64) (bool, error) {
	text, err := router.QueueDurableAgentAnalyze(ctx, core.InboundMessage{
		ChatID:          chatID,
		SenderID:        senderID,
		MessageID:       messageID,
		IngressSurface:  telegramAgentsAnalyzeIngressSurface,
		IngressUpdateID: cb.UpdateID,
		Text:            "/agents analyze",
	})
	if err != nil {
		recordTelegramCallbackError(router, chatID, "agents.analyze", err)
		return true, err
	}
	if strings.TrimSpace(text) == "" {
		text = "Agent board analysis queued."
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	return true, nil
}

func handleDurableAgentTargetCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, req durableAgentsCallbackRequest) (bool, error) {
	agents, err := router.DurableAgentsList(senderID)
	if err != nil {
		return true, err
	}
	agent, ok := resolveDurableAgentCallbackToken(agents, req.Token)
	if !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleAgentsCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	agentID := strings.TrimSpace(agent.AgentID)
	switch req.Action {
	case durableAgentsCallbackDetail:
		return handleDurableAgentDetailCallback(ctx, sender, router, cb, chatID, messageID, agent, req)
	case durableAgentsCallbackBrief:
		return handleDurableAgentBriefCallback(ctx, sender, router, cb, chatID, messageID, senderID, agentID)
	case durableAgentsCallbackPark, durableAgentsCallbackResume:
		return handleDurableAgentLifecycleCallback(ctx, sender, router, cb, chatID, messageID, senderID, agentID, string(req.Action))
	case durableAgentsCallbackRetireAsk:
		return handleDurableAgentRetireAskCallback(ctx, sender, router, cb, chatID, messageID, agent, req)
	case durableAgentsCallbackRetireConfirm:
		return handleDurableAgentLifecycleCallback(ctx, sender, router, cb, chatID, messageID, senderID, agentID, "retire")
	case durableAgentsCallbackRetireBrief:
		return handleDurableAgentRetireBriefCallback(ctx, sender, router, cb, chatID, messageID, senderID, agent, req)
	case durableAgentsCallbackRetireCancel:
		return handleDurableAgentDetailCallback(ctx, sender, router, cb, chatID, messageID, agent, req)
	default:
		return true, nil
	}
}

func handleDurableAgentDetailCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, agent core.DurableAgentStatusSnapshot, req durableAgentsCallbackRequest) (bool, error) {
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Agent opened."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	rendered, rows := renderDurableAgentDetail(agent, req.View, req.Page)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "agents.detail.edit", err)
		return true, err
	}
	if err := router.RecordTelegramAgentCallbackMessage(chatID, agent.AgentID, messageID, "agent_detail"); err != nil {
		return true, err
	}
	return true, nil
}

func handleDurableAgentBriefCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, agentID string) (bool, error) {
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Brief requested."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	note, err := router.StartDurableAgentConversation(ctx, chatID, senderID, agentID)
	if err != nil {
		recordTelegramCallbackError(router, chatID, "agents.brief", err)
		return true, err
	}
	if strings.TrimSpace(note) == "" {
		note = fmt.Sprintf("Started background conversation with durable agent %s.", agentID)
	}
	sentID, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  chatID,
		Text:    durableAgentPrefixedText(agentID, note),
		ReplyTo: replyToMessageID(messageID),
	})
	if err != nil {
		recordTelegramCallbackError(router, chatID, "agents.brief.send", err)
		return true, err
	}
	if err := router.RecordTelegramAgentCallbackMessage(chatID, agentID, sentID, "agent_brief"); err != nil {
		return true, err
	}
	return true, nil
}

func handleDurableAgentLifecycleCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, agentID string, action string) (bool, error) {
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), durableAgentLifecycleAck(action)); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	text, err := router.DurableAgentLifecycleAction(ctx, chatID, senderID, agentID, action)
	if err != nil {
		recordTelegramCallbackError(router, chatID, "agents."+action, err)
		return true, err
	}
	if strings.TrimSpace(text) == "" {
		text = fmt.Sprintf("durable-agent %s completed for %s", action, agentID)
	}
	if err := router.RecordTelegramAgentCallbackMessage(chatID, agentID, messageID, "agent_"+action); err != nil {
		return true, err
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, durableAgentPrefixedText(agentID, text)); err != nil {
		recordTelegramCallbackError(router, chatID, "agents."+action+".edit", err)
		return true, err
	}
	return true, nil
}

func handleDurableAgentRetireAskCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, agent core.DurableAgentStatusSnapshot, req durableAgentsCallbackRequest) (bool, error) {
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Review retirement."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	rendered, rows := renderDurableAgentRetireConfirm(agent, req.View, req.Page)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "agents.retire_ask.edit", err)
		return true, err
	}
	if err := router.RecordTelegramAgentCallbackMessage(chatID, agent.AgentID, messageID, "agent_retire_confirm"); err != nil {
		return true, err
	}
	return true, nil
}

func handleDurableAgentRetireBriefCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, chatID int64, messageID int64, senderID int64, agent core.DurableAgentStatusSnapshot, req durableAgentsCallbackRequest) (bool, error) {
	if _, err := handleDurableAgentBriefCallback(ctx, sender, router, cb, chatID, messageID, senderID, strings.TrimSpace(agent.AgentID)); err != nil {
		return true, err
	}
	rendered, rows := renderDurableAgentRetireConfirm(agent, req.View, req.Page)
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
		recordTelegramCallbackError(router, chatID, "agents.retire_brief.edit", err)
		return true, err
	}
	return true, nil
}

func durableAgentLifecycleAck(action string) string {
	switch strings.TrimSpace(action) {
	case "park":
		return "Parking agent."
	case "resume":
		return "Resuming agent."
	case "retire":
		return "Retiring agent."
	default:
		return "Updating agent."
	}
}

func durableAgentPrefixedText(agentID string, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "No detail returned."
	}
	return "(agent " + strings.TrimSpace(agentID) + ")\n\n" + text
}
