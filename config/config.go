//go:build linux

package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Identity      IdentityConfig      `toml:"identity"`
	Telegram      TelegramConfig      `toml:"telegram"`
	Principals    PrincipalsConfig    `toml:"principals"`
	Governor      GovernorConfig      `toml:"governor"`
	Providers     ProvidersConfig     `toml:"providers"`
	OpenAI        OpenAIConfig        `toml:"openai"`
	Sessions      SessionsConfig      `toml:"sessions"`
	Agent         AgentConfig         `toml:"agent"`
	Tools         ToolsConfig         `toml:"tools"`
	Memory        MemoryConfig        `toml:"memory"`
	Thinking      ThinkingConfig      `toml:"thinking"`
	Face          FaceConfig          `toml:"face"`
	Heartbeat     HeartbeatConfig     `toml:"heartbeat"`
	Cron          CronConfig          `toml:"cron"`
	Voice         VoiceConfig         `toml:"voice"`
	DurableAgents DurableAgentsConfig `toml:"durable_agents"`
	Tailscale     TailscaleConfig     `toml:"tailscale"`
}

type IdentityConfig struct {
	UserAgent string `toml:"user_agent"`
}

type TelegramConfig struct {
	BotToken               string                       `toml:"bot_token"`
	DetachPendingOnRestart bool                         `toml:"detach_pending_on_restart"`
	PollTimeout            int                          `toml:"poll_timeout"`
	StreamEditInterval     string                       `toml:"stream_edit_interval"`
	StreamCursor           string                       `toml:"stream_cursor"`
	ToolProgress           string                       `toml:"tool_progress"`
	ToolProgressStyle      string                       `toml:"tool_progress_style"`
	ToolProgressWindow     int                          `toml:"tool_progress_window"`
	ToolProgressCleanup    bool                         `toml:"tool_progress_cleanup"`
	MiniApp                TelegramMiniAppConfig        `toml:"mini_app"`
	Media                  TelegramMediaConfig          `toml:"media"`
	DurableGroups          []TelegramDurableGroupConfig `toml:"durable_groups"`
}

type TelegramMiniAppConfig struct {
	Enabled    bool   `toml:"enabled"`
	ListenAddr string `toml:"listen_addr"`
	PublicURL  string `toml:"public_url"`
	AuthMaxAge string `toml:"auth_max_age"`
}

type TelegramDurableGroupConfig struct {
	ChatID             int64  `toml:"chat_id"`
	AgentID            string `toml:"agent_id"`
	Charter            string `toml:"charter"`
	RespondOn          string `toml:"respond_on"`
	ReviewTargetChatID int64  `toml:"review_target_chat_id"`
	LLMBackend         string `toml:"llm_backend"`
	LLMProvider        string `toml:"llm_provider"`
	LLMAPIKey          string `toml:"llm_api_key"`
	LLMBaseURL         string `toml:"llm_base_url"`
	LLMModel           string `toml:"llm_model"`
	LLMMaxTokens       int    `toml:"llm_max_tokens"`
	LLMCodexAuthSource string `toml:"llm_codex_auth_source"`
	LLMCodexHome       string `toml:"llm_codex_home"`
	LLMCodexBaseURL    string `toml:"llm_codex_base_url"`
}

type TelegramMediaConfig struct {
	DownloadMaxSize  string `toml:"download_max_size"`
	AutoVisionPhotos bool   `toml:"auto_vision_photos"`
	AutoVisionDocs   bool   `toml:"auto_vision_documents"`
	ExtractPDFText   bool   `toml:"extract_pdf_text"`
	MaxPDFBytes      string `toml:"max_pdf_bytes"`
}

type TailscaleConfig struct {
	Enabled          bool     `toml:"enabled"`
	Backend          string   `toml:"backend"`
	CLIPath          string   `toml:"cli_path"`
	CommandTimeout   string   `toml:"command_timeout"`
	ExpectedTailnet  string   `toml:"expected_tailnet"`
	ExpectedHostname string   `toml:"expected_hostname"`
	ExpectedTags     []string `toml:"expected_tags"`
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
	Brokerage      BrokerageConfig     `toml:"brokerage"`
}

