//go:build linux

package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func validate(cfg *Config) error {
	if err := validateTelegramConfig(cfg); err != nil {
		return err
	}
	if err := validateTailscaleConfig(cfg); err != nil {
		return err
	}
	if err := validateGitHubConfig(cfg); err != nil {
		return err
	}
	if err := validateThinkingConfig(cfg.Thinking); err != nil {
		return err
	}
	governorBackend, err := validateGovernorConfig(cfg.Governor)
	if err != nil {
		return err
	}
	if err := validateAgentBudgetConfig(cfg.Agent); err != nil {
		return err
	}
	if err := validateSessionsConfig(cfg.Sessions); err != nil {
		return err
	}
	if err := validateRecoveryConfig(cfg.Recovery); err != nil {
		return err
	}
	if err := validateAgentRootsConfig(cfg.Agent); err != nil {
		return err
	}
	if err := validateWebSearchConfig(cfg.Tools.WebSearch); err != nil {
		return err
	}
	if err := validateAgentDailyNotesConfig(cfg.Agent); err != nil {
		return err
	}
	if err := validateMemoryConfig(cfg.Memory); err != nil {
		return err
	}
	faceBackend, err := validateFaceConfig(cfg.Face)
	if err != nil {
		return err
	}
	if err := validateProviderAndWorkSelectionConfig(cfg); err != nil {
		return err
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
	if err := validateNativeProviderChainConfig(cfg, governorBackend, faceBackend); err != nil {
		return err
	}
	if err := validateHeartbeatConfig(cfg.Heartbeat); err != nil {
		return err
	}
	if err := validateCronConfig(cfg.Cron); err != nil {
		return err
	}
	if err := validateVoiceConfig(cfg.Voice); err != nil {
		return err
	}
	if err := validateOpenAIStorageConfig(cfg); err != nil {
		return err
	}
	if err := validatePrincipalCountConfig(cfg.Principals); err != nil {
		return err
	}
	if err := validateDurableAgentsConfig(cfg.DurableAgents); err != nil {
		return err
	}
	if err := validatePrincipalDetailsConfig(cfg.Principals); err != nil {
		return err
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
