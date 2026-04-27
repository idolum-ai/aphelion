//go:build linux

package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	modelCallbackPrefix    = "model:"
	staleModelCallbackText = "This model action is no longer active. Run /model again."
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
	case modelCallbackHistory:
		records, err := router.ModelSlotHistory(slot, 8)
		if err != nil {
			return true, err
		}
		text := renderModelSlotHistory(records)
		rows := renderModelHistoryRows(slot)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackClear:
		status, err := router.ClearModelSlot(slot, actor, "telegram button: clear")
		if err != nil {
			return true, err
		}
		text := renderModelSlotChange("Cleared", status)
		rows := renderModelSlotRows(status)
		return true, sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, clampTelegramModelText(text), "", rows)
	case modelCallbackRollback:
		status, err := router.RollbackModelSlot(slot, actor, "telegram button: rollback")
		if err != nil {
			return true, err
		}
		text := renderModelSlotChange("Rolled back", status)
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
	return router.SetModelSlotConfig(cfg, actor, "telegram button: effort "+cfg.Effort, 0)
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
	return router.SetModelSlotConfig(cfg, actor, "telegram button: preset "+strings.TrimSpace(preset), 0)
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
		cfg.Provider = core.ModelProviderOpenAI
		cfg.Model = "gpt-5.5"
		if effort == "" {
			effort = "high"
		}
	default:
		return core.ModelSlotConfig{}, fmt.Errorf("unknown model preset %q", preset)
	}
	cfg.Effort = effort
	cfg.Transport = core.ModelTransportAuto
	return core.NormalizeModelSlotConfig(cfg), nil
}

type modelSlotMutation struct {
	Config core.ModelSlotConfig
	TTL    time.Duration
}

func parseModelSlotMutation(raw string) (core.ModelSlotConfig, time.Duration, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) < 2 {
		return core.ModelSlotConfig{}, 0, fmt.Errorf("usage: /model set <slot> <provider/model> [effort=high] [transport=auto] [ttl=2h] [reason=text]")
	}
	slot := core.NormalizeModelSlot(fields[0])
	if slot == "" {
		return core.ModelSlotConfig{}, 0, fmt.Errorf("unknown model slot %q", fields[0])
	}
	cfg := core.ModelSlotConfig{Slot: slot, Transport: core.ModelTransportAuto}
	provider, model := core.ParseProviderModel(fields[1])
	if provider == "" || model == "" {
		return core.ModelSlotConfig{}, 0, fmt.Errorf("model must be written as provider/model")
	}
	cfg.Provider = provider
	cfg.Model = model
	var ttl time.Duration
	for _, field := range fields[2:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "provider":
			cfg.Provider = core.NormalizeModelProvider(value)
		case "model":
			cfg.Model = value
		case "effort":
			cfg.Effort = core.NormalizeModelEffort(value)
		case "transport":
			cfg.Transport = core.NormalizeModelTransport(value)
		case "fallback", "fallbacks":
			cfg.Fallbacks = parseModelFallbacks(value, cfg.Provider)
		case "ttl", "expires", "expires_in":
			parsed, err := time.ParseDuration(value)
			if err != nil {
				return core.ModelSlotConfig{}, 0, fmt.Errorf("invalid ttl %q", value)
			}
			ttl = parsed
		case "reason":
			cfg.Reason = modelCommandReason(raw)
		}
	}
	cfg = core.NormalizeModelSlotConfig(cfg)
	if cfg.Provider == "" || cfg.Model == "" {
		return core.ModelSlotConfig{}, 0, fmt.Errorf("provider and model are required")
	}
	return cfg, ttl, nil
}

func parseModelFallbacks(raw string, defaultProvider string) []core.ModelFallback {
	parts := strings.Split(raw, ",")
	out := make([]core.ModelFallback, 0, len(parts))
	for _, part := range parts {
		provider, model := core.ParseProviderModel(part)
		if model == "" {
			continue
		}
		if provider == "" {
			provider = core.NormalizeModelProvider(defaultProvider)
		}
		out = append(out, core.ModelFallback{Provider: provider, Model: model})
	}
	return out
}

func parseModelSlotActionTarget(raw string) (string, string) {
	fields := strings.Fields(strings.TrimSpace(raw))
	slot := ""
	if len(fields) > 0 {
		slot = fields[0]
	}
	return core.NormalizeModelSlot(slot), modelCommandReason(raw)
}

