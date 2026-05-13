//go:build linux

package main

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRepairCapabilityGrantDriftDryRunLeavesMissingRuntimeActive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-missing-runtime"
	if _, err := store.UpsertCapabilityGrant(testRepairCapabilityGrant(grantID, "missing_tool", now)); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	result, err := repairCapabilityGrantDrift(context.Background(), store, nil, capabilityGrantRepairOptions{
		Limit:  10,
		DryRun: true,
		Source: "test",
		Now:    now,
	})
	if err != nil {
		t.Fatalf("repairCapabilityGrantDrift() err = %v", err)
	}
	if result.Inspected != 1 || result.RevokeCandidates != 1 || result.RevokesApplied != 0 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want dry-run revoke candidate only", result)
	}
	updated, ok, err := store.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusActive || !updated.RevokedAt.IsZero() {
		t.Fatalf("updated grant = %#v, want active and not revoked", updated)
	}
}

func TestRepairCapabilityGrantDriftRevokesExpiredActiveGrant(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-expired-runtime"
	grant := testRepairCapabilityGrant(grantID, "expired_tool", now)
	grant.ExpiresAt = now.Add(-time.Minute)
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	result, err := repairCapabilityGrantDrift(context.Background(), store, nil, capabilityGrantRepairOptions{
		Limit:  10,
		DryRun: false,
		Source: "test",
		Now:    now,
	})
	if err != nil {
		t.Fatalf("repairCapabilityGrantDrift() err = %v", err)
	}
	if result.Inspected != 1 || result.RevokeCandidates != 1 || result.RevokesApplied != 1 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want one revoked expired grant", result)
	}
	updated, ok, err := store.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusRevoked || updated.RevokedAt.IsZero() {
		t.Fatalf("updated grant = %#v, want revoked with revoked_at", updated)
	}
	if !strings.Contains(updated.StaleReason, "expired") {
		t.Fatalf("stale reason = %q, want expired reason", updated.StaleReason)
	}
}

func TestRepairCapabilityGrantDriftRepairsFromManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-repair-runtime"
	grant := testRepairCapabilityGrant(grantID, "repair_tool", now)
	grant.Constraints = `{"child_runtime":{"executable":"relative/path"},"max_runtime_seconds":10}`
	if _, err := store.UpsertCapabilityGrant(grant); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	executable := filepath.Join(root, "bin", "repair-tool")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) err = %v", err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(executable) err = %v", err)
	}
	manifestPath := filepath.Join(root, "external-tools", "repair_tool", "manifest.json")
	manifest := capabilityGrantRepairManifest{
		Path: manifestPath,
		Manifest: tool.ExternalToolManifest{
			Name:  "repair_tool",
			Owner: "test",
			Execution: tool.ExternalToolManifestExecution{
				Mode:  "process",
				Entry: executable,
			},
		},
	}

	result, err := repairCapabilityGrantDrift(context.Background(), store, []capabilityGrantRepairManifest{manifest}, capabilityGrantRepairOptions{
		Limit:  10,
		DryRun: false,
		Source: "test",
		Now:    now,
	})
	if err != nil {
		t.Fatalf("repairCapabilityGrantDrift() err = %v", err)
	}
	if result.Inspected != 1 || result.RepairCandidates != 1 || result.RepairsApplied != 1 || result.Errors != 0 {
		t.Fatalf("repair result = %#v, want one repaired grant", result)
	}
	updated, ok, err := store.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusActive {
		t.Fatalf("updated grant = %#v, want active repaired grant", updated)
	}
	material, found, err := core.ExtractChildRuntimeContract(updated.Contract, updated.Constraints)
	if err != nil {
		t.Fatalf("ExtractChildRuntimeContract() err = %v", err)
	}
	if !found || material.Executable != executable {
		t.Fatalf("child runtime = %#v found=%t, want executable %s", material, found, executable)
	}
	var constraints map[string]json.RawMessage
	if err := json.Unmarshal([]byte(updated.Constraints), &constraints); err != nil {
		t.Fatalf("decode updated constraints err = %v", err)
	}
	if _, ok := constraints["child_runtime"]; ok {
		t.Fatalf("updated constraints = %q, want stale child_runtime removed", updated.Constraints)
	}
	if _, ok := constraints["max_runtime_seconds"]; !ok {
		t.Fatalf("updated constraints = %q, want other constraints preserved", updated.Constraints)
	}
	if updated.BaselinePolicyHash == "" || updated.BaselinePolicyHash != updated.CurrentPolicyHash || updated.AnchorFingerprint != updated.CurrentPolicyHash {
		t.Fatalf("policy hashes = baseline %q current %q anchor %q, want repaired hash copied to all", updated.BaselinePolicyHash, updated.CurrentPolicyHash, updated.AnchorFingerprint)
	}
}