type GovernorCodexConfig struct {
	AuthSource       string `toml:"auth_source"`
	AuthPath         string `toml:"auth_path"`
	CodexHome        string `toml:"codex_home"`
	BaseURL          string `toml:"base_url"`
	Model            string `toml:"model"`
	ContextWindow    int    `toml:"context_window"`
	StoreResponses   bool   `toml:"store_responses"`
	MaxContinuations int    `toml:"max_continuations"`
	TransportRetries int    `toml:"transport_retries"`
}

type BrokerageConfig struct {
	MinRounds              int    `toml:"min_rounds"`
	MaxRounds              int    `toml:"max_rounds"`
	AbsoluteMaxRounds      int    `toml:"absolute_max_rounds"`
	MaxElapsed             string `toml:"max_elapsed"`
	StableContractRounds   int    `toml:"stable_contract_rounds"`
	StopOnStableContract   bool   `toml:"stop_on_stable_contract"`
	StopOnRepeatedProposal bool   `toml:"stop_on_repeated_proposal"`
	StopOnReject           bool   `toml:"stop_on_reject"`
}

type ProvidersConfig struct {
	Selection     string               `toml:"selection"`
	AutoOrder     []string             `toml:"auto_order"`
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
	APIKey         string   `toml:"api_key"`
	BaseURL        string   `toml:"base_url"`
	Model          string   `toml:"model"`
	FallbackModels []string `toml:"fallback_models"`
	MaxTokens      int      `toml:"max_tokens"`
	ContextWindow  int      `toml:"context_window"`
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
	DBPath             string                     `toml:"db_path"`
	IdleExpiry         string                     `toml:"idle_expiry"`
	MaxContextRatio    float64                    `toml:"max_context_ratio"`
	CompactionRatio    float64                    `toml:"compaction_ratio"`
	CompactionStrategy string                     `toml:"compaction_strategy"`
	TESRetention       SessionsTESRetentionConfig `toml:"tes_retention"`
}

