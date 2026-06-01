//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

func TestPromptContractBehaviorStaleApprovalDoesNotAuthorizeContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 97101, UserID: 0, Scope: telegramDMScopeRef(97101)}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "decision-stale-prompt-contract",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:        "aprop-stale-prompt-contract",
			Summary:   "Expired continuation approval",
			Status:    session.ProposalStatusApproved,
			ExpiresAt: expiredAt,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-stale-prompt-contract",
			ProposalID:     "aprop-stale-prompt-contract",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ExpiresAt:      expiredAt,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), key.ChatID); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.RemainingTurns != 0 {
		t.Fatalf("continuation = %#v, want idle with no remaining execution authority", got)
	}
	if got.ActionProposal.Status != session.ProposalStatusExpired || got.ContinuationLease.Status != session.ContinuationLeaseStatusExpired {
		t.Fatalf("continuation = %#v, want expired proposal and lease", got)
	}
}

func TestPromptContractBehaviorUngroundedToolAndTestClaimsRequireEvidence(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 97102, UserID: 0, Scope: telegramDMScopeRef(97102)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{EventType: core.ExecutionEventTurnStarted, Stage: "turn", Status: "running", PayloadJSON: `{}`, CreatedAt: now.Add(-20 * time.Second)},
		{EventType: core.ExecutionEventTurnCompleted, Stage: "turn", Status: "completed", PayloadJSON: `{"summary":"done"}`, CreatedAt: now.Add(-10 * time.Second)},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reply := "Done — I ran go test ./... and validation passed."
	rewritten, note := rt.groundFinalReplyWithExecutionEvidence(key, reply)
	if rewritten != reply {
		t.Fatalf("rewritten = %q, want persona repair path to preserve candidate reply", rewritten)
	}
	for _, want := range []string{
		"execution claims are not grounded by TES",
		"test-execution claim has no test-related tool evidence",
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("grounding note = %q, want %q", note, want)
		}
	}
}

func TestPromptContractBehaviorVagueRecurrenceDoesNotBecomeFalseContinuityClaim(t *testing.T) {
	t.Parallel()

	reply := "Here is the plan."
	got := enforceVisibleRecurrenceContract(reply, prompt.RuntimeAwareness{
		HiddenInputsActive:    true,
		HiddenInputCategories: []string{hiddenInputUnresolvedMemory},
		ProvenanceSummary:     "related prior material in memory/questions.md is surfacing again; an open question overlaps with this turn",
	})
	if got != reply {
		t.Fatalf("reply = %q, want unchanged reply when recurrence provenance is too vague/internal", got)
	}
	for _, notWant := range []string{"memory/questions.md", "related prior material", "Continuity note:"} {
		if strings.Contains(got, notWant) {
			t.Fatalf("reply = %q, leaked or invented vague recurrence marker %q", got, notWant)
		}
	}
}

func TestPromptContractBehaviorDeployRestartRequiresStandaloneManualApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 97103, UserID: 0, Scope: telegramDMScopeRef(97103)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "prompt-contract-deploy-op",
		Objective: "Ship validated prompt-contract eval changes to the live service.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "prompt-contract-deploy-plan",
			CurrentPhaseID: "phase-deploy",
			Phases: []session.OperationPhase{
				{ID: "phase-tests", Summary: "Add prompt contract behavioral evals", Status: session.PlanStatusCompleted, AuthorityClass: "workspace_write"},
				{
					ID:             "phase-deploy",
					Summary:        "Deploy the validated runtime",
					Status:         session.PlanStatusPending,
					AuthorityClass: "deploy",
					BoundedEffect:  "Commit the intended repo changes, build, install, restart the user service, and run verify-deploy.",
					AllowedActions: []string{"git_commit_intended_changes", "make_build", "install_user_service", "restart_aphelion_service", "run_verify_deploy"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, Text: "continue and ship it", MessageID: 1}, "continue and ship it", nil)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want standalone deploy approval materialized")
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ApprovalBundle.Active() || cont.RemainingTurns != 1 {
		t.Fatalf("continuation = %#v, want standalone one-turn deploy/restart lease", cont)
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDeployRestart {
		t.Fatalf("lease class = %q, want deploy_restart", cont.ContinuationLease.LeaseClass)
	}
	if cont.ActionProposal.AutoApproveEligible == nil || *cont.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want explicit manual approval requirement", cont.ActionProposal.AutoApproveEligible)
	}
	for _, want := range []string{"git_commit_intended_changes", "make_build", "install_user_service", "restart_aphelion_service", "run_verify_deploy"} {
		if !actionListContains(cont.ActionProposal.AllowedActions, want) {
			t.Fatalf("allowed actions = %#v, want %q", cont.ActionProposal.AllowedActions, want)
		}
	}
	for _, want := range []string{"commit_unrelated_changes", "skip_build_or_tests_before_restart", "skip_post_deploy_verification"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, want) {
			t.Fatalf("forbidden actions = %#v, want %q", cont.ActionProposal.ForbiddenActions, want)
		}
	}
}
