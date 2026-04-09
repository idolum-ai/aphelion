//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Identity   IdentityConfig   `toml:"identity"`
	Telegram   TelegramConfig   `toml:"telegram"`
	Principals PrincipalsConfig `toml:"principals"`
	Providers  ProvidersConfig  `toml:"providers"`
	Sessions   SessionsConfig   `toml:"sessions"`
	Agent      AgentConfig      `toml:"agent"`
}

type IdentityConfig struct {
	UserAgent string `toml:"user_agent"`
}

type TelegramConfig struct {
	BotToken    string `toml:"bot_token"`
	PollTimeout int    `toml:"poll_timeout"`
}

type PrincipalsConfig struct {
	Telegram TelegramPrincipalsConfig `toml:"telegram"`
}

type TelegramPrincipalsConfig struct {
	AdminUserIDs    []int64 `toml:"admin_user_ids"`
	ApprovedUserIDs []int64 `toml:"approved_user_ids"`
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
	DBPath     string `toml:"db_path"`
	IdleExpiry string `toml:"idle_expiry"`
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
			DBPath:     "~/.config/aphelion/sessions.db",
			IdleExpiry: "24h",
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
	if strings.TrimSpace(cfg.Sessions.IdleExpiry) == "" {
		return fmt.Errorf("sessions.idle_expiry is required")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.Sessions.IdleExpiry)); err != nil {
		return fmt.Errorf("sessions.idle_expiry must be a valid duration: %w", err)
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
	if len(cfg.Principals.Telegram.AdminUserIDs) == 0 {
		return fmt.Errorf("principals.telegram.admin_user_ids must contain at least one user id")
	}

	admin := make(map[int64]struct{}, len(cfg.Principals.Telegram.AdminUserIDs))
	for _, id := range cfg.Principals.Telegram.AdminUserIDs {
		if id <= 0 {
			return fmt.Errorf("principals.telegram.admin_user_ids must contain positive user ids")
		}
		if _, exists := admin[id]; exists {
			return fmt.Errorf("principals.telegram.admin_user_ids contains duplicate user id %d", id)
		}
		admin[id] = struct{}{}
	}

	approved := make(map[int64]struct{}, len(cfg.Principals.Telegram.ApprovedUserIDs))
	for _, id := range cfg.Principals.Telegram.ApprovedUserIDs {
		if id <= 0 {
			return fmt.Errorf("principals.telegram.approved_user_ids must contain positive user ids")
		}
		if _, exists := approved[id]; exists {
			return fmt.Errorf("principals.telegram.approved_user_ids contains duplicate user id %d", id)
		}
		if _, exists := admin[id]; exists {
			return fmt.Errorf("principals.telegram user id %d cannot be both admin and approved_user", id)
		}
		approved[id] = struct{}{}
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
