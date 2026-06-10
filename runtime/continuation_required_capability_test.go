//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

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
	beforeApproval, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(before approval) err = %v", err)
	}
	if beforeApproval.ContinuationLease.ExpiresAt.IsZero() {
		t.Fatal("continuation lease expiry is zero before approval, want bounded grant expiry source")
	}
	approved, err := rt.ApproveContinuationForKey(key, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	grant, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github", "telegram:1001", "write")
	if err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant(after approval) ok=%t err=%v", ok, err)
	}
	if grant.RequestID != "cap-pr-105" {
		t.Fatalf("grant.RequestID=%q, want cap-pr-105", grant.RequestID)
	}
	if !grant.ExpiresAt.Equal(beforeApproval.ContinuationLease.ExpiresAt) {
		t.Fatalf("grant expiry = %s, want lease expiry %s", grant.ExpiresAt, beforeApproval.ContinuationLease.ExpiresAt)
	}
	if !stringSliceContains(approved.ContinuationLease.CapabilityGrantIDs, grant.GrantID) {
		t.Fatalf("minted grant ids = %#v, want %q", approved.ContinuationLease.CapabilityGrantIDs, grant.GrantID)
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

func TestRequiredCapabilityPhaseRescopesExternalAccountMetadataApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9141, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9141, UserID: 0, Scope: telegramDMScopeRef(9141)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-pr-158",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		Purpose:        "Update PR #158 title and body.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "planning-improvements-pr-metadata",
		Objective: "Update PR #158 title and description.",
		Status:    session.OperationStatusBlocked,
		Stage:     "approval_request",
		PhasePlan: session.OperationPhasePlan{
			ID:             "planning-improvements-pr-metadata",
			CurrentPhaseID: "update-pr-158-title-body",
			Phases: []session.OperationPhase{{
				ID:             "update-pr-158-title-body",
				Summary:        "Update PR #158 title and description",
				Status:         session.PlanStatusPending,
				AuthorityClass: "commit",
				WhyNow:         "PR #158 metadata should accurately represent the branch.",
				BoundedEffect:  "Update only PR #158 title and body. No code/repo changes, merge, release/tag, deploy/restart, branch mutation, or unrelated GitHub effects.",
				AllowedActions: []string{
					"read_pr_metadata_if_needed",
					"update_pull_request_title",
					"update_pull_request_body",
					"report_updated_pr_url",
				},
				ForbiddenActions: []string{
					"git_commit",
					"git_push",
					"merge_pull_request",
					"release_or_tag",
					"deploy_or_restart",
					"credential_token_output",
					"unrelated_github_effects",
				},
				RequiresApproval: true,
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					RequestID:      "cap-pr-158",
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"read", "write"},
				}},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9141, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want rescope to valid GitHub metadata approval")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending {
		t.Fatalf("continuation status = %q, want pending", cont.Status)
	}
	if cont.ActionProposal.RiskClass != "external_account_action" {
		t.Fatalf("risk class = %q, want external_account_action", cont.ActionProposal.RiskClass)
	}
	if cont.ActionProposal.AutoApproveEligible == nil || *cont.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want manual button-backed request", cont.ActionProposal.AutoApproveEligible)
	}
	for _, notWant := range []string{"commit", "git_commit", "repo_history_mutation"} {
		if actionListContains(cont.ActionProposal.AllowedActions, notWant) || actionListContains(cont.ContinuationLease.AllowedActions, notWant) {
			t.Fatalf("allowed action %q present in action=%#v lease=%#v", notWant, cont.ActionProposal.AllowedActions, cont.ContinuationLease.AllowedActions)
		}
	}
	for _, want := range []string{"update_pull_request_title", "update_pull_request_body"} {
		if !actionListContains(cont.ActionProposal.AllowedActions, want) {
			t.Fatalf("allowed actions = %#v, want %q", cont.ActionProposal.AllowedActions, want)
		}
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassCapabilityGrant {
		t.Fatalf("lease class = %q, want capability_grant", cont.ContinuationLease.LeaseClass)
	}
	if len(cont.ContinuationLease.RequiredCapabilityGrants) != 1 || cont.ContinuationLease.RequiredCapabilityGrants[0].RequestID != "cap-pr-158" {
		t.Fatalf("required grants = %#v, want cap-pr-158 preserved", cont.ContinuationLease.RequiredCapabilityGrants)
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github", "telegram:1001", "write"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant() ok=%t err=%v, want no grant before button approval", ok, err)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9141, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want one unused lease", leases)
	}
	if compilation := continuationAuthorityCompilation(cont); compilation.Invalid() {
		t.Fatalf("authority compilation = %#v, want valid rescope", compilation)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval prompt", inlineCount)
	}
}

