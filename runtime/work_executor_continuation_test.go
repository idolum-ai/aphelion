//go:build linux

package runtime

import (
	"context"
	"errors"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLeaseAccessDeniedResetsOperationPhaseForFreshApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8189, UserID: 0, Scope: telegramDMScopeRef(8189)}
	opState := session.OperationState{
		ID:        "phase-denial-recovery-op",
		Objective: "Recover from a denied phase lease.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "phase-denial-recovery-plan",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{{
				ID:             "phase-1",
				Summary:        "Patch the implementation",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "workspace_write",
				LeaseID:        "lease-phase-denied",
			}},
		},
	}
	proposalID := operationPhaseProposalID(opState, opState.PhasePlan.Phases[0])
	opState.Proposal = session.OperationProposal{
		ID:      proposalID,
		Kind:    "workspace_write",
		Summary: "Patch the implementation",
		Status:  session.ProposalStatusApproved,
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     proposalID,
		Objective:      "Recover from a denied phase lease.",
		StageSummary:   "Patch the implementation",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:          "aprop-" + proposalID,
			OperationID: proposalID,
			Summary:     "Patch the implementation",
			RiskClass:   "workspace_write",
			Status:      session.ProposalStatusApproved,
			ExpiresAt:   expiresAt,
			PlanHash:    "sha256:phase-denied",
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-phase-denied",
			ProposalID:       "aprop-" + proposalID,
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			AllowedActions:   []string{"read_only"},
			ForbiddenActions: []string{"workspace_write"},
			ApprovedBy:       1001,
			ApprovedAt:       expiresAt.Add(-time.Hour),
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:phase-denied",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8189); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if work.calls != 0 {
		t.Fatalf("work calls = %d, want denial before executor", work.calls)
	}
	got, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if got.Status != session.OperationStatusBlocked || got.PhasePlan.Phases[0].Status != session.PlanStatusPending || got.PhasePlan.Phases[0].LeaseID != "" {
		t.Fatalf("operation = %#v, want blocked with phase reset to pending", got)
	}
}

func TestMetadataPreflightContinuationRunsReadOnlyDespiteWorkspaceWriteDiagnosticText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8191, UserID: 0, Scope: telegramDMScopeRef(8191)}
	action := session.ActionProposal{
		ID:            "aprop-metadata-preflight",
		Summary:       "Live-adjacent metadata preflight. Prior diagnostic mentioned workspace_write mismatch.",
		BoundedEffect: "Inspect live config route and token-file metadata only; no token contents and no Telegram network.",
		RiskClass:     session.AuthorityClassLocalSecretMetadataReadLiveConfigRead,
		Status:        session.ProposalStatusApproved,
		ExpiresAt:     expiresAt,
	}
	action = applyContinuationLeaseClassBoundaries(action)
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, 1, time.Now().UTC())
	lease.Status = session.ContinuationLeaseStatusActive
	lease.ApprovedAt = expiresAt.Add(-time.Hour)
	lease.ApprovedBy = 1001
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "metadata-preflight",
		Objective:         "Run metadata-only preflight.",
		StageSummary:      action.Summary,
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    action,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-metadata-preflight", Objective: "Run metadata-only preflight.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	mode := continuationWorkMode(session.ContinuationState{ActionProposal: action, ContinuationLease: lease, StageSummary: action.Summary})
	if mode != WorkModeReadOnly {
		t.Fatalf("continuationWorkMode() = %q, want read_only", mode)
	}
	if err := rt.TriggerContinuation(context.Background(), 8191); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if work.calls != 1 {
		t.Fatalf("work calls = %d, want one read-only executor call", work.calls)
	}
	if work.lastReq.Mode != WorkModeReadOnly {
		t.Fatalf("work mode = %q, want read_only", work.lastReq.Mode)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.Status == session.ContinuationLeaseStatusRevoked {
		t.Fatalf("continuation lease = %#v, want not revoked by workspace_write mismatch", got.ContinuationLease)
	}
}

func TestTriggerCodingContinuationRunsWorkExecutor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true, result: WorkResult{
		Summary:      "patched tests",
		ChangedFiles: []string{"runtime/work_executor.go"},
		Commands:     []string{"go test ./runtime"},
		CodexEvents: []session.WorkCodexEvent{
			{Kind: "file_change", Method: "item/fileChange/completed", Path: "runtime/work_executor.go", Status: "completed", Preview: "@@ patched"},
			{Kind: "command", Method: "item/commandExecution/completed", Command: "go test ./runtime", Status: "completed"},
		},
		PatchPreview:     "@@ patched",
		CommitLaneStatus: "commit_requires_separate_lease",
	}}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex", "native"}}, []WorkExecutor{work})
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{}}
	rt.interactiveDMAssembler = recorder

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8188, UserID: 0, Scope: telegramDMScopeRef(8188)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "work-lane",
		Objective:      "Patch the work lane.",
		StageSummary:   "Edit runtime work executor files and test.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-work-lane",
			Summary:       "Patch work executor",
			BoundedEffect: "Edit runtime work executor files and run focused tests.",
			RiskClass:     "workspace_write",
			AllowedActions: []string{
				"execute_bounded_proposal_once",
				"workspace_write",
				"run_tests",
			},
			Status:    session.ProposalStatusApproved,
			ExpiresAt: expiresAt,
			PlanHash:  "sha256:work-lane",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-work-lane",
			ProposalID:     "aprop-work-lane",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			AllowedActions: []string{
				"execute_bounded_proposal_once",
				"workspace_write",
				"run_tests",
			},
			ExpiresAt: expiresAt,
			PlanHash:  "sha256:work-lane",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-work-lane", Objective: "Patch the work lane.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8188); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if work.calls != 1 {
		t.Fatalf("work calls = %d, want 1", work.calls)
	}
	if recorder.called {
		t.Fatal("interactive assembler called, want coding continuation routed through work executor")
	}
	if work.lastReq.OperationID != "op-work-lane" || work.lastReq.LeaseID != "lease-work-lane" {
		t.Fatalf("work request = %#v, want operation and lease ids", work.lastReq)
	}
	if work.lastReq.Mode != WorkModeWorkspaceWrite {
		t.Fatalf("work mode = %q, want workspace_write", work.lastReq.Mode)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want consumed idle", got)
	}
	op, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if op.Work.Executor != "codex" || op.Work.LastSummary != "patched tests" || len(op.Work.ChangedFiles) != 1 {
		t.Fatalf("operation work metadata = %#v, want codex result persisted", op.Work)
	}
	if op.Work.LastCompletedAt.IsZero() || op.Work.LastError != "" {
		t.Fatalf("operation work completion = %#v, want completed evidence without error", op.Work)
	}
	if len(op.Work.CodexEvents) != 2 || op.Work.CodexEvents[0].Kind != "file_change" || op.Work.PatchPreview != "@@ patched" || op.Work.CommitLaneStatus != "commit_requires_separate_lease" {
		t.Fatalf("operation codex work metadata = %#v, want captured Codex interface evidence", op.Work)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "patched tests") || !strings.Contains(sender.sent[0].Text, "runtime/work_executor.go") || !strings.Contains(sender.sent[0].Text, "commit_requires_separate_lease") {
		t.Fatalf("sent = %#v, want visible work executor summary", sender.sent)
	}
}

