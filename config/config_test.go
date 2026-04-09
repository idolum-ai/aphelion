//go:build linux

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMinimalConfig(t *testing.T) {
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
workspace = "./workspace"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if cfg.Telegram.PollTimeout != 30 {
		t.Fatalf("poll timeout = %d, want 30", cfg.Telegram.PollTimeout)
	}
	if cfg.Governor.Backend != "auto" {
		t.Fatalf("governor.backend = %q, want auto", cfg.Governor.Backend)
	}
	if cfg.Providers.Anthropic.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q", cfg.Providers.Anthropic.Model)
	}
	if cfg.Sessions.IdleExpiry != "24h" {
		t.Fatalf("idle_expiry = %q, want 24h", cfg.Sessions.IdleExpiry)
	}
	if !strings.HasSuffix(cfg.Agent.Workspace, "/workspace") {
		t.Fatalf("workspace = %q, want expanded relative path", cfg.Agent.Workspace)
	}
	if len(cfg.Agent.BootstrapFiles) == 0 || cfg.Agent.BootstrapFiles[0] != "SOUL.md" {
		t.Fatalf("bootstrap files = %#v, want defaults", cfg.Agent.BootstrapFiles)
	}
	if !cfg.Agent.DailyNotes {
		t.Fatal("daily notes should default to enabled")
	}
}

func TestLoadParsesMultilineArrays(t *testing.T) {
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
workspace = "./workspace"
bootstrap_files = [
  "AGENTS.md",
  "SOUL.md",
]
dynamic_files = ["MEMORY.md", "HEARTBEAT.md", "memory/2026-04-08.md"]
bootstrap_max_chars = 123
bootstrap_total_max_chars = 456
daily_notes = false
daily_notes_dir = "notes"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if got, want := cfg.Agent.BootstrapFiles, []string{"AGENTS.md", "SOUL.md"}; !equalStrings(got, want) {
		t.Fatalf("bootstrap_files = %#v, want %#v", got, want)
	}
	if got, want := cfg.Agent.DynamicFiles, []string{"MEMORY.md", "HEARTBEAT.md", "memory/2026-04-08.md"}; !equalStrings(got, want) {
		t.Fatalf("dynamic_files = %#v, want %#v", got, want)
	}
	if cfg.Agent.BootstrapMaxChars != 123 || cfg.Agent.BootstrapTotalMaxChars != 456 {
		t.Fatalf("bootstrap caps = %d/%d, want 123/456", cfg.Agent.BootstrapMaxChars, cfg.Agent.BootstrapTotalMaxChars)
	}
	if cfg.Agent.DailyNotes {
		t.Fatal("daily notes should be disabled")
	}
	if cfg.Agent.DailyNotesDir != "notes" {
		t.Fatalf("daily_notes_dir = %q, want notes", cfg.Agent.DailyNotesDir)
	}
}

func TestLoadParsesBasicTypedFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"
poll_timeout = 11

[principals.telegram]
admin_user_ids = [123]
approved_user_ids = [456]

[governor]
backend = "native"
native_provider = "anthropic"

[governor.codex]
auth_source = "codex_cli"
codex_home = "~/codex-home"
base_url = "https://chatgpt.com/backend-api/codex"

[providers.anthropic]
api_key = "sk-ant-test"
model = "claude-opus-4-6"
max_tokens = 8192

[sessions]
db_path = "~/tmp/sessions.db"
idle_expiry = "36h"

[agent]
workspace = "~/workspace"
max_iterations = 77
tool_timeout = 9
bootstrap_files = ["AGENTS.md"]
dynamic_files = ["MEMORY.md", "HEARTBEAT.md"]
bootstrap_max_chars = 500
bootstrap_total_max_chars = 600
daily_notes = false
daily_notes_dir = "notes"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	if cfg.Telegram.PollTimeout != 11 {
		t.Fatalf("poll_timeout = %d, want 11", cfg.Telegram.PollTimeout)
	}
	if cfg.Providers.Anthropic.Model != "claude-opus-4-6" {
		t.Fatalf("model = %q, want claude-opus-4-6", cfg.Providers.Anthropic.Model)
	}
	if cfg.Providers.Anthropic.MaxTokens != 8192 {
		t.Fatalf("max_tokens = %d, want 8192", cfg.Providers.Anthropic.MaxTokens)
	}
	if cfg.Agent.MaxIterations != 77 || cfg.Agent.ToolTimeout != 9 {
		t.Fatalf("agent limits = %d/%d, want 77/9", cfg.Agent.MaxIterations, cfg.Agent.ToolTimeout)
	}
	if cfg.Governor.Backend != "native" {
		t.Fatalf("governor.backend = %q, want native", cfg.Governor.Backend)
	}
	if cfg.Governor.Codex.AuthSource != "codex_cli" {
		t.Fatalf("governor.codex.auth_source = %q, want codex_cli", cfg.Governor.Codex.AuthSource)
	}
	if cfg.Sessions.IdleExpiry != "36h" {
		t.Fatalf("idle_expiry = %q, want 36h", cfg.Sessions.IdleExpiry)
	}
	if cfg.Agent.DailyNotes {
		t.Fatal("daily_notes = true, want false")
	}
}

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

func TestLoadRejectsMissingSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = ""

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = ""
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want validation error")
	}
}

func TestLoadRejectsInvalidTOML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want parse error")
	}
}

func TestLoadRejectsInvalidIdleExpiry(t *testing.T) {
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

[sessions]
idle_expiry = "definitely-not-a-duration"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want idle_expiry validation error")
	}
}

func TestLoadRejectsInvalidGovernorBackend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[governor]
backend = "wild"

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want governor backend validation error")
	}
	if !strings.Contains(err.Error(), "governor.backend") {
		t.Fatalf("error = %v, want governor.backend message", err)
	}
}

func TestLoadRejectsInvalidGovernorCodexAuthSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[governor.codex]
auth_source = "mystery"

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want governor codex auth_source validation error")
	}
	if !strings.Contains(err.Error(), "governor.codex.auth_source") {
		t.Fatalf("error = %v, want governor.codex.auth_source message", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadRejectsMissingAdminPrincipal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want principal validation error")
	}
	if !strings.Contains(err.Error(), "principals.telegram.admin_user_ids") {
		t.Fatalf("error = %v, want principals.telegram.admin_user_ids message", err)
	}
}

func TestLoadRejectsPrincipalOverlap(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]
approved_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want overlap validation error")
	}
	if !strings.Contains(err.Error(), "both admin and approved_user") {
		t.Fatalf("error = %v, want overlap message", err)
	}
}
