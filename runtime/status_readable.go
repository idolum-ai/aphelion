//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	providerpkg "github.com/idolum-ai/aphelion/provider"
)

const (
	statusReadableModelAnthropic  = "claude-haiku-4-5"
	statusReadableModelOpenAI     = "gpt-5.4"
	statusReadableModelOpenRouter = "anthropic/claude-haiku-4-5"
	statusReadableModelGemini     = "gemini-3.1-flash"
	statusReadableInputMaxChars   = 2600
	statusReadableOutputMaxChars  = 320
	statusReadableSummaryTimeout  = 4 * time.Second
)

var (
	newStatusReadableAnthropicProvider = func(opts providerpkg.AnthropicOptions) (agent.Provider, error) {
		return providerpkg.NewAnthropic(opts)
	}
	newStatusReadableOpenAIProvider = func(opts providerpkg.OpenAIOptions) (agent.Provider, error) {
		return providerpkg.NewOpenAI(opts)
	}
	newStatusReadableOpenRouterProvider = func(opts providerpkg.OpenRouterOptions) (agent.Provider, error) {
		return providerpkg.NewOpenRouter(opts)
	}
	newStatusReadableGeminiProvider = func(opts providerpkg.GeminiOptions) (agent.Provider, error) {
		return providerpkg.NewGemini(opts)
	}
	newStatusReadableOllamaProvider = func(opts providerpkg.OllamaOptions) (agent.Provider, error) {
		return providerpkg.NewOllama(opts)
	}
	newStatusReadableFailoverChain = func(entries []providerpkg.NamedProvider) (agent.Provider, error) {
		return providerpkg.NewFailoverChain(entries)
	}
)

func (r *Runtime) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	if r == nil {
		return ""
	}
	statusText = strings.TrimSpace(statusText)
	if statusText == "" {
		return ""
	}

	provider := r.statusReadableSummaryProvider()
	if provider == nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	summaryCtx, cancel := context.WithTimeout(ctx, statusReadableSummaryTimeout)
	defer cancel()

	messages := statusReadableSummaryMessages(view, statusText)
	resp, err := completeProvider(summaryCtx, provider, messages, nil, &agent.CompleteOptions{
		Reasoning: agent.ReasoningConfig{
			Effort:  agent.ReasoningEffortLow,
			Summary: agent.ReasoningSummaryCompact,
		},
		Verbosity: agent.VerbosityLow,
	})
	if err != nil {
		log.Printf("WARN status readable summary failed view=%s err=%v", strings.TrimSpace(view), err)
		return ""
	}
	return normalizeStatusReadableSummary(resp.Content)
}

func (r *Runtime) statusReadableSummaryProvider() agent.Provider {
	if r == nil {
		return nil
	}
	r.statusReadableMu.Lock()
	defer r.statusReadableMu.Unlock()
	if r.statusReadableReady {
		return r.statusReadableProvider
	}
	provider, err := buildStatusReadableProviderChain(r.cfg)
	if err != nil {
		log.Printf("WARN status readable summary provider disabled err=%v", err)
	}
	r.statusReadableProvider = provider
	r.statusReadableReady = true
	return r.statusReadableProvider
}

func buildStatusReadableProviderChain(cfg *config.Config) (agent.Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	httpClient := &http.Client{Timeout: 15 * time.Second}
	names := orderedFaceProviderNames(cfg)
	entries := make([]providerpkg.NamedProvider, 0, len(names))
	for _, name := range names {
		provider, err := buildNamedStatusReadableProvider(name, cfg, httpClient)
		if err != nil {
			return nil, err
		}
		if provider == nil {
			continue
		}
		entries = append(entries, providerpkg.NamedProvider{Name: name, Provider: provider})
	}
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) == 1 {
		return entries[0].Provider, nil
	}
	return newStatusReadableFailoverChain(entries)
}

