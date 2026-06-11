//go:build linux

package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

func validateHeartbeatConfig(cfg HeartbeatConfig) error {
	if strings.TrimSpace(cfg.Every) == "" {
		return fmt.Errorf("heartbeat.every is required")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.Every)); err != nil {
		return fmt.Errorf("heartbeat.every must be a valid duration: %w", err)
	}
	switch target := strings.TrimSpace(cfg.Target); target {
	case "", "none", "last":
	default:
		if _, err := parsePositiveInt64(target); err != nil {
			return fmt.Errorf("heartbeat.target must be one of none|last|<admin_chat_id>")
		}
	}
	if _, err := validateClock(cfg.ActiveHours.Start, "heartbeat.active_hours.start"); err != nil {
		return err
	}
	if _, err := validateClock(cfg.ActiveHours.End, "heartbeat.active_hours.end"); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.ActiveHours.Timezone) != "" {
		if _, err := time.LoadLocation(strings.TrimSpace(cfg.ActiveHours.Timezone)); err != nil {
			return fmt.Errorf("heartbeat.active_hours.timezone must be a valid IANA timezone: %w", err)
		}
	}
	return nil
}

func validateCuriosityConfig(cfg CuriosityConfig) error {
	if strings.TrimSpace(cfg.Every) == "" {
		return fmt.Errorf("curiosity.every is required")
	}
	if cadence, err := time.ParseDuration(strings.TrimSpace(cfg.Every)); err != nil {
		return fmt.Errorf("curiosity.every must be a valid duration: %w", err)
	} else if cadence <= 0 {
		return fmt.Errorf("curiosity.every must be positive")
	}
	if strings.TrimSpace(cfg.LeaseTTL) == "" {
		return fmt.Errorf("curiosity.lease_ttl is required")
	}
	if ttl, err := time.ParseDuration(strings.TrimSpace(cfg.LeaseTTL)); err != nil {
		return fmt.Errorf("curiosity.lease_ttl must be a valid duration: %w", err)
	} else if ttl <= 0 {
		return fmt.Errorf("curiosity.lease_ttl must be positive")
	}
	if cfg.DailyTurnBudget < 0 {
		return fmt.Errorf("curiosity.daily_turn_budget must be >= 0")
	}
	if cfg.Enabled && cfg.DailyTurnBudget <= 0 {
		return fmt.Errorf("curiosity.daily_turn_budget must be > 0 when curiosity is enabled")
	}
	if cfg.MaxLooksPerTurn < 0 {
		return fmt.Errorf("curiosity.max_looks_per_turn must be >= 0")
	}
	if cfg.Enabled && cfg.MaxLooksPerTurn <= 0 {
		return fmt.Errorf("curiosity.max_looks_per_turn must be > 0 when curiosity is enabled")
	}
	if cfg.MinSignalIntensity <= 0 || cfg.MinSignalIntensity > 1 {
		return fmt.Errorf("curiosity.min_signal_intensity must be > 0 and <= 1")
	}
	for i, source := range cfg.SourceClasses {
		switch strings.ToLower(strings.TrimSpace(source)) {
		case "session", "memory", "workspace", "url":
		default:
			return fmt.Errorf("curiosity.source_classes[%d] must be one of session|memory|workspace|url", i)
		}
	}
	for i, path := range cfg.WorkspacePaths {
		if err := validateCuriosityRelativePath(path); err != nil {
			return fmt.Errorf("curiosity.workspace_paths[%d]: %w", i, err)
		}
	}
	for i, path := range cfg.MemoryPaths {
		if err := validateCuriosityRelativePath(path); err != nil {
			return fmt.Errorf("curiosity.memory_paths[%d]: %w", i, err)
		}
	}
	for i, raw := range cfg.AllowlistedURLs {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed == nil {
			return fmt.Errorf("curiosity.allowlisted_urls[%d] must be a valid URL", i)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("curiosity.allowlisted_urls[%d] must use http or https", i)
		}
		if parsed.User != nil {
			return fmt.Errorf("curiosity.allowlisted_urls[%d] must not include embedded credentials", i)
		}
		if strings.TrimSpace(parsed.Host) == "" {
			return fmt.Errorf("curiosity.allowlisted_urls[%d] must include a host", i)
		}
	}
	return nil
}

func validateCuriosityRelativePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("path must be relative")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path must stay inside the configured root")
	}
	return nil
}

