//go:build linux

package config

import (
	"fmt"
	"strings"
	"time"
)

func validateAgentBudgetConfig(cfg AgentConfig) error {
	if cfg.MaxIterations <= 0 {
		return fmt.Errorf("agent.max_iterations must be > 0")
	}
	if cfg.ToolTimeout <= 0 {
		return fmt.Errorf("agent.tool_timeout must be > 0")
	}
	if cfg.BootstrapMaxChars <= 0 {
		return fmt.Errorf("agent.bootstrap_max_chars must be > 0")
	}
	if cfg.BootstrapTotalMaxChars <= 0 {
		return fmt.Errorf("agent.bootstrap_total_max_chars must be > 0")
	}
	return nil
}

func validateAgentRootsConfig(cfg AgentConfig) error {
	if strings.TrimSpace(cfg.EffectivePromptRoot()) == "" {
		return fmt.Errorf("agent.prompt_root is required")
	}
	if strings.TrimSpace(cfg.EffectiveExecRoot()) == "" {
		return fmt.Errorf("agent.exec_root is required")
	}
	if strings.TrimSpace(cfg.EffectiveSharedMemoryRoot()) == "" {
		return fmt.Errorf("agent.shared_memory_root is required")
	}
	if strings.TrimSpace(cfg.EffectiveUserWorkspaceRoot()) == "" {
		return fmt.Errorf("agent.user_workspace_root is required")
	}
	if strings.TrimSpace(cfg.EffectiveUserMemoryRoot()) == "" {
		return fmt.Errorf("agent.user_memory_root is required")
	}
	if len(cfg.BootstrapFiles) == 0 {
		return fmt.Errorf("agent.bootstrap_files must not be empty")
	}
	return nil
}

func validateAgentDailyNotesConfig(cfg AgentConfig) error {
	if strings.TrimSpace(cfg.DailyNotesDir) == "" {
		return fmt.Errorf("agent.daily_notes_dir is required")
	}
	return nil
}

func validateSessionsConfig(cfg SessionsConfig) error {
	if strings.TrimSpace(cfg.DBPath) == "" {
		return fmt.Errorf("sessions.db_path is required")
	}
	if strings.TrimSpace(cfg.IdleExpiry) == "" {
		return fmt.Errorf("sessions.idle_expiry is required")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.IdleExpiry)); err != nil {
		return fmt.Errorf("sessions.idle_expiry must be a valid duration: %w", err)
	}
	if cfg.MaxContextRatio <= 0 || cfg.MaxContextRatio >= 1 {
		return fmt.Errorf("sessions.max_context_ratio must be > 0 and < 1")
	}
	if cfg.CompactionRatio <= 0 || cfg.CompactionRatio >= 1 {
		return fmt.Errorf("sessions.compaction_ratio must be > 0 and < 1")
	}
	if cfg.CompactionRatio >= cfg.MaxContextRatio {
		return fmt.Errorf("sessions.compaction_ratio must be < max_context_ratio")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.CompactionStrategy)) {
	case "", "summarize", "truncate":
	default:
		return fmt.Errorf("sessions.compaction_strategy must be one of summarize|truncate")
	}
	maxAge, err := time.ParseDuration(strings.TrimSpace(cfg.TESRetention.MaxAge))
	if err != nil {
		return fmt.Errorf("sessions.tes_retention.max_age must be a valid duration: %w", err)
	}
	if maxAge < 24*time.Hour {
		return fmt.Errorf("sessions.tes_retention.max_age must be >= 24h")
	}
	if cfg.TESRetention.MinRetainedRows < 100 {
		return fmt.Errorf("sessions.tes_retention.min_retained_rows must be >= 100")
	}
	if cfg.TESRetention.MaxDeletePerGC <= 0 {
		return fmt.Errorf("sessions.tes_retention.max_delete_per_gc must be > 0")
	}
	if cfg.TESRetention.MaxDeletePerGC > cfg.TESRetention.MinRetainedRows {
		return fmt.Errorf("sessions.tes_retention.max_delete_per_gc must be <= min_retained_rows")
	}
	if cfg.TESRetention.Enabled && strings.TrimSpace(cfg.TESRetention.ExportDir) == "" {
		return fmt.Errorf("sessions.tes_retention.export_dir is required when retention is enabled")
	}
	return nil
}

func validateRecoveryConfig(cfg RecoveryConfig) error {
	if strings.TrimSpace(cfg.Watchdog.StaleTurnThreshold) == "" {
		return fmt.Errorf("recovery.watchdog.stale_turn_threshold is required")
	}
	if threshold, err := time.ParseDuration(strings.TrimSpace(cfg.Watchdog.StaleTurnThreshold)); err != nil {
		return fmt.Errorf("recovery.watchdog.stale_turn_threshold must be a valid duration: %w", err)
	} else if threshold <= 0 {
		return fmt.Errorf("recovery.watchdog.stale_turn_threshold must be > 0")
	}
	if cfg.Watchdog.StaleTurnLimit <= 0 {
		return fmt.Errorf("recovery.watchdog.stale_turn_limit must be > 0")
	}
	return nil
}

func validateMemoryConfig(cfg MemoryConfig) error {
	if strings.TrimSpace(cfg.Reflection.Every) == "" {
		return fmt.Errorf("memory.reflection.every is required")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.Reflection.Every)); err != nil {
		return fmt.Errorf("memory.reflection.every must be a valid duration: %w", err)
	}
	if cfg.Decay.HotDays <= 0 {
		return fmt.Errorf("memory.decay.hot_days must be > 0")
	}
	if cfg.Decay.WarmDays <= 0 {
		return fmt.Errorf("memory.decay.warm_days must be > 0")
	}
	if cfg.Decay.ColdDays <= 0 {
		return fmt.Errorf("memory.decay.cold_days must be > 0")
	}
	if cfg.Decay.HotDays > cfg.Decay.WarmDays {
		return fmt.Errorf("memory.decay.hot_days must be <= warm_days")
	}
	if cfg.Decay.WarmDays > cfg.Decay.ColdDays {
		return fmt.Errorf("memory.decay.warm_days must be <= cold_days")
	}
	if len(cfg.Identity.Preserve) == 0 {
		return fmt.Errorf("memory.identity.preserve must not be empty")
	}
	if err := validateMemoryWritePolicy(cfg.WritePolicy); err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Semantic.Backend)) {
	case "", "local":
	default:
		return fmt.Errorf("memory.semantic.backend must be one of local")
	}
	refresh := strings.ToLower(strings.TrimSpace(cfg.Semantic.Refresh))
	switch refresh {
	case "", "manual", "heartbeat":
	default:
		if _, err := time.ParseDuration(strings.TrimSpace(cfg.Semantic.Refresh)); err != nil {
			return fmt.Errorf("memory.semantic.refresh must be manual|heartbeat|<duration>: %w", err)
		}
	}
	if cfg.Semantic.InteractiveTopK <= 0 {
		return fmt.Errorf("memory.semantic.interactive_top_k must be > 0")
	}
	if cfg.Semantic.HeartbeatTopK <= 0 {
		return fmt.Errorf("memory.semantic.heartbeat_top_k must be > 0")
	}
	if cfg.Semantic.InteractiveMaxChars <= 0 {
		return fmt.Errorf("memory.semantic.interactive_max_chars must be > 0")
	}
	if cfg.Semantic.HeartbeatMaxChars <= 0 {
		return fmt.Errorf("memory.semantic.heartbeat_max_chars must be > 0")
	}
	return nil
}
