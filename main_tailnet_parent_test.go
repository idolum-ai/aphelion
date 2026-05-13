//go:build linux

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

func newTailnetPrivateTestRequest(method string, target string, body io.Reader, login string) *http.Request {
	req := httptest.NewRequest(method, target, body)
	ctx := core.WithTailnetPeerIdentity(req.Context(), core.TailnetPeerIdentity{
		StableNodeID: "node-stable-1",
		NodeName:     "admin.example.ts.net",
		ComputedName: "admin",
		LoginName:    login,
	})
	return req.WithContext(ctx)
}

func TestTailnetParentAuthKeyUsesEnvBeforeFile(t *testing.T) {
	t.Setenv("APHELION_TEST_TS_AUTHKEY", "env-key")
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "auth.key")
	if err := os.WriteFile(keyFile, []byte("file-key"), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	key, source, err := tailnetParentAuthKey(config.TailscaleParentConfig{
		AuthKeyEnv:  "APHELION_TEST_TS_AUTHKEY",
		AuthKeyFile: keyFile,
	})
	if err != nil {
		t.Fatalf("tailnetParentAuthKey() err = %v", err)
	}
	if key != "env-key" || source != "env:APHELION_TEST_TS_AUTHKEY" {
		t.Fatalf("auth key = (%q,%q), want env source", key, source)
	}
}

func TestTailnetPrivateHTTPHandlerServesHealthTailnetAndStatus(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		canRestart: true,
		tailnetStatus: core.TailnetStatusSnapshot{
			Enabled: true,
			Backend: "cli",
			Status:  "healthy",
		},
		tailnetSurfaces: []core.TailnetSurfaceStatus{{
			SurfaceID: "parent:tsnet_http:status",
			Status:    "active",
		}},
		tailnetGrantBindings: []core.TailnetGrantBindingStatus{{
			BindingID: "tailnet-bind-capg-status",
			GrantID:   "capg-status",
			SurfaceID: "parent:tsnet_http:status",
			Status:    "applied",
		}},
		latestDoctorReport: session.DoctorReportRecord{
			SessionID:      "telegram_dm:1001",
			ChatID:         1001,
			TurnIndex:      7,
			FullReport:     "State of Things\nRuntime is diagnosable.",
			TelegramReport: "State of Things\nRuntime is diagnosable.",
			CreatedAt:      time.Date(2026, 5, 10, 7, 0, 0, 0, time.UTC),
		},
		latestDoctorReportOK: true,
		statusSystem: core.SystemStatusSnapshot{
			ActiveTurnCount: 1,
		},
		personaEffort:  "gpt-5.5",
		governorEffort: "high",
	}
	handler := tailnetPrivateHTTPHandler(router, 1001, []string{"admin@example.com"}, nil)

	for _, path := range []string{"/healthz", "/tailnet", "/tailnet/surfaces", "/tailnet/grants", "/status", "/health/diagnosis/latest"} {
		req := newTailnetPrivateTestRequest(http.MethodGet, path, nil, "admin@example.com")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%q, want 200", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("%s content-type = %q, want json", path, rec.Header().Get("Content-Type"))
		}
	}
	if router.tailnetStatusSenderID != 1001 {
		t.Fatalf("tailnet sender = %d, want admin id", router.tailnetStatusSenderID)
	}
	if router.tailnetSurfacesSenderID != 1001 {
		t.Fatalf("tailnet surfaces sender = %d, want admin id", router.tailnetSurfacesSenderID)
	}
	if router.tailnetGrantBindingsSenderID != 1001 {
		t.Fatalf("tailnet grant bindings sender = %d, want admin id", router.tailnetGrantBindingsSenderID)
	}
	req := newTailnetPrivateTestRequest(http.MethodGet, "/tailnet/grants", nil, "admin@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"grant_bindings"`) || !strings.Contains(rec.Body.String(), `"tailnet-bind-capg-status"`) {
		t.Fatalf("tailnet grants body = %q, want grant binding mirror", rec.Body.String())
	}
	if router.latestDoctorReportChatID != 1001 || router.latestDoctorReportSenderID != 1001 {
		t.Fatalf("doctor latest lookup = (%d,%d), want configured admin chat/sender", router.latestDoctorReportChatID, router.latestDoctorReportSenderID)
	}
	req = newTailnetPrivateTestRequest(http.MethodGet, "/health/diagnosis/latest", nil, "admin@example.com")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"available":true`) || !strings.Contains(rec.Body.String(), `"full_report":"State of Things`) {
		t.Fatalf("doctor latest body = %q, want latest report payload", rec.Body.String())
	}
}

