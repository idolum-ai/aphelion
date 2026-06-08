//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func TestGoalContinuationInfersNextPhaseAfterConsumedPhaseOneLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9041, UserID: 0, Scope: telegramDMScopeRef(9041)}
	now := time.Now().UTC()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "lighthouse-proton-inbox",
		Objective: "Enable Lighthouse to help plan a Proton Bridge inbox integration.",
		Status:    session.OperationStatusCompleted,
		Stage:     "phase_one_probe_complete",
		Summary:   "Phase one completed a read-only contract and suggested one simple smoke test.",
		Proposal: session.OperationProposal{
			ID:            "lighthouse-proton-inbox-readonly-plan",
			Kind:          "read_only_review",
			Summary:       "Run the first read-only Lighthouse Proton Bridge smoke test",
			WhyNow:        "The live thread needed a minimal first probe.",
			BoundedEffect: "Inspect only and report one simple test.",
			Status:        session.ProposalStatusApproved,
			UpdatedAt:     now,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:         session.TurnAuthorizationKindContinuation,
		Status:       session.ContinuationStatusIdle,
		Objective:    "Enable Lighthouse to help plan a Proton Bridge inbox integration.",
		StageSummary: "Run the first read-only Lighthouse Proton Bridge smoke test",
		ActionProposal: session.ActionProposal{
			ID:            "aprop-lighthouse-proton-inbox-readonly-plan",
			OperationID:   "lighthouse-proton-inbox-readonly-plan",
			Summary:       "Run the first read-only Lighthouse Proton Bridge smoke test",
			BoundedEffect: "Inspect only and report one simple test.",
			Status:        session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-lighthouse-proton-inbox-readonly-plan",
			ProposalID:     "aprop-lighthouse-proton-inbox-readonly-plan",
			Status:         session.ContinuationLeaseStatusConsumed,
			MaxTurns:       1,
			RemainingTurns: 0,
			ConsumedAt:     now,
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	msg := core.InboundMessage{
		ChatID:       9041,
		SenderID:     1001,
		Text:         approvedContinuationEventText,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
		MessageID:    42,
	}
	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, msg, "continue the approved lease", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if !inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = false, want next-phase proposal")
	}
	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, msg, msg.Text, &turn.Result{})
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want inferred next-phase proposal buttons")
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Status != session.OperationStatusBlocked || opState.Stage != "next_phase_proposal" || opState.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("operation state = %#v, want blocked next_phase_proposal with pending proposal", opState)
	}
	if !strings.HasPrefix(opState.Proposal.ID, goalContinuationIDPrefix) || !strings.Contains(opState.Proposal.Summary, "Plan the next bounded phase") {
		t.Fatalf("proposal = %#v, want goal-continuation next-phase summary", opState.Proposal)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != opState.Proposal.ID {
		t.Fatalf("continuation = %#v, want pending continuation linked to goal proposal", cont)
	}
	for _, want := range []string{"inspect_readonly_state", "draft_next_phase_plan", "propose_one_safe_live_test"} {
		if !actionListContains(cont.ActionProposal.AllowedActions, want) {
			t.Fatalf("allowed actions = %#v, want %q", cont.ActionProposal.AllowedActions, want)
		}
	}
	for _, want := range []string{"edit_files", "read_secrets_or_credentials", "external_account_action", "deploy", "restart_service"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, want) {
			t.Fatalf("forbidden actions = %#v, want %q", cont.ActionProposal.ForbiddenActions, want)
		}
	}
	if !strings.Contains(cont.ActionProposal.BoundedEffect, "read-only next-phase planning") {
		t.Fatalf("bounded effect = %q, want explicit read-only boundary", cont.ActionProposal.BoundedEffect)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 || !strings.Contains(inlineText, "Approve:\nPlan the next bounded phase") {
		t.Fatalf("inline count/text = %d/%q, want next-phase approval prompt", inlineCount, inlineText)
	}
}

