//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

func DefaultConfigPath() string {
	return defaultHomePath(".aphelion", "aphelion.toml")
}

func ResolveConfigPath(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return expandPath(override)
	}
	if envPath := strings.TrimSpace(os.Getenv("APHELION_CONFIG")); envPath != "" {
		return expandPath(envPath)
	}

	primary := DefaultConfigPath()
	if fileExists(primary) {
		return primary, nil
	}
	return primary, nil
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	baseDir := filepath.Dir(path)

	cfg := Default()
	md, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return nil, fmt.Errorf("decode toml: %w", err)
	}
	cfg.warnings = configWarningsFromMetadata(md)
	if md.IsDefined("agent", "workspace") {
		return nil, fmt.Errorf("agent.workspace has been removed; set agent.prompt_root, agent.exec_root, and agent.shared_memory_root explicitly")
	}
	for _, removed := range [][]string{{"recovery", "watchdog", "restart_cooldown"}, {"recovery", "watchdog", "max_restart_attempts"}} {
		if md.IsDefined(removed...) {
			return nil, fmt.Errorf("%s has been removed; stale turn recovery now interrupts scoped turns instead of restarting the service", strings.Join(removed, "."))
		}
	}

	cfg.Providers.Selection = normalizeProviderSelection(cfg.Providers.Selection)
	cfg.Providers.AutoOrder = normalizeProviderNameList(cfg.Providers.AutoOrder)
	if len(cfg.Providers.AutoOrder) == 0 {
		cfg.Providers.AutoOrder = []string{"openai", "anthropic", "openrouter"}
	}
	cfg.Providers.Anthropic.CacheStrategy = normalizeAnthropicCacheStrategy(cfg.Providers.Anthropic.CacheStrategy)
	cfg.Providers.Anthropic.CacheTTL = normalizeAnthropicCacheTTL(cfg.Providers.Anthropic.CacheTTL)
	cfg.Providers.OpenAI.FallbackModels = normalizeOpenAIModelFallbacks(cfg.Providers.OpenAI.Model, cfg.Providers.OpenAI.FallbackModels)
	applyProviderSelectionHeuristic(&cfg, md)
	cfg.Work.Executor = normalizeWorkExecutor(cfg.Work.Executor)
	cfg.Work.AutoOrder = normalizeWorkExecutorList(cfg.Work.AutoOrder)
	if len(cfg.Work.AutoOrder) == 0 {
		cfg.Work.AutoOrder = []string{"native", "codex"}
	}
	cfg.Work.Codex.AppServerAddress = strings.TrimSpace(cfg.Work.Codex.AppServerAddress)
	cfg.Autonomy.DefaultMode = NormalizeAutonomyMode(cfg.Autonomy.DefaultMode)
	cfg.Autonomy.Ceiling = NormalizeAutonomyMode(cfg.Autonomy.Ceiling)
	if strings.TrimSpace(cfg.Autonomy.MaxOverrideDuration) == "" {
		cfg.Autonomy.MaxOverrideDuration = "4h"
	}
	cfg.Sandbox.Profiles.Admin = normalizeSandboxProfileConfig(cfg.Sandbox.Profiles.Admin)
	cfg.Sandbox.Profiles.ApprovedUser = normalizeSandboxProfileConfig(cfg.Sandbox.Profiles.ApprovedUser)
	cfg.Sandbox.Profiles.DurableAgent = normalizeSandboxProfileConfig(cfg.Sandbox.Profiles.DurableAgent)

	cfg.Sessions.DBPath, err = expandConfiguredPath(cfg.Sessions.DBPath, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand sessions.db_path: %w", err)
	}
	cfg.Sessions.TESRetention.ExportDir, err = expandConfiguredPath(cfg.Sessions.TESRetention.ExportDir, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand sessions.tes_retention.export_dir: %w", err)
	}
	cfg.Agent.PromptRoot, err = expandConfiguredPath(cfg.Agent.PromptRoot, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand agent.prompt_root: %w", err)
	}
	cfg.Agent.ExecRoot, err = expandConfiguredPath(cfg.Agent.ExecRoot, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand agent.exec_root: %w", err)
	}
	cfg.Agent.SharedMemoryRoot, err = expandConfiguredPath(cfg.Agent.SharedMemoryRoot, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand agent.shared_memory_root: %w", err)
	}
	cfg.Agent.UserWorkspaceRoot, err = expandConfiguredPath(cfg.Agent.UserWorkspaceRoot, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand agent.user_workspace_root: %w", err)
	}
	cfg.Agent.UserMemoryRoot, err = expandConfiguredPath(cfg.Agent.UserMemoryRoot, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand agent.user_memory_root: %w", err)
	}
	cfg.Tools.ExternalManifestDir, err = expandConfiguredPath(cfg.Tools.ExternalManifestDir, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand tools.external_manifest_dir: %w", err)
	}
	cfg.Tailscale.Parent.StateDir, err = expandConfiguredPath(cfg.Tailscale.Parent.StateDir, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand tailscale.parent.state_dir: %w", err)
	}
	cfg.Tailscale.Parent.AuthKeyFile, err = expandConfiguredPath(cfg.Tailscale.Parent.AuthKeyFile, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand tailscale.parent.auth_key_file: %w", err)
	}
	if strings.TrimSpace(cfg.GitHub.APIBaseURL) == "" {
		cfg.GitHub.APIBaseURL = "https://api.github.com"
	}
	if strings.TrimSpace(cfg.GitHub.APIVersion) == "" {
		cfg.GitHub.APIVersion = "2026-03-10"
	}
	for i := range cfg.GitHub.Apps {
		cfg.GitHub.Apps[i].PrivateKeyFile, err = expandConfiguredPath(cfg.GitHub.Apps[i].PrivateKeyFile, baseDir)
		if err != nil {
			return nil, fmt.Errorf("expand github.apps[%d].private_key_file: %w", i, err)
		}
	}
	for i := range cfg.Telegram.ChildBots {
		cfg.Telegram.ChildBots[i].TokenFile, err = expandConfiguredPath(cfg.Telegram.ChildBots[i].TokenFile, baseDir)
		if err != nil {
			return nil, fmt.Errorf("expand telegram.child_bots[%d].token_file: %w", i, err)
		}
	}
	normalizeAgentRoots(&cfg)
	normalizeGitHubConfig(&cfg)
	addGitHubSecretHiddenPaths(&cfg)
	cfg.Face.Backend = NormalizeFaceBackendValue(cfg.Face.Backend)
	normalizeTelegramDurableGroups(&cfg)
	normalizeTelegramChildBots(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (cfg *Config) Warnings() []ConfigWarning {
	if cfg == nil || len(cfg.warnings) == 0 {
		return nil
	}
	return append([]ConfigWarning(nil), cfg.warnings...)
}

func (cfg *Config) WarningSummary() string {
	warnings := cfg.Warnings()
	if len(warnings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		path := strings.TrimSpace(warning.Path)
		message := strings.TrimSpace(warning.Message)
		switch {
		case path != "" && message != "":
			parts = append(parts, path+": "+message)
		case path != "":
			parts = append(parts, path)
		case message != "":
			parts = append(parts, message)
		}
	}
	return strings.Join(parts, "; ")
}

func configWarningsFromMetadata(md toml.MetaData) []ConfigWarning {
	undecoded := md.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	paths := make([]string, 0, len(undecoded))
	for _, key := range undecoded {
		path := strings.TrimSpace(key.String())
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	out := make([]ConfigWarning, 0, len(paths))
	for _, path := range paths {
		out = append(out, ConfigWarning{
			Path:    path,
			Message: "ignored by this build; config remains valid but this key has no runtime effect",
		})
	}
	return out
}

func expandPath(path string) (string, error) {
	return expandConfiguredPath(path, "")
}

func expandConfiguredPath(path string, baseDir string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	} else if !filepath.IsAbs(path) && strings.TrimSpace(baseDir) != "" {
		path = filepath.Join(baseDir, path)
	}
	return filepath.Abs(path)
}

func defaultHomePath(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(parts...)
	}
	return filepath.Join(append([]string{home}, parts...)...)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
