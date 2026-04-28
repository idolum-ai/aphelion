//go:build linux

package tailnet

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCLIBackendLiveSmoke(t *testing.T) {
	if os.Getenv("APHELION_TAILSCALE_INTEGRATION") != "1" {
		t.Skip("set APHELION_TAILSCALE_INTEGRATION=1 to run live Tailscale smoke tests")
	}
	backend := NewCLIBackend(CLIOptions{CommandTimeout: 10 * time.Second})
	snapshot, err := backend.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() err = %v", err)
	}
	if snapshot.Status == "" || snapshot.Status == "unknown" {
		t.Fatalf("tailnet status = %#v, want concrete status", snapshot)
	}
	if snapshot.RawStatusError != "" {
		t.Fatalf("status error = %q, want tailscale status --json readable", snapshot.RawStatusError)
	}
}
