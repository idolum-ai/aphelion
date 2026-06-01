//go:build linux

package telegramcommands

import (
	"context"
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
	ApproveContinuationBundle(chatID int64, approverID int64, phaseIDs []string) (session.ContinuationState, error)
	StopContinuation(chatID int64) (core.StopResult, error)
	TriggerContinuation(ctx context.Context, chatID int64) error
	QueueReinstall(ctx context.Context, msg core.InboundMessage) error
	QueueDoctor(ctx context.Context, msg core.InboundMessage) error
	LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error)
	ToggleProgressView(ctx context.Context, chatID int64, senderID int64, runID int64, details bool) (bool, string, error)
	CurrentEfforts() (persona string, governor string)
	ModelSlotStatuses() ([]core.ModelSlotStatus, error)
	ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation
	SetModelSlotConfig(cfg core.ModelSlotConfig, actor string, reason string) (core.ModelSlotStatus, error)
	ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error)
	ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error)
	RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error)
	DurableAgentsList(senderID int64) ([]core.DurableAgentStatusSnapshot, error)
	StartDurableAgentConversation(ctx context.Context, chatID int64, senderID int64, agentID string) (string, error)
	SendDurableAgentParentMessage(ctx context.Context, chatID int64, senderID int64, agentID string, message string) (string, error)
	DurableAgentLifecycleAction(ctx context.Context, chatID int64, senderID int64, agentID string, action string) (string, error)
	QueueDurableAgentAnalyze(ctx context.Context, msg core.InboundMessage) (string, error)
	RecordTelegramAgentCallbackMessage(chatID int64, agentID string, messageID int64, surface string) error
	TelegramAgentIDForReplyMessage(chatID int64, replyMessageID int64) (string, bool, error)
	MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source memoryReviewSource) (memoryReviewSnapshot, error)
	MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error)
	MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error)
	MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error)
	SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error)
	UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error)
	MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error)
	MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error)
	ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error)
	MissionAskPrompt(ctx context.Context, senderID int64, promptID string) (session.MissionAskPrompt, bool, error)
	ResolveMissionAskPrompt(ctx context.Context, senderID int64, promptID string, status session.MissionAskStatus, summary string) (session.MissionAskPrompt, error)
	QueueMissionClarification(ctx context.Context, msg core.InboundMessage, promptID string) error
}

type commandScopedStatusRouter interface {
	StatusChatForMessage(msg core.InboundMessage) (core.ChatStatusSnapshot, error)
}

type commandScopedSessionRouter interface {
	StopForMessage(msg core.InboundMessage) core.StopResult
	NewForMessage(msg core.InboundMessage) (core.NewSessionResult, error)
	DetachForMessage(msg core.InboundMessage) (core.DetachResult, error)
}

type approvalWindowRouter interface {
	CreateApprovalWindowOfferForMessage(ctx context.Context, msg core.InboundMessage, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error)
	EnableApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage, duration time.Duration) (string, error)
	EnableApprovalWindowForMessageResult(ctx context.Context, msg core.InboundMessage, duration time.Duration) (core.ApprovalWindowEnableResult, error)
	DoubleApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error)
	CancelApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error)
	CancelApprovalWindowForMessageResult(ctx context.Context, msg core.InboundMessage) (core.ApprovalWindowCancelResult, error)
	EnableApprovalWindowOffer(ctx context.Context, offerID string, senderID int64, duration time.Duration) (string, error)
	EnableApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error)
	DoubleApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error)
	CancelApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error)
	CancelApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64) (core.ApprovalWindowCancelResult, error)
	CloseApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) error
}

type commandScopedMemoryRouter interface {
	MemoryReviewSnapshotForMessage(ctx context.Context, msg core.InboundMessage, source memoryReviewSource) (memoryReviewSnapshot, error)
}

func handleTelegramCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return false, nil
	}
	command, ok := parseTelegramCommand(msg.Text)
	if !ok {
		return false, nil
	}
	if routed, handled, err := resolveTelegramThreadCommandTarget(ctx, sender, router, msg, command); err != nil {
		return true, err
	} else if handled {
		return true, nil
	} else {
		msg = routed
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
		rendered, rows, renderErr := renderStatusCommand(ctx, router, msg, personaEffort, governorEffort)
		if renderErr != nil {
			return true, renderErr
		}
		messageID, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID))
		if err != nil {
			return true, err
		}
		if msg.TelegramThreadID > 0 {
			if err := recordTelegramThreadCallbackMessage(router, msg.ChatID, msg.TelegramThreadID, messageID, "status"); err != nil {
				return true, err
			}
		}
		return true, nil
	case "health":
		return handleTelegramHealthCommand(ctx, sender, router, msg, personaEffort, governorEffort, isAdmin)
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
			surfaceID := strings.TrimSpace(rest)
			if surfaceID == "" {
				text = "Usage: /tailnet revoke <surface_id>"
				break
			}
			surfaces, err := router.TailnetSurfaces(msg.SenderID)
			if err != nil {
				return true, err
			}
			surface, found := findTailnetSurfaceByID(surfaces, surfaceID)
			if !found {
				rendered := renderTailnetRevokeResult(surfaceID, core.TailnetSurfaceStatus{}, false)
				if _, err := sender.SendMessage(ctx, core.OutboundMessage{ChatID: msg.ChatID, Text: rendered, ReplyTo: replyToMessageID(msg.MessageID)}); err != nil {
					return true, err
				}
				return true, nil
			}
			rendered, rows := renderTailnetRevokeTokenConfirmation(surface.SurfaceID)
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
	case "context":
		snapshot, err := contextSnapshotForCommand(ctx, router, msg)
		if err != nil {
			return true, err
		}
		rendered, rows := renderContextPanel(snapshot)
		rendered = telegramThreadDisplayPrefixForMessage(msg) + rendered
		messageID, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID))
		if err != nil {
			return true, err
		}
		if err := recordTelegramThreadCallbackMessage(router, msg.ChatID, msg.TelegramThreadID, messageID, "context"); err != nil {
			return true, err
		}
		return true, nil
	case "memory":
		snapshot, err := memoryReviewSnapshotForCommand(ctx, router, msg, memoryReviewSourceSession)
		if err != nil {
			return true, err
		}
		rendered, rows := renderMemoryReviewPanel(snapshot)
		rendered = telegramThreadDisplayPrefixForMessage(msg) + rendered
		messageID, err := sender.SendInlineKeyboard(ctx, msg.ChatID, rendered, rows, replyToMessageID(msg.MessageID))
		if err != nil {
			return true, err
		}
		if err := recordTelegramThreadCallbackMessage(router, msg.ChatID, msg.TelegramThreadID, messageID, "memory"); err != nil {
			return true, err
		}
		return true, nil
	case "thread", "threads", "absorb":
		return handleTelegramThreadCommand(ctx, sender, router, msg, command)
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
	case "stop":
		text = face.RenderTelegramStop(stopForCommand(router, msg))
	case "new":
		reset, resetErr := newForCommand(router, msg)
		if resetErr != nil {
			return true, resetErr
		}
		text = face.RenderTelegramNewSession(reset)
	case "detach":
		detached, detachErr := detachForCommand(router, msg)
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
	default:
		return false, nil
	}

	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    telegramThreadDisplayPrefixForMessage(msg) + text,
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
	token = strings.ToLower(token)
	if !registeredTelegramCommand(token) {
		return "", false
	}
	return token, true
}

func registeredTelegramCommand(command string) bool {
	for _, registered := range defaultTelegramCommands {
		if registered.Command == command {
			return true
		}
	}
	return false
}

func replyToMessageID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
