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

func TestExecutionAuthorityContinuityToolBoundaryMatrix(t *testing.T) {
	t.Parallel()

	type expectedInvocation struct {
		status               string
		turnRunID            int64
		authoritySource      string
		continuationLeaseID  string
		operationPlanLeaseID string
	}
	cases := []struct {
		name       string
		species    string
		grantActs  []string
		setup      func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context
		wantErr    string
		invocation *expectedInvocation
	}{
		{
			name:      "interactive context uses durable run authority bound to continuation lease",
			species:   "interactive",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				grantAuthorityUseLeaseWithID(t, store, key, "lease-matrix-continuation")
				ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, "lease-matrix-continuation", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "interactive")
				return ctx
			},
			invocation: &expectedInvocation{
				status:              "allowed",
				authoritySource:     "continuation_lease",
				continuationLeaseID: "lease-matrix-continuation",
			},
		},
		{
			name:      "native continuation context revalidates current continuation lease",
			species:   "native_continuation",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				grantAuthorityUseLeaseWithID(t, store, key, "lease-matrix-context")
				ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, "lease-matrix-context", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_continuation")
				return ctx
			},
			invocation: &expectedInvocation{
				status:              "allowed",
				authoritySource:     "continuation_lease",
				continuationLeaseID: "lease-matrix-context",
			},
		},
		{
			name:      "operation plan continuation context revalidates current plan lease",
			species:   "operation_plan_continuation",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				grantOperationPlanLeaseWithID(t, store, key, "plan-lease-matrix")
				ctx, _ := contextWithOperationPlanRunAuthority(t, store, key, actor, "plan-lease-matrix", session.PlanLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "operation_plan_continuation")
				return ctx
			},
			invocation: &expectedInvocation{
				status:               "allowed",
				authoritySource:      "operation_plan_lease",
				operationPlanLeaseID: "plan-lease-matrix",
			},
		},
		{
			name:      "durable child context cannot fabricate lease evidence",
			species:   "durable_group_child",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID: session.SessionIDForKey(key),
					TurnRunID: 999999,
				})
			},
			wantErr: "not durable",
			invocation: &expectedInvocation{
				status: "blocked",
			},
		},
		{
			name:      "remote child context cannot cross session boundary",
			species:   "remote_child",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				grantAuthorityUseLeaseWithID(t, store, key, "lease-session-matrix")
				_, turnRunID := contextWithContinuationRunAuthority(t, store, key, actor, "lease-session-matrix", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "remote_child")
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID: "telegram_dm:9999",
					TurnRunID: turnRunID,
				})
			},
			wantErr: "authority evidence belongs to session",
			invocation: &expectedInvocation{
				status: "blocked",
			},
		},
		{
			name:      "maintenance recovery rejects expired continuation lease",
			species:   "maintenance_recovery",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				storeContinuationLeaseForMatrix(t, store, key, session.ContinuationLease{
					ID:             "lease-expired-matrix",
					Status:         session.ContinuationLeaseStatusActive,
					RemainingTurns: 1,
					ExpiresAt:      time.Now().UTC().Add(-time.Minute),
				})
				ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, "lease-expired-matrix", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(-time.Minute), "maintenance_recovery")
				return ctx
			},
			wantErr: "not active",
			invocation: &expectedInvocation{
				status: "blocked",
			},
		},
		{
			name:      "scheduled continuation rejects exhausted continuation lease",
			species:   "scheduled_continuation",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				storeContinuationLeaseForMatrix(t, store, key, session.ContinuationLease{
					ID:             "lease-exhausted-matrix",
					Status:         session.ContinuationLeaseStatusActive,
					RemainingTurns: 0,
					ExpiresAt:      time.Now().UTC().Add(time.Hour),
				})
				ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, "lease-exhausted-matrix", session.ContinuationLeaseStatusActive, 0, time.Now().UTC().Add(time.Hour), "scheduled_continuation")
				return ctx
			},
			wantErr: "not active",
			invocation: &expectedInvocation{
				status: "blocked",
			},
		},
		{
			name:      "operation plan continuation rejects revoked plan lease",
			species:   "operation_plan_continuation",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				storeOperationPlanLeaseForMatrix(t, store, key, session.OperationPlanLease{
					ID:             "plan-lease-revoked-matrix",
					Status:         session.PlanLeaseStatusRevoked,
					RemainingTurns: 1,
					ExpiresAt:      time.Now().UTC().Add(time.Hour),
				})
				ctx, _ := contextWithOperationPlanRunAuthority(t, store, key, actor, "plan-lease-revoked-matrix", session.PlanLeaseStatusRevoked, 1, time.Now().UTC().Add(time.Hour), "operation_plan_continuation")
				return ctx
			},
			wantErr: "not active",
			invocation: &expectedInvocation{
				status: "blocked",
			},
		},
		{
			name:      "restart revalidates minted context after durable lease revocation",
			species:   "restart_revalidation",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				leaseID := "lease-restart-revalidated-matrix"
				grantAuthorityUseLeaseWithID(t, store, key, leaseID)
				ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, leaseID, session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "restart_revalidation")
				storeContinuationLeaseForMatrix(t, store, key, session.ContinuationLease{
					ID:             leaseID,
					Status:         session.ContinuationLeaseStatusRevoked,
					RemainingTurns: 1,
					ExpiresAt:      time.Now().UTC().Add(time.Hour),
				})
				return ctx
			},
			wantErr: "not active",
			invocation: &expectedInvocation{
				status: "blocked",
			},
		},
		{
			name:      "valid lease does not repair grant action mismatch",
			species:   "native_continuation",
			grantActs: []string{"inspect"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
				grantAuthorityUseLeaseWithID(t, store, key, "lease-action-mismatch-matrix")
				ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, "lease-action-mismatch-matrix", session.ContinuationLeaseStatusActive, 1, time.Now().UTC().Add(time.Hour), "native_continuation")
				return ctx
			},
			wantErr: "not granted",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
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
				GrantID:        "capg-matrix-tool",
				GrantedBy:      "telegram:1001",
				GrantedTo:      "telegram:1001",
				Kind:           session.CapabilityKindTool,
				TargetResource: "leased_tool",
				AllowedActions: tc.grantActs,
				Status:         session.CapabilityGrantStatusActive,
			}); err != nil {
				t.Fatalf("UpsertCapabilityGrant(%s) err = %v", tc.species, err)
			}

			actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
			key := adminSessionKey()
			ctx := tc.setup(t, store, key)
			_, _, _, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("%s requireAuthorityToolAccess() err = %v, want %q", tc.species, err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("%s requireAuthorityToolAccess() err = %v", tc.species, err)
			}

			if tc.invocation == nil {
				return
			}
			invocations, err := store.CapabilityInvocationsByGrant("capg-matrix-tool", 10)
			if err != nil {
				t.Fatalf("CapabilityInvocationsByGrant(%s) err = %v", tc.species, err)
			}
			if len(invocations) != 1 {
				t.Fatalf("%s invocations = %#v, want one invocation", tc.species, invocations)
			}
			got := invocations[0]
			if got.Status != tc.invocation.status {
				t.Fatalf("%s invocation status = %q, want %q", tc.species, got.Status, tc.invocation.status)
			}
			if tc.invocation.status == "allowed" && got.TurnRunID <= 0 {
				t.Fatalf("%s invocation turn_run_id = %d, want durable run authority", tc.species, got.TurnRunID)
			}
			if tc.invocation.authoritySource != "" && got.AuthoritySource != tc.invocation.authoritySource {
				t.Fatalf("%s authority source = %q, want %q", tc.species, got.AuthoritySource, tc.invocation.authoritySource)
			}
			if tc.invocation.continuationLeaseID != "" && got.ContinuationLeaseID != tc.invocation.continuationLeaseID {
				t.Fatalf("%s continuation lease = %q, want %q", tc.species, got.ContinuationLeaseID, tc.invocation.continuationLeaseID)
			}
			if tc.invocation.operationPlanLeaseID != "" && got.OperationPlanLeaseID != tc.invocation.operationPlanLeaseID {
				t.Fatalf("%s operation plan lease = %q, want %q", tc.species, got.OperationPlanLeaseID, tc.invocation.operationPlanLeaseID)
			}
		})
	}
}

