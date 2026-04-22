//go:build linux

package telegram

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

type durableGroupRoute struct {
	agentID   string
	respondOn string
}

func durableGroupRoutes(groups []config.TelegramDurableGroupConfig) map[int64]durableGroupRoute {
	if len(groups) == 0 {
		return nil
	}
	routes := make(map[int64]durableGroupRoute, len(groups))
	for _, group := range groups {
		agentID := strings.TrimSpace(group.AgentID)
		if group.ChatID == 0 || agentID == "" {
			continue
		}
		routes[group.ChatID] = durableGroupRoute{
			agentID:   agentID,
			respondOn: normalizeGroupRespondOn(group.RespondOn),
		}
	}
	return routes
}

func normalizeDurableGroupMessage(msg *Message, route durableGroupRoute, botUser *User) *core.InboundMessage {
	if msg == nil || msg.Chat == nil {
		return nil
	}
	if msg.Chat.Type != "group" && msg.Chat.Type != "supergroup" {
		return nil
	}
	hasArtifacts := hasNormalizableArtifacts(msg)
	text := inboundMessageText(msg, hasArtifacts)
	if text == "" && !hasArtifacts {
		return nil
	}
	if !durableGroupShouldWake(msg, route, botUser) {
		return nil
	}
	return &core.InboundMessage{
		ChatID:         msg.Chat.ID,
		ChatType:       msg.Chat.Type,
		ChatTitle:      strings.TrimSpace(msg.Chat.Title),
		SenderID:       senderID(msg.From),
		SenderName:     buildSenderName(msg.From),
		Text:           text,
		ReplyTo:        inboundReplyToMessageID(msg),
		MessageID:      msg.MessageID,
		DurableAgentID: route.agentID,
		Timestamp:      time.Unix(msg.Date, 0),
		Raw:            msg.Raw,
	}
}

func durableGroupShouldWake(msg *Message, route durableGroupRoute, botUser *User) bool {
	switch normalizeGroupRespondOn(route.respondOn) {
	case "all":
		return true
	default:
		return addressedToBot(msg, botUser)
	}
}

func normalizeGroupRespondOn(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "all":
		return "all"
	default:
		return "mentions"
	}
}

func addressedToBot(msg *Message, botUser *User) bool {
	if msg == nil {
		return false
	}
	if botUser != nil {
		if reply := msg.ReplyToMessage; reply != nil && reply.From != nil && reply.From.ID == botUser.ID {
			return true
		}
	}
	if mentionedBotUsername(msg.Text, msg.Entities, botUser) {
		return true
	}
	if mentionedBotUsername(msg.Caption, msg.Entities, botUser) {
		return true
	}
	return false
}

func mentionedBotUsername(text string, entities []MessageEntity, botUser *User) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	username := ""
	if botUser != nil {
		username = strings.ToLower(strings.TrimSpace(botUser.Username))
	}
	for _, entity := range entities {
		switch entity.Type {
		case "bot_command":
			if username == "" {
				continue
			}
			value, ok := entityText(text, entity)
			if !ok {
				continue
			}
			if idx := strings.IndexByte(value, '@'); idx >= 0 && strings.ToLower(strings.TrimSpace(value[idx+1:])) == username {
				return true
			}
		case "mention":
			if username == "" {
				continue
			}
			value, ok := entityText(text, entity)
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(value), "@"), username) {
				return true
			}
		}
	}
	return false
}

func entityText(text string, entity MessageEntity) (string, bool) {
	runes := []rune(text)
	if entity.Offset < 0 || entity.Length <= 0 {
		return "", false
	}
	if entity.Offset >= len(runes) || entity.Offset+entity.Length > len(runes) {
		return "", false
	}
	return string(runes[entity.Offset : entity.Offset+entity.Length]), true
}
