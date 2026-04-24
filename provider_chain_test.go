//go:build linux

package main

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	providerpkg "github.com/idolum-ai/aphelion/provider"
)

func TestBuildNativeProviderChainExpandsOpenAIModelFallbacks(t *testing.T) {
	cfg := config.Default()
	cfg.Governor.NativeProvider = "openai"
	cfg.Providers.Default = "openai"
	cfg.Providers.FallbackChain = []string{"anthropic", "openrouter"}
	cfg.Providers.OpenAI.APIKey = "sk-openai-test"
	cfg.Providers.OpenAI.Model = "gpt-5.5"
	cfg.Providers.OpenAI.FallbackModels = []string{"gpt-5.4", "gpt-5.4-mini"}
	cfg.Providers.Anthropic.APIKey = "sk-ant-test"
	cfg.Providers.OpenRouter.APIKey = "sk-or-test"

	built, err := buildNativeProviderChain(&cfg, &http.Client{})
	if err != nil {
		t.Fatalf("buildNativeProviderChain() err = %v", err)
	}
	chain, ok := built.(*providerpkg.FailoverChain)
	if !ok {
		t.Fatalf("provider = %T, want failover chain", built)
	}
	state := chain.RuntimeState()
	want := []string{"openai:gpt-5.5", "openai:gpt-5.4", "openai:gpt-5.4-mini", "anthropic", "openrouter"}
	if !reflect.DeepEqual(state.ConfiguredChain, want) {
		t.Fatalf("configured chain = %#v, want %#v", state.ConfiguredChain, want)
	}
}
