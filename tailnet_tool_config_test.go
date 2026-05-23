//go:build linux

package main

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func TestRemoteHostSSHTimeoutFromConfig(t *testing.T) {
	t.Parallel()

	if got := remoteHostSSHTimeoutFromConfig(nil); got != 15*time.Minute {
		t.Fatalf("remoteHostSSHTimeoutFromConfig(nil) = %s, want 15m", got)
	}
	if got := remoteHostSSHTimeoutFromConfig(&config.Config{}); got != 15*time.Minute {
		t.Fatalf("remoteHostSSHTimeoutFromConfig(empty) = %s, want 15m", got)
	}
	if got := remoteHostSSHTimeoutFromConfig(&config.Config{Tailscale: config.TailscaleConfig{CommandTimeout: "5s", SSHCommandTimeout: "20m"}}); got != 20*time.Minute {
		t.Fatalf("remoteHostSSHTimeoutFromConfig(explicit) = %s, want 20m", got)
	}
}
