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
		text = renderModelSlotStatuses(statuses)
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
			}
		}
	case "rollback":
		slot, reason := parseModelSlotActionTarget(rest)
		var status core.ModelSlotStatus
		status, err = router.RollbackModelSlot(slot, actor, reason)
		if err == nil {
			text = renderModelSlotChange("Rolled back", status)
		}
	case "clear":
		slot, reason := parseModelSlotActionTarget(rest)
		var status core.ModelSlotStatus
		status, err = router.ClearModelSlot(slot, actor, reason)
		if err == nil {
			text = renderModelSlotChange("Cleared", status)
		}
	case "history":
		slot, limit := parseModelSlotHistoryArgs(rest)
		var records []session.ModelSlotOverrideRecord
		records, err = router.ModelSlotHistory(slot, limit)
		if err == nil {
			text = renderModelSlotHistory(records)
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
