//go:build linux

package telegramcommands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

type modelSlotMutation struct {
	Config core.ModelSlotConfig
}

func parseModelSlotMutation(raw string) (core.ModelSlotConfig, error) {
	optionsRaw, reason := splitModelCommandReason(raw)
	fields := strings.Fields(strings.TrimSpace(optionsRaw))
	if len(fields) < 2 {
		return core.ModelSlotConfig{}, fmt.Errorf("usage: /model set <slot> <provider/model> [effort=high] [speed=fast] [transport=auto] [reason=text]")
	}
	slot := core.NormalizeModelSlot(fields[0])
	if slot == "" {
		return core.ModelSlotConfig{}, fmt.Errorf("unknown model slot %q", fields[0])
	}
	cfg := core.ModelSlotConfig{Slot: slot, Transport: core.ModelTransportAuto}
	provider, model := core.ParseProviderModel(fields[1])
	if provider == "" || model == "" {
		return core.ModelSlotConfig{}, fmt.Errorf("model must be written as provider/model")
	}
	cfg.Provider = provider
	cfg.Model = model
	cfg.Reason = reason
	for _, field := range fields[2:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return core.ModelSlotConfig{}, fmt.Errorf("unknown model option %q", field)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "provider":
			cfg.Provider = core.NormalizeModelProvider(value)
			if cfg.Provider == "" {
				return core.ModelSlotConfig{}, fmt.Errorf("unknown provider %q", value)
			}
		case "model":
			cfg.Model = value
		case "effort":
			cfg.Effort = core.NormalizeModelEffort(value)
			if cfg.Effort == "" && strings.TrimSpace(value) != "" && !strings.EqualFold(value, "default") {
				return core.ModelSlotConfig{}, fmt.Errorf("unknown effort %q", value)
			}
		case "transport":
			cfg.Transport = core.NormalizeModelTransport(value)
			if cfg.Transport == "" {
				return core.ModelSlotConfig{}, fmt.Errorf("unknown transport %q", value)
			}
		case "speed", "service_tier":
			cfg.ServiceTier = core.NormalizeModelServiceTier(value)
			if cfg.ServiceTier == "" && !isModelServiceTierStandardAlias(value) {
				return core.ModelSlotConfig{}, fmt.Errorf("unknown speed %q", value)
			}
		case "fallback", "fallbacks":
			fallbacks, err := parseModelFallbacks(value, cfg.Provider)
			if err != nil {
				return core.ModelSlotConfig{}, err
			}
			cfg.Fallbacks = fallbacks
		case "ttl", "expires", "expires_in":
			return core.ModelSlotConfig{}, fmt.Errorf("%s is not a /model option; use /model clear <slot> to return to the default", key)
		case "reason":
			cfg.Reason = reason
		default:
			return core.ModelSlotConfig{}, fmt.Errorf("unknown model option %q", key)
		}
	}
	cfg = core.NormalizeModelSlotConfig(cfg)
	if cfg.Provider == "" || cfg.Model == "" {
		return core.ModelSlotConfig{}, fmt.Errorf("provider and model are required")
	}
	return cfg, nil
}

func parseModelFallbacks(raw string, defaultProvider string) ([]core.ModelFallback, error) {
	parts := strings.Split(raw, ",")
	out := make([]core.ModelFallback, 0, len(parts))
	for _, part := range parts {
		provider, model := core.ParseProviderModel(part)
		if model == "" {
			return nil, fmt.Errorf("fallback model must be written as provider/model or model")
		}
		if provider == "" {
			provider = core.NormalizeModelProvider(defaultProvider)
		}
		if provider == "" {
			return nil, fmt.Errorf("fallback provider is required")
		}
		out = append(out, core.ModelFallback{Provider: provider, Model: model})
	}
	return out, nil
}

func isModelServiceTierStandardAlias(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "standard", "default":
		return true
	default:
		return false
	}
}

func parseModelSlotActionTarget(raw string) (string, string) {
	fields := strings.Fields(strings.TrimSpace(raw))
	slot := ""
	if len(fields) > 0 {
		slot = fields[0]
	}
	return core.NormalizeModelSlot(slot), modelCommandReason(raw)
}

func parseModelSlotChangesArgs(raw string) (string, int) {
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
	_, reason := splitModelCommandReason(raw)
	return reason
}

func splitModelCommandReason(raw string) (string, string) {
	for _, marker := range []string{" reason=", " reason:"} {
		if idx := strings.Index(raw, marker); idx >= 0 {
			return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+len(marker):])
		}
	}
	return raw, ""
}
