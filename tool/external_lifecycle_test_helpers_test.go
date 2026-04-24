//go:build linux

package tool

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func seedVerifiedExternalToolLifecycle(t *testing.T, registry *Registry, store *session.SQLiteStore, manifest ExternalToolManifest, scope sandbox.Scope) {
	t.Helper()

	manifest = NormalizeExternalToolManifest(manifest)
	if scope.WorkingRoot == "" {
		scope.WorkingRoot = registry.workspace
	}
	fingerprint, err := externalToolFingerprint(manifest, scope.WorkingRoot)
	if err != nil {
		t.Fatalf("externalToolFingerprint(%s) err = %v", manifest.Name, err)
	}

	now := time.Now().UTC()
	installedAt := now.Add(-3 * time.Minute)
	auditedAt := now.Add(-2 * time.Minute)
	probedAt := now.Add(-1 * time.Minute)
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{
		ToolName:    manifest.Name,
		Status:      session.ToolProbeStatusPassed,
		ProbeOutput: "stdout: probe ok",
		Rationale:   "probe_run passed against the declared probe command",
		CreatedAt:   probedAt,
		UpdatedAt:   probedAt,
		ProbedAt:    probedAt,
	}); err != nil {
		t.Fatalf("UpsertToolProbeRecord(%s) err = %v", manifest.Name, err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{
		ToolName:            manifest.Name,
		Status:              session.ToolAuditStatusPassed,
		AuditOutput:         "entry_path: test fixture",
		Rationale:           "audit_run resolved the declared execution entry",
		BaselineFingerprint: fingerprint,
		CurrentFingerprint:  fingerprint,
		CreatedAt:           auditedAt,
		UpdatedAt:           auditedAt,
		AuditedAt:           auditedAt,
	}); err != nil {
		t.Fatalf("UpsertToolAuditRecord(%s) err = %v", manifest.Name, err)
	}
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{
		ToolName:            manifest.Name,
		Installer:           "test",
		InstallRef:          "test:fixture",
		Status:              session.ToolInstallStatusVerified,
		ProbeStatus:         session.ToolProbeStatusPassed,
		ProbeOutput:         "stdout: probe ok",
		BaselineFingerprint: fingerprint,
		CurrentFingerprint:  fingerprint,
		CreatedAt:           installedAt,
		UpdatedAt:           now,
		InstalledAt:         installedAt,
		LastProbedAt:        probedAt,
		AttestedAt:          now,
	}); err != nil {
		t.Fatalf("UpsertToolInstallRecord(%s) err = %v", manifest.Name, err)
	}
}