func TestTailnetPrivateHTTPHandlerRequiresConfiguredTailnetAdminLogin(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		canRestart: true,
		tailnetStatus: core.TailnetStatusSnapshot{
			Enabled: true,
			Backend: "tsnet",
			Status:  "healthy",
		},
	}
	handler := tailnetPrivateHTTPHandler(router, 1001, []string{"admin@example.com"}, nil)

	req := httptest.NewRequest(http.MethodGet, "/tailnet", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing identity status = %d, body=%q, want 403", rec.Code, rec.Body.String())
	}

	req = newTailnetPrivateTestRequest(http.MethodGet, "/tailnet", nil, "operator@example.com")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unauthorized identity status = %d, body=%q, want 403", rec.Code, rec.Body.String())
	}

	req = newTailnetPrivateTestRequest(http.MethodGet, "/tailnet", nil, "admin@example.com")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized identity status = %d, body=%q, want 200", rec.Code, rec.Body.String())
	}
}

func TestTailnetPrivateHTTPHandlerRejectsMutationRoutes(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		canRestart: true,
		revokeTailnetSurfaceReturn: core.TailnetSurfaceStatus{
			SurfaceID: "parent:tsnet_http:status",
			Status:    "revoked",
		},
		revokeTailnetSurfaceOK: true,
	}
	handler := tailnetPrivateHTTPHandler(router, 1001, []string{"admin@example.com"}, nil)

	req := newTailnetPrivateTestRequest(http.MethodPost, "/tailnet/surfaces/parent:tsnet_http:status/revoke", nil, "admin@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("private revoke status = %d body=%q, want 405", rec.Code, rec.Body.String())
	}
	if router.revokeTailnetSurfaceID != "" {
		t.Fatalf("revokeTailnetSurfaceID = %q, want no mutation from private mirror", router.revokeTailnetSurfaceID)
	}
	if !strings.Contains(rec.Body.String(), "read-only mirrors") {
		t.Fatalf("private revoke body = %q, want read-only mirror explanation", rec.Body.String())
	}
}

func TestTailnetPrivateHTTPHandlerMountsDurableAgentControlPlane(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	handler := tailnetPrivateHTTPHandler(
		&stubCommandRouter{},
		1001,
		[]string{"admin@example.com"},
		durableagent.NewHTTPHandler(store).HandlerWithBasePath("/control"),
	)

	req := httptest.NewRequest(http.MethodPost, "/control/v1/durable-agent/enroll", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotFound {
		t.Fatalf("control plane status = %d body=%q, want durable-agent control handler mounted", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("control plane status = %d body=%q, want mounted handler validation failure", rec.Code, rec.Body.String())
	}
}

func TestTailnetParentServiceDisabledByDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	service, err := tailnetParentService(&cfg, &stubCommandRouter{}, nil)
	if err != nil {
		t.Fatalf("tailnetParentService() err = %v", err)
	}
	if service != nil {
		t.Fatalf("service = %#v, want nil when disabled by default", service)
	}
}

func TestTailnetParentServiceReportsAuthKeyFileErrorsOnStart(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.ExpectedTailnet = "example.ts.net"
	cfg.Tailscale.Parent.Enabled = true
	cfg.Tailscale.Parent.StateDir = t.TempDir()
	cfg.Tailscale.Parent.AuthKeyEnv = ""
	cfg.Tailscale.Parent.AuthKeyFile = filepath.Join(t.TempDir(), "missing.key")
	service, err := tailnetParentService(&cfg, &stubCommandRouter{}, nil)
	if err != nil {
		t.Fatalf("tailnetParentService() err = %v, want nonfatal auth key read failure", err)
	}
	if service == nil {
		t.Fatal("service = nil, want configured parent service")
	}
	err = startTailnetParent(context.Background(), service)
	if err == nil || !strings.Contains(err.Error(), "missing.key") {
		t.Fatalf("startTailnetParent() err = %v, want auth-key start failure", err)
	}
	status := service.Status()
	if status.Running || !strings.Contains(status.LastError, "load auth key") || !strings.Contains(status.LastError, "missing.key") {
		t.Fatalf("status = %#v, want stopped auth-key file error", status)
	}
}

func TestStartTailnetParentFailsOnStartFailure(t *testing.T) {
	t.Parallel()

	service := tailnet.NewParentService(tailnet.ParentOptions{
		Enabled:  true,
		Hostname: "aphelion-test",
		StateDir: t.TempDir(),
	})
	if err := startTailnetParent(context.Background(), service); err == nil {
		t.Fatal("startTailnetParent() err = nil, want startup failure")
	}
	status := service.Status()
	if status.Running || !strings.Contains(status.LastError, "auth key") {
		t.Fatalf("status = %#v, want stopped auth-key startup error", status)
	}
}
