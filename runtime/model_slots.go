//go:build linux

package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/governorauth"
	"github.com/idolum-ai/aphelion/pipeline"
	providerpkg "github.com/idolum-ai/aphelion/provider"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) ModelSlotStatuses() ([]core.ModelSlotStatus, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime is nil")
	}
	statuses := make([]core.ModelSlotStatus, 0, len(core.ModelSlotNames()))
	for _, slot := range core.ModelSlotNames() {
		status, err := r.EffectiveModelSlot(slot)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (r *Runtime) EffectiveModelSlot(slot string) (core.ModelSlotStatus, error) {
	if r == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is nil")
	}
	slot = core.NormalizeModelSlot(slot)
	if slot == "" {
		return core.ModelSlotStatus{}, fmt.Errorf("model slot is required")
	}
	defaultCfg := r.defaultModelSlot(slot)
	status := core.ModelSlotStatus{
		Slot:      slot,
		Effective: defaultCfg,
		Source:    "default",
		Default:   defaultCfg,
	}
	if r.store != nil {
		if record, ok, err := r.store.ActiveModelSlotOverride(slot); err != nil {
			return core.ModelSlotStatus{}, err
		} else if ok {
			status.Effective = core.NormalizeModelSlotConfig(record.Config)
			status.Source = "override"
			status.OverrideID = record.ID
			status.CreatedBy = record.CreatedBy
			status.Reason = record.Reason
		}
	}
	status.Effective.Slot = slot
	status.Validation = r.ValidateModelSlotConfig(status.Effective)
	return status, nil
}

func (r *Runtime) ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation {
	cfg = core.NormalizeModelSlotConfig(cfg)
	validation := core.ValidateModelSlotConfig(cfg, core.ModelSlotUsesTools(cfg.Slot))
	if !validation.Valid {
		return validation
	}
	if err := r.validateConfiguredModelProvider(validation.Config.Provider); err != nil {
		validation.Valid = false
		validation.Error = err.Error()
	}
	return validation
}

func (r *Runtime) SetModelSlotOverride(cfg core.ModelSlotConfig, createdBy string, reason string) (core.ModelSlotStatus, error) {
	if r == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is nil")
	}
	cfg = core.NormalizeModelSlotConfig(cfg)
	if strings.TrimSpace(reason) != "" {
		cfg.Reason = strings.TrimSpace(reason)
	}
	validation := r.ValidateModelSlotConfig(cfg)
	if !validation.Valid {
		r.recordModelConfigEvent(core.ExecutionEventModelConfigRejected, "rejected", map[string]any{
			"slot":     cfg.Slot,
			"provider": cfg.Provider,
			"model":    cfg.Model,
			"error":    validation.Error,
		})
		return core.ModelSlotStatus{}, errors.New(validation.Error)
	}
	if r.store == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("session store is nil")
	}
	previous, err := r.EffectiveModelSlot(cfg.Slot)
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	record := session.ModelSlotOverrideRecord{
		Slot:           cfg.Slot,
		Config:         validation.Config,
		PreviousConfig: previous.Effective,
		CreatedBy:      strings.TrimSpace(createdBy),
		Reason:         strings.TrimSpace(firstNonEmptyRuntimeModel(reason, cfg.Reason)),
		CreatedAt:      time.Now().UTC(),
	}
	if _, err := r.store.SetModelSlotOverride(record); err != nil {
		return core.ModelSlotStatus{}, err
	}
	r.invalidateModelSlotCaches(cfg.Slot)
	status, err := r.EffectiveModelSlot(cfg.Slot)
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	r.recordModelConfigEvent(core.ExecutionEventModelConfigChanged, "active", map[string]any{
		"slot":               status.Slot,
		"override_id":        status.OverrideID,
		"provider":           status.Effective.Provider,
		"model":              status.Effective.Model,
		"effort":             status.Effective.Effort,
		"service_tier":       status.Effective.ServiceTier,
		"transport":          status.Effective.Transport,
		"resolved_transport": status.Validation.ResolvedTransport,
		"created_by":         strings.TrimSpace(createdBy),
		"reason":             strings.TrimSpace(reason),
	})
	return status, nil
}

func (r *Runtime) ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	return r.clearModelSlot(slot, "cleared", actor, reason)
}

