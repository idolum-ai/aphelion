//go:build linux

package main

import (
	"context"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	autoCallbackPrefix = "auto:"

	autoSurfaceHome      = "home"
	autoSurfacePolicy    = "policy"
	autoSurfaceApprovals = "approvals"

	autoActionShow    = "show"
	autoActionRefresh = "refresh"
)

const staleAutoCallbackText = "This auto action is no longer available. Run /auto again."

type operatorPolicyPreset struct {
	Action      string
	Label       string
	AutoApprove string
	Autonomy    string
}

var operatorPolicyPresets = []operatorPolicyPreset{
	{Action: "off", Label: "Off", AutoApprove: "off", Autonomy: "off"},
	{Action: "work15", Label: "15m Work", AutoApprove: "15m workspace uses=2", Autonomy: "leased 15m workspace uses=2"},
	{Action: "deploy15", Label: "15m Deploy", AutoApprove: "15m deploy uses=1", Autonomy: "leased 15m deploy uses=1"},
	{Action: "all15", Label: "15m All", AutoApprove: "15m all uses=1", Autonomy: "leased 15m all uses=1"},
}

func handleTelegramAutoCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	target, rest := nextCommandToken(telegramCommandArgs(msg.Text))
	switch target {
	case "", "home", autoActionRefresh:
		return sendAutoHomePanel(ctx, sender, msg)
	case autoSurfacePolicy:
		if strings.TrimSpace(rest) == "" || strings.EqualFold(strings.TrimSpace(rest), "status") {
			return sendAutoPolicyPanel(ctx, sender, router, msg)
		}
		configured, err := router.ConfigureAutonomy(ctx, msg.ChatID, msg.SenderID, rest)
		if err != nil {
			log.Printf("WARN auto policy command rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
			return sendAutoCommandText(ctx, sender, msg, renderAutonomyCommandError(err))
		}
		return sendAutoCommandText(ctx, sender, msg, configured)
	case "approval", autoSurfaceApprovals:
		if strings.TrimSpace(rest) == "" || strings.EqualFold(strings.TrimSpace(rest), "status") {
			return sendAutoApprovalsPanel(ctx, sender, router, msg)
		}
		configured, err := router.ConfigureAutoApproval(ctx, msg.ChatID, msg.SenderID, rest)
		if err != nil {
			log.Printf("WARN auto approvals command rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
			return sendAutoCommandText(ctx, sender, msg, renderAutoApprovalCommandError(err))
		}
		return sendAutoCommandText(ctx, sender, msg, configured)
	default:
		return sendAutoCommandText(ctx, sender, msg, renderAutoCommandUsage(target))
	}
}

func sendAutoCommandText(ctx context.Context, sender commandSender, msg core.InboundMessage, text string) (bool, error) {
	_, err := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    strings.TrimSpace(text),
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	return true, err
}

func sendAutoHomePanel(ctx context.Context, sender commandSender, msg core.InboundMessage) (bool, error) {
	_, err := sender.SendInlineKeyboard(ctx, msg.ChatID, renderAutoHomePanel(), autoHomeRows(), replyToMessageID(msg.MessageID))
	return true, err
}

func sendAutoPolicyPanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	snapshot, err := router.AutonomyStatus(msg.ChatID, msg.SenderID)
	if err != nil {
		return true, err
	}
	text := face.RenderTelegramAutonomyStatus(snapshot)
	_, err = sender.SendInlineKeyboard(ctx, msg.ChatID, text, autoPolicyRows(), replyToMessageID(msg.MessageID))
	return true, err
}

func sendAutoApprovalsPanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	text, err := router.AutoApprovalStatus(ctx, msg.ChatID, msg.SenderID)
	if err != nil {
		log.Printf("WARN auto approvals status rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
		text = renderAutoApprovalCommandError(err)
	}
	_, sendErr := sender.SendInlineKeyboard(ctx, msg.ChatID, strings.TrimSpace(text), autoApprovalsRows(), replyToMessageID(msg.MessageID))
	return true, sendErr
}

func renderAutoHomePanel() string {
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Auto",
		State: "ready",
		Why:   "Authority controls are split by policy and prompt approvals.",
		Next:  "Open policy for autonomy leases or approvals for prompt leases.",
		Details: []string{
			"Policy changes the current autonomy override within the configured ceiling.",
			"Approvals grant bounded automatic approval for eligible admin prompts.",
		},
	})
}

func renderAutoCommandUsage(target string) string {
	target = strings.TrimSpace(target)
	why := "Unknown auto target."
	if target != "" {
		why = "Unknown auto target: " + target + "."
	}
	return face.RenderOperatorPanel(face.OperatorPanel{
		Title: "Auto",
		State: "not applied",
		Why:   why,
		Next:  "Use /auto policy, /auto approvals, /auto policy leased <duration> <scope>, or /auto approvals <duration> <scope>.",
	})
}

func autoHomeRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			autoButton(autoSurfacePolicy, autoActionShow, "Policy"),
			autoButton(autoSurfaceApprovals, autoActionShow, "Approvals"),
		},
		{
			autoButton(autoSurfaceHome, autoActionRefresh, "Refresh"),
		},
	}
}

