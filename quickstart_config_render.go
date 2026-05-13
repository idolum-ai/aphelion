//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/config"
)

type quickstartConfigValues struct {
	TelegramBotToken string
	AdminUserID      int64
	Provider         string
	ProviderAPIKey   string
	ProviderModel    string
}

func quickstartHasConfigInputs(opts quickstartOptions) bool {
	return strings.TrimSpace(opts.TelegramBotToken) != "" ||
		opts.AdminUserID > 0 ||
		opts.DetectAdmin ||
		strings.TrimSpace(opts.Provider) != "" ||
		strings.TrimSpace(opts.ProviderAPIKey) != "" ||
		strings.TrimSpace(opts.ProviderModel) != ""
}

func normalizeQuickstartOptions(opts quickstartOptions) quickstartOptions {
	if opts.In == nil {
		opts.In = os.Stdin
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.DetectAdminTimeout <= 0 {
		opts.DetectAdminTimeout = defaultQuickstartDetectAdminTimeout
	}
	if opts.NewTelegramClient == nil {
		opts.NewTelegramClient = defaultQuickstartOptions().NewTelegramClient
	}
	if opts.CommandRunner == nil {
		opts.CommandRunner = execQuickstartCommand
	}
	return opts
}

func renderQuickstartConfig(values quickstartConfigValues) string {
	provider := normalizeQuickstartProvider(values.Provider)
	var b strings.Builder
	fmt.Fprintf(&b, "[telegram]\n")
	fmt.Fprintf(&b, "bot_token = %s\n\n", strconv.Quote(strings.TrimSpace(values.TelegramBotToken)))
	fmt.Fprintf(&b, "[principals.telegram]\n")
	fmt.Fprintf(&b, "admin_user_ids = [%d]\n\n", values.AdminUserID)
	fmt.Fprintf(&b, "[autonomy]\n")
	fmt.Fprintf(&b, "default_mode = \"ask_first\"\n")
	fmt.Fprintf(&b, "ceiling = \"leased\"\n")
	fmt.Fprintf(&b, "allow_live_overrides = true\n")
	fmt.Fprintf(&b, "max_override_duration = \"4h\"\n\n")
	fmt.Fprintf(&b, "[providers]\n")
	fmt.Fprintf(&b, "selection = \"manual\"\n")
	fmt.Fprintf(&b, "auto_order = [%s]\n", strconv.Quote(provider))
	fmt.Fprintf(&b, "default = %s\n", strconv.Quote(provider))
	fmt.Fprintf(&b, "fallback_chain = []\n\n")
	switch provider {
	case "anthropic":
		fmt.Fprintf(&b, "[providers.anthropic]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "openai":
		fmt.Fprintf(&b, "[providers.openai]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "openrouter":
		fmt.Fprintf(&b, "[providers.openrouter]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "gemini":
		fmt.Fprintf(&b, "[providers.gemini]\n")
		fmt.Fprintf(&b, "api_key = %s\n", strconv.Quote(strings.TrimSpace(values.ProviderAPIKey)))
	case "ollama":
		fmt.Fprintf(&b, "[providers.ollama]\n")
	}
	if model := strings.TrimSpace(values.ProviderModel); model != "" {
		fmt.Fprintf(&b, "model = %s\n", strconv.Quote(model))
	}
	return b.String()
}

func writeValidatedQuickstartConfig(path string, raw string, force bool) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("config path is required")
	}
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config %s already exists; pass --force to overwrite it", path)
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("stat config %s: %w", path, err)
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".aphelion-quickstart-*.toml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temporary config: %w", err)
	}
	if _, err := tmp.WriteString(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if _, err := config.Load(tmpPath); err != nil {
		return fmt.Errorf("generated config did not validate: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install config %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod config %s: %w", path, err)
	}
	return nil
}

func inferQuickstartProviderFromEnv(getenv func(string) string) string {
	for _, entry := range []struct {
		Provider string
		EnvNames []string
	}{
		{Provider: "openai", EnvNames: []string{"OPENAI_API_KEY", "APHELION_OPENAI_API_KEY"}},
		{Provider: "anthropic", EnvNames: []string{"ANTHROPIC_API_KEY", "APHELION_ANTHROPIC_API_KEY"}},
		{Provider: "openrouter", EnvNames: []string{"OPENROUTER_API_KEY", "APHELION_OPENROUTER_API_KEY"}},
		{Provider: "gemini", EnvNames: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "APHELION_GEMINI_API_KEY"}},
	} {
		for _, name := range entry.EnvNames {
			if strings.TrimSpace(getenv(name)) != "" {
				return entry.Provider
			}
		}
	}
	return ""
}

func providerAPIKeyEnvNames(provider string) []string {
	envs := []string{"APHELION_PROVIDER_API_KEY"}
	switch normalizeQuickstartProvider(provider) {
	case "anthropic":
		return append(envs, "ANTHROPIC_API_KEY", "APHELION_ANTHROPIC_API_KEY")
	case "openai":
		return append(envs, "OPENAI_API_KEY", "APHELION_OPENAI_API_KEY")
	case "openrouter":
		return append(envs, "OPENROUTER_API_KEY", "APHELION_OPENROUTER_API_KEY")
	case "gemini":
		return append(envs, "GEMINI_API_KEY", "GOOGLE_API_KEY", "APHELION_GEMINI_API_KEY")
	default:
		return envs
	}
}

func providerPrompt(provider string) string {
	switch normalizeQuickstartProvider(provider) {
	case "anthropic":
		return "Anthropic API key: "
	case "openai":
		return "OpenAI API key: "
	case "openrouter":
		return "OpenRouter API key: "
	case "gemini":
		return "Gemini API key: "
	default:
		return provider + " API key: "
	}
}

func providerRequiresAPIKey(provider string) bool {
	switch normalizeQuickstartProvider(provider) {
	case "anthropic", "openai", "openrouter", "gemini":
		return true
	default:
		return false
	}
}

func normalizeQuickstartProvider(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.ReplaceAll(name, "-", "")
	name = strings.ReplaceAll(name, "_", "")
	switch name {
	case "":
		return ""
	case "anthropic", "claude":
		return "anthropic"
	case "openai":
		return "openai"
	case "openrouter":
		return "openrouter"
	case "gemini", "google":
		return "gemini"
	case "ollama", "local":
		return "ollama"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func isQuickstartProvider(provider string) bool {
	switch normalizeQuickstartProvider(provider) {
	case "anthropic", "openai", "openrouter", "gemini", "ollama":
		return true
	default:
		return false
	}
}