func (r *Runtime) clearModelSlot(slot string, statusText string, actor string, reason string) (core.ModelSlotStatus, error) {
	if r == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is nil")
	}
	slot = core.NormalizeModelSlot(slot)
	if slot == "" {
		return core.ModelSlotStatus{}, fmt.Errorf("model slot is required")
	}
	if r.store == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("session store is nil")
	}
	active, ok, err := r.store.ClearModelSlotOverride(slot, statusText, time.Now().UTC())
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	r.invalidateModelSlotCaches(slot)
	status, err := r.EffectiveModelSlot(slot)
	if err != nil {
		return core.ModelSlotStatus{}, err
	}
	payload := map[string]any{
		"slot":       slot,
		"created_by": strings.TrimSpace(actor),
		"reason":     strings.TrimSpace(reason),
	}
	if ok {
		payload["override_id"] = active.ID
	}
	r.recordModelConfigEvent(core.ExecutionEventModelConfigChanged, statusText, payload)
	return status, nil
}

func (r *Runtime) ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("session store is nil")
	}
	return r.store.ModelSlotOverrideHistory(slot, limit)
}

func (r *Runtime) defaultModelSlot(slot string) core.ModelSlotConfig {
	slot = core.NormalizeModelSlot(slot)
	switch slot {
	case core.ModelSlotPersona:
		return r.defaultPersonaModelSlot()
	case core.ModelSlotDoctor:
		governor := r.defaultGovernorModelSlot()
		governor.Slot = core.ModelSlotDoctor
		return governor
	case core.ModelSlotChildDefault:
		providerName := r.nativeProviderName()
		return core.NormalizeModelSlotConfig(core.ModelSlotConfig{
			Slot:      slot,
			Provider:  providerName,
			Model:     r.nativeModelName(),
			Effort:    defaultReasoningEffortFromConfig(r.cfg),
			Transport: core.ModelTransportAuto,
		})
	default:
		return r.defaultGovernorModelSlot()
	}
}

func (r *Runtime) defaultGovernorModelSlot() core.ModelSlotConfig {
	if r == nil {
		return core.ModelSlotConfig{Slot: core.ModelSlotGovernor, Transport: core.ModelTransportAuto}
	}
	snapshot := r.currentRecipeSnapshot()
	return core.NormalizeModelSlotConfig(core.ModelSlotConfig{
		Slot:      core.ModelSlotGovernor,
		Provider:  r.governorProviderName(),
		Model:     r.governorModelName(),
		Effort:    snapshot.GovernorEffort,
		Transport: core.ModelTransportAuto,
	})
}

func (r *Runtime) defaultPersonaModelSlot() core.ModelSlotConfig {
	snapshot := r.currentRecipeSnapshot()
	providerName, modelName := r.defaultPersonaProviderModel(snapshot.PersonaModel)
	return core.NormalizeModelSlotConfig(core.ModelSlotConfig{
		Slot:      core.ModelSlotPersona,
		Provider:  providerName,
		Model:     modelName,
		Transport: core.ModelTransportAuto,
	})
}

func (r *Runtime) defaultPersonaProviderModel(personaModel string) (string, string) {
	if r == nil || r.cfg == nil {
		return "", ""
	}
	for _, name := range orderedFaceProviderNames(r.cfg) {
		model := faceModelForProvider(name, personaModel)
		if model == "" || !r.modelProviderConfigured(name) {
			continue
		}
		return core.NormalizeModelProvider(name), model
	}
	for _, name := range []string{"anthropic", "openai", "openrouter"} {
		model := faceModelForProvider(name, personaModel)
		if model == "" || !r.modelProviderConfigured(name) {
			continue
		}
		return core.NormalizeModelProvider(name), model
	}
	return core.NormalizeModelProvider(r.faceProviderName()), strings.TrimSpace(personaModel)
}

func defaultReasoningEffortFromConfig(cfg *config.Config) string {
	if cfg == nil {
		return string(agent.ReasoningEffortMedium)
	}
	effort := firstNonEmptyThinking(cfg.Thinking.Defaults.Default, cfg.Thinking.Effort)
	if effort == "" {
		return string(agent.ReasoningEffortMedium)
	}
	return core.NormalizeModelEffort(effort)
}

