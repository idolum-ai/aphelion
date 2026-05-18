//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsAutonomyDefaultAboveCeiling(t *testing.T) {
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

[autonomy]
default_mode = "mission"
ceiling = "ask_first"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want autonomy precedence validation error")
	}
	if !strings.Contains(err.Error(), "autonomy.default_mode must not exceed autonomy.ceiling") {
		t.Fatalf("Load() err = %v, want autonomy ceiling validation", err)
	}
}

func TestLoadRejectsLongLiveAutonomyOverrideDuration(t *testing.T) {
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

[autonomy]
default_mode = "ask_first"
ceiling = "leased"
allow_live_overrides = true
max_override_duration = "25h"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want live autonomy override duration validation error")
	}
	if !strings.Contains(err.Error(), "autonomy.max_override_duration must be <= 24h") {
		t.Fatalf("Load() err = %v, want autonomy max duration validation", err)
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

func TestLoadRejectsInvalidRecoveryWatchdogConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "invalid threshold",
			body: `stale_turn_threshold = "soon"
stale_turn_limit = 8`,
			wantErr: "recovery.watchdog.stale_turn_threshold",
		},
		{
			name: "invalid limit",
			body: `stale_turn_threshold = "3m"
stale_turn_limit = 0`,
			wantErr: "recovery.watchdog.stale_turn_limit",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
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

[recovery.watchdog]
` + tc.body + `
`
			if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			_, err := Load(configPath)
			if err == nil {
				t.Fatal("Load() err = nil, want watchdog validation error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadRejectsInvalidCompactionRatios(t *testing.T) {
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
max_context_ratio = 0.50
compaction_ratio = 0.60
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want compaction ratio validation error")
	}
	if !strings.Contains(err.Error(), "sessions.compaction_ratio") {
		t.Fatalf("error = %v, want sessions.compaction_ratio message", err)
	}
}

func TestLoadRejectsInvalidTESRetentionMaxAge(t *testing.T) {
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

[sessions.tes_retention]
max_age = "soon"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want tes retention max_age validation error")
	}
	if !strings.Contains(err.Error(), "sessions.tes_retention.max_age") {
		t.Fatalf("error = %v, want sessions.tes_retention.max_age message", err)
	}
}

func TestLoadRejectsTESRetentionDeleteBatchAboveFloor(t *testing.T) {
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

[sessions.tes_retention]
max_age = "168h"
min_retained_rows = 300
max_delete_per_gc = 301
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want tes retention max_delete_per_gc validation error")
	}
	if !strings.Contains(err.Error(), "sessions.tes_retention.max_delete_per_gc") {
		t.Fatalf("error = %v, want sessions.tes_retention.max_delete_per_gc message", err)
	}
}

func TestLoadRejectsEnabledTESRetentionWithoutExportDir(t *testing.T) {
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

[sessions.tes_retention]
enabled = true
max_age = "168h"
min_retained_rows = 300
max_delete_per_gc = 200
export_dir = ""
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want tes retention export_dir validation error")
	}
	if !strings.Contains(err.Error(), "sessions.tes_retention.export_dir") {
		t.Fatalf("error = %v, want sessions.tes_retention.export_dir message", err)
	}
}

func TestLoadRejectsInvalidStreamEditInterval(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"
stream_edit_interval = "later"

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
		t.Fatal("Load() err = nil, want validation error")
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

func TestLoadRejectsInvalidBrokerageConvergenceLimits(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[governor.brokerage]
min_rounds = 3
max_rounds = 2

[providers.anthropic]
api_key = "sk-ant-test"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want brokerage convergence validation error")
	}
	if !strings.Contains(err.Error(), "governor.brokerage.min_rounds") {
		t.Fatalf("error = %v, want governor.brokerage.min_rounds message", err)
	}
}

func TestLoadRejectsInvalidAnthropicCachePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setting string
		wantErr string
	}{
		{name: "strategy", setting: `cache_strategy = "forever"`, wantErr: "providers.anthropic.cache_strategy"},
		{name: "ttl", setting: `cache_ttl = "10m"`, wantErr: "providers.anthropic.cache_ttl"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.toml")
			raw := fmt.Sprintf(`
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"
%s
`, tt.setting)
			if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := Load(configPath)
			if err == nil {
				t.Fatal("Load() err = nil, want cache validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %s", err, tt.wantErr)
			}
		})
	}
}

func TestLoadRejectsInvalidSandboxProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setting string
		wantErr string
	}{
		{name: "mode", setting: `mode = "container"`, wantErr: "sandbox.profiles.approved_user.mode"},
		{name: "network", setting: `network = "full"`, wantErr: "sandbox.profiles.approved_user.network"},
		{name: "allowlist without destinations", setting: `network = "allowlist"`, wantErr: "sandbox.profiles.approved_user.network_allow"},
		{name: "allowlist destination without port", setting: "network = \"allowlist\"\nnetwork_allow = [\"example.com\"]", wantErr: "sandbox.profiles.approved_user.network_allow[0]"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.toml")
			raw := fmt.Sprintf(`
[telegram]
bot_token = "tg-test"

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"

[sandbox.profiles.approved_user]
%s
`, tt.setting)
			if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			_, err := Load(configPath)
			if err == nil {
				t.Fatal("Load() err = nil, want sandbox validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %s", err, tt.wantErr)
			}
		})
	}
}

func TestLoadRejectsOpenAIStorageWithoutOpenAIAPIKey(t *testing.T) {
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

[openai.files]
enabled = true
purpose = "assistants"

[agent]
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want OpenAI storage validation error")
	}
	if !strings.Contains(err.Error(), "providers.openai.api_key") {
		t.Fatalf("error = %v, want providers.openai.api_key requirement", err)
	}
}