func TestLoadCapabilityRepairManifestsResolvesNestedRepoRelativeEntry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifestDir := filepath.Join(root, "external-tools")
	executable := filepath.Join(manifestDir, "nested_tool", "bin", "nested-tool")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) err = %v", err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(executable) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(executable), "payload.json"), []byte(`{"not":"a manifest"}`), 0o600); err != nil {
		t.Fatalf("WriteFile(payload) err = %v", err)
	}
	manifestPath := filepath.Join(manifestDir, "nested_tool", "manifest.json")
	raw := `{
  "name": "nested_tool",
  "owner": "test",
  "execution": {
    "mode": "process",
    "entry": "external-tools/nested_tool/bin/nested-tool"
  },
  "io": {}
}`
	if err := os.WriteFile(manifestPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) err = %v", err)
	}

	manifests, err := loadCapabilityRepairManifests(manifestDir)
	if err != nil {
		t.Fatalf("loadCapabilityRepairManifests() err = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifests len = %d, want 1", len(manifests))
	}
	material, ok, reason := childRuntimeFromRepairManifest(manifests[0])
	if !ok {
		t.Fatalf("childRuntimeFromRepairManifest() ok=false reason=%q", reason)
	}
	if material.Executable != executable {
		t.Fatalf("material executable = %q, want %q", material.Executable, executable)
	}
}

func TestRunRepairCapabilityGrantsCommandDefaultsToDryRun(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	grantID := "capg-command-dry-run"
	if _, err := store.UpsertCapabilityGrant(testRepairCapabilityGrant(grantID, "command_tool", now)); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	store.Close()

	out, err := captureStdout(t, func() error {
		return runRepairCapabilityGrantsCommand([]string{"--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runRepairCapabilityGrantsCommand() err = %v", err)
	}
	for _, needle := range []string{"action: repair-capability-grants", "dry_run: true", "revoke_candidates: 1", "revokes_applied: 0"} {
		if !strings.Contains(out, needle) {
			t.Fatalf("output = %q, want %q", out, needle)
		}
	}
	reopened, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer reopened.Close()
	updated, ok, err := reopened.CapabilityGrant(grantID)
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || updated.Status != session.CapabilityGrantStatusActive || !updated.RevokedAt.IsZero() {
		t.Fatalf("updated grant = %#v, want unchanged active grant after dry-run", updated)
	}
}

func TestRunRepairCapabilityGrantsCommandRequiresApplyForMutation(t *testing.T) {
	err := runRepairCapabilityGrantsCommand([]string{"--dry-run=false"})
	if err == nil || !strings.Contains(err.Error(), "requires --apply") {
		t.Fatalf("runRepairCapabilityGrantsCommand() err = %v, want --apply requirement", err)
	}
}

func TestRunAuthorityCommandsReportRepairPreview(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 77710, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "77710"}}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status: session.ContinuationStatusApproved,
		ActionProposal: session.ActionProposal{
			ID:        "proposal-authority-cli",
			Status:    session.ProposalStatusApproved,
			ExpiresAt: now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-authority-cli",
			ProposalID:     "proposal-authority-cli",
			Status:         session.ContinuationLeaseStatusActive,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	store.Close()

	doctorOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"doctor", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(doctor) err = %v", err)
	}
	for _, needle := range []string{"action: authority-doctor", "status: needs_attention", "code=expired_continuation_lease"} {
		if !strings.Contains(doctorOut, needle) {
			t.Fatalf("doctor output = %q, want %q", doctorOut, needle)
		}
	}

	repairOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(repair) err = %v", err)
	}
	for _, needle := range []string{"action: authority-repair", "dry_run: true", "apply_action=expire_continuation_lease", "apply_scope=continuation_lease", "applicable=true"} {
		if !strings.Contains(repairOut, needle) {
			t.Fatalf("repair output = %q, want %q", repairOut, needle)
		}
	}
	if id := authorityFindingIDFromOutput(t, repairOut, "expired_continuation_lease"); id == "" {
		t.Fatalf("repair output = %q, want finding_id", repairOut)
	}
}