func TestGoalContinuationInfersPushPRFollowUpAfterCompletedLocalPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9048, UserID: 0, Scope: telegramDMScopeRef(9048)}
	now := time.Now().UTC()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "eval-jobs-semantics-plan",
		Objective: "Triage and fix eval --jobs concurrency semantics, validate, and commit locally only.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		Summary:   "Approved eval --jobs semantics phase is complete. Branch work/eval-jobs-semantics-20260608 has local commit 62e7c0f; validation passed; no push, PR, deploy, or restart was performed.",
		Proposal: session.OperationProposal{
			ID:            "eval-jobs-semantics-local-phase",
			Kind:          "commit",
			Summary:       "Triage and fix eval --jobs semantics, validate, and commit locally",
			WhyNow:        "Fix a timing-sensitive eval runner test found during live review.",
			BoundedEffect: "Edit local files, run tests, and create one local commit; stop before push, PR, deploy, or restart.",
			Status:        session.ProposalStatusApproved,
			UpdatedAt:     now,
		},
		Work: session.WorkOperationMetadata{
			Executor:    "codex",
			Commands:    []string{"go test ./runtime -count=1", "go test ./..."},
			LastSummary: "Runtime and full tests passed; local commit 62e7c0f created.",
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:         session.TurnAuthorizationKindContinuation,
		Status:       session.ContinuationStatusIdle,
		Objective:    "Triage and fix eval --jobs concurrency semantics, validate, and commit locally only.",
		StageSummary: "Local commit phase completed; stop before push or PR.",
		ActionProposal: session.ActionProposal{
			ID:            "aprop-eval-jobs-semantics-local-phase",
			OperationID:   "eval-jobs-semantics-local-phase",
			Summary:       "Triage and fix eval --jobs semantics, validate, and commit locally",
			BoundedEffect: "Edit local files, run tests, and create one local commit; stop before push, PR, deploy, or restart.",
			Status:        session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-eval-jobs-semantics-local-phase",
			ProposalID:     "aprop-eval-jobs-semantics-local-phase",
			Status:         session.ContinuationLeaseStatusConsumed,
			MaxTurns:       1,
			RemainingTurns: 0,
			ConsumedAt:     now,
		},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	msg := core.InboundMessage{
		ChatID:       9048,
		SenderID:     1001,
		Text:         approvedContinuationEventText,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
		MessageID:    43,
	}
	result := &turn.Result{
		VisibleReply: "Recovered cleanly. No more work is needed inside the approved phase.\n\nNext useful step would be a separate bounded approval for push/PR.",
		Turn:         &core.TurnResult{Text: "Recovered cleanly. Next useful step would be a separate bounded approval for push/PR."},
	}
	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, msg, "Recover: budget hop 3/3", result)
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if !inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = false, want push/PR follow-up proposal")
	}
	materialized, err := rt.materializePendingOperationProposalApproval(context.Background(), key, msg, msg.Text, result)
	if err != nil {
		t.Fatalf("materializePendingOperationProposalApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want inferred push/PR approval buttons")
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Status != session.OperationStatusBlocked || opState.Stage != "next_phase_proposal" || opState.Proposal.Status != session.ProposalStatusPending {
		t.Fatalf("operation state = %#v, want blocked pending follow-up proposal", opState)
	}
	if opState.Proposal.Kind != "commit_push_pr" || !strings.Contains(opState.Proposal.Summary, "pull request") {
		t.Fatalf("proposal = %#v, want commit_push_pr pull-request follow-up", opState.Proposal)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ActionProposal.OperationID != opState.Proposal.ID {
		t.Fatalf("continuation = %#v, want pending continuation linked to follow-up proposal", cont)
	}
	if cont.ActionProposal.RiskClass != "commit_push_pr" || continuationWorkMode(cont) != WorkModeCommit {
		t.Fatalf("action proposal = %#v, want commit_push_pr commit-mode follow-up", cont.ActionProposal)
	}
	for _, want := range []string{"publish_existing_branch_for_review", "create_or_update_pull_request", "report_pr_url"} {
		if !actionListContains(cont.ActionProposal.AllowedActions, want) {
			t.Fatalf("allowed actions = %#v, want %q", cont.ActionProposal.AllowedActions, want)
		}
	}
	for _, want := range []string{"additional_file_edits", "merge_pull_request", "deploy_or_restart", "credential_token_output"} {
		if !actionListContains(cont.ActionProposal.ForbiddenActions, want) {
			t.Fatalf("forbidden actions = %#v, want %q", cont.ActionProposal.ForbiddenActions, want)
		}
	}
	if strings.Contains(cont.ActionProposal.BoundedEffect, "read-only next-phase planning") || actionListContains(cont.ActionProposal.ForbiddenActions, "push_remote") {
		t.Fatalf("action proposal = %#v, want concrete follow-up without generic read-only goal sandbox", cont.ActionProposal)
	}
	if compilation := continuationAuthorityCompilation(cont); compilation.Invalid() {
		t.Fatalf("compilation = %#v, want valid follow-up authority", compilation)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 || !strings.Contains(inlineText, "Approve:\nPublish the completed branch") || !strings.Contains(inlineText, "pull request") {
		t.Fatalf("inline count/text = %d/%q, want push/PR approval prompt", inlineCount, inlineText)
	}
}

func TestGoalContinuationDoesNotInferPushPRFromCompletedPhaseWithoutFollowUpSignal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9049, UserID: 0, Scope: telegramDMScopeRef(9049)}
	now := time.Now().UTC()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "eval-jobs-semantics-done",
		Objective: "Clarify eval --jobs semantics and commit locally only.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		Summary:   "Local commit completed; no push or PR was performed.",
		Proposal: session.OperationProposal{
			ID:            "eval-jobs-local-only",
			Kind:          "commit",
			Summary:       "Clarify eval --jobs semantics and commit locally",
			BoundedEffect: "Stop before push or PR.",
			Status:        session.ProposalStatusApproved,
			UpdatedAt:     now,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status: session.ContinuationStatusIdle,
		ContinuationLease: session.ContinuationLease{
			Status:         session.ContinuationLeaseStatusConsumed,
			MaxTurns:       1,
			RemainingTurns: 0,
			ConsumedAt:     now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, core.InboundMessage{
		ChatID: 9049,
		Text:   approvedContinuationEventText,
		Origin: core.InboundOriginTurnAuthorization,
	}, "completed local-only phase", &turn.Result{VisibleReply: "Recovered cleanly. No more work is needed inside the approved phase. Next useful step would be a separate bounded approval for deployment."})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = true, want false without explicit follow-up approval signal")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no approval prompt", inlineCount)
	}
}

