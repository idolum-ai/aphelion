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

func TestValidateModelSlotConfigAcceptsOpenAIFastServiceTier(t *testing.T) {
	t.Parallel()

	got := ValidateModelSlotConfig(ModelSlotConfig{
		Slot:        "governor",
		Provider:    "openai",
		Model:       "gpt-5.5",
		Effort:      "high",
		ServiceTier: "fast",
	}, true)

	if !got.Valid {
		t.Fatalf("Valid = false: %s", got.Error)
	}
	if got.Config.ServiceTier != ModelServiceTierPriority {
		t.Fatalf("service tier = %q, want priority", got.Config.ServiceTier)
	}
}

func TestValidateModelSlotConfigRejectsFastForNonOpenAI(t *testing.T) {
	t.Parallel()

	got := ValidateModelSlotConfig(ModelSlotConfig{
		Slot:        "governor",
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-6",
		ServiceTier: "fast",
	}, true)

	if got.Valid {
		t.Fatal("Valid = true, want rejected fast mode")
	}
	if !strings.Contains(got.Error, "only available for openai") {
		t.Fatalf("error = %q, want openai-only guidance", got.Error)
	}
}

func TestValidateModelSlotConfigRejectsUnknownSpeed(t *testing.T) {
	t.Parallel()

	got := ValidateModelSlotConfig(ModelSlotConfig{
		Slot:        "governor",
		Provider:    "openai",
		Model:       "gpt-5.5",
		ServiceTier: "turbo",
	}, true)

	if got.Valid {
		t.Fatal("Valid = true, want unknown speed rejected")
	}
	if !strings.Contains(got.Error, "standard or fast") {
		t.Fatalf("error = %q, want speed guidance", got.Error)
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

	provider, model = ParseProviderModel("gemini/gemini-3.1-pro")
	if provider != ModelProviderGemini || model != "gemini-3.1-pro" {
		t.Fatalf("ParseProviderModel(gemini) = (%q, %q)", provider, model)
	}
}

func TestValidateModelSlotConfigAcceptsGeminiAndOllama(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider string
		model    string
		want     string
	}{
		{provider: "gemini", model: "gemini-3.1-pro", want: ModelTransportGeminiGenerate},
		{provider: "ollama", model: "llama3.2", want: ModelTransportOllamaChat},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.provider, func(t *testing.T) {
			t.Parallel()
			got := ValidateModelSlotConfig(ModelSlotConfig{
				Slot:     "doctor",
				Provider: tt.provider,
				Model:    tt.model,
			}, true)
			if !got.Valid {
				t.Fatalf("Valid = false: %s", got.Error)
			}
			if got.ResolvedTransport != tt.want {
				t.Fatalf("resolved transport = %q, want %q", got.ResolvedTransport, tt.want)
			}
		})
	}
}