func TestRunAuthorityRepairApplyExpiresContinuationLeaseByFindingID(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 77711, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "77711"}}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "decision-authority-apply",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:        "proposal-authority-apply",
			Status:    session.ProposalStatusApproved,
			ExpiresAt: now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-authority-apply",
			ProposalID:     "proposal-authority-apply",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	store.Close()

	previewOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(repair preview) err = %v", err)
	}
	findingID := authorityFindingIDFromOutput(t, previewOut, "expired_continuation_lease")

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen dry-run check) err = %v", err)
	}
	dryRunState, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(dry-run) err = %v", err)
	}
	if dryRunState.ContinuationLease.Status != session.ContinuationLeaseStatusActive || dryRunState.RemainingTurns != 1 {
		t.Fatalf("dry-run state = %#v, want unchanged active lease", dryRunState)
	}
	store.Close()

	applyOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--apply", "--finding", findingID})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(repair apply) err = %v", err)
	}
	for _, needle := range []string{"dry_run: false", "applied: true", "apply_action: expire_continuation_lease", "apply_scope: continuation_lease", "after_findings: 0"} {
		if !strings.Contains(applyOut, needle) {
			t.Fatalf("apply output = %q, want %q", applyOut, needle)
		}
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen apply check) err = %v", err)
	}
	defer store.Close()
	repaired, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(repaired) err = %v", err)
	}
	if repaired.Status != session.ContinuationStatusIdle || repaired.RemainingTurns != 0 || repaired.ActionProposal.Status != session.ProposalStatusExpired || repaired.ContinuationLease.Status != session.ContinuationLeaseStatusExpired || repaired.ContinuationLease.RemainingTurns != 0 {
		t.Fatalf("repaired state = %#v, want idle expired lease", repaired)
	}
	events, err := store.LatestExecutionEventsBySession(key, 10)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	if !executionEventsContainAuthorityRepair(events, findingID, "continuation_lease_expired") {
		t.Fatalf("events = %#v, want authority repair event for %s", events, findingID)
	}
	err = runAuthorityCommand([]string{"repair", "--config", cfgPath, "--apply", "--finding", findingID})
	if err == nil || !strings.Contains(err.Error(), "is not present") {
		t.Fatalf("second repair apply err = %v, want stale finding rejection", err)
	}
}

func TestRunAuthorityRepairApplyExpiresOperationPlanLeaseByFindingID(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 77712, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "77712"}}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:      "op-authority-plan-apply",
		Status:  session.OperationStatusActive,
		Summary: "Existing operation summary.",
		PlanLease: session.OperationPlanLease{
			ID:             "plan-lease-authority-apply",
			Status:         session.PlanLeaseStatusActive,
			TurnBudget:     1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(-time.Minute),
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	store.Close()

	previewOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(repair preview) err = %v", err)
	}
	findingID := authorityFindingIDFromOutput(t, previewOut, "expired_operation_plan_lease")
	if _, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--apply", "--finding", findingID})
	}); err != nil {
		t.Fatalf("runAuthorityCommand(repair operation apply) err = %v", err)
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen operation) err = %v", err)
	}
	defer store.Close()
	repaired, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if repaired.PlanLease.Status != session.PlanLeaseStatusExpired || repaired.PlanLease.RemainingTurns != 0 || !strings.Contains(repaired.Summary, "Authority repair expired") {
		t.Fatalf("operation state = %#v, want expired plan lease with evidence summary", repaired)
	}
	if len(repaired.Artifacts) == 0 || !strings.Contains(repaired.Artifacts[len(repaired.Artifacts)-1].Ref, findingID) {
		t.Fatalf("operation artifacts = %#v, want authority repair artifact", repaired.Artifacts)
	}
}