func TestAuthoritySanitizerReoffersRevokedRequiredCapabilityPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9142, UserID: 1001, Scope: telegramDMScopeRef(9142)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-pr-158-sanitize",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		Purpose:        "Update PR #158 title and body.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	opState := requiredCapabilityPRMetadataOperationState("planning-improvements-pr-metadata-sanitize", "cap-pr-158-sanitize")
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	now := time.Now().UTC()
	bad := invalidPRMetadataContinuationForTest(opState, now)
	if err := store.UpdateContinuationState(key, bad); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	repaired, err := rt.repairContinuationAuthorityContradictions(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("repairContinuationAuthorityContradictions() err = %v", err)
	}
	if repaired < 2 {
		t.Fatalf("repaired = %d, want sanitizer plus reoffer", repaired)
	}
	expectedGrantRequestID := "cap-pr-158-sanitize"
	assertPRMetadataContinuationReoffered(t, store, sender, key, expectedGrantRequestID)
}

func TestStartupRepairReoffersAlreadyRevokedInvalidRequiredCapabilityPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9143, UserID: 1001, Scope: telegramDMScopeRef(9143)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-pr-158-reoffer",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		Purpose:        "Update PR #158 title and body.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	opState := requiredCapabilityPRMetadataOperationState("planning-improvements-pr-metadata-reoffer", "cap-pr-158-reoffer")
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	now := time.Now().UTC()
	bad := invalidPRMetadataContinuationForTest(opState, now)
	compilation := continuationAuthorityCompilation(bad)
	if compilation.Valid() {
		t.Fatal("bad continuation compilation valid, want invalid authority fixture")
	}
	revoked := continuationStateWithInvalidAuthorityContract(bad, compilation, now)
	if err := store.UpdateContinuationState(key, revoked); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	repaired, err := rt.repairInvalidPendingContinuationApprovals(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("repairInvalidPendingContinuationApprovals() err = %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired = %d, want one reoffered approval", repaired)
	}
	assertPRMetadataContinuationReoffered(t, store, sender, key, "cap-pr-158-reoffer")
}

func TestInvalidAuthorityReconciliationPreservesExternalAccountGrant(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	_ = store
	_ = sender
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "invalid-pr-metadata",
		Objective:      "Update PR metadata.",
		StageSummary:   "Update PR #158 title and description",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:          "aprop-invalid-pr-metadata",
			OperationID: "invalid-pr-metadata",
			Summary:     "Update PR #158 title and description",
			RiskClass:   "commit",
			AllowedActions: []string{
				"read_pr_metadata_if_needed",
				"update_pull_request_title",
				"update_pull_request_body",
				"commit",
				"git_commit",
			},
			ForbiddenActions: []string{"git_commit", "git_push", "deploy_or_restart"},
			Status:           session.ProposalStatusPending,
			ExpiresAt:        now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-invalid-pr-metadata",
			ProposalID:       "aprop-invalid-pr-metadata",
			Status:           session.ContinuationLeaseStatusPending,
			MaxTurns:         1,
			RemainingTurns:   1,
			AllowedActions:   []string{"read_pr_metadata_if_needed", "update_pull_request_title", "update_pull_request_body", "commit", "git_commit"},
			ForbiddenActions: []string{"git_commit", "git_push", "deploy_or_restart"},
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
				RequestID:      "cap-pr-158",
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: "github",
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{"read", "write"},
			}},
			ExpiresAt: now.Add(time.Hour),
		},
	}
	compilation := continuationAuthorityCompilation(state)
	if compilation.Valid() {
		t.Fatal("compilation valid, want invalid fixture")
	}
	reconciled, ok := rt.reconciledContinuationStateFromInvalidAuthority(state, compilation, now)
	if !ok {
		t.Fatal("reconciled = false, want narrower external-account proposal")
	}
	if reconciled.ActionProposal.RiskClass != "external_account_action" {
		t.Fatalf("risk class = %q, want external_account_action", reconciled.ActionProposal.RiskClass)
	}
	if len(reconciled.ContinuationLease.RequiredCapabilityGrants) != 1 || reconciled.ContinuationLease.RequiredCapabilityGrants[0].RequestID != "cap-pr-158" {
		t.Fatalf("required grants = %#v, want cap-pr-158 preserved", reconciled.ContinuationLease.RequiredCapabilityGrants)
	}
	if compilation := continuationAuthorityCompilation(reconciled); compilation.Invalid() {
		t.Fatalf("reconciled compilation = %#v, want valid", compilation)
	}
}

