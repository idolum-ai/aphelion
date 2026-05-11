//go:build linux

package runtime

import (
	"context"
	"path/filepath"
	"strings"
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
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(tailnet audit) err = %v", err)
	}
	if !containsExecutionEventType(events, core.ExecutionEventTailnetSurfaceChanged) {
		t.Fatalf("events = %#v, want tailnet surface audit event", events)
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

func TestTailnetStatusSnapshotFlagsRevokedButObservedParentSurface(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:   "parent:tsnet_http:status",
		OwnerKind:   "parent",
		OwnerID:     "aphelion",
		SurfaceKind: "tsnet_http",
		Name:        "status",
		Status:      session.TailnetSurfaceStatusRevoked,
		LastError:   "revoked by admin",
		RevokedAt:   time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface(revoked) err = %v", err)
	}

	cfg := config.Default()
	cfg.Tailscale.Enabled = true
	rt := &Runtime{
		cfg:   &cfg,
		store: store,
		tailnetBackend: fakeTailnetBackend{
			snapshot: core.TailnetStatusSnapshot{
				GeneratedAt: time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC),
				Enabled:     true,
				Backend:     "cli",
				Status:      "healthy",
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
		}
	})

	snapshot, err := rt.TailnetStatusSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TailnetStatusSnapshot() err = %v", err)
	}
	if !tailnetIssuesContain(snapshot.Issues, "surface_revoked_but_observed") {
		t.Fatalf("issues = %#v, want revoked-but-observed issue", snapshot.Issues)
	}
	stored, ok, err := store.TailnetSurface("parent:tsnet_http:status")
	if err != nil || !ok {
		t.Fatalf("TailnetSurface() = %#v, %t, %v; want stored", stored, ok, err)
	}
	if stored.Status != session.TailnetSurfaceStatusRevoked {
		t.Fatalf("stored status = %q, want revoked not reactivated", stored.Status)
	}
}

func TestTailnetStatusSnapshotFlagsDeclaredUnobservedSurface(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:   "child:tsnet_http:status:email",
		OwnerKind:   "durable_agent",
		OwnerID:     "email",
		SurfaceKind: "tsnet_http",
		Name:        "status",
		Status:      session.TailnetSurfaceStatusDeclared,
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface(declared) err = %v", err)
	}

	cfg := config.Default()
	cfg.Tailscale.Enabled = true
	rt := &Runtime{
		cfg:   &cfg,
		store: store,
		tailnetBackend: fakeTailnetBackend{
			snapshot: core.TailnetStatusSnapshot{
				GeneratedAt: time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC),
				Enabled:     true,
				Backend:     "cli",
				Status:      "healthy",
			},
		},
	}

	snapshot, err := rt.TailnetStatusSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TailnetStatusSnapshot() err = %v", err)
	}
	if !tailnetIssuesContain(snapshot.Issues, "surface_declared_not_observed") {
		t.Fatalf("issues = %#v, want declared-not-observed issue", snapshot.Issues)
	}
}

func TestTailnetSurfacesSnapshotProjectsDurableAgentDeclaration(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:     "email-child",
		ChannelKind: "external_channel",
		Status:      "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			TailnetMode:          "tsnet",
			TailnetHostname:      "mail-helper",
			TailnetTags:          []string{"tag:aphelion-child", "tag:mail"},
			TailnetSurfacePolicy: "private_status",
		}),
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	rt := &Runtime{store: store}

	surfaces, err := rt.TailnetSurfacesSnapshot()
	if err != nil {
		t.Fatalf("TailnetSurfacesSnapshot() err = %v", err)
	}
	if len(surfaces) != 1 {
		t.Fatalf("surfaces = %#v, want one durable child declaration", surfaces)
	}
	surface := surfaces[0]
	if surface.SurfaceID != "durable_agent:email-child:tsnet_http:status" ||
		surface.OwnerKind != "durable_agent" ||
		surface.OwnerID != "email-child" ||
		surface.Hostname != "mail-helper" ||
		surface.Status != session.TailnetSurfaceStatusDeclared ||
		!surface.LastObservedAt.IsZero() {
		t.Fatalf("surface = %#v, want declared unobserved durable child status surface", surface)
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(tailnet audit) err = %v", err)
	}
	if !containsExecutionEventType(events, core.ExecutionEventTailnetSurfaceChanged) {
		t.Fatalf("events = %#v, want child declaration audit event", events)
	}
}