func TestConcurrentWorkContinuationTriggerExecutesSingleLeaseTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true, result: WorkResult{Summary: "patched once"}}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	key := session.SessionKey{ChatID: 8198, UserID: 0, Scope: telegramDMScopeRef(8198)}
	action := session.ActionProposal{
		ID:            "aprop-concurrent-work-trigger",
		Summary:       "Patch one runtime file",
		BoundedEffect: "Run one bounded workspace-write work executor turn.",
		RiskClass:     "workspace_write",
		AllowedActions: []string{
			"execute_bounded_proposal_once",
			"workspace_write",
			"run_tests",
		},
		Status:    session.ProposalStatusApproved,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	action.PlanHash = actionProposalHash(action)
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "concurrent-work-trigger",
		Objective:      "Prove concurrent work triggers cannot execute more than the lease allows.",
		StageSummary:   action.Summary,
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: action,
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-concurrent-work-trigger",
			ProposalID:     action.ID,
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			AllowedActions: action.AllowedActions,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			ExpiresAt:      expiresAt,
			PlanHash:       action.PlanHash,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-concurrent-work-trigger", Objective: "Patch once.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rt.TriggerContinuationForKey(context.Background(), key)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("TriggerContinuationForKey() err = %v", err)
		}
	}
	if got := work.CallCount(); got != 1 {
		t.Fatalf("work executor calls = %d, want exactly one executed continuation turn", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 1 {
		t.Fatalf("consumed events = %d, want 1", got)
	}
}

func TestConsumedWorkPhaseOffersNextPhaseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true, result: WorkResult{Summary: "committed and pushed"}}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	key := session.SessionKey{ChatID: 8192, UserID: 0, Scope: telegramDMScopeRef(8192)}
	opState := session.OperationState{
		ID:        "planning-improvements-pr-review",
		Objective: "Commit and push the branch, then create a draft PR and assess readiness.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "planning-improvements-pr-review-plan",
			Goal:           "Prepare branch for draft PR review.",
			CurrentPhaseID: "commit-push",
			Phases: []session.OperationPhase{
				{
					ID:                "commit-push",
					Summary:           "Commit and push inspected planning changes",
					Status:            session.PlanStatusInProgress,
					AuthorityClass:    "commit",
					BoundedEffect:     "Commit and push only the inspected branch changes, then report the remote head.",
					AllowedActions:    []string{"git_commit", "git_push", "report_commit_evidence"},
					ForbiddenActions:  []string{"create_or_update_pull_request", "deploy_or_restart"},
					RequiresApproval:  true,
					GateLevel:         operationGateLevelNormalApproval,
					GateReasonCode:    "capability_grant",
					ApprovalSubject:   "operator",
					BlockedReasonCode: "requires_approval",
					LeaseID:           "lease-phase-planning-improvements-pr-review-commit-push",
				},
				{
					ID:                "phase-planning-improvements-pr-review-commit-push",
					Summary:           "Commit and push inspected planning changes",
					Status:            session.PlanStatusPending,
					AuthorityClass:    "commit",
					BoundedEffect:     "Duplicate proposal-shaped phase that should be reconciled to the completed commit/push work.",
					AllowedActions:    []string{"git_commit", "git_push", "report_commit_evidence"},
					ForbiddenActions:  []string{"create_or_update_pull_request", "deploy_or_restart"},
					RequiresApproval:  true,
					GateLevel:         operationGateLevelNormalApproval,
					GateReasonCode:    "capability_grant",
					ApprovalSubject:   "operator",
					BlockedReasonCode: "requires_approval",
				},
				{
					ID:                "draft-pr-review",
					Summary:           "Read full branch, create draft PR, and assess readiness",
					Status:            session.PlanStatusPending,
					AuthorityClass:    "commit",
					BoundedEffect:     "Read the full branch diff, create or update one draft PR against main, and report readiness. No merge or deploy.",
					AllowedActions:    []string{"read_full_branch_diff", "create_or_update_draft_pull_request", "report_pr_url", "provide_readiness_review"},
					ForbiddenActions:  []string{"merge_pull_request", "deploy_or_restart", "credential_token_output"},
					RequiresApproval:  true,
					GateLevel:         operationGateLevelNormalApproval,
					GateReasonCode:    "capability_grant",
					ApprovalSubject:   "operator",
					BlockedReasonCode: "requires_approval",
				},
			},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	proposalID := operationPhaseProposalID(opState, opState.PhasePlan.Phases[0])
	action := session.ActionProposal{
		ID:               "aprop-" + proposalID,
		OperationID:      proposalID,
		Summary:          opState.PhasePlan.Phases[0].Summary,
		BoundedEffect:    opState.PhasePlan.Phases[0].BoundedEffect,
		RiskClass:        "commit",
		AllowedActions:   opState.PhasePlan.Phases[0].AllowedActions,
		ForbiddenActions: opState.PhasePlan.Phases[0].ForbiddenActions,
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        expiresAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, 1, now)
	lease.ID = "lease-phase-planning-improvements-pr-review-commit-push"
	lease.Status = session.ContinuationLeaseStatusActive
	lease.RemainingTurns = 1
	lease.ApprovedBy = 1001
	lease.ApprovedAt = now
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        proposalID,
		Objective:         opState.Objective,
		StageSummary:      action.Summary,
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    action,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if work.calls != 1 {
		t.Fatalf("work calls = %d, want one approved work phase", work.calls)
	}
	gotOp, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if gotOp.PhasePlan.Phases[0].Status != session.PlanStatusCompleted {
		t.Fatalf("first phase status = %q, want completed", gotOp.PhasePlan.Phases[0].Status)
	}
	if gotOp.PhasePlan.Phases[1].Status != session.PlanStatusCompleted || !gotOp.PhasePlan.Phases[1].StaleAuthority {
		t.Fatalf("duplicate phase = %#v, want stale completed duplicate", gotOp.PhasePlan.Phases[1])
	}
	if gotOp.PhasePlan.Phases[2].LeaseID == "" || gotOp.PhasePlan.CurrentPhaseID != "draft-pr-review" {
		t.Fatalf("phase plan = %#v, want next phase linked to a pending approval", gotOp.PhasePlan)
	}
	gotCont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if gotCont.Status != session.ContinuationStatusPending || !strings.Contains(gotCont.ActionProposal.Summary, "draft PR") {
		t.Fatalf("continuation = %#v, want pending draft PR approval", gotCont)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 || !strings.Contains(inlineText, "draft PR") {
		t.Fatalf("inline count/text = %d/%q, want next approval prompt", inlineCount, inlineText)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if countEventsByType(events, core.ExecutionEventContinuationOffered) != 1 || !hasExecutionEvent(events, core.ExecutionEventContinuationBoundaryReached) {
		t.Fatalf("events = %#v, want boundary plus next continuation offer", events)
	}
}

func TestStartupRepairRevokesStaleDuplicateCompletedPhaseApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8193, UserID: 0, Scope: telegramDMScopeRef(8193)}
	duplicateID := "phase-stale-duplicate-op-commit-push"
	opState := session.OperationState{
		ID:        "stale-duplicate-op",
		Objective: "Commit and push the branch, then review the result.",
		Status:    session.OperationStatusBlocked,
		Stage:     "plan_lease_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "stale-duplicate-plan",
			Goal:           "Do not re-offer already completed work.",
			CurrentPhaseID: duplicateID,
			Phases: []session.OperationPhase{
				{
					ID:             "commit-push",
					Summary:        "Commit and push inspected planning changes",
					Status:         session.PlanStatusCompleted,
					AuthorityClass: "commit",
					CompletedAt:    now.Add(-10 * time.Minute),
				},
				{
					ID:               duplicateID,
					Summary:          "Commit and push inspected planning changes",
					Status:           session.PlanStatusPending,
					AuthorityClass:   "commit",
					BoundedEffect:    "Re-offered duplicate of already completed commit/push work.",
					AllowedActions:   []string{"git_commit", "git_push", "report_commit_evidence"},
					ForbiddenActions: []string{"deploy_or_restart"},
					RequiresApproval: true,
				},
			},
		},
	}
	lease, ok := operationPlanLeaseFromPhasePlan(opState, now)
	if !ok {
		t.Fatal("operationPlanLeaseFromPhasePlan() ok = false, want stale plan lease fixture")
	}
	opState.PlanLease = lease
	state := continuationStateFromOperationPlanLease(opState, lease, "continue", now)
	opState = operationStateWithMaterializedPlanLease(opState, state, now)
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	repaired, err := rt.repairInvalidPendingContinuationApprovals(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("repairInvalidPendingContinuationApprovals() err = %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired = %d, want one stale duplicate approval repair", repaired)
	}
	gotOp, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if gotOp.PhasePlan.Phases[1].Status != session.PlanStatusCompleted || !gotOp.PhasePlan.Phases[1].StaleAuthority {
		t.Fatalf("duplicate phase = %#v, want reconciled completed duplicate", gotOp.PhasePlan.Phases[1])
	}
	if gotOp.PlanLease.Status != session.PlanLeaseStatusCompleted {
		t.Fatalf("plan lease status = %q, want completed stale lease", gotOp.PlanLease.Status)
	}
	if gotOp.Status != session.OperationStatusCompleted || gotOp.Stage != "completed" {
		t.Fatalf("operation status/stage = %q/%q, want completed/completed", gotOp.Status, gotOp.Stage)
	}
	gotCont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if gotCont.Status != session.ContinuationStatusRevoked || gotCont.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
		t.Fatalf("continuation = %#v, want stale pending approval revoked", gotCont)
	}
	if gotCont.HandshakeBlockedReason != "stale_completed_operation" {
		t.Fatalf("HandshakeBlockedReason = %q, want stale_completed_operation", gotCont.HandshakeBlockedReason)
	}
}

