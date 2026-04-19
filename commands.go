//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
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
	commandSender
	AnswerCallbackQuery(ctx context.Context, id string, text string) error
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
	EditMessageTextWithInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string, rows [][]telegram.InlineButton) error
}

type commandRouter interface {
	Stop(chatID int64) core.StopResult
	Detach(chatID int64, senderID int64) (core.DetachResult, error)
	Restart(chatID int64) error
	CanRestart(senderID int64) bool
	Status(chatID int64) core.SessionStatus
	StatusChat(chatID int64) (core.ChatStatusSnapshot, error)
	StatusSystem(senderID int64) (core.SystemStatusSnapshot, error)
	StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error)
	StatusReadableSummary(ctx context.Context, view string, statusText string) string
	ContinuationState(chatID int64) (session.ContinuationState, error)
	ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error)
	StopContinuation(chatID int64) (core.StopResult, error)
	TriggerContinuation(ctx context.Context, chatID int64) error
	QueueReinstall(ctx context.Context, msg core.InboundMessage) error
	CurrentEfforts() (persona string, governor string)
	CurrentPersonaModel() string
	PersonaModelOptions() []string
	SetPersonaModel(model string) (string, error)
	GovernorEffortOptions() []string
	SetGovernorEffort(effort string) (string, error)
	RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error)
}

var defaultTelegramCommands = []telegram.BotCommand{
	{Command: "start", Description: "Show intro and command help"},
	{Command: "help", Description: "Show available commands"},
	{Command: "status", Description: "Show live status and controls"},
	{Command: "debug", Description: "Show a detailed debug snapshot"},
	{Command: "stop", Description: "Stop current work in this chat"},
	{Command: "detach", Description: "Detach from pending work in this chat"},
	{Command: "restart", Description: "Force an immediate gateway restart"},
	{Command: "reinstall", Description: "Queue a rebuild/reinstall/restart request"},
	{Command: "set_persona_model", Description: "Choose Idolum persona model"},
	{Command: "set_governor_effort", Description: "Choose governor reasoning effort"},
}

const debugCallbackPrefix = "debug:"
const staleDebugCallbackText = "This debug action is no longer available. Run /debug again."

type debugView string

