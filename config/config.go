//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Identity  IdentityConfig  `toml:"identity"`
	Telegram  TelegramConfig  `toml:"telegram"`
	Providers ProvidersConfig `toml:"providers"`
	Sessions  SessionsConfig  `toml:"sessions"`
	Agent     AgentConfig     `toml:"agent"`
}

type IdentityConfig struct {
	UserAgent string `toml:"user_agent"`
}

type TelegramConfig struct {
	BotToken    string `toml:"bot_token"`
	PollTimeout int    `toml:"poll_timeout"`
}

type ProvidersConfig struct {
	Default   string          `toml:"default"`
	Anthropic AnthropicConfig `toml:"anthropic"`
}

type AnthropicConfig struct {
	APIKey    string `toml:"api_key"`
	Model     string `toml:"model"`
	MaxTokens int    `toml:"max_tokens"`
}

type SessionsConfig struct {
	DBPath string `toml:"db_path"`
}

type AgentConfig struct {
	Workspace              string   `toml:"workspace"`
	MaxIterations          int      `toml:"max_iterations"`
	ToolTimeout            int      `toml:"tool_timeout"`
	BootstrapFiles         []string `toml:"bootstrap_files"`
	DynamicFiles           []string `toml:"dynamic_files"`
	BootstrapMaxChars      int      `toml:"bootstrap_max_chars"`
	BootstrapTotalMaxChars int      `toml:"bootstrap_total_max_chars"`
	DailyNotes             bool     `toml:"daily_notes"`
	DailyNotesDir          string   `toml:"daily_notes_dir"`
}

func Default() Config {
	return Config{
		Telegram: TelegramConfig{
			PollTimeout: 30,
		},
		Providers: ProvidersConfig{
			Default: "anthropic",
			Anthropic: AnthropicConfig{
				Model:     "claude-sonnet-4-6",
				MaxTokens: 4096,
			},
		},
		Sessions: SessionsConfig{
			DBPath: "~/.config/aphelion/sessions.db",
		},
		Agent: AgentConfig{
			Workspace:     ".",
			MaxIterations: 50,
			ToolTimeout:   300,
			BootstrapFiles: []string{
				"SOUL.md",
				"IDENTITY.md",
				"USER.md",
				"AGENTS.md",
				"TOOLS.md",
				"BOOTSTRAP.md",
			},
			DynamicFiles:           []string{"MEMORY.md", "HEARTBEAT.md"},
			BootstrapMaxChars:      20000,
			BootstrapTotalMaxChars: 150000,
			DailyNotes:             true,
			DailyNotesDir:          "memory",
		},
	}
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		return nil, fmt.Errorf("decode toml: %w", err)
	}

	cfg.Sessions.DBPath, err = expandPath(cfg.Sessions.DBPath)
	if err != nil {
		return nil, fmt.Errorf("expand sessions.db_path: %w", err)
	}
	cfg.Agent.Workspace, err = expandPath(cfg.Agent.Workspace)
	if err != nil {
		return nil, fmt.Errorf("expand agent.workspace: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Telegram.BotToken) == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if strings.TrimSpace(cfg.Providers.Anthropic.APIKey) == "" {
		return fmt.Errorf("providers.anthropic.api_key is required")
	}
	if cfg.Telegram.PollTimeout <= 0 {
		return fmt.Errorf("telegram.poll_timeout must be > 0")
	}
	if cfg.Agent.MaxIterations <= 0 {
		return fmt.Errorf("agent.max_iterations must be > 0")
	}
	if cfg.Agent.ToolTimeout <= 0 {
		return fmt.Errorf("agent.tool_timeout must be > 0")
	}
	if cfg.Agent.BootstrapMaxChars <= 0 {
		return fmt.Errorf("agent.bootstrap_max_chars must be > 0")
	}
	if cfg.Agent.BootstrapTotalMaxChars <= 0 {
		return fmt.Errorf("agent.bootstrap_total_max_chars must be > 0")
	}
	if strings.TrimSpace(cfg.Sessions.DBPath) == "" {
		return fmt.Errorf("sessions.db_path is required")
	}
	if strings.TrimSpace(cfg.Agent.Workspace) == "" {
		return fmt.Errorf("agent.workspace is required")
	}
	if len(cfg.Agent.BootstrapFiles) == 0 {
		return fmt.Errorf("agent.bootstrap_files must not be empty")
	}
	if strings.TrimSpace(cfg.Agent.DailyNotesDir) == "" {
		return fmt.Errorf("agent.daily_notes_dir is required")
	}
	return nil
}

func expandPath(path string) (string, error) {
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
	}
	return filepath.Abs(path)
}
