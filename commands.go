//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

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

type commandInlineKeyboardClearer interface {
	EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type commandRouter interface {
	Stop(chatID int64) core.StopResult
	New(chatID int64, senderID int64) (core.NewSessionResult, error)
	Detach(chatID int64, senderID int64) (core.DetachResult, error)
	Restart(chatID int64) error
	CanRestart(senderID int64) bool
	Status(chatID int64) core.SessionStatus
	StatusChat(chatID int64) (core.ChatStatusSnapshot, error)
	StatusSystem(senderID int64) (core.SystemStatusSnapshot, error)
	StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error)
	StatusReadableSummary(ctx context.Context, view string, statusText string) string
	TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error)
	TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error)
	RevokeTailnetSurface(ctx context.Context, senderID int64, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error)
	ContinuationState(chatID int64) (session.ContinuationState, error)
	ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error)
	StopContinuation(chatID int64) (core.StopResult, error)
	TriggerContinuation(ctx context.Context, chatID int64) error
	QueueReinstall(ctx context.Context, msg core.InboundMessage) error
	QueueDoctor(ctx context.Context, msg core.InboundMessage) error
	CurrentEfforts() (persona string, governor string)
	CurrentPersonaModel() string
	PersonaModelOptions() []string
	SetPersonaModel(model string) (string, error)
	GovernorEffortOptions() []string
	SetGovernorEffort(effort string) (string, error)
	ModelSlotStatuses() ([]core.ModelSlotStatus, error)
	ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation
	SetModelSlotConfig(cfg core.ModelSlotConfig, actor string, reason string, ttl time.Duration) (core.ModelSlotStatus, error)
	RollbackModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error)
	ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error)
	ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error)
	RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error)
	DurableAgentsList(senderID int64) ([]core.DurableAgentStatusSnapshot, error)
	StartDurableAgentConversation(ctx context.Context, chatID int64, senderID int64, agentID string) (string, error)
	MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source memoryReviewSource) (memoryReviewSnapshot, error)
	MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error)
	MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error)
	ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error)
	MemoryFocus(chatID int64) (core.MemoryFocus, bool)
	SetMemoryFocus(chatID int64, focus core.MemoryFocus)
	ClearMemoryFocus(chatID int64) bool
}

type commandStreamControlRouter interface {
	MarkStreamControlStopping(streamID string, chatID int64) bool
}

type telegramCallbackErrorRecorder interface {
	RecordTelegramCallbackError(chatID int64, callbackKind string, err error)
}

func recordTelegramCallbackError(router commandRouter, chatID int64, callbackKind string, err error) {
	if err == nil {
		return
	}
	if recorder, ok := router.(telegramCallbackErrorRecorder); ok {
		recorder.RecordTelegramCallbackError(chatID, callbackKind, err)
	}
}

func answerContinuationCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, cb telegram.CallbackQuery, callbackKind string, text string) {
	err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text)
	if err == nil || telegram.IsStaleCallbackQueryError(err) {
		return
	}
	recordTelegramCallbackError(router, chatID, callbackKind+".answer", err)
	log.Printf("WARN continuation callback answer failed chat_id=%d callback_id=%s kind=%s err=%v", chatID, strings.TrimSpace(cb.ID), strings.TrimSpace(callbackKind), err)
}

func editContinuationCallbackMessage(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string) {
	if messageID == 0 {
		return
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		recordTelegramCallbackError(router, chatID, callbackKind+".edit", err)
		log.Printf("WARN continuation callback message update failed chat_id=%d message_id=%d kind=%s err=%v", chatID, messageID, strings.TrimSpace(callbackKind), err)
	}
}

func editCallbackMessageClearingInlineKeyboard(ctx context.Context, sender commandCallbackSender, chatID int64, messageID int64, text string) error {
	if clearer, ok := sender.(commandInlineKeyboardClearer); ok {
		return clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, messageID, text, "")
	}
	return sender.EditMessageText(ctx, chatID, messageID, text, "")
}

