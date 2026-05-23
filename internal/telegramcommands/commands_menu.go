//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

const commandMenuCallbackPrefix = "menu:"
const staleCommandMenuCallbackText = "This command menu is no longer active. Run /help again."

var commandMenuPublicCommands = []struct {
	Label   string
	Command string
}{
	{Label: "Status", Command: "status"},
	{Label: "Health", Command: "health"},
	{Label: "Memory", Command: "memory"},
	{Label: "Threads", Command: "threads"},
	{Label: "Mission", Command: "mission"},
	{Label: "Stop", Command: "stop"},
	{Label: "New", Command: "new"},
	{Label: "Detach", Command: "detach"},
}

var commandMenuAdminCommands = []struct {
	Label   string
	Command string
}{
	{Label: "Models", Command: "model"},
	{Label: "Agents", Command: "agents"},
	{Label: "Tailnet", Command: "tailnet"},
	{Label: "Reinstall", Command: "reinstall"},
	{Label: "Restart", Command: "restart"},
}

func commandMenuRows(includeAdmin bool) [][]telegram.InlineButton {
	items := append([]struct {
		Label   string
		Command string
	}{}, commandMenuPublicCommands...)
	if includeAdmin {
		items = append(items, commandMenuAdminCommands...)
	}
	rows := make([][]telegram.InlineButton, 0, (len(items)+1)/2)
	for i := 0; i < len(items); i += 2 {
		row := []telegram.InlineButton{{
			Text:         items[i].Label,
			CallbackData: encodeCommandMenuCallbackData(items[i].Command),
		}}
		if i+1 < len(items) {
			row = append(row, telegram.InlineButton{
				Text:         items[i+1].Label,
				CallbackData: encodeCommandMenuCallbackData(items[i+1].Command),
			})
		}
		rows = append(rows, row)
	}
	return rows
}

func encodeCommandMenuCallbackData(command string) string {
	return commandMenuCallbackPrefix + strings.TrimSpace(command)
}

func decodeCommandMenuCallbackData(data string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, commandMenuCallbackPrefix) {
		return "", false
	}
	command := strings.TrimSpace(strings.TrimPrefix(trimmed, commandMenuCallbackPrefix))
	if command == "" {
		return "", false
	}
	for _, item := range commandMenuPublicCommands {
		if item.Command == command {
			return command, true
		}
	}
	for _, item := range commandMenuAdminCommands {
		if item.Command == command {
			return command, true
		}
	}
	return "", false
}

func handleCommandMenuCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, command string) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	senderID := int64(0)
	chatType := ""
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
			chatType = cb.Message.Chat.Type
		}
	}
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if chatID == 0 || messageID == 0 || senderID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleCommandMenuCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !commandMenuAllows(command, router.CanRestart(senderID)) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "This command is available to Telegram admins only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	return handleTelegramCommand(ctx, sender, router, core.InboundMessage{
		ChatID:    chatID,
		SenderID:  senderID,
		MessageID: messageID,
		ChatType:  chatType,
		Text:      "/" + command,
	})
}

func commandMenuAllows(command string, isAdmin bool) bool {
	for _, item := range commandMenuPublicCommands {
		if item.Command == command {
			return true
		}
	}
	for _, item := range commandMenuAdminCommands {
		if item.Command == command {
			return isAdmin
		}
	}
	return false
}
