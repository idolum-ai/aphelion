//go:build linux

package tool

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func seedVerifiedExternalToolLifecycle(t *testing.T, registry *Registry, store *session.SQLiteStore, manifest ExternalToolManifest, scope sandbox.Scope) {
	t.Helper()

	manifest = NormalizeExternalToolManifest(manifest)
	if scope.WorkingRoot == "" {
		scope.WorkingRoot = registry.workspace
	}
	const installRef = "test:fixture"
	fingerprint, err := externalToolFingerprints(manifest, scope.WorkingRoot, installRef)
	if err != nil {
		t.Fatalf("externalToolFingerprints(%s) err = %v", manifest.Name, err)
	}

	now := time.Now().UTC()
	installedAt := now.Add(-3 * time.Minute)
	auditedAt := now.Add(-2 * time.Minute)
	probedAt := now.Add(-1 * time.Minute)
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{
		ToolName:                     manifest.Name,
		Status:                       session.ToolProbeStatusPassed,
		ProbeOutput:                  "stdout: probe ok",
		Rationale:                    "probe_run passed against the declared probe command",
		BaselineFingerprint:          fingerprint.Aggregate,
		CurrentFingerprint:           fingerprint.Aggregate,
		BaselineInstallRef:           fingerprint.InstallRef,
		CurrentInstallRef:            fingerprint.InstallRef,
		BaselineManifestHash:         fingerprint.ManifestHash,
		CurrentManifestHash:          fingerprint.ManifestHash,
		BaselineWorkspaceFingerprint: fingerprint.WorkspaceFingerprint,
		CurrentWorkspaceFingerprint:  fingerprint.WorkspaceFingerprint,
		CreatedAt:                    probedAt,
		UpdatedAt:                    probedAt,
		ProbedAt:                     probedAt,
	}); err != nil {
		t.Fatalf("UpsertToolProbeRecord(%s) err = %v", manifest.Name, err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{
		ToolName:                     manifest.Name,
		Status:                       session.ToolAuditStatusPassed,
		AuditOutput:                  "entry_path: test fixture",
		Rationale:                    "audit_run resolved the declared execution entry",
		BaselineFingerprint:          fingerprint.Aggregate,
		CurrentFingerprint:           fingerprint.Aggregate,
		BaselineInstallRef:           fingerprint.InstallRef,
		CurrentInstallRef:            fingerprint.InstallRef,
		BaselineManifestHash:         fingerprint.ManifestHash,
		CurrentManifestHash:          fingerprint.ManifestHash,
		BaselineWorkspaceFingerprint: fingerprint.WorkspaceFingerprint,
		CurrentWorkspaceFingerprint:  fingerprint.WorkspaceFingerprint,
		CreatedAt:                    auditedAt,
		UpdatedAt:                    auditedAt,
		AuditedAt:                    auditedAt,
	}); err != nil {
		t.Fatalf("UpsertToolAuditRecord(%s) err = %v", manifest.Name, err)
	}
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{
		ToolName:                     manifest.Name,
		Installer:                    "test",
		InstallRef:                   installRef,
		Status:                       session.ToolInstallStatusVerified,
		ProbeStatus:                  session.ToolProbeStatusPassed,
		ProbeOutput:                  "stdout: probe ok",
		BaselineFingerprint:          fingerprint.Aggregate,
		CurrentFingerprint:           fingerprint.Aggregate,
		BaselineInstallRef:           fingerprint.InstallRef,
		CurrentInstallRef:            fingerprint.InstallRef,
		BaselineManifestHash:         fingerprint.ManifestHash,
		CurrentManifestHash:          fingerprint.ManifestHash,
		BaselineWorkspaceFingerprint: fingerprint.WorkspaceFingerprint,
		CurrentWorkspaceFingerprint:  fingerprint.WorkspaceFingerprint,
		CreatedAt:                    installedAt,
		UpdatedAt:                    now,
		InstalledAt:                  installedAt,
		LastProbedAt:                 probedAt,
		AttestedAt:                   now,
	}); err != nil {
		t.Fatalf("UpsertToolInstallRecord(%s) err = %v", manifest.Name, err)
	}
}

func toolDefExists(defs []agent.ToolDef, name string) bool {
	for _, def := range defs {
		if def.Name == name {
			return true
		}
	}
	return false
}

func grantToolInvoke(t *testing.T, store *session.SQLiteStore, toolName string, principal string) {
	t.Helper()

	toolName = strings.TrimSpace(toolName)
	principal = strings.TrimSpace(principal)
	if toolName == "" || principal == "" {
		t.Fatalf("grantToolInvoke requires toolName and principal")
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant:" + toolName + ":" + principal,
		GrantedBy:      "test",
		GrantedTo:      principal,
		Kind:           session.CapabilityKindTool,
		TargetResource: toolName,
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(%s/%s) err = %v", toolName, principal, err)
	}
	grantAuthorityUseLease(t, store, adminSessionKey())
}

func grantAuthorityUseLease(t *testing.T, store *session.SQLiteStore, key session.SessionKey) {
	t.Helper()

	grantAuthorityUseLeaseWithID(t, store, key, "lease-authority-use-"+session.SessionIDForKey(key))
}

func authorityRunContextForPrincipal(t *testing.T, store *session.SQLiteStore, key session.SessionKey, actor principal.Principal) context.Context {
	t.Helper()

	leaseID := fmt.Sprintf("lease-authority-use-%s-%d", session.SessionIDForKey(key), time.Now().UnixNano())
	grantAuthorityUseLeaseWithID(t, store, key, leaseID)
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, leaseID, session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "test_tool_invocation")
	return ctx
}

func adminAuthorityRunContext(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
	t.Helper()
	return authorityRunContextForPrincipal(t, store, key, principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
}

func durableAgentAuthorityRunContext(t *testing.T, store *session.SQLiteStore, key session.SessionKey, agentID string) context.Context {
	t.Helper()
	return authorityRunContextForPrincipal(t, store, key, principal.Principal{Role: principal.RoleDurableAgent, DurableAgentID: strings.TrimSpace(agentID)})
}

func grantAuthorityUseLeaseWithID(t *testing.T, store *session.SQLiteStore, key session.SessionKey, leaseID string) {
	t.Helper()

	now := time.Now().UTC()
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 1,
		ContinuationLease: session.ContinuationLease{
			ID:             leaseID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
			ApprovedAt:     now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState(authority use lease) err = %v", err)
	}
}
