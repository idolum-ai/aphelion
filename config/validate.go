//go:build linux

package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

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
	if _, err := ParseByteSize(strings.TrimSpace(cfg.Telegram.Media.DownloadMaxSize)); err != nil {
		return fmt.Errorf("telegram.media.download_max_size must be a valid positive size: %w", err)
	}
	if _, err := ParseByteSize(strings.TrimSpace(cfg.Telegram.Media.MaxPDFBytes)); err != nil {
		return fmt.Errorf("telegram.media.max_pdf_bytes must be a valid positive size: %w", err)
	}
	if err := validateTelegramDurableGroups(cfg); err != nil {
		return err
	}
	if err := validateTelegramChildBots(cfg); err != nil {
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
		if !isNativeProviderName(providerName(name)) {
			return fmt.Errorf("providers.auto_order[%d] must be one of anthropic|openai|openrouter|gemini|ollama", i)
		}
	}
	switch providerName(strings.TrimSpace(cfg.Providers.Default)) {
	case "":
	case "anthropic", "openai", "openrouter", "gemini", "ollama":
	default:
		return fmt.Errorf("providers.default must be one of anthropic|openai|openrouter|gemini|ollama")
	}
	for i, name := range cfg.Providers.FallbackChain {
		if providerName(name) == "" {
			return fmt.Errorf("providers.fallback_chain[%d] must not be empty", i)
		}
		if !isNativeProviderName(providerName(name)) {
			return fmt.Errorf("providers.fallback_chain[%d] must be one of anthropic|openai|openrouter|gemini|ollama", i)
		}
	}
	switch normalizeWorkExecutor(cfg.Work.Executor) {
	case "auto", "codex", "native":
	default:
		return fmt.Errorf("work.executor must be one of auto|codex|native")
	}
	if len(cfg.Work.AutoOrder) == 0 {
		return fmt.Errorf("work.auto_order must contain at least one executor")
	}
	for i, name := range cfg.Work.AutoOrder {
		switch normalizeWorkExecutor(name) {
		case "codex", "native":
		default:
			return fmt.Errorf("work.auto_order[%d] must be one of codex|native", i)
		}
	}
	if err := validateAutonomyConfig(cfg.Autonomy); err != nil {
		return err
	}
	if err := validateSandboxProfileConfig("sandbox.profiles.admin", cfg.Sandbox.Profiles.Admin); err != nil {
		return err
	}
	if err := validateSandboxProfileConfig("sandbox.profiles.approved_user", cfg.Sandbox.Profiles.ApprovedUser); err != nil {
		return err
	}
	if err := validateSandboxProfileConfig("sandbox.profiles.durable_agent", cfg.Sandbox.Profiles.DurableAgent); err != nil {
		return err
	}
	nativePrimary := providerName(firstNonEmpty(strings.TrimSpace(cfg.Governor.NativeProvider), strings.TrimSpace(cfg.Providers.Default)))
	switch nativePrimary {
	case "":
		if nativePrimary == "" && (governorBackend == "native" || faceBackend == "" || faceBackend == "provider") {
			return fmt.Errorf("governor.native_provider or providers.default is required when native provider access is enabled")
		}
	default:
		if !isNativeProviderName(nativePrimary) {
			return fmt.Errorf("governor.native_provider must be one of anthropic|openai|openrouter|gemini|ollama")
		}
	}
	needsNativeProvider := governorBackend == "native" || faceBackend == "" || faceBackend == "provider" || len(cfg.Providers.FallbackChain) > 0
	if needsNativeProvider && nativePrimary == "" {
		return fmt.Errorf("governor.native_provider is required when native provider access is enabled")
	}
	if cfg.Providers.Anthropic.ContextWindow <= 0 {
		return fmt.Errorf("providers.anthropic.context_window must be > 0")
	}
	switch cfg.Providers.Anthropic.CacheStrategy {
	case "auto", "explicit", "hybrid", "off":
	default:
		return fmt.Errorf("providers.anthropic.cache_strategy must be one of auto|explicit|hybrid|off")
	}
	switch cfg.Providers.Anthropic.CacheTTL {
	case "5m", "1h":
	default:
		return fmt.Errorf("providers.anthropic.cache_ttl must be one of 5m|1h")
	}
	if cfg.Providers.OpenAI.ContextWindow <= 0 {
		return fmt.Errorf("providers.openai.context_window must be > 0")
	}
	if cfg.Providers.OpenRouter.ContextWindow <= 0 {
		return fmt.Errorf("providers.openrouter.context_window must be > 0")
	}
	if cfg.Providers.Gemini.ContextWindow <= 0 {
		return fmt.Errorf("providers.gemini.context_window must be > 0")
	}
	if cfg.Providers.Ollama.ContextWindow <= 0 {
		return fmt.Errorf("providers.ollama.context_window must be > 0")
	}
	if strings.TrimSpace(cfg.Providers.OpenAI.BaseURL) == "" {
		return fmt.Errorf("providers.openai.base_url is required")
	}
	if strings.TrimSpace(cfg.Providers.OpenRouter.BaseURL) == "" {
		return fmt.Errorf("providers.openrouter.base_url is required")
	}
	if strings.TrimSpace(cfg.Providers.Gemini.BaseURL) == "" {
		return fmt.Errorf("providers.gemini.base_url is required")
	}
	if strings.TrimSpace(cfg.Providers.Ollama.BaseURL) == "" {
		return fmt.Errorf("providers.ollama.base_url is required")
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
				if strings.TrimSpace(cfg.Providers.OpenRouter.Model) == "" {
					return fmt.Errorf("providers.openrouter.model is required when openrouter is in the native provider chain")
				}
			case "gemini":
				if strings.TrimSpace(cfg.Providers.Gemini.APIKey) == "" {
					return fmt.Errorf("providers.gemini.api_key is required when gemini is in the native provider chain")
				}
				if strings.TrimSpace(cfg.Providers.Gemini.Model) == "" {
					return fmt.Errorf("providers.gemini.model is required when gemini is in the native provider chain")
				}
			case "ollama":
				if strings.TrimSpace(cfg.Providers.Ollama.BaseURL) == "" {
					return fmt.Errorf("providers.ollama.base_url is required when ollama is in the native provider chain")
				}
				if strings.TrimSpace(cfg.Providers.Ollama.Model) == "" {
					return fmt.Errorf("providers.ollama.model is required when ollama is in the native provider chain")
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
	if cfg.DurableAgents.ControlPlane.Enabled &&
		strings.TrimSpace(cfg.DurableAgents.ControlPlane.CertFile) == "" &&
		!durableAgentControlPlaneListenIsLoopback(cfg.DurableAgents.ControlPlane.Listen) {
		return fmt.Errorf("durable_agents.control_plane.listen may use plaintext only on loopback; configure cert_file/key_file for non-loopback listeners")
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

func validateTelegramChildBots(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	seenChats := make(map[int64]string, len(cfg.Telegram.ChildBots))
	seenAgents := make(map[string]int64, len(cfg.Telegram.ChildBots))
	durableGroupChats := make(map[int64]string, len(cfg.Telegram.DurableGroups))
	for _, group := range cfg.Telegram.DurableGroups {
		if group.ChatID != 0 && strings.TrimSpace(group.AgentID) != "" {
			durableGroupChats[group.ChatID] = strings.TrimSpace(group.AgentID)
		}
	}
	defaultReviewTarget := int64(0)
	if len(cfg.Principals.Telegram.AdminUserIDs) > 0 {
		defaultReviewTarget = cfg.Principals.Telegram.AdminUserIDs[0]
	}
	for i, bot := range cfg.Telegram.ChildBots {
		agentID := strings.TrimSpace(bot.AgentID)
		if agentID == "" {
			return fmt.Errorf("telegram.child_bots[%d].agent_id is required", i)
		}
		if !isSafeDurableAgentID(agentID) {
			return fmt.Errorf("telegram.child_bots[%d].agent_id must contain only letters, digits, _, or -", i)
		}
		if strings.TrimSpace(bot.TokenFile) == "" {
			return fmt.Errorf("telegram.child_bots[%d].token_file is required", i)
		}
		if bot.ChatID == 0 {
			return fmt.Errorf("telegram.child_bots[%d].chat_id is required", i)
		}
		if existing, ok := seenChats[bot.ChatID]; ok {
			return fmt.Errorf("telegram.child_bots[%d].chat_id duplicates child bot %q", i, existing)
		}
		if existing, ok := durableGroupChats[bot.ChatID]; ok {
			return fmt.Errorf("telegram.child_bots[%d].chat_id duplicates telegram.durable_groups route %q", i, existing)
		}
		if existing, ok := seenAgents[agentID]; ok {
			return fmt.Errorf("telegram.child_bots[%d].agent_id duplicates chat_id %d", i, existing)
		}
		switch normalizeTelegramDurableGroupRespondOn(bot.RespondOn) {
		case "all", "mentions":
		default:
			return fmt.Errorf("telegram.child_bots[%d].respond_on must be one of all|mentions", i)
		}
		if bot.ReviewTargetChatID == 0 && defaultReviewTarget == 0 {
			return fmt.Errorf("telegram.child_bots[%d].review_target_chat_id is required when no admin_user_ids are configured", i)
		}
		if bot.ReviewTargetChatID < 0 {
			return fmt.Errorf("telegram.child_bots[%d].review_target_chat_id must be positive", i)
		}
		seenChats[bot.ChatID] = agentID
		seenAgents[agentID] = bot.ChatID
	}
	return nil
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
			return fmt.Errorf("telegram.durable_groups[%d].agent_id must contain only letters, digits, _, or -", i)
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
			if !isNativeProviderName(group.LLMProvider) {
				return fmt.Errorf("telegram.durable_groups[%d].llm_provider must be one of anthropic|openai|openrouter|gemini|ollama for native backend", i)
			}
			if group.LLMProvider != "ollama" && strings.TrimSpace(group.LLMAPIKey) == "" {
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
	cfg.Tailscale.Parent.Hostname = strings.TrimSpace(cfg.Tailscale.Parent.Hostname)
	cfg.Tailscale.Parent.StateDir = strings.TrimSpace(cfg.Tailscale.Parent.StateDir)
	cfg.Tailscale.Parent.ListenAddr = strings.TrimSpace(cfg.Tailscale.Parent.ListenAddr)
	cfg.Tailscale.Parent.AuthKeyEnv = strings.TrimSpace(cfg.Tailscale.Parent.AuthKeyEnv)
	cfg.Tailscale.Parent.AuthKeyFile = strings.TrimSpace(cfg.Tailscale.Parent.AuthKeyFile)
	cfg.Tailscale.Parent.Tags = normalizeStringList(cfg.Tailscale.Parent.Tags)
	cfg.Tailscale.Parent.AdminLoginNames = normalizeStringList(cfg.Tailscale.Parent.AdminLoginNames)
	if cfg.Tailscale.Parent.Hostname == "" {
		cfg.Tailscale.Parent.Hostname = "aphelion"
	}
	if cfg.Tailscale.Parent.StateDir == "" {
		cfg.Tailscale.Parent.StateDir = defaultHomePath(".aphelion", "state", "tailnet", "parent")
	}
	if cfg.Tailscale.Parent.ListenAddr == "" {
		cfg.Tailscale.Parent.ListenAddr = ":8765"
	}
	if cfg.Tailscale.Parent.AuthKeyEnv == "" {
		cfg.Tailscale.Parent.AuthKeyEnv = "APHELION_TS_AUTHKEY"
	}
	if cfg.Tailscale.Parent.Enabled && !cfg.Tailscale.Enabled {
		return fmt.Errorf("tailscale.enabled must be true when tailscale.parent.enabled is true")
	}
	return nil
}

func isSafeDurableAgentID(value string) bool {
	return core.ValidateDurableAgentID(value) == nil
}

func validateSandboxProfileConfig(path string, profile SandboxProfileConfig) error {
	switch profile.Mode {
	case "trusted", "isolated":
	default:
		return fmt.Errorf("%s.mode must be one of trusted|isolated", path)
	}
	switch profile.Network {
	case "allowlist", "deny":
	default:
		return fmt.Errorf("%s.network must be one of allowlist|deny", path)
	}
	for i, destination := range profile.NetworkAllow {
		if err := validateSandboxNetworkDestination(destination); err != nil {
			return fmt.Errorf("%s.network_allow[%d]: %w", path, i, err)
		}
	}
	if profile.Network == "allowlist" && profile.Mode == "isolated" && len(profile.NetworkAllow) == 0 {
		return fmt.Errorf("%s.network_allow must contain at least one host:port, ip:port, or cidr:port when isolated network=allowlist", path)
	}
	return nil
}

func validateSandboxNetworkDestination(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fmt.Errorf("destination is required")
	}
	host, portRaw, err := net.SplitHostPort(value)
	if err != nil {
		return fmt.Errorf("destination must include an explicit port: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("host is required")
	}
	if _, err := netip.ParsePrefix(host); err != nil {
		if _, err := netip.ParseAddr(host); err != nil {
			if err := validateSandboxNetworkHostname(host); err != nil {
				return err
			}
		}
	}
	port, err := strconv.Atoi(strings.TrimSpace(portRaw))
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func validateSandboxNetworkHostname(host string) error {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || len(host) > 253 {
		return fmt.Errorf("hostname length is invalid")
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("hostname contains an empty label")
		}
		if len(label) > 63 {
			return fmt.Errorf("hostname label %q is too long", label)
		}
		for i, ch := range label {
			valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-'
			if !valid {
				return fmt.Errorf("hostname label %q contains %q", label, ch)
			}
			if (i == 0 || i == len(label)-1) && ch == '-' {
				return fmt.Errorf("hostname label %q starts or ends with '-'", label)
			}
		}
	}
	return nil
}