func TestStartupRepairClosesCompletedPhasePlanWithoutPendingContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 8194, UserID: 0, Scope: telegramDMScopeRef(8194)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "completed-after-stale-approval-op",
		Objective: "Commit and push the branch, then review the result.",
		Status:    session.OperationStatusBlocked,
		Stage:     "phase_approval_adjudicated",
		Proposal: session.OperationProposal{
			ID:     "plan-lease-completed-after-stale-approval-op",
			Kind:   "plan_lease",
			Status: session.ProposalStatusSuperseded,
		},
		PlanLease: session.OperationPlanLease{
			ID:              "completed-after-stale-approval-op",
			Status:          session.PlanLeaseStatusCompleted,
			CoveredPhaseIDs: []string{"phase-completed-after-stale-approval-op-commit-push"},
			UpdatedAt:       now,
		},
		PhasePlan: session.OperationPhasePlan{
			ID:             "completed-after-stale-approval-plan",
			Goal:           "Close when no pending work remains.",
			CurrentPhaseID: "phase-completed-after-stale-approval-op-commit-push",
			Phases: []session.OperationPhase{
				{
					ID:                 "commit-push",
					Summary:            "Commit and push inspected planning changes",
					Status:             session.PlanStatusCompleted,
					AuthorityClass:     "commit",
					CompletedAt:        now.Add(-10 * time.Minute),
					SupersedesPhaseIDs: []string{"phase-completed-after-stale-approval-op-commit-push"},
				},
				{
					ID:                "phase-completed-after-stale-approval-op-commit-push",
					Summary:           "Commit and push inspected planning changes",
					Status:            session.PlanStatusCompleted,
					AuthorityClass:    "commit",
					StaleAuthority:    true,
					BlockedReasonCode: "superseded_phase",
					CompletedAt:       now.Add(-10 * time.Minute),
				},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:            session.ContinuationStatusRevoked,
		ContinuationLease: session.ContinuationLease{Status: session.ContinuationLeaseStatusRevoked},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	repaired, err := rt.repairInvalidPendingContinuationApprovals(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("repairInvalidPendingContinuationApprovals() err = %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired = %d, want one operation closure repair", repaired)
	}
	gotOp, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if gotOp.Status != session.OperationStatusCompleted || gotOp.Stage != "completed" {
		t.Fatalf("operation status/stage = %q/%q, want completed/completed", gotOp.Status, gotOp.Stage)
	}
}

func TestNativeWorkExecutorTreatsProviderFailureTurnAsFailed(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.interactiveDMAssembler = &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{
		Text:            "Inference backend failed before provider fallback was applicable. This turn did not complete.",
		ProviderFailure: "codex: stream failed: request error",
		ProviderEvents: []core.ProviderEvent{
			{EventType: "provider.error", Provider: "codex", Error: "stream failed", PartialToolCalls: 1},
		},
	}}

	result, err := nativeWorkExecutor{runtime: rt}.Run(context.Background(), WorkRequest{
		ChatID: 8189,
		Actor:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
	})
	if err == nil || !strings.Contains(err.Error(), "inference backend failed") {
		t.Fatalf("Run() err = %v, want provider failure error", err)
	}
	if result.CompletionKind != "native_turn_provider_failed" || result.ProviderFailure == "" || !result.SideEffects {
		t.Fatalf("result = %#v, want failed native turn marked with provider failure and side effects", result)
	}
	if len(result.ProviderEvents) != 1 || result.ProviderEvents[0].PartialToolCalls != 1 {
		t.Fatalf("provider events = %#v, want captured provider event evidence", result.ProviderEvents)
	}
}

