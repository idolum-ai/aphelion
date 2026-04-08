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
	if cfg.Providers.Anthropic.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q", cfg.Providers.Anthropic.Model)
	}
	if !strings.HasSuffix(cfg.Agent.Workspace, "/workspace") {
		t.Fatalf("workspace = %q, want expanded relative path", cfg.Agent.Workspace)
	}
}

func TestLoadRejectsMissingSecrets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = ""

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

func TestStripCommentPreservesQuotedHashes(t *testing.T) {
	t.Parallel()

	got := stripComment(`bot_token = "abc#123" # comment`)
	if want := `bot_token = "abc#123" `; got != want {
		t.Fatalf("stripComment() = %q, want %q", got, want)
	}
}