func contextWithOperationPlanRunAuthority(t *testing.T, store *session.SQLiteStore, key session.SessionKey, actor principal.Principal, leaseID string, status session.PlanLeaseStatus, remainingTurns int, expiresAt time.Time, species string) (context.Context, int64) {
	t.Helper()

	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "operation-plan authority continuity test")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if species == "" {
		species = "test"
	}
	_, err = store.UpsertExecutionRunAuthority(session.ExecutionRunAuthority{
		TurnRunID:            run.ID,
		SessionID:            run.SessionID,
		ChatID:               run.ChatID,
		UserID:               run.UserID,
		Scope:                run.Scope,
		Principal:            toolAuthorityCanonicalPrincipal(actor),
		PrincipalRole:        string(actor.Role),
		ExecutionSpecies:     species,
		LeaseKind:            session.ExecutionAuthorityLeaseKindOperationPlan,
		OperationPlanLeaseID: leaseID,
		LeaseStatus:          string(status),
		LeaseRemainingTurns:  remainingTurns,
		LeaseExpiresAt:       expiresAt,
		AdmittedAt:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertExecutionRunAuthority(operation plan) err = %v", err)
	}
	return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{SessionID: run.SessionID, TurnRunID: run.ID}), run.ID
}

func grantOperationPlanLeaseWithID(t *testing.T, store *session.SQLiteStore, key session.SessionKey, leaseID string) {
	t.Helper()

	storeOperationPlanLeaseForMatrix(t, store, key, session.OperationPlanLease{
		ID:             leaseID,
		Status:         session.PlanLeaseStatusActive,
		TurnBudget:     1,
		RemainingTurns: 1,
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	})
}

func storeContinuationLeaseForMatrix(t *testing.T, store *session.SQLiteStore, key session.SessionKey, lease session.ContinuationLease) {
	t.Helper()

	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusApproved,
		RemainingTurns:    lease.RemainingTurns,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState(matrix) err = %v", err)
	}
}

func storeOperationPlanLeaseForMatrix(t *testing.T, store *session.SQLiteStore, key session.SessionKey, lease session.OperationPlanLease) {
	t.Helper()

	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-matrix",
		Objective: "Exercise execution-authority continuity.",
		Status:    session.OperationStatusActive,
		PlanLease: lease,
	}); err != nil {
		t.Fatalf("UpdateOperationState(matrix) err = %v", err)
	}
}
