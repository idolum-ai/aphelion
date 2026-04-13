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
	if cfg.Telegram.StreamEditInterval != "300ms" || cfg.Telegram.StreamCursor != " ▉" {
		t.Fatalf("telegram streaming defaults = %#v, want 300ms/block cursor", cfg.Telegram)
	}
	if cfg.Telegram.ToolProgress != "all" || cfg.Telegram.ToolProgressStyle != "semantic" || cfg.Telegram.ToolProgressWindow != 4 || cfg.Telegram.ToolProgressCleanup {
		t.Fatalf("telegram progress defaults = %#v, want all/false", cfg.Telegram)
	}
	if cfg.Telegram.Media.DownloadMaxSize != "20MB" || !cfg.Telegram.Media.AutoVisionPhotos || !cfg.Telegram.Media.AutoVisionDocs || !cfg.Telegram.Media.ExtractPDFText || cfg.Telegram.Media.MaxPDFBytes != "8MB" {
		t.Fatalf("telegram media defaults = %#v, want 20MB + auto vision/pdf extract", cfg.Telegram.Media)
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
	if cfg.Sessions.MaxContextRatio != 0.75 || cfg.Sessions.CompactionRatio != 0.55 || cfg.Sessions.CompactionStrategy != "summarize" {
		t.Fatalf("session compaction defaults = %#v, want 0.75/0.55/summarize", cfg.Sessions)
	}
	if cfg.Governor.Codex.ContextWindow != 200000 {
		t.Fatalf("governor.codex.context_window = %d, want 200000", cfg.Governor.Codex.ContextWindow)
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
	if !strings.HasSuffix(cfg.Agent.Workspace, "/workspace") {
		t.Fatalf("workspace = %q, want expanded relative path", cfg.Agent.Workspace)
	}
	if len(cfg.Agent.BootstrapFiles) == 0 || cfg.Agent.BootstrapFiles[0] != "SOUL.md" {
		t.Fatalf("bootstrap files = %#v, want defaults", cfg.Agent.BootstrapFiles)
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
	if !cfg.Memory.Decay.Enabled || cfg.Memory.Decay.HotDays != 3 || cfg.Memory.Decay.WarmDays != 14 || cfg.Memory.Decay.ColdDays != 30 {
		t.Fatalf("memory.decay defaults = %#v, want enabled 3/14/30", cfg.Memory.Decay)
	}
	if len(cfg.Memory.Identity.Preserve) == 0 || cfg.Memory.Identity.Preserve[0] != "SOUL.md" {
		t.Fatalf("memory.identity.preserve = %#v, want defaults", cfg.Memory.Identity.Preserve)
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

func TestLoadParsesDurableAgentControlPlane(t *testing.T) {
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

[durable_agents.control_plane]
enabled = true
listen = "127.0.0.1:8787"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if !cfg.DurableAgents.ControlPlane.Enabled {
		t.Fatal("durable_agents.control_plane.enabled = false, want true")
	}
	if cfg.DurableAgents.ControlPlane.Listen != "127.0.0.1:8787" {
		t.Fatalf("durable_agents.control_plane.listen = %q, want 127.0.0.1:8787", cfg.DurableAgents.ControlPlane.Listen)
	}
}

func TestLoadRejectsEnabledDurableAgentControlPlaneWithoutListen(t *testing.T) {
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

[durable_agents.control_plane]
enabled = true
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want durable agent control plane listen validation error")
	}
	if !strings.Contains(err.Error(), "durable_agents.control_plane.listen is required") {
		t.Fatalf("Load() err = %v, want durable_agents.control_plane.listen validation", err)
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

[principals.telegram]
admin_user_ids = [123]
approved_user_ids = [456]

[governor]
backend = "native"
native_provider = "anthropic"

[governor.codex]
auth_source = "codex_cli"
codex_home = "~/codex-home"
base_url = "https://chatgpt.com/backend-api"
context_window = 180000

[providers.anthropic]
api_key = "sk-ant-test"
model = "claude-opus-4-6"
max_tokens = 8192
context_window = 190000

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

[sessions]
db_path = "~/tmp/sessions.db"
idle_expiry = "36h"
max_context_ratio = 0.7
compaction_ratio = 0.5
compaction_strategy = "truncate"

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

[memory.decay]
enabled = true
hot_days = 2
warm_days = 7
cold_days = 21

[memory.identity]
preserve = ["SOUL.md", "IDENTITY.md"]

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
	if cfg.Telegram.Media.DownloadMaxSize != "12MB" || cfg.Telegram.Media.AutoVisionPhotos || !cfg.Telegram.Media.AutoVisionDocs || cfg.Telegram.Media.ExtractPDFText || cfg.Telegram.Media.MaxPDFBytes != "4MB" {
		t.Fatalf("telegram.media = %#v, want explicit overrides", cfg.Telegram.Media)
	}
	if cfg.Telegram.StreamEditInterval != "450ms" || cfg.Telegram.StreamCursor != " .." {
		t.Fatalf("telegram stream config = %#v, want 450ms/' ..'", cfg.Telegram)
	}
	if cfg.Telegram.ToolProgress != "new" || cfg.Telegram.ToolProgressStyle != "raw" || cfg.Telegram.ToolProgressWindow != 6 || !cfg.Telegram.ToolProgressCleanup {
		t.Fatalf("telegram progress = %#v, want new/raw/6/true", cfg.Telegram)
	}
	if cfg.Providers.Anthropic.Model != "claude-opus-4-6" {
		t.Fatalf("model = %q, want claude-opus-4-6", cfg.Providers.Anthropic.Model)
	}
	if cfg.Providers.Anthropic.MaxTokens != 8192 {
		t.Fatalf("max_tokens = %d, want 8192", cfg.Providers.Anthropic.MaxTokens)
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
	if cfg.Agent.MaxIterations != 77 || cfg.Agent.ToolTimeout != 9 {
		t.Fatalf("agent limits = %d/%d, want 77/9", cfg.Agent.MaxIterations, cfg.Agent.ToolTimeout)
	}
	if cfg.Governor.Backend != "native" {
		t.Fatalf("governor.backend = %q, want native", cfg.Governor.Backend)
	}
	if cfg.Governor.Codex.AuthSource != "codex_cli" {
		t.Fatalf("governor.codex.auth_source = %q, want codex_cli", cfg.Governor.Codex.AuthSource)
	}
	if cfg.Governor.Codex.ContextWindow != 180000 {
		t.Fatalf("governor.codex.context_window = %d, want 180000", cfg.Governor.Codex.ContextWindow)
	}
	if cfg.Sessions.IdleExpiry != "36h" {
		t.Fatalf("idle_expiry = %q, want 36h", cfg.Sessions.IdleExpiry)
	}
	if cfg.Sessions.MaxContextRatio != 0.7 || cfg.Sessions.CompactionRatio != 0.5 || cfg.Sessions.CompactionStrategy != "truncate" {
		t.Fatalf("sessions compaction = %#v, want 0.7/0.5/truncate", cfg.Sessions)
	}
	if cfg.Providers.Anthropic.ContextWindow != 190000 {
		t.Fatalf("providers.anthropic.context_window = %d, want 190000", cfg.Providers.Anthropic.ContextWindow)
	}
	if cfg.Agent.DailyNotes {
		t.Fatal("daily_notes = true, want false")
	}
	if cfg.Memory.Reflection.Enabled || cfg.Memory.Reflection.Every != "12h" {
		t.Fatalf("memory.reflection = %#v, want disabled/12h", cfg.Memory.Reflection)
	}
	if !cfg.Memory.Semantic.Enabled || cfg.Memory.Semantic.Backend != "local" || cfg.Memory.Semantic.Refresh != "heartbeat" {
		t.Fatalf("memory.semantic = %#v, want enabled/local/heartbeat", cfg.Memory.Semantic)
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

func TestLoadParsesTelegramDurableGroups(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[[telegram.durable_groups]]
chat_id = -100123
agent_id = "family-group"
charter = "Help locally in the family group without taking on standing role changes."
respond_on = "all"
llm_provider = "openrouter"
llm_api_key = "sk-or-group"
llm_model = "openrouter/test-model"

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
	if len(cfg.Telegram.DurableGroups) != 1 {
		t.Fatalf("durable groups = %d, want 1", len(cfg.Telegram.DurableGroups))
	}
	group := cfg.Telegram.DurableGroups[0]
	if group.ChatID != -100123 {
		t.Fatalf("chat_id = %d, want -100123", group.ChatID)
	}
	if group.AgentID != "family-group" {
		t.Fatalf("agent_id = %q, want family-group", group.AgentID)
	}
	if group.RespondOn != "all" {
		t.Fatalf("respond_on = %q, want all", group.RespondOn)
	}
	if group.LLMProvider != "openrouter" {
		t.Fatalf("llm_provider = %q, want openrouter", group.LLMProvider)
	}
	if group.LLMAPIKey != "sk-or-group" {
		t.Fatalf("llm_api_key = %q, want sk-or-group", group.LLMAPIKey)
	}
	if group.LLMModel != "openrouter/test-model" {
		t.Fatalf("llm_model = %q, want openrouter/test-model", group.LLMModel)
	}
}

func TestLoadRejectsInvalidTelegramDurableGroupAgentID(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[[telegram.durable_groups]]
chat_id = -100123
agent_id = "family/group"
charter = "Help locally."
llm_provider = "anthropic"
llm_api_key = "sk-ant-group"

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

	_, err := Load(configPath)
	if err == nil || !strings.Contains(err.Error(), "agent_id must contain only") {
		t.Fatalf("Load() err = %v, want durable group agent_id validation error", err)
	}
}

func TestLoadRejectsTelegramDurableGroupMissingLLMBootstrap(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[[telegram.durable_groups]]
chat_id = -100123
agent_id = "family-group"
charter = "Help locally."
respond_on = "mentions"

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

	_, err := Load(configPath)
	if err == nil || !strings.Contains(err.Error(), "llm_backend must be one of native|codex") {
		t.Fatalf("Load() err = %v, want durable group llm bootstrap validation error", err)
	}
}

func TestLoadParsesTelegramDurableGroupCodexBootstrap(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	raw := `
[telegram]
bot_token = "tg-test"

[[telegram.durable_groups]]
chat_id = -100123
agent_id = "family-group"
charter = "Help locally in the family group."
respond_on = "mentions"
llm_backend = "codex"
llm_codex_auth_source = "codex_cli"
llm_codex_home = "/srv/family-group/.codex"
llm_codex_base_url = "https://chatgpt.example.test/backend-api"

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
	group := cfg.Telegram.DurableGroups[0]
	if group.LLMBackend != "codex" {
		t.Fatalf("llm_backend = %q, want codex", group.LLMBackend)
	}
	if group.LLMCodexAuthSource != "codex_cli" {
		t.Fatalf("llm_codex_auth_source = %q, want codex_cli", group.LLMCodexAuthSource)
	}
	if group.LLMCodexHome != "/srv/family-group/.codex" {
		t.Fatalf("llm_codex_home = %q, want /srv/family-group/.codex", group.LLMCodexHome)
	}
	if group.LLMCodexBaseURL != "https://chatgpt.example.test/backend-api" {
		t.Fatalf("llm_codex_base_url = %q, want https://chatgpt.example.test/backend-api", group.LLMCodexBaseURL)
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
workspace = "./workspace"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err != nil {
		t.Fatalf("Load() err = %v, want codex passthrough config to validate without Anthropic key", err)
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
workspace = "./workspace"
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