func TestNativeWorkExecutorSchedulesBudgetRecoveryTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recovery := nativeWorkBudgetRecoveryTestRecovery()
	recorder := &budgetRecoveryDeliveringAssembler{
		runtime: rt,
		results: []*core.TurnResult{
			{Text: turnBudgetRecoveryHandoffText(recovery) + "\n\nNo edits/commits/pushes completed.", Recovery: recovery},
			{Text: "Recovery re-latched to durable state and narrowed the next action."},
		},
	}
	rt.interactiveDMAssembler = recorder

	result, err := nativeWorkExecutor{runtime: rt}.Run(context.Background(), WorkRequest{
		ChatID: 8195,
		Actor:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
	})
	if err != nil {
		t.Fatalf("Run() err = %v, want scheduled budget recovery without manual retry failure", err)
	}
	if result.CompletionKind != "native_turn_budget_recovery_scheduled" || result.RecoveryKind != string(core.TurnRecoveryTokenBudgetExhausted) || result.RecoveryDelivery != "budget_recovery_scheduled" || !result.SideEffects {
		t.Fatalf("result = %#v, want native turn marked with scheduled budget recovery", result)
	}
	if !strings.Contains(result.Summary, "No edits/commits/pushes completed") {
		t.Fatalf("summary = %q, want recovery handoff evidence preserved", result.Summary)
	}
	inputs := recorder.Inputs()
	if len(inputs) == 0 {
		t.Fatal("work executor calls = 0, want initial continuation call")
	}
	firstInput := inputs[0]
	if firstInput.Msg.OriginDetail != string(session.TurnAuthorizationKindContinuation) {
		t.Fatalf("work executor origin detail = %q, want continuation provenance", firstInput.Msg.OriginDetail)
	}
	if firstInput.DeferBudgetRecoveryToWorkFailureRetry {
		t.Fatal("work executor input requested manual retry deferral; want automatic budget recovery")
	}
	if firstInput.EventAwareness.TurnAuthorizationKind != string(session.TurnAuthorizationKindContinuation) {
		t.Fatalf("event awareness turn authorization kind = %q, want continuation", firstInput.EventAwareness.TurnAuthorizationKind)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.WaitForBackgroundLoops(waitCtx); err != nil {
		t.Fatalf("WaitForBackgroundLoops() err = %v", err)
	}
	if !recorder.HasOriginDetail(turnBudgetRecoveryOriginDetail) {
		t.Fatalf("recorded inputs = %#v, want scheduled budget recovery turn", recorder.Inputs())
	}
}

type budgetRecoveryDeliveringAssembler struct {
	mu        sync.Mutex
	runtime   *Runtime
	result    *core.TurnResult
	results   []*core.TurnResult
	inputs    []interactiveDMTurnAssemblyInput
	callCount int
}

func (a *budgetRecoveryDeliveringAssembler) Run(ctx context.Context, input interactiveDMTurnAssemblyInput) (*core.TurnResult, error) {
	result, err := a.RunTurn(ctx, input)
	if result == nil {
		return nil, err
	}
	return result.Turn, err
}

func (a *budgetRecoveryDeliveringAssembler) RunTurn(ctx context.Context, input interactiveDMTurnAssemblyInput) (*turn.Result, error) {
	a.mu.Lock()
	index := a.callCount
	a.callCount++
	a.inputs = append(a.inputs, input)
	a.mu.Unlock()

	result := a.result
	if index < len(a.results) {
		result = a.results[index]
	}
	if result == nil {
		result = &core.TurnResult{}
	}
	recovery, _ := turnResultBudgetRecovery(result)
	visible := strings.TrimSpace(result.Text)
	if visible == "" {
		visible = turnBudgetRecoveryHandoffText(recovery)
	}
	turnResult := &turn.Result{
		Turn:         result,
		VisibleReply: visible,
	}
	if a.runtime != nil && a.runtime.store != nil {
		if opState, err := a.runtime.store.OperationState(input.Key); err == nil {
			turnResult.OperationState = opState
		}
	}
	port := &turnDeliveryPort{
		runtime:                               a.runtime,
		key:                                   input.Key,
		msg:                                   input.Msg,
		deliver:                               true,
		recordOutbound:                        false,
		deferBudgetRecoveryToWorkFailureRetry: input.DeferBudgetRecoveryToWorkFailureRetry,
	}
	delivered, err := port.Deliver(ctx, turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: input.Msg.ChatID, Text: visible},
		Result:  turnResult,
	})
	if err != nil {
		return turnResult, err
	}
	if delivered != nil {
		turnResult.Delivery = *delivered
	}
	return turnResult, nil
}

func (a *budgetRecoveryDeliveringAssembler) Inputs() []interactiveDMTurnAssemblyInput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]interactiveDMTurnAssemblyInput(nil), a.inputs...)
}

func (a *budgetRecoveryDeliveringAssembler) HasOriginDetail(originDetail string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, input := range a.inputs {
		if input.Msg.OriginDetail == originDetail {
			return true
		}
	}
	return false
}

