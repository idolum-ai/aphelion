//go:build linux

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

type commandSender interface {
	SendMessage(ctx context.Context, msg core.OutboundMessage) (int64, error)
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}

type commandCallbackSender interface {
	AnswerCallbackQuery(ctx context.Context, id string, text string) error
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type commandRouter interface {
	Stop(chatID int64) core.StopResult
	Status(chatID int64) core.SessionStatus
	ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error)
	RevokeContinuation(chatID int64) (session.ContinuationState, error)
	TriggerContinuation(ctx context.Context, chatID int64) error
	TogglePersonaEffort() (string, error)
	ToggleGovernorEffort() (string, error)
	CurrentEfforts() (persona string, governor string)
	CurrentPersonaModel() string
	PersonaModelOptions() []string
	SetPersonaModel(model string) (string, error)
	GovernorEffortOptions() []string
	SetGovernorEffort(effort string) (string, error)
}

var defaultTelegramCommands = []telegram.BotCommand{
	{Command: "start", Description: "Show intro and command help"},
	{Command: "help", Description: "Show available commands"},
	{Command: "status", Description: "Show current work state"},
	{Command: "stop", Description: "Stop current work in this chat"},
	{Command: "set_persona_model", Description: "Choose Idolum persona model"},
	{Command: "set_governor_effort", Description: "Choose governor reasoning effort"},
	{Command: "toggle_persona_effort", Description: "Quick-toggle Idolum between Sonnet and Opus"},
	{Command: "toggle_governor_effort", Description: "Quick-toggle governor effort between medium and high"},
}

const recipeCallbackPrefix = "recipe:"
const continuationCallbackPrefix = "continuation:"

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

	personaEffort, governorEffort := router.CurrentEfforts()
	var text string
	switch command {
	case "start":
		text = face.RenderTelegramStart(personaEffort, governorEffort)
	case "help":
		text = face.RenderTelegramHelp(personaEffort, governorEffort)
	case "status":
		text = face.RenderTelegramStatus(router.Status(msg.ChatID), personaEffort, governorEffort)
	case "stop":
		text = face.RenderTelegramStop(router.Stop(msg.ChatID))
	case "set_persona_model":
		return sendPersonaModelSelector(ctx, sender, router, msg)
	case "set_governor_effort":
		return sendGovernorEffortSelector(ctx, sender, router, msg)
	case "toggle_persona_effort":
		mode, toggleErr := router.TogglePersonaEffort()
		if toggleErr != nil {
			return true, toggleErr
		}
		text = face.RenderTelegramTogglePersona(mode)
	case "toggle_governor_effort":
		mode, toggleErr := router.ToggleGovernorEffort()
		if toggleErr != nil {
			return true, toggleErr
		}
		text = face.RenderTelegramToggleGovernor(mode)
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

func sendPersonaModelSelector(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if sender == nil || router == nil {
		return true, fmt.Errorf("command handler unavailable")
	}
	current := strings.TrimSpace(router.CurrentPersonaModel())
	options := router.PersonaModelOptions()
	rows := recipeSelectorRows("persona_model", current, options, personaModelButtonLabel)
	text := strings.TrimSpace(face.RenderTelegramPersonaModelSelector(current, options))
	_, err := sender.SendInlineKeyboard(ctx, msg.ChatID, text, rows, replyToMessageID(msg.MessageID))
	if err != nil {
		return true, err
	}
	return true, nil
}

func sendGovernorEffortSelector(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if sender == nil || router == nil {
		return true, fmt.Errorf("command handler unavailable")
	}
	_, current := router.CurrentEfforts()
	options := router.GovernorEffortOptions()
	rows := recipeSelectorRows("governor_effort", current, options, governorEffortButtonLabel)
	text := strings.TrimSpace(face.RenderTelegramGovernorEffortSelector(current, options))
	_, err := sender.SendInlineKeyboard(ctx, msg.ChatID, text, rows, replyToMessageID(msg.MessageID))
	if err != nil {
		return true, err
	}
	return true, nil
}

func handleTelegramCommandCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	if sender == nil || router == nil {
		return false, nil
	}
	if action, ok := decodeContinuationCallbackData(cb.Data); ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil && cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
			messageID = cb.Message.MessageID
		}
		var text string
		switch action {
		case "approve":
			approverID := int64(0)
			if cb.From != nil {
				approverID = cb.From.ID
			}
			state, err := router.ApproveContinuation(chatID, approverID)
			if err != nil {
				return true, err
			}
			text = renderContinuationDecision(state, true)
			if messageID != 0 {
				if err := sender.EditMessageText(ctx, chatID, messageID, text, ""); err != nil {
					return true, err
				}
			}
			if err := router.TriggerContinuation(ctx, chatID); err != nil {
				return true, err
			}
			return true, nil
		case "stop":
			state, err := router.RevokeContinuation(chatID)
			if err != nil {
				return true, err
			}
			text = renderContinuationDecision(state, false)
			if messageID != 0 {
				if err := sender.EditMessageText(ctx, chatID, messageID, text, ""); err != nil {
					return true, err
				}
			}
			return true, nil
		default:
			return true, nil
		}
	}
	kind, value, ok := decodeRecipeCallbackData(cb.Data)
	if !ok {
		return false, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil {
		if !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
	}

	var (
		text string
		err  error
	)
	switch kind {
	case "persona_model":
		var selected string
		selected, err = router.SetPersonaModel(value)
		if err == nil {
			text = face.RenderTelegramSetPersonaModel(selected)
		}
	case "governor_effort":
		var selected string
		selected, err = router.SetGovernorEffort(value)
		if err == nil {
			text = face.RenderTelegramSetGovernorEffort(selected)
		}
	default:
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if cb.Message != nil && cb.Message.Chat != nil && cb.Message.MessageID != 0 {
		if editErr := sender.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, ""); editErr != nil {
			return true, editErr
		}
	}
	return true, nil
}

