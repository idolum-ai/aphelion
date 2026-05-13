//go:build linux

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if !strings.Contains(err.Error(), "add [principals.telegram] admin_user_ids") {
		t.Fatalf("error = %v, want actionable principal bootstrap hint", err)
	}
}

func TestLoadRejectsApprovedUserPrincipals(t *testing.T) {
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
		t.Fatal("Load() err = nil, want approved-user deprecation error")
	}
	if !strings.Contains(err.Error(), "approved_user_ids is not supported") {
		t.Fatalf("error = %v, want approved-user deprecation message", err)
	}
}

func TestLoadRejectsMultipleAdminPrincipals(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123, 456]

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want single-admin validation error")
	}
	if !strings.Contains(err.Error(), "must contain exactly one user id") {
		t.Fatalf("error = %v, want single-admin validation message", err)
	}
}

func TestResolveConfigPathPrefersPrimaryThenEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APHELION_CONFIG", "")

	primary := filepath.Join(home, ".aphelion", "aphelion.toml")
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err != nil {
		t.Fatalf("mkdir primary dir: %v", err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath() err = %v", err)
	}
	if got != primary {
		t.Fatalf("config path = %q, want primary default %q when unset", got, primary)
	}

	if err := os.WriteFile(primary, []byte("primary"), 0o600); err != nil {
		t.Fatalf("write primary config: %v", err)
	}
	got, err = ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath() err = %v", err)
	}
	if got != primary {
		t.Fatalf("config path = %q, want primary %q", got, primary)
	}

	custom := filepath.Join(home, "custom.toml")
	t.Setenv("APHELION_CONFIG", custom)
	got, err = ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath() err = %v", err)
	}
	if got != custom {
		t.Fatalf("config path = %q, want env override %q", got, custom)
	}
}

func TestLoadRejectsRemovedWorkspaceAlias(t *testing.T) {
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
db_path = "./state/sessions.db"

[agent]
workspace = "./workspace"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want removed agent.workspace rejection")
	}
	if !strings.Contains(err.Error(), "agent.workspace has been removed") {
		t.Fatalf("Load() err = %v, want removed agent.workspace rejection", err)
	}
}

func TestLoadTelegramChildBotConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[[telegram.child_bots]]
agent_id = "sample-child"
token_file = "./secrets/sample-child-token"
chat_id = -1001234567890
respond_on = "mentions"
enabled = true

[principals.telegram]
admin_user_ids = [123]

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
	if len(cfg.Telegram.ChildBots) != 1 {
		t.Fatalf("child bots = %d, want 1", len(cfg.Telegram.ChildBots))
	}
	bot := cfg.Telegram.ChildBots[0]
	if bot.AgentID != "sample-child" || bot.ChatID != -1001234567890 || bot.RespondOn != "mentions" || !bot.Enabled {
		t.Fatalf("child bot = %#v, want normalized sample child route", bot)
	}
	wantTokenFile := filepath.Join(dir, "secrets", "sample-child-token")
	if bot.TokenFile != wantTokenFile {
		t.Fatalf("token file = %q, want %q", bot.TokenFile, wantTokenFile)
	}
}

func TestLoadTelegramChildBotRejectsUnsafeOrDuplicateConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "missing token file",
			body: `[[telegram.child_bots]]
agent_id = "sample-child"
chat_id = -1001234567890
`,
			wantErr: "telegram.child_bots[0].token_file is required",
		},
		{
			name: "invalid respond_on",
			body: `[[telegram.child_bots]]
agent_id = "sample-child"
token_file = "./token"
chat_id = -1001234567890
respond_on = "sometimes"
`,
			wantErr: "telegram.child_bots[0].respond_on must be one of all|mentions",
		},
		{
			name: "duplicate chat",
			body: `[[telegram.child_bots]]
agent_id = "sample-child"
token_file = "./token-a"
chat_id = -1001234567890

[[telegram.child_bots]]
agent_id = "sample-child-2"
token_file = "./token-b"
chat_id = -1001234567890
`,
			wantErr: "telegram.child_bots[1].chat_id duplicates child bot \"sample-child\"",
		},
		{
			name: "duplicate durable group chat",
			body: `[[telegram.durable_groups]]
agent_id = "main-group"
charter = "Help in the group."
chat_id = -1001234567890
llm_backend = "codex"
llm_codex_home = "./codex-home"

[[telegram.child_bots]]
agent_id = "sample-child"
token_file = "./token"
chat_id = -1001234567890
`,
			wantErr: "telegram.child_bots[0].chat_id duplicates telegram.durable_groups route \"main-group\"",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.toml")
			raw := `[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"

` + tc.body
			if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := Load(configPath)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}
