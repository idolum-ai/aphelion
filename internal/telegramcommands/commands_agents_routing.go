//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func resolveTelegramAgentReply(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if msg.ReplyTo == nil || *msg.ReplyTo <= 0 || strings.TrimSpace(msg.DurableAgentID) != "" || msg.TelegramThreadID > 0 {
		return false, nil
	}
	if _, ok := parseTelegramCommand(msg.Text); ok {
		return false, nil
	}
	agentID, ok, err := router.TelegramAgentIDForReplyMessage(msg.ChatID, *msg.ReplyTo)
	if err != nil {
		return true, err
	}
	if !ok || strings.TrimSpace(agentID) == "" {
		return false, nil
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID:  msg.ChatID,
			Text:    durableAgentPrefixedText(agentID, "Add a message to send to this durable agent."),
			ReplyTo: replyToMessageID(msg.MessageID),
		}); err != nil {
			return true, err
		}
		return true, nil
	}
	note, err := router.SendDurableAgentParentMessage(ctx, msg.ChatID, msg.SenderID, agentID, text)
	if err != nil {
		return true, err
	}
	if strings.TrimSpace(note) == "" {
		note = fmt.Sprintf("Queued parent message for durable agent %s.", strings.TrimSpace(agentID))
	}
	sentID, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    durableAgentPrefixedText(agentID, note),
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if err != nil {
		return true, err
	}
	if err := router.RecordTelegramAgentCallbackMessage(msg.ChatID, agentID, sentID, "agent_reply_ack"); err != nil {
		return true, err
	}
	return true, nil
}
