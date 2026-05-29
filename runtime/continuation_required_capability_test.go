//go:build linux

package runtime

import (
	"context"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestBundledPlanApprovalCreatesRequiredCapabilityGrant(t *testing.T) {
	t.Parallel()
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9137, UserID: 0, Scope: telegramDMScopeRef(9137)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-pr-105",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		Purpose:        "Update PR #105 metadata only.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op", Objective: "Update PR metadata", Status: session.OperationStatusBlocked, PhasePlan: session.OperationPhasePlan{ID: "plan", Goal: "Approve plan and capability together", Phases: []session.OperationPhase{{ID: "phase-pr", Summary: "Update PR metadata", Status: session.PlanStatusPending, AuthorityClass: "external_account", BoundedEffect: "GitHub PR #105 metadata only.", AllowedActions: []string{"github_pr_update"}, RequiredCapabilityGrants: []session.CapabilityGrantSpec{{RequestID: "cap-pr-105", Kind: session.CapabilityKindExternalAccount, TargetResource: "github", GrantedTo: "telegram:1001", AllowedActions: []string{"read", "write"}}}}}}}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9137, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil || !materialized {
		t.Fatalf("materialize = %t err=%v", materialized, err)
	}
	if _, _, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github", "telegram:1001", "write"); err != nil {
		t.Fatalf("ActiveCapabilityGrant(before approval) err = %v", err)
	} else if _, ok, _ := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github", "telegram:1001", "write"); ok {
		t.Fatal("grant active before plan approval")
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	grant, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github", "telegram:1001", "write")
	if err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant(after approval) ok=%t err=%v", ok, err)
	}
	if grant.RequestID != "cap-pr-105" {
		t.Fatalf("grant.RequestID=%q, want cap-pr-105", grant.RequestID)
	}
	req, ok, err := store.CapabilityRequest("cap-pr-105")
	if err != nil || !ok || req.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("request ok=%t status=%q err=%v, want approved", ok, req.ReviewStatus, err)
	}
}