func parseModelSlotHistoryArgs(raw string) (string, int) {
	fields := strings.Fields(strings.TrimSpace(raw))
	slot := ""
	limit := 8
	for _, field := range fields {
		if key, value, ok := strings.Cut(field, "="); ok && strings.EqualFold(strings.TrimSpace(key), "limit") {
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && parsed > 0 {
				limit = parsed
			}
			continue
		}
		if slot == "" {
			slot = core.NormalizeModelSlot(field)
		}
	}
	return slot, limit
}

func telegramCommandArgs(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if idx := strings.IndexAny(text, " \n\t"); idx >= 0 {
		return strings.TrimSpace(text[idx+1:])
	}
	return ""
}

func nextModelToken(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if idx := strings.IndexAny(raw, " \n\t"); idx >= 0 {
		return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+1:])
	}
	return raw, ""
}

func modelCommandReason(raw string) string {
	for _, marker := range []string{" reason=", " reason:"} {
		if idx := strings.Index(raw, marker); idx >= 0 {
			return strings.TrimSpace(raw[idx+len(marker):])
		}
	}
	return ""
}

func renderModelSlotStatuses(statuses []core.ModelSlotStatus) string {
	var b strings.Builder
	b.WriteString("Models\n")
	if len(statuses) == 0 {
		b.WriteString("No model slot status available.")
		return b.String()
	}
	for _, status := range statuses {
		b.WriteString("\n")
		b.WriteString(modelSlotTitle(status.Slot))
		b.WriteString(": ")
		b.WriteString(renderModelSlotConfig(status.Effective))
		b.WriteString(" (")
		b.WriteString(status.Source)
		if !status.ExpiresAt.IsZero() {
			b.WriteString(", expires ")
			b.WriteString(status.ExpiresAt.UTC().Format("2006-01-02 15:04Z"))
		}
		b.WriteString(")")
		if !status.Validation.Valid {
			b.WriteString("\n  Invalid: ")
			b.WriteString(status.Validation.Error)
		} else if status.Validation.ResolvedTransport != "" {
			b.WriteString("\n  Transport: ")
			b.WriteString(status.Validation.ResolvedTransport)
		}
		if len(status.Validation.Warnings) > 0 {
			b.WriteString("\n  Warning: ")
			b.WriteString(strings.Join(status.Validation.Warnings, "; "))
		}
	}
	b.WriteString("\n\nUse /model set <slot> <provider/model> effort=<low|medium|high|xhigh> ttl=2h")
	return b.String()
}

func renderModelSlotStatusPanel(statuses []core.ModelSlotStatus) (string, [][]telegram.InlineButton) {
	return renderModelSlotStatuses(statuses), renderModelStatusRows()
}

func renderModelStatusRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{
		{
			{Text: "Persona", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotPersona, "")},
			{Text: "Governor", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotGovernor, "")},
		},
		{
			{Text: "Doctor", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotDoctor, "")},
			{Text: "Children", CallbackData: encodeModelCallbackData(modelCallbackSlot, core.ModelSlotChildDefault, "")},
		},
		{
			{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
		},
	}
}

func renderModelSlotDetail(status core.ModelSlotStatus) string {
	var b strings.Builder
	b.WriteString(modelSlotTitle(status.Slot))
	b.WriteString("\nCurrent: ")
	b.WriteString(renderModelSlotConfig(status.Effective))
	b.WriteString("\nSource: ")
	b.WriteString(firstNonEmptyModelUI(status.Source, "default"))
	if !status.ExpiresAt.IsZero() {
		b.WriteString("\nExpires: ")
		b.WriteString(status.ExpiresAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if status.Reason != "" {
		b.WriteString("\nReason: ")
		b.WriteString(status.Reason)
	}
	b.WriteString("\nDefault: ")
	b.WriteString(renderModelSlotConfig(status.Default))
	if status.Validation.Valid {
		if status.Validation.ResolvedTransport != "" {
			b.WriteString("\nTransport: ")
			b.WriteString(status.Validation.ResolvedTransport)
		}
	} else {
		b.WriteString("\nInvalid: ")
		b.WriteString(status.Validation.Error)
	}
	if len(status.Validation.Warnings) > 0 {
		b.WriteString("\nWarning: ")
		b.WriteString(strings.Join(status.Validation.Warnings, "; "))
	}
	return b.String()
}

func renderModelSlotRows(status core.ModelSlotStatus) [][]telegram.InlineButton {
	slot := core.NormalizeModelSlot(status.Slot)
	rows := [][]telegram.InlineButton{
		{
			{Text: "Sonnet", CallbackData: encodeModelCallbackData(modelCallbackPreset, slot, "sonnet")},
			{Text: "Opus 4.7", CallbackData: encodeModelCallbackData(modelCallbackPreset, slot, "opus47")},
			{Text: "GPT-5.5", CallbackData: encodeModelCallbackData(modelCallbackPreset, slot, "gpt55")},
		},
		{
			{Text: "Low", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "low")},
			{Text: "Medium", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "medium")},
			{Text: "High", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "high")},
			{Text: "Max", CallbackData: encodeModelCallbackData(modelCallbackEffort, slot, "xhigh")},
		},
		{
			{Text: "History", CallbackData: encodeModelCallbackData(modelCallbackHistory, slot, "")},
			{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackSlot, slot, "")},
			{Text: "All Slots", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
		},
	}
	if strings.EqualFold(strings.TrimSpace(status.Source), "override") {
		rows = append(rows, []telegram.InlineButton{
			{Text: "Rollback", CallbackData: encodeModelCallbackData(modelCallbackRollback, slot, "")},
			{Text: "Clear", CallbackData: encodeModelCallbackData(modelCallbackClear, slot, "")},
		})
	}
	return rows
}

