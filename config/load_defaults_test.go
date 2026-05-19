//go:build linux

package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"

[tools]
external_manifest_dir = "./external-tools"
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
	if cfg.Identity.ProjectName != "aphelion" || cfg.Identity.AnonymousProfile {
		t.Fatalf("identity defaults = %#v, want project_name aphelion and anonymous disabled", cfg.Identity)
	}
	if got := EffectiveGovernorName(cfg, "Idolum (System)"); got != "Idolum (System)" {
		t.Fatalf("EffectiveGovernorName = %q, want live default", got)
	}
	if got := EffectiveFaceName(cfg, "Idolum"); got != "Idolum" {
		t.Fatalf("EffectiveFaceName = %q, want live default", got)
	}
	if !cfg.Telegram.DetachPendingOnRestart {
		t.Fatalf("detach_pending_on_restart = %t, want true by default", cfg.Telegram.DetachPendingOnRestart)
	}
	if cfg.Telegram.StreamEditInterval != "300ms" || cfg.Telegram.StreamCursor != " ▉" {
		t.Fatalf("telegram streaming defaults = %#v, want 300ms/block cursor", cfg.Telegram)
	}
	if cfg.Telegram.ToolProgress != "all" || cfg.Telegram.ToolProgressStyle != "semantic" || cfg.Telegram.ToolProgressWindow != 4 || cfg.Telegram.ToolProgressCleanup {
		t.Fatalf("telegram progress defaults = %#v, want all/false", cfg.Telegram)
	}
	if cfg.Telegram.Media.DownloadMaxSize != "20MB" || !cfg.Telegram.Media.AutoVisionPhotos || !cfg.Telegram.Media.AutoVisionDocs || !cfg.Telegram.Media.ExtractPDFText || cfg.Telegram.Media.MaxPDFBytes != "8MB" {
		t.Fatalf("telegram media defaults = %#v, want 20MB + auto vision/pdf extract", cfg.Telegram.Media)
	}
	if !cfg.Recovery.Watchdog.Enabled || cfg.Recovery.Watchdog.StaleTurnThreshold != "3m" || cfg.Recovery.Watchdog.StaleTurnLimit != 8 {
		t.Fatalf("recovery watchdog defaults = %#v, want enabled 3m/8", cfg.Recovery.Watchdog)
	}
	if cfg.Tailscale.Enabled || cfg.Tailscale.Backend != "cli" || cfg.Tailscale.CLIPath != "tailscale" || cfg.Tailscale.CommandTimeout != "5s" {
		t.Fatalf("tailscale defaults = %#v, want disabled cli backend", cfg.Tailscale)
	}
	if cfg.Tailscale.Parent.Enabled || cfg.Tailscale.Parent.Hostname != "aphelion" || !strings.HasSuffix(cfg.Tailscale.Parent.StateDir, "/.aphelion/state/tailnet/parent") || cfg.Tailscale.Parent.ListenAddr != ":8765" || cfg.Tailscale.Parent.AuthKeyEnv != "APHELION_TS_AUTHKEY" {
		t.Fatalf("tailscale parent defaults = %#v, want disabled parent tsnet defaults", cfg.Tailscale.Parent)
	}
	if cfg.Governor.Backend != "auto" {
		t.Fatalf("governor.backend = %q, want auto", cfg.Governor.Backend)
	}
	if cfg.Work.Executor != "auto" || !reflect.DeepEqual(cfg.Work.AutoOrder, []string{"native", "codex"}) {
		t.Fatalf("work executor defaults = %#v, want auto native->codex", cfg.Work)
	}
	if cfg.Autonomy.DefaultMode != "ask_first" || cfg.Autonomy.Ceiling != "leased" || !cfg.Autonomy.AllowLiveOverrides || cfg.Autonomy.MaxOverrideDuration != "4h" {
		t.Fatalf("autonomy defaults = %#v, want ask_first default with leased live override ceiling", cfg.Autonomy)
	}
	if cfg.Governor.NativeProvider != "anthropic" || cfg.Providers.Default != "anthropic" {
		t.Fatalf("provider heuristic = governor:%q default:%q, want anthropic/anthropic", cfg.Governor.NativeProvider, cfg.Providers.Default)
	}
	if cfg.Providers.Anthropic.Model != "claude-sonnet-4-6" {
		t.Fatalf("model = %q", cfg.Providers.Anthropic.Model)
	}
	if cfg.Sessions.IdleExpiry != "24h" {
		t.Fatalf("idle_expiry = %q, want 24h", cfg.Sessions.IdleExpiry)
	}
	if cfg.Sessions.MaxContextRatio != 0.9 || cfg.Sessions.CompactionRatio != 0.7 || cfg.Sessions.CompactionStrategy != "summarize" {
		t.Fatalf("session compaction defaults = %#v, want 0.9/0.7/summarize", cfg.Sessions)
	}
	if cfg.Sessions.TESRetention.Enabled || cfg.Sessions.TESRetention.MaxAge != "720h" || cfg.Sessions.TESRetention.MinRetainedRows != 5000 || cfg.Sessions.TESRetention.MaxDeletePerGC != 1000 {
		t.Fatalf("session tes retention defaults = %#v, want disabled/720h/5000/1000", cfg.Sessions.TESRetention)
	}
	if !strings.HasSuffix(cfg.Sessions.TESRetention.ExportDir, "/.aphelion/state/tes-exports") {
		t.Fatalf("session tes retention export_dir = %q, want ~/.aphelion/state/tes-exports expansion", cfg.Sessions.TESRetention.ExportDir)
	}
	if cfg.Governor.Codex.ContextWindow != 250000 {
		t.Fatalf("governor.codex.context_window = %d, want 250000", cfg.Governor.Codex.ContextWindow)
	}
	if !cfg.Governor.Codex.StoreResponses {
		t.Fatalf("governor.codex.store_responses = false, want true by default")
	}
	if cfg.Governor.Brokerage.MinRounds != 1 || cfg.Governor.Brokerage.MaxRounds != 4 || cfg.Governor.Brokerage.AbsoluteMaxRounds != 6 || cfg.Governor.Brokerage.MaxElapsed != "20s" || cfg.Governor.Brokerage.StableContractRounds != 2 {
		t.Fatalf("governor.brokerage defaults = %#v, want 1/4/6/20s/stable=2", cfg.Governor.Brokerage)
	}
	if !cfg.Governor.Brokerage.StopOnStableContract || !cfg.Governor.Brokerage.StopOnRepeatedProposal || !cfg.Governor.Brokerage.StopOnReject {
		t.Fatalf("governor.brokerage stop defaults = %#v, want all enabled", cfg.Governor.Brokerage)
	}
	if cfg.Providers.Anthropic.ContextWindow != 200000 {
		t.Fatalf("providers.anthropic.context_window = %d, want 200000", cfg.Providers.Anthropic.ContextWindow)
	}
	if cfg.Providers.OpenRouter.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("providers.openrouter.base_url = %q, want default openrouter url", cfg.Providers.OpenRouter.BaseURL)
	}
	if len(cfg.Providers.FallbackChain) != 0 {
		t.Fatalf("providers.fallback_chain = %#v, want empty", cfg.Providers.FallbackChain)
	}
	if cfg.Providers.OpenAI.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("providers.openai.base_url = %q, want default openai url", cfg.Providers.OpenAI.BaseURL)
	}
	if cfg.OpenAI.Files.Enabled || cfg.OpenAI.Files.Purpose != "assistants" {
		t.Fatalf("openai.files defaults = %#v, want disabled/assistants", cfg.OpenAI.Files)
	}
	if cfg.OpenAI.VectorStores.Enabled || cfg.OpenAI.VectorStores.DefaultStore != "" {
		t.Fatalf("openai.vector_stores defaults = %#v, want disabled/empty", cfg.OpenAI.VectorStores)
	}
	if !strings.HasSuffix(cfg.Agent.ExecRoot, "/workspace") {
		t.Fatalf("exec_root = %q, want expanded relative path", cfg.Agent.ExecRoot)
	}
	if !strings.HasSuffix(cfg.Tools.ExternalManifestDir, "/external-tools") {
		t.Fatalf("tools.external_manifest_dir = %q, want expanded relative path", cfg.Tools.ExternalManifestDir)
	}
	if len(cfg.Agent.BootstrapFiles) == 0 || cfg.Agent.BootstrapFiles[0] != "SOUL.md" {
		t.Fatalf("bootstrap files = %#v, want defaults", cfg.Agent.BootstrapFiles)
	}
	if !containsString(cfg.Agent.DynamicFiles, "SKILLS.md") || !containsString(cfg.Agent.DynamicFiles, "memory/questions.md") || !containsString(cfg.Agent.DynamicFiles, "memory/rhizome.md") || !containsString(cfg.Agent.DynamicFiles, "memory/dreams.md") {
		t.Fatalf("dynamic files = %#v, want skills and all structured memory stores", cfg.Agent.DynamicFiles)
	}
	if cfg.Agent.BootstrapTotalMaxChars < 900000 {
		t.Fatalf("bootstrap_total_max_chars = %d, want enough character budget for ~250k-token model context", cfg.Agent.BootstrapTotalMaxChars)
	}
	if !cfg.Agent.DailyNotes {
		t.Fatal("daily notes should default to enabled")
	}
	if cfg.Agent.DailyNotesDir != "memory/daily" {
		t.Fatalf("daily_notes_dir = %q, want memory/daily", cfg.Agent.DailyNotesDir)
	}
	if !cfg.Memory.Reflection.Enabled || cfg.Memory.Reflection.Every != "6h" {
		t.Fatalf("memory.reflection defaults = %#v, want enabled/6h", cfg.Memory.Reflection)
	}
	if cfg.Memory.Semantic.Enabled || cfg.Memory.Semantic.Backend != "local" || cfg.Memory.Semantic.Refresh != "manual" {
		t.Fatalf("memory.semantic defaults = %#v, want disabled/local/manual", cfg.Memory.Semantic)
	}
	if got, want := cfg.Memory.Semantic.Sources, []string{"MEMORY.md", "SKILLS.md", "memory/knowledge.md", "memory/decisions.md", "memory/questions.md", "memory/rhizome.md", "memory/dreams.md"}; !equalStrings(got, want) {
		t.Fatalf("memory.semantic.sources defaults = %#v, want %#v", got, want)
	}
	if !cfg.Memory.Semantic.IncludeDailyNotes || !cfg.Memory.Semantic.IncludeQuestions || !cfg.Memory.Semantic.IncludeRhizome {
		t.Fatalf("memory.semantic include defaults = %#v, want daily/questions/rhizome enabled", cfg.Memory.Semantic)
	}
	if cfg.Memory.Aggressive.Enabled || cfg.Memory.Aggressive.CaptureEveryTurn || cfg.Memory.Aggressive.PrefetchEveryTurn || cfg.Memory.Aggressive.FlushOnSessionBoundary {
		t.Fatalf("memory.aggressive defaults = %#v, want all disabled", cfg.Memory.Aggressive)
	}
	if !cfg.Memory.Decay.Enabled || cfg.Memory.Decay.HotDays != 3 || cfg.Memory.Decay.WarmDays != 14 || cfg.Memory.Decay.ColdDays != 30 {
		t.Fatalf("memory.decay defaults = %#v, want enabled 3/14/30", cfg.Memory.Decay)
	}
	if len(cfg.Memory.Identity.Preserve) == 0 || cfg.Memory.Identity.Preserve[0] != "SOUL.md" {
		t.Fatalf("memory.identity.preserve = %#v, want defaults", cfg.Memory.Identity.Preserve)
	}
	if cfg.Memory.WritePolicy.DirectUserWrites != "apply" || cfg.Memory.WritePolicy.ReflectionWrites != "propose" || cfg.Memory.WritePolicy.AggressiveWrites != "propose" || cfg.Memory.WritePolicy.AutoAcceptLowRisk {
		t.Fatalf("memory.write_policy defaults = %#v, want apply/propose/propose/manual", cfg.Memory.WritePolicy)
	}
	if cfg.Thinking.Effort != "medium" || cfg.Thinking.Summary != "auto" {
		t.Fatalf("thinking defaults = %#v, want medium/auto", cfg.Thinking)
	}
	if cfg.Thinking.Defaults.Default != "medium" || cfg.Thinking.Defaults.Heartbeat != "low" || cfg.Thinking.Defaults.Cron != "low" || cfg.Thinking.Defaults.Recovery != "medium" {
		t.Fatalf("thinking.defaults = %#v, want medium/low/low/medium", cfg.Thinking.Defaults)
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
	if cfg.DurableAgents.ControlPlane.Enabled {
		t.Fatalf("durable_agents.control_plane.enabled = true, want false by default")
	}
}

func TestLoadWarnsOnIgnoredConfigKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"
allowed_chats = [123]

[principals.telegram]
admin_user_ids = [123]

[providers.anthropic]
api_key = "sk-ant-test"
old_cache_ttl = "1h"

[providers.experimental]
api_key = "experimental-test"

[agent]
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	summary := cfg.WarningSummary()
	for _, want := range []string{"telegram.allowed_chats", "providers.anthropic.old_cache_ttl", "providers.experimental"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("warning summary = %q, want %s", summary, want)
		}
	}
}

func TestLoadConfigExampleIsLiveSchema(t *testing.T) {
	t.Parallel()

	cfg, err := Load(filepath.Join("..", "config.example.toml"))
	if err != nil {
		t.Fatalf("Load(config.example.toml) err = %v", err)
	}
	if summary := cfg.WarningSummary(); summary != "" {
		t.Fatalf("config.example.toml warning summary = %q, want no ignored live-example keys", summary)
	}
	if cfg.Sandbox.Profiles.ApprovedUser.Network != "deny" || cfg.Sandbox.Profiles.DurableAgent.Network != "deny" {
		t.Fatalf("config.example sandbox networks = approved:%q durable:%q, want deny/deny", cfg.Sandbox.Profiles.ApprovedUser.Network, cfg.Sandbox.Profiles.DurableAgent.Network)
	}
}

