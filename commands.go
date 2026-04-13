//go:build linux

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

type commandSender interface {
	SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error)
}

type commandRouter interface {
	Stop(chatID int64) core.StopResult
	Status(chatID int64) core.SessionStatus
	TogglePersonaEffort() (string, error)
	ToggleGovernorEffort() (string, error)
	CurrentEfforts() (persona string, governor string)
}

var defaultTelegramCommands = []telegram.BotCommand{
	{Command: "start", Description: "Show intro and command help"},
	{Command: "help", Description: "Show available commands"},
	{Command: "status", Description: "Show current work state"},
	{Command: "stop", Description: "Stop current work in this chat"},
	{Command: "toggle_persona_effort", Description: "Switch Idolum between Sonnet and Opus"},
	{Command: "toggle_governor_effort", Description: "Switch governor effort between medium and high"},
}

func registerTelegramCommands(ctx context.Context, client *telegram.Client) error {
	if client == nil {
		return fmt.Errorf("telegram client is required")
	}
	return client.SetMyCommands(ctx, defaultTelegramCommands)
}

func handleTelegramCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false, nil
	}
	command, ok := parseTelegramCommand(msg.Text)
	if !ok {
		return false, nil
	}

	var text string
	switch command {
	case "start":
		personaEffort, governorEffort := router.CurrentEfforts()
		text = strings.Join([]string{
			"Aphelion is active.",
			"",
			"Available commands:",
			"/help - show command help",
			"/status - show whether I am currently working",
			"/stop - stop current work in this chat",
			"/toggle_persona_effort - switch Idolum between Sonnet and Opus",
			"/toggle_governor_effort - switch governor effort between medium and high",
			"",
			fmt.Sprintf("Current persona effort: %s", personaEffort),
			fmt.Sprintf("Current governor effort: %s", governorEffort),
		}, "\n")
	case "help":
		personaEffort, governorEffort := router.CurrentEfforts()
		text = strings.Join([]string{
			"Commands:",
			"/start - show intro and command help",
			"/help - show this help",
			"/status - show current work state",
			"/stop - stop current work in this chat",
			"/toggle_persona_effort - switch Idolum between Sonnet and Opus",
			"/toggle_governor_effort - switch governor effort between medium and high",
			"",
			fmt.Sprintf("Current persona effort: %s", personaEffort),
			fmt.Sprintf("Current governor effort: %s", governorEffort),
		}, "\n")
	case "status":
		status := router.Status(msg.ChatID)
		personaEffort, governorEffort := router.CurrentEfforts()
		state := "idle"
		if status.Active {
			state = "working"
		}
		text = fmt.Sprintf("Current state: %s.", state)
		if status.Queued {
			text += "\nA newer message is queued behind the current turn."
		}
		text += fmt.Sprintf("\nPersona effort: %s.", personaEffort)
		text += fmt.Sprintf("\nGovernor effort: %s.", governorEffort)
	case "stop":
		stopped := router.Stop(msg.ChatID)
		switch {
		case stopped.ActiveCanceled && stopped.QueuedDropped:
			text = "Stopped the current turn and cleared queued work for this chat."
		case stopped.ActiveCanceled:
			text = "Stopped the current turn."
		case stopped.QueuedDropped:
			text = "Cleared queued work for this chat."
		default:
			text = "There is no active work to stop."
		}
	case "toggle_persona_effort":
		mode, toggleErr := router.TogglePersonaEffort()
		if toggleErr != nil {
			return true, toggleErr
		}
		text = fmt.Sprintf("Idolum persona effort is now %s. Future rendered turns will use the %s recipe.", mode, titleCaseWord(mode))
	case "toggle_governor_effort":
		mode, toggleErr := router.ToggleGovernorEffort()
		if toggleErr != nil {
			return true, toggleErr
		}
		text = fmt.Sprintf("Governor effort is now %s. Future interactive turns will use %s reasoning.", mode, mode)
	default:
		return false, nil
	}

	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    text,
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if err != nil {
		return true, err
	}
	return true, nil
}

func parseTelegramCommand(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || text[0] != '/' {
		return "", false
	}

	token := text
	if idx := strings.IndexAny(token, " \n\t"); idx >= 0 {
		token = token[:idx]
	}
	if len(token) < 2 {
		return "", false
	}

	token = token[1:]
	if at := strings.IndexByte(token, '@'); at >= 0 {
		token = token[:at]
	}
	if token == "" {
		return "", false
	}
	for i, r := range token {
		if i == 0 {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
				return "", false
			}
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return "", false
	}
	return strings.ToLower(token), true
}

func replyToMessageID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func titleCaseWord(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + strings.ToLower(value[1:])
}
