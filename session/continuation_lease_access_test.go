//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestContinuationLeaseActionAccessAndPersistence(t *testing.T) {
	now := time.Date(2026, time.May, 4, 20, 30, 0, 0, time.UTC)
	lease := ContinuationLease{
		ID:               "lease-local-workspace",
		ProposalID:       "aprop-local-workspace",
		Status:           ContinuationLeaseStatusActive,
		MaxTurns:         2,
		RemainingTurns:   2,
		AllowedActions:   []string{"workspace_write", "focused_tests", "git_diff_check"},
		ForbiddenActions: []string{"deploy", "restart"},
		ExpiresAt:        now.Add(time.Hour),
	}

	allowed := CheckContinuationLeaseAction(lease, "workspace-write", now)
	if !allowed.Allowed || allowed.Reason != "allowed" {
		t.Fatalf("workspace action decision = %#v, want allowed", allowed)
	}
	forbidden := CheckContinuationLeaseAction(lease, "deploy", now)
	if forbidden.Allowed || forbidden.Reason != "action_forbidden" {
		t.Fatalf("deploy decision = %#v, want forbidden", forbidden)
	}
	missing := CheckContinuationLeaseAction(lease, "commit", now)
	if missing.Allowed || missing.Reason != "action_not_allowed" {
		t.Fatalf("commit decision = %#v, want not allowed", missing)
	}
	expired := CheckContinuationLeaseAction(lease, "workspace_write", now.Add(2*time.Hour))
	if expired.Allowed || expired.Reason != "lease_inactive_or_expired" {
		t.Fatalf("expired decision = %#v, want inactive/expired", expired)
	}

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	key := SessionKey{ChatID: 9201, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "telegram_dm:9201"}}
	if err := store.UpdateContinuationState(key, ContinuationState{
		Status:            ContinuationStatusApproved,
		RemainingTurns:    2,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	reloaded, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	persisted := CheckContinuationLeaseAction(reloaded.ContinuationLease, "focused_tests", now)
	if !persisted.Allowed {
		t.Fatalf("persisted decision = %#v, want focused_tests allowed", persisted)
	}
	persistedForbidden := CheckContinuationLeaseAction(reloaded.ContinuationLease, "restart", now)
	if persistedForbidden.Allowed || persistedForbidden.Reason != "action_forbidden" {
		t.Fatalf("persisted forbidden decision = %#v, want restart forbidden", persistedForbidden)
	}
}

func TestContinuationLeaseClassConstraintsRequireExactActions(t *testing.T) {
	now := time.Date(2026, time.May, 4, 21, 0, 0, 0, time.UTC)
	lease := NormalizeContinuationLease(ContinuationLease{
		ID:             "lease-capability",
		Status:         ContinuationLeaseStatusActive,
		MaxTurns:       1,
		RemainingTurns: 1,
		LeaseClass:     ContinuationLeaseClassCapabilityGrant,
		AllowedActions: []string{"*"},
		ExpiresAt:      now.Add(time.Hour),
	})

	wildcardOnly := CheckContinuationLeaseAction(lease, "grant_set", now)
	if wildcardOnly.Allowed || wildcardOnly.Reason != "lease_class_requires_explicit_action" {
		t.Fatalf("wildcard decision = %#v, want explicit-action denial", wildcardOnly)
	}
	lease.AllowedActions = append(lease.AllowedActions, "grant_set")
	explicit := CheckContinuationLeaseAction(lease, "grant-set", now)
	if !explicit.Allowed || explicit.Reason != "allowed" {
		t.Fatalf("explicit decision = %#v, want allowed", explicit)
	}
	if lease.Constraints["grant"] == "" || lease.Constraints["actions"] == "" {
		t.Fatalf("constraints = %#v, want capability grant defaults", lease.Constraints)
	}
}

func TestContinuationLeaseClassInferenceAndBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		risk    string
		actions []string
		effect  string
		want    ContinuationLeaseClass
	}{
		{name: "data", risk: "data_access", actions: []string{"read_image"}, effect: "inspect one generated artifact", want: ContinuationLeaseClassDataAccess},
		{name: "child", risk: "durable_child_wake", actions: []string{"selected_child_wake"}, effect: "wake image2 once", want: ContinuationLeaseClassChildWake},
		{name: "capability", risk: "capability_grant", actions: []string{"capability_access_check"}, effect: "review grant", want: ContinuationLeaseClassCapabilityGrant},
		{name: "deploy", risk: "deploy", actions: []string{"service_restart"}, effect: "restart and verify", want: ContinuationLeaseClassDeployRestart},
		{name: "workspace", risk: "workspace_write", actions: []string{"focused_tests"}, effect: "patch locally", want: ContinuationLeaseClassLocalWorkspace},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InferContinuationLeaseClass(tc.risk, tc.actions, tc.effect); got != tc.want {
				t.Fatalf("InferContinuationLeaseClass() = %q, want %q", got, tc.want)
			}
			if boundary := ContinuationLeaseClassBoundary(tc.want); boundary == "" || boundary == ContinuationLeaseClassBoundary("") {
				t.Fatalf("boundary for %q = %q, want class-specific boundary", tc.want, boundary)
			}
		})
	}
}
