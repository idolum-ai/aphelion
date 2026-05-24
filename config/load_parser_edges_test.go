//go:build linux

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadIgnoresUnknownKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers]
default = "anthropic"
failover = ["gemini", "openai"]

[providers.anthropic]
api_key = "sk-ant-test"

[logging]
level = "debug"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if cfg.Providers.Default != "anthropic" {
		t.Fatalf("providers.default = %q, want anthropic", cfg.Providers.Default)
	}
}

func TestLoadRejectsRemovedRecoveryWatchdogRestartFieldsWithoutCompatibilityAlias(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[recovery.watchdog]
restart_cooldown = "30m"
max_restart_attempts = 1
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want hard rejection for removed watchdog restart fields")
	}
	for _, want := range []string{
		"recovery.watchdog.restart_cooldown has been removed",
		"stale turn recovery now interrupts scoped turns instead of restarting the service",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Load() err = %v, want %q", err, want)
		}
	}
}

func TestLoadWebSearchConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"

[agent]
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"

[tools.web_search]
enabled = true
provider_order = ["brave", "openai_hosted"]
default_count = 3
max_count = 7
timeout = "5s"
cache_ttl = "1m"

[tools.web_search.openai_hosted]
enabled = true
context_size = "high"

[tools.web_search.brave]
enabled = true
api_key_env = "BRAVE_UNIT_KEY"
api_key_file = "./brave.key"
endpoint = "https://search.example.test/res/v1/web/search"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !cfg.Tools.WebSearch.Enabled || cfg.Tools.WebSearch.DefaultCount != 3 || cfg.Tools.WebSearch.MaxCount != 7 || cfg.Tools.WebSearch.OpenAIHosted.ContextSize != "high" {
		t.Fatalf("web_search = %#v", cfg.Tools.WebSearch)
	}
	if !reflect.DeepEqual(cfg.Tools.WebSearch.ProviderOrder, []string{"brave", "openai_hosted"}) {
		t.Fatalf("provider_order = %#v", cfg.Tools.WebSearch.ProviderOrder)
	}
	if !strings.HasSuffix(cfg.Tools.WebSearch.Brave.APIKeyFile, "/brave.key") {
		t.Fatalf("api_key_file = %q, want expanded", cfg.Tools.WebSearch.Brave.APIKeyFile)
	}
}