func TestTriggerCodingContinuationFailureOffersFreshRetry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	workErr := errors.New("codex stream failed after partial response")
	work := &fakeWorkExecutor{name: "codex", ready: true, err: workErr}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8190, UserID: 0, Scope: telegramDMScopeRef(8190)}
	prior := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "work-failure-retry",
		Objective:      "Patch the work failure retry.",
		StageSummary:   "Run bounded code work and report.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-work-failure-retry",
			Summary:        "Patch work failure retry",
			WhyNow:         "The prior approved step should run now.",
			BoundedEffect:  "Edit runtime work executor files and run focused tests.",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write", "run_tests"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-failure-retry",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-work-failure-retry",
			ProposalID:     "aprop-work-failure-retry",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			AllowedActions: []string{"workspace_write", "run_tests"},
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-failure-retry",
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-work-failure-retry", Objective: "Patch the work failure retry.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomyForKey(context.Background(), key, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomyForKey() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApprovalForKey(context.Background(), key, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApprovalForKey() err = %v", err)
	}

	err = rt.TriggerContinuation(context.Background(), 8190)
	if err == nil || !strings.Contains(err.Error(), workErr.Error()) {
		t.Fatalf("TriggerContinuation() err = %v, want work executor failure", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusPending || got.ActionProposal.Status != session.ProposalStatusPending || got.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want fresh pending retry proposal", got)
	}
	if got.ActionProposal.ID == prior.ActionProposal.ID || got.ContinuationLease.ID == prior.ContinuationLease.ID {
		t.Fatalf("fresh ids reused old proposal/lease: proposal=%q lease=%q", got.ActionProposal.ID, got.ContinuationLease.ID)
	}
	if got.ActionProposal.BoundedEffect != prior.ActionProposal.BoundedEffect || !strings.Contains(got.ActionProposal.WhyNow, "approval before retrying") {
		t.Fatalf("fresh proposal = %#v, want same bounded effect with failure reason", got.ActionProposal)
	}
	if got.ActionProposal.AutoApproveEligible == nil || *got.ActionProposal.AutoApproveEligible {
		t.Fatalf("AutoApproveEligible = %#v, want explicit manual-only retry", got.ActionProposal.AutoApproveEligible)
	}
	if got.ActionProposal.PlanHash == "" || got.ContinuationLease.PlanHash != got.ActionProposal.PlanHash {
		t.Fatalf("plan hashes proposal=%q lease=%q, want synced manual-only hash", got.ActionProposal.PlanHash, got.ContinuationLease.PlanHash)
	}
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := store.ActiveOperatorAutoApprovalLeasesForScope(key.ChatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("active auto-approval leases = %#v, want cleared at manual retry barrier", leases)
	}
	overrides, err := store.ActiveOperatorAutonomyOverridesForScope(key.ChatID, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverridesForScope() err = %v", err)
	}
	if len(overrides) != 0 {
		t.Fatalf("active autonomy overrides = %#v, want cleared at manual retry barrier", overrides)
	}
	resolution, err := rt.AutoResolveDecision(context.Background(), decision.PendingDecision{
		ID: "dec-after-manual-retry-barrier",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        key.ChatID,
			SenderID:      1002,
			ScopeKind:     scopeKind,
			ScopeID:       scopeID,
			Prompt:        "Approve retry?",
			Details:       got.ActionProposal.BoundedEffect,
			Choices:       []decision.Choice{{ID: "deny", Label: "Deny"}, {ID: "approve", Label: "Approve"}},
			DefaultChoice: "deny",
		},
	})
	if err != nil {
		t.Fatalf("AutoResolveDecision() err = %v", err)
	}
	if resolution.Choice != "" {
		t.Fatalf("AutoResolveDecision() = %#v, want no auto-resolution after manual retry barrier", resolution)
	}
	op, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if !strings.Contains(op.Work.LastError, workErr.Error()) || !op.Work.LastCompletedAt.IsZero() {
		t.Fatalf("operation work = %#v, want failure recorded without completion", op.Work)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one retry approval prompt", inlineCount)
	}
	if !strings.Contains(inlineText, "approval before retrying") || !strings.Contains(inlineText, prior.ActionProposal.BoundedEffect) {
		t.Fatalf("inline text = %q, want retry reason and bounded effect", inlineText)
	}
	if !strings.Contains(inlineText, "Approval window paused for this retry; approve this one step manually.") {
		t.Fatalf("inline text = %q, want visible manual retry barrier notice", inlineText)
	}
	if strings.Contains(inlineText, "failed before completion") || strings.Contains(inlineText, "fresh lease") {
		t.Fatalf("inline text leaked internal retry copy: %q", inlineText)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWorkExecutorFailed) {
		t.Fatalf("events = %#v, want work executor failure event", events)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventAutoApprovalRevoked, "manual_retry_barrier") {
		t.Fatalf("events = %#v, want auto-approval revocation for manual retry barrier", events)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventAutoModeRevoked, "manual_retry_barrier") {
		t.Fatalf("events = %#v, want auto-mode revocation for manual retry barrier", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventWorkExecutorSucceeded) {
		t.Fatalf("events = %#v, want no work executor success event", events)
	}
}

func TestTriggerCodingContinuationEmptySuccessOffersFreshRetry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true, result: WorkResult{}, allowEmptyResult: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8194, UserID: 0, Scope: telegramDMScopeRef(8194)}
	prior := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "work-empty-success-retry",
		Objective:      "Patch the work empty-success retry.",
		StageSummary:   "Run bounded code work and report.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-work-empty-success-retry",
			Summary:        "Patch work empty-success retry",
			WhyNow:         "The prior approved step should run now.",
			BoundedEffect:  "Edit runtime work executor files and run focused tests.",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write", "run_tests"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-empty-success-retry",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-work-empty-success-retry",
			ProposalID:     "aprop-work-empty-success-retry",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			AllowedActions: []string{"workspace_write", "run_tests"},
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-empty-success-retry",
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-work-empty-success-retry", Objective: "Patch the work empty-success retry.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	err = rt.TriggerContinuation(context.Background(), 8194)
	if err == nil || !strings.Contains(err.Error(), errWorkExecutorNoCompletionEvidence.Error()) {
		t.Fatalf("TriggerContinuation() err = %v, want no-completion-evidence failure", err)
	}
	if work.calls != 1 {
		t.Fatalf("work calls = %d, want one executor attempt", work.calls)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusPending || got.ActionProposal.Status != session.ProposalStatusPending || got.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want fresh pending retry proposal", got)
	}
	if got.ActionProposal.ID == prior.ActionProposal.ID || got.ContinuationLease.ID == prior.ContinuationLease.ID {
		t.Fatalf("fresh ids reused old proposal/lease: proposal=%q lease=%q", got.ActionProposal.ID, got.ContinuationLease.ID)
	}
	op, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if !strings.Contains(op.Work.LastError, errWorkExecutorNoCompletionEvidence.Error()) || !op.Work.LastCompletedAt.IsZero() {
		t.Fatalf("operation work = %#v, want no-evidence failure without completion", op.Work)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want retry approval prompt", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWorkExecutorFailed) {
		t.Fatalf("events = %#v, want work executor failure event", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventWorkExecutorSucceeded) {
		t.Fatalf("events = %#v, want no work executor success event", events)
	}
}

