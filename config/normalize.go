//go:build linux

package config

import "strings"

func normalizeSandboxProfileConfig(profile SandboxProfileConfig) SandboxProfileConfig {
	profile.Mode = strings.ToLower(strings.TrimSpace(profile.Mode))
	profile.Network = strings.ToLower(strings.TrimSpace(profile.Network))
	profile.WritablePaths = normalizeStringList(profile.WritablePaths)
	profile.ReadonlyPaths = normalizeStringList(profile.ReadonlyPaths)
	profile.HiddenPaths = normalizeStringList(profile.HiddenPaths)
	profile.NetworkAllow = normalizeStringList(profile.NetworkAllow)
	return profile
}

func normalizeAgentRoots(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.Agent.PromptRoot = cfg.Agent.EffectivePromptRoot()
	cfg.Agent.ExecRoot = cfg.Agent.EffectiveExecRoot()
	cfg.Agent.SharedMemoryRoot = cfg.Agent.EffectiveSharedMemoryRoot()
	cfg.Agent.UserWorkspaceRoot = cfg.Agent.EffectiveUserWorkspaceRoot()
	cfg.Agent.UserMemoryRoot = cfg.Agent.EffectiveUserMemoryRoot()
}

func normalizeTelegramDurableGroups(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Telegram.DurableGroups {
		cfg.Telegram.DurableGroups[i].AgentID = strings.TrimSpace(cfg.Telegram.DurableGroups[i].AgentID)
		cfg.Telegram.DurableGroups[i].Charter = strings.TrimSpace(cfg.Telegram.DurableGroups[i].Charter)
		cfg.Telegram.DurableGroups[i].RespondOn = normalizeTelegramDurableGroupRespondOn(cfg.Telegram.DurableGroups[i].RespondOn)
		cfg.Telegram.DurableGroups[i].LLMBackend = normalizeTelegramDurableGroupLLMBackend(
			cfg.Telegram.DurableGroups[i].LLMBackend,
			cfg.Telegram.DurableGroups[i].LLMProvider,
			cfg.Telegram.DurableGroups[i].LLMAPIKey,
			cfg.Telegram.DurableGroups[i].LLMBaseURL,
			cfg.Telegram.DurableGroups[i].LLMModel,
			cfg.Telegram.DurableGroups[i].LLMMaxTokens,
			cfg.Telegram.DurableGroups[i].LLMCodexAuthSource,
			cfg.Telegram.DurableGroups[i].LLMCodexHome,
			cfg.Telegram.DurableGroups[i].LLMCodexBaseURL,
		)
		cfg.Telegram.DurableGroups[i].LLMProvider = strings.ToLower(strings.TrimSpace(cfg.Telegram.DurableGroups[i].LLMProvider))
		cfg.Telegram.DurableGroups[i].LLMAPIKey = strings.TrimSpace(cfg.Telegram.DurableGroups[i].LLMAPIKey)
		cfg.Telegram.DurableGroups[i].LLMBaseURL = strings.TrimSpace(cfg.Telegram.DurableGroups[i].LLMBaseURL)
		cfg.Telegram.DurableGroups[i].LLMModel = strings.TrimSpace(cfg.Telegram.DurableGroups[i].LLMModel)
		cfg.Telegram.DurableGroups[i].LLMCodexAuthSource = normalizeTelegramDurableGroupCodexAuthSource(cfg.Telegram.DurableGroups[i].LLMCodexAuthSource)
		cfg.Telegram.DurableGroups[i].LLMCodexHome = strings.TrimSpace(cfg.Telegram.DurableGroups[i].LLMCodexHome)
		cfg.Telegram.DurableGroups[i].LLMCodexBaseURL = strings.TrimSpace(cfg.Telegram.DurableGroups[i].LLMCodexBaseURL)
		if cfg.Telegram.DurableGroups[i].LLMMaxTokens < 0 {
			cfg.Telegram.DurableGroups[i].LLMMaxTokens = 0
		}
	}
}

func normalizeTelegramChildBots(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Telegram.ChildBots {
		cfg.Telegram.ChildBots[i].AgentID = strings.TrimSpace(cfg.Telegram.ChildBots[i].AgentID)
		cfg.Telegram.ChildBots[i].TokenFile = strings.TrimSpace(cfg.Telegram.ChildBots[i].TokenFile)
		cfg.Telegram.ChildBots[i].RespondOn = normalizeTelegramDurableGroupRespondOn(cfg.Telegram.ChildBots[i].RespondOn)
	}
}

func normalizeGitHubConfig(cfg *Config) {
	if cfg == nil {
		return
	}
	cfg.GitHub.APIBaseURL = strings.TrimRight(strings.TrimSpace(cfg.GitHub.APIBaseURL), "/")
	cfg.GitHub.APIVersion = strings.TrimSpace(cfg.GitHub.APIVersion)
	for i := range cfg.GitHub.Apps {
		app := &cfg.GitHub.Apps[i]
		app.Name = strings.TrimSpace(app.Name)
		app.PrivateKeyFile = strings.TrimSpace(app.PrivateKeyFile)
		app.Repositories = normalizeStringList(app.Repositories)
		app.Permissions = normalizeGitHubPermissionList(app.Permissions)
	}
}

func addGitHubSecretHiddenPaths(cfg *Config) {
	if cfg == nil {
		return
	}
	var paths []string
	for _, app := range cfg.GitHub.Apps {
		if path := strings.TrimSpace(app.PrivateKeyFile); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return
	}
	cfg.Sandbox.Profiles.ApprovedUser.HiddenPaths = normalizeStringList(append(cfg.Sandbox.Profiles.ApprovedUser.HiddenPaths, paths...))
	cfg.Sandbox.Profiles.DurableAgent.HiddenPaths = normalizeStringList(append(cfg.Sandbox.Profiles.DurableAgent.HiddenPaths, paths...))
}

func normalizeGitHubPermissionList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		name, level, ok := splitGitHubPermission(value)
		if !ok {
			value = strings.ToLower(strings.TrimSpace(value))
		} else {
			value = name + ":" + level
		}
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func splitGitHubPermission(raw string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return "", "", false
	}
	name := strings.ToLower(strings.TrimSpace(parts[0]))
	level := strings.ToLower(strings.TrimSpace(parts[1]))
	if name == "" || level == "" {
		return "", "", false
	}
	return name, level, true
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeTelegramDurableGroupRespondOn(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "mentions":
		return "mentions"
	case "all":
		return "all"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func normalizeTelegramDurableGroupLLMBackend(backend string, provider string, apiKey string, baseURL string, model string, maxTokens int, codexAuthSource string, codexHome string, codexBaseURL string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "native", "codex":
		return strings.ToLower(strings.TrimSpace(backend))
	}
	hasCodexFields := strings.TrimSpace(codexAuthSource) != "" || strings.TrimSpace(codexHome) != "" || strings.TrimSpace(codexBaseURL) != ""
	if hasCodexFields {
		return "codex"
	}
	hasNativeFields := strings.TrimSpace(provider) != "" ||
		strings.TrimSpace(apiKey) != "" ||
		strings.TrimSpace(baseURL) != "" ||
		strings.TrimSpace(model) != "" ||
		maxTokens > 0
	if hasNativeFields {
		return "native"
	}
	return ""
}

func normalizeTelegramDurableGroupCodexAuthSource(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "codex_cli":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