func renderModelHistoryRows(slot string) [][]telegram.InlineButton {
	slot = core.NormalizeModelSlot(slot)
	if slot == "" {
		return [][]telegram.InlineButton{{
			{Text: "All Slots", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
			{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackHistory, "", "")},
		}}
	}
	return [][]telegram.InlineButton{
		{
			{Text: modelSlotTitle(slot), CallbackData: encodeModelCallbackData(modelCallbackSlot, slot, "")},
			{Text: "Refresh", CallbackData: encodeModelCallbackData(modelCallbackHistory, slot, "")},
			{Text: "All Slots", CallbackData: encodeModelCallbackData(modelCallbackStatus, "", "")},
		},
	}
}

func renderModelSlotValidation(validation core.ModelValidation) string {
	var b strings.Builder
	if validation.Valid {
		b.WriteString("Model config is valid.\n")
		b.WriteString(renderModelSlotConfig(validation.Config))
		if validation.ResolvedTransport != "" {
			b.WriteString("\nTransport: ")
			b.WriteString(validation.ResolvedTransport)
		}
	} else {
		b.WriteString("Model config is invalid.\n")
		b.WriteString(trimTelegramModelError(validation.Error))
	}
	if len(validation.Warnings) > 0 {
		b.WriteString("\nWarning: ")
		b.WriteString(strings.Join(validation.Warnings, "; "))
	}
	return b.String()
}

func renderModelSlotChange(prefix string, status core.ModelSlotStatus) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(" ")
	b.WriteString(modelSlotTitle(status.Slot))
	b.WriteString(".\n")
	b.WriteString(renderModelSlotConfig(status.Effective))
	b.WriteString("\nSource: ")
	b.WriteString(status.Source)
	if !status.ExpiresAt.IsZero() {
		b.WriteString("\nExpires: ")
		b.WriteString(status.ExpiresAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if status.Validation.ResolvedTransport != "" {
		b.WriteString("\nTransport: ")
		b.WriteString(status.Validation.ResolvedTransport)
	}
	return b.String()
}

func renderModelSlotHistory(records []session.ModelSlotOverrideRecord) string {
	if len(records) == 0 {
		return "Model override history is empty."
	}
	var b strings.Builder
	b.WriteString("Model history")
	for _, record := range records {
		b.WriteString("\n")
		b.WriteString(strconv.FormatInt(record.ID, 10))
		b.WriteString(" ")
		b.WriteString(modelSlotTitle(record.Slot))
		b.WriteString(" ")
		b.WriteString(record.Status)
		b.WriteString(": ")
		b.WriteString(renderModelSlotConfig(record.Config))
		if !record.CreatedAt.IsZero() {
			b.WriteString(" at ")
			b.WriteString(record.CreatedAt.UTC().Format("2006-01-02 15:04Z"))
		}
	}
	return b.String()
}

func renderModelSlotConfig(cfg core.ModelSlotConfig) string {
	cfg = core.NormalizeModelSlotConfig(cfg)
	var parts []string
	parts = append(parts, cfg.Provider+"/"+cfg.Model)
	if cfg.Effort != "" {
		parts = append(parts, "effort="+cfg.Effort)
	}
	if cfg.Transport != "" && cfg.Transport != core.ModelTransportAuto {
		parts = append(parts, "transport="+cfg.Transport)
	}
	if len(cfg.Fallbacks) > 0 {
		fallbacks := make([]string, 0, len(cfg.Fallbacks))
		for _, fallback := range cfg.Fallbacks {
			fallbacks = append(fallbacks, fallback.Provider+"/"+fallback.Model)
		}
		parts = append(parts, "fallbacks="+strings.Join(fallbacks, ","))
	}
	return strings.Join(parts, " ")
}

func renderModelCommandHelp() string {
	return strings.Join([]string{
		"Model controls",
		"/model status",
		"/model validate <slot> <provider/model> effort=high transport=auto",
		"/model set <slot> <provider/model> effort=high ttl=2h reason=why",
		"/model rollback <slot>",
		"/model clear <slot>",
		"/model history [slot] limit=8",
		"Slots: persona, governor, doctor, child_default",
		"Providers: openai, anthropic, openrouter, codex",
	}, "\n")
}

func modelSlotTitle(slot string) string {
	switch core.NormalizeModelSlot(slot) {
	case core.ModelSlotPersona:
		return "Persona"
	case core.ModelSlotGovernor:
		return "Governor"
	case core.ModelSlotDoctor:
		return "Doctor"
	case core.ModelSlotChildDefault:
		return "Child default"
	default:
		return strings.TrimSpace(slot)
	}
}

func trimTelegramModelError(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "unknown error"
	}
	return text
}

