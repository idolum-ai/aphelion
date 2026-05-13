//go:build linux

package main

import (
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func TestRunAuthorityRepairApplyRejectsPreviewOnlyFinding(t *testing.T) {
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
	key := session.SessionKey{ChatID: 77715, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "77715"}}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "missing-decision-authority",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:        "proposal-missing-decision-authority",
			Status:    session.ProposalStatusPending,
			ExpiresAt: now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-missing-decision-authority",
			ProposalID:     "proposal-missing-decision-authority",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
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
	findingID := authorityFindingIDFromOutput(t, previewOut, "pending_proposal_missing_decision")
	if !strings.Contains(previewOut, "suggested_repair=") || strings.Contains(previewOut, "apply_action=") || strings.Contains(previewOut, "applicable=true") {
		t.Fatalf("preview output = %q, want suggested-only pending decision repair", previewOut)
	}
	err = runAuthorityCommand([]string{"repair", "--config", cfgPath, "--apply", "--finding", findingID})
	if err == nil || !strings.Contains(err.Error(), "no apply_action") {
		t.Fatalf("runAuthorityCommand(suggested-only apply) err = %v, want no apply_action rejection", err)
	}
}

func TestRunAuthorityRevokeGrantCommandRevokesExplicitGrantWithEvidence(t *testing.T) {
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
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-authority-revoke-stale-email",
		RequestID:      "capreq-authority-revoke-stale-email",
		GrantedBy:      "telegram:1",
		GrantedTo:      "durable_agent:child-mail-archiver",
		Kind:           session.CapabilityKindTool,
		TargetResource: "gog_cli",
		AllowedActions: []string{"invoke_archive_approved_threads"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       "{}",
		Constraints:    "{}",
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	store.Close()

	doctorOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"doctor", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(doctor) err = %v", err)
	}
	if !strings.Contains(doctorOut, "child_runtime_contract_missing") {
		t.Fatalf("doctor output = %q, want child runtime warning before revoke", doctorOut)
	}
	applyOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{
			"revoke-grant",
			"--config", cfgPath,
			"--grant-id", "grant-authority-revoke-stale-email",
			"--reason", "stale parent-visible email archive grant belongs to the email child",
			"--apply",
		})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(revoke-grant) err = %v", err)
	}
	for _, needle := range []string{"action: authority-revoke-grant", "applied: true", "prior_status: active", "status: revoked", "changed: true"} {
		if !strings.Contains(applyOut, needle) {
			t.Fatalf("revoke output = %q, want %q", applyOut, needle)
		}
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer store.Close()
	grant, ok, err := store.CapabilityGrant("grant-authority-revoke-stale-email")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || grant.Status != session.CapabilityGrantStatusRevoked || grant.RevokedAt.IsZero() {
		t.Fatalf("grant = %#v ok=%t, want revoked grant with timestamp", grant, ok)
	}
	events, err := store.LatestExecutionEventsBySession(maintenanceRepairKey(), 10)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	if !executionEventsContainStatus(events, core.ExecutionEventCapabilityGrantChanged, "authority_maintenance", "revoked", `"grant_id":"grant-authority-revoke-stale-email"`) {
		t.Fatalf("events = %#v, want authority maintenance grant revoke evidence", events)
	}
}

func TestRunAuthorityRevokeContinuationCommandClosesMissingDecisionFinding(t *testing.T) {
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
	key := session.SessionKey{ChatID: 77716, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "77716"}}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		DecisionID:     "missing-decision-authority-maintenance",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:        "proposal-authority-maintenance",
			Status:    session.ProposalStatusPending,
			ExpiresAt: now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-authority-maintenance",
			ProposalID:     "proposal-authority-maintenance",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	store.Close()

	doctorOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"doctor", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(doctor before) err = %v", err)
	}
	if !strings.Contains(doctorOut, "pending_proposal_missing_decision") {
		t.Fatalf("doctor output = %q, want missing decision warning", doctorOut)
	}
	applyOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{
			"revoke-continuation",
			"--config", cfgPath,
			"--chat-id", "77716",
			"--proposal-id", "proposal-authority-maintenance",
			"--reason", "stale proposal references a missing pending decision",
			"--apply",
		})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(revoke-continuation) err = %v", err)
	}
	for _, needle := range []string{"action: authority-revoke-continuation", "applied: true", "prior_status: pending", "status: revoked", "changed: true"} {
		if !strings.Contains(applyOut, needle) {
			t.Fatalf("revoke continuation output = %q, want %q", applyOut, needle)
		}
	}

	store, err = session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer store.Close()
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusRevoked || state.DecisionID != "" || state.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked || state.ContinuationLease.RemainingTurns != 0 {
		t.Fatalf("continuation = %#v, want revoked missing-decision continuation", state)
	}
	events, err := store.LatestExecutionEventsBySession(key, 10)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	if !executionEventsContainStatus(events, core.ExecutionEventContinuationRevoked, "authority_maintenance", "revoked", `"proposal_id":"proposal-authority-maintenance"`) {
		t.Fatalf("events = %#v, want continuation revoke evidence", events)
	}
	afterOut, err := captureStdout(t, func() error {
		return runAuthorityCommand([]string{"doctor", "--config", cfgPath, "--limit", "10"})
	})
	if err != nil {
		t.Fatalf("runAuthorityCommand(doctor after) err = %v", err)
	}
	if strings.Contains(afterOut, "pending_proposal_missing_decision") {
		t.Fatalf("doctor output after revoke = %q, want missing decision warning closed", afterOut)
	}
}
