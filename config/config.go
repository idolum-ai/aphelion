//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Identity   IdentityConfig   `toml:"identity"`
	Telegram   TelegramConfig   `toml:"telegram"`
	Principals PrincipalsConfig `toml:"principals"`
	Governor   GovernorConfig   `toml:"governor"`
	Providers  ProvidersConfig  `toml:"providers"`
	Sessions   SessionsConfig   `toml:"sessions"`
	Agent      AgentConfig      `toml:"agent"`
	Face       FaceConfig       `toml:"face"`
	Heartbeat  HeartbeatConfig  `toml:"heartbeat"`
	Cron       CronConfig       `toml:"cron"`
	Voice      VoiceConfig      `toml:"voice"`
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

type GovernorConfig struct {
	Backend        string              `toml:"backend"`
	NativeProvider string              `toml:"native_provider"`
	Codex          GovernorCodexConfig `toml:"codex"`
}

type GovernorCodexConfig struct {
	AuthSource string `toml:"auth_source"`
	CodexHome  string `toml:"codex_home"`
	BaseURL    string `toml:"base_url"`
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

type FaceConfig struct {
	Backend string `toml:"backend"`
}

type HeartbeatConfig struct {
	Enabled     bool                      `toml:"enabled"`
	Every       string                    `toml:"every"`
	Target      string                    `toml:"target"`
	ActiveHours HeartbeatActiveHoursConfig `toml:"active_hours"`
}

type HeartbeatActiveHoursConfig struct {
	Start    string `toml:"start"`
	End      string `toml:"end"`
	Timezone string `toml:"timezone"`
}

type CronConfig struct {
	Enabled bool            `toml:"enabled"`
	Jobs    []CronJobConfig `toml:"jobs"`
}

type CronJobConfig struct {
	ID       string `toml:"id"`
	Every    string `toml:"every"`
	Prompt   string `toml:"prompt"`
	Delivery string `toml:"delivery"`
	Enabled  bool   `toml:"enabled"`
}

type VoiceConfig struct {
	Mode               string `toml:"mode"`
	OpenAIAPIKey       string `toml:"openai_api_key"`
	OpenAIBaseURL      string `toml:"openai_base_url"`
	OpenAIModel        string `toml:"openai_model"`
	ElevenLabsAPIKey   string `toml:"elevenlabs_api_key"`
	ElevenLabsBaseURL  string `toml:"elevenlabs_base_url"`
	ElevenLabsVoiceID  string `toml:"elevenlabs_voice_id"`
	ElevenLabsModelID  string `toml:"elevenlabs_model_id"`
}

func Default() Config {
	return Config{
		Telegram: TelegramConfig{
			PollTimeout: 30,
		},
		Governor: GovernorConfig{
			Backend:        "auto",
			NativeProvider: "anthropic",
			Codex: GovernorCodexConfig{
				AuthSource: "auto",
				BaseURL:    "https://chatgpt.com/backend-api/codex",
			},
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
		Face: FaceConfig{
			Backend: "provider",
		},
		Heartbeat: HeartbeatConfig{
			Enabled: false,
			Every:   "30m",
			Target:  "last",
		},
		Cron: CronConfig{
			Enabled: false,
		},
		Voice: VoiceConfig{
			Mode:              "off",
			OpenAIModel:       "whisper-1",
			ElevenLabsModelID: "eleven_multilingual_v2",
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
	switch strings.ToLower(strings.TrimSpace(cfg.Governor.Backend)) {
	case "auto", "codex", "native":
	default:
		return fmt.Errorf("governor.backend must be one of auto|codex|native")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Governor.Codex.AuthSource)) {
	case "auto", "codex_cli", "aphelion":
	default:
		return fmt.Errorf("governor.codex.auth_source must be one of auto|codex_cli|aphelion")
	}
	if strings.TrimSpace(cfg.Governor.NativeProvider) == "" {
		return fmt.Errorf("governor.native_provider is required")
	}
	if strings.TrimSpace(cfg.Governor.Codex.BaseURL) == "" {
		return fmt.Errorf("governor.codex.base_url is required")
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
	switch strings.ToLower(strings.TrimSpace(cfg.Face.Backend)) {
	case "", "provider", "governor_passthrough":
	default:
		return fmt.Errorf("face.backend must be one of provider|governor_passthrough")
	}
	if strings.TrimSpace(cfg.Heartbeat.Every) == "" {
		return fmt.Errorf("heartbeat.every is required")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.Heartbeat.Every)); err != nil {
		return fmt.Errorf("heartbeat.every must be a valid duration: %w", err)
	}
	switch target := strings.TrimSpace(cfg.Heartbeat.Target); target {
	case "", "none", "last":
	default:
		if _, err := parsePositiveInt64(target); err != nil {
			return fmt.Errorf("heartbeat.target must be one of none|last|<admin_chat_id>")
		}
	}
	if _, err := validateClock(cfg.Heartbeat.ActiveHours.Start, "heartbeat.active_hours.start"); err != nil {
		return err
	}
	if _, err := validateClock(cfg.Heartbeat.ActiveHours.End, "heartbeat.active_hours.end"); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Heartbeat.ActiveHours.Timezone) != "" {
		if _, err := time.LoadLocation(strings.TrimSpace(cfg.Heartbeat.ActiveHours.Timezone)); err != nil {
			return fmt.Errorf("heartbeat.active_hours.timezone must be a valid IANA timezone: %w", err)
		}
	}
	for i, job := range cfg.Cron.Jobs {
		if strings.TrimSpace(job.ID) == "" {
			return fmt.Errorf("cron.jobs[%d].id is required", i)
		}
		if strings.TrimSpace(job.Every) == "" {
			return fmt.Errorf("cron.jobs[%d].every is required", i)
		}
		if _, err := time.ParseDuration(strings.TrimSpace(job.Every)); err != nil {
			return fmt.Errorf("cron.jobs[%d].every must be a valid duration: %w", i, err)
		}
		if strings.TrimSpace(job.Prompt) == "" {
			return fmt.Errorf("cron.jobs[%d].prompt is required", i)
		}
		switch strings.ToLower(strings.TrimSpace(job.Delivery)) {
		case "", "none", "announce":
		default:
			return fmt.Errorf("cron.jobs[%d].delivery must be one of none|announce", i)
		}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Voice.Mode)) {
	case "", "off", "voice_only", "all":
	default:
		return fmt.Errorf("voice.mode must be one of off|voice_only|all")
	}
	if strings.TrimSpace(cfg.Voice.Mode) != "" && !strings.EqualFold(strings.TrimSpace(cfg.Voice.Mode), "off") {
		if strings.TrimSpace(cfg.Voice.OpenAIAPIKey) == "" {
			return fmt.Errorf("voice.openai_api_key is required when voice.mode is enabled")
		}
		if strings.TrimSpace(cfg.Voice.OpenAIModel) == "" {
			return fmt.Errorf("voice.openai_model is required when voice.mode is enabled")
		}
		if strings.TrimSpace(cfg.Voice.ElevenLabsAPIKey) == "" {
			return fmt.Errorf("voice.elevenlabs_api_key is required when voice.mode is enabled")
		}
		if strings.TrimSpace(cfg.Voice.ElevenLabsVoiceID) == "" {
			return fmt.Errorf("voice.elevenlabs_voice_id is required when voice.mode is enabled")
		}
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

func validateClock(raw string, field string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	if _, err := time.Parse("15:04", trimmed); err != nil {
		return "", fmt.Errorf("%s must be in HH:MM format: %w", field, err)
	}
	return trimmed, nil
}

func parsePositiveInt64(raw string) (int64, error) {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return value, nil
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