func (r *Runtime) modelSlotProvider(slot string) (agent.Provider, core.ModelSlotStatus, bool) {
	status, err := r.EffectiveModelSlot(slot)
	if err != nil || status.Source != "override" || !status.Validation.Valid {
		return nil, status, false
	}
	provider, err := r.cachedProviderForModelSlot(status.Effective)
	if err != nil {
		r.recordModelConfigEvent(core.ExecutionEventModelConfigRejected, "provider_build_failed", map[string]any{
			"slot":     status.Slot,
			"provider": status.Effective.Provider,
			"model":    status.Effective.Model,
			"error":    err.Error(),
		})
		return nil, status, false
	}
	return provider, status, true
}

func (r *Runtime) cachedProviderForModelSlot(cfg core.ModelSlotConfig) (agent.Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime is nil")
	}
	cfg = core.NormalizeModelSlotConfig(cfg)
	key := modelSlotProviderCacheKey(cfg)
	r.modelProviderMu.Lock()
	if r.modelProviderCache == nil {
		r.modelProviderCache = make(map[string]agent.Provider)
	}
	if provider := r.modelProviderCache[key]; provider != nil {
		r.modelProviderMu.Unlock()
		return provider, nil
	}
	r.modelProviderMu.Unlock()

	provider, err := buildProviderForModelSlot(r.cfg, cfg)
	if err != nil {
		return nil, err
	}

	r.modelProviderMu.Lock()
	r.modelProviderCache[key] = provider
	r.modelProviderMu.Unlock()
	return provider, nil
}

