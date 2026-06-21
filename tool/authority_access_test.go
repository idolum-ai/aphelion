//go:build linux

package tool

import (
	"context"
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
	_, _, err := registry.requireAuthorityToolAccess(context.Background(), "leased_tool", actor, key, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "requires active continuation or operation plan lease evidence") {
		t.Fatalf("requireAuthorityToolAccess() err = %v, want missing lease evidence", err)
	}
	invocations, err := store.CapabilityInvocationsByGrant("capg-leased-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(blocked) err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].Status != "blocked" || invocations[0].ContinuationLeaseID != "" {
		t.Fatalf("blocked invocations = %#v, want blocked without lease evidence", invocations)
	}

	grantAuthorityUseLease(t, store, key)
	grant, managed, err := registry.requireAuthorityToolAccess(context.Background(), "leased_tool", actor, key, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("requireAuthorityToolAccess(with lease) err = %v", err)
	}
	if !managed || grant.GrantID != "capg-leased-tool" {
		t.Fatalf("grant=%#v managed=%t, want capg-leased-tool managed", grant, managed)
	}
	invocations, err = store.CapabilityInvocationsByGrant("capg-leased-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(allowed) err = %v", err)
	}
	if len(invocations) < 2 || invocations[0].Status != "allowed" || invocations[0].ContinuationLeaseID == "" || invocations[0].SessionID == "" {
		t.Fatalf("allowed invocations = %#v, want lease-backed allowed invocation", invocations)
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
	ctx := WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
		SessionID:           session.SessionIDForKey(key),
		ContinuationLeaseID: "lease-context-tool",
		AuthoritySource:     "continuation_lease",
	})
	grant, managed, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("requireAuthorityToolAccess(context lease) err = %v", err)
	}
	if !managed || grant.GrantID != "capg-context-tool" {
		t.Fatalf("grant=%#v managed=%t, want capg-context-tool managed", grant, managed)
	}
	invocations, err := store.CapabilityInvocationsByGrant("capg-context-tool", 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(context) err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].Status != "allowed" || invocations[0].ContinuationLeaseID != "lease-context-tool" || invocations[0].AuthoritySource != "continuation_lease" {
		t.Fatalf("context invocations = %#v, want allowed invocation with context lease evidence", invocations)
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
		SessionID:           session.SessionIDForKey(key),
		ContinuationLeaseID: "lease-fabricated",
		AuthoritySource:     "continuation_lease",
	})
	_, _, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "does not match current session lease") && !strings.Contains(err.Error(), "not durable") {
		t.Fatalf("requireAuthorityToolAccess(fabricated) err = %v, want fabricated lease rejection", err)
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
		wantErr string
	}{
		{
			name: "sessionless",
			ref: session.AuthorityUseRef{
				ContinuationLeaseID: "lease-sessionless",
				AuthoritySource:     "continuation_lease",
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
				SessionID:           session.SessionIDForKey(key),
				ContinuationLeaseID: "lease-expired-context",
				AuthoritySource:     "continuation_lease",
			},
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
				SessionID:           session.SessionIDForKey(key),
				ContinuationLeaseID: "lease-exhausted-context",
				AuthoritySource:     "continuation_lease",
			},
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
				SessionID:           session.SessionIDForKey(key),
				ContinuationLeaseID: "lease-revoked-context",
				AuthoritySource:     "continuation_lease",
			},
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
				SessionID:           session.SessionIDForKey(key),
				ContinuationLeaseID: "lease-wrong-source-context",
				AuthoritySource:     "operation_plan_lease",
			},
			wantErr: "operation_plan_lease_id",
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
			_, _, err := registry.requireAuthorityToolAccess(WithAuthorityUseRef(context.Background(), tc.ref), "leased_tool", actor, key, json.RawMessage(`{}`))
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
	ctx := WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
		SessionID:           "telegram_dm:9999",
		ContinuationLeaseID: "lease-other-session",
		AuthoritySource:     "continuation_lease",
	})
	_, _, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "authority evidence belongs to session") {
		t.Fatalf("requireAuthorityToolAccess(mismatch) err = %v, want session mismatch", err)
	}
}
