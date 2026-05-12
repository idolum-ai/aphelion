//go:build linux

package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTailnetGrantBindingLifecycle(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	stored, err := store.UpsertTailnetGrantBinding(TailnetGrantBinding{
		BindingID:         "tailnet-bind-capg-1-status",
		GrantID:           "capg-1",
		SurfaceID:         "parent:tsnet_http:status",
		GrantedTo:         "durable_agent:child-alpha",
		CapabilityKind:    string(CapabilityKindNetworkAccess),
		TargetResource:    "grafana.tailnet",
		DesiredPolicyJSON: `{"grant_id":"capg-1"}`,
	})
	if err != nil {
		t.Fatalf("UpsertTailnetGrantBinding() err = %v", err)
	}
	if stored.Status != TailnetGrantBindingStatusProposed {
		t.Fatalf("Status = %q, want proposed", stored.Status)
	}

	appliedAt := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	stored, ok, err := store.ApplyTailnetGrantBinding(stored.BindingID, "sha256:applied", "sha256:applied", appliedAt)
	if err != nil || !ok {
		t.Fatalf("ApplyTailnetGrantBinding() = %#v, %t, %v", stored, ok, err)
	}
	if stored.Status != TailnetGrantBindingStatusApplied || stored.AppliedPolicyHash != "sha256:applied" || stored.AppliedAt.IsZero() {
		t.Fatalf("applied binding = %#v, want applied policy evidence", stored)
	}

	stored, ok, err = store.DriftTailnetGrantBinding(stored.BindingID, "policy changed outside Aphelion", "sha256:observed", appliedAt.Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("DriftTailnetGrantBinding() = %#v, %t, %v", stored, ok, err)
	}
	if stored.Status != TailnetGrantBindingStatusDrifted || !strings.Contains(stored.DriftReason, "outside Aphelion") {
		t.Fatalf("drifted binding = %#v, want drift evidence", stored)
	}

	stored, ok, err = store.RevokeTailnetGrantBinding(stored.BindingID, "rollback", appliedAt.Add(2*time.Minute))
	if err != nil || !ok {
		t.Fatalf("RevokeTailnetGrantBinding() = %#v, %t, %v", stored, ok, err)
	}
	if stored.Status != TailnetGrantBindingStatusRevoked || stored.RevokedAt.IsZero() {
		t.Fatalf("revoked binding = %#v, want revoked", stored)
	}

	events, err := store.TailnetGrantBindingEvents(stored.BindingID, 10)
	if err != nil {
		t.Fatalf("TailnetGrantBindingEvents() err = %v", err)
	}
	if len(events) < 4 {
		t.Fatalf("events = %#v, want lifecycle events", events)
	}
	if events[0].Status != TailnetGrantBindingStatusRevoked {
		t.Fatalf("latest event = %#v, want revoked", events[0])
	}
}

func TestTailnetGrantBindingCurrentSchemaReopens(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	store.Close()

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer reopened.Close()
	var version int
	if err := reopened.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	if _, err := reopened.UpsertTailnetGrantBinding(TailnetGrantBinding{
		BindingID: "tailnet-bind-capg-2-status",
		GrantID:   "capg-2",
		SurfaceID: "parent:tsnet_http:status",
	}); err != nil {
		t.Fatalf("UpsertTailnetGrantBinding(after reopen) err = %v", err)
	}
}

func TestTailnetGrantBindingRequiresEvidenceForAppliedAndDriftedStates(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	_, err = store.UpsertTailnetGrantBinding(TailnetGrantBinding{
		BindingID: "tailnet-bind-applied-without-hash",
		GrantID:   "capg-1",
		SurfaceID: "parent:tsnet_http:status",
		Status:    TailnetGrantBindingStatusApplied,
	})
	if err == nil || !strings.Contains(err.Error(), "applied_policy_hash") {
		t.Fatalf("UpsertTailnetGrantBinding(applied without hash) err = %v, want evidence error", err)
	}

	_, err = store.UpsertTailnetGrantBinding(TailnetGrantBinding{
		BindingID: "tailnet-bind-drifted-without-reason",
		GrantID:   "capg-1",
		SurfaceID: "parent:tsnet_http:status",
		Status:    TailnetGrantBindingStatusDrifted,
	})
	if err == nil || !strings.Contains(err.Error(), "drift_reason") {
		t.Fatalf("UpsertTailnetGrantBinding(drifted without reason) err = %v, want evidence error", err)
	}
}

func TestTailnetGrantBindingPreventsDuplicateActiveGrantSurfacePair(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	first, err := store.UpsertTailnetGrantBinding(TailnetGrantBinding{
		BindingID: "tailnet-bind-capg-1-status-a",
		GrantID:   "capg-1",
		SurfaceID: "parent:tsnet_http:status",
	})
	if err != nil {
		t.Fatalf("UpsertTailnetGrantBinding(first) err = %v", err)
	}
	_, err = store.UpsertTailnetGrantBinding(TailnetGrantBinding{
		BindingID: "tailnet-bind-capg-1-status-b",
		GrantID:   "capg-1",
		SurfaceID: "parent:tsnet_http:status",
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("UpsertTailnetGrantBinding(duplicate active pair) err = %v, want duplicate active-pair rejection", err)
	}
	if _, ok, err := store.RevokeTailnetGrantBinding(first.BindingID, "replace binding", time.Now().UTC()); err != nil || !ok {
		t.Fatalf("RevokeTailnetGrantBinding(first) ok=%t err=%v", ok, err)
	}
	if _, err := store.UpsertTailnetGrantBinding(TailnetGrantBinding{
		BindingID: "tailnet-bind-capg-1-status-b",
		GrantID:   "capg-1",
		SurfaceID: "parent:tsnet_http:status",
	}); err != nil {
		t.Fatalf("UpsertTailnetGrantBinding(after revoke) err = %v", err)
	}
}
