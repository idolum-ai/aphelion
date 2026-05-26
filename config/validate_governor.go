//go:build linux

package config

import (
	"fmt"
	"strings"
	"time"
)

func validateThinkingConfig(cfg ThinkingConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.Effort)) {
	case "", "none", "low", "medium", "high", "xhigh":
	default:
		return fmt.Errorf("thinking.effort must be one of none|low|medium|high|xhigh")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Summary)) {
	case "", "none", "auto", "compact":
	default:
		return fmt.Errorf("thinking.summary must be one of none|auto|compact")
	}
	for name, value := range map[string]string{
		"thinking.defaults.default":   cfg.Defaults.Default,
		"thinking.defaults.heartbeat": cfg.Defaults.Heartbeat,
		"thinking.defaults.cron":      cfg.Defaults.Cron,
		"thinking.defaults.recovery":  cfg.Defaults.Recovery,
	} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "", "none", "low", "medium", "high", "xhigh":
		default:
			return fmt.Errorf("%s must be one of none|low|medium|high|xhigh", name)
		}
	}
	return nil
}

func validateGovernorConfig(cfg GovernorConfig) (string, error) {
	governorBackend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	switch governorBackend {
	case "auto", "codex", "native":
	default:
		return "", fmt.Errorf("governor.backend must be one of auto|codex|native")
	}
	if err := validateGovernorCodexConfig(cfg.Codex); err != nil {
		return "", err
	}
	if err := validateGovernorBrokerageConfig(cfg.Brokerage); err != nil {
		return "", err
	}
	return governorBackend, nil
}

func validateGovernorCodexConfig(cfg GovernorCodexConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.AuthSource)) {
	case "auto", "codex_cli", "aphelion":
	default:
		return fmt.Errorf("governor.codex.auth_source must be one of auto|codex_cli|aphelion")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return fmt.Errorf("governor.codex.base_url is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("governor.codex.model is required")
	}
	if cfg.ContextWindow <= 0 {
		return fmt.Errorf("governor.codex.context_window must be > 0")
	}
	if cfg.MaxContinuations <= 0 {
		return fmt.Errorf("governor.codex.max_continuations must be > 0")
	}
	if cfg.TransportRetries < 0 {
		return fmt.Errorf("governor.codex.transport_retries must be >= 0")
	}
	if strings.TrimSpace(cfg.ResponseHeaderTimeout) == "" {
		return fmt.Errorf("governor.codex.response_header_timeout is required")
	}
	responseHeaderTimeout, err := time.ParseDuration(strings.TrimSpace(cfg.ResponseHeaderTimeout))
	if err != nil {
		return fmt.Errorf("governor.codex.response_header_timeout must be a valid duration: %w", err)
	}
	if responseHeaderTimeout <= 0 {
		return fmt.Errorf("governor.codex.response_header_timeout must be > 0")
	}
	return nil
}

func validateGovernorBrokerageConfig(cfg BrokerageConfig) error {
	if cfg.MinRounds <= 0 {
		return fmt.Errorf("governor.brokerage.min_rounds must be > 0")
	}
	if cfg.MaxRounds <= 0 {
		return fmt.Errorf("governor.brokerage.max_rounds must be > 0")
	}
	if cfg.AbsoluteMaxRounds <= 0 {
		return fmt.Errorf("governor.brokerage.absolute_max_rounds must be > 0")
	}
	if cfg.MinRounds > cfg.MaxRounds {
		return fmt.Errorf("governor.brokerage.min_rounds must be <= max_rounds")
	}
	if cfg.MaxRounds > cfg.AbsoluteMaxRounds {
		return fmt.Errorf("governor.brokerage.max_rounds must be <= absolute_max_rounds")
	}
	if cfg.StableContractRounds < 2 {
		return fmt.Errorf("governor.brokerage.stable_contract_rounds must be >= 2")
	}
	if elapsed, err := time.ParseDuration(strings.TrimSpace(cfg.MaxElapsed)); err != nil {
		return fmt.Errorf("governor.brokerage.max_elapsed must be a valid duration: %w", err)
	} else if elapsed <= 0 {
		return fmt.Errorf("governor.brokerage.max_elapsed must be > 0")
	}
	return nil
}