func TestRunAuthorityRepairApplyExpiresCapabilityGrantByFindingID(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-authority-expire",
		GrantedBy:      "telegram:1",
		GrantedTo:      "telegram:77713",
		Kind:           session.CapabilityKindPublicWeb,
		TargetResource: "example.com",
		AllowedActions: []string{"fetch"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       "{}",
		Constraints:    "{}",
		ExpiresAt:      now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	store.Close()

	previewOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(repair preview) err = %v", err)
	}
	findingID := authorityFindingIDFromOutput(t, previewOut, "active_capability_grant_expired")
	if _, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--apply", "--finding", findingID})
	}); err != nil {
		t.Fatalf("runAuthorityCommand(repair capability apply) err = %v", err)
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen grant) err = %v", err)
	}
	defer store.Close()
	grant, ok, err := store.CapabilityGrant("capg-authority-expire")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || grant.Status != session.CapabilityGrantStatusExpired {
		t.Fatalf("grant = %#v ok=%t, want expired grant", grant, ok)
	}
}

func TestRunAuthorityRepairApplyRevokesTailnetBindingLocally(t *testing.T) {
	root := t.TempDir()
	cfgPath := writeMaintenanceConfig(t, root)
	cfg, _, err := loadConfigForCommand(cfgPath)
	if err != nil {
		t.Fatalf("loadConfigForCommand() err = %v", err)
	}
	store, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-tailnet-authority",
		GrantedBy:      "telegram:1",
		GrantedTo:      "telegram:77714",
		Kind:           session.CapabilityKindNetworkAccess,
		TargetResource: "grafana.tailnet",
		AllowedActions: []string{"connect"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       "{}",
		Constraints:    "{}",
		ExpiresAt:      now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	if _, err := store.UpsertTailnetGrantBinding(session.TailnetGrantBinding{
		BindingID:         "tailnet-bind-authority-missing-surface",
		GrantID:           "capg-tailnet-authority",
		SurfaceID:         "tailnet:missing:surface-authority",
		GrantedTo:         "telegram:77714",
		CapabilityKind:    string(session.CapabilityKindNetworkAccess),
		TargetResource:    "grafana.tailnet",
		DesiredPolicyJSON: `{"grant_id":"capg-tailnet-authority"}`,
		Status:            session.TailnetGrantBindingStatusApplied,
		AppliedPolicyHash: "sha256:applied-authority",
	}); err != nil {
		t.Fatalf("UpsertTailnetGrantBinding() err = %v", err)
	}
	store.Close()

	previewOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(repair preview) err = %v", err)
	}
	findingID := authorityFindingIDFromOutput(t, previewOut, "tailnet_binding_surface_missing")
	if !strings.Contains(previewOut, "apply_action=revoke_tailnet_grant_binding") {
		t.Fatalf("preview output = %q, want local revoke repair action", previewOut)
	}
	if _, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"repair", "--config", cfgPath, "--apply", "--finding", findingID})
	}); err != nil {
		t.Fatalf("runAuthorityCommand(repair tailnet apply) err = %v", err)
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen tailnet) err = %v", err)
	}
	defer store.Close()
	binding, ok, err := store.TailnetGrantBinding("tailnet-bind-authority-missing-surface")
	if err != nil {
		t.Fatalf("TailnetGrantBinding() err = %v", err)
	}
	if !ok || binding.Status != session.TailnetGrantBindingStatusRevoked || !strings.Contains(binding.DriftReason, "authority_repair") {
		t.Fatalf("binding = %#v ok=%t, want locally revoked binding", binding, ok)
	}
}