func buildNamedStatusReadableProvider(name string, cfg *config.Config, httpClient *http.Client) (agent.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic":
		if strings.TrimSpace(cfg.Providers.Anthropic.APIKey) == "" {
			return nil, nil
		}
		return newStatusReadableAnthropicProvider(providerpkg.AnthropicOptions{
			APIKey:        cfg.Providers.Anthropic.APIKey,
			Model:         statusReadableModelAnthropic,
			MaxTokens:     512,
			CacheStrategy: cfg.Providers.Anthropic.CacheStrategy,
			CacheTTL:      cfg.Providers.Anthropic.CacheTTL,
			HTTPClient:    httpClient,
			UserAgent:     config.EffectiveUserAgent(cfg, ""),
		})
	case "openai":
		if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
			return nil, nil
		}
		model := strings.TrimSpace(cfg.Providers.OpenAI.Model)
		if model == "" {
			model = statusReadableModelOpenAI
		}
		return newStatusReadableOpenAIProvider(providerpkg.OpenAIOptions{
			APIKey:     cfg.Providers.OpenAI.APIKey,
			BaseURL:    cfg.Providers.OpenAI.BaseURL,
			Model:      model,
			MaxTokens:  512,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "openrouter":
		if strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) == "" {
			return nil, nil
		}
		return newStatusReadableOpenRouterProvider(providerpkg.OpenRouterOptions{
			APIKey:     cfg.Providers.OpenRouter.APIKey,
			BaseURL:    cfg.Providers.OpenRouter.BaseURL,
			Model:      statusReadableModelOpenRouter,
			MaxTokens:  512,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "gemini":
		if strings.TrimSpace(cfg.Providers.Gemini.APIKey) == "" {
			return nil, nil
		}
		model := strings.TrimSpace(cfg.Providers.Gemini.Model)
		if model == "" {
			model = statusReadableModelGemini
		}
		return newStatusReadableGeminiProvider(providerpkg.GeminiOptions{
			APIKey:     cfg.Providers.Gemini.APIKey,
			BaseURL:    cfg.Providers.Gemini.BaseURL,
			Model:      model,
			MaxTokens:  512,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	case "ollama":
		model := strings.TrimSpace(cfg.Providers.Ollama.Model)
		if strings.TrimSpace(cfg.Providers.Ollama.BaseURL) == "" || model == "" {
			return nil, nil
		}
		return newStatusReadableOllamaProvider(providerpkg.OllamaOptions{
			BaseURL:    cfg.Providers.Ollama.BaseURL,
			Model:      model,
			MaxTokens:  512,
			HTTPClient: httpClient,
			UserAgent:  config.EffectiveUserAgent(cfg, ""),
		})
	default:
		return nil, nil
	}
}

func statusReadableSummaryMessages(view string, statusText string) []agent.Message {
	normalizedView := strings.TrimSpace(strings.ToLower(view))
	if normalizedView == "" {
		normalizedView = "chat"
	}
	return []agent.Message{
		{
			Role: "system",
			Content: strings.Join([]string{
				"You summarize Telegram status diagnostics for an operator.",
				"Return exactly one plain-text paragraph under 320 characters.",
				"Describe current state first: needs recovery, blocked, working, queued, failed, interrupted, or idle when present.",
				"Then mention the most important action item. Treat backlog items as non-urgent backlog, not pending operator action.",
				"Do not invent details. Do not use markdown or bullet points.",
			}, "\n"),
		},
		{
			Role: "user",
			Content: fmt.Sprintf(
				"status_view=%s\n\nraw_status:\n%s",
				normalizedView,
				clampText(statusText, statusReadableInputMaxChars),
			),
		},
	}
}

func normalizeStatusReadableSummary(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "`")
	if raw == "" {
		return ""
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.Join(strings.Fields(raw), " ")
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "summary:"))
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "Summary:"))
	if raw == "" {
		return ""
	}
	return sentenceAwareSummary(raw, statusReadableOutputMaxChars)
}
