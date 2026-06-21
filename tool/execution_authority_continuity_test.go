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
			name:      "interactive durable fallback uses current continuation lease",
			species:   "interactive",
			grantActs: []string{"invoke"},
			setup: func(t *testing.T, store *session.SQLiteStore, key session.SessionKey) context.Context {
				grantAuthorityUseLeaseWithID(t, store, key, "lease-matrix-continuation")
				return context.Background()
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
				grantAuthorityUseLeaseWithID(t, store, key, "lease-matrix-context")
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:           session.SessionIDForKey(key),
					ContinuationLeaseID: "lease-matrix-context",
					AuthoritySource:     "continuation_lease",
				})
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
				grantOperationPlanLeaseWithID(t, store, key, "plan-lease-matrix")
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:            session.SessionIDForKey(key),
					OperationPlanLeaseID: "plan-lease-matrix",
					AuthoritySource:      "operation_plan_lease",
				})
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
					SessionID:           session.SessionIDForKey(key),
					ContinuationLeaseID: "lease-fabricated-matrix",
					AuthoritySource:     "continuation_lease",
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
				grantAuthorityUseLeaseWithID(t, store, key, "lease-session-matrix")
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:           "telegram_dm:9999",
					ContinuationLeaseID: "lease-session-matrix",
					AuthoritySource:     "continuation_lease",
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
				storeContinuationLeaseForMatrix(t, store, key, session.ContinuationLease{
					ID:             "lease-expired-matrix",
					Status:         session.ContinuationLeaseStatusActive,
					RemainingTurns: 1,
					ExpiresAt:      time.Now().UTC().Add(-time.Minute),
				})
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:           session.SessionIDForKey(key),
					ContinuationLeaseID: "lease-expired-matrix",
					AuthoritySource:     "continuation_lease",
				})
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
				storeContinuationLeaseForMatrix(t, store, key, session.ContinuationLease{
					ID:             "lease-exhausted-matrix",
					Status:         session.ContinuationLeaseStatusActive,
					RemainingTurns: 0,
					ExpiresAt:      time.Now().UTC().Add(time.Hour),
				})
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:           session.SessionIDForKey(key),
					ContinuationLeaseID: "lease-exhausted-matrix",
					AuthoritySource:     "continuation_lease",
				})
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
				storeOperationPlanLeaseForMatrix(t, store, key, session.OperationPlanLease{
					ID:             "plan-lease-revoked-matrix",
					Status:         session.PlanLeaseStatusRevoked,
					RemainingTurns: 1,
					ExpiresAt:      time.Now().UTC().Add(time.Hour),
				})
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:            session.SessionIDForKey(key),
					OperationPlanLeaseID: "plan-lease-revoked-matrix",
					AuthoritySource:      "operation_plan_lease",
				})
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
				leaseID := "lease-restart-revalidated-matrix"
				grantAuthorityUseLeaseWithID(t, store, key, leaseID)
				ctx := WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:           session.SessionIDForKey(key),
					ContinuationLeaseID: leaseID,
					AuthoritySource:     "continuation_lease",
				})
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
				grantAuthorityUseLeaseWithID(t, store, key, "lease-action-mismatch-matrix")
				return WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{
					SessionID:           session.SessionIDForKey(key),
					ContinuationLeaseID: "lease-action-mismatch-matrix",
					AuthoritySource:     "continuation_lease",
				})
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
			_, _, err := registry.requireAuthorityToolAccess(ctx, "leased_tool", actor, key, json.RawMessage(`{}`))
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