func requiredCapabilityPRMetadataOperationState(operationID string, capabilityRequestID string) session.OperationState {
	return session.OperationState{
		ID:        operationID,
		Objective: "Update PR #158 title and description.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval_adjudicated",
		PhasePlan: session.OperationPhasePlan{
			ID:             operationID,
			CurrentPhaseID: "update-pr-158-title-body",
			Phases: []session.OperationPhase{{
				ID:             "update-pr-158-title-body",
				Summary:        "Update PR #158 title and description",
				Status:         session.PlanStatusPending,
				AuthorityClass: "commit",
				WhyNow:         "PR #158 metadata should accurately represent the branch.",
				BoundedEffect:  "Update only PR #158 title and body. No code/repo changes, merge, release/tag, deploy/restart, branch mutation, or unrelated GitHub effects.",
				AllowedActions: []string{
					"read_pr_metadata_if_needed",
					"update_pull_request_title",
					"update_pull_request_body",
					"report_updated_pr_url",
				},
				ForbiddenActions: []string{
					"git_commit",
					"git_push",
					"merge_pull_request",
					"release_or_tag",
					"deploy_or_restart",
					"credential_token_output",
					"unrelated_github_effects",
				},
				RequiresApproval: true,
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					RequestID:      capabilityRequestID,
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"read", "write"},
				}},
			}},
		},
	}
}

func invalidPRMetadataContinuationForTest(opState session.OperationState, now time.Time) session.ContinuationState {
	phase := opState.PhasePlan.Phases[0]
	state := continuationStateFromOperationPhase(opState, phase, "continue", now)
	state.ActionProposal.RiskClass = "commit"
	state.ActionProposal.AllowedActions = append(state.ActionProposal.AllowedActions, "commit", "git_commit")
	state.ActionProposal.ForbiddenActions = append(state.ActionProposal.ForbiddenActions, "git_commit", "git_push", "deploy_or_restart")
	state.ActionProposal.Status = session.ProposalStatusPending
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	state.ContinuationLease.RequiredCapabilityGrants = append([]session.CapabilityGrantSpec(nil), phase.RequiredCapabilityGrants...)
	state.ContinuationLease.AllowedActions = append(state.ContinuationLease.AllowedActions, "commit", "git_commit")
	state.ContinuationLease.ForbiddenActions = append(state.ContinuationLease.ForbiddenActions, "git_commit", "git_push", "deploy_or_restart")
	state = session.NormalizeContinuationState(state)
	if compilation := continuationAuthorityCompilation(state); compilation.Valid() {
		panic("invalid PR metadata continuation fixture compiled valid")
	}
	return state
}

