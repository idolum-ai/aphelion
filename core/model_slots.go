//go:build linux

package core

import (
	"fmt"
	"strings"
)

const (
	ModelSlotPersona      = "persona"
	ModelSlotGovernor     = "governor"
	ModelSlotDoctor       = "doctor"
	ModelSlotChildDefault = "child_default"

	ModelProviderOpenAI     = "openai"
	ModelProviderAnthropic  = "anthropic"
	ModelProviderOpenRouter = "openrouter"
	ModelProviderGemini     = "gemini"
	ModelProviderOllama     = "ollama"
	ModelProviderCodex      = "codex"

	ModelTransportAuto              = "auto"
	ModelTransportOpenAIResponses   = "responses"
	ModelTransportOpenAIChat        = "chat_completions"
	ModelTransportAnthropicMessages = "anthropic_messages"
	ModelTransportOpenRouterChat    = "openrouter_chat"
	ModelTransportGeminiGenerate    = "gemini_generate_content"
	ModelTransportOllamaChat        = "ollama_chat"
	ModelTransportCodex             = "codex"

	ModelServiceTierPriority = "priority"
)

var modelSlots = []string{
	ModelSlotPersona,
	ModelSlotGovernor,
	ModelSlotDoctor,
	ModelSlotChildDefault,
}

type ModelFallback struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type ModelSlotConfig struct {
	Slot        string          `json:"slot,omitempty"`
	Provider    string          `json:"provider,omitempty"`
	Model       string          `json:"model,omitempty"`
	Effort      string          `json:"effort,omitempty"`
	Transport   string          `json:"transport,omitempty"`
	ServiceTier string          `json:"service_tier,omitempty"`
	Fallbacks   []ModelFallback `json:"fallbacks,omitempty"`
	Reason      string          `json:"reason,omitempty"`
}