func autoPolicyRows() [][]telegram.InlineButton {
	return operatorPolicyRows(autoSurfacePolicy)
}

func autoApprovalsRows() [][]telegram.InlineButton {
	return operatorPolicyRows(autoSurfaceApprovals)
}

func operatorPolicyRows(surface string) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			autoButton(autoSurfaceHome, autoActionShow, "Back"),
			autoButton(surface, autoActionRefresh, "Refresh"),
		},
		{
			operatorPolicyButton(surface, "off"),
		},
		{
			operatorPolicyButton(surface, "work15"),
			operatorPolicyButton(surface, "deploy15"),
			operatorPolicyButton(surface, "all15"),
		},
	}
}

func operatorPolicyButton(surface string, action string) telegram.InlineButton {
	preset, ok := operatorPolicyPresetForAction(action)
	if !ok {
		return autoButton(surface, action, strings.TrimSpace(action))
	}
	return autoButton(surface, preset.Action, preset.Label)
}

func autoButton(surface string, action string, label string) telegram.InlineButton {
	return telegram.InlineButton{
		Text:         strings.TrimSpace(label),
		CallbackData: encodeAutoCallbackData(surface, action),
	}
}

func encodeAutoCallbackData(surface string, action string) string {
	surface = strings.TrimSpace(surface)
	action = strings.TrimSpace(action)
	if surface == "" {
		surface = autoSurfaceHome
	}
	if action == "" {
		action = autoActionShow
	}
	return autoCallbackPrefix + surface + ":" + action
}

func decodeAutoCallbackData(data string) (string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, autoCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, autoCallbackPrefix))
	surface, action := nextCommandToken(strings.ReplaceAll(payload, ":", " "))
	if action == "" {
		action = autoActionShow
	}
	if !validAutoSurface(surface) || !validAutoAction(action) {
		return "", "", false
	}
	return surface, action, true
}

func validAutoSurface(surface string) bool {
	switch strings.TrimSpace(surface) {
	case autoSurfaceHome, autoSurfacePolicy, autoSurfaceApprovals:
		return true
	default:
		return false
	}
}

func validAutoAction(action string) bool {
	switch strings.TrimSpace(action) {
	case autoActionShow, autoActionRefresh:
		return true
	default:
		_, ok := operatorPolicyPresetForAction(action)
		return ok
	}
}

func handleAutoCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, surface string, action string) (bool, error) {
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
	if chatID == 0 || messageID == 0 || senderID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleAutoCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Auto controls are admin only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}

	text, rows, err := renderAutoCallbackResult(ctx, router, chatID, senderID, surface, action)
	if err != nil {
		return true, err
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, strings.TrimSpace(text), "", rows); err != nil {
		return true, err
	}
	return true, nil
}

func renderAutoCallbackResult(ctx context.Context, router commandRouter, chatID int64, senderID int64, surface string, action string) (string, [][]telegram.InlineButton, error) {
	switch surface {
	case autoSurfaceHome:
		return renderAutoHomePanel(), autoHomeRows(), nil
	case autoSurfacePolicy:
		text, err := renderAutoPolicyCallbackText(ctx, router, chatID, senderID, action)
		return text, autoPolicyRows(), err
	case autoSurfaceApprovals:
		text, err := renderAutoApprovalsCallbackText(ctx, router, chatID, senderID, action)
		return text, autoApprovalsRows(), err
	default:
		return renderAutoHomePanel(), autoHomeRows(), nil
	}
}

func renderAutoPolicyCallbackText(ctx context.Context, router commandRouter, chatID int64, senderID int64, action string) (string, error) {
	switch action {
	case autoActionShow, autoActionRefresh:
		snapshot, err := router.AutonomyStatus(chatID, senderID)
		if err != nil {
			return "", err
		}
		return face.RenderTelegramAutonomyStatus(snapshot), nil
	default:
		preset, ok := operatorPolicyPresetForAction(action)
		if !ok {
			return renderAutonomyCommandError(nil), nil
		}
		text, err := router.ConfigureAutonomy(ctx, chatID, senderID, preset.Autonomy)
		if err != nil {
			return renderAutonomyCommandError(err), nil
		}
		return text, nil
	}
}

func renderAutoApprovalsCallbackText(ctx context.Context, router commandRouter, chatID int64, senderID int64, action string) (string, error) {
	switch action {
	case autoActionShow, autoActionRefresh:
		text, err := router.AutoApprovalStatus(ctx, chatID, senderID)
		if err != nil {
			return renderAutoApprovalCommandError(err), nil
		}
		return text, nil
	default:
		preset, ok := operatorPolicyPresetForAction(action)
		if !ok {
			return renderAutoApprovalCommandError(nil), nil
		}
		text, err := router.ConfigureAutoApproval(ctx, chatID, senderID, preset.AutoApprove)
		if err != nil {
			return renderAutoApprovalCommandError(err), nil
		}
		return text, nil
	}
}

func operatorPolicyPresetForAction(action string) (operatorPolicyPreset, bool) {
	action = strings.TrimSpace(action)
	for _, preset := range operatorPolicyPresets {
		if preset.Action == action {
			return preset, true
		}
	}
	return operatorPolicyPreset{}, false
}
