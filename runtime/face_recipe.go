//go:build linux

package runtime

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	providerpkg "github.com/idolum-ai/aphelion/provider"
)

func (r *Runtime) currentFaceRenderer() face.Renderer {
	if r == nil {
		return nil
	}
	if r.faceBackend == face.BackendFloorFallback {
		return r.faceModel
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
	return newFaceRenderer(provider, face.ProviderRendererConfig{
		GovernorName:  prompt.DefaultGovernorName,
		FaceName:      face.DefaultFaceName,
		Channel:       "telegram",
		WorkspaceRoot: r.cfg.Agent.PromptRoot,
	})
}

func buildFaceProviderChainForRecipe(cfg *config.Config, personaModel string) (agent.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	httpClient := &http.Client{Timeout: 90 * time.Second}
	names := orderedFaceProviderNames(cfg)
	entries := make([]providerpkg.NamedProvider, 0, len(names))
	for _, name := range names {
		p, err := buildNamedFaceProvider(name, cfg, personaModel, httpClient)
		if err != nil {
			return nil, err
		}
		if p == nil {
			continue
		}
		entries = append(entries, providerpkg.NamedProvider{Name: name, Provider: p})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no face providers configured")
	}
	if len(entries) == 1 {
		return entries[0].Provider, nil
	}
	return providerpkg.NewFailoverChain(entries)
}

func orderedFaceProviderNames(cfg *config.Config) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 1+len(cfg.Providers.FallbackChain))
	for _, raw := range append([]string{resolveFaceProviderName(cfg)}, cfg.Providers.FallbackChain...) {
		name := strings.ToLower(strings.TrimSpace(raw))
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

func resolveFaceProviderName(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if name := strings.ToLower(strings.TrimSpace(cfg.Governor.NativeProvider)); name != "" {
		return name
	}
	if name := strings.ToLower(strings.TrimSpace(cfg.Providers.Default)); name != "" {
		return name
	}
	return "anthropic"
}

func buildNamedFaceProvider(name string, cfg *config.Config, personaModel string, httpClient *http.Client) (agent.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		if strings.TrimSpace(cfg.Providers.Anthropic.APIKey) == "" {
			return nil, nil
		}
		return providerpkg.NewAnthropic(providerpkg.AnthropicOptions{
			APIKey:     cfg.Providers.Anthropic.APIKey,
			Model:      faceModelForProvider("anthropic", personaModel),
			MaxTokens:  cfg.Providers.Anthropic.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  cfg.Identity.UserAgent,
		})
	case "openrouter":
		if strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) == "" {
			return nil, nil
		}
		return providerpkg.NewOpenRouter(providerpkg.OpenRouterOptions{
			APIKey:     cfg.Providers.OpenRouter.APIKey,
			BaseURL:    cfg.Providers.OpenRouter.BaseURL,
			Model:      faceModelForProvider("openrouter", personaModel),
			MaxTokens:  cfg.Providers.OpenRouter.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  cfg.Identity.UserAgent,
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
	if model == personaModelOpus {
		if strings.EqualFold(strings.TrimSpace(providerName), "openrouter") {
			return "anthropic/" + personaModelOpus
		}
		return personaModelOpus
	}
	if strings.EqualFold(strings.TrimSpace(providerName), "openrouter") {
		return "anthropic/" + personaModelSonnet
	}
	return personaModelSonnet
}

func (r *Runtime) faceModelName() string {
	if r.faceBackend == face.BackendFloorFallback {
		return r.governorModelName()
	}
	snapshot := r.currentRecipeSnapshot()
	return faceModelForProvider(r.faceProviderName(), snapshot.PersonaModel)
}
