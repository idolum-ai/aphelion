//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestContinuationClassScopeAllowsSameActiveLeaseClassAndActions(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 2,
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-class-scope",
			Status:           session.ContinuationLeaseStatusActive,
			LeaseClass:       session.ContinuationLeaseClassLocalWorkspace,
			RemainingTurns:   2,
			AllowedActions:   []string{"workspace_write", "run_tests"},
			ForbiddenActions: []string{"deploy"},
			ExpiresAt:        now.Add(time.Hour),
		},
	}
	proposed := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write", "run_tests"},
			BoundedEffect:  "Patch local repo code and run focused tests.",
		},
		ContinuationLease: session.ContinuationLease{
			LeaseClass: session.ContinuationLeaseClassLocalWorkspace,
		},
	}

	decision := continuationClassScopeDecisionForMaterializedState(prior, proposed, now)
	if !decision.Allowed {
		t.Fatalf("decision = %#v, want allowed within active lease class and action scope", decision)
	}
}

func TestContinuationClassScopeRejectsLocalWorkspaceWildcardOnlyLease(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	prior := session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		RemainingTurns: 2,
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-class-scope-wildcard",
			Status:           session.ContinuationLeaseStatusActive,
			LeaseClass:       session.ContinuationLeaseClassLocalWorkspace,
			RemainingTurns:   2,
			AllowedActions:   []string{"*"},
			ForbiddenActions: []string{"deploy"},
			ExpiresAt:        now.Add(time.Hour),
		},
	}
	proposed := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write"},
			BoundedEffect:  "Patch local repo code and run focused tests.",
		},
		ContinuationLease: session.ContinuationLease{
			LeaseClass: session.ContinuationLeaseClassLocalWorkspace,
		},
	}

	decision := continuationClassScopeDecisionForMaterializedState(prior, proposed, now)
	if decision.Allowed || decision.FailedDimension != "allowed_actions" || !strings.Contains(decision.Reason, "workspace_write") {
		t.Fatalf("decision = %#v, want local workspace wildcard-only lease rejected", decision)
	}

	prior.ContinuationLease.AllowedActions = append(prior.ContinuationLease.AllowedActions, "workspace_write")
	decision = continuationClassScopeDecisionForMaterializedState(prior, proposed, now)
	if !decision.Allowed {
		t.Fatalf("decision = %#v, want explicit local workspace action allowed", decision)
	}
}

func TestContinuationClassScopeRejectsNewCapabilityGrantRequirement(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	prior := session.ContinuationState{
		Status: session.ContinuationStatusApproved,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-class-scope",
			Status:         session.ContinuationLeaseStatusActive,
			LeaseClass:     session.ContinuationLeaseClassLocalWorkspace,
			RemainingTurns: 2,
			AllowedActions: []string{"workspace_write"},
			ExpiresAt:      now.Add(time.Hour),
		},
	}
	proposed := session.ContinuationState{
		ActionProposal: session.ActionProposal{
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write"},
		},
		ContinuationLease: session.ContinuationLease{
			LeaseClass: session.ContinuationLeaseClassLocalWorkspace,
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{
				{Kind: "filesystem", TargetResource: "/tmp/example", GrantedTo: "telegram:1001", AllowedActions: []string{"read"}},
			},
		},
	}

	decision := continuationClassScopeDecisionForMaterializedState(prior, proposed, now)
	if decision.Allowed || decision.FailedDimension != "capability_grants" {
		t.Fatalf("decision = %#v, want capability grant boundary", decision)
	}
}

func TestContinuationClassScopeRejectsLocalWorkspaceExternalEffect(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	prior := session.ContinuationState{
		Status: session.ContinuationStatusApproved,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-class-scope",
			Status:         session.ContinuationLeaseStatusActive,
			LeaseClass:     session.ContinuationLeaseClassLocalWorkspace,
			RemainingTurns: 2,
			AllowedActions: []string{"workspace_write", "publish"},
			ExpiresAt:      now.Add(time.Hour),
		},
	}
	proposed := session.ContinuationState{
		ActionProposal: session.ActionProposal{
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write", "publish"},
			BoundedEffect:  "Publish the generated report.",
		},
		ContinuationLease: session.ContinuationLease{
			LeaseClass: session.ContinuationLeaseClassLocalWorkspace,
		},
	}

	decision := continuationClassScopeDecisionForMaterializedState(prior, proposed, now)
	if decision.Allowed || decision.FailedDimension != "external_effect" {
		t.Fatalf("decision = %#v, want external-effect boundary", decision)
	}
}