func TestGoalContinuationDoesNotInferForNarrowCompletedTask(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9042, UserID: 0, Scope: telegramDMScopeRef(9042)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "doctor-status-answer",
		Objective: "Answer whether /health diagnose is broken.",
		Status:    session.OperationStatusCompleted,
		Stage:     "answer_complete",
		Summary:   "Answered the narrow status question.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status: session.ContinuationStatusIdle,
		ContinuationLease: session.ContinuationLease{
			Status: session.ContinuationLeaseStatusConsumed,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, core.InboundMessage{
		ChatID: 9042,
		Text:   approvedContinuationEventText,
		Origin: core.InboundOriginTurnAuthorization,
	}, "done", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = true, want false for narrow completed task")
	}
}

func TestGoalContinuationDoesNotInferFromGenericSystemTestLanguage(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9043, UserID: 0, Scope: telegramDMScopeRef(9043)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "review-system-tests",
		Objective: "Review system tests.",
		Status:    session.OperationStatusCompleted,
		Stage:     "review_complete",
		Summary:   "Reviewed the system tests and the plan is done.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status: session.ContinuationStatusIdle,
		ContinuationLease: session.ContinuationLease{
			Status: session.ContinuationLeaseStatusConsumed,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, core.InboundMessage{
		ChatID: 9043,
		Text:   approvedContinuationEventText,
		Origin: core.InboundOriginTurnAuthorization,
	}, "review system tests done", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = true, want false for generic system/test language")
	}
}

