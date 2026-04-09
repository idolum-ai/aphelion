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
	if cfg.Telegram.ToolProgress != "all" || cfg.Telegram.ToolProgressCleanup {
		t.Fatalf("telegram progress defaults = %#v, want all/false", cfg.Telegram)
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
	if cfg.Face.Backend != "provider" {
		t.Fatalf("face.backend = %q, want provider", cfg.Face.Backend)
	}
	if !(!cfg.Heartbeat.Enabled && cfg.Heartbeat.Every == "30m" && cfg.Heartbeat.Target == "last") {
		t.Fatalf("heartbeat defaults = %#v, want disabled 30m last", cfg.Heartbeat)
	}
	if cfg.Cron.Enabled || len(cfg.Cron.Jobs) != 0 {
		t.Fatalf("cron defaults = %#v, want disabled with no jobs", cfg.Cron)
	}
	if cfg.Voice.Mode != "off" {
		t.Fatalf("voice.mode = %q, want off", cfg.Voice.Mode)
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
tool_progress = "new"
tool_progress_cleanup = true

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

[face]
backend = "governor_passthrough"

[heartbeat]
enabled = true
every = "45m"
target = "123"

[heartbeat.active_hours]
start = "08:00"
end = "23:00"
timezone = "America/New_York"

[cron]
enabled = true

[[cron.jobs]]
id = "check-in"
every = "2h"
prompt = "Ping the admin if there is anything worth surfacing."
delivery = "announce"
enabled = true

[voice]
mode = "voice_only"
openai_api_key = "sk-openai"
openai_model = "whisper-1"
elevenlabs_api_key = "xi-test"
elevenlabs_voice_id = "voice-123"
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
	if cfg.Telegram.ToolProgress != "new" || !cfg.Telegram.ToolProgressCleanup {
		t.Fatalf("telegram progress = %#v, want new/true", cfg.Telegram)
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
	if cfg.Face.Backend != "governor_passthrough" {
		t.Fatalf("face.backend = %q, want governor_passthrough", cfg.Face.Backend)
	}
	if !cfg.Heartbeat.Enabled || cfg.Heartbeat.Every != "45m" || cfg.Heartbeat.Target != "123" {
		t.Fatalf("heartbeat = %#v, want enabled 45m target 123", cfg.Heartbeat)
	}
	if !cfg.Cron.Enabled || len(cfg.Cron.Jobs) != 1 {
		t.Fatalf("cron = %#v, want enabled with 1 job", cfg.Cron)
	}
	if cfg.Cron.Jobs[0].ID != "check-in" || cfg.Cron.Jobs[0].Every != "2h" || cfg.Cron.Jobs[0].Delivery != "announce" {
		t.Fatalf("cron job = %#v, want parsed job", cfg.Cron.Jobs[0])
	}
	if cfg.Voice.Mode != "voice_only" || cfg.Voice.OpenAIModel != "whisper-1" || cfg.Voice.ElevenLabsVoiceID != "voice-123" {
		t.Fatalf("voice = %#v, want parsed voice config", cfg.Voice)
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

func TestLoadAllowsCodexGovernorPassthroughWithoutAnthropicKey(t *testing.T) {
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
backend = "governor_passthrough"

[agent]
workspace = "./workspace"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err != nil {
		t.Fatalf("Load() err = %v, want codex passthrough config to validate without Anthropic key", err)
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
	if !strings.Contains(err.Error(), "add [principals.telegram] admin_user_ids") {
		t.Fatalf("error = %v, want actionable principal bootstrap hint", err)
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

func TestResolveConfigPathPrefersPrimaryThenLegacyAndEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APHELION_CONFIG", "")

	primary := filepath.Join(home, ".aphelion", "aphelion.toml")
	legacy := filepath.Join(home, ".config", "aphelion", "config.toml")
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err != nil {
		t.Fatalf("mkdir primary dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if err := os.WriteFile(legacy, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := ResolveConfigPath("")
	if err != nil {
		t.Fatalf("ResolveConfigPath() err = %v", err)
	}
	if got != legacy {
		t.Fatalf("config path = %q, want legacy %q when primary is absent", got, legacy)
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

func TestLoadLegacyWorkspaceBackfillsSplitRoots(t *testing.T) {
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

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	wantWorkspace := filepath.Join(dir, "workspace")
	if cfg.Agent.PromptRoot != wantWorkspace {
		t.Fatalf("prompt_root = %q, want %q", cfg.Agent.PromptRoot, wantWorkspace)
	}
	if cfg.Agent.ExecRoot != wantWorkspace {
		t.Fatalf("exec_root = %q, want %q", cfg.Agent.ExecRoot, wantWorkspace)
	}
	if cfg.Agent.SharedMemoryRoot != wantWorkspace {
		t.Fatalf("shared_memory_root = %q, want %q", cfg.Agent.SharedMemoryRoot, wantWorkspace)
	}
	wantUserWorkspaceRoot := filepath.Join(dir, "state", "isolated", "workspaces")
	if cfg.Agent.UserWorkspaceRoot != wantUserWorkspaceRoot {
		t.Fatalf("user_workspace_root = %q, want %q", cfg.Agent.UserWorkspaceRoot, wantUserWorkspaceRoot)
	}
	wantUserMemoryRoot := filepath.Join(dir, "state", "isolated", "memory")
	if cfg.Agent.UserMemoryRoot != wantUserMemoryRoot {
		t.Fatalf("user_memory_root = %q, want %q", cfg.Agent.UserMemoryRoot, wantUserMemoryRoot)
	}
	if cfg.Agent.Workspace != wantWorkspace {
		t.Fatalf("workspace = %q, want legacy root preserved", cfg.Agent.Workspace)
	}
}
