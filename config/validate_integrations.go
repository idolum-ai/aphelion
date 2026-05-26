//go:build linux

package config

import (
	"fmt"
	"strings"
	"time"
)

func validateWebSearchConfig(cfg WebSearchConfig) error {
	if cfg.MaxCount <= 0 {
		return fmt.Errorf("tools.web_search.max_count must be > 0")
	}
	if cfg.DefaultCount <= 0 {
		return fmt.Errorf("tools.web_search.default_count must be > 0")
	}
	if cfg.DefaultCount > cfg.MaxCount {
		return fmt.Errorf("tools.web_search.default_count must be <= max_count")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.Timeout)); err != nil {
		return fmt.Errorf("tools.web_search.timeout must be a valid duration: %w", err)
	}
	if _, err := time.ParseDuration(strings.TrimSpace(cfg.CacheTTL)); err != nil {
		return fmt.Errorf("tools.web_search.cache_ttl must be a valid duration: %w", err)
	}
	for i, provider := range cfg.ProviderOrder {
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "openai_hosted", "brave":
		default:
			return fmt.Errorf("tools.web_search.provider_order[%d] must be one of openai_hosted|brave", i)
		}
	}
	switch strings.ToLower(strings.TrimSpace(cfg.OpenAIHosted.ContextSize)) {
	case "low", "medium", "high":
	default:
		return fmt.Errorf("tools.web_search.openai_hosted.context_size must be one of low|medium|high")
	}
	if strings.TrimSpace(cfg.Brave.Endpoint) == "" {
		return fmt.Errorf("tools.web_search.brave.endpoint is required")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.Brave.Endpoint)), "https://") && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.Brave.Endpoint)), "http://") {
		return fmt.Errorf("tools.web_search.brave.endpoint must be http(s)")
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
	if strings.TrimSpace(cfg.Tailscale.SSHPath) == "" {
		cfg.Tailscale.SSHPath = "ssh"
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
	if raw := strings.TrimSpace(cfg.Tailscale.SSHCommandTimeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("tailscale.ssh_command_timeout must be a valid duration: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("tailscale.ssh_command_timeout must be > 0")
		}
	} else {
		cfg.Tailscale.SSHCommandTimeout = "15m"
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