const (
	debugViewMore debugView = "more"
)

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
	isAdmin := router.CanRestart(msg.SenderID)
	var text string
	restartRequested := false
	switch command {
	case "start":
		text = face.RenderTelegramStart(personaEffort, governorEffort, isAdmin)
	case "help":
		text = face.RenderTelegramHelp(personaEffort, governorEffort, isAdmin)
	case "status":
		rendered, rows, renderErr := renderStatusView(ctx, router, msg.ChatID, msg.SenderID, statusViewChat, msg.ChatID, personaEffort, governorEffort)
		if renderErr != nil {
			return true, renderErr
		}
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "debug":
		quickText, fullText, err := renderDebugSnapshot(ctx, router, msg.ChatID, msg.SenderID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		if strings.TrimSpace(fullText) == "" {
			fullText = "debug_scope=chat\nsummary unavailable"
		}
		if strings.TrimSpace(quickText) == "" {
			quickText = "quick_read unavailable. Tap Read More for the full debug snapshot."
		}
		rows := [][]telegram.InlineButton{{
			{Text: "Read More", CallbackData: encodeDebugCallbackData(debugViewMore)},
		}}
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, quickText, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "stop":
		text = face.RenderTelegramStop(router.Stop(msg.ChatID))
	case "detach":
		detached, detachErr := router.Detach(msg.ChatID, msg.SenderID)
		if detachErr != nil {
			return true, detachErr
		}
		text = face.RenderTelegramDetach(detached)
	case "restart":
		if isAdmin {
			text = face.RenderTelegramRestart()
			restartRequested = true
		} else {
			text = face.RenderTelegramRestartDenied()
		}
	case "reinstall":
		if err := router.QueueReinstall(ctx, msg); err != nil {
			return true, err
		}
		text = face.RenderTelegramQueuedReinstall()
	case "set_persona_model":
		return sendPersonaModelSelector(ctx, sender, router, msg)
	case "set_governor_effort":
		return sendGovernorEffortSelector(ctx, sender, router, msg)
	default:
		return false, nil
	}

	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    text,
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if restartRequested {
		if restartErr := router.Restart(msg.ChatID); restartErr != nil {
			return true, restartErr
		}
	}
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
	if runID, action, ok := core.DecodeDeliberationControlCallbackData(cb.Data); ok {
		return handleDeliberationControlCallback(ctx, sender, router, cb, runID, action)
	}
	if view, ok := decodeDebugCallbackData(cb.Data); ok {
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
		if chatID == 0 || messageID == 0 || view != debugViewMore {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleDebugCallbackText); err != nil {
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
		personaEffort, governorEffort := router.CurrentEfforts()
		_, fullText, err := renderDebugSnapshot(ctx, router, chatID, senderID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		if err := deliverDebugCallbackView(ctx, sender, chatID, messageID, fullText); err != nil {
			return true, err
		}
		return true, nil
	}
	if view, targetChatID, ok := decodeStatusCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil {
			messageID = cb.Message.MessageID
			if cb.Message.Chat != nil {
				chatID = cb.Message.Chat.ID
			}
		}
		senderID := int64(0)
		if cb.From != nil {
			senderID = cb.From.ID
		}
		if statusViewRequiresAdmin(view, chatID, targetChatID) && !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminStatusOnlyText); err != nil {
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
		personaEffort, governorEffort := router.CurrentEfforts()
		rendered, rows, err := renderStatusView(ctx, router, chatID, senderID, view, targetChatID, personaEffort, governorEffort)
		if err != nil {
			return true, err
		}
		if chatID == 0 {
			chatID = targetChatID
		}
		if chatID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if err := deliverStatusCallbackView(ctx, sender, chatID, messageID, rendered, rows); err != nil {
			return true, err
		}
		return true, nil
	}
	if decisionID, action, ok := decodeContinuationCallbackData(cb.Data); ok {
		chatID := int64(0)
		messageID := int64(0)
		if cb.Message != nil && cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
			messageID = cb.Message.MessageID
		}
		state, err := router.ContinuationState(chatID)
		if err != nil {
			return true, err
		}
		if !continuationCallbackMatchesState(state, decisionID, action) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleContinuationCallbackText); err != nil {
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
			if err := router.TriggerContinuation(ctx, chatID); err != nil {
				return true, err
			}
			text = renderContinuationDecision(state, true)
			if messageID != 0 {
				if err := sender.EditMessageText(ctx, chatID, messageID, text, ""); err != nil {
					log.Printf("WARN continuation approve message update failed chat_id=%d message_id=%d err=%v", chatID, messageID, err)
				}
			}
			return true, nil
		case "stop":
			stopped, err := router.StopContinuation(chatID)
			if err != nil {
				return true, err
			}
			text = face.RenderTelegramStop(stopped)
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
	if action, step, option, ok := decodeDurableWizardCallbackData(cb.Data); ok {
		return handleDurableWizardCallback(ctx, sender, router, cb, action, step, option)
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

func renderDebugSnapshot(ctx context.Context, router commandRouter, chatID int64, senderID int64, personaEffort string, governorEffort string) (string, string, error) {
	chat, err := router.StatusChat(chatID)
	if err != nil {
		return "", "", err
	}
	var (
		system   *core.SystemStatusSnapshot
		durables *core.DurableAgentsStatusSnapshot
	)
	if router.CanRestart(senderID) {
		sys, err := router.StatusSystem(senderID)
		if err != nil {
			return "", "", err
		}
		system = &sys
		durs, err := router.StatusDurables(senderID)
		if err != nil {
			return "", "", err
		}
		durables = &durs
	}
	full := face.RenderTelegramDebug(chat, system, durables, personaEffort, governorEffort)
	full = strings.TrimSpace(full)
	summary := strings.TrimSpace(router.StatusReadableSummary(ctx, "debug", full))
	quick := ""
	if summary != "" {
		quick = "quick_read " + summary
		full = "quick_read " + summary + "\n\n" + full
	}
	return quick, full, nil
}

func encodeDebugCallbackData(view debugView) string {
	switch view {
	case debugViewMore:
		return debugCallbackPrefix + "more"
	default:
		return debugCallbackPrefix + "more"
	}
}

func decodeDebugCallbackData(data string) (debugView, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, debugCallbackPrefix) {
		return "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, debugCallbackPrefix))
	switch payload {
	case "more":
		return debugViewMore, true
	default:
		return "", false
	}
}

func deliverDebugCallbackView(ctx context.Context, sender commandCallbackSender, chatID int64, messageID int64, text string) error {
	if sender == nil {
		return nil
	}
	chunks := splitStatusTextChunks(text, statusMessageChunkLimit)
	if len(chunks) == 0 {
		chunks = []string{"debug_scope=chat\nsummary unavailable"}
	}
	first := chunks[0]
	if messageID != 0 {
		if err := sender.EditMessageText(ctx, chatID, messageID, first, ""); err != nil {
			return err
		}
	} else {
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   first,
		}); err != nil {
			return err
		}
	}
	for i := 1; i < len(chunks); i++ {
		if _, err := sender.SendMessage(ctx, core.OutboundMessage{
			ChatID: chatID,
			Text:   chunks[i],
		}); err != nil {
			return err
		}
	}
	return nil
}