func TestTailnetSurfacesSnapshotPreservesMaterializedDurableAgentSurface(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:     "email-child",
		ChannelKind: "external_channel",
		Status:      "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			TailnetMode:          "tsnet",
			TailnetHostname:      "mail-helper",
			TailnetTags:          []string{"tag:aphelion-child", "tag:mail"},
			TailnetSurfacePolicy: "private_status",
		}),
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:      "durable_agent:email-child:tsnet_http:status",
		OwnerKind:      "durable_agent",
		OwnerID:        "email-child",
		SurfaceKind:    "tsnet_http",
		Name:           "status",
		Hostname:       "mail-helper",
		URL:            "http://mail-helper.example.ts.net/status",
		Status:         session.TailnetSurfaceStatusActive,
		LastObservedAt: time.Date(2026, 4, 28, 21, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface(active) err = %v", err)
	}
	rt := &Runtime{store: store}

	surfaces, err := rt.TailnetSurfacesSnapshot()
	if err != nil {
		t.Fatalf("TailnetSurfacesSnapshot() err = %v", err)
	}
	if len(surfaces) != 1 {
		t.Fatalf("surfaces = %#v, want one surface", surfaces)
	}
	if surfaces[0].Status != session.TailnetSurfaceStatusActive || surfaces[0].URL != "http://mail-helper.example.ts.net/status" || surfaces[0].LastObservedAt.IsZero() {
		t.Fatalf("surface = %#v, want active materialized surface preserved", surfaces[0])
	}
}

func TestRevokeTailnetSurfaceRecordsAuditEvent(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:   "parent:tsnet_http:status",
		OwnerKind:   "parent",
		OwnerID:     "aphelion",
		SurfaceKind: "tsnet_http",
		Name:        "status",
		Status:      session.TailnetSurfaceStatusActive,
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface(active) err = %v", err)
	}
	rt := &Runtime{store: store}

	revoked, ok, err := rt.RevokeTailnetSurface(context.Background(), "parent:tsnet_http:status", "telegram admin revoke")
	if err != nil || !ok {
		t.Fatalf("RevokeTailnetSurface() = %#v, %t, %v; want revoked", revoked, ok, err)
	}
	if revoked.Status != session.TailnetSurfaceStatusRevoked || !strings.Contains(revoked.LastError, "telegram admin revoke") {
		t.Fatalf("revoked = %#v, want revoked status and reason", revoked)
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(tailnet audit) err = %v", err)
	}
	if !containsExecutionEventType(events, core.ExecutionEventTailnetSurfaceChanged) {
		t.Fatalf("events = %#v, want revoke audit event", events)
	}
}

func TestTailnetStatusSnapshotProjectsGrantBindingsAndDrift(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertTailnetSurface(session.TailnetSurfaceRecord{
		SurfaceID:   "parent:tsnet_http:status",
		OwnerKind:   "parent",
		OwnerID:     "aphelion",
		SurfaceKind: "tsnet_http",
		Name:        "status",
		Status:      session.TailnetSurfaceStatusDeclared,
	}); err != nil {
		t.Fatalf("UpsertTailnetSurface() err = %v", err)
	}
	if _, err := store.UpsertTailnetGrantBinding(session.TailnetGrantBinding{
		BindingID:         "tailnet-bind-capg-status",
		GrantID:           "capg-status",
		SurfaceID:         "parent:tsnet_http:status",
		GrantedTo:         "durable_agent:child-alpha",
		CapabilityKind:    string(session.CapabilityKindNetworkAccess),
		TargetResource:    "grafana.tailnet",
		DesiredPolicyJSON: `{"grant_id":"capg-status"}`,
		Status:            session.TailnetGrantBindingStatusDrifted,
		DriftReason:       "observed policy no longer matches approved grant",
	}); err != nil {
		t.Fatalf("UpsertTailnetGrantBinding() err = %v", err)
	}
	cfg := config.Default()
	cfg.Tailscale.Enabled = true
	rt := &Runtime{
		cfg:   &cfg,
		store: store,
		tailnetBackend: fakeTailnetBackend{
			snapshot: core.TailnetStatusSnapshot{
				GeneratedAt: time.Date(2026, 4, 28, 20, 0, 0, 0, time.UTC),
				Enabled:     true,
				Backend:     "cli",
				Status:      "healthy",
			},
		},
	}

	snapshot, err := rt.TailnetStatusSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TailnetStatusSnapshot() err = %v", err)
	}
	if len(snapshot.GrantBindings) != 1 || snapshot.GrantBindings[0].BindingID != "tailnet-bind-capg-status" {
		t.Fatalf("GrantBindings = %#v, want projected binding", snapshot.GrantBindings)
	}
	if !tailnetIssuesContain(snapshot.Issues, "grant_binding_drifted") {
		t.Fatalf("issues = %#v, want grant_binding_drifted", snapshot.Issues)
	}
}

func tailnetIssuesContain(issues []core.TailnetIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

type fakeTailnetBackend struct {
	snapshot core.TailnetStatusSnapshot
	err      error
}

func (b fakeTailnetBackend) Snapshot(context.Context) (core.TailnetStatusSnapshot, error) {
	return b.snapshot, b.err
}