func TestWorkFailureRetryFallbackSendsPlainNoticeWhenInlinePromptFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	sender.inlineErr = errors.New("telegram inline keyboard failed")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8191, UserID: 0, Scope: telegramDMScopeRef(8191)}
	now := time.Now().UTC()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     "work-failure-inline-fallback",
		Objective:      "Patch retry fallback.",
		StageSummary:   "Retry the bounded work manually.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-work-failure-inline-fallback",
			Summary:        "Patch retry fallback",
			WhyNow:         "A prior run failed and needs manual approval before retrying.",
			BoundedEffect:  "Edit runtime retry fallback only.",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write"},
			Status:         session.ProposalStatusPending,
			ExpiresAt:      now.Add(time.Hour),
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-work-failure-inline-fallback",
			ProposalID:     "aprop-work-failure-inline-fallback",
			Status:         session.ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			AllowedActions: []string{"workspace_write"},
			ExpiresAt:      now.Add(time.Hour),
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomyForKey(context.Background(), key, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomyForKey() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApprovalForKey(context.Background(), key, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApprovalForKey() err = %v", err)
	}

	workErr := errors.New("codex failed before completion")
	rt.offerWorkFailureRetry(context.Background(), key, key.ChatID, workErr)

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sent := append([]core.OutboundMessage(nil), sender.sent...)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want failed inline prompt not recorded", inlineCount)
	}
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "I could not show the retry approval buttons.") || !strings.Contains(sent[0].Text, "fresh manual approval") {
		t.Fatalf("sent = %#v, want plain fallback retry notice", sent)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventContinuationBlocked, `"fallback_sent":true`) {
		t.Fatalf("events = %#v, want retry_offer_failed event with fallback_sent=true", events)
	}
}

func hasExecutionEventPayload(events []session.ExecutionEvent, eventType string, payloadNeedle string) bool {
	for _, event := range events {
		if event.EventType == eventType && strings.Contains(event.PayloadJSON, payloadNeedle) {
			return true
		}
	}
	return false
}

func TestNoEffectRecoveryHandoffRequiresFreshApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response. Pending tool calls were not executed and must be re-decided from persisted state.",
		MaxAutoHops:    3,
	}
	key := session.SessionKey{ChatID: 8195, UserID: 0, Scope: telegramDMScopeRef(8195)}
	work := &fakeWorkExecutor{
		name:  "native",
		ready: true,
		result: WorkResult{
			ExecutorName:    "native",
			Summary:         "Budget recovery handoff: " + recovery.Summary,
			Recovery:        recovery,
			CompletionKind:  "native_turn_recovery_handoff",
			ProviderFailure: "",
		},
		runHook: func(_ WorkRequest) {
			scope, scopePayload := rt.turnBudgetRecoveryScope(key, core.InboundMessage{ChatID: key.ChatID, SenderID: 1001}, nil)
			payload := turnBudgetRecoveryPayload(recovery, scope, scopePayload, 1, 3)
			rt.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "resuming", payload, time.Now().UTC())
		},
	}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"native"}}, []WorkExecutor{work})

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	opState := session.OperationState{
		ID:        "op-no-effect-recovery",
		Objective: "Patch the approved runtime task.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-no-effect-recovery",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{{
				ID:             "phase-1",
				Summary:        "Patch runtime continuation recovery",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Edit runtime continuation recovery files and run focused tests.",
				AllowedActions: []string{"workspace_write", "run_tests"},
				LeaseID:        "lease-no-effect-recovery",
			}},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	prior := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "phase-1",
		Objective:      opState.Objective,
		StageSummary:   "Patch runtime continuation recovery",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-no-effect-recovery",
			OperationID:    operationPhaseProposalID(opState, opState.PhasePlan.Phases[0]),
			Summary:        "Patch runtime continuation recovery",
			BoundedEffect:  "Edit runtime continuation recovery files and run focused tests.",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write", "run_tests"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:no-effect-recovery",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-no-effect-recovery",
			ProposalID:     "aprop-no-effect-recovery",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			AllowedActions: []string{"workspace_write", "run_tests"},
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:no-effect-recovery",
		},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if work.calls != 1 {
		t.Fatalf("work calls = %d, want one call while recovery owns the next attempt", work.calls)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed || got.ContinuationLease.RemainingTurns != 0 {
		t.Fatalf("continuation = %#v, want consumed lease awaiting fresh approval after recovery", got)
	}
	gotOp, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if gotOp.PhasePlan.Phases[0].Status != session.PlanStatusInProgress {
		t.Fatalf("phase = %#v, want still in_progress after recovery handoff without completed work evidence", gotOp.PhasePlan.Phases[0])
	}
	if !gotOp.Work.LastCompletedAt.IsZero() || !strings.Contains(gotOp.Work.LastError, "token_budget_exhausted") {
		t.Fatalf("operation work = %#v, want recovery error recorded while fresh approval is pending", gotOp.Work)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no silent lease restore prompt", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if hasExecutionEvent(events, core.ExecutionEventRecoveryIssued) {
		t.Fatalf("events = %#v, want no silent lease restoration recovery event", events)
	}
	var boundary session.ExecutionEvent
	for _, event := range events {
		if event.EventType == core.ExecutionEventContinuationBoundaryReached {
			boundary = event
		}
	}
	if boundary.ID == 0 {
		t.Fatalf("events = %#v, want continuation boundary", events)
	}
	payload := executionEventPayload(boundary.PayloadJSON)
	if payloadString(payload, "boundary_reason") != "not_approved" {
		t.Fatalf("boundary payload = %#v, want not_approved after consumed recovery lease", payload)
	}
}