type SessionsTESRetentionConfig struct {
	Enabled         bool   `toml:"enabled"`
	MaxAge          string `toml:"max_age"`
	MinRetainedRows int    `toml:"min_retained_rows"`
	MaxDeletePerGC  int    `toml:"max_delete_per_gc"`
	ExportDir       string `toml:"export_dir"`
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

type ToolsConfig struct {
	ExternalManifestDir string `toml:"external_manifest_dir"`
}

type MemoryConfig struct {
	SessionSearch    bool                    `toml:"session_search"`
	SemanticIndexing bool                    `toml:"semantic_indexing"`
	Semantic         MemorySemanticConfig    `toml:"semantic"`
	Aggressive       MemoryAggressiveConfig  `toml:"aggressive"`
	Reflection       MemoryReflectionConfig  `toml:"reflection"`
	Decay            MemoryDecayConfig       `toml:"decay"`
	Identity         MemoryIdentityConfig    `toml:"identity"`
	WritePolicy      MemoryWritePolicyConfig `toml:"write_policy"`
}

type MemorySemanticConfig struct {
	Enabled             bool     `toml:"enabled"`
	Backend             string   `toml:"backend"`
	Refresh             string   `toml:"refresh"`
	Sources             []string `toml:"sources"`
	IncludeDailyNotes   bool     `toml:"include_daily_notes"`
	IncludeQuestions    bool     `toml:"include_questions"`
	IncludeRhizome      bool     `toml:"include_rhizome"`
	InteractiveTopK     int      `toml:"interactive_top_k"`
	HeartbeatTopK       int      `toml:"heartbeat_top_k"`
	InteractiveMaxChars int      `toml:"interactive_max_chars"`
	HeartbeatMaxChars   int      `toml:"heartbeat_max_chars"`
}

type MemoryReflectionConfig struct {
	Enabled bool   `toml:"enabled"`
	Every   string `toml:"every"`
}

type MemoryAggressiveConfig struct {
	Enabled                bool `toml:"enabled"`
	CaptureEveryTurn       bool `toml:"capture_every_turn"`
	PrefetchEveryTurn      bool `toml:"prefetch_every_turn"`
	FlushOnSessionBoundary bool `toml:"flush_on_session_boundary"`
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

type MemoryWritePolicyConfig struct {
	DirectUserWrites  string `toml:"direct_user_writes"`
	ReflectionWrites  string `toml:"reflection_writes"`
	AggressiveWrites  string `toml:"aggressive_writes"`
	AutoAcceptLowRisk bool   `toml:"auto_accept_low_risk"`
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

type DurableAgentsConfig struct {
	ControlPlane DurableAgentControlPlaneConfig `toml:"control_plane"`
}

type DurableAgentControlPlaneConfig struct {
	Enabled  bool   `toml:"enabled"`
	Listen   string `toml:"listen"`
	BasePath string `toml:"base_path"`
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`
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
			DetachPendingOnRestart: true,
			PollTimeout:            30,
			StreamEditInterval:     "300ms",
			StreamCursor:           " ▉",
			ToolProgress:           "all",
			ToolProgressStyle:      "semantic",
			ToolProgressWindow:     4,
			ToolProgressCleanup:    false,
			MiniApp: TelegramMiniAppConfig{
				Enabled:    false,
				ListenAddr: "127.0.0.1:8765",
				AuthMaxAge: "24h",
			},
			Media: TelegramMediaConfig{
				DownloadMaxSize:  "20MB",
				AutoVisionPhotos: true,
				AutoVisionDocs:   true,
				ExtractPDFText:   true,
				MaxPDFBytes:      "8MB",
			},
		},
		Governor: GovernorConfig{
			Backend:        "auto",
			NativeProvider: "",
			Codex: GovernorCodexConfig{
				AuthSource:       "auto",
				BaseURL:          "https://chatgpt.com/backend-api",
				Model:            "gpt-5.5",
				ContextWindow:    250000,
				StoreResponses:   true,
				MaxContinuations: 3,
				TransportRetries: 3,
			},
			Brokerage: BrokerageConfig{
				MinRounds:              1,
				MaxRounds:              4,
				AbsoluteMaxRounds:      6,
				MaxElapsed:             "20s",
				StableContractRounds:   2,
				StopOnStableContract:   true,
				StopOnRepeatedProposal: true,
				StopOnReject:           true,
			},
		},
		Providers: ProvidersConfig{
			Selection:     "auto",
			AutoOrder:     []string{"openai", "anthropic", "openrouter"},
			Default:       "",
			FallbackChain: []string{},
			Anthropic: AnthropicConfig{
				Model:         "claude-sonnet-4-6",
				MaxTokens:     4096,
				ContextWindow: 200000,
			},
			OpenAI: OpenAIProviderConfig{
				BaseURL:        "https://api.openai.com/v1",
				Model:          "gpt-5.5",
				FallbackModels: []string{"gpt-5.4", "gpt-5.4-mini"},
				MaxTokens:      16384,
				ContextWindow:  128000,
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
			MaxContextRatio:    0.90,
			CompactionRatio:    0.70,
			CompactionStrategy: "summarize",
			TESRetention: SessionsTESRetentionConfig{
				Enabled:         false,
				MaxAge:          "720h",
				MinRetainedRows: 5000,
				MaxDeletePerGC:  1000,
				ExportDir:       "~/.aphelion/state/tes-exports",
			},
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
			DynamicFiles:           []string{"MEMORY.md", "HEARTBEAT.md", "SKILLS.md", "memory/knowledge.md", "memory/decisions.md", "memory/questions.md", "memory/rhizome.md", "memory/dreams.md"},
			BootstrapMaxChars:      20000,
			BootstrapTotalMaxChars: 950000,
			DailyNotes:             true,
			DailyNotesDir:          "memory/daily",
		},
		Memory: MemoryConfig{
			SessionSearch:    false,
			SemanticIndexing: false,
			Semantic: MemorySemanticConfig{
				Enabled:             false,
				Backend:             "local",
				Refresh:             "manual",
				Sources:             []string{"MEMORY.md", "SKILLS.md", "memory/knowledge.md", "memory/decisions.md", "memory/questions.md", "memory/rhizome.md", "memory/dreams.md"},
				IncludeDailyNotes:   true,
				IncludeQuestions:    true,
				IncludeRhizome:      true,
				InteractiveTopK:     5,
				HeartbeatTopK:       12,
				InteractiveMaxChars: 4000,
				HeartbeatMaxChars:   12000,
			},
			Aggressive: MemoryAggressiveConfig{
				Enabled:                false,
				CaptureEveryTurn:       false,
				PrefetchEveryTurn:      false,
				FlushOnSessionBoundary: false,
			},
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
			WritePolicy: MemoryWritePolicyConfig{
				DirectUserWrites:  "apply",
				ReflectionWrites:  "propose",
				AggressiveWrites:  "propose",
				AutoAcceptLowRisk: false,
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
		DurableAgents: DurableAgentsConfig{},
		Tailscale: TailscaleConfig{
			Enabled:        false,
			Backend:        "cli",
			CLIPath:        "tailscale",
			CommandTimeout: "5s",
		},
	}
}

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

	applyLegacyAgentRoots(&cfg, md)
	cfg.Providers.Selection = normalizeProviderSelection(cfg.Providers.Selection)
	cfg.Providers.AutoOrder = normalizeProviderNameList(cfg.Providers.AutoOrder)
	if len(cfg.Providers.AutoOrder) == 0 {
		cfg.Providers.AutoOrder = []string{"openai", "anthropic", "openrouter"}
	}
	cfg.Providers.OpenAI.FallbackModels = normalizeOpenAIModelFallbacks(cfg.Providers.OpenAI.Model, cfg.Providers.OpenAI.FallbackModels)
	applyProviderSelectionHeuristic(&cfg, md)

	cfg.Sessions.DBPath, err = expandConfiguredPath(cfg.Sessions.DBPath, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand sessions.db_path: %w", err)
	}
	cfg.Sessions.TESRetention.ExportDir, err = expandConfiguredPath(cfg.Sessions.TESRetention.ExportDir, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand sessions.tes_retention.export_dir: %w", err)
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
	cfg.Tools.ExternalManifestDir, err = expandConfiguredPath(cfg.Tools.ExternalManifestDir, baseDir)
	if err != nil {
		return nil, fmt.Errorf("expand tools.external_manifest_dir: %w", err)
	}
	normalizeAgentRoots(&cfg)
	cfg.Face.Backend = NormalizeFaceBackendValue(cfg.Face.Backend)
	normalizeTelegramDurableGroups(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyProviderSelectionHeuristic(cfg *Config, md toml.MetaData) {
	if cfg == nil {
		return
	}
	defaultDefined := md.IsDefined("providers", "default") && providerName(cfg.Providers.Default) != ""
	nativeDefined := md.IsDefined("governor", "native_provider") && providerName(cfg.Governor.NativeProvider) != ""
	fallbackDefined := md.IsDefined("providers", "fallback_chain")

	if defaultDefined && !nativeDefined {
		cfg.Governor.NativeProvider = providerName(cfg.Providers.Default)
	}
	if nativeDefined && !defaultDefined {
		cfg.Providers.Default = providerName(cfg.Governor.NativeProvider)
	}
	if normalizeProviderSelection(cfg.Providers.Selection) != "auto" {
		return
	}
	if !defaultDefined && !nativeDefined {
		if primary := firstConfiguredProviderByOrder(cfg, cfg.Providers.AutoOrder); primary != "" {
			cfg.Governor.NativeProvider = primary
			cfg.Providers.Default = primary
		}
	}
	if !fallbackDefined {
		primary := providerName(firstNonEmpty(cfg.Governor.NativeProvider, cfg.Providers.Default))
		cfg.Providers.FallbackChain = configuredProviderFallbacks(cfg, primary)
	}
}

func configuredProviderFallbacks(cfg *Config, primary string) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]struct{}{}
	if primary = providerName(primary); primary != "" {
		seen[primary] = struct{}{}
	}
	out := make([]string, 0, len(cfg.Providers.AutoOrder))
	for _, name := range cfg.Providers.AutoOrder {
		name = providerName(name)
		if name == "" || !providerConfigured(cfg, name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func firstConfiguredProviderByOrder(cfg *Config, order []string) string {
	for _, name := range order {
		name = providerName(name)
		if providerConfigured(cfg, name) {
			return name
		}
	}
	return ""
}

func providerConfigured(cfg *Config, name string) bool {
	if cfg == nil {
		return false
	}
	switch providerName(name) {
	case "anthropic":
		return strings.TrimSpace(cfg.Providers.Anthropic.APIKey) != ""
	case "openai":
		return strings.TrimSpace(cfg.Providers.OpenAI.APIKey) != ""
	case "openrouter":
		return strings.TrimSpace(cfg.Providers.OpenRouter.APIKey) != ""
	default:
		return false
	}
}

func normalizeProviderSelection(selection string) string {
	switch strings.ToLower(strings.TrimSpace(selection)) {
	case "", "auto":
		return "auto"
	case "manual", "explicit":
		return "manual"
	default:
		return strings.ToLower(strings.TrimSpace(selection))
	}
}

func normalizeProviderNameList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		name := providerName(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func normalizeOpenAIModelFallbacks(primary string, fallbacks []string) []string {
	seen := map[string]struct{}{}
	if primary = strings.TrimSpace(primary); primary != "" {
		seen[primary] = struct{}{}
	}
	out := make([]string, 0, len(fallbacks))
	for _, raw := range fallbacks {
		model := strings.TrimSpace(raw)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	return out
}

func EffectiveProviderChain(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 2+len(cfg.Providers.FallbackChain))
	for _, raw := range append([]string{cfg.Governor.NativeProvider, cfg.Providers.Default}, cfg.Providers.FallbackChain...) {
		name := providerName(raw)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if len(out) > 0 {
		return out
	}
	for _, name := range cfg.Providers.AutoOrder {
		name = providerName(name)
		if name == "" || !providerConfigured(cfg, name) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func EffectiveNativeProvider(cfg *Config) string {
	chain := EffectiveProviderChain(cfg)
	if len(chain) == 0 {
		return ""
	}
	return chain[0]
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
	if cfg.Telegram.MiniApp.Enabled && strings.TrimSpace(cfg.Telegram.MiniApp.ListenAddr) == "" {
		return fmt.Errorf("telegram.mini_app.listen_addr is required when enabled")
	}
	if raw := strings.TrimSpace(cfg.Telegram.MiniApp.PublicURL); raw != "" {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("telegram.mini_app.public_url must be an absolute http or https URL")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
		default:
			return fmt.Errorf("telegram.mini_app.public_url must be an absolute http or https URL")
		}
	}
	if raw := strings.TrimSpace(cfg.Telegram.MiniApp.AuthMaxAge); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("telegram.mini_app.auth_max_age must be a valid duration: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("telegram.mini_app.auth_max_age must be > 0")
		}
	}
	if _, err := ParseByteSize(strings.TrimSpace(cfg.Telegram.Media.DownloadMaxSize)); err != nil {
		return fmt.Errorf("telegram.media.download_max_size must be a valid positive size: %w", err)
	}
	if _, err := ParseByteSize(strings.TrimSpace(cfg.Telegram.Media.MaxPDFBytes)); err != nil {
		return fmt.Errorf("telegram.media.max_pdf_bytes must be a valid positive size: %w", err)
	}
	if err := validateTelegramDurableGroups(cfg); err != nil {
		return err
	}
	if err := validateTailscaleConfig(cfg); err != nil {
		return err
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
	if strings.TrimSpace(cfg.Governor.Codex.Model) == "" {
		return fmt.Errorf("governor.codex.model is required")
	}
	if cfg.Governor.Codex.ContextWindow <= 0 {
		return fmt.Errorf("governor.codex.context_window must be > 0")
	}
	if cfg.Governor.Codex.MaxContinuations <= 0 {
		return fmt.Errorf("governor.codex.max_continuations must be > 0")
	}
	if cfg.Governor.Codex.TransportRetries < 0 {
		return fmt.Errorf("governor.codex.transport_retries must be >= 0")
	}
	if cfg.Governor.Brokerage.MinRounds <= 0 {
		return fmt.Errorf("governor.brokerage.min_rounds must be > 0")
	}
	if cfg.Governor.Brokerage.MaxRounds <= 0 {
		return fmt.Errorf("governor.brokerage.max_rounds must be > 0")
	}
	if cfg.Governor.Brokerage.AbsoluteMaxRounds <= 0 {
		return fmt.Errorf("governor.brokerage.absolute_max_rounds must be > 0")
	}
	if cfg.Governor.Brokerage.MinRounds > cfg.Governor.Brokerage.MaxRounds {
		return fmt.Errorf("governor.brokerage.min_rounds must be <= max_rounds")
	}
	if cfg.Governor.Brokerage.MaxRounds > cfg.Governor.Brokerage.AbsoluteMaxRounds {
		return fmt.Errorf("governor.brokerage.max_rounds must be <= absolute_max_rounds")
	}
	if cfg.Governor.Brokerage.StableContractRounds < 2 {
		return fmt.Errorf("governor.brokerage.stable_contract_rounds must be >= 2")
	}
	if elapsed, err := time.ParseDuration(strings.TrimSpace(cfg.Governor.Brokerage.MaxElapsed)); err != nil {
		return fmt.Errorf("governor.brokerage.max_elapsed must be a valid duration: %w", err)
	} else if elapsed <= 0 {
		return fmt.Errorf("governor.brokerage.max_elapsed must be > 0")
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
	maxAge, err := time.ParseDuration(strings.TrimSpace(cfg.Sessions.TESRetention.MaxAge))
	if err != nil {
		return fmt.Errorf("sessions.tes_retention.max_age must be a valid duration: %w", err)
	}
	if maxAge < 24*time.Hour {
		return fmt.Errorf("sessions.tes_retention.max_age must be >= 24h")
	}
	if cfg.Sessions.TESRetention.MinRetainedRows < 100 {
		return fmt.Errorf("sessions.tes_retention.min_retained_rows must be >= 100")
	}
	if cfg.Sessions.TESRetention.MaxDeletePerGC <= 0 {
		return fmt.Errorf("sessions.tes_retention.max_delete_per_gc must be > 0")
	}
	if cfg.Sessions.TESRetention.MaxDeletePerGC > cfg.Sessions.TESRetention.MinRetainedRows {
		return fmt.Errorf("sessions.tes_retention.max_delete_per_gc must be <= min_retained_rows")
	}
	if cfg.Sessions.TESRetention.Enabled && strings.TrimSpace(cfg.Sessions.TESRetention.ExportDir) == "" {
		return fmt.Errorf("sessions.tes_retention.export_dir is required when retention is enabled")
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
	if err := validateMemoryWritePolicy(cfg.Memory.WritePolicy); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Memory.Semantic.Backend)) {
	case "", "local":
	default:
		return fmt.Errorf("memory.semantic.backend must be one of local")
	}
	refresh := strings.ToLower(strings.TrimSpace(cfg.Memory.Semantic.Refresh))
	switch refresh {
	case "", "manual", "heartbeat":
	default:
		if _, err := time.ParseDuration(strings.TrimSpace(cfg.Memory.Semantic.Refresh)); err != nil {
			return fmt.Errorf("memory.semantic.refresh must be manual|heartbeat|<duration>: %w", err)
		}
	}
	if cfg.Memory.Semantic.InteractiveTopK <= 0 {
		return fmt.Errorf("memory.semantic.interactive_top_k must be > 0")
	}
	if cfg.Memory.Semantic.HeartbeatTopK <= 0 {
		return fmt.Errorf("memory.semantic.heartbeat_top_k must be > 0")
	}
	if cfg.Memory.Semantic.InteractiveMaxChars <= 0 {
		return fmt.Errorf("memory.semantic.interactive_max_chars must be > 0")
	}
	if cfg.Memory.Semantic.HeartbeatMaxChars <= 0 {
		return fmt.Errorf("memory.semantic.heartbeat_max_chars must be > 0")
	}
	faceBackend := NormalizeFaceBackendValue(cfg.Face.Backend)
	switch faceBackend {
	case "", "provider", "floor_fallback":
	default:
		return fmt.Errorf("face.backend must be one of provider|floor_fallback")
	}
	switch normalizeProviderSelection(cfg.Providers.Selection) {
	case "auto", "manual":
	default:
		return fmt.Errorf("providers.selection must be one of auto|manual")
	}
	if len(cfg.Providers.AutoOrder) == 0 {
		return fmt.Errorf("providers.auto_order must contain at least one provider")
	}
	for i, name := range cfg.Providers.AutoOrder {
		switch providerName(name) {
		case "anthropic", "openai", "openrouter":
		default:
			return fmt.Errorf("providers.auto_order[%d] must be one of anthropic|openai|openrouter", i)
		}
	}
	switch providerName(strings.TrimSpace(cfg.Providers.Default)) {
	case "", "anthropic", "openai", "openrouter":
	default:
		return fmt.Errorf("providers.default must be one of anthropic|openai|openrouter")
	}
	for i, name := range cfg.Providers.FallbackChain {
		switch providerName(name) {
		case "", "anthropic", "openai", "openrouter":
			if providerName(name) == "" {
				return fmt.Errorf("providers.fallback_chain[%d] must not be empty", i)
			}
		default:
			return fmt.Errorf("providers.fallback_chain[%d] must be one of anthropic|openai|openrouter", i)
		}
	}
	nativePrimary := providerName(firstNonEmpty(strings.TrimSpace(cfg.Governor.NativeProvider), strings.TrimSpace(cfg.Providers.Default)))
	switch nativePrimary {
	case "", "anthropic", "openai", "openrouter":
		if nativePrimary == "" && (governorBackend == "native" || faceBackend == "" || faceBackend == "provider") {
			return fmt.Errorf("governor.native_provider or providers.default is required when native provider access is enabled")
		}
	default:
		return fmt.Errorf("governor.native_provider must be one of anthropic|openai|openrouter")
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
			case "openai":
				if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
					return fmt.Errorf("providers.openai.api_key is required when openai is in the native provider chain")
				}
				if strings.TrimSpace(cfg.Providers.OpenAI.Model) == "" {
					return fmt.Errorf("providers.openai.model is required when openai is in the native provider chain")
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
	case "", "off", "auto", "all":
	default:
		return fmt.Errorf("voice.mode must be one of off|auto|all")
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
	if len(cfg.Principals.Telegram.AdminUserIDs) != 1 {
		return fmt.Errorf("principals.telegram.admin_user_ids must contain exactly one user id")
	}
	if cfg.DurableAgents.ControlPlane.Enabled && strings.TrimSpace(cfg.DurableAgents.ControlPlane.Listen) == "" {
		return fmt.Errorf("durable_agents.control_plane.listen is required when durable_agents.control_plane.enabled = true")
	}
	if strings.TrimSpace(cfg.DurableAgents.ControlPlane.BasePath) != "" && !strings.HasPrefix(strings.TrimSpace(cfg.DurableAgents.ControlPlane.BasePath), "/") {
		return fmt.Errorf("durable_agents.control_plane.base_path must start with / when set")
	}
	if (strings.TrimSpace(cfg.DurableAgents.ControlPlane.CertFile) == "") != (strings.TrimSpace(cfg.DurableAgents.ControlPlane.KeyFile) == "") {
		return fmt.Errorf("durable_agents.control_plane.cert_file and key_file must be set together")
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
	if len(cfg.Principals.Telegram.ApprovedUserIDs) > 0 {
		return fmt.Errorf("principals.telegram.approved_user_ids is not supported; use durable-agent access grants instead")
	}
	return nil
}

func validateMemoryWritePolicy(policy MemoryWritePolicyConfig) error {
	for name, value := range map[string]string{
		"memory.write_policy.direct_user_writes": strings.TrimSpace(policy.DirectUserWrites),
		"memory.write_policy.reflection_writes":  strings.TrimSpace(policy.ReflectionWrites),
		"memory.write_policy.aggressive_writes":  strings.TrimSpace(policy.AggressiveWrites),
	} {
		switch strings.ToLower(value) {
		case "apply", "propose":
		default:
			return fmt.Errorf("%s must be apply or propose", name)
		}
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

func ParseByteSize(raw string) (int64, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" {
		return 0, fmt.Errorf("must not be empty")
	}
	multiplier := int64(1)
	for _, unit := range []struct {
		Suffix string
		Mult   int64
	}{
		{Suffix: "KB", Mult: 1024},
		{Suffix: "MB", Mult: 1024 * 1024},
		{Suffix: "GB", Mult: 1024 * 1024 * 1024},
		{Suffix: "B", Mult: 1},
	} {
		if strings.HasSuffix(trimmed, unit.Suffix) {
			trimmed = strings.TrimSpace(strings.TrimSuffix(trimmed, unit.Suffix))
			multiplier = unit.Mult
			break
		}
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return value * multiplier, nil
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

func validateTelegramDurableGroups(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	seenChats := make(map[int64]string, len(cfg.Telegram.DurableGroups))
	seenAgents := make(map[string]int64, len(cfg.Telegram.DurableGroups))
	defaultReviewTarget := int64(0)
	if len(cfg.Principals.Telegram.AdminUserIDs) > 0 {
		defaultReviewTarget = cfg.Principals.Telegram.AdminUserIDs[0]
	}
	for i, group := range cfg.Telegram.DurableGroups {
		if group.ChatID == 0 {
			return fmt.Errorf("telegram.durable_groups[%d].chat_id is required", i)
		}
		if existing, ok := seenChats[group.ChatID]; ok {
			return fmt.Errorf("telegram.durable_groups[%d].chat_id duplicates durable group %q", i, existing)
		}
		agentID := strings.TrimSpace(group.AgentID)
		if agentID == "" {
			return fmt.Errorf("telegram.durable_groups[%d].agent_id is required", i)
		}
		if !isSafeDurableAgentID(agentID) {
			return fmt.Errorf("telegram.durable_groups[%d].agent_id must contain only a-z, 0-9, ., _, or -", i)
		}
		if existing, ok := seenAgents[agentID]; ok {
			return fmt.Errorf("telegram.durable_groups[%d].agent_id duplicates chat_id %d", i, existing)
		}
		if strings.TrimSpace(group.Charter) == "" {
			return fmt.Errorf("telegram.durable_groups[%d].charter is required", i)
		}
		switch normalizeTelegramDurableGroupRespondOn(group.RespondOn) {
		case "all", "mentions":
		default:
			return fmt.Errorf("telegram.durable_groups[%d].respond_on must be one of all|mentions", i)
		}
		if group.ReviewTargetChatID == 0 && defaultReviewTarget == 0 {
			return fmt.Errorf("telegram.durable_groups[%d].review_target_chat_id is required when no admin_user_ids are configured", i)
		}
		if group.ReviewTargetChatID < 0 {
			return fmt.Errorf("telegram.durable_groups[%d].review_target_chat_id must be positive", i)
		}
		switch group.LLMBackend {
		case "native":
			switch group.LLMProvider {
			case "anthropic", "openai", "openrouter":
			default:
				return fmt.Errorf("telegram.durable_groups[%d].llm_provider must be one of anthropic|openai|openrouter for native backend", i)
			}
			if strings.TrimSpace(group.LLMAPIKey) == "" {
				return fmt.Errorf("telegram.durable_groups[%d].llm_api_key is required for native backend", i)
			}
			if strings.TrimSpace(group.LLMCodexAuthSource) != "" || strings.TrimSpace(group.LLMCodexHome) != "" || strings.TrimSpace(group.LLMCodexBaseURL) != "" {
				return fmt.Errorf("telegram.durable_groups[%d] mixes native llm settings with codex bootstrap settings", i)
			}
		case "codex":
			if strings.TrimSpace(group.LLMCodexHome) == "" {
				return fmt.Errorf("telegram.durable_groups[%d].llm_codex_home is required for codex backend", i)
			}
			if strings.TrimSpace(group.LLMProvider) != "" || strings.TrimSpace(group.LLMAPIKey) != "" || strings.TrimSpace(group.LLMBaseURL) != "" || strings.TrimSpace(group.LLMModel) != "" || group.LLMMaxTokens > 0 {
				return fmt.Errorf("telegram.durable_groups[%d] mixes codex llm settings with native provider bootstrap settings", i)
			}
		default:
			return fmt.Errorf("telegram.durable_groups[%d].llm_backend must be one of native|codex", i)
		}
		seenChats[group.ChatID] = agentID
		seenAgents[agentID] = group.ChatID
	}
	return nil
}

func validateTailscaleConfig(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.Tailscale.Backend))
	if backend == "" {
		backend = "cli"
		cfg.Tailscale.Backend = backend
	}
	switch backend {
	case "cli":
	default:
		return fmt.Errorf("tailscale.backend must be cli")
	}
	if strings.TrimSpace(cfg.Tailscale.CLIPath) == "" {
		cfg.Tailscale.CLIPath = "tailscale"
	}
	if raw := strings.TrimSpace(cfg.Tailscale.CommandTimeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("tailscale.command_timeout must be a valid duration: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("tailscale.command_timeout must be > 0")
		}
	} else {
		cfg.Tailscale.CommandTimeout = "5s"
	}
	cfg.Tailscale.ExpectedTailnet = strings.TrimSpace(cfg.Tailscale.ExpectedTailnet)
	cfg.Tailscale.ExpectedHostname = strings.TrimSpace(cfg.Tailscale.ExpectedHostname)
	cfg.Tailscale.ExpectedTags = normalizeStringList(cfg.Tailscale.ExpectedTags)
	return nil
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

func isSafeDurableAgentID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
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

func NormalizeFaceBackendValue(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "provider":
		return strings.ToLower(strings.TrimSpace(raw))
	case "floor_fallback":
		return "floor_fallback"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}
