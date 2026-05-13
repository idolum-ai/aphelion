//go:build linux

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"

[durable_agents.control_plane]
enabled = true
listen = "127.0.0.1:8787"
base_path = "/control"
cert_file = "/tmp/cert.pem"
key_file = "/tmp/key.pem"
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
	if cfg.DurableAgents.ControlPlane.BasePath != "/control" {
		t.Fatalf("durable_agents.control_plane.base_path = %q, want /control", cfg.DurableAgents.ControlPlane.BasePath)
	}
	if cfg.DurableAgents.ControlPlane.CertFile != "/tmp/cert.pem" {
		t.Fatalf("durable_agents.control_plane.cert_file = %q, want /tmp/cert.pem", cfg.DurableAgents.ControlPlane.CertFile)
	}
	if cfg.DurableAgents.ControlPlane.KeyFile != "/tmp/key.pem" {
		t.Fatalf("durable_agents.control_plane.key_file = %q, want /tmp/key.pem", cfg.DurableAgents.ControlPlane.KeyFile)
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
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"

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

func TestLoadRejectsPartialDurableAgentControlPlaneTLSConfig(t *testing.T) {
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

[durable_agents.control_plane]
enabled = true
listen = "127.0.0.1:8787"
cert_file = "/tmp/cert.pem"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want durable agent control plane tls validation error")
	}
	if !strings.Contains(err.Error(), "durable_agents.control_plane.cert_file and key_file must be set together") {
		t.Fatalf("Load() err = %v, want cert/key pair validation", err)
	}
}

func TestLoadRejectsPlaintextDurableAgentControlPlaneOnNonLoopback(t *testing.T) {
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

[durable_agents.control_plane]
enabled = true
listen = "0.0.0.0:8787"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() err = nil, want non-loopback plaintext validation error")
	}
	if !strings.Contains(err.Error(), "plaintext only on loopback") {
		t.Fatalf("Load() err = %v, want plaintext loopback validation", err)
	}
}

func TestLoadAllowsLoopbackPlaintextDurableAgentControlPlane(t *testing.T) {
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

[durable_agents.control_plane]
enabled = true
listen = "localhost:8787"
`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(configPath); err != nil {
		t.Fatalf("Load() err = %v, want loopback plaintext allowed", err)
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
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"
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
prompt_root = "./agent"
exec_root = "./workspace"
shared_memory_root = "./agent"
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