func TestTriggerCodingContinuationBudgetRecoveryDoesNotCompleteOperation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recovery := nativeWorkBudgetRecoveryTestRecovery()
	rt.interactiveDMAssembler = &budgetRecoveryDeliveringAssembler{
		runtime: rt,
		results: []*core.TurnResult{
			{Text: turnBudgetRecoveryHandoffText(recovery) + "\n\nNo edits/commits/pushes completed.", Recovery: recovery},
			{Text: "Recovery re-latched to durable state and narrowed the next action."},
		},
	}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "native"}, []WorkExecutor{nativeWorkExecutor{runtime: rt}})

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	key := session.SessionKey{ChatID: 8196, UserID: 0, Scope: telegramDMScopeRef(8196)}
	leaseID := "lease-phase-budget-recovery-work"
	opState := session.OperationState{
		ID:        "op-budget-recovery-work",
		Objective: "Patch and push the PR branch.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "op-budget-recovery-work-plan",
			Goal:           "Patch and push the PR branch.",
			CurrentPhaseID: "patch-and-push",
			Phases: []session.OperationPhase{{
				ID:               "patch-and-push",
				Summary:          "Patch, validate, commit, and push the PR branch",
				Status:           session.PlanStatusInProgress,
				AuthorityClass:   "commit",
				BoundedEffect:    "Patch the approved PR branch, run focused tests, commit, push, and report evidence.",
				AllowedActions:   []string{"git_commit", "git_push", "run_tests", "report_commit_evidence"},
				ForbiddenActions: []string{"deploy_or_restart", "merge_pull_request"},
				RequiresApproval: true,
				LeaseID:          leaseID,
			}},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	phase := opState.PhasePlan.Phases[0]
	proposalID := operationPhaseProposalID(opState, phase)
	action := session.ActionProposal{
		ID:               "aprop-" + proposalID,
		OperationID:      proposalID,
		Summary:          phase.Summary,
		BoundedEffect:    phase.BoundedEffect,
		RiskClass:        "commit",
		AllowedActions:   phase.AllowedActions,
		ForbiddenActions: phase.ForbiddenActions,
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        expiresAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, 1, now)
	lease.ID = leaseID
	lease.Status = session.ContinuationLeaseStatusActive
	lease.RemainingTurns = 1
	lease.ApprovedBy = 1001
	lease.ApprovedAt = now
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        proposalID,
		Objective:         opState.Objective,
		StageSummary:      action.Summary,
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    action,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v, want automatic budget recovery rollover", err)
	}
	gotOp, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if gotOp.Status == session.OperationStatusCompleted || gotOp.Stage == "completed" {
		t.Fatalf("operation status/stage = %q/%q, want not completed after budget recovery", gotOp.Status, gotOp.Stage)
	}
	if gotOp.PhasePlan.Phases[0].Status != session.PlanStatusInProgress {
		t.Fatalf("phase status = %q, want still in_progress after incomplete work turn", gotOp.PhasePlan.Phases[0].Status)
	}
	if !strings.Contains(gotOp.Work.LastError, "token_budget_exhausted") || !gotOp.Work.LastCompletedAt.IsZero() {
		t.Fatalf("operation work = %#v, want recovery error without completed timestamp", gotOp.Work)
	}
	gotCont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if gotCont.Status != session.ContinuationStatusIdle || gotCont.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed || gotCont.ContinuationLease.RemainingTurns != 0 {
		t.Fatalf("continuation = %#v, want consumed original lease while budget recovery owns rollover", gotCont)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no manual retry approval prompt", inlineCount)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.WaitForBackgroundLoops(waitCtx); err != nil {
		t.Fatalf("WaitForBackgroundLoops() err = %v", err)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 80)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWorkExecutorRecovering) {
		t.Fatalf("events = %#v, want work executor recovering event", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventWorkExecutorFailed) {
		t.Fatalf("events = %#v, want no work executor failure event for scheduled budget recovery", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventWorkExecutorSucceeded) {
		t.Fatalf("events = %#v, want no work executor success event", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventContinuationOffered) {
		t.Fatalf("events = %#v, want no manual retry continuation offer", events)
	}
	assertBudgetRecoveryEventStatus(t, events, "scheduled")
	assertBudgetRecoveryEventStatus(t, events, "resuming")
	assertBudgetRecoveryEventStatus(t, events, "resumed")
	assertNoBudgetRecoveryEventStatus(t, events, "deferred")
	if !budgetRecoveryEventPayloadContains(events, core.ExecutionEventWorkExecutorRecovering, "completion_kind", "native_turn_budget_recovery_scheduled") {
		t.Fatalf("events = %#v, want recovering event with scheduled completion kind", events)
	}
}

func TestTriggerCodingContinuationBudgetRecoveryBlockedDoesNotOfferManualRetry(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recovery := nativeWorkBudgetRecoveryTestRecovery()
	work := &fakeWorkExecutor{
		name:  "native",
		ready: true,
		result: WorkResult{
			ExecutorName:     "native",
			Summary:          "Budget recovery stopped after repeated token exhaustion.",
			Recovery:         recovery,
			RecoveryKind:     string(core.TurnRecoveryTokenBudgetExhausted),
			RecoverySummary:  recovery.Summary,
			RecoveryDelivery: "budget_recovery_blocked",
			CompletionKind:   "native_turn_budget_recovery_blocked",
			SideEffects:      true,
			ProviderFailure:  "",
			ChangedFiles:     nil,
			Commands:         nil,
			CodexEvents:      nil,
			PatchPreview:     "",
			CommitLaneStatus: "",
		},
	}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "native"}, []WorkExecutor{work})

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	key := session.SessionKey{ChatID: 8197, UserID: 0, Scope: telegramDMScopeRef(8197)}
	leaseID := "lease-phase-budget-recovery-blocked"
	opState := session.OperationState{
		ID:        "op-budget-recovery-blocked",
		Objective: "Patch and push the PR branch.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "op-budget-recovery-blocked-plan",
			CurrentPhaseID: "patch-and-push",
			Phases: []session.OperationPhase{{
				ID:               "patch-and-push",
				Summary:          "Patch, validate, commit, and push the PR branch",
				Status:           session.PlanStatusInProgress,
				AuthorityClass:   "commit",
				BoundedEffect:    "Patch the approved PR branch, run focused tests, commit, push, and report evidence.",
				AllowedActions:   []string{"git_commit", "git_push", "run_tests", "report_commit_evidence"},
				ForbiddenActions: []string{"deploy_or_restart", "merge_pull_request"},
				RequiresApproval: true,
				LeaseID:          leaseID,
			}},
		},
	}
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	phase := opState.PhasePlan.Phases[0]
	proposalID := operationPhaseProposalID(opState, phase)
	action := session.ActionProposal{
		ID:               "aprop-" + proposalID,
		OperationID:      proposalID,
		Summary:          phase.Summary,
		BoundedEffect:    phase.BoundedEffect,
		RiskClass:        "commit",
		AllowedActions:   phase.AllowedActions,
		ForbiddenActions: phase.ForbiddenActions,
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        expiresAt,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, 1, now)
	lease.ID = leaseID
	lease.Status = session.ContinuationLeaseStatusActive
	lease.RemainingTurns = 1
	lease.ApprovedBy = 1001
	lease.ApprovedAt = now
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        proposalID,
		Objective:         opState.Objective,
		StageSummary:      action.Summary,
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    action,
		ContinuationLease: lease,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v, want recovery block without callback failure", err)
	}
	gotOp, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if gotOp.Status == session.OperationStatusCompleted || gotOp.PhasePlan.Phases[0].Status == session.PlanStatusCompleted || !gotOp.Work.LastCompletedAt.IsZero() {
		t.Fatalf("operation = %#v, want incomplete operation after max-hop recovery block", gotOp)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no manual retry approval prompt", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventWorkExecutorFailed) {
		t.Fatalf("events = %#v, want work executor recovery_blocked event", events)
	}
	if !budgetRecoveryEventPayloadContains(events, core.ExecutionEventWorkExecutorFailed, "completion_kind", "native_turn_budget_recovery_blocked") {
		t.Fatalf("events = %#v, want blocked completion kind", events)
	}
	if hasExecutionEvent(events, core.ExecutionEventContinuationOffered) {
		t.Fatalf("events = %#v, want no manual retry continuation offer", events)
	}
}

