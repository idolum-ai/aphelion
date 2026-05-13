//go:build linux

package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/openai"
	"github.com/idolum-ai/aphelion/provider"
)

func buildNativeProviderChain(cfg *config.Config, httpClient *http.Client) (agent.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}

	names := orderedNativeProviderNames(cfg)
	entries := make([]provider.NamedProvider, 0, len(names)+len(cfg.Providers.OpenAI.FallbackModels))
	required := nativeProviderRequired(cfg)
	for idx, name := range names {
		if !isConfiguredProvider(name, cfg) {
			if idx == 0 && required {
				return nil, fmt.Errorf("native provider %q is enabled but not configured", name)
			}
			continue
		}
		built, err := buildNamedProviderEntries(name, cfg, httpClient)
		if err != nil {
			return nil, err
		}
		entries = append(entries, built...)
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) == 1 {
		return entries[0].Provider, nil
	}
	return provider.NewFailoverChain(entries)
}

func buildNamedProviderEntries(name string, cfg *config.Config, httpClient *http.Client) ([]provider.NamedProvider, error) {
	if strings.EqualFold(strings.TrimSpace(name), "openai") {
		models := openAIModelChain(cfg.Providers.OpenAI)
		entries := make([]provider.NamedProvider, 0, len(models))
		for _, model := range models {
			p, err := provider.NewOpenAI(provider.OpenAIOptions{
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
			entries = append(entries, provider.NamedProvider{Name: "openai:" + model, Provider: p})
		}
		return entries, nil
	}
	p, err := buildNamedProvider(name, cfg, httpClient)
	if err != nil || p == nil {
		return nil, err
	}
	return []provider.NamedProvider{{Name: strings.ToLower(strings.TrimSpace(name)), Provider: p}}, nil
}

func openAIModelChain(cfg config.OpenAIProviderConfig) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 1+len(cfg.FallbackModels))
	for _, raw := range append([]string{cfg.Model}, cfg.FallbackModels...) {
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

func buildOpenAIPlatformServices(cfg *config.Config, httpClient *http.Client) (memory.FileStore, memory.RetrievalStore, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("config is nil")
	}
	if !cfg.OpenAI.Files.Enabled && !cfg.OpenAI.VectorStores.Enabled {
		return nil, nil, nil
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}

	client, err := openai.NewClient(openai.ClientOptions{
		APIKey:     cfg.Providers.OpenAI.APIKey,
		BaseURL:    cfg.Providers.OpenAI.BaseURL,
		HTTPClient: httpClient,
		UserAgent:  config.EffectiveUserAgent(cfg, ""),
	})
	if err != nil {
		return nil, nil, err
	}

	var fileStore memory.FileStore
	if cfg.OpenAI.Files.Enabled {
		files, err := openai.NewFilesClient(client)
		if err != nil {
			return nil, nil, err
		}
		fileStore = files
	}

	var retrievalStore memory.RetrievalStore
	if cfg.OpenAI.VectorStores.Enabled {
		vectorStores, err := openai.NewVectorStoresClient(client)
		if err != nil {
			return nil, nil, err
		}
		retrievalStore = vectorStores
	}

	return fileStore, retrievalStore, nil
}

func buildNamedProvider(name string, cfg *config.Config, httpClient *http.Client) (agent.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		return provider.NewAnthropic(provider.AnthropicOptions{
			APIKey:        cfg.Providers.Anthropic.APIKey,
			Model:         cfg.Providers.Anthropic.Model,
			MaxTokens:     cfg.Providers.Anthropic.MaxTokens,
			CacheStrategy: cfg.Providers.Anthropic.CacheStrategy,
			CacheTTL:      cfg.Providers.Anthropic.CacheTTL,
			HTTPClient:    httpClient,
			UserAgent:     config.EffectiveUserAgent(cfg, ""),
		})
	case "openai":
		return provider.NewOpenAI(provider.OpenAIOptions{
			APIKey:     cfg.Providers.OpenAI.APIKey,
			BaseURL:    cfg.Providers.OpenAI.BaseURL,
			Model:      cfg.Providers.OpenAI.Model,
			MaxTokens:  cfg.Providers.OpenAI.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "openrouter":
		return provider.NewOpenRouter(provider.OpenRouterOptions{
			APIKey:     cfg.Providers.OpenRouter.APIKey,
			BaseURL:    cfg.Providers.OpenRouter.BaseURL,
			Model:      cfg.Providers.OpenRouter.Model,
			MaxTokens:  cfg.Providers.OpenRouter.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "gemini":
		return provider.NewGemini(provider.GeminiOptions{
			APIKey:     cfg.Providers.Gemini.APIKey,
			BaseURL:    cfg.Providers.Gemini.BaseURL,
			Model:      cfg.Providers.Gemini.Model,
			MaxTokens:  cfg.Providers.Gemini.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "ollama":
		return provider.NewOllama(provider.OllamaOptions{
			BaseURL:    cfg.Providers.Ollama.BaseURL,
			Model:      cfg.Providers.Ollama.Model,
			MaxTokens:  cfg.Providers.Ollama.MaxTokens,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", name)
	}
}

func orderedNativeProviderNames(cfg *config.Config) []string {
	return config.EffectiveProviderChain(cfg)
}

func resolveNativeProviderName(cfg *config.Config) string {
	return config.EffectiveNativeProvider(cfg)
}

func activeNativeModel(cfg *config.Config) string {
	switch resolveNativeProviderName(cfg) {
	case "openai":
		return cfg.Providers.OpenAI.Model
	case "openrouter":
		return cfg.Providers.OpenRouter.Model
	case "gemini":
		return cfg.Providers.Gemini.Model
	case "ollama":
		return cfg.Providers.Ollama.Model
	case "anthropic":
		return cfg.Providers.Anthropic.Model
	default:
		return ""
	}
}

func nativeProviderRequired(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	governorBackend := strings.ToLower(strings.TrimSpace(cfg.Governor.Backend))
	faceBackend := config.NormalizeFaceBackendValue(cfg.Face.Backend)
	return governorBackend == "native" || faceBackend == "" || faceBackend == "provider"
}

func isConfiguredProvider(name string, cfg *config.Config) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
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
