//go:build linux

package telegram

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const replyContextSnippetMaxRunes = 320

func inboundMessageText(msg *Message, allowReplyContextWithoutBase bool) string {
	if msg == nil {
		return ""
	}
	base := strings.TrimSpace(firstNonEmpty(msg.Text, msg.Caption))
	replyContext := inboundReplyContext(msg)
	if replyContext == "" {
		return base
	}
	if base == "" {
		if !allowReplyContextWithoutBase {
			return ""
		}
		return "Reply context:\n" + replyContext
	}
	return strings.Join([]string{
		base,
		"",
		"Reply context:",
		replyContext,
	}, "\n")
}

func inboundReplyToMessageID(msg *Message) *int64 {
	if msg == nil || msg.ReplyToMessage == nil || msg.ReplyToMessage.MessageID == 0 {
		return nil
	}
	id := msg.ReplyToMessage.MessageID
	return &id
}

func inboundReplyContext(msg *Message) string {
	if msg == nil || msg.ReplyToMessage == nil {
		return ""
	}
	reply := msg.ReplyToMessage
	text := strings.TrimSpace(firstNonEmpty(reply.Text, reply.Caption))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	text = truncateReplyContext(text, replyContextSnippetMaxRunes)
	if text == "" {
		return ""
	}
	if sender := strings.TrimSpace(buildSenderName(reply.From)); sender != "" {
		return fmt.Sprintf("%s: %s", sender, text)
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncateReplyContext(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}