func clampTelegramModelText(text string) string {
	text = strings.TrimSpace(text)
	const limit = 4096
	if len(text) <= limit {
		return text
	}
	return strings.TrimSpace(text[:limit-32]) + "\n[truncated]"
}

func encodeModelCallbackData(action modelCallbackAction, slot string, value string) string {
	actionToken := modelCallbackActionToken(action)
	if actionToken == "" {
		return ""
	}
	slotToken := modelSlotToken(slot)
	value = strings.TrimSpace(value)
	switch action {
	case modelCallbackStatus:
		return modelCallbackPrefix + actionToken
	case modelCallbackSlot, modelCallbackHistory, modelCallbackClear, modelCallbackRollback:
		return modelCallbackPrefix + actionToken + ":" + slotToken
	case modelCallbackEffort, modelCallbackPreset:
		return modelCallbackPrefix + actionToken + ":" + slotToken + ":" + value
	default:
		return ""
	}
}

func decodeModelCallbackData(data string) (modelCallbackAction, string, string, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, modelCallbackPrefix) {
		return "", "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, modelCallbackPrefix))
	if payload == "" {
		return "", "", "", false
	}
	parts := strings.Split(payload, ":")
	action, ok := decodeModelCallbackActionToken(parts[0])
	if !ok {
		return "", "", "", false
	}
	switch action {
	case modelCallbackStatus:
		return action, "", "", len(parts) == 1
	case modelCallbackSlot, modelCallbackHistory, modelCallbackClear, modelCallbackRollback:
		if len(parts) != 2 {
			return "", "", "", false
		}
		slot := decodeModelSlotToken(parts[1])
		if slot == "" && action != modelCallbackHistory {
			return "", "", "", false
		}
		return action, slot, "", true
	case modelCallbackEffort, modelCallbackPreset:
		if len(parts) != 3 {
			return "", "", "", false
		}
		slot := decodeModelSlotToken(parts[1])
		value := strings.TrimSpace(parts[2])
		if slot == "" || value == "" {
			return "", "", "", false
		}
		return action, slot, value, true
	default:
		return "", "", "", false
	}
}

func modelCallbackActionToken(action modelCallbackAction) string {
	switch action {
	case modelCallbackStatus:
		return "status"
	case modelCallbackSlot:
		return "slot"
	case modelCallbackHistory:
		return "hist"
	case modelCallbackClear:
		return "clear"
	case modelCallbackRollback:
		return "rb"
	case modelCallbackEffort:
		return "eff"
	case modelCallbackPreset:
		return "preset"
	default:
		return ""
	}
}

func decodeModelCallbackActionToken(token string) (modelCallbackAction, bool) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "status":
		return modelCallbackStatus, true
	case "slot":
		return modelCallbackSlot, true
	case "hist":
		return modelCallbackHistory, true
	case "clear":
		return modelCallbackClear, true
	case "rb":
		return modelCallbackRollback, true
	case "eff":
		return modelCallbackEffort, true
	case "preset":
		return modelCallbackPreset, true
	default:
		return "", false
	}
}

func modelSlotToken(slot string) string {
	switch core.NormalizeModelSlot(slot) {
	case core.ModelSlotPersona:
		return "p"
	case core.ModelSlotGovernor:
		return "g"
	case core.ModelSlotDoctor:
		return "d"
	case core.ModelSlotChildDefault:
		return "c"
	default:
		return ""
	}
}

func decodeModelSlotToken(token string) string {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "p":
		return core.ModelSlotPersona
	case "g":
		return core.ModelSlotGovernor
	case "d":
		return core.ModelSlotDoctor
	case "c":
		return core.ModelSlotChildDefault
	default:
		return ""
	}
}

func firstNonEmptyModelUI(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
