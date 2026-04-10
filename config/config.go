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
	OpenAI     OpenAIConfig     `toml:"openai"`
	Sessions   SessionsConfig   `toml:"sessions"`
	Agent      AgentConfig      `toml:"agent"`
	Memory     MemoryConfig     `toml:"memory"`
	Thinking   ThinkingConfig   `toml:"thinking"`
	Face       FaceConfig       `toml:"face"`
	Heartbeat  HeartbeatConfig  `toml:"heartbeat"`
	Cron       CronConfig       `toml:"cron"`
	Voice      VoiceConfig      `toml:"voice"`
}

type IdentityConfig struct {
	UserAgent string `toml:"user_agent"`
}

type TelegramConfig struct {
	BotToken            string `toml:"bot_token"`
	PollTimeout         int    `toml:"poll_timeout"`
	StreamEditInterval  string `toml:"stream_edit_interval"`
	StreamCursor        string `toml:"stream_cursor"`
	ToolProgress        string `toml:"tool_progress"`
	ToolProgressStyle   string `toml:"tool_progress_style"`
	ToolProgressWindow  int    `toml:"tool_progress_window"`
	ToolProgressCleanup bool   `toml:"tool_progress_cleanup"`
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
	AuthSource    string `toml:"auth_source"`
	CodexHome     string `toml:"codex_home"`
	BaseURL       string `toml:"base_url"`
	ContextWindow int    `toml:"context_window"`
}

type ProvidersConfig struct {
	Default       string               `toml:"default"`
	FallbackChain []string             `toml:"fallback_chain"`
	Anthropic     AnthropicConfig      `toml:"anthropic"`
	OpenAI        OpenAIProviderConfig `toml:"openai"`
	OpenRouter    OpenRouterConfig     `toml:"openrouter"`
}

type AnthropicConfig struct {
	APIKey        string `toml:"api_key"`
	Model         string `toml:"model"`
	MaxTokens     int    `toml:"max_tokens"`
	ContextWindow int    `toml:"context_window"`
}

type OpenRouterConfig struct {
	APIKey        string `toml:"api_key"`
	BaseURL       string `toml:"base_url"`
	Model         string `toml:"model"`
	MaxTokens     int    `toml:"max_tokens"`
	ContextWindow int    `toml:"context_window"`
}

type OpenAIProviderConfig struct {
	APIKey        string `toml:"api_key"`
	BaseURL       string `toml:"base_url"`
	Model         string `toml:"model"`
	MaxTokens     int    `toml:"max_tokens"`
	ContextWindow int    `toml:"context_window"`
}

type OpenAIConfig struct {
	Files        OpenAIFilesConfig        `toml:"files"`
	VectorStores OpenAIVectorStoresConfig `toml:"vector_stores"`
}

type OpenAIFilesConfig struct {
	Enabled bool   `toml:"enabled"`
	Purpose string `toml:"purpose"`
}

type OpenAIVectorStoresConfig struct {
	Enabled      bool   `toml:"enabled"`
	DefaultStore string `toml:"default_store"`
}

type SessionsConfig struct {
	DBPath             string  `toml:"db_path"`
	IdleExpiry         string  `toml:"idle_expiry"`
	MaxContextRatio    float64 `toml:"max_context_ratio"`
	CompactionRatio    float64 `toml:"compaction_ratio"`
	CompactionStrategy string  `toml:"compaction_strategy"`
}

type AgentConfig struct {
	Workspace              string   `toml:"workspace"`
	PromptRoot             string   `toml:"prompt_root"`
	ExecRoot               string   `toml:"exec_root"`
	SharedMemoryRoot       string   `toml:"shared_memory_root"`
	UserWorkspaceRoot      string   `toml:"user_workspace_root"`
	UserMemoryRoot         string   `toml:"user_memory_root"`
	MaxIterations          int      `toml:"max_iterations"`
	ToolTimeout            int      `toml:"tool_timeout"`
	BootstrapFiles         []string `toml:"bootstrap_files"`
	DynamicFiles           []string `toml:"dynamic_files"`
	BootstrapMaxChars      int      `toml:"bootstrap_max_chars"`
	BootstrapTotalMaxChars int      `toml:"bootstrap_total_max_chars"`
	DailyNotes             bool     `toml:"daily_notes"`
	DailyNotesDir          string   `toml:"daily_notes_dir"`
}

