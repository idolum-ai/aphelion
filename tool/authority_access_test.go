//go:build linux

package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestAuthorityManagedToolRequiresTurnLeaseEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "leased_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-leased-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "leased_tool",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	_, _, _, err := registry.requireAuthorityToolAccess(context.Background(), "leased_tool", actor, key, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "requires durable run authority evidence") {
		t.Fatalf("requireAuthorityToolAccess() err = %v, want missing durable run authority", err)
	}
	invocations, err := store.CapabilityInvocationsByGrant("capg-leased-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(blocked) err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].Status != "blocked" || invocations[0].ContinuationLeaseID != "" {
		t.Fatalf("blocked invocations = %#v, want blocked without lease evidence", invocations)
	}

	grantAuthorityUseLease(t, store, key)
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, "lease-authority-use-"+session.SessionIDForKey(key), session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "test")
	grant, permit, managed, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("requireAuthorityToolAccess(with run authority) err = %v", err)
	}
	if !managed || grant.GrantID != "capg-leased-tool" {
		t.Fatalf("grant=%#v managed=%t, want capg-leased-tool managed", grant, managed)
	}
	if permit == nil || permit.InvocationID <= 0 {
		t.Fatalf("permit = %#v, want durable invocation permit", permit)
	}
	invocations, err = store.CapabilityInvocationsByGrant("capg-leased-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(allowed) err = %v", err)
	}
	if len(invocations) < 2 || invocations[0].Status != "allowed" || invocations[0].ContinuationLeaseID == "" || invocations[0].SessionID == "" || invocations[0].TurnRunID <= 0 {
		t.Fatalf("allowed invocations = %#v, want run-authority-backed allowed invocation", invocations)
	}
}

func TestAuthorityManagedToolDoesNotMintRunAuthorityFromAmbientSessionLease(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "leased_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-no-ambient-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "leased_tool",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-ambient-not-causal")
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "leased_tool", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "requires durable run authority evidence") {
		t.Fatalf("ExecuteForSessionPrincipal() err = %v, want durable run authority blocker", err)
	}
	if run, err := store.LatestTurnRun(key); err == nil {
		t.Fatalf("LatestTurnRun() = %#v, want no synthetic run authority admission", run)
	} else if err != sql.ErrNoRows {
		t.Fatalf("LatestTurnRun() err = %v, want sql.ErrNoRows", err)
	}
}

func TestAuthorityManagedToolUsesContextLeaseEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "leased_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-context-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "leased_tool",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-context-tool")
	ctx, turnRunID := contextWithContinuationRunAuthority(t, store, key, actor, "lease-context-tool", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_continuation")
	grant, permit, managed, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("requireAuthorityToolAccess(context run authority) err = %v", err)
	}
	if !managed || grant.GrantID != "capg-context-tool" {
		t.Fatalf("grant=%#v managed=%t, want capg-context-tool managed", grant, managed)
	}
	if permit == nil || permit.InvocationID <= 0 {
		t.Fatalf("permit = %#v, want durable invocation permit", permit)
	}
	invocations, err := store.CapabilityInvocationsByGrant("capg-context-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(context) err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].Status != "allowed" || invocations[0].TurnRunID != turnRunID || invocations[0].ContinuationLeaseID != "lease-context-tool" || invocations[0].AuthoritySource != "continuation_lease" {
		t.Fatalf("context invocations = %#v, want allowed invocation with durable run authority", invocations)
	}
}

func TestAuthorityManagedToolRejectsFabricatedContextLeaseEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "leased_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-context-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "leased_tool",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	ctx := WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
		SessionID: session.SessionIDForKey(key),
		TurnRunID: 999999,
	})
	_, _, _, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "not durable") {
		t.Fatalf("requireAuthorityToolAccess(fabricated) err = %v, want fabricated run-authority rejection", err)
	}
}