func nativeWorkBudgetRecoveryTestRecovery() *core.TurnRecovery {
	return &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}
}

func TestTriggerCodingContinuationAllowsCompoundWorkspaceRiskClass(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8201, UserID: 0, Scope: telegramDMScopeRef(8201)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "compound-workspace-risk",
		Objective:      "Patch the child bot runner.",
		StageSummary:   "Retry the bounded code/tests lease.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-compound-workspace-risk",
			Summary:        "Retry the bounded code/tests lease.",
			BoundedEffect:  "Inspect/edit repo code and docs, add tests, run local Go tests/build/config checks.",
			RiskClass:      "workspace_write_code_tests_bounded_autoapprove",
			AllowedActions: []string{"execute_bounded_proposal_once", "use_existing_authority_only", "report_evidence"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:compound-workspace-risk",
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-compound-workspace-risk",
			ProposalID:       "aprop-compound-workspace-risk",
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			AllowedActions:   []string{"execute_bounded_proposal_once", "use_existing_authority_only", "report_evidence"},
			ForbiddenActions: []string{"deploy", "restart_service", "commit"},
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:compound-workspace-risk",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-compound-workspace-risk", Objective: "Patch the child bot runner.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8201); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if work.calls != 1 {
		t.Fatalf("work calls = %d, want approved compound workspace risk to run", work.calls)
	}
	if work.lastReq.Mode != WorkModeWorkspaceWrite {
		t.Fatalf("work mode = %q, want workspace_write", work.lastReq.Mode)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusIdle || got.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed {
		t.Fatalf("continuation = %#v, want consumed idle", got)
	}
}

func TestTriggerCodingContinuationWarnsWhenFallingBackToNative(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	codex := &fakeWorkExecutor{name: "codex", ready: false, reason: "app-server unreachable"}
	native := &fakeWorkExecutor{name: "native", ready: true, result: WorkResult{Summary: "native completed"}}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex", "native"}}, []WorkExecutor{codex, native})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8198, UserID: 0, Scope: telegramDMScopeRef(8198)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "work-fallback",
		Objective:      "Run bounded work with fallback.",
		StageSummary:   "Patch code.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-work-fallback",
			Summary:        "Patch code",
			BoundedEffect:  "Patch code under workspace write authority.",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-fallback",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-work-fallback",
			ProposalID:     "aprop-work-fallback",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			AllowedActions: []string{"workspace_write"},
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-fallback",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-work-fallback", Objective: "Run bounded work with fallback.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8198); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if native.calls != 1 {
		t.Fatalf("native calls = %d, want fallback native execution", native.calls)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want one fallback warning", len(sender.sent))
	}
	if got := sender.sent[0].Text; got != "Work executor fallback: codex unavailable; using native." || strings.Contains(got, "\n") {
		t.Fatalf("warning = %q, want one-line work fallback warning", got)
	}
}

func TestTriggerCodingContinuationStoresFullWorkEvidenceArtifact(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	longSummary := "full tool evidence " + strings.Repeat("line-with-important-output ", 120)
	work := &fakeWorkExecutor{name: "codex", ready: true, result: WorkResult{
		Summary:      longSummary,
		ChangedFiles: []string{"runtime/runtime.go"},
		Commands:     []string{"go test ./runtime"},
		PatchPreview: strings.Repeat("+patch\n", 120),
	}}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex", "native"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8199, UserID: 0, Scope: telegramDMScopeRef(8199)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "work-artifact",
		Objective:      "Preserve work evidence.",
		StageSummary:   "Run work and report.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-work-artifact",
			Summary:        "Run work",
			BoundedEffect:  "Run one bounded work turn.",
			RiskClass:      "workspace_write",
			AllowedActions: []string{"workspace_write"},
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-artifact",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-work-artifact",
			ProposalID:     "aprop-work-artifact",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			AllowedActions: []string{"workspace_write"},
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:work-artifact",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-work-artifact", Objective: "Preserve work evidence.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8199); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	op, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if len(op.Artifacts) != 1 || op.Artifacts[0].Label != "Work evidence" {
		t.Fatalf("artifacts = %#v, want one work evidence artifact", op.Artifacts)
	}
	raw, err := os.ReadFile(op.Artifacts[0].Ref)
	if err != nil {
		t.Fatalf("ReadFile(work evidence) err = %v", err)
	}
	if !strings.Contains(string(raw), strings.TrimSpace(longSummary)) || !strings.Contains(string(raw), "## Patch Preview") {
		t.Fatalf("artifact body missing full evidence: %q", string(raw))
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if strings.Contains(sender.sent[0].Text, longSummary) {
		t.Fatalf("telegram text includes untruncated full evidence")
	}
	if !strings.Contains(sender.sent[0].Text, "Full evidence artifact:") || !strings.Contains(sender.sent[0].Text, op.Artifacts[0].Ref) {
		t.Fatalf("telegram text = %q, want artifact reference", sender.sent[0].Text)
	}
}
