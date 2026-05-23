//go:build linux

package config

import (
	"strings"
	"time"
)

type Config struct {
	Identity      IdentityConfig      `toml:"identity"`
	Telegram      TelegramConfig      `toml:"telegram"`
	Principals    PrincipalsConfig    `toml:"principals"`
	Governor      GovernorConfig      `toml:"governor"`
	Providers     ProvidersConfig     `toml:"providers"`
	OpenAI        OpenAIConfig        `toml:"openai"`
	Work          WorkConfig          `toml:"work"`
	Autonomy      AutonomyConfig      `toml:"autonomy"`
	Sessions      SessionsConfig      `toml:"sessions"`
	Recovery      RecoveryConfig      `toml:"recovery"`
	Agent         AgentConfig         `toml:"agent"`
	Tools         ToolsConfig         `toml:"tools"`
	Sandbox       SandboxConfig       `toml:"sandbox"`
	Memory        MemoryConfig        `toml:"memory"`
	Thinking      ThinkingConfig      `toml:"thinking"`
	Face          FaceConfig          `toml:"face"`
	Heartbeat     HeartbeatConfig     `toml:"heartbeat"`
	Cron          CronConfig          `toml:"cron"`
	Nocturne      NocturneConfig      `toml:"nocturne"`
	Voice         VoiceConfig         `toml:"voice"`
	DurableAgents DurableAgentsConfig `toml:"durable_agents"`
	Tailscale     TailscaleConfig     `toml:"tailscale"`
	GitHub        GitHubConfig        `toml:"github"`

	warnings []ConfigWarning
}

type ConfigWarning struct {
	Path    string
	Message string
}

type IdentityConfig struct {
	UserAgent        string `toml:"user_agent"`
	ProjectName      string `toml:"project_name"`
	GovernorName     string `toml:"governor_name"`
	FaceName         string `toml:"face_name"`
	AnonymousProfile bool   `toml:"anonymous_profile"`
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
	Media                  TelegramMediaConfig          `toml:"media"`
	DurableGroups          []TelegramDurableGroupConfig `toml:"durable_groups"`
	ChildBots              []TelegramChildBotConfig     `toml:"child_bots"`
}

type TelegramChildBotConfig struct {
	AgentID            string `toml:"agent_id"`
	TokenFile          string `toml:"token_file"`
	ChatID             int64  `toml:"chat_id"`
	RespondOn          string `toml:"respond_on"`
	ReviewTargetChatID int64  `toml:"review_target_chat_id"`
	Enabled            bool   `toml:"enabled"`
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
	Enabled           bool                  `toml:"enabled"`
	Backend           string                `toml:"backend"`
	CLIPath           string                `toml:"cli_path"`
	SSHPath           string                `toml:"ssh_path"`
	CommandTimeout    string                `toml:"command_timeout"`
	SSHCommandTimeout string                `toml:"ssh_command_timeout"`
	ExpectedTailnet   string                `toml:"expected_tailnet"`
	ExpectedHostname  string                `toml:"expected_hostname"`
	ExpectedTags      []string              `toml:"expected_tags"`
	Parent            TailscaleParentConfig `toml:"parent"`
}

type TailscaleParentConfig struct {
	Enabled         bool     `toml:"enabled"`
	Hostname        string   `toml:"hostname"`
	StateDir        string   `toml:"state_dir"`
	ListenAddr      string   `toml:"listen_addr"`
	AuthKeyEnv      string   `toml:"auth_key_env"`
	AuthKeyFile     string   `toml:"auth_key_file"`
	Tags            []string `toml:"tags"`
	AdminLoginNames []string `toml:"admin_login_names"`
}

type GitHubConfig struct {
	Enabled    bool              `toml:"enabled"`
	APIBaseURL string            `toml:"api_base_url"`
	APIVersion string            `toml:"api_version"`
	Apps       []GitHubAppConfig `toml:"apps"`
}