func assertPRMetadataContinuationReoffered(t *testing.T, store *session.SQLiteStore, sender *fakeSender, key session.SessionKey, capabilityRequestID string) {
	t.Helper()
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending {
		t.Fatalf("continuation status = %q, want pending reoffered approval", cont.Status)
	}
	if cont.ActionProposal.RiskClass != "external_account_action" {
		t.Fatalf("risk class = %q, want external_account_action", cont.ActionProposal.RiskClass)
	}
	for _, notWant := range []string{"commit", "git_commit", "repo_history_mutation"} {
		if actionListContains(cont.ActionProposal.AllowedActions, notWant) || actionListContains(cont.ContinuationLease.AllowedActions, notWant) {
			t.Fatalf("allowed action %q present in action=%#v lease=%#v", notWant, cont.ActionProposal.AllowedActions, cont.ContinuationLease.AllowedActions)
		}
	}
	for _, want := range []string{"update_pull_request_title", "update_pull_request_body"} {
		if !actionListContains(cont.ActionProposal.AllowedActions, want) {
			t.Fatalf("allowed actions = %#v, want %q", cont.ActionProposal.AllowedActions, want)
		}
	}
	if len(cont.ContinuationLease.RequiredCapabilityGrants) != 1 || cont.ContinuationLease.RequiredCapabilityGrants[0].RequestID != capabilityRequestID {
		t.Fatalf("required grants = %#v, want %s preserved", cont.ContinuationLease.RequiredCapabilityGrants, capabilityRequestID)
	}
	if compilation := continuationAuthorityCompilation(cont); compilation.Invalid() {
		t.Fatalf("authority compilation = %#v, want valid reoffered approval", compilation)
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	phase := opState.PhasePlan.Phases[0]
	if phase.LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("phase lease id = %q, want %q", phase.LeaseID, cont.ContinuationLease.ID)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one reoffered approval prompt", inlineCount)
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

func TestRequiredCapabilityExistingGrantMustCoverAllActions(t *testing.T) {
	t.Parallel()
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9140, UserID: 0, Scope: telegramDMScopeRef(9140)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-partial-existing",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github-partial-existing",
		Purpose:        "Existing read-only grant must not satisfy read+write.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-existing-read-only",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github-partial-existing",
		AllowedActions: []string{"read"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(read-only) err = %v", err)
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-partial-existing-required-grant",
		Objective:      "Existing grant must cover every required action.",
		StageSummary:   "Grant read and write if only read exists",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-partial-existing-required-grant",
			OperationID:      "partial-existing-required-grant",
			Summary:          "Valid authority with partial existing grant",
			RiskClass:        "workspace_write",
			AllowedActions:   []string{"edit_files"},
			ForbiddenActions: []string{"deploy"},
			Status:           session.ProposalStatusPending,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-partial-existing-required-grant",
			ProposalID:     "aprop-partial-existing-required-grant",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
				RequestID:      "cap-partial-existing",
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: "github-partial-existing",
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{"read", "write"},
			}},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	readGrant, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-partial-existing", "telegram:1001", "read")
	if err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant(read) ok=%t err=%v, want existing/new grant", ok, err)
	}
	writeGrant, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-partial-existing", "telegram:1001", "write")
	if err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant(write) ok=%t err=%v, want new grant", ok, err)
	}
	if writeGrant.GrantID == "capg-existing-read-only" {
		t.Fatalf("write grant id = existing read-only grant %q, want bundled required grant", writeGrant.GrantID)
	}
	if readGrant.GrantID == "capg-existing-read-only" && writeGrant.RequestID != "cap-partial-existing" {
		t.Fatalf("write grant = %#v, want request-linked grant covering missing action", writeGrant)
	}
	req, ok, err := store.CapabilityRequest("cap-partial-existing")
	if err != nil || !ok || req.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("request ok=%t status=%q err=%v, want approved", ok, req.ReviewStatus, err)
	}
}

func TestRequiredCapabilityGrantPreservesExplicitSpecExpiry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9144, UserID: 0, Scope: telegramDMScopeRef(9144)}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-explicit-expiry",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github-explicit-expiry",
		Purpose:        "Update a bounded external account resource.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	now := time.Now().UTC()
	specExpiry := now.Add(7 * time.Minute).UTC()
	leaseExpiry := now.Add(30 * time.Minute).UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-explicit-expiry",
		Objective:      "Preserve explicit grant expiry.",
		StageSummary:   "Grant explicitly bounded capability",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-explicit-expiry",
			OperationID:    "explicit-expiry",
			Summary:        "Valid authority with explicit grant expiry",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"edit_files"},
			Status:         session.ProposalStatusPending,
			ExpiresAt:      leaseExpiry,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-explicit-expiry",
			ProposalID:     "aprop-explicit-expiry",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      leaseExpiry,
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
				RequestID:      "cap-explicit-expiry",
				Kind:           session.CapabilityKindExternalAccount,
				TargetResource: "github-explicit-expiry",
				GrantedTo:      "telegram:1001",
				AllowedActions: []string{"write"},
				ExpiresAt:      specExpiry,
			}},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	grant, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-explicit-expiry", "telegram:1001", "write")
	if err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant() ok=%t err=%v, want explicit-expiry grant", ok, err)
	}
	if !grant.ExpiresAt.Equal(specExpiry) {
		t.Fatalf("grant expiry = %s, want spec expiry %s", grant.ExpiresAt, specExpiry)
	}
}

