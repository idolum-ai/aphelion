//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestInvalidAuthorityContractDoesNotRenderApprovalButtons(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "invalid-authority-contract",
		Objective:      "Commit contradictory local work.",
		StageSummary:   "Commit validated local slices.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-invalid-authority-contract",
			Summary:          "Commit validated local slices",
			RiskClass:        "workspace_commit_then_repo_write_bounded",
			AllowedActions:   []string{"git_commit_validated_slices", "edit_repo_code"},
			ForbiddenActions: []string{"commit"},
			Status:           session.ProposalStatusPending,
			ExpiresAt:        now.Add(time.Hour),
		},
	}
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, 1, now)
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.sendContinuationApprovalPrompt(context.Background(), key, core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "continue", MessageID: 1}, state, "approve?"); err != nil {
		t.Fatalf("sendContinuationApprovalPrompt() err = %v", err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no approval buttons", inlineCount)
	}
	if len(sent) != 0 {
		t.Fatalf("sent = %#v, want no user-visible internal contradiction diagnostic", sent)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusRevoked || got.ActionProposal.Status != session.ProposalStatusSuperseded {
		t.Fatalf("state = %#v, want revoked/superseded invalid authority", got)
	}
}

func TestMaterializedInvalidAuthorityContractReconcilesToFreshApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9045, UserID: 0, Scope: telegramDMScopeRef(9045)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "deploy-reconciliation-op",
		Objective: "Deploy the validated runtime without leaking compiler diagnostics.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "deploy-reconciliation-plan",
			CurrentPhaseID: "phase-deploy",
			Phases: []session.OperationPhase{{
				ID:             "phase-deploy",
				Summary:        "Deploy the validated runtime",
				Status:         session.PlanStatusPending,
				AuthorityClass: "deploy",
				BoundedEffect:  "Build, install, restart, and verify the service.",
				AllowedActions: []string{"install_user_service", "restart_aphelion_service", "run_verify_deploy"},
				ForbiddenActions: []string{
					"deploy or restart",
					"credentials_or_tokens",
				},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: 9045, SenderID: 1001, Text: "continue", MessageID: 1}, "continue", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want reconciled approval prompt")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sender.mu.Unlock()
	if len(sent) != 0 {
		t.Fatalf("sent = %#v, want no raw invalid authority diagnostic", sent)
	}
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one reconciled approval", inlineCount)
	}
	if strings.Contains(inlineText, "internally contradictory") || strings.Contains(inlineText, "allowed_action_implies_forbidden_authority") {
		t.Fatalf("inline text = %q, want no compiler diagnostic", inlineText)
	}
	if !strings.Contains(inlineText, "Deploy the validated runtime") {
		t.Fatalf("inline text = %q, want reconciled approval summary", inlineText)
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.Status != session.ProposalStatusPending {
		t.Fatalf("continuation = %#v, want pending reconciled approval", cont)
	}
	if compilation := continuationAuthorityCompilation(cont); compilation.Invalid() {
		t.Fatalf("compilation = %#v, want valid reconciled authority", compilation)
	}
	if actionListContains(cont.ActionProposal.ForbiddenActions, "deploy or restart") {
		t.Fatalf("forbidden actions = %#v, want broad self-cancelling deploy/restart stop removed", cont.ActionProposal.ForbiddenActions)
	}
	for _, want := range []string{"credentials_or_tokens", "deploy_without_handoff", "restart_without_recovery_artifact"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, want) {
			t.Fatalf("forbidden actions = %#v, want %q", cont.ActionProposal.ForbiddenActions, want)
		}
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Stage != "deploy_approval" || opState.PhasePlan.Phases[0].LeaseID != cont.ContinuationLease.ID {
		t.Fatalf("operation state = %#v, want deploy approval linked to reconciled lease %q", opState, cont.ContinuationLease.ID)
	}
}
