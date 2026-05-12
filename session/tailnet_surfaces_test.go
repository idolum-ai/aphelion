//go:build linux

package session

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSQLiteStoreCreatesTailnetSurfacesTables(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	for _, table := range []string{"tailnet_surfaces", "tailnet_surface_events"} {
		var count int
		if err := store.db.QueryRow(`
			SELECT COUNT(1)
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`, table).Scan(&count); err != nil {
			t.Fatalf("query sqlite_master %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s table count = %d, want 1", table, count)
		}
	}
}

func TestTailnetSurfaceUpsertListsAndRecordsLifecycleEvents(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	declaredAt := time.Date(2026, 4, 28, 17, 0, 0, 0, time.UTC)
	stored, err := store.UpsertTailnetSurface(TailnetSurfaceRecord{
		SurfaceID:      "parent:tsnet_http:status",
		OwnerKind:      "parent",
		OwnerID:        "aphelion",
		SurfaceKind:    "tsnet_http",
		Name:           "status",
		Hostname:       "aphelion",
		TailnetName:    "example.ts.net",
		ListenAddr:     ":8765",
		URL:            "http://aphelion.example.ts.net:8765/status",
		Tags:           []string{"tag:admin", "tag:admin", "tag:aphelion"},
		Status:         TailnetSurfaceStatusActive,
		DeclaredAt:     declaredAt,
		LastObservedAt: declaredAt,
	})
	if err != nil {
		t.Fatalf("UpsertTailnetSurface(active) err = %v", err)
	}
	if stored.Status != TailnetSurfaceStatusActive || stored.ActivatedAt.IsZero() {
		t.Fatalf("stored = %#v, want active with activated_at", stored)
	}
	if got, want := stored.Tags, []string{"tag:admin", "tag:aphelion"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}

	degradedAt := declaredAt.Add(time.Minute)
	stored.Status = TailnetSurfaceStatusDegraded
	stored.LastError = "listener failed"
	stored.UpdatedAt = degradedAt
	stored.LastObservedAt = degradedAt
	stored, err = store.UpsertTailnetSurface(stored)
	if err != nil {
		t.Fatalf("UpsertTailnetSurface(degraded) err = %v", err)
	}
	if stored.Status != TailnetSurfaceStatusDegraded || stored.LastError != "listener failed" {
		t.Fatalf("stored degraded = %#v, want degraded error", stored)
	}

	surfaces, err := store.TailnetSurfaces(TailnetSurfaceFilter{OwnerKind: "parent", Limit: 10})
	if err != nil {
		t.Fatalf("TailnetSurfaces() err = %v", err)
	}
	if len(surfaces) != 1 || surfaces[0].SurfaceID != "parent:tsnet_http:status" {
		t.Fatalf("surfaces = %#v, want parent status surface", surfaces)
	}

	events, err := store.TailnetSurfaceEvents("parent:tsnet_http:status", 10)
	if err != nil {
		t.Fatalf("TailnetSurfaceEvents() err = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event len = %d, want 2 events: %#v", len(events), events)
	}
	if events[0].EventType != "status_changed" || events[0].Status != TailnetSurfaceStatusDegraded || !strings.Contains(events[0].Detail, "listener failed") {
		t.Fatalf("latest event = %#v, want degraded status_changed", events[0])
	}
	if events[1].EventType != "active" || events[1].Status != TailnetSurfaceStatusActive {
		t.Fatalf("first event = %#v, want active", events[1])
	}
}

func TestTailnetSurfaceRevokeIsIdempotent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if _, err := store.UpsertTailnetSurface(TailnetSurfaceRecord{
		SurfaceID:   "parent:tsnet_http:status",
		OwnerKind:   "parent",
		OwnerID:     "aphelion",
		SurfaceKind: "tsnet_http",
		Name:        "status",
		Status:      TailnetSurfaceStatusActive,
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface() err = %v", err)
	}
	revokedAt := time.Date(2026, 4, 28, 18, 0, 0, 0, time.UTC)
	revoked, ok, err := store.RevokeTailnetSurface("parent:tsnet_http:status", "disabled by admin", revokedAt)
	if err != nil || !ok {
		t.Fatalf("RevokeTailnetSurface() = %#v, %t, %v; want revoked", revoked, ok, err)
	}
	if revoked.Status != TailnetSurfaceStatusRevoked || revoked.RevokedAt.IsZero() || revoked.LastError != "disabled by admin" {
		t.Fatalf("revoked = %#v, want revoked metadata", revoked)
	}
	again, ok, err := store.RevokeTailnetSurface("parent:tsnet_http:status", "second", revokedAt.Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("RevokeTailnetSurface(second) = %#v, %t, %v", again, ok, err)
	}
	if !again.RevokedAt.Equal(revoked.RevokedAt) || again.LastError != revoked.LastError {
		t.Fatalf("second revoke changed record: first=%#v second=%#v", revoked, again)
	}
}

func TestTailnetSurfaceCurrentSchemaReopens(t *testing.T) {
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
		t.Fatalf("schema version query: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	if _, err := reopened.UpsertTailnetSurface(TailnetSurfaceRecord{
		SurfaceID:   "parent:tsnet_http:status",
		OwnerKind:   "parent",
		OwnerID:     "aphelion",
		SurfaceKind: "tsnet_http",
		Name:        "status",
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface(after reopen) err = %v", err)
	}
}
