//go:build linux

package main

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func remoteHostSSHTimeoutFromConfig(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 15 * time.Minute
	}
	raw := strings.TrimSpace(cfg.Tailscale.SSHCommandTimeout)
	if raw == "" {
		return 15 * time.Minute
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return 15 * time.Minute
	}
	return timeout
}
