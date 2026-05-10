//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestRunTailnetCommandSurfacesAndRevoke(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Sessions.DBPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(db dir) err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:   "parent:tsnet_http:status",
		OwnerKind:   "parent",
		SurfaceKind: "tsnet_http",
		Name:        "status",
		URL:         "https://aphelion.example.ts.net/status",
		Status:      session.TailnetSurfaceStatusActive,
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface() err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	surfacesOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"surfaces", "--config", cfgPath, "--format=kv"})
	})
	if err != nil {
		t.Fatalf("tailnet surfaces err = %v", err)
	}
	if !strings.Contains(surfacesOut, "action: tailnet surfaces") || !strings.Contains(surfacesOut, "surface: parent:tsnet_http:status status=active") {
		t.Fatalf("tailnet surfaces output = %q, want active surface row", surfacesOut)
	}

	revokeOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"revoke", "parent:tsnet_http:status", "--config", cfgPath, "--format=kv", "--reason", "test revoke"})
	})
	if err != nil {
		t.Fatalf("tailnet revoke err = %v", err)
	}
	if !strings.Contains(revokeOut, "action: tailnet revoke") || !strings.Contains(revokeOut, "status: revoked") || !strings.Contains(revokeOut, "reason: test revoke") {
		t.Fatalf("tailnet revoke output = %q, want revoked report", revokeOut)
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("reopen store err = %v", err)
	}
	defer store.Close()
	surface, ok, err := store.TailnetSurface("parent:tsnet_http:status")
	if err != nil || !ok || surface.Status != session.TailnetSurfaceStatusRevoked {
		t.Fatalf("TailnetSurface after revoke = %#v ok=%t err=%v, want revoked", surface, ok, err)
	}
	events, err := store.ExecutionEventsBySession(tailnetMaintenanceSessionKey(), 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !executionEventTypeExists(events, core.ExecutionEventTailnetSurfaceChanged) {
		t.Fatalf("events = %#v, want tailnet surface changed TES event", events)
	}
}

func executionEventTypeExists(events []session.ExecutionEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}
