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
	autoApprovalCallbackPrefix = "autoapprove:"
	autonomyCallbackPrefix     = "autonomy:"
)

type operatorPolicyPreset struct {
	Action      string
	Label       string
	AutoApprove string
	Autonomy    string
}

var operatorPolicyPresets = []operatorPolicyPreset{
	{Action: "refresh", Label: "Refresh"},
	{Action: "off", Label: "Off", AutoApprove: "off", Autonomy: "off"},
	{Action: "work15", Label: "15m Work", AutoApprove: "15m workspace uses=2", Autonomy: "leased 15m workspace uses=2"},
	{Action: "deploy15", Label: "15m Deploy", AutoApprove: "15m deploy uses=1", Autonomy: "leased 15m deploy uses=1"},
	{Action: "all15", Label: "15m All", AutoApprove: "15m all uses=1", Autonomy: "leased 15m all uses=1"},
}

func sendAutonomyPanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	snapshot, err := router.AutonomyStatus(msg.ChatID, msg.SenderID)
	if err != nil {
		return true, err
	}
	text := face.RenderTelegramAutonomyStatus(snapshot)
	_, err = sender.SendInlineKeyboard(ctx, msg.ChatID, text, autonomyRows(), replyToMessageID(msg.MessageID))
	return true, err
}

func sendAutoApprovalPanel(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	text, err := router.ConfigureAutoApproval(ctx, msg.ChatID, msg.SenderID, "status")
	if err != nil {
		log.Printf("WARN auto-approval command rejected chat_id=%d sender_id=%d err=%v", msg.ChatID, msg.SenderID, err)
		text = renderAutoApprovalCommandError(err)
	}
	_, sendErr := sender.SendInlineKeyboard(ctx, msg.ChatID, text, autoApprovalRows(), replyToMessageID(msg.MessageID))
	return true, sendErr
}

func autoApprovalRows() [][]telegram.InlineButton {
	return operatorPolicyRows(autoApprovalCallbackPrefix)
}

func autonomyRows() [][]telegram.InlineButton {
	return operatorPolicyRows(autonomyCallbackPrefix)
}

func operatorPolicyRows(prefix string) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			operatorPolicyButton(prefix, "refresh"),
			operatorPolicyButton(prefix, "off"),
		},
		{
			operatorPolicyButton(prefix, "work15"),
			operatorPolicyButton(prefix, "deploy15"),
			operatorPolicyButton(prefix, "all15"),
		},
	}
}

func operatorPolicyButton(prefix string, action string) telegram.InlineButton {
	preset, ok := operatorPolicyPresetForAction(action)
	if !ok {
		return telegram.InlineButton{Text: strings.TrimSpace(action), CallbackData: prefix + strings.TrimSpace(action)}
	}
	return telegram.InlineButton{Text: preset.Label, CallbackData: prefix + preset.Action}
}

func decodeAutoApprovalCallbackData(data string) (string, bool) {
	return decodeOperatorPolicyCallbackData(data, autoApprovalCallbackPrefix)
}

func decodeAutonomyCallbackData(data string) (string, bool) {
	return decodeOperatorPolicyCallbackData(data, autonomyCallbackPrefix)
}

func decodeOperatorPolicyCallbackData(data string, prefix string) (string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	action := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
	for _, preset := range operatorPolicyPresets {
		if preset.Action == action {
			return action, true
		}
	}
	return "", false
}

func handleAutoApprovalCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string) (bool, error) {
	return handleOperatorPolicyCallback(ctx, sender, router, cb, action, false)
}

func handleAutonomyCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string) (bool, error) {
	return handleOperatorPolicyCallback(ctx, sender, router, cb, action, true)
}

func handleOperatorPolicyCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action string, autonomy bool) (bool, error) {
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
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "This control is available to Telegram admins only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	preset, ok := operatorPolicyPresetForAction(action)
	if !ok {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}

	var (
		text string
		err  error
		rows [][]telegram.InlineButton
	)
	if autonomy {
		rows = autonomyRows()
		if preset.Action == "refresh" {
			snapshot, statusErr := router.AutonomyStatus(chatID, senderID)
			if statusErr != nil {
				return true, statusErr
			}
			text = face.RenderTelegramAutonomyStatus(snapshot)
		} else {
			text, err = router.ConfigureAutonomy(ctx, chatID, senderID, preset.Autonomy)
		}
		if err != nil {
			text = renderAutonomyCommandError(err)
		}
	} else {
		rows = autoApprovalRows()
		args := preset.AutoApprove
		if preset.Action == "refresh" {
			args = "status"
		}
		text, err = router.ConfigureAutoApproval(ctx, chatID, senderID, args)
		if err != nil {
			text = renderAutoApprovalCommandError(err)
		}
	}
	if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, strings.TrimSpace(text), "", rows); err != nil {
		return true, err
	}
	return true, nil
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
