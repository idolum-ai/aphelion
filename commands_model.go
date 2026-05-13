//go:build linux

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	modelCallbackPrefix    = "model:"
	staleModelCallbackText = "This model action is no longer active. Run /model again."
	modelButtonOverrideTTL = 2 * time.Hour
)

type modelCallbackAction string

const (
	modelCallbackStatus   modelCallbackAction = "status"
	modelCallbackSlot     modelCallbackAction = "slot"
	modelCallbackHistory  modelCallbackAction = "history"
	modelCallbackClear    modelCallbackAction = "clear"
	modelCallbackRollback modelCallbackAction = "rollback"
	modelCallbackEffort   modelCallbackAction = "effort"
	modelCallbackPreset   modelCallbackAction = "preset"
)

func handleTelegramModelCommand(ctx context.Context, sender commandSender, router commandRouter, msg core.InboundMessage) (bool, error) {
	args := telegramCommandArgs(msg.Text)
	action, rest := nextModelToken(args)
	if action == "" {
		action = "status"
	}
	action = strings.ToLower(strings.TrimSpace(action))
	actor := fmt.Sprintf("telegram:%d", msg.SenderID)

	var (
		text string
		err  error
	)
	switch action {
	case "status", "show":
		var statuses []core.ModelSlotStatus
		statuses, err = router.ModelSlotStatuses()
		text, rows := renderModelSlotStatusPanel(statuses)
		if err == nil {
			_, sendErr := sender.SendInlineKeyboard(ctx, msg.ChatID, clampTelegramModelText(text), rows, replyToMessageID(msg.MessageID))
			if sendErr != nil {
				return true, sendErr
			}
			return true, nil
		}
	case "validate":
		var cfg core.ModelSlotConfig
		cfg, _, err = parseModelSlotMutation(rest)
		if err == nil {
			validation := router.ValidateModelSlotConfig(cfg)
			text = renderModelSlotValidation(validation)
		}
	case "set":
		var parsed modelSlotMutation
		parsed.Config, parsed.TTL, err = parseModelSlotMutation(rest)
		if err == nil {
			status, setErr := router.SetModelSlotConfig(parsed.Config, actor, parsed.Config.Reason, parsed.TTL)
			if setErr != nil {
				err = setErr
			} else {
				text = renderModelSlotChange("Updated", status)
				rows := renderModelSlotRows(status)
				_, sendErr := sender.SendInlineKeyboard(ctx, msg.ChatID, clampTelegramModelText(text), rows, replyToMessageID(msg.MessageID))
				if sendErr != nil {
					return true, sendErr
				}
				return true, nil
			}
		}
	case "rollback":
		slot, reason := parseModelSlotActionTarget(rest)
		var status core.ModelSlotStatus
		status, err = router.RollbackModelSlot(slot, actor, reason)
		if err == nil {
			text = renderModelSlotChange("Rolled back", status)
			rows := renderModelSlotRows(status)
			_, sendErr := sender.SendInlineKeyboard(ctx, msg.ChatID, clampTelegramModelText(text), rows, replyToMessageID(msg.MessageID))
			if sendErr != nil {
				return true, sendErr
			}
			return true, nil
		}
	case "clear":
		slot, reason := parseModelSlotActionTarget(rest)
		var status core.ModelSlotStatus
		status, err = router.ClearModelSlot(slot, actor, reason)
		if err == nil {
			text = renderModelSlotChange("Cleared", status)
			rows := renderModelSlotRows(status)
			_, sendErr := sender.SendInlineKeyboard(ctx, msg.ChatID, clampTelegramModelText(text), rows, replyToMessageID(msg.MessageID))
			if sendErr != nil {
				return true, sendErr
			}
			return true, nil
		}
	case "history":
		slot, limit := parseModelSlotHistoryArgs(rest)
		var records []session.ModelSlotOverrideRecord
		records, err = router.ModelSlotHistory(slot, limit)
		if err == nil {
			text = renderModelSlotHistory(records)
			rows := renderModelHistoryRows(slot)
			_, sendErr := sender.SendInlineKeyboard(ctx, msg.ChatID, clampTelegramModelText(text), rows, replyToMessageID(msg.MessageID))
			if sendErr != nil {
				return true, sendErr
			}
			return true, nil
		}
	default:
		text = renderModelCommandHelp()
	}
	if err != nil {
		text = "Model command failed: " + trimTelegramModelError(err.Error())
	}
	if strings.TrimSpace(text) == "" {
		text = renderModelCommandHelp()
	}
	_, sendErr := sender.SendMessage(ctx, core.OutboundMessage{
		ChatID:  msg.ChatID,
		Text:    clampTelegramModelText(text),
		ReplyTo: replyToMessageID(msg.MessageID),
	})
	if sendErr != nil {
		return true, sendErr
	}
	return true, nil
}
