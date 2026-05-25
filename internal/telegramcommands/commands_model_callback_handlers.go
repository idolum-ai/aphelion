//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

func handleModelCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, action modelCallbackAction, slot string, value string) (bool, error) {
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
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleModelCallbackText); err != nil {
			if !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Model controls are admin only."); err != nil {
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

	actor := fmt.Sprintf("telegram:%d", senderID)
	switch action {
	case modelCallbackStatus:
		statuses, err := router.ModelSlotStatuses()
		if err != nil {
			return true, err
		}
		text, rows := renderModelSlotStatusPanel(statuses)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackSlot:
		status, err := modelSlotStatus(router, slot)
		if err != nil {
			return true, err
		}
		text := renderModelSlotDetail(status)
		rows := renderModelSlotRows(status)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackChanges:
		records, err := router.ModelSlotHistory(slot, 8)
		if err != nil {
			return true, err
		}
		text := renderModelSlotChanges(records)
		rows := renderModelChangesRows(slot)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackClear:
		status, err := router.ClearModelSlot(slot, actor, "telegram button: clear")
		if err != nil {
			return true, err
		}
		text := renderModelSlotChange("Cleared", status)
		rows := renderModelSlotRows(status)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackEffort:
		status, err := setModelSlotEffortFromCallback(router, slot, value, actor)
		if err != nil {
			return true, err
		}
		text := renderModelSlotChange("Updated", status)
		rows := renderModelSlotRows(status)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackSpeed:
		status, err := setModelSlotSpeedFromCallback(router, slot, value, actor)
		if err != nil {
			return true, err
		}
		text := renderModelSlotChange("Updated", status)
		rows := renderModelSlotRows(status)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackPreset:
		status, err := setModelSlotPresetFromCallback(router, slot, value, actor)
		if err != nil {
			return true, err
		}
		text := renderModelSlotChange("Updated", status)
		rows := renderModelSlotRows(status)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	default:
		return true, nil
	}
}

func setModelSlotEffortFromCallback(router commandRouter, slot string, effort string, actor string) (core.ModelSlotStatus, error) {
	status, err := modelSlotStatus(router, slot)
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	cfg := status.Effective
	cfg.Slot = core.NormalizeModelSlot(slot)
	cfg.Effort = core.NormalizeModelEffort(effort)
	if cfg.Effort == "" {
		return core.ModelSlotStatus{}, fmt.Errorf("unknown effort %q", effort)
	}
	return router.SetModelSlotConfig(cfg, actor, "telegram button: effort "+cfg.Effort)
}

func setModelSlotSpeedFromCallback(router commandRouter, slot string, speed string, actor string) (core.ModelSlotStatus, error) {
	status, err := modelSlotStatus(router, slot)
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	cfg := status.Effective
	cfg.Slot = core.NormalizeModelSlot(slot)
	if core.NormalizeModelProvider(cfg.Provider) != core.ModelProviderOpenAI {
		return core.ModelSlotStatus{}, fmt.Errorf("fast mode is only available for openai model slots")
	}
	cfg.ServiceTier = core.NormalizeModelServiceTier(speed)
	if cfg.ServiceTier == "" && !isModelServiceTierStandardAlias(speed) {
		return core.ModelSlotStatus{}, fmt.Errorf("unknown speed %q", speed)
	}
	reason := "telegram button: standard speed"
	if cfg.ServiceTier == core.ModelServiceTierPriority {
		reason = "telegram button: fast speed"
	}
	return router.SetModelSlotConfig(cfg, actor, reason)
}

func setModelSlotPresetFromCallback(router commandRouter, slot string, preset string, actor string) (core.ModelSlotStatus, error) {
	status, err := modelSlotStatus(router, slot)
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	cfg, err := modelPresetConfig(status, preset)
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	return router.SetModelSlotConfig(cfg, actor, "telegram button: preset "+strings.TrimSpace(preset))
}

func modelSlotStatus(router commandRouter, slot string) (core.ModelSlotStatus, error) {
	slot = core.NormalizeModelSlot(slot)
	if slot == "" {
		return core.ModelSlotStatus{}, fmt.Errorf("model slot is required")
	}
	statuses, err := router.ModelSlotStatuses()
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	for _, status := range statuses {
		if core.NormalizeModelSlot(status.Slot) == slot {
			return status, nil
		}
	}
	return core.ModelSlotStatus{}, fmt.Errorf("model slot %s was not found", slot)
}

func modelPresetConfig(status core.ModelSlotStatus, preset string) (core.ModelSlotConfig, error) {
	slot := core.NormalizeModelSlot(status.Slot)
	cfg := status.Effective
	cfg.Slot = slot
	cfg.Fallbacks = nil
	effort := core.NormalizeModelEffort(cfg.Effort)
	serviceTier := cfg.ServiceTier
	switch strings.ToLower(strings.TrimSpace(preset)) {
	case "sonnet":
		cfg.Provider = core.ModelProviderAnthropic
		cfg.Model = "claude-sonnet-4-6"
		if effort == "" {
			effort = "medium"
		}
	case "opus47":
		cfg.Provider = core.ModelProviderAnthropic
		cfg.Model = "claude-opus-4.7"
		if effort == "" {
			effort = "xhigh"
		}
	case "gpt55":
		if slot == core.ModelSlotDoctor {
			cfg.Provider = core.ModelProviderCodex
		} else {
			cfg.Provider = core.ModelProviderOpenAI
		}
		cfg.Model = "gpt-5.5"
		if effort == "" {
			effort = "high"
		}
	default:
		return core.ModelSlotConfig{}, fmt.Errorf("unknown model preset %q", preset)
	}
	cfg.Effort = effort
	if cfg.Provider == core.ModelProviderOpenAI {
		cfg.ServiceTier = serviceTier
	} else {
		cfg.ServiceTier = ""
	}
	cfg.Transport = core.ModelTransportAuto
	return core.NormalizeModelSlotConfig(cfg), nil
}