type GitHubAppConfig struct {
	Name                 string   `toml:"name"`
	AppID                int64    `toml:"app_id"`
	InstallationID       int64    `toml:"installation_id"`
	PrivateKeyFile       string   `toml:"private_key_file"`
	Repositories         []string `toml:"repositories"`
	Permissions          []string `toml:"permissions"`
	AllowAllRepositories bool     `toml:"allow_all_repositories"`
	AllowAllPermissions  bool     `toml:"allow_all_permissions"`
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
	Gemini        GeminiConfig         `toml:"gemini"`
	Ollama        OllamaConfig         `toml:"ollama"`
}

type AnthropicConfig struct {
	APIKey        string `toml:"api_key"`
	Model         string `toml:"model"`
	MaxTokens     int    `toml:"max_tokens"`
	ContextWindow int    `toml:"context_window"`
	CacheStrategy string `toml:"cache_strategy"`
	CacheTTL      string `toml:"cache_ttl"`
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

type GeminiConfig struct {
	APIKey        string `toml:"api_key"`
	BaseURL       string `toml:"base_url"`
	Model         string `toml:"model"`
	MaxTokens     int    `toml:"max_tokens"`
	ContextWindow int    `toml:"context_window"`
}

type OllamaConfig struct {
	BaseURL       string `toml:"base_url"`
	Model         string `toml:"model"`
	MaxTokens     int    `toml:"max_tokens"`
	ContextWindow int    `toml:"context_window"`
}

type OpenAIConfig struct {
	Files        OpenAIFilesConfig        `toml:"files"`
	VectorStores OpenAIVectorStoresConfig `toml:"vector_stores"`
}

type WorkConfig struct {
	Executor  string          `toml:"executor"`
	AutoOrder []string        `toml:"auto_order"`
	Codex     WorkCodexConfig `toml:"codex"`
}

type AutonomyConfig struct {
	DefaultMode         string `toml:"default_mode"`
	Ceiling             string `toml:"ceiling"`
	AllowLiveOverrides  bool   `toml:"allow_live_overrides"`
	MaxOverrideDuration string `toml:"max_override_duration"`
}

type AutonomyPolicy struct {
	DefaultMode         string
	Ceiling             string
	AllowLiveOverrides  bool
	MaxOverrideDuration time.Duration
}

type WorkCodexConfig struct {
	AppServerAddress string `toml:"app_server_address"`
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

type RecoveryConfig struct {
	Watchdog RecoveryWatchdogConfig `toml:"watchdog"`
}

type RecoveryWatchdogConfig struct {
	Enabled            bool   `toml:"enabled"`
	StaleTurnThreshold string `toml:"stale_turn_threshold"`
	StaleTurnLimit     int    `toml:"stale_turn_limit"`
}

type AgentConfig struct {
	Workspace              string   `toml:"-"`
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

type SandboxConfig struct {
	Profiles SandboxProfilesConfig `toml:"profiles"`
}

type SandboxProfilesConfig struct {
	Admin        SandboxProfileConfig `toml:"admin"`
	ApprovedUser SandboxProfileConfig `toml:"approved_user"`
	DurableAgent SandboxProfileConfig `toml:"durable_agent"`
}

type SandboxProfileConfig struct {
	Mode          string   `toml:"mode"`
	ReadonlyRoot  bool     `toml:"readonly_root"`
	WritablePaths []string `toml:"writable_paths"`
	ReadonlyPaths []string `toml:"readonly_paths"`
	HiddenPaths   []string `toml:"hidden_paths"`
	Network       string   `toml:"network"`
	NetworkAllow  []string `toml:"network_allow"`
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

type NocturneConfig struct {
	Enabled      bool   `toml:"enabled"`
	CheckEvery   string `toml:"check_every"`
	WindowStart  string `toml:"window_start"`
	WindowEnd    string `toml:"window_end"`
	Timezone     string `toml:"timezone"`
	ArtifactDir  string `toml:"artifact_dir"`
	Prompt       string `toml:"prompt"`
	Confirmation string `toml:"confirmation"`
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
	return strings.TrimSpace(a.PromptRoot)
}

func (a AgentConfig) EffectiveExecRoot() string {
	return strings.TrimSpace(a.ExecRoot)
}

func (a AgentConfig) EffectiveSharedMemoryRoot() string {
	return strings.TrimSpace(a.SharedMemoryRoot)
}

func (a AgentConfig) EffectiveUserWorkspaceRoot() string {
	return strings.TrimSpace(a.UserWorkspaceRoot)
}

func (a AgentConfig) EffectiveUserMemoryRoot() string {
	return strings.TrimSpace(a.UserMemoryRoot)
}

func Default() Config {
	return Config{
		Identity: IdentityConfig{
			UserAgent:        "",
			ProjectName:      "aphelion",
			GovernorName:     "",
			FaceName:         "",
			AnonymousProfile: false,
		},
		Telegram: TelegramConfig{
			DetachPendingOnRestart: true,
			PollTimeout:            30,
			StreamEditInterval:     "300ms",
			StreamCursor:           " ▉",
			ToolProgress:           "all",
			ToolProgressStyle:      "semantic",
			ToolProgressWindow:     4,
			ToolProgressCleanup:    false,
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
				CacheStrategy: "explicit",
				CacheTTL:      "5m",
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
			Gemini: GeminiConfig{
				BaseURL:       "https://generativelanguage.googleapis.com/v1beta",
				Model:         "gemini-3.1-pro",
				MaxTokens:     16384,
				ContextWindow: 1048576,
			},
			Ollama: OllamaConfig{
				BaseURL:       "http://localhost:11434",
				Model:         "llama3.2",
				MaxTokens:     4096,
				ContextWindow: 128000,
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
		Work: WorkConfig{
			Executor:  "auto",
			AutoOrder: []string{"native", "codex"},
		},
		Autonomy: AutonomyConfig{
			DefaultMode:         "ask_first",
			Ceiling:             "leased",
			AllowLiveOverrides:  true,
			MaxOverrideDuration: "4h",
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
		Recovery: RecoveryConfig{
			Watchdog: RecoveryWatchdogConfig{
				Enabled:            true,
				StaleTurnThreshold: "3m",
				StaleTurnLimit:     8,
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
		Sandbox: SandboxConfig{
			Profiles: SandboxProfilesConfig{
				Admin: SandboxProfileConfig{
					Mode:    "trusted",
					Network: "allowlist",
				},
				ApprovedUser: SandboxProfileConfig{
					Mode:          "isolated",
					ReadonlyRoot:  true,
					WritablePaths: []string{"{user_workspace}", "{user_memory}", "/tmp"},
					ReadonlyPaths: []string{"{global_root}", "{shared_memory_root}"},
					HiddenPaths: []string{
						"~/.aphelion/aphelion.toml",
						"~/.config/aphelion/config.toml",
						"~/.ssh",
						"~/.gnupg",
					},
					Network: "deny",
				},
				DurableAgent: SandboxProfileConfig{
					Mode:          "isolated",
					ReadonlyRoot:  true,
					WritablePaths: []string{"{working_root}", "{shared_memory_root}", "/tmp"},
					ReadonlyPaths: []string{"{global_root}"},
					HiddenPaths: []string{
						"~/.aphelion/aphelion.toml",
						"~/.config/aphelion/config.toml",
						"~/.ssh",
						"~/.gnupg",
					},
					Network: "deny",
				},
			},
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
		Nocturne: NocturneConfig{
			Enabled:      false,
			CheckEvery:   "15m",
			WindowStart:  "23:00",
			WindowEnd:    "07:00",
			ArtifactDir:  "memory/nocturne",
			Confirmation: "Nocturne happened",
		},
		Voice: VoiceConfig{
			Mode:              "off",
			OpenAIModel:       "whisper-1",
			ElevenLabsModelID: "eleven_multilingual_v2",
		},
		DurableAgents: DurableAgentsConfig{},
		Tailscale: TailscaleConfig{
			Enabled:           false,
			Backend:           "cli",
			CLIPath:           "tailscale",
			SSHPath:           "ssh",
			CommandTimeout:    "5s",
			SSHCommandTimeout: "15m",
			Parent: TailscaleParentConfig{
				Enabled:    false,
				Hostname:   "aphelion",
				StateDir:   "~/.aphelion/state/tailnet/parent",
				ListenAddr: ":8765",
				AuthKeyEnv: "APHELION_TS_AUTHKEY",
			},
		},
		GitHub: GitHubConfig{
			Enabled:    false,
			APIBaseURL: "https://api.github.com",
			APIVersion: "2026-03-10",
		},
	}
}