type MemoryConfig struct {
	SessionSearch    bool                   `toml:"session_search"`
	SemanticIndexing bool                   `toml:"semantic_indexing"`
	Reflection       MemoryReflectionConfig `toml:"reflection"`
	Decay            MemoryDecayConfig      `toml:"decay"`
	Identity         MemoryIdentityConfig   `toml:"identity"`
}

type MemoryReflectionConfig struct {
	Enabled bool   `toml:"enabled"`
	Every   string `toml:"every"`
}

type MemoryDecayConfig struct {
	Enabled  bool `toml:"enabled"`
	HotDays  int  `toml:"hot_days"`
	WarmDays int  `toml:"warm_days"`
	ColdDays int  `toml:"cold_days"`
}

type MemoryIdentityConfig struct {
	Preserve []string `toml:"preserve"`
}

type ThinkingConfig struct {
	Effort   string                 `toml:"effort"`
	Summary  string                 `toml:"summary"`
	Defaults ThinkingDefaultsConfig `toml:"defaults"`
}

type ThinkingDefaultsConfig struct {
	Default   string `toml:"default"`
	Heartbeat string `toml:"heartbeat"`
	Cron      string `toml:"cron"`
	Recovery  string `toml:"recovery"`
}

type FaceConfig struct {
	Backend string `toml:"backend"`
}

type HeartbeatConfig struct {
	Enabled     bool                       `toml:"enabled"`
	Every       string                     `toml:"every"`
	Target      string                     `toml:"target"`
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
	Mode              string `toml:"mode"`
	OpenAIAPIKey      string `toml:"openai_api_key"`
	OpenAIBaseURL     string `toml:"openai_base_url"`
	OpenAIModel       string `toml:"openai_model"`
	ElevenLabsAPIKey  string `toml:"elevenlabs_api_key"`
	ElevenLabsBaseURL string `toml:"elevenlabs_base_url"`
	ElevenLabsVoiceID string `toml:"elevenlabs_voice_id"`
	ElevenLabsModelID string `toml:"elevenlabs_model_id"`
}

func (a AgentConfig) EffectivePromptRoot() string {
	return firstNonEmpty(strings.TrimSpace(a.PromptRoot), strings.TrimSpace(a.Workspace))
}

func (a AgentConfig) EffectiveExecRoot() string {
	return firstNonEmpty(strings.TrimSpace(a.ExecRoot), strings.TrimSpace(a.Workspace), strings.TrimSpace(a.PromptRoot))
}

func (a AgentConfig) EffectiveSharedMemoryRoot() string {
	return firstNonEmpty(strings.TrimSpace(a.SharedMemoryRoot), strings.TrimSpace(a.PromptRoot), strings.TrimSpace(a.Workspace))
}

func (a AgentConfig) EffectiveUserWorkspaceRoot() string {
	return strings.TrimSpace(a.UserWorkspaceRoot)
}

func (a AgentConfig) EffectiveUserMemoryRoot() string {
	return strings.TrimSpace(a.UserMemoryRoot)
}

