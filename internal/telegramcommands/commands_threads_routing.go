//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func resolveTelegramThreadStartCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (core.InboundMessage, bool, bool, error) {
	if msg.TelegramThreadID > 0 || strings.TrimSpace(msg.DurableAgentID) != "" {
		return msg, false, false, nil
	}
	command, ok := parseTelegramCommand(msg.Text)
	if !ok || command != "thread" {
		return msg, false, false, nil
	}
	text := strings.TrimSpace(telegramCommandArgs(msg.Text))
	if text == "" {
		return msg, false, false, nil
	}
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		_, err := sendTelegramThreadText(ctx, sender, msg, "Thread controls are unavailable.")
		return msg, false, true, err
	}
	routed, _, err := threadRouter.StartTelegramThreadTarget(ctx, msg, text)
	if err != nil {
		if isTelegramThreadUserError(err) {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, err.Error())
			return msg, false, true, sendErr
		}
		return msg, false, true, err
	}
	return routed, true, false, nil
}
func resolveTelegramThreadPrefix(ctx context.Context, sender commandSender, router commandThreadRouter, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	threadID, text, ok := parseTelegramThreadPrefix(msg.Text)
	if !ok {
		return msg, false, nil
	}
	threadID, err := resolveTelegramThreadTargetID(router, msg.ChatID, threadID)
	if err != nil {
		return msg, true, err
	}
	if strings.TrimSpace(text) == "" {
		_, err := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Add a message after `(thread %d)`.", threadID))
		return msg, true, err
	}
	if command, ok := parseTelegramCommand(text); ok {
		if !telegramThreadLaneCommand(command) {
			_, err := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("/%s is a global command. Run it without `(thread %d)`.", command, threadID))
			return msg, true, err
		}
		thread, ok, err := router.TelegramThread(msg.ChatID, threadID)
		if err != nil {
			return msg, true, err
		}
		if !ok {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d does not exist. Start a new side thread with `/thread <message>`.", threadID))
			return msg, true, sendErr
		}
		if !thread.Open() {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", threadID))
			return msg, true, sendErr
		}
		routed := msg
		routed.TelegramThreadID = threadID
		routed.Text = text
		return routed, false, nil
	}
	routed, _, err := router.TargetTelegramThreadMessage(ctx, msg, threadID, text)
	if err != nil {
		if isTelegramThreadUserError(err) {
			_, sendErr := sendTelegramThreadText(ctx, sender, msg, err.Error())
			return msg, true, sendErr
		}
		return msg, true, err
	}
	return routed, false, nil
}
func telegramThreadLaneCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "status", "stop", "new", "detach", "memory":
		return true
	default:
		return false
	}
}
func resolveTelegramThreadCommandTarget(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage, command string) (core.InboundMessage, bool, error) {
	if msg.TelegramThreadID > 0 || !telegramThreadLaneCommand(command) || msg.ReplyTo == nil || *msg.ReplyTo <= 0 {
		return msg, false, nil
	}
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return msg, false, nil
	}
	thread, ok, err := threadRouter.TelegramThreadForReplyMessage(msg.ChatID, *msg.ReplyTo)
	if err != nil {
		return msg, true, err
	}
	if !ok {
		return msg, false, nil
	}
	if !thread.Open() {
		_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", thread.ThreadID))
		return msg, true, sendErr
	}
	if err := threadRouter.MarkTelegramThreadReminderResumed(msg.ChatID, *msg.ReplyTo); err != nil {
		return msg, true, err
	}
	routed := msg
	routed.TelegramThreadID = thread.ThreadID
	return routed, false, nil
}
func resolveTelegramThreadReply(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (core.InboundMessage, bool, error) {
	if msg.TelegramThreadID > 0 || msg.ReplyTo == nil || *msg.ReplyTo <= 0 || strings.TrimSpace(msg.DurableAgentID) != "" {
		return msg, false, nil
	}
	if _, ok := parseTelegramCommand(msg.Text); ok {
		return msg, false, nil
	}
	threadRouter, ok := router.(commandThreadRouter)
	if !ok {
		return msg, false, nil
	}
	thread, ok, err := threadRouter.TelegramThreadForReplyMessage(msg.ChatID, *msg.ReplyTo)
	if err != nil {
		return msg, true, err
	}
	if !ok {
		return msg, false, nil
	}
	if !thread.Open() {
		_, sendErr := sendTelegramThreadText(ctx, sender, msg, fmt.Sprintf("Thread %d is closed. Start a new side thread with `/thread <message>`.", thread.ThreadID))
		return msg, true, sendErr
	}
	if err := threadRouter.MarkTelegramThreadReminderResumed(msg.ChatID, *msg.ReplyTo); err != nil {
		return msg, true, err
	}
	routed := msg
	routed.TelegramThreadID = thread.ThreadID
	return routed, false, nil
}
func parseTelegramThreadPrefix(text string) (int64, string, bool) {
	matches := telegramThreadPrefixPattern.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) != 2 {
		return 0, "", false
	}
	threadID, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil || threadID <= 0 {
		return 0, "", false
	}
	rest := telegramThreadPrefixPattern.ReplaceAllString(strings.TrimSpace(text), "")
	return threadID, strings.TrimSpace(rest), true
}
func parseTelegramThreadIDArg(raw string) (int64, bool) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) == 0 {
		return 0, false
	}
	threadID, err := strconv.ParseInt(fields[0], 10, 64)
	return threadID, err == nil && threadID > 0
}
func sendTelegramThreadText(ctx context.Context, sender commandSender, msg core.InboundMessage, text string) (bool, error) {
	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    strings.TrimSpace(text),
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	return true, err
}