func TestAuthorityManagedToolRejectsInvalidContextLeaseEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "leased_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-context-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "leased_tool",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	now := time.Now().UTC()
	cases := []struct {
		name    string
		lease   session.ContinuationLease
		ref     session.AuthorityUseRef
		mintRun bool
		wantErr string
	}{
		{
			name: "sessionless",
			ref: session.AuthorityUseRef{
				TurnRunID: 1,
			},
			wantErr: "session_id",
		},
		{
			name: "expired",
			lease: session.ContinuationLease{
				ID:             "lease-expired-context",
				Status:         session.ContinuationLeaseStatusActive,
				RemainingTurns: 1,
				ExpiresAt:      now.Add(-time.Minute),
			},
			ref: session.AuthorityUseRef{
				SessionID: session.SessionIDForKey(key),
			},
			mintRun: true,
			wantErr: "not active",
		},
		{
			name: "exhausted",
			lease: session.ContinuationLease{
				ID:             "lease-exhausted-context",
				Status:         session.ContinuationLeaseStatusActive,
				RemainingTurns: 0,
				ExpiresAt:      now.Add(time.Hour),
			},
			ref: session.AuthorityUseRef{
				SessionID: session.SessionIDForKey(key),
			},
			mintRun: true,
			wantErr: "not active",
		},
		{
			name: "revoked",
			lease: session.ContinuationLease{
				ID:             "lease-revoked-context",
				Status:         session.ContinuationLeaseStatusRevoked,
				RemainingTurns: 1,
				ExpiresAt:      now.Add(time.Hour),
			},
			ref: session.AuthorityUseRef{
				SessionID: session.SessionIDForKey(key),
			},
			mintRun: true,
			wantErr: "not active",
		},
		{
			name: "wrong source",
			lease: session.ContinuationLease{
				ID:             "lease-wrong-source-context",
				Status:         session.ContinuationLeaseStatusActive,
				RemainingTurns: 1,
				ExpiresAt:      now.Add(time.Hour),
			},
			ref: session.AuthorityUseRef{
				SessionID:            session.SessionIDForKey(key),
				AuthoritySource:      "operation_plan_lease",
				OperationPlanLeaseID: "plan-lease-not-run",
			},
			mintRun: true,
			wantErr: "operation plan lease outside the run authority",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.TrimSpace(tc.lease.ID) != "" {
				if err := store.UpdateContinuationState(key, session.ContinuationState{
					Status:            session.ContinuationStatusApproved,
					RemainingTurns:    tc.lease.RemainingTurns,
					ContinuationLease: tc.lease,
				}); err != nil {
					t.Fatalf("UpdateContinuationState() err = %v", err)
				}
			}
			ref := tc.ref
			if tc.mintRun {
				_, turnRunID := contextWithContinuationRunAuthority(t, store, key, actor, tc.lease.ID, tc.lease.Status, tc.lease.RemainingTurns, tc.lease.ExpiresAt, "test")
				ref.TurnRunID = turnRunID
			}
			_, _, _, err := registry.requireAuthorityToolAccess(WithAuthorityUseRef(context.Background(), ref), "leased_tool", actor, key, json.RawMessage(`{}`))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("requireAuthorityToolAccess(%s) err = %v, want %q", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestAuthorityManagedToolRejectsMismatchedContextLeaseEvidence(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	manifest := ExternalToolManifest{
		Name:      "leased_tool",
		Owner:     "child-alpha",
		Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
	}
	if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
		t.Fatalf("WithExternalToolManifests() err = %v", err)
	}
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-context-tool",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "leased_tool",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	grantAuthorityUseLeaseWithID(t, store, key, "lease-other-session")
	_, turnRunID := contextWithContinuationRunAuthority(t, store, key, actor, "lease-other-session", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "test")
	ctx := WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
		SessionID: "telegram_dm:9999",
		TurnRunID: turnRunID,
	})
	_, _, _, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "authority evidence belongs to session") {
		t.Fatalf("requireAuthorityToolAccess(mismatch) err = %v, want session mismatch", err)
	}
}

func TestAuthorityManagedToolRejectsTerminalRunAuthorityReplay(t *testing.T) {
	t.Parallel()

	for _, status := range []session.TurnRunStatus{
		session.TurnRunStatusCompleted,
		session.TurnRunStatusFailed,
		session.TurnRunStatusInterrupted,
	} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			registry, store := newDurableAgentToolRegistry(t)
			manifest := ExternalToolManifest{
				Name:      "leased_tool",
				Owner:     "child-alpha",
				Execution: ExternalToolManifestExecution{Mode: "process", Entry: "./run.sh"},
			}
			if _, err := registry.WithExternalToolManifests([]ExternalToolManifest{manifest}); err != nil {
				t.Fatalf("WithExternalToolManifests() err = %v", err)
			}
			if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: "leased_tool", ImplementationRef: "external:leased_tool", Registered: true}); err != nil {
				t.Fatalf("UpsertRegisteredTool() err = %v", err)
			}
			if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
				GrantID:        "capg-terminal-tool-" + string(status),
				GrantedBy:      "telegram:1001",
				GrantedTo:      "telegram:1001",
				Kind:           session.CapabilityKindTool,
				TargetResource: "leased_tool",
				AllowedActions: []string{"invoke"},
				Status:         session.CapabilityGrantStatusActive,
			}); err != nil {
				t.Fatalf("UpsertCapabilityGrant() err = %v", err)
			}

			key := adminSessionKey()
			actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
			grantAuthorityUseLeaseWithID(t, store, key, "lease-terminal-"+string(status))
			ctx, turnRunID := contextWithContinuationRunAuthority(t, store, key, actor, "lease-terminal-"+string(status), session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "terminal_replay")
			if err := store.CompleteTurnRun(turnRunID, status, "terminal replay regression"); err != nil {
				t.Fatalf("CompleteTurnRun() err = %v", err)
			}

			_, _, _, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
			if err == nil || !strings.Contains(err.Error(), "execution authority turn run") || !strings.Contains(err.Error(), string(status)) {
				t.Fatalf("requireAuthorityToolAccess() err = %v, want terminal run replay denial", err)
			}
		})
	}
}

func contextWithContinuationRunAuthority(t *testing.T, store *session.SQLiteStore, key session.SessionKey, actor principal.Principal, leaseID string, status session.ContinuationLeaseStatus, remainingTurns int, expiresAt time.Time, species string) (context.Context, int64) {
	t.Helper()

	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "authority continuity test")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if species == "" {
		species = "test"
	}
	_, err = store.UpsertExecutionRunAuthority(session.ExecutionRunAuthority{
		TurnRunID:           run.ID,
		SessionID:           run.SessionID,
		ChatID:              run.ChatID,
		UserID:              run.UserID,
		Scope:               run.Scope,
		Principal:           toolAuthorityCanonicalPrincipal(actor),
		PrincipalRole:       string(actor.Role),
		ExecutionSpecies:    species,
		LeaseKind:           session.ExecutionAuthorityLeaseKindContinuation,
		ContinuationLeaseID: leaseID,
		LeaseStatus:         string(status),
		LeaseRemainingTurns: remainingTurns,
		LeaseExpiresAt:      expiresAt,
		AdmittedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertExecutionRunAuthority() err = %v", err)
	}
	return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{SessionID: run.SessionID, TurnRunID: run.ID}), run.ID
}
