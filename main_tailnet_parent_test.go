//go:build linux

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tailnet"
)

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
	handler := tailnetPrivateHTTPHandler(router, 1001)

	for _, path := range []string{"/healthz", "/tailnet", "/tailnet/surfaces", "/status", "/doctor/latest"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
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
	if router.latestDoctorReportChatID != 1001 || router.latestDoctorReportSenderID != 1001 {
		t.Fatalf("doctor latest lookup = (%d,%d), want configured admin chat/sender", router.latestDoctorReportChatID, router.latestDoctorReportSenderID)
	}
	req := httptest.NewRequest(http.MethodGet, "/doctor/latest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"available":true`) || !strings.Contains(rec.Body.String(), `"full_report":"State of Things`) {
		t.Fatalf("doctor latest body = %q, want latest report payload", rec.Body.String())
	}
}

func TestTailnetPrivateHTTPHandlerRevokeRequiresConfirmationHeader(t *testing.T) {
	t.Parallel()

	router := &stubCommandRouter{
		canRestart: true,
		revokeTailnetSurfaceReturn: core.TailnetSurfaceStatus{
			SurfaceID: "parent:tsnet_http:status",
			Status:    "revoked",
		},
		revokeTailnetSurfaceOK: true,
	}
	handler := tailnetPrivateHTTPHandler(router, 1001)

	req := httptest.NewRequest(http.MethodPost, "/tailnet/surfaces/parent:tsnet_http:status/revoke", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("unconfirmed revoke status = %d body=%q, want 428", rec.Code, rec.Body.String())
	}
	if router.revokeTailnetSurfaceID != "" {
		t.Fatalf("revokeTailnetSurfaceID = %q, want no revoke without confirmation", router.revokeTailnetSurfaceID)
	}

	req = httptest.NewRequest(http.MethodPost, "/tailnet/surfaces/parent:tsnet_http:status/revoke", nil)
	req.Header.Set("X-Aphelion-Confirm", "revoke")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed revoke status = %d body=%q, want 200", rec.Code, rec.Body.String())
	}
	if router.revokeTailnetSurfaceSenderID != 1001 || router.revokeTailnetSurfaceID != "parent:tsnet_http:status" {
		t.Fatalf("revoke call sender=%d surface=%q, want admin surface revoke", router.revokeTailnetSurfaceSenderID, router.revokeTailnetSurfaceID)
	}
	if !strings.Contains(rec.Body.String(), `"revoked"`) {
		t.Fatalf("confirmed revoke body = %q, want revoked payload", rec.Body.String())
	}
}

func TestTailnetParentServiceDisabledByDefault(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	service, err := tailnetParentService(&cfg, &stubCommandRouter{})
	if err != nil {
		t.Fatalf("tailnetParentService() err = %v", err)
	}
	if service != nil {
		t.Fatalf("service = %#v, want nil when disabled by default", service)
	}
}

func TestTailnetParentServiceDefersAuthKeyFileErrorsToStatus(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Tailscale.Enabled = true
	cfg.Tailscale.ExpectedTailnet = "example.ts.net"
	cfg.Tailscale.Parent.Enabled = true
	cfg.Tailscale.Parent.StateDir = t.TempDir()
	cfg.Tailscale.Parent.AuthKeyEnv = ""
	cfg.Tailscale.Parent.AuthKeyFile = filepath.Join(t.TempDir(), "missing.key")
	service, err := tailnetParentService(&cfg, &stubCommandRouter{})
	if err != nil {
		t.Fatalf("tailnetParentService() err = %v, want nonfatal auth key read failure", err)
	}
	if service == nil {
		t.Fatal("service = nil, want configured parent service")
	}
	if err := startTailnetParent(context.Background(), service); err != nil {
		t.Fatalf("startTailnetParent() err = %v, want nonfatal parent startup failure", err)
	}
	status := service.Status()
	if status.Running || !strings.Contains(status.LastError, "load auth key") || !strings.Contains(status.LastError, "missing.key") {
		t.Fatalf("status = %#v, want stopped auth-key file error", status)
	}
}

func TestStartTailnetParentKeepsAphelionRunningOnStartFailure(t *testing.T) {
	t.Parallel()

	service := tailnet.NewParentService(tailnet.ParentOptions{
		Enabled:  true,
		Hostname: "aphelion-test",
		StateDir: t.TempDir(),
	})
	if err := startTailnetParent(context.Background(), service); err != nil {
		t.Fatalf("startTailnetParent() err = %v, want nonfatal parent startup failure", err)
	}
	status := service.Status()
	if status.Running || !strings.Contains(status.LastError, "auth key") {
		t.Fatalf("status = %#v, want stopped auth-key startup error", status)
	}
}