func TestRevokeContinuationRevokesOnlyMintedRequiredCapabilityGrants(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9145, UserID: 0, Scope: telegramDMScopeRef(9145)}
	for _, request := range []session.CapabilityRequest{
		{
			RequestID:      "cap-revoke-minted",
			RequestedBy:    "telegram:1001",
			RequestedFor:   "telegram:1001",
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "github-revoke-minted",
			Purpose:        "Minted grant should be revoked with continuation.",
			ReviewStatus:   session.CapabilityReviewStatusProposed,
		},
		{
			RequestID:      "cap-revoke-existing",
			RequestedBy:    "telegram:1001",
			RequestedFor:   "telegram:1001",
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "github-revoke-existing",
			Purpose:        "Existing grant should survive continuation revocation.",
			ReviewStatus:   session.CapabilityReviewStatusProposed,
		},
	} {
		if _, err := store.UpsertCapabilityRequest(request); err != nil {
			t.Fatalf("UpsertCapabilityRequest(%s) err = %v", request.RequestID, err)
		}
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-revoke-existing",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github-revoke-existing",
		AllowedActions: []string{"read", "write"},
		Status:         session.CapabilityGrantStatusActive,
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(existing) err = %v", err)
	}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "decision-revoke-minted",
		Objective:      "Revoke only grants minted by this continuation.",
		StageSummary:   "Grant one new capability and reuse one existing capability",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-revoke-minted",
			OperationID:    "revoke-minted",
			Summary:        "Valid authority with mixed grant provenance",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"edit_files"},
			Status:         session.ProposalStatusPending,
			ExpiresAt:      now.Add(30 * time.Minute),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-revoke-minted",
			ProposalID:     "aprop-revoke-minted",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(30 * time.Minute),
			RequiredCapabilityGrants: []session.CapabilityGrantSpec{
				{
					RequestID:      "cap-revoke-minted",
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github-revoke-minted",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"write"},
				},
				{
					RequestID:      "cap-revoke-existing",
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github-revoke-existing",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"read", "write"},
				},
			},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	approved, err := rt.ApproveContinuationForKey(key, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	if len(approved.ContinuationLease.CapabilityGrantIDs) != 1 {
		t.Fatalf("minted grant ids = %#v, want only newly minted grant", approved.ContinuationLease.CapabilityGrantIDs)
	}
	mintedID := approved.ContinuationLease.CapabilityGrantIDs[0]
	minted, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-revoke-minted", "telegram:1001", "write")
	if err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant(minted) ok=%t err=%v, want active minted grant", ok, err)
	}
	if minted.GrantID != mintedID {
		t.Fatalf("minted grant id = %q, want recorded id %q", minted.GrantID, mintedID)
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-revoke-existing", "telegram:1001", "write"); err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant(existing before revoke) ok=%t err=%v, want active existing grant", ok, err)
	}
	if _, err := rt.RevokeContinuationForKey(key); err != nil {
		t.Fatalf("RevokeContinuationForKey() err = %v", err)
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-revoke-minted", "telegram:1001", "write"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant(minted after revoke) ok=%t err=%v, want revoked minted grant", ok, err)
	}
	storedMinted, ok, err := store.CapabilityGrant(mintedID)
	if err != nil || !ok {
		t.Fatalf("CapabilityGrant(%s) ok=%t err=%v", mintedID, ok, err)
	}
	if storedMinted.Status != session.CapabilityGrantStatusRevoked || storedMinted.RevokedAt.IsZero() {
		t.Fatalf("minted grant = %#v, want revoked with timestamp", storedMinted)
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github-revoke-existing", "telegram:1001", "write"); err != nil || !ok {
		t.Fatalf("ActiveCapabilityGrant(existing after revoke) ok=%t err=%v, want existing grant still active", ok, err)
	}
}