func buildProviderForModelSlot(cfg *config.Config, slot core.ModelSlotConfig) (agent.Provider, error) {
	slot = core.NormalizeModelSlotConfig(slot)
	httpClient := &http.Client{Timeout: 90 * time.Second}
	candidates := []core.ModelFallback{{Provider: slot.Provider, Model: slot.Model}}
	candidates = append(candidates, slot.Fallbacks...)
	entries := make([]providerpkg.NamedProvider, 0, len(candidates))
	for _, candidate := range candidates {
		providerName := core.NormalizeModelProvider(candidate.Provider)
		model := strings.TrimSpace(candidate.Model)
		if providerName == "" || model == "" {
			continue
		}
		candidateSlot := slot
		candidateSlot.Provider = providerName
		candidateSlot.Model = model
		provider, err := buildSingleProviderForModelSlot(cfg, candidateSlot, httpClient)
		if err != nil {
			return nil, err
		}
		entries = append(entries, providerpkg.NamedProvider{
			Name:     providerName + ":" + model,
			Provider: provider,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no model providers configured for slot %s", slot.Slot)
	}
	if len(entries) == 1 {
		return entries[0].Provider, nil
	}
	return providerpkg.NewFailoverChain(entries)
}

func buildSingleProviderForModelSlot(cfg *config.Config, slot core.ModelSlotConfig, httpClient *http.Client) (agent.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	slot = core.NormalizeModelSlotConfig(slot)
	providerName := slot.Provider
	model := strings.TrimSpace(slot.Model)
	resolvedTransport := core.ResolveModelTransport(slot, core.ModelSlotUsesTools(slot.Slot))
	switch core.NormalizeModelProvider(providerName) {
	case core.ModelProviderOpenAI:
		return providerpkg.NewOpenAI(providerpkg.OpenAIOptions{
			APIKey:      cfg.Providers.OpenAI.APIKey,
			BaseURL:     cfg.Providers.OpenAI.BaseURL,
			Model:       model,
			MaxTokens:   cfg.Providers.OpenAI.MaxTokens,
			Transport:   resolvedTransport,
			ServiceTier: slot.ServiceTier,
			HTTPClient:  httpClient,
			UserAgent:   config.EffectiveUserAgent(cfg, ""),
		})
	case core.ModelProviderAnthropic:
		return providerpkg.NewAnthropic(providerpkg.AnthropicOptions{
			APIKey:        cfg.Providers.Anthropic.APIKey,
			Model:         model,
			MaxTokens:     cfg.Providers.Anthropic.MaxTokens,
			CacheStrategy: cfg.Providers.Anthropic.CacheStrategy,
			CacheTTL:      cfg.Providers.Anthropic.CacheTTL,
			HTTPClient:    httpClient,
			UserAgent:     config.EffectiveUserAgent(cfg, ""),
		})
	case core.ModelProviderOpenRouter:
		return providerpkg.NewOpenRouter(providerpkg.OpenRouterOptions{
			APIKey:     cfg.Providers.OpenRouter.APIKey,
			BaseURL:    cfg.Providers.OpenRouter.BaseURL,
			Model:      model,
			MaxTokens:  cfg.Providers.OpenRouter.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case core.ModelProviderGemini:
		return providerpkg.NewGemini(providerpkg.GeminiOptions{
			APIKey:     cfg.Providers.Gemini.APIKey,
			BaseURL:    cfg.Providers.Gemini.BaseURL,
			Model:      model,
			MaxTokens:  cfg.Providers.Gemini.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case core.ModelProviderOllama:
		return providerpkg.NewOllama(providerpkg.OllamaOptions{
			BaseURL:    cfg.Providers.Ollama.BaseURL,
			Model:      model,
			MaxTokens:  cfg.Providers.Ollama.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case core.ModelProviderCodex:
		local := *cfg
		local.Governor.Backend = governorauth.BackendCodex
		local.Governor.Codex.Model = model
		bundle, err := resolveGovernorAuth(local.Governor)
		if err != nil {
			return nil, err
		}
		return newCodexProvider(bundle, &local)
	default:
		return nil, fmt.Errorf("unsupported model provider %q", providerName)
	}
}

func (r *Runtime) applyModelSlotExecution(exec *pipeline.TurnExecutionContract, slot string) {
	if exec == nil {
		return
	}
	provider, status, ok := r.modelSlotProvider(slot)
	if !ok {
		return
	}
	exec.Provider = provider
	exec.Backend = status.Effective.Provider
	exec.ProviderName = status.Effective.Provider
	exec.ModelName = status.Effective.Model
	exec.ProviderPath = modelSlotProviderPath(status.Effective)
}

func modelSlotProviderPath(cfg core.ModelSlotConfig) []string {
	cfg = core.NormalizeModelSlotConfig(cfg)
	out := []string{cfg.Provider}
	for _, fallback := range cfg.Fallbacks {
		name := core.NormalizeModelProvider(fallback.Provider)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func modelSlotProviderCacheKey(cfg core.ModelSlotConfig) string {
	cfg = core.NormalizeModelSlotConfig(cfg)
	data, _ := json.Marshal(cfg)
	return string(data)
}

func (r *Runtime) invalidateModelSlotCaches(slot string) {
	if r == nil {
		return
	}
	r.modelProviderMu.Lock()
	r.modelProviderCache = make(map[string]agent.Provider)
	r.modelProviderMu.Unlock()
	if core.NormalizeModelSlot(slot) == core.ModelSlotPersona {
		r.faceModelsMu.Lock()
		r.faceModels = make(map[string]face.Renderer)
		r.faceModelsMu.Unlock()
	}
}

func (r *Runtime) validateConfiguredModelProvider(providerName string) error {
	if r == nil {
		return fmt.Errorf("runtime is nil")
	}
	if !r.modelProviderConfigured(providerName) {
		return fmt.Errorf("%s provider is not configured", providerName)
	}
	return nil
}

func (r *Runtime) modelProviderConfigured(providerName string) bool {
	if r == nil || r.cfg == nil {
		return false
	}
	switch core.NormalizeModelProvider(providerName) {
	case core.ModelProviderOpenAI:
		return strings.TrimSpace(r.cfg.Providers.OpenAI.APIKey) != ""
	case core.ModelProviderAnthropic:
		return strings.TrimSpace(r.cfg.Providers.Anthropic.APIKey) != ""
	case core.ModelProviderOpenRouter:
		return strings.TrimSpace(r.cfg.Providers.OpenRouter.APIKey) != ""
	case core.ModelProviderGemini:
		return strings.TrimSpace(r.cfg.Providers.Gemini.APIKey) != ""
	case core.ModelProviderOllama:
		return strings.TrimSpace(r.cfg.Providers.Ollama.BaseURL) != "" && strings.TrimSpace(r.cfg.Providers.Ollama.Model) != ""
	case core.ModelProviderCodex:
		local := *r.cfg
		local.Governor.Backend = governorauth.BackendCodex
		_, err := resolveGovernorAuth(local.Governor)
		return err == nil
	default:
		return false
	}
}

func (r *Runtime) recordModelConfigEvent(eventType string, status string, payload map[string]any) {
	if r == nil {
		return
	}
	key := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
	r.recordExecutionEvent(key, eventType, "model_config", status, payload, time.Now().UTC())
}

func firstNonEmptyRuntimeModel(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
