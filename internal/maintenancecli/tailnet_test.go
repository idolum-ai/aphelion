//go:build linux

package maintenancecli

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

func TestRunTailnetCommandGrantBindingLifecycle(t *testing.T) {
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
	now := time.Now().UTC()
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:      "parent:tsnet_http:status",
		OwnerKind:      "parent",
		OwnerID:        "aphelion",
		SurfaceKind:    "tsnet_http",
		Name:           "status",
		Hostname:       "aphelion",
		URL:            "https://aphelion.example.ts.net/status",
		Status:         session.TailnetSurfaceStatusActive,
		LastObservedAt: now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-tailnet",
		RequestID:      "req-tailnet",
		GrantedBy:      "telegram:1",
		GrantedTo:      core.DurableAgentPrincipal("agent-alpha"),
		Kind:           session.CapabilityKindNetworkAccess,
		TargetResource: "tailnet:aphelion/status",
		AllowedActions: []string{"reach_private_status"},
		Contract:       "{}",
		Constraints:    "{}",
		Status:         session.CapabilityGrantStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}

	bindingID := "tailnet-bind-capg-tailnet-parent-tsnet-http-status"
	bindOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"bind-grant", "--config", cfgPath, "--format=kv", "--grant-id", "capg-tailnet", "--surface-id", "parent:tsnet_http:status", "--reason", "test bind"})
	})
	if err != nil {
		t.Fatalf("tailnet bind-grant err = %v", err)
	}
	if !strings.Contains(bindOut, "action: tailnet bind-grant") || !strings.Contains(bindOut, "status: proposed") || !strings.Contains(bindOut, "grant_binding: "+bindingID+" status=proposed") {
		t.Fatalf("tailnet bind-grant output = %q, want proposed binding", bindOut)
	}

	applyOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"apply-binding", bindingID, "--config", cfgPath, "--format=kv", "--policy-hash", "policy-a"})
	})
	if err != nil {
		t.Fatalf("tailnet apply-binding err = %v", err)
	}
	if !strings.Contains(applyOut, "action: tailnet apply-binding") || !strings.Contains(applyOut, "status: applied") || !strings.Contains(applyOut, "grant_binding: "+bindingID+" status=applied") {
		t.Fatalf("tailnet apply-binding output = %q, want applied binding", applyOut)
	}

	grantsOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"grants", "--config", cfgPath, "--format=kv"})
	})
	if err != nil {
		t.Fatalf("tailnet grants err = %v", err)
	}
	if !strings.Contains(grantsOut, "action: tailnet grants") || !strings.Contains(grantsOut, "status: ready") || !strings.Contains(grantsOut, "grant_binding: "+bindingID+" status=applied") {
		t.Fatalf("tailnet grants output = %q, want ready applied binding", grantsOut)
	}

	driftOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"drift-binding", bindingID, "--config", cfgPath, "--format=kv", "--reason", "observed policy changed", "--observed-policy-hash", "policy-b"})
	})
	if err != nil {
		t.Fatalf("tailnet drift-binding err = %v", err)
	}
	if !strings.Contains(driftOut, "action: tailnet drift-binding") || !strings.Contains(driftOut, "status: drifted") || !strings.Contains(driftOut, "reason: observed policy changed") {
		t.Fatalf("tailnet drift-binding output = %q, want drifted binding", driftOut)
	}

	rollbackOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"rollback-binding", bindingID, "--config", cfgPath, "--format=kv", "--reason", "rollback test"})
	})
	if err != nil {
		t.Fatalf("tailnet rollback-binding err = %v", err)
	}
	if !strings.Contains(rollbackOut, "action: tailnet rollback-binding") || !strings.Contains(rollbackOut, "status: revoked") || !strings.Contains(rollbackOut, "reason: rollback test") {
		t.Fatalf("tailnet rollback-binding output = %q, want revoked binding", rollbackOut)
	}
	revokedGrantsOut, err := captureStdout(t, func() error {
		return runTailnetCommand([]string{"grants", "--config", cfgPath, "--format=kv"})
	})
	if err != nil {
		t.Fatalf("tailnet grants after rollback err = %v", err)
	}
	if !strings.Contains(revokedGrantsOut, "action: tailnet grants") || !strings.Contains(revokedGrantsOut, "status: revoked") {
		t.Fatalf("tailnet grants after rollback output = %q, want revoked registry status", revokedGrantsOut)
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("reopen store err = %v", err)
	}
	defer store.Close()
	binding, ok, err := store.TailnetGrantBinding(bindingID)
	if err != nil || !ok || binding.Status != session.TailnetGrantBindingStatusRevoked {
		t.Fatalf("TailnetGrantBinding after rollback = %#v ok=%t err=%v, want revoked", binding, ok, err)
	}
	events, err := store.ExecutionEventsBySession(tailnetMaintenanceSessionKey(), 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if countExecutionEventType(events, core.ExecutionEventTailnetGrantChanged) < 4 {
		t.Fatalf("events = %#v, want grant changed TES events for bind/apply/drift/rollback", events)
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

func countExecutionEventType(events []session.ExecutionEvent, eventType string) int {
	count := 0
	for _, event := range events {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}
