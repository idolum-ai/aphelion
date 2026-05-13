//go:build linux

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadAcceptsOpenAINativeProvider(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[governor]
backend = "native"
native_provider = "openai"

[providers.openai]
api_key = "sk-openai-test"
model = "gpt-5.5"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if cfg.Governor.NativeProvider != "openai" {
		t.Fatalf("governor.native_provider = %q, want openai", cfg.Governor.NativeProvider)
	}
	if cfg.Providers.OpenAI.Model != "gpt-5.5" {
		t.Fatalf("providers.openai.model = %q, want gpt-5.5", cfg.Providers.OpenAI.Model)
	}
}

func TestLoadAcceptsGeminiAndOllamaNativeProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		raw        string
		wantModel  string
		wantAPIKey string
	}{
		{
			name: "gemini",
			raw: `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[governor]
backend = "native"
native_provider = "gemini"

[providers.gemini]
api_key = "gemini-test"
model = "gemini-test-model"
`,
			wantModel:  "gemini-test-model",
			wantAPIKey: "gemini-test",
		},
		{
			name: "ollama",
			raw: `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[governor]
backend = "native"
native_provider = "ollama"

[providers.ollama]
base_url = "http://ollama.test:11434"
model = "llama-test"
`,
			wantModel: "llama-test",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(configPath, []byte(tt.raw), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			cfg, err := Load(configPath)
			if err != nil {
				t.Fatalf("Load() err = %v", err)
			}
			if cfg.Governor.NativeProvider != tt.name {
				t.Fatalf("governor.native_provider = %q, want %s", cfg.Governor.NativeProvider, tt.name)
			}
			switch tt.name {
			case "gemini":
				if cfg.Providers.Gemini.Model != tt.wantModel || cfg.Providers.Gemini.APIKey != tt.wantAPIKey {
					t.Fatalf("providers.gemini = %#v", cfg.Providers.Gemini)
				}
			case "ollama":
				if cfg.Providers.Ollama.Model != tt.wantModel || cfg.Providers.Ollama.BaseURL != "http://ollama.test:11434" {
					t.Fatalf("providers.ollama = %#v", cfg.Providers.Ollama)
				}
			}
		})
	}
}

func TestLoadExampleStyleAutoSelectsOpenAIFirstWithConfiguredFallbacks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers]
selection = "auto"
auto_order = ["openai", "anthropic", "openrouter"]
default = ""
# fallback_chain intentionally omitted, matching config.example.toml auto mode.

[providers.openai]
api_key = "sk-openai-test"

[providers.anthropic]
api_key = "sk-ant-test"

[providers.openrouter]
api_key = "sk-or-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if cfg.Governor.NativeProvider != "openai" || cfg.Providers.Default != "openai" {
		t.Fatalf("provider heuristic = governor:%q default:%q, want openai/openai", cfg.Governor.NativeProvider, cfg.Providers.Default)
	}
	if !reflect.DeepEqual(cfg.Providers.FallbackChain, []string{"anthropic", "openrouter"}) {
		t.Fatalf("providers.fallback_chain = %#v, want anthropic -> openrouter", cfg.Providers.FallbackChain)
	}
	if cfg.Providers.OpenAI.Model != "gpt-5.5" {
		t.Fatalf("providers.openai.model = %q, want gpt-5.5", cfg.Providers.OpenAI.Model)
	}
	if !reflect.DeepEqual(cfg.Providers.OpenAI.FallbackModels, []string{"gpt-5.4", "gpt-5.4-mini"}) {
		t.Fatalf("providers.openai.fallback_models = %#v, want gpt-5.4/gpt-5.4-mini", cfg.Providers.OpenAI.FallbackModels)
	}
}

func TestLoadProviderManualSelectionPreservesConfiguredChain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers]
selection = "manual"
default = "anthropic"
fallback_chain = []

[providers.openai]
api_key = "sk-openai-test"

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if cfg.Governor.NativeProvider != "anthropic" || cfg.Providers.Default != "anthropic" {
		t.Fatalf("provider selection = governor:%q default:%q, want anthropic/anthropic", cfg.Governor.NativeProvider, cfg.Providers.Default)
	}
	if len(cfg.Providers.FallbackChain) != 0 {
		t.Fatalf("providers.fallback_chain = %#v, want explicit empty", cfg.Providers.FallbackChain)
	}
}

func TestLoadAllowsCodexFloorFallbackWithoutAnthropicKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[governor]
backend = "codex"

[face]
backend = "floor_fallback"

[agent]
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err != nil {
		t.Fatalf("Load() err = %v, want codex passthrough config to validate without Anthropic key", err)
	}
}
