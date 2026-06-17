//go:build linux

package runtime

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	providerpkg "github.com/idolum-ai/aphelion/provider"
)

func (r *Runtime) currentFaceRenderer() face.Renderer {
	if r == nil {
		return nil
	}
	if r.faceBackend == face.BackendFloorFallback {
		return r.faceModel
	}
	if status, err := r.EffectiveModelSlot(core.ModelSlotPersona); err == nil && status.Source == "override" && status.Validation.Valid {
		key := "slot:" + modelSlotProviderCacheKey(status.Effective)
		r.faceModelsMu.Lock()
		renderer, ok := r.faceModels[key]
		r.faceModelsMu.Unlock()
		if ok && renderer != nil {
			return renderer
		}
		provider, err := r.cachedProviderForModelSlot(status.Effective)
		if err == nil {
			renderer, err = r.newFaceRendererForProvider(provider, status.Effective)
		}
		if err == nil && renderer != nil {
			r.faceModelsMu.Lock()
			if r.faceModels == nil {
				r.faceModels = make(map[string]face.Renderer)
			}
			r.faceModels[key] = renderer
			r.faceModelsMu.Unlock()
			return renderer
		}
	}
	snapshot := r.currentRecipeSnapshot()
	key := snapshot.PersonaModel
	if key == "" {
		key = personaModelSonnet
	}

	r.faceModelsMu.Lock()
	renderer, ok := r.faceModels[key]
	r.faceModelsMu.Unlock()
	if ok && renderer != nil {
		return renderer
	}

	renderer, err := r.buildFaceRendererForRecipe(key)
	if err != nil {
		return r.faceModel
	}
	r.faceModelsMu.Lock()
	if r.faceModels == nil {
		r.faceModels = make(map[string]face.Renderer)
	}
	r.faceModels[key] = renderer
	r.faceModelsMu.Unlock()
	return renderer
}

func (r *Runtime) buildFaceRendererForRecipe(recipe string) (face.Renderer, error) {
	if r == nil {
		return nil, fmt.Errorf("runtime is nil")
	}
	if r.faceBackend == face.BackendFloorFallback {
		return r.faceModel, nil
	}
	provider, err := buildFaceProviderChainForRecipe(r.cfg, recipe)
	if err != nil {
		return nil, err
	}
	providerName, modelName := r.defaultPersonaProviderModel(recipe)
	return r.newFaceRendererForProvider(provider, core.ModelSlotConfig{
		Provider: providerName,
		Model:    modelName,
	})
}

func (r *Runtime) newFaceRendererForProvider(provider agent.Provider, slot core.ModelSlotConfig) (face.Renderer, error) {
	reasoning := agent.ReasoningConfig{}
	if effort := core.NormalizeModelEffort(slot.Effort); effort != "" {
		reasoning.Effort = agent.ReasoningEffort(effort)
		reasoning.Summary = agent.ReasoningSummaryAuto
	}
	return newFaceRenderer(provider, face.ProviderRendererConfig{
		GovernorName:  r.governorName(),
		FaceName:      r.faceName(),
		Channel:       "telegram",
		WorkspaceRoot: r.cfg.Agent.PromptRoot,
		CacheStrategy: r.facePromptCacheStrategyForProvider(slot.Provider),
		Reasoning:     reasoning,
		MaxTokens:     faceRenderMaxTokens,
	})
}

func (r *Runtime) facePromptCacheStrategyForProvider(providerName string) string {
	if r == nil || r.cfg == nil {
		return ""
	}
	return facePromptCacheStrategyForConfig(r.cfg, providerName)
}

func facePromptCacheStrategyForConfig(cfg *config.Config, providerName string) string {
	return promptCacheStrategyForProviderConfig(cfg, providerName)
}

func buildFaceProviderChainForRecipe(cfg *config.Config, personaModel string) (agent.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	httpClient := &http.Client{Timeout: 90 * time.Second}
	names := orderedFaceProviderNames(cfg)
	entries := make([]providerpkg.NamedProvider, 0, len(names)+len(cfg.Providers.OpenAI.FallbackModels))
	for _, name := range names {
		built, err := buildNamedFaceProviderEntries(name, cfg, personaModel, httpClient)
		if err != nil {
			return nil, err
		}
		entries = append(entries, built...)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no face providers configured")
	}
	if len(entries) == 1 {
		return entries[0].Provider, nil
	}
	return providerpkg.NewFailoverChain(entries)
}

