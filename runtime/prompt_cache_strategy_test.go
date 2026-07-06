//go:build linux

package runtime

import (
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
)

func TestPromptCacheStrategyForProviderConfigHonorsProviderMechanics(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Providers.Anthropic.CacheStrategy = "explicit"

	tests := []struct {
		name     string
		provider string
		baseURL  string
		want     string
	}{
		{
			name:     "anthropic uses configured explicit cache controls",
			provider: core.ModelProviderAnthropic,
			want:     "explicit",
		},
		{
			name:     "openai official endpoint uses automatic prefix cache shaping",
			provider: core.ModelProviderOpenAI,
			baseURL:  "https://api.openai.com/v1",
			want:     providerPrefixCacheAwareStrategy,
		},
		{
			name:     "openai default endpoint uses automatic prefix cache shaping",
			provider: core.ModelProviderOpenAI,
			want:     providerPrefixCacheAwareStrategy,
		},
		{
			name:     "openai compatible endpoints stay conservative",
			provider: core.ModelProviderOpenAI,
			baseURL:  "https://example.invalid/v1",
			want:     "",
		},
		{
			name:     "other providers do not opt into cache-aware shaping",
			provider: core.ModelProviderOpenRouter,
			want:     "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := cfg
			cfg.Providers.OpenAI.BaseURL = tt.baseURL
			got := promptCacheStrategyForProviderConfig(&cfg, tt.provider)
			if got != tt.want {
				t.Fatalf("promptCacheStrategyForProviderConfig(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestRuntimePromptCacheStrategyForExecutionNormalizesProviderAliases(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Providers.Anthropic.CacheStrategy = "hybrid"
	rt := &Runtime{cfg: &cfg}

	if got := rt.promptCacheStrategyForExecution(pipeline.TurnExecutionContract{ProviderName: "oai"}); got != providerPrefixCacheAwareStrategy {
		t.Fatalf("OpenAI alias strategy = %q, want %q", got, providerPrefixCacheAwareStrategy)
	}
	if got := rt.promptCacheStrategyForExecution(pipeline.TurnExecutionContract{ProviderName: "claude"}); got != "hybrid" {
		t.Fatalf("Anthropic alias strategy = %q, want hybrid", got)
	}
}
