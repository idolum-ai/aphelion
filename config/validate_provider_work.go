//go:build linux

package config

import (
	"fmt"
	"strings"
)

func validateFaceConfig(cfg FaceConfig) (string, error) {
	faceBackend := NormalizeFaceBackendValue(cfg.Backend)
	switch faceBackend {
	case "", "provider", "floor_fallback":
	default:
		return "", fmt.Errorf("face.backend must be one of provider|floor_fallback")
	}
	return faceBackend, nil
}

func validateProviderAndWorkSelectionConfig(cfg *Config) error {
	if err := validateProviderSelectionConfig(cfg.Providers); err != nil {
		return err
	}
	if err := validateWorkConfig(cfg.Work); err != nil {
		return err
	}
	return nil
}

func validateNativeProviderChainConfig(cfg *Config, governorBackend string, faceBackend string) error {
	nativePrimary := providerName(firstNonEmpty(strings.TrimSpace(cfg.Governor.NativeProvider), strings.TrimSpace(cfg.Providers.Default)))
	switch nativePrimary {
	case "":
		if nativePrimary == "" && (governorBackend == "native" || faceBackend == "" || faceBackend == "provider") {
			return fmt.Errorf("governor.native_provider or providers.default is required when native provider access is enabled")
		}
	default:
		if !isNativeProviderName(nativePrimary) {
			return fmt.Errorf("governor.native_provider must be one of anthropic|openai|openrouter|gemini|ollama")
		}
	}
	needsNativeProvider := governorBackend == "native" || faceBackend == "" || faceBackend == "provider" || len(cfg.Providers.FallbackChain) > 0
	if needsNativeProvider && nativePrimary == "" {
		return fmt.Errorf("governor.native_provider is required when native provider access is enabled")
	}
	if err := validateNativeProviderConfig(cfg.Providers, needsNativeProvider, nativePrimary); err != nil {
		return err
	}
	return nil
}

func validateProviderSelectionConfig(cfg ProvidersConfig) error {
	switch normalizeProviderSelection(cfg.Selection) {
	case "auto", "manual":
	default:
		return fmt.Errorf("providers.selection must be one of auto|manual")
	}
	if len(cfg.AutoOrder) == 0 {
		return fmt.Errorf("providers.auto_order must contain at least one provider")
	}
	for i, name := range cfg.AutoOrder {
		if !isNativeProviderName(providerName(name)) {
			return fmt.Errorf("providers.auto_order[%d] must be one of anthropic|openai|openrouter|gemini|ollama", i)
		}
	}
	switch providerName(strings.TrimSpace(cfg.Default)) {
	case "":
	case "anthropic", "openai", "openrouter", "gemini", "ollama":
	default:
		return fmt.Errorf("providers.default must be one of anthropic|openai|openrouter|gemini|ollama")
	}
	for i, name := range cfg.FallbackChain {
		if providerName(name) == "" {
			return fmt.Errorf("providers.fallback_chain[%d] must not be empty", i)
		}
		if !isNativeProviderName(providerName(name)) {
			return fmt.Errorf("providers.fallback_chain[%d] must be one of anthropic|openai|openrouter|gemini|ollama", i)
		}
	}
	return nil
}

func validateWorkConfig(cfg WorkConfig) error {
	switch normalizeWorkExecutor(cfg.Executor) {
	case "auto", "codex", "native":
	default:
		return fmt.Errorf("work.executor must be one of auto|codex|native")
	}
	if len(cfg.AutoOrder) == 0 {
		return fmt.Errorf("work.auto_order must contain at least one executor")
	}
	for i, name := range cfg.AutoOrder {
		switch normalizeWorkExecutor(name) {
		case "codex", "native":
		default:
			return fmt.Errorf("work.auto_order[%d] must be one of codex|native", i)
		}
	}
	return nil
}

func validateNativeProviderConfig(cfg ProvidersConfig, needsNativeProvider bool, nativePrimary string) error {
	if cfg.Anthropic.ContextWindow <= 0 {
		return fmt.Errorf("providers.anthropic.context_window must be > 0")
	}
	switch cfg.Anthropic.CacheStrategy {
	case "auto", "explicit", "hybrid", "off":
	default:
		return fmt.Errorf("providers.anthropic.cache_strategy must be one of auto|explicit|hybrid|off")
	}
	switch cfg.Anthropic.CacheTTL {
	case "5m", "1h":
	default:
		return fmt.Errorf("providers.anthropic.cache_ttl must be one of 5m|1h")
	}
	if cfg.OpenAI.ContextWindow <= 0 {
		return fmt.Errorf("providers.openai.context_window must be > 0")
	}
	if cfg.OpenRouter.ContextWindow <= 0 {
		return fmt.Errorf("providers.openrouter.context_window must be > 0")
	}
	if cfg.Gemini.ContextWindow <= 0 {
		return fmt.Errorf("providers.gemini.context_window must be > 0")
	}
	if cfg.Ollama.ContextWindow <= 0 {
		return fmt.Errorf("providers.ollama.context_window must be > 0")
	}
	if strings.TrimSpace(cfg.OpenAI.BaseURL) == "" {
		return fmt.Errorf("providers.openai.base_url is required")
	}
	if strings.TrimSpace(cfg.OpenRouter.BaseURL) == "" {
		return fmt.Errorf("providers.openrouter.base_url is required")
	}
	if strings.TrimSpace(cfg.Gemini.BaseURL) == "" {
		return fmt.Errorf("providers.gemini.base_url is required")
	}
	if strings.TrimSpace(cfg.Ollama.BaseURL) == "" {
		return fmt.Errorf("providers.ollama.base_url is required")
	}
	if !needsNativeProvider {
		return nil
	}
	required := append([]string{nativePrimary}, cfg.FallbackChain...)
	for _, name := range required {
		switch providerName(name) {
		case "anthropic":
			if strings.TrimSpace(cfg.Anthropic.APIKey) == "" {
				return fmt.Errorf("providers.anthropic.api_key is required when anthropic is in the native provider chain")
			}
		case "openai":
			if strings.TrimSpace(cfg.OpenAI.APIKey) == "" {
				return fmt.Errorf("providers.openai.api_key is required when openai is in the native provider chain")
			}
			if strings.TrimSpace(cfg.OpenAI.Model) == "" {
				return fmt.Errorf("providers.openai.model is required when openai is in the native provider chain")
			}
		case "openrouter":
			if strings.TrimSpace(cfg.OpenRouter.APIKey) == "" {
				return fmt.Errorf("providers.openrouter.api_key is required when openrouter is in the native provider chain")
			}
			if strings.TrimSpace(cfg.OpenRouter.Model) == "" {
				return fmt.Errorf("providers.openrouter.model is required when openrouter is in the native provider chain")
			}
		case "gemini":
			if strings.TrimSpace(cfg.Gemini.APIKey) == "" {
				return fmt.Errorf("providers.gemini.api_key is required when gemini is in the native provider chain")
			}
			if strings.TrimSpace(cfg.Gemini.Model) == "" {
				return fmt.Errorf("providers.gemini.model is required when gemini is in the native provider chain")
			}
		case "ollama":
			if strings.TrimSpace(cfg.Ollama.BaseURL) == "" {
				return fmt.Errorf("providers.ollama.base_url is required when ollama is in the native provider chain")
			}
			if strings.TrimSpace(cfg.Ollama.Model) == "" {
				return fmt.Errorf("providers.ollama.model is required when ollama is in the native provider chain")
			}
		}
	}
	return nil
}