func TestGoalContinuationDoesNotInferWhenPlanDoneWithoutDurableObjective(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "small-plan-answer",
		Objective: "Answer the small planning question.",
		Status:    session.OperationStatusCompleted,
		Stage:     "plan_done",
		Summary:   "The plan is done.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusIdle,
		ContinuationLease: session.ContinuationLease{Status: session.ContinuationLeaseStatusConsumed},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, core.InboundMessage{
		ChatID: 9044,
		Text:   approvedContinuationEventText,
		Origin: core.InboundOriginTurnAuthorization,
	}, "plan is done", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = true, want false when no durable objective remains")
	}
}

func TestGoalContinuationInfersWithPendingPlanStepAsRemainingWork(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9045, UserID: 0, Scope: telegramDMScopeRef(9045)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "lighthouse-mini-agent",
		Objective: "Enable a Lighthouse local inbox workflow.",
		Status:    session.OperationStatusCompleted,
		Stage:     "phase_one_complete",
		Summary:   "Phase one completed the read-only contract.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdatePlanState(key, session.PlanState{
		Explanation: "Enable a Lighthouse local inbox workflow.",
		Steps: []session.PlanStep{{
			Step:   "Run one local-only smoke test without credentials.",
			Status: session.PlanStatusPending,
		}},
	}); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusIdle,
		ContinuationLease: session.ContinuationLease{Status: session.ContinuationLeaseStatusConsumed},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, core.InboundMessage{
		ChatID: 9045,
		Text:   approvedContinuationEventText,
		Origin: core.InboundOriginTurnAuthorization,
	}, "phase one complete", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if !inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = false, want true with durable objective, phase-one signal, and pending plan work")
	}
}

func TestGoalContinuationDoesNotInferWhenDurablePhasePlanOwnsNextStep(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9046, UserID: 0, Scope: telegramDMScopeRef(9046)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "lighthouse-durable-phase-plan",
		Objective: "Deliver the Lighthouse inbox workflow through a durable phase plan.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_plan",
		Summary:   "Phase one is done and phase two is pending approval.",
		PhasePlan: session.OperationPhasePlan{
			ID:             "lighthouse-durable-plan",
			CurrentPhaseID: "phase-2",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "Write the read-only contract", Status: session.PlanStatusCompleted},
				{ID: "phase-2", Summary: "Implement the local inbox bridge", Status: session.PlanStatusPending, AuthorityClass: "workspace_write"},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusIdle,
		ContinuationLease: session.ContinuationLease{Status: session.ContinuationLeaseStatusConsumed},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, core.InboundMessage{
		ChatID: 9046,
		Text:   approvedContinuationEventText,
		Origin: core.InboundOriginTurnAuthorization,
	}, "phase one complete; next phase remains", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = true, want false because phase_plan owns the pending next step")
	}
}

func TestGoalContinuationDoesNotInferWhenDurablePhasePlanCompleted(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9047, UserID: 0, Scope: telegramDMScopeRef(9047)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "lighthouse-durable-phase-plan-complete",
		Objective: "Deliver the Lighthouse inbox workflow through a durable phase plan.",
		Status:    session.OperationStatusCompleted,
		Stage:     "phase_plan_complete",
		Summary:   "All durable phases are complete.",
		PhasePlan: session.OperationPhasePlan{
			ID: "lighthouse-durable-plan-complete",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "Write the read-only contract", Status: session.PlanStatusCompleted},
				{ID: "phase-2", Summary: "Implement the local inbox bridge", Status: session.PlanStatusCompleted},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusIdle,
		ContinuationLease: session.ContinuationLease{Status: session.ContinuationLeaseStatusConsumed},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	inferred, err := rt.maybeInferGoalContinuationProposal(context.Background(), key, core.InboundMessage{
		ChatID: 9047,
		Text:   approvedContinuationEventText,
		Origin: core.InboundOriginTurnAuthorization,
	}, "all phases complete", &turn.Result{})
	if err != nil {
		t.Fatalf("maybeInferGoalContinuationProposal() err = %v", err)
	}
	if inferred {
		t.Fatal("maybeInferGoalContinuationProposal() = true, want false because durable phase plan is complete")
	}
}