var defaultTelegramCommands = []telegram.BotCommand{
	{Command: "start", Description: "Show intro and command help"},
	{Command: "help", Description: "Show available commands"},
	{Command: "status", Description: "Show live status and controls"},
	{Command: "debug", Description: "Show a detailed debug snapshot"},
	{Command: "doctor", Description: "Run an admin runtime diagnosis"},
	{Command: "tailnet", Description: "Show tailnet status and controls"},
	{Command: "agents", Description: "List durable agents and controls"},
	{Command: "memory", Description: "Review memory and set focus"},
	{Command: "mission", Description: "Show and manage the Mission Ledger"},
	{Command: "model", Description: "Show and change model slots"},
	{Command: "stop", Description: "Stop current work in this chat"},
	{Command: "new", Description: "Start a fresh chat session context"},
	{Command: "detach", Description: "Detach from pending work in this chat"},
	{Command: "restart", Description: "Force an immediate gateway restart"},
	{Command: "reinstall", Description: "Queue a rebuild/reinstall/restart request"},
	{Command: "set_persona_model", Description: "Choose Idolum persona model"},
	{Command: "set_governor_effort", Description: "Choose system reasoning effort"},
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
			fullText = humanizeTelegramTelemetryText("debug_scope=chat\nsummary unavailable")
		}
		if strings.TrimSpace(quickText) == "" {
			quickText = "Quick Read: unavailable. Tap Read More for the full debug snapshot."
		}
		rows := [][]telegram.InlineButton{{
			{Text: "Read More", CallbackData: encodeDebugCallbackData(debugViewMore)},
		}}
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, quickText, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "doctor":
		if !isAdmin {
			text = "Doctor diagnostics are admin only."
			break
		}
		if chatType := strings.TrimSpace(msg.ChatType); chatType != "" && chatType != "private" && chatType != "dm" {
			text = "Doctor diagnostics must be run from an admin private chat."
			break
		}
		if err := router.QueueDoctor(ctx, msg); err != nil {
			return true, err
		}
		text = "Doctor diagnostics started. I will post the report here when the read-only model analysis finishes."
	case "tailnet":
		if !isAdmin {
			text = "Tailnet diagnostics are admin only."
			break
		}
		action, rest := nextTailnetToken(telegramCommandArgs(msg.Text))
		if action == tailnetCommandSurfaces {
			surfaces, err := router.TailnetSurfaces(msg.SenderID)
			if err != nil {
				return true, err
			}
			rendered, rows := renderTailnetSurfacesCommand(surfaces)
			if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
				return true, err
			}
			return true, nil
		}
		if action == tailnetCommandRevoke {
			surfaceID, _ := nextTailnetToken(rest)
			if surfaceID == "" {
				text = "Usage: /tailnet revoke <surface_id>"
				break
			}
			rendered, rows := renderTailnetRevokeConfirmation(surfaceID)
			if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
				return true, err
			}
			return true, nil
		}
		snapshot, err := router.TailnetStatus(ctx, msg.SenderID)
		if err != nil {
			return true, err
		}
		rendered, rows := renderTailnetCommand(snapshot)
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "agents":
		if !router.CanRestart(msg.SenderID) {
			text = "Durable-agent controls are admin only."
			break
		}
		agents, err := router.DurableAgentsList(msg.SenderID)
		if err != nil {
			return true, err
		}
		rendered, rows := renderDurableAgentsCommand(agents)
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "memory":
		snapshot, err := router.MemoryReviewSnapshot(ctx, msg.ChatID, msg.SenderID, memoryReviewSourceSession)
		if err != nil {
			return true, err
		}
		focus, _ := router.MemoryFocus(msg.ChatID)
		rendered, rows := renderMemoryReviewPanel(snapshot, focus)
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "mission":
		args := telegramCommandArgs(msg.Text)
		if missionID, ok := missionProposalCommandMissionID(args); ok {
			proposal, err := router.MissionActionProposal(ctx, msg.ChatID, msg.SenderID, missionID)
			if err != nil {
				return true, err
			}
			if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, renderActionProposalPrompt(proposal), actionProposalButtonRows(proposal.ID), replyToMessageID(msg.MessageID)); err != nil {
				return true, err
			}
			return true, nil
		}
		missionText, err := router.MissionCommand(ctx, msg.ChatID, msg.SenderID, args)
		if err != nil {
			return true, err
		}
		text = missionText
	case "model":
		if !isAdmin {
			text = "Model controls are admin only."
			break
		}
		return handleTelegramModelCommand(ctx, sender, router, msg)
	case "stop":
		text = face.RenderTelegramStop(router.Stop(msg.ChatID))
	case "new":
		reset, resetErr := router.New(msg.ChatID, msg.SenderID)
		if resetErr != nil {
			return true, resetErr
		}
		text = face.RenderTelegramNewSession(reset)
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
	if streamID, action, ok := core.DecodeStreamControlCallbackData(cb.Data); ok {
		return handleStreamControlCallback(ctx, sender, router, cb, streamID, action)
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
	if action, surfaceID, ok := decodeTailnetRevokeCallbackData(cb.Data); ok {
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
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		if !router.CanRestart(senderID) {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Tailnet controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		if action == tailnetRevokeCallbackCancel {
			if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeCanceled(surfaceID)); err != nil {
				return true, err
			}
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		surface, found, err := router.RevokeTailnetSurface(ctx, senderID, surfaceID, "telegram tailnet revoke confirmation")
		if err != nil {
			return true, err
		}
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderTailnetRevokeResult(surfaceID, surface, found)); err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if action, ok := decodeTailnetCallbackData(cb.Data); ok {
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
		if (action != tailnetCallbackRefresh && action != tailnetCallbackSurfaces) || chatID == 0 || messageID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil {
				if !telegram.IsStaleCallbackQueryError(err) {
					return true, err
				}
			}
			return true, nil
		}
		if !router.CanRestart(senderID) {
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
		var rendered string
		var rows [][]telegram.InlineButton
		if action == tailnetCallbackSurfaces {
			surfaces, err := router.TailnetSurfaces(senderID)
			if err != nil {
				return true, err
			}
			rendered, rows = renderTailnetSurfacesCommand(surfaces)
		} else {
			snapshot, err := router.TailnetStatus(ctx, senderID)
			if err != nil {
				return true, err
			}
			rendered, rows = renderTailnetCommand(snapshot)
		}
		if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, rendered, "", rows); err != nil {
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
	if proposalID, action, ok := decodeActionProposalCallbackData(cb.Data); ok {
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
		missionID := missionIDFromActionProposalID(proposalID)
		if missionID == "" || chatID == 0 {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleActionProposalCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		proposal, err := router.MissionActionProposal(ctx, chatID, senderID, missionID)
		if err != nil || strings.TrimSpace(proposal.ID) != proposalID {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleActionProposalCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		mission, changed, err := router.ApplyMissionActionProposalDecision(ctx, chatID, senderID, missionID, action)
		if err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		if messageID != 0 {
			if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, renderActionProposalDecision(proposal, mission, action, changed)); err != nil {
				return true, err
			}
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
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.stale", staleContinuationCallbackText)
			return true, nil
		}
		var text string
		switch action {
		case continuationActionApprove, continuationActionApproveLease, continuationActionContinueOnce:
			approverID := int64(0)
			if cb.From != nil {
				approverID = cb.From.ID
			}
			state, err := router.ApproveContinuation(chatID, approverID)
			if err != nil {
				recordTelegramCallbackError(router, chatID, "continuation.approve", err)
				log.Printf("WARN continuation approve callback failed chat_id=%d approver_id=%d err=%v", chatID, approverID, err)
				answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", continuationCallbackErrorText(err))
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", renderContinuationCallbackError(state, err))
				return true, nil
			}
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.approve", "")
			if err := router.TriggerContinuation(ctx, chatID); err != nil {
				recordTelegramCallbackError(router, chatID, "continuation.trigger", err)
				log.Printf("WARN continuation trigger callback failed chat_id=%d err=%v", chatID, err)
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.trigger", renderContinuationCallbackError(state, err))
				return true, nil
			}
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.approve", text)
			return true, nil
		case continuationActionResumeEdge:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.resume", "")
			if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
				if err := router.TriggerContinuation(ctx, chatID); err != nil {
					recordTelegramCallbackError(router, chatID, "continuation.resume", err)
					log.Printf("WARN continuation resume callback failed chat_id=%d err=%v", chatID, err)
					editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.resume", renderContinuationCallbackError(state, err))
					return true, nil
				}
			}
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.resume", text)
			return true, nil
		case continuationActionAskEdit:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.ask_edit", "")
			if _, err := router.StopContinuation(chatID); err != nil {
				recordTelegramCallbackError(router, chatID, "continuation.ask_edit", err)
				log.Printf("WARN continuation ask-edit callback failed chat_id=%d err=%v", chatID, err)
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.ask_edit", renderContinuationCallbackError(state, err))
				return true, nil
			}
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.ask_edit", text)
			return true, nil
		case continuationActionStop, continuationActionStopPark:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.stop", "")
			stopped, err := router.StopContinuation(chatID)
			if err != nil {
				recordTelegramCallbackError(router, chatID, "continuation.stop", err)
				log.Printf("WARN continuation stop callback failed chat_id=%d err=%v", chatID, err)
				editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stop", renderContinuationCallbackError(state, err))
				return true, nil
			}
			if action == continuationActionStopPark {
				text = "Continuation parked. " + face.RenderTelegramStop(stopped)
			} else {
				text = face.RenderTelegramStop(stopped)
			}
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.stop", text)
			return true, nil
		case continuationActionAskNextLease, continuationActionStatusOnly:
			answerContinuationCallback(ctx, sender, router, chatID, cb, "continuation.status", "")
			text = renderContinuationDecision(state, action)
			editContinuationCallbackMessage(ctx, sender, router, chatID, messageID, "continuation.status", text)
			return true, nil
		default:
			return true, nil
		}
	}
	if action, step, option, ok := decodeDurableWizardCallbackData(cb.Data); ok {
		return handleDurableWizardCallback(ctx, sender, router, cb, action, step, option)
	}
	if action, agentID, ok := decodeDurableAgentsCallbackData(cb.Data); ok {
		return handleDurableAgentsCallback(ctx, sender, router, cb, action, agentID)
	}
	if action, source, index, ok := decodeMemoryReviewCallbackData(cb.Data); ok {
		return handleMemoryReviewCallback(ctx, sender, router, cb, action, source, index)
	}
	if action, slot, value, ok := decodeModelCallbackData(cb.Data); ok {
		return handleModelCallback(ctx, sender, router, cb, action, slot, value)
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
		if editErr := editCallbackMessageClearingInlineKeyboard(ctx, sender, cb.Message.Chat.ID, cb.Message.MessageID, text); editErr != nil {
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
	summary = groundDebugReadableSummary(summary, chat, system)
	if summary == "" {
		summary = composeDebugReadableSummary(chat, system)
	}
	quick := ""
	if summary != "" {
		quick = "quick_read " + summary
		full = "quick_read " + summary + "\n\n" + full
	}
	quick = humanizeTelegramTelemetryText(quick)
	full = humanizeTelegramTelemetryText(full)
	return quick, full, nil
}

func groundDebugReadableSummary(summary string, chat core.ChatStatusSnapshot, system *core.SystemStatusSnapshot) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	state := debugChatState(chat)
	lower := strings.ToLower(summary)
	inconsistent := false
	for _, candidate := range []string{"idle", "working", "blocked", "queued", "failed", "interrupted"} {
		if strings.Contains(lower, candidate) && candidate != state {
			inconsistent = true
			break
		}
	}
	if strings.Contains(lower, "completed successfully") || strings.HasPrefix(lower, "done") || strings.HasPrefix(lower, "all set") {
		latestStatus := strings.ToLower(strings.TrimSpace(debugLatestTurnStatus(chat)))
		if latestStatus != "" && latestStatus != "completed" {
			inconsistent = true
		}
	}
	if !inconsistent {
		return summary
	}
	return composeDebugReadableSummary(chat, system)
}

func composeDebugReadableSummary(chat core.ChatStatusSnapshot, system *core.SystemStatusSnapshot) string {
	state := debugChatState(chat)
	parts := []string{fmt.Sprintf("Chat %d is %s", chat.ChatID, state)}
	latestStatus := strings.ToLower(strings.TrimSpace(debugLatestTurnStatus(chat)))
	if latestStatus != "" {
		parts = append(parts, "latest turn is "+latestStatus)
	}
	if tool := strings.TrimSpace(debugLatestTurnTool(chat)); tool != "" {
		parts = append(parts, "last tool "+tool)
	}
	if pending := len(chat.PendingItems); pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending item(s)", pending))
	}
	if system != nil {
		if pending := len(system.PendingItems); pending > 0 {
			parts = append(parts, fmt.Sprintf("system has %d pending item(s)", pending))
		}
	}
	return strings.Join(parts, "; ") + "."
}

func debugChatState(chat core.ChatStatusSnapshot) string {
	if len(chat.ActiveTurnIDs) > 0 || strings.TrimSpace(chat.TurnPhase) != "" {
		return "working"
	}
	latestStatus := strings.ToLower(strings.TrimSpace(debugLatestTurnStatus(chat)))
	if latestStatus == "running" {
		return "working"
	}
	for _, item := range chat.PendingItems {
		switch item.Kind {
		case core.PendingItemKindDecision, core.PendingItemKindContinuation:
			return "blocked"
		}
	}
	if strings.EqualFold(strings.TrimSpace(chat.OperationStatus), "blocked") {
		return "blocked"
	}
	switch latestStatus {
	case "interrupted":
		return "interrupted"
	case "failed":
		return "failed"
	}
	if chat.QueueDepth > 0 {
		return "queued"
	}
	return "idle"
}

func debugLatestTurnStatus(chat core.ChatStatusSnapshot) string {
	if chat.LatestTurnRun == nil {
		return ""
	}
	return strings.TrimSpace(chat.LatestTurnRun.Status)
}

func debugLatestTurnTool(chat core.ChatStatusSnapshot) string {
	if chat.LatestTurnRun == nil {
		return ""
	}
	return strings.TrimSpace(chat.LatestTurnRun.LastToolName)
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
		chunks = []string{humanizeTelegramTelemetryText("debug_scope=chat\nsummary unavailable")}
	}
	first := chunks[0]
	if messageID != 0 {
		if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, first); err != nil {
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