func recipeSelectorRows(kind string, current string, options []string, labelFor func(string) string) [][]telegram.InlineButton {
	rows := make([][]telegram.InlineButton, 0, 2)
	row := make([]telegram.InlineButton, 0, 2)
	current = strings.ToLower(strings.TrimSpace(current))
	for _, option := range options {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		label := labelFor(option)
		if strings.EqualFold(option, current) {
			label = "• " + label
		}
		row = append(row, telegram.InlineButton{
			Text:         label,
			CallbackData: encodeRecipeCallbackData(kind, option),
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = make([]telegram.InlineButton, 0, 2)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

func encodeRecipeCallbackData(kind string, value string) string {
	return recipeCallbackPrefix + strings.TrimSpace(kind) + ":" + strings.TrimSpace(value)
}

func decodeRecipeCallbackData(data string) (kind string, value string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, recipeCallbackPrefix) {
		return "", "", false
	}
	parts := strings.SplitN(trimmed, ":", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	kind = strings.TrimSpace(parts[1])
	value = strings.TrimSpace(parts[2])
	if kind == "" || value == "" {
		return "", "", false
	}
	return kind, value, true
}

func continuationApprovalRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Stop", CallbackData: encodeContinuationCallbackData("stop")},
		{Text: "Continue", CallbackData: encodeContinuationCallbackData("approve")},
	}}
}

func encodeContinuationCallbackData(action string) string {
	return continuationCallbackPrefix + strings.TrimSpace(action)
}

func decodeContinuationCallbackData(data string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, continuationCallbackPrefix) {
		return "", false
	}
	action := strings.TrimSpace(strings.TrimPrefix(trimmed, continuationCallbackPrefix))
	if action == "" {
		return "", false
	}
	return action, true
}

func renderContinuationDecision(state session.ContinuationState, approved bool) string {
	if approved {
		text := "Continuation approved."
		if state.RemainingTurns > 0 {
			text += fmt.Sprintf(" Remaining turns: %d.", state.RemainingTurns)
		}
		if state.StageSummary != "" {
			text += " Next: " + state.StageSummary
		}
		return text
	}
	if state.Status == session.ContinuationStatusRevoked {
		return "Stopped the current continuation and revoked approval for this chat."
	}
	return "There was no active continuation approval to revoke."
}

func personaModelButtonLabel(model string) string {
	trimmed := strings.TrimSpace(model)
	switch strings.ToLower(trimmed) {
	case "claude-opus-4-7":
		return "Opus 4.7"
	case "claude-opus-4-6":
		return "Opus 4.6"
	case "claude-sonnet-4-6":
		return "Sonnet 4.6"
	default:
		return trimmed
	}
}

func governorEffortButtonLabel(effort string) string {
	return strings.ToUpper(strings.TrimSpace(effort))
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