type ModelSlotStatus struct {
	Slot       string          `json:"slot"`
	Effective  ModelSlotConfig `json:"effective"`
	Source     string          `json:"source"`
	OverrideID int64           `json:"override_id,omitempty"`
	CreatedBy  string          `json:"created_by,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Validation ModelValidation `json:"validation"`
	Default    ModelSlotConfig `json:"default"`
}

type ModelValidation struct {
	Valid             bool            `json:"valid"`
	ResolvedTransport string          `json:"resolved_transport,omitempty"`
	Config            ModelSlotConfig `json:"config"`
	Warnings          []string        `json:"warnings,omitempty"`
	Error             string          `json:"error,omitempty"`
}

func ModelSlotNames() []string {
	return append([]string(nil), modelSlots...)
}

func NormalizeModelSlotConfig(cfg ModelSlotConfig) ModelSlotConfig {
	cfg.Slot = NormalizeModelSlot(cfg.Slot)
	cfg.Provider = NormalizeModelProvider(cfg.Provider)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.Effort = NormalizeModelEffort(cfg.Effort)
	cfg.Transport = NormalizeModelTransport(cfg.Transport)
	cfg.ServiceTier = NormalizeModelServiceTier(cfg.ServiceTier)
	cfg.Reason = strings.TrimSpace(cfg.Reason)
	if cfg.Transport == "" {
		cfg.Transport = ModelTransportAuto
	}
	fallbacks := make([]ModelFallback, 0, len(cfg.Fallbacks))
	for _, fallback := range cfg.Fallbacks {
		provider := NormalizeModelProvider(fallback.Provider)
		model := strings.TrimSpace(fallback.Model)
		if provider == "" && model == "" {
			continue
		}
		if provider == "" {
			provider = cfg.Provider
		}
		fallbacks = append(fallbacks, ModelFallback{Provider: provider, Model: model})
	}
	cfg.Fallbacks = fallbacks
	return cfg
}

func NormalizeModelServiceTier(serviceTier string) string {
	switch strings.ToLower(strings.TrimSpace(serviceTier)) {
	case "", "standard", "default":
		return ""
	case "fast", ModelServiceTierPriority:
		return ModelServiceTierPriority
	default:
		return ""
	}
}

func NormalizeModelSlot(slot string) string {
	switch strings.ToLower(strings.TrimSpace(slot)) {
	case ModelSlotPersona, "face", "idolum":
		return ModelSlotPersona
	case ModelSlotGovernor, "system":
		return ModelSlotGovernor
	case ModelSlotDoctor, "diagnostic", "diagnostics":
		return ModelSlotDoctor
	case ModelSlotChildDefault, "child", "children", "durable_child", "child-default":
		return ModelSlotChildDefault
	default:
		return ""
	}
}

func NormalizeModelProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ModelProviderOpenAI, "oai":
		return ModelProviderOpenAI
	case ModelProviderAnthropic, "claude":
		return ModelProviderAnthropic
	case ModelProviderOpenRouter, "or":
		return ModelProviderOpenRouter
	case ModelProviderGemini, "google":
		return ModelProviderGemini
	case ModelProviderOllama, "local":
		return ModelProviderOllama
	case ModelProviderCodex:
		return ModelProviderCodex
	default:
		return ""
	}
}

func NormalizeModelEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "default":
		return ""
	case "none", "off":
		return "none"
	case "low":
		return "low"
	case "medium", "balanced", "normal":
		return "medium"
	case "high", "deep":
		return "high"
	case "xhigh", "extra", "max", "maximum":
		return "xhigh"
	default:
		return ""
	}
}

func NormalizeModelTransport(transport string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "", ModelTransportAuto:
		return ModelTransportAuto
	case ModelTransportOpenAIResponses, "openai_responses":
		return ModelTransportOpenAIResponses
	case ModelTransportOpenAIChat, "chat", "chat_completion":
		return ModelTransportOpenAIChat
	case ModelTransportAnthropicMessages, "messages":
		return ModelTransportAnthropicMessages
	case ModelTransportOpenRouterChat, "openrouter":
		return ModelTransportOpenRouterChat
	case ModelTransportGeminiGenerate, "gemini":
		return ModelTransportGeminiGenerate
	case ModelTransportOllamaChat, "ollama":
		return ModelTransportOllamaChat
	case ModelTransportCodex:
		return ModelTransportCodex
	default:
		return ""
	}
}

func ParseProviderModel(raw string) (provider string, model string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 {
		return "", trimmed
	}
	if provider = NormalizeModelProvider(parts[0]); provider == "" {
		return "", trimmed
	}
	return provider, strings.TrimSpace(parts[1])
}

func ValidateModelSlotConfig(cfg ModelSlotConfig, usesTools bool) ModelValidation {
	rawServiceTier := strings.TrimSpace(cfg.ServiceTier)
	normalized := NormalizeModelSlotConfig(cfg)
	result := ModelValidation{Config: normalized}
	if normalized.Slot == "" {
		result.Error = "model slot must be one of persona, governor, doctor, child_default"
		return result
	}
	if normalized.Provider == "" {
		result.Error = "model provider must be one of openai, anthropic, openrouter, gemini, ollama, codex"
		return result
	}
	if normalized.Model == "" {
		result.Error = "model is required"
		return result
	}
	if normalized.Transport == "" {
		result.Error = "transport must be auto, responses, chat_completions, anthropic_messages, openrouter_chat, gemini_generate_content, ollama_chat, or codex"
		return result
	}
	if rawServiceTier != "" && normalized.ServiceTier == "" && !isModelServiceTierStandardAlias(rawServiceTier) {
		result.Error = "speed must be standard or fast"
		return result
	}
	if normalized.ServiceTier != "" && normalized.Provider != ModelProviderOpenAI {
		result.Error = "fast mode is only available for openai model slots"
		return result
	}

	resolved := ResolveModelTransport(normalized, usesTools)
	if resolved == "" {
		result.Error = fmt.Sprintf("provider %s does not support transport %s", normalized.Provider, normalized.Transport)
		return result
	}
	result.ResolvedTransport = resolved
	if err := validateResolvedModelTransport(normalized, resolved, usesTools); err != nil {
		result.Error = err.Error()
		return result
	}
	if normalized.Provider == ModelProviderOpenRouter && normalized.Effort != "" && normalized.Effort != "none" {
		result.Warnings = append(result.Warnings, "openrouter may ignore configured effort depending on the routed model")
	}
	result.Valid = true
	return result
}

func isModelServiceTierStandardAlias(serviceTier string) bool {
	switch strings.ToLower(strings.TrimSpace(serviceTier)) {
	case "", "standard", "default":
		return true
	default:
		return false
	}
}

func ResolveModelTransport(cfg ModelSlotConfig, usesTools bool) string {
	cfg = NormalizeModelSlotConfig(cfg)
	if cfg.Transport != ModelTransportAuto {
		return cfg.Transport
	}
	switch cfg.Provider {
	case ModelProviderOpenAI:
		if isGPT5Model(cfg.Model) && cfg.Effort != "" && cfg.Effort != "none" {
			return ModelTransportOpenAIResponses
		}
		return ModelTransportOpenAIChat
	case ModelProviderAnthropic:
		return ModelTransportAnthropicMessages
	case ModelProviderOpenRouter:
		return ModelTransportOpenRouterChat
	case ModelProviderGemini:
		return ModelTransportGeminiGenerate
	case ModelProviderOllama:
		return ModelTransportOllamaChat
	case ModelProviderCodex:
		return ModelTransportCodex
	default:
		return ""
	}
}

func validateResolvedModelTransport(cfg ModelSlotConfig, resolved string, usesTools bool) error {
	switch cfg.Provider {
	case ModelProviderOpenAI:
		switch resolved {
		case ModelTransportOpenAIResponses, ModelTransportOpenAIChat:
		default:
			return fmt.Errorf("openai requires responses or chat_completions transport")
		}
		if usesTools && isGPT5Model(cfg.Model) && cfg.Effort != "" && cfg.Effort != "none" && resolved == ModelTransportOpenAIChat {
			return fmt.Errorf("openai %s with tools and effort requires responses transport", cfg.Model)
		}
	case ModelProviderAnthropic:
		if resolved != ModelTransportAnthropicMessages {
			return fmt.Errorf("anthropic requires anthropic_messages transport")
		}
	case ModelProviderOpenRouter:
		if resolved != ModelTransportOpenRouterChat {
			return fmt.Errorf("openrouter requires openrouter_chat transport")
		}
	case ModelProviderGemini:
		if resolved != ModelTransportGeminiGenerate {
			return fmt.Errorf("gemini requires gemini_generate_content transport")
		}
	case ModelProviderOllama:
		if resolved != ModelTransportOllamaChat {
			return fmt.Errorf("ollama requires ollama_chat transport")
		}
	case ModelProviderCodex:
		if resolved != ModelTransportCodex {
			return fmt.Errorf("codex requires codex transport")
		}
	}
	return nil
}

func ModelSlotUsesTools(slot string) bool {
	switch NormalizeModelSlot(slot) {
	case ModelSlotGovernor, ModelSlotDoctor, ModelSlotChildDefault:
		return true
	default:
		return false
	}
}

func isGPT5Model(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-5")
}
