//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	providerpkg "github.com/idolum-ai/aphelion/provider"
)

func TestBuildStatusReadableProviderChainUsesHaikuModels(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Providers.Anthropic.APIKey = "sk-ant-test"
	cfg.Providers.OpenRouter.APIKey = "sk-or-test"
	cfg.Providers.Default = "anthropic"
	cfg.Providers.FallbackChain = []string{"openrouter"}

	var (
		gotAnthropicModel  string
		gotOpenRouterModel string
		failoverEntries    int
	)
	origAnthropicFactory := newStatusReadableAnthropicProvider
	origOpenRouterFactory := newStatusReadableOpenRouterProvider
	origFailoverFactory := newStatusReadableFailoverChain
	defer func() {
		newStatusReadableAnthropicProvider = origAnthropicFactory
		newStatusReadableOpenRouterProvider = origOpenRouterFactory
		newStatusReadableFailoverChain = origFailoverFactory
	}()

	newStatusReadableAnthropicProvider = func(opts providerpkg.AnthropicOptions) (agent.Provider, error) {
		gotAnthropicModel = strings.TrimSpace(opts.Model)
		return &fakeProvider{replyText: "ok"}, nil
	}
	newStatusReadableOpenRouterProvider = func(opts providerpkg.OpenRouterOptions) (agent.Provider, error) {
		gotOpenRouterModel = strings.TrimSpace(opts.Model)
		return &fakeProvider{replyText: "ok"}, nil
	}
	newStatusReadableFailoverChain = func(entries []providerpkg.NamedProvider) (agent.Provider, error) {
		failoverEntries = len(entries)
		return &fakeProvider{replyText: "ok"}, nil
	}

	provider, err := buildStatusReadableProviderChain(&cfg)
	if err != nil {
		t.Fatalf("buildStatusReadableProviderChain() err = %v", err)
	}
	if provider == nil {
		t.Fatal("buildStatusReadableProviderChain() provider = nil, want non-nil")
	}
	if gotAnthropicModel != statusReadableModelAnthropic {
		t.Fatalf("anthropic status model = %q, want %q", gotAnthropicModel, statusReadableModelAnthropic)
	}
	if gotOpenRouterModel != statusReadableModelOpenRouter {
		t.Fatalf("openrouter status model = %q, want %q", gotOpenRouterModel, statusReadableModelOpenRouter)
	}
	if failoverEntries != 2 {
		t.Fatalf("failover entries = %d, want 2", failoverEntries)
	}
}

func TestStatusReadableSummaryUsesProviderAndNormalizesText(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{
		replyText: "Summary: \n\nBlocked waiting for your approval on one pending decision.",
	}
	rt := &Runtime{
		statusReadableProvider: provider,
		statusReadableReady:    true,
	}
	got := rt.StatusReadableSummary(context.Background(), "chat", "status_scope=chat\nsummary state=blocked")
	if !strings.Contains(got, "Blocked waiting for your approval") {
		t.Fatalf("StatusReadableSummary() = %q, want normalized provider summary", got)
	}
	if provider.callCount != 1 {
		t.Fatalf("provider call count = %d, want 1", provider.callCount)
	}
	provider.mu.Lock()
	gotVerbosity := provider.lastVerbosity
	provider.mu.Unlock()
	if gotVerbosity != agent.VerbosityLow {
		t.Fatalf("status readable verbosity = %q, want low", gotVerbosity)
	}
}

func TestStatusReadableSummaryReturnsEmptyWhenProviderUnavailable(t *testing.T) {
	t.Parallel()

	rt := &Runtime{
		statusReadableReady: true,
	}
	got := rt.StatusReadableSummary(context.Background(), "chat", "status_scope=chat\nsummary state=idle")
	if got != "" {
		t.Fatalf("StatusReadableSummary() = %q, want empty when provider unavailable", got)
	}
}
