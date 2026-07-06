//go:build linux

package runtime

import (
	"net/url"
	"strings"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
)

const providerPrefixCacheAwareStrategy = "hybrid"

func promptCacheStrategyForProviderConfig(cfg *config.Config, providerName string) string {
	if cfg == nil {
		return ""
	}
	switch core.NormalizeModelProvider(providerName) {
	case core.ModelProviderAnthropic:
		return cfg.Providers.Anthropic.CacheStrategy
	case core.ModelProviderOpenAI:
		if openAIAutomaticPrefixCacheApplies(cfg.Providers.OpenAI.BaseURL) {
			// OpenAI prompt caching is automatic for long exact-prefix prompts.
			// The prompt package uses "hybrid" as the cache-aware layout strategy;
			// this is not an OpenAI transport cache-control parameter.
			return providerPrefixCacheAwareStrategy
		}
	}
	return ""
}

func openAIAutomaticPrefixCacheApplies(baseURL string) bool {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return true
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "api.openai.com")
}