func Default() Config {
	return Config{
		Telegram: TelegramConfig{
			PollTimeout:         30,
			StreamEditInterval:  "300ms",
			StreamCursor:        " ▉",
			ToolProgress:        "all",
			ToolProgressStyle:   "semantic",
			ToolProgressWindow:  4,
			ToolProgressCleanup: false,
		},
		Governor: GovernorConfig{
			Backend:        "auto",
			NativeProvider: "anthropic",
			Codex: GovernorCodexConfig{
				AuthSource:    "auto",
				BaseURL:       "https://chatgpt.com/backend-api/codex",
				ContextWindow: 200000,
			},
		},
		Providers: ProvidersConfig{
			Default:       "anthropic",
			FallbackChain: []string{},
			Anthropic: AnthropicConfig{
				Model:         "claude-sonnet-4-6",
				MaxTokens:     4096,
				ContextWindow: 200000,
			},
			OpenAI: OpenAIProviderConfig{
				BaseURL:       "https://api.openai.com/v1",
				Model:         "gpt-5.4",
				MaxTokens:     16384,
				ContextWindow: 128000,
			},
			OpenRouter: OpenRouterConfig{
				BaseURL:       "https://openrouter.ai/api/v1",
				Model:         "anthropic/claude-sonnet-4-6",
				MaxTokens:     4096,
				ContextWindow: 200000,
			},
		},
		OpenAI: OpenAIConfig{
			Files: OpenAIFilesConfig{
				Enabled: false,
				Purpose: "assistants",
			},
			VectorStores: OpenAIVectorStoresConfig{
				Enabled: false,
			},
		},
		Sessions: SessionsConfig{
			DBPath:             "~/.aphelion/state/sessions.db",
			IdleExpiry:         "24h",
			MaxContextRatio:    0.75,
			CompactionRatio:    0.55,
			CompactionStrategy: "summarize",
		},
		Agent: AgentConfig{
			PromptRoot:        "~/.aphelion/agent",
			ExecRoot:          "~/.aphelion/workspace",
			SharedMemoryRoot:  "~/.aphelion/agent",
			UserWorkspaceRoot: "~/.aphelion/state/isolated/workspaces",
			UserMemoryRoot:    "~/.aphelion/state/isolated/memory",
			MaxIterations:     50,
			ToolTimeout:       300,
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
			DailyNotesDir:          "memory/daily",
		},
		Memory: MemoryConfig{
			SessionSearch:    false,
			SemanticIndexing: false,
			Reflection: MemoryReflectionConfig{
				Enabled: true,
				Every:   "6h",
			},
			Decay: MemoryDecayConfig{
				Enabled:  true,
				HotDays:  3,
				WarmDays: 14,
				ColdDays: 30,
			},
			Identity: MemoryIdentityConfig{
				Preserve: []string{"SOUL.md", "IDENTITY.md", "IDOLUM.md", "MEMORY.md"},
			},
		},
		Thinking: ThinkingConfig{
			Effort:  "medium",
			Summary: "auto",
			Defaults: ThinkingDefaultsConfig{
				Default:   "medium",
				Heartbeat: "low",
				Cron:      "low",
				Recovery:  "medium",
			},
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

func DefaultConfigPath() string {
	return defaultHomePath(".aphelion", "aphelion.toml")
}

func LegacyConfigPath() string {
	return defaultHomePath(".config", "aphelion", "config.toml")
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
	legacy := LegacyConfigPath()
	if fileExists(legacy) {
		return legacy, nil
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

	applyLegacyAgentRoots(&cfg, md)

	cfg.Sessions.DBPath, err = expandConfiguredPath(cfg.Sessions.DBPath, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand sessions.db_path: %w", err)
	}
	cfg.Agent.Workspace, err = expandConfiguredPath(cfg.Agent.Workspace, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand agent.workspace: %w", err)
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
	normalizeAgentRoots(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func validate(cfg *Config) error {
	if strings.TrimSpace(cfg.Telegram.BotToken) == "" {
		return fmt.Errorf("telegram.bot_token is required")
	}
	if cfg.Telegram.PollTimeout <= 0 {
		return fmt.Errorf("telegram.poll_timeout must be > 0")
	}
	if raw := strings.TrimSpace(cfg.Telegram.StreamEditInterval); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("telegram.stream_edit_interval must be a valid duration: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("telegram.stream_edit_interval must be > 0")
		}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Telegram.ToolProgress)) {
	case "", "all", "new", "off":
	default:
		return fmt.Errorf("telegram.tool_progress must be one of all|new|off")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Telegram.ToolProgressStyle)) {
	case "", "semantic", "raw":
	default:
		return fmt.Errorf("telegram.tool_progress_style must be one of semantic|raw")
	}
	if cfg.Telegram.ToolProgressWindow <= 0 {
		return fmt.Errorf("telegram.tool_progress_window must be > 0")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Thinking.Effort)) {
	case "", "none", "low", "medium", "high", "xhigh":
	default:
		return fmt.Errorf("thinking.effort must be one of none|low|medium|high|xhigh")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Thinking.Summary)) {
	case "", "none", "auto", "compact":
	default:
		return fmt.Errorf("thinking.summary must be one of none|auto|compact")
	}
	for name, value := range map[string]string{
		"thinking.defaults.default":   cfg.Thinking.Defaults.Default,
		"thinking.defaults.heartbeat": cfg.Thinking.Defaults.Heartbeat,
		"thinking.defaults.cron":      cfg.Thinking.Defaults.Cron,
		"thinking.defaults.recovery":  cfg.Thinking.Defaults.Recovery,
	} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "none", "low", "medium", "high", "xhigh":
		default:
			return fmt.Errorf("%s must be one of none|low|medium|high|xhigh", name)
		}
	}
	governorBackend := strings.ToLower(strings.TrimSpace(cfg.Governor.Backend))
	switch governorBackend {
	case "auto", "codex", "native":
	default:
		return fmt.Errorf("governor.backend must be one of auto|codex|native")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Governor.Codex.AuthSource)) {
	case "auto", "codex_cli", "aphelion":
	default:
		return fmt.Errorf("governor.codex.auth_source must be one of auto|codex_cli|aphelion")
	}
	if strings.TrimSpace(cfg.Governor.Codex.BaseURL) == "" {
		return fmt.Errorf("governor.codex.base_url is required")
	}
	if cfg.Governor.Codex.ContextWindow <= 0 {
		return fmt.Errorf("governor.codex.context_window must be > 0")
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
	if cfg.Sessions.MaxContextRatio <= 0 || cfg.Sessions.MaxContextRatio >= 1 {
		return fmt.Errorf("sessions.max_context_ratio must be > 0 and < 1")
	}
	if cfg.Sessions.CompactionRatio <= 0 || cfg.Sessions.CompactionRatio >= 1 {
		return fmt.Errorf("sessions.compaction_ratio must be > 0 and < 1")
	}
	if cfg.Sessions.CompactionRatio >= cfg.Sessions.MaxContextRatio {
		return fmt.Errorf("sessions.compaction_ratio must be < max_context_ratio")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Sessions.CompactionStrategy)) {
	case "", "summarize", "truncate":
	default:
		return fmt.Errorf("sessions.compaction_strategy must be one of summarize|truncate")
	}
	if strings.TrimSpace(cfg.Agent.EffectivePromptRoot()) == "" {
		return fmt.Errorf("agent.prompt_root is required")
	}
	if strings.TrimSpace(cfg.Agent.EffectiveExecRoot()) == "" {
		return fmt.Errorf("agent.exec_root is required")
	}
	if strings.TrimSpace(cfg.Agent.EffectiveSharedMemoryRoot()) == "" {
		return fmt.Errorf("agent.shared_memory_root is required")
	}
	if strings.TrimSpace(cfg.Agent.EffectiveUserWorkspaceRoot()) == "" {
		return fmt.Errorf("agent.user_workspace_root is required")
	}
	if strings.TrimSpace(cfg.Agent.EffectiveUserMemoryRoot()) == "" {
		return fmt.Errorf("agent.user_memory_root is required")
	}
	if len(cfg.Agent.BootstrapFiles) == 0 {
		return fmt.Errorf("agent.bootstrap_files must not be empty")
	}
	if strings.TrimSpace(cfg.Agent.DailyNotesDir) == "" {
		return fmt.Errorf("agent.daily_notes_dir is required")
	}
	if strings.TrimSpace(cfg.Memory.Reflection.Every) == "" {
		return fmt.Errorf("memory.reflection.every is required")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.Memory.Reflection.Every)); err != nil {
		return fmt.Errorf("memory.reflection.every must be a valid duration: %w", err)
	}
	if cfg.Memory.Decay.HotDays <= 0 {
		return fmt.Errorf("memory.decay.hot_days must be > 0")
	}
	if cfg.Memory.Decay.WarmDays <= 0 {
		return fmt.Errorf("memory.decay.warm_days must be > 0")
	}
	if cfg.Memory.Decay.ColdDays <= 0 {
		return fmt.Errorf("memory.decay.cold_days must be > 0")
	}
	if cfg.Memory.Decay.HotDays > cfg.Memory.Decay.WarmDays {
		return fmt.Errorf("memory.decay.hot_days must be <= warm_days")
	}
	if cfg.Memory.Decay.WarmDays > cfg.Memory.Decay.ColdDays {
		return fmt.Errorf("memory.decay.warm_days must be <= cold_days")
	}
	if len(cfg.Memory.Identity.Preserve) == 0 {
		return fmt.Errorf("memory.identity.preserve must not be empty")
	}
	faceBackend := strings.ToLower(strings.TrimSpace(cfg.Face.Backend))
	switch faceBackend {
	case "", "provider", "governor_passthrough":
	default:
		return fmt.Errorf("face.backend must be one of provider|governor_passthrough")
	}
	switch providerName(strings.TrimSpace(cfg.Providers.Default)) {
	case "", "anthropic", "openrouter":
	default:
		return fmt.Errorf("providers.default must be one of anthropic|openrouter")
	}
	for i, name := range cfg.Providers.FallbackChain {
		switch providerName(name) {
		case "", "anthropic", "openrouter":
			if providerName(name) == "" {
				return fmt.Errorf("providers.fallback_chain[%d] must not be empty", i)
			}
		default:
			return fmt.Errorf("providers.fallback_chain[%d] must be one of anthropic|openrouter", i)
		}
	}
	nativePrimary := providerName(firstNonEmpty(strings.TrimSpace(cfg.Governor.NativeProvider), strings.TrimSpace(cfg.Providers.Default)))
	if nativePrimary == "" {
		nativePrimary = "anthropic"
	}
	switch nativePrimary {
	case "anthropic", "openrouter":
	default:
		return fmt.Errorf("governor.native_provider must be one of anthropic|openrouter")
	}
	needsNativeProvider := governorBackend == "native" || faceBackend == "" || faceBackend == "provider" || len(cfg.Providers.FallbackChain) > 0
	if needsNativeProvider && nativePrimary == "" {
		return fmt.Errorf("governor.native_provider is required when native provider access is enabled")
	}
	if cfg.Providers.Anthropic.ContextWindow <= 0 {
		return fmt.Errorf("providers.anthropic.context_window must be > 0")
	}
	if cfg.Providers.OpenAI.ContextWindow <= 0 {
		return fmt.Errorf("providers.openai.context_window must be > 0")
	}
	if cfg.Providers.OpenRouter.ContextWindow <= 0 {
		return fmt.Errorf("providers.openrouter.context_window must be > 0")
	}
	if strings.TrimSpace(cfg.Providers.OpenAI.BaseURL) == "" {
		return fmt.Errorf("providers.openai.base_url is required")
	}
	if strings.TrimSpace(cfg.Providers.OpenRouter.BaseURL) == "" {
		return fmt.Errorf("providers.openrouter.base_url is required")
	}
	if needsNativeProvider {
		required := append([]string{nativePrimary}, cfg.Providers.FallbackChain...)
		for _, name := range required {
			switch providerName(name) {
			case "anthropic":
				if strings.TrimSpace(cfg.Providers.Anthropic.APIKey) == "" {
					return fmt.Errorf("providers.anthropic.api_key is required when anthropic is in the native provider chain")
				}
			case "openrouter":
				if strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) == "" {
					return fmt.Errorf("providers.openrouter.api_key is required when openrouter is in the native provider chain")
				}
			}
		}
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
	if cfg.OpenAI.Files.Enabled || cfg.OpenAI.VectorStores.Enabled {
		if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
			return fmt.Errorf("providers.openai.api_key is required when OpenAI platform storage is enabled")
		}
		if cfg.OpenAI.Files.Enabled && strings.TrimSpace(cfg.OpenAI.Files.Purpose) == "" {
			return fmt.Errorf("openai.files.purpose is required when openai.files.enabled = true")
		}
	}
	if len(cfg.Principals.Telegram.AdminUserIDs) == 0 {
		return fmt.Errorf("principals.telegram.admin_user_ids must contain at least one user id; add [principals.telegram] admin_user_ids = [123456789]")
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

func applyLegacyAgentRoots(cfg *Config, md toml.MetaData) {
	if cfg == nil || !md.IsDefined("agent", "workspace") {
		return
	}
	if !md.IsDefined("agent", "prompt_root") {
		cfg.Agent.PromptRoot = cfg.Agent.Workspace
	}
	if !md.IsDefined("agent", "exec_root") {
		cfg.Agent.ExecRoot = cfg.Agent.Workspace
	}
	if !md.IsDefined("agent", "shared_memory_root") {
		cfg.Agent.SharedMemoryRoot = cfg.Agent.Workspace
	}
	if !md.IsDefined("agent", "user_workspace_root") {
		cfg.Agent.UserWorkspaceRoot = filepath.Join(filepath.Dir(cfg.Sessions.DBPath), "isolated", "workspaces")
	}
	if !md.IsDefined("agent", "user_memory_root") {
		cfg.Agent.UserMemoryRoot = filepath.Join(filepath.Dir(cfg.Sessions.DBPath), "isolated", "memory")
	}
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
	if strings.TrimSpace(cfg.Agent.Workspace) == "" {
		cfg.Agent.Workspace = cfg.Agent.ExecRoot
	}
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

func providerName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
