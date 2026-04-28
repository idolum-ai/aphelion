//go:build linux

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestTailnetStatusSnapshotRegistersParentSurface(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := config.Default()
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.ExpectedTailnet = "example.ts.net"
	rt := &Runtime{
		cfg:   &cfg,
		store: store,
		tailnetBackend: fakeTailnetBackend{
			snapshot: core.TailnetStatusSnapshot{
				GeneratedAt:     time.Date(2026, 4, 28, 19, 0, 0, 0, time.UTC),
				Enabled:         true,
				Backend:         "cli",
				Status:          "healthy",
				TailnetName:     "example.ts.net",
				ExpectedTailnet: "example.ts.net",
			},
		},
	}
	rt.SetTailnetParentStatusProvider(func() core.TailnetParentStatus {
		return core.TailnetParentStatus{
			Enabled:     true,
			Running:     true,
			Hostname:    "aphelion",
			ListenAddr:  ":8765",
			MagicDNSURL: "http://aphelion.example.ts.net:8765",
			Tags:        []string{"tag:admin"},
		}
	})

	snapshot, err := rt.TailnetStatusSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TailnetStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Surfaces) != 1 {
		t.Fatalf("surfaces = %#v, want one registered parent surface", snapshot.Surfaces)
	}
	surface := snapshot.Surfaces[0]
	if surface.SurfaceID != "parent:tsnet_http:status" || surface.Status != session.TailnetSurfaceStatusActive || surface.URL != "http://aphelion.example.ts.net:8765/status" {
		t.Fatalf("surface = %#v, want active parent status surface", surface)
	}
	stored, ok, err := store.TailnetSurface("parent:tsnet_http:status")
	if err != nil || !ok {
		t.Fatalf("TailnetSurface() = %#v, %t, %v; want stored", stored, ok, err)
	}
	if stored.URL != surface.URL || stored.TailnetName != "example.ts.net" {
		t.Fatalf("stored = %#v, want URL and tailnet projected", stored)
	}
}

func TestTailnetStatusSnapshotMarksParentSurfaceDegraded(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	cfg := config.Default()
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.ExpectedTailnet = "example.ts.net"
	rt := &Runtime{
		cfg:   &cfg,
		store: store,
		tailnetBackend: fakeTailnetBackend{
			snapshot: core.TailnetStatusSnapshot{
				GeneratedAt:     time.Date(2026, 4, 28, 19, 0, 0, 0, time.UTC),
				Enabled:         true,
				Backend:         "cli",
				Status:          "healthy",
				ExpectedTailnet: "example.ts.net",
			},
		},
	}
	rt.SetTailnetParentStatusProvider(func() core.TailnetParentStatus {
		return core.TailnetParentStatus{
			Enabled:    true,
			Running:    false,
			Hostname:   "aphelion",
			ListenAddr: ":8765",
			LastError:  "parent tsnet: auth key is required",
		}
	})

	snapshot, err := rt.TailnetStatusSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TailnetStatusSnapshot() err = %v", err)
	}
	if len(snapshot.Surfaces) != 1 || snapshot.Surfaces[0].Status != session.TailnetSurfaceStatusDegraded || snapshot.Surfaces[0].LastError == "" {
		t.Fatalf("surfaces = %#v, want degraded parent surface", snapshot.Surfaces)
	}
}

type fakeTailnetBackend struct {
	snapshot core.TailnetStatusSnapshot
	err      error
}

func (b fakeTailnetBackend) Snapshot(context.Context) (core.TailnetStatusSnapshot, error) {
	return b.snapshot, b.err
}
