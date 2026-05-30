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

func TestInvalidContinuationAuthorityDoesNotCreateRequiredCapabilityGrant(t *testing.T) {
	t.Parallel()
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9138, UserID: 0, Scope: telegramDMScopeRef(9138)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-invalid-authority",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github-invalid-authority",
		Purpose:        "Should not be granted when authority contract is invalid.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-invalid-authority-required-grant",
		Objective:      "Invalid authority must not grant capability.",
		StageSummary:   "Contradictory authority",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-invalid-authority-required-grant",
			OperationID:      "invalid-authority-required-grant",
			Summary:          "Contradictory required grant approval",
			RiskClass:        "workspace_write",
			AllowedActions:   []string{"edit_files"},
			ForbiddenActions: []string{"edit_files"},
			Status:           session.ProposalStatusPending,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-invalid-authority-required-grant",
			ProposalID:     "aprop-invalid-authority-required-grant",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
				RequestID:      "cap-invalid-authority",
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: "github-invalid-authority",
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{"write"},
			}},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err == nil {
		t.Fatal("ApproveContinuationForKey() err = nil, want invalid authority error")
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-invalid-authority", "telegram:1001", "write"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant() ok=%t err=%v, want no grant", ok, err)
	}
	req, ok, err := store.CapabilityRequest("cap-invalid-authority")
	if err != nil || !ok || req.ReviewStatus != session.CapabilityReviewStatusProposed {
		t.Fatalf("request ok=%t status=%q err=%v, want still proposed", ok, req.ReviewStatus, err)
	}
}

func TestRequiredCapabilityGrantPrevalidationPreventsPartialGrant(t *testing.T) {
	t.Parallel()
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9139, UserID: 0, Scope: telegramDMScopeRef(9139)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-partial-first",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github-partial-first",
		Purpose:        "First spec must not be granted if later spec is invalid.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-partial-required-grants",
		Objective:      "Multi-spec required grant prevalidation.",
		StageSummary:   "Grant two capabilities atomically",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-partial-required-grants",
			OperationID:      "partial-required-grants",
			Summary:          "Valid authority with one missing required grant request",
			RiskClass:        "workspace_write",
			AllowedActions:   []string{"edit_files"},
			ForbiddenActions: []string{"deploy"},
			Status:           session.ProposalStatusPending,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-partial-required-grants",
			ProposalID:     "aprop-partial-required-grants",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
				RequestID:      "cap-partial-first",
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: "github-partial-first",
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{"write"},
			}, {
				RequestID:      "cap-partial-missing",
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: "github-partial-missing",
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{"write"},
			}},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err == nil {
		t.Fatal("ApproveContinuationForKey() err = nil, want missing request error")
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-partial-first", "telegram:1001", "write"); err != nil || ok {
		t.Fatalf("first ActiveCapabilityGrant() ok=%t err=%v, want no partial grant", ok, err)
	}
	req, ok, err := store.CapabilityRequest("cap-partial-first")
	if err != nil || !ok || req.ReviewStatus != session.CapabilityReviewStatusProposed {
		t.Fatalf("first request ok=%t status=%q err=%v, want still proposed", ok, req.ReviewStatus, err)
	}
}
