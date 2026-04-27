//go:build linux

package core

import (
	"strings"
	"testing"
)

func TestValidateModelSlotConfigRoutesOpenAIGPT5ToolsWithEffortToResponses(t *testing.T) {
	t.Parallel()

	got := ValidateModelSlotConfig(ModelSlotConfig{
		Slot:      "governor",
		Provider:  "openai",
		Model:     "gpt-5.5",
		Effort:    "max",
		Transport: "auto",
	}, true)

	if !got.Valid {
		t.Fatalf("Valid = false: %s", got.Error)
	}
	if got.Config.Effort != "xhigh" {
		t.Fatalf("effort = %q, want xhigh", got.Config.Effort)
	}
	if got.ResolvedTransport != ModelTransportOpenAIResponses {
		t.Fatalf("resolved transport = %q, want responses", got.ResolvedTransport)
	}
}

func TestValidateModelSlotConfigRejectsOpenAIGPT5ToolsEffortOnChatCompletions(t *testing.T) {
	t.Parallel()

	got := ValidateModelSlotConfig(ModelSlotConfig{
		Slot:      "governor",
		Provider:  "openai",
		Model:     "gpt-5.5",
		Effort:    "high",
		Transport: "chat_completions",
	}, true)

	if got.Valid {
		t.Fatal("Valid = true, want rejected transport")
	}
	if !strings.Contains(got.Error, "requires responses") {
		t.Fatalf("error = %q, want responses guidance", got.Error)
	}
}

func TestParseProviderModel(t *testing.T) {
	t.Parallel()

	provider, model := ParseProviderModel("anthropic/claude-opus-4.7")
	if provider != ModelProviderAnthropic || model != "claude-opus-4.7" {
		t.Fatalf("ParseProviderModel() = (%q, %q), want anthropic/claude-opus-4.7", provider, model)
	}

	provider, model = ParseProviderModel("openrouter/anthropic/claude-opus-4.7")
	if provider != ModelProviderOpenRouter || model != "anthropic/claude-opus-4.7" {
		t.Fatalf("ParseProviderModel(openrouter) = (%q, %q)", provider, model)
	}
}
