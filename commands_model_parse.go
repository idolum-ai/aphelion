//go:build linux

package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

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