func buildNamedFaceProviderEntries(name string, cfg *config.Config, personaModel string, httpClient *http.Client) ([]providerpkg.NamedProvider, error) {
	if strings.EqualFold(strings.TrimSpace(name), "openai") {
		if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
			return nil, nil
		}
		model := faceModelForProvider("openai", personaModel)
		if model == "" {
			return nil, nil
		}
		models := openAIFaceModelChain(model, cfg.Providers.OpenAI.FallbackModels)
		entries := make([]providerpkg.NamedProvider, 0, len(models))
		for _, model := range models {
			p, err := providerpkg.NewOpenAI(providerpkg.OpenAIOptions{
				APIKey:     cfg.Providers.OpenAI.APIKey,
				BaseURL:    cfg.Providers.OpenAI.BaseURL,
				Model:      model,
				MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
				HTTPClient: httpClient,
				UserAgent:  config.EffectiveUserAgent(cfg, ""),
			})
			if err != nil {
				return nil, err
			}
			entries = append(entries, providerpkg.NamedProvider{Name: "openai:" + model, Provider: p})
		}
		return entries, nil
	}
	p, err := buildNamedFaceProvider(name, cfg, personaModel, httpClient)
	if err != nil || p == nil {
		return nil, err
	}
	return []providerpkg.NamedProvider{{Name: strings.ToLower(strings.TrimSpace(name)), Provider: p}}, nil
}

func openAIFaceModelChain(primary string, fallbacks []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 1+len(fallbacks))
	for _, raw := range append([]string{primary}, fallbacks...) {
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

func orderedFaceProviderNames(cfg *config.Config) []string {
	return config.EffectiveProviderChain(cfg)
}

func buildNamedFaceProvider(name string, cfg *config.Config, personaModel string, httpClient *http.Client) (agent.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		if strings.TrimSpace(cfg.Providers.Anthropic.APIKey) == "" {
			return nil, nil
		}
		model := faceModelForProvider("anthropic", personaModel)
		if model == "" {
			return nil, nil
		}
		return providerpkg.NewAnthropic(providerpkg.AnthropicOptions{
			APIKey:        cfg.Providers.Anthropic.APIKey,
			Model:         model,
			MaxTokens:     cfg.Providers.Anthropic.MaxTokens,
			CacheStrategy: cfg.Providers.Anthropic.CacheStrategy,
			CacheTTL:      cfg.Providers.Anthropic.CacheTTL,
			HTTPClient:    httpClient,
			UserAgent:     config.EffectiveUserAgent(cfg, ""),
		})
	case "openai":
		if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
			return nil, nil
		}
		model := faceModelForProvider("openai", personaModel)
		if model == "" {
			return nil, nil
		}
		return providerpkg.NewOpenAI(providerpkg.OpenAIOptions{
			APIKey:     cfg.Providers.OpenAI.APIKey,
			BaseURL:    cfg.Providers.OpenAI.BaseURL,
			Model:      model,
			MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "openrouter":
		if strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) == "" {
			return nil, nil
		}
		model := faceModelForProvider("openrouter", personaModel)
		if model == "" {
			return nil, nil
		}
		return providerpkg.NewOpenRouter(providerpkg.OpenRouterOptions{
			APIKey:     cfg.Providers.OpenRouter.APIKey,
			BaseURL:    cfg.Providers.OpenRouter.BaseURL,
			Model:      model,
			MaxTokens:  cfg.Providers.OpenRouter.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "gemini":
		if strings.TrimSpace(cfg.Providers.Gemini.APIKey) == "" {
			return nil, nil
		}
		model := strings.TrimSpace(cfg.Providers.Gemini.Model)
		if model == "" {
			return nil, nil
		}
		return providerpkg.NewGemini(providerpkg.GeminiOptions{
			APIKey:     cfg.Providers.Gemini.APIKey,
			BaseURL:    cfg.Providers.Gemini.BaseURL,
			Model:      model,
			MaxTokens:  cfg.Providers.Gemini.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "ollama":
		model := strings.TrimSpace(cfg.Providers.Ollama.Model)
		if strings.TrimSpace(cfg.Providers.Ollama.BaseURL) == "" || model == "" {
			return nil, nil
		}
		return providerpkg.NewOllama(providerpkg.OllamaOptions{
			BaseURL:    cfg.Providers.Ollama.BaseURL,
			Model:      model,
			MaxTokens:  cfg.Providers.Ollama.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported face provider %q", name)
	}
}

func faceModelForProvider(providerName, personaModel string) string {
	model := normalizePersonaModel(personaModel)
	if model == "" {
		model = personaModelSonnet
	}
	provider := strings.ToLower(strings.TrimSpace(providerName))
	if model == personaModelGPT55 {
		switch provider {
		case "anthropic":
			return personaModelSonnet
		case "openai":
			return model
		case "openrouter":
			return "openai/" + model
		default:
			return ""
		}
	}
	if model == personaModelSonnet || model == personaModelOpus46 || model == personaModelOpus47 {
		switch provider {
		case "anthropic":
			return model
		case "openrouter":
			return "anthropic/" + model
		default:
			return ""
		}
	}
	return ""
}

func (r *Runtime) faceModelName() string {
	if r.faceBackend == face.BackendFloorFallback {
		return r.governorModelName()
	}
	snapshot := r.currentRecipeSnapshot()
	if model := faceModelForProvider(r.faceProviderName(), snapshot.PersonaModel); model != "" {
		return model
	}
	return snapshot.PersonaModel
}