func validateCronConfig(cfg CronConfig) error {
	for i, job := range cfg.Jobs {
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
	return nil
}

func validateVoiceConfig(cfg VoiceConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.Mode)) {
	case "", "off", "auto", "all":
	default:
		return fmt.Errorf("voice.mode must be one of off|auto|all")
	}
	if strings.TrimSpace(cfg.Mode) != "" && !strings.EqualFold(strings.TrimSpace(cfg.Mode), "off") {
		if strings.TrimSpace(cfg.OpenAIAPIKey) == "" {
			return fmt.Errorf("voice.openai_api_key is required when voice.mode is enabled")
		}
		if strings.TrimSpace(cfg.OpenAIModel) == "" {
			return fmt.Errorf("voice.openai_model is required when voice.mode is enabled")
		}
		if strings.TrimSpace(cfg.ElevenLabsAPIKey) == "" {
			return fmt.Errorf("voice.elevenlabs_api_key is required when voice.mode is enabled")
		}
		if strings.TrimSpace(cfg.ElevenLabsVoiceID) == "" {
			return fmt.Errorf("voice.elevenlabs_voice_id is required when voice.mode is enabled")
		}
	}
	return nil
}

func validateOpenAIStorageConfig(cfg *Config) error {
	if cfg.OpenAI.Files.Enabled || cfg.OpenAI.VectorStores.Enabled {
		if strings.TrimSpace(cfg.Providers.OpenAI.APIKey) == "" {
			return fmt.Errorf("providers.openai.api_key is required when OpenAI platform storage is enabled")
		}
		if cfg.OpenAI.Files.Enabled && strings.TrimSpace(cfg.OpenAI.Files.Purpose) == "" {
			return fmt.Errorf("openai.files.purpose is required when openai.files.enabled = true")
		}
	}
	return nil
}

func validatePrincipalCountConfig(cfg PrincipalsConfig) error {
	if len(cfg.Telegram.AdminUserIDs) == 0 {
		return fmt.Errorf("principals.telegram.admin_user_ids must contain at least one user id; add [principals.telegram] admin_user_ids = [123456789]")
	}
	if len(cfg.Telegram.AdminUserIDs) != 1 {
		return fmt.Errorf("principals.telegram.admin_user_ids must contain exactly one user id")
	}
	return nil
}

func validatePrincipalDetailsConfig(cfg PrincipalsConfig) error {
	admin := make(map[int64]struct{}, len(cfg.Telegram.AdminUserIDs))
	for _, id := range cfg.Telegram.AdminUserIDs {
		if id <= 0 {
			return fmt.Errorf("principals.telegram.admin_user_ids must contain positive user ids")
		}
		if _, exists := admin[id]; exists {
			return fmt.Errorf("principals.telegram.admin_user_ids contains duplicate user id %d", id)
		}
		admin[id] = struct{}{}
	}
	if len(cfg.Telegram.ApprovedUserIDs) > 0 {
		return fmt.Errorf("principals.telegram.approved_user_ids is not supported; use durable-agent access grants instead")
	}
	return nil
}

func validateDurableAgentsConfig(cfg DurableAgentsConfig) error {
	if cfg.ControlPlane.Enabled && strings.TrimSpace(cfg.ControlPlane.Listen) == "" {
		return fmt.Errorf("durable_agents.control_plane.listen is required when durable_agents.control_plane.enabled = true")
	}
	if strings.TrimSpace(cfg.ControlPlane.BasePath) != "" && !strings.HasPrefix(strings.TrimSpace(cfg.ControlPlane.BasePath), "/") {
		return fmt.Errorf("durable_agents.control_plane.base_path must start with / when set")
	}
	if (strings.TrimSpace(cfg.ControlPlane.CertFile) == "") != (strings.TrimSpace(cfg.ControlPlane.KeyFile) == "") {
		return fmt.Errorf("durable_agents.control_plane.cert_file and key_file must be set together")
	}
	if cfg.ControlPlane.Enabled &&
		strings.TrimSpace(cfg.ControlPlane.CertFile) == "" &&
		!durableAgentControlPlaneListenIsLoopback(cfg.ControlPlane.Listen) {
		return fmt.Errorf("durable_agents.control_plane.listen may use plaintext only on loopback; configure cert_file/key_file for non-loopback listeners")
	}
	return nil
}
