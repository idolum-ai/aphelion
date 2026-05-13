//go:build linux

package config

import (
	"strings"

	"github.com/BurntSushi/toml"
)

func EffectiveUserAgent(cfg *Config, defaultUserAgent string) string {
	if cfg != nil {
		if trimmed := strings.TrimSpace(cfg.Identity.UserAgent); trimmed != "" {
			return trimmed
		}
		if cfg.Identity.AnonymousProfile {
			return ""
		}
	}
	return strings.TrimSpace(defaultUserAgent)
}

func EffectiveGovernorName(cfg *Config, defaultName string) string {
	if cfg != nil {
		if trimmed := strings.TrimSpace(cfg.Identity.GovernorName); trimmed != "" {
			return trimmed
		}
		if cfg.Identity.AnonymousProfile {
			return "System"
		}
	}
	if trimmed := strings.TrimSpace(defaultName); trimmed != "" {
		return trimmed
	}
	return "System"
}

func EffectiveFaceName(cfg *Config, defaultName string) string {
	if cfg != nil {
		if trimmed := strings.TrimSpace(cfg.Identity.FaceName); trimmed != "" {
			return trimmed
		}
		if cfg.Identity.AnonymousProfile {
			return "Assistant"
		}
	}
	if trimmed := strings.TrimSpace(defaultName); trimmed != "" {
		return trimmed
	}
	return "Assistant"
}

func applyProviderSelectionHeuristic(cfg *Config, md toml.MetaData) {
	if cfg == nil {
		return
	}
	defaultDefined := md.IsDefined("providers", "default") && providerName(cfg.Providers.Default) != ""
	nativeDefined := md.IsDefined("governor", "native_provider") && providerName(cfg.Governor.NativeProvider) != ""
	fallbackDefined := md.IsDefined("providers", "fallback_chain")

	if defaultDefined && !nativeDefined {
		cfg.Governor.NativeProvider = providerName(cfg.Providers.Default)
	}
	if nativeDefined && !defaultDefined {
		cfg.Providers.Default = providerName(cfg.Governor.NativeProvider)
	}
	if normalizeProviderSelection(cfg.Providers.Selection) != "auto" {
		return
	}
	if !defaultDefined && !nativeDefined {
		if primary := firstConfiguredProviderByOrder(cfg, cfg.Providers.AutoOrder); primary != "" {
			cfg.Governor.NativeProvider = primary
			cfg.Providers.Default = primary
		}
	}
	if !fallbackDefined {
		primary := providerName(firstNonEmpty(cfg.Governor.NativeProvider, cfg.Providers.Default))
		cfg.Providers.FallbackChain = configuredProviderFallbacks(cfg, primary)
	}
}

func configuredProviderFallbacks(cfg *Config, primary string) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]struct{}{}
	if primary = providerName(primary); primary != "" {
		seen[primary] = struct{}{}
	}
	out := make([]string, 0, len(cfg.Providers.AutoOrder))
	for _, name := range cfg.Providers.AutoOrder {
		name = providerName(name)
		if name == "" || !providerConfigured(cfg, name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func firstConfiguredProviderByOrder(cfg *Config, order []string) string {
	for _, name := range order {
		name = providerName(name)
		if providerConfigured(cfg, name) {
			return name
		}
	}
	return ""
}

func providerConfigured(cfg *Config, name string) bool {
	if cfg == nil {
		return false
	}
	switch providerName(name) {
	case "anthropic":
		return strings.TrimSpace(cfg.Providers.Anthropic.APIKey) != ""
	case "openai":
		return strings.TrimSpace(cfg.Providers.OpenAI.APIKey) != ""
	case "openrouter":
		return strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) != ""
	case "gemini":
		return strings.TrimSpace(cfg.Providers.Gemini.APIKey) != ""
	case "ollama":
		return strings.TrimSpace(cfg.Providers.Ollama.BaseURL) != "" && strings.TrimSpace(cfg.Providers.Ollama.Model) != ""
	default:
		return false
	}
}

func normalizeProviderSelection(selection string) string {
	switch strings.ToLower(strings.TrimSpace(selection)) {
	case "", "auto":
		return "auto"
	case "manual", "explicit":
		return "manual"
	default:
		return strings.ToLower(strings.TrimSpace(selection))
	}
}

func normalizeAnthropicCacheStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "explicit":
		return "explicit"
	case "auto", "hybrid", "off":
		return strings.ToLower(strings.TrimSpace(strategy))
	default:
		return strings.ToLower(strings.TrimSpace(strategy))
	}
}

func normalizeAnthropicCacheTTL(ttl string) string {
	switch strings.ToLower(strings.TrimSpace(ttl)) {
	case "", "5m":
		return "5m"
	case "1h":
		return "1h"
	default:
		return strings.ToLower(strings.TrimSpace(ttl))
	}
}

func normalizeProviderNameList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		name := providerName(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func normalizeOpenAIModelFallbacks(primary string, fallbacks []string) []string {
	seen := map[string]struct{}{}
	if primary = strings.TrimSpace(primary); primary != "" {
		seen[primary] = struct{}{}
	}
	out := make([]string, 0, len(fallbacks))
	for _, raw := range fallbacks {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func EffectiveProviderChain(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 2+len(cfg.Providers.FallbackChain))
	for _, raw := range append([]string{cfg.Governor.NativeProvider, cfg.Providers.Default}, cfg.Providers.FallbackChain...) {
		name := providerName(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) > 0 {
		return out
	}
	for _, name := range cfg.Providers.AutoOrder {
		name = providerName(name)
		if name == "" || !providerConfigured(cfg, name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func EffectiveNativeProvider(cfg *Config) string {
	chain := EffectiveProviderChain(cfg)
	if len(chain) == 0 {
		return ""
	}
	return chain[0]
}

func providerName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isNativeProviderName(value string) bool {
	switch providerName(value) {
	case "anthropic", "openai", "openrouter", "gemini", "ollama":
		return true
	default:
		return false
	}
}

func NormalizeFaceBackendValue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "provider":
		return strings.ToLower(strings.TrimSpace(raw))
	case "floor_fallback":
		return "floor_fallback"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}
