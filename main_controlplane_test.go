//go:build linux

package main

import (
	"path/filepath"
	"testing"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/session"
)

func TestDurableAgentControlPlaneServerDisabledByDefault(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	server := durableAgentControlPlaneServer(&config.Config{}, store)
	if server != nil {
		t.Fatalf("durableAgentControlPlaneServer() = %#v, want nil when disabled", server)
	}
}

func TestDurableAgentControlPlaneServerUsesConfiguredListenAddress(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := &config.Config{
		DurableAgents: config.DurableAgentsConfig{
			ControlPlane: config.DurableAgentControlPlaneConfig{
				Enabled: true,
				Listen:  "127.0.0.1:8787",
			},
		},
	}
	server := durableAgentControlPlaneServer(cfg, store)
	if server == nil {
		t.Fatal("durableAgentControlPlaneServer() = nil, want configured server")
	}
	if server.Addr != "127.0.0.1:8787" {
		t.Fatalf("server.Addr = %q, want 127.0.0.1:8787", server.Addr)
	}
	if server.Handler == nil {
		t.Fatal("server.Handler = nil, want durable agent control-plane handler")
	}
}
