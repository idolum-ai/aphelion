//go:build linux

package main

import (
	"context"
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

type commandRouter interface {
	Stop(chatID int64) core.StopResult
	New(chatID int64, senderID int64) (core.NewSessionResult, error)
	Detach(chatID int64, senderID int64) (core.DetachResult, error)
	Restart(chatID int64) error
	CanRestart(senderID int64) bool
	Status(chatID int64) core.SessionStatus
	StatusChat(chatID int64) (core.ChatStatusSnapshot, error)
	StatusSystem(senderID int64) (core.SystemStatusSnapshot, error)
	AutonomyStatus(chatID int64, senderID int64) (core.AutonomyStatusSnapshot, error)
	StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error)
	StatusReadableSummary(ctx context.Context, view string, statusText string) string
	TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error)
	TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error)
	TailnetGrantBindings(senderID int64) ([]core.TailnetGrantBindingStatus, error)
	RevokeTailnetSurface(ctx context.Context, senderID int64, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error)
	ContinuationState(chatID int64) (session.ContinuationState, error)
	ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error)
	StopContinuation(chatID int64) (core.StopResult, error)
	TriggerContinuation(ctx context.Context, chatID int64) error
	QueueReinstall(ctx context.Context, msg core.InboundMessage) error
	QueueDoctor(ctx context.Context, msg core.InboundMessage) error
	LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error)
	ConfigureAutoApproval(ctx context.Context, chatID int64, senderID int64, args string) (string, error)
	ConfigureAutonomy(ctx context.Context, chatID int64, senderID int64, args string) (string, error)
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
	MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error)
	MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error)
	SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error)
	UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error)
	MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error)
	MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error)
	ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error)
	MemoryFocus(chatID int64) (core.MemoryFocus, bool)
	SetMemoryFocus(chatID int64, focus core.MemoryFocus)
	ClearMemoryFocus(chatID int64) bool
}

const staleDebugCallbackText = "This debug action is no longer available. Run /debug again."

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
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, text, commandMenuRows(isAdmin), replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
	case "help":
		text = face.RenderTelegramHelp(personaEffort, governorEffort, isAdmin)
		if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, text, commandMenuRows(isAdmin), replyToMessageID(msg.MessageID)); err != nil {
			return true, err
		}
		return true, nil
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
		if action == tailnetCommandGrants {
			bindings, err := router.TailnetGrantBindings(msg.SenderID)
			if err != nil {
				return true, err
			}
			rendered, rows := renderTailnetGrantBindingsCommand(bindings)
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
		if strings.TrimSpace(args) == "" || strings.EqualFold(strings.TrimSpace(args), "list") {
			missions, working, isAdmin, homeErr := router.MissionHome(ctx, msg.ChatID, msg.SenderID)
			if homeErr != nil {
				return true, homeErr
			}
			rendered, rows := renderMissionHomePanel(missions, working, isAdmin, false)
			if _, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID)); err != nil {
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
	case "autonomy":
		if !isAdmin {
			text = "Autonomy policy is admin only."
			break
		}
		args := telegramCommandArgs(msg.Text)
		if strings.TrimSpace(args) != "" {
			configured, err := router.ConfigureAutonomy(ctx, msg.ChatID, msg.SenderID, args)
			if err != nil {
				log.Printf("WARN autonomy command rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
				text = renderAutonomyCommandError(err)
				break
			}
			text = configured
			break
		}
		return sendAutonomyPanel(ctx, sender, router, msg)
	case "autoapprove":
		if !isAdmin {
			text = "Auto-approval controls are admin only."
			break
		}
		if strings.TrimSpace(telegramCommandArgs(msg.Text)) == "" {
			return sendAutoApprovalPanel(ctx, sender, router, msg)
		}
		configured, err := router.ConfigureAutoApproval(ctx, msg.ChatID, msg.SenderID, telegramCommandArgs(msg.Text))
		if err != nil {
			log.Printf("WARN auto-approval command rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
			text = renderAutoApprovalCommandError(err)
			break
		}
		text = configured
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

func renderAutoApprovalCommandError(err error) string {
	if err == nil {
		return face.RenderOperatorPanel(face.OperatorPanel{Title: "Auto-approval", State: "not applied", Next: "Check the command shape and retry."})
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return face.RenderOperatorPanel(face.OperatorPanel{Title: "Auto-approval", State: "not applied", Next: "Check the command shape and retry."})
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Auto-approval",
		State: "not applied",
		Why:   msg,
		Next:  "Adjust the duration, scope, or config ceiling and retry.",
	})
}

func renderAutonomyCommandError(err error) string {
	if err == nil {
		return face.RenderOperatorPanel(face.OperatorPanel{Title: "Autonomy", State: "not applied", Next: "Check the command shape and retry."})
	}
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return face.RenderOperatorPanel(face.OperatorPanel{Title: "Autonomy", State: "not applied", Next: "Check the command shape and retry."})
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Autonomy",
		State: "not applied",
		Why:   msg,
		Next:  "Adjust the duration, mode, scope, or config ceiling and retry.",
	})
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