func TestIdentityAnonymousProfileUsesGenericDefaults(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Identity.AnonymousProfile = true
	if got := EffectiveGovernorName(&cfg, "Idolum (System)"); got != "System" {
		t.Fatalf("anonymous governor name = %q, want System", got)
	}
	if got := EffectiveFaceName(&cfg, "Idolum"); got != "Assistant" {
		t.Fatalf("anonymous face name = %q, want Assistant", got)
	}
	cfg.Identity.GovernorName = "House"
	cfg.Identity.FaceName = "Scene"
	if got := EffectiveGovernorName(&cfg, "Idolum (System)"); got != "House" {
		t.Fatalf("configured governor name = %q, want House", got)
	}
	if got := EffectiveFaceName(&cfg, "Idolum"); got != "Scene" {
		t.Fatalf("configured face name = %q, want Scene", got)
	}
}

func TestEffectiveUserAgentHonorsAnonymousProfile(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if got := EffectiveUserAgent(&cfg, "aphelion-fetch-url/1"); got != "aphelion-fetch-url/1" {
		t.Fatalf("EffectiveUserAgent default = %q, want configured default", got)
	}
	cfg.Identity.AnonymousProfile = true
	if got := EffectiveUserAgent(&cfg, "aphelion-fetch-url/1"); got != "" {
		t.Fatalf("EffectiveUserAgent anonymous = %q, want empty", got)
	}
	cfg.Identity.UserAgent = "custom-agent/1"
	if got := EffectiveUserAgent(&cfg, "aphelion-fetch-url/1"); got != "custom-agent/1" {
		t.Fatalf("EffectiveUserAgent configured = %q, want custom-agent/1", got)
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
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"
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
detach_pending_on_restart = false
poll_timeout = 11
stream_edit_interval = "450ms"
stream_cursor = " .."
tool_progress = "new"
tool_progress_style = "raw"
tool_progress_window = 6
tool_progress_cleanup = true

[telegram.media]
download_max_size = "12MB"
auto_vision_photos = false
auto_vision_documents = true
extract_pdf_text = false
max_pdf_bytes = "4MB"

[tailscale]
enabled = true
backend = "cli"
cli_path = "/usr/bin/tailscale"
command_timeout = "3s"
expected_tailnet = "example.ts.net"
expected_hostname = "aphelion-admin"
expected_tags = ["tag:admin", "tag:aphelion", "tag:admin"]

[tailscale.parent]
enabled = true
hostname = "aphelion-admin"
state_dir = "~/tailnet-parent"
listen_addr = ":9443"
auth_key_env = "APHELION_TAILSCALE_TEST_AUTHKEY"
auth_key_file = "~/tailnet-auth.key"
tags = ["tag:aphelion-admin", "tag:admin", "tag:admin"]
admin_login_names = ["admin@example.com", "ops@example.com", "admin@example.com"]

[principals.telegram]
admin_user_ids = [123]

[governor]
backend = "native"
native_provider = "anthropic"

	[governor.codex]
	auth_source = "codex_cli"
	auth_path = "/var/lib/aphelion/codex-auth.json"
	codex_home = "~/codex-home"
	base_url = "https://chatgpt.com/backend-api"
	model = "gpt-5.5"
	context_window = 180000
	store_responses = false
	max_continuations = 5
	transport_retries = 2

	[governor.brokerage]
	min_rounds = 2
	max_rounds = 5
	absolute_max_rounds = 7
	max_elapsed = "45s"
	stable_contract_rounds = 3
	stop_on_stable_contract = false
	stop_on_repeated_proposal = true
	stop_on_reject = false

[providers.anthropic]
api_key = "sk-ant-test"
model = "claude-opus-4-6"
max_tokens = 8192
context_window = 190000
cache_strategy = "hybrid"
cache_ttl = "1h"

[providers.openai]
api_key = "sk-openai-test"
base_url = "https://api.openai.test/v1"
model = "gpt-5.4"
max_tokens = 12000
context_window = 128000

[openai.files]
enabled = true
purpose = "assistants"

[openai.vector_stores]
enabled = true
default_store = "vs_default"

[autonomy]
default_mode = "review-only"
ceiling = "leased"
allow_live_overrides = true
max_override_duration = "3h"

[sessions]
db_path = "~/tmp/sessions.db"
idle_expiry = "36h"
max_context_ratio = 0.7
compaction_ratio = 0.5
compaction_strategy = "truncate"

[sessions.tes_retention]
enabled = true
max_age = "336h"
min_retained_rows = 8000
max_delete_per_gc = 400
export_dir = "~/tmp/tes-exports"

[recovery.watchdog]
enabled = false
stale_turn_threshold = "12m"
stale_turn_limit = 3

[agent]
prompt_root = "~/agent"
exec_root = "~/workspace"
shared_memory_root = "~/agent"
max_iterations = 77
tool_timeout = 9
bootstrap_files = ["AGENTS.md"]
dynamic_files = ["MEMORY.md", "HEARTBEAT.md"]
bootstrap_max_chars = 500
bootstrap_total_max_chars = 600
daily_notes = false
daily_notes_dir = "notes"

[sandbox.profiles.admin]
mode = "trusted"
network = "allowlist"

[sandbox.profiles.approved_user]
mode = "isolated"
readonly_root = true
writable_paths = ["{user_workspace}", "/tmp/aphelion-approved"]
readonly_paths = ["{global_root}"]
hidden_paths = ["~/.ssh", "~/.tokens"]
network = "deny"

[sandbox.profiles.durable_agent]
mode = "isolated"
readonly_root = true
writable_paths = ["{working_root}", "{shared_memory_root}", "/tmp/aphelion-durable"]
readonly_paths = ["{global_root}"]
hidden_paths = ["~/.ssh"]
network = "allowlist"
network_allow = ["api.openai.com:443"]

[memory.reflection]
enabled = false
every = "12h"

[memory.semantic]
enabled = true
backend = "local"
refresh = "heartbeat"
sources = ["MEMORY.md", "memory/knowledge.md"]
include_daily_notes = true
include_questions = true
include_rhizome = false
interactive_top_k = 7
heartbeat_top_k = 15
interactive_max_chars = 5000
heartbeat_max_chars = 14000

[memory.aggressive]
enabled = true
capture_every_turn = true
prefetch_every_turn = true
flush_on_session_boundary = true

[memory.decay]
enabled = true
hot_days = 2
warm_days = 7
cold_days = 21

	[memory.identity]
	preserve = ["SOUL.md", "IDENTITY.md"]

	[memory.write_policy]
	direct_user_writes = "apply"
	reflection_writes = "apply"
	aggressive_writes = "propose"
	auto_accept_low_risk = true

	[thinking]
effort = "high"
summary = "compact"

[thinking.defaults]
default = "high"
heartbeat = "medium"
cron = "low"
recovery = "high"

[face]
backend = "floor_fallback"

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
mode = "auto"
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
	if cfg.Telegram.DetachPendingOnRestart {
		t.Fatalf("detach_pending_on_restart = %t, want false", cfg.Telegram.DetachPendingOnRestart)
	}
	if cfg.Telegram.Media.DownloadMaxSize != "12MB" || cfg.Telegram.Media.AutoVisionPhotos || !cfg.Telegram.Media.AutoVisionDocs || cfg.Telegram.Media.ExtractPDFText || cfg.Telegram.Media.MaxPDFBytes != "4MB" {
		t.Fatalf("telegram.media = %#v, want explicit overrides", cfg.Telegram.Media)
	}
	if cfg.Telegram.StreamEditInterval != "450ms" || cfg.Telegram.StreamCursor != " .." {
		t.Fatalf("telegram stream config = %#v, want 450ms/' ..'", cfg.Telegram)
	}
	if cfg.Telegram.ToolProgress != "new" || cfg.Telegram.ToolProgressStyle != "raw" || cfg.Telegram.ToolProgressWindow != 6 || !cfg.Telegram.ToolProgressCleanup {
		t.Fatalf("telegram progress = %#v, want new/raw/6/true", cfg.Telegram)
	}
	if !cfg.Tailscale.Enabled || cfg.Tailscale.Backend != "cli" || cfg.Tailscale.CLIPath != "/usr/bin/tailscale" || cfg.Tailscale.CommandTimeout != "3s" || cfg.Tailscale.ExpectedTailnet != "example.ts.net" || cfg.Tailscale.ExpectedHostname != "aphelion-admin" || !reflect.DeepEqual(cfg.Tailscale.ExpectedTags, []string{"tag:admin", "tag:aphelion"}) {
		t.Fatalf("tailscale config = %#v, want explicit normalized overrides", cfg.Tailscale)
	}
	if !cfg.Tailscale.Parent.Enabled || cfg.Tailscale.Parent.Hostname != "aphelion-admin" || !strings.HasSuffix(cfg.Tailscale.Parent.StateDir, "/tailnet-parent") || cfg.Tailscale.Parent.ListenAddr != ":9443" || cfg.Tailscale.Parent.AuthKeyEnv != "APHELION_TAILSCALE_TEST_AUTHKEY" || !strings.HasSuffix(cfg.Tailscale.Parent.AuthKeyFile, "/tailnet-auth.key") || !reflect.DeepEqual(cfg.Tailscale.Parent.Tags, []string{"tag:aphelion-admin", "tag:admin"}) || !reflect.DeepEqual(cfg.Tailscale.Parent.AdminLoginNames, []string{"admin@example.com", "ops@example.com"}) {
		t.Fatalf("tailscale parent = %#v, want explicit normalized parent config", cfg.Tailscale.Parent)
	}
	if cfg.Providers.Anthropic.Model != "claude-opus-4-6" {
		t.Fatalf("model = %q, want claude-opus-4-6", cfg.Providers.Anthropic.Model)
	}
	if cfg.Providers.Anthropic.MaxTokens != 8192 {
		t.Fatalf("max_tokens = %d, want 8192", cfg.Providers.Anthropic.MaxTokens)
	}
	if cfg.Providers.Anthropic.CacheStrategy != "hybrid" || cfg.Providers.Anthropic.CacheTTL != "1h" {
		t.Fatalf("anthropic cache = %s/%s, want hybrid/1h", cfg.Providers.Anthropic.CacheStrategy, cfg.Providers.Anthropic.CacheTTL)
	}
	if cfg.Providers.OpenAI.APIKey != "sk-openai-test" || cfg.Providers.OpenAI.BaseURL != "https://api.openai.test/v1" {
		t.Fatalf("providers.openai = %#v, want parsed openai provider config", cfg.Providers.OpenAI)
	}
	if !cfg.OpenAI.Files.Enabled || cfg.OpenAI.Files.Purpose != "assistants" {
		t.Fatalf("openai.files = %#v, want enabled assistants", cfg.OpenAI.Files)
	}
	if !cfg.OpenAI.VectorStores.Enabled || cfg.OpenAI.VectorStores.DefaultStore != "vs_default" {
		t.Fatalf("openai.vector_stores = %#v, want enabled/vs_default", cfg.OpenAI.VectorStores)
	}
	if cfg.Autonomy.DefaultMode != "review_only" || cfg.Autonomy.Ceiling != "leased" || !cfg.Autonomy.AllowLiveOverrides || cfg.Autonomy.MaxOverrideDuration != "3h" {
		t.Fatalf("autonomy = %#v, want normalized review_only/leased with live overrides", cfg.Autonomy)
	}
	policy := EffectiveAutonomyPolicy(cfg)
	if policy.DefaultMode != "review_only" || policy.Ceiling != "leased" || !policy.AllowLiveOverrides || policy.MaxOverrideDuration != 3*time.Hour {
		t.Fatalf("EffectiveAutonomyPolicy = %#v, want parsed policy", policy)
	}
	if cfg.Agent.MaxIterations != 77 || cfg.Agent.ToolTimeout != 9 {
		t.Fatalf("agent limits = %d/%d, want 77/9", cfg.Agent.MaxIterations, cfg.Agent.ToolTimeout)
	}
	if cfg.Recovery.Watchdog.Enabled || cfg.Recovery.Watchdog.StaleTurnThreshold != "12m" || cfg.Recovery.Watchdog.StaleTurnLimit != 3 {
		t.Fatalf("recovery.watchdog = %#v, want explicit overrides", cfg.Recovery.Watchdog)
	}
	if cfg.Governor.Backend != "native" {
		t.Fatalf("governor.backend = %q, want native", cfg.Governor.Backend)
	}
	if cfg.Governor.Codex.AuthSource != "codex_cli" {
		t.Fatalf("governor.codex.auth_source = %q, want codex_cli", cfg.Governor.Codex.AuthSource)
	}
	if cfg.Governor.Codex.AuthPath != "/var/lib/aphelion/codex-auth.json" {
		t.Fatalf("governor.codex.auth_path = %q, want /var/lib/aphelion/codex-auth.json", cfg.Governor.Codex.AuthPath)
	}
	if cfg.Governor.Codex.Model != "gpt-5.5" {
		t.Fatalf("governor.codex.model = %q, want gpt-5.5", cfg.Governor.Codex.Model)
	}
	if cfg.Governor.Codex.ContextWindow != 180000 {
		t.Fatalf("governor.codex.context_window = %d, want 180000", cfg.Governor.Codex.ContextWindow)
	}
	if cfg.Governor.Codex.StoreResponses {
		t.Fatalf("governor.codex.store_responses = true, want explicit false")
	}
	if cfg.Governor.Codex.MaxContinuations != 5 {
		t.Fatalf("governor.codex.max_continuations = %d, want 5", cfg.Governor.Codex.MaxContinuations)
	}
	if cfg.Governor.Codex.TransportRetries != 2 {
		t.Fatalf("governor.codex.transport_retries = %d, want 2", cfg.Governor.Codex.TransportRetries)
	}
	if cfg.Governor.Brokerage.MinRounds != 2 || cfg.Governor.Brokerage.MaxRounds != 5 || cfg.Governor.Brokerage.AbsoluteMaxRounds != 7 || cfg.Governor.Brokerage.MaxElapsed != "45s" || cfg.Governor.Brokerage.StableContractRounds != 3 {
		t.Fatalf("governor.brokerage = %#v, want explicit convergence limits", cfg.Governor.Brokerage)
	}
	if cfg.Governor.Brokerage.StopOnStableContract || !cfg.Governor.Brokerage.StopOnRepeatedProposal || cfg.Governor.Brokerage.StopOnReject {
		t.Fatalf("governor.brokerage stop flags = %#v, want false/true/false", cfg.Governor.Brokerage)
	}
	if cfg.Sessions.IdleExpiry != "36h" {
		t.Fatalf("idle_expiry = %q, want 36h", cfg.Sessions.IdleExpiry)
	}
	if cfg.Sessions.MaxContextRatio != 0.7 || cfg.Sessions.CompactionRatio != 0.5 || cfg.Sessions.CompactionStrategy != "truncate" {
		t.Fatalf("sessions compaction = %#v, want 0.7/0.5/truncate", cfg.Sessions)
	}
	if !cfg.Sessions.TESRetention.Enabled || cfg.Sessions.TESRetention.MaxAge != "336h" || cfg.Sessions.TESRetention.MinRetainedRows != 8000 || cfg.Sessions.TESRetention.MaxDeletePerGC != 400 {
		t.Fatalf("sessions.tes_retention = %#v, want enabled/336h/8000/400", cfg.Sessions.TESRetention)
	}
	if !strings.HasSuffix(cfg.Sessions.TESRetention.ExportDir, "/tmp/tes-exports") {
		t.Fatalf("sessions.tes_retention.export_dir = %q, want ~/tmp/tes-exports expansion", cfg.Sessions.TESRetention.ExportDir)
	}
	if cfg.Providers.Anthropic.ContextWindow != 190000 {
		t.Fatalf("providers.anthropic.context_window = %d, want 190000", cfg.Providers.Anthropic.ContextWindow)
	}
	if cfg.Agent.DailyNotes {
		t.Fatal("daily_notes = true, want false")
	}
	if cfg.Sandbox.Profiles.ApprovedUser.Mode != "isolated" || !cfg.Sandbox.Profiles.ApprovedUser.ReadonlyRoot || cfg.Sandbox.Profiles.ApprovedUser.Network != "deny" {
		t.Fatalf("sandbox approved_user profile = %#v, want isolated readonly deny", cfg.Sandbox.Profiles.ApprovedUser)
	}
	if got, want := cfg.Sandbox.Profiles.ApprovedUser.WritablePaths, []string{"{user_workspace}", "/tmp/aphelion-approved"}; !equalStrings(got, want) {
		t.Fatalf("sandbox approved_user writable_paths = %#v, want %#v", got, want)
	}
	if cfg.Sandbox.Profiles.DurableAgent.Network != "allowlist" {
		t.Fatalf("sandbox durable_agent network = %q, want allowlist", cfg.Sandbox.Profiles.DurableAgent.Network)
	}
	if got, want := cfg.Sandbox.Profiles.DurableAgent.NetworkAllow, []string{"api.openai.com:443"}; !equalStrings(got, want) {
		t.Fatalf("sandbox durable_agent network_allow = %#v, want %#v", got, want)
	}
	if cfg.Memory.Reflection.Enabled || cfg.Memory.Reflection.Every != "12h" {
		t.Fatalf("memory.reflection = %#v, want disabled/12h", cfg.Memory.Reflection)
	}
	if !cfg.Memory.Semantic.Enabled || cfg.Memory.Semantic.Backend != "local" || cfg.Memory.Semantic.Refresh != "heartbeat" {
		t.Fatalf("memory.semantic = %#v, want enabled/local/heartbeat", cfg.Memory.Semantic)
	}
	if !cfg.Memory.Aggressive.Enabled || !cfg.Memory.Aggressive.CaptureEveryTurn || !cfg.Memory.Aggressive.PrefetchEveryTurn || !cfg.Memory.Aggressive.FlushOnSessionBoundary {
		t.Fatalf("memory.aggressive = %#v, want all enabled", cfg.Memory.Aggressive)
	}
	if got, want := cfg.Memory.Semantic.Sources, []string{"MEMORY.md", "memory/knowledge.md"}; !equalStrings(got, want) {
		t.Fatalf("memory.semantic.sources = %#v, want %#v", got, want)
	}
	if !cfg.Memory.Decay.Enabled || cfg.Memory.Decay.HotDays != 2 || cfg.Memory.Decay.WarmDays != 7 || cfg.Memory.Decay.ColdDays != 21 {
		t.Fatalf("memory.decay = %#v, want enabled 2/7/21", cfg.Memory.Decay)
	}
	if got, want := cfg.Memory.Identity.Preserve, []string{"SOUL.md", "IDENTITY.md"}; !equalStrings(got, want) {
		t.Fatalf("memory.identity.preserve = %#v, want %#v", got, want)
	}
	if cfg.Memory.WritePolicy.DirectUserWrites != "apply" || cfg.Memory.WritePolicy.ReflectionWrites != "apply" || cfg.Memory.WritePolicy.AggressiveWrites != "propose" || !cfg.Memory.WritePolicy.AutoAcceptLowRisk {
		t.Fatalf("memory.write_policy = %#v", cfg.Memory.WritePolicy)
	}
	if cfg.Thinking.Effort != "high" || cfg.Thinking.Summary != "compact" {
		t.Fatalf("thinking = %#v, want high/compact", cfg.Thinking)
	}
	if cfg.Thinking.Defaults.Default != "high" || cfg.Thinking.Defaults.Heartbeat != "medium" || cfg.Thinking.Defaults.Cron != "low" || cfg.Thinking.Defaults.Recovery != "high" {
		t.Fatalf("thinking.defaults = %#v", cfg.Thinking.Defaults)
	}
	if cfg.Face.Backend != "floor_fallback" {
		t.Fatalf("face.backend = %q, want floor_fallback", cfg.Face.Backend)
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
	if cfg.Voice.Mode != "auto" || cfg.Voice.OpenAIModel != "whisper-1" || cfg.Voice.ElevenLabsVoiceID != "voice-123" {
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
