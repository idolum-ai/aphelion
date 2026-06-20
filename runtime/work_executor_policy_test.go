//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestContinuationCommitModeStillBlocksBroadCommitForbiddenAction(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	action := session.ActionProposal{
		ID:               "aprop-local-commit-forbidden",
		Summary:          "Commit validated local repo slices",
		BoundedEffect:    "Review current dirty diff, run tests, commit coherent repo-only hardening, and report evidence.",
		RiskClass:        "workspace_commit_then_repo_write_bounded",
		AllowedActions:   []string{"git_commit_validated_slices", "edit_repo_code"},
		ForbiddenActions: []string{"commit"},
		Status:           session.ProposalStatusApproved,
		ExpiresAt:        now.Add(time.Hour),
	}
	action.PlanHash = actionProposalHash(action)
	state := session.ContinuationState{
		Status:            session.ContinuationStatusApproved,
		RemainingTurns:    1,
		ActionProposal:    action,
		ContinuationLease: buildContinuationLease(action, 1, now),
	}
	state.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	state.ContinuationLease.RemainingTurns = 1
	state.ContinuationLease.ApprovedAt = now
	state.ContinuationLease.ApprovedBy = 1001

	mode := continuationWorkMode(state)
	if mode != WorkModeCommit {
		t.Fatalf("continuationWorkMode() = %q, want commit", mode)
	}
	decision := continuationWorkModeAccessCheck(state, mode, now)
	if decision.Allowed || decision.Reason != "action_forbidden" {
		t.Fatalf("access decision = %#v, want broad commit forbidden", decision)
	}
}

func TestNativeWorkResultClassifiesProviderFailure(t *testing.T) {
	t.Parallel()

	result := nativeWorkResultFromTurnResult(&core.TurnResult{
		Text:            "Inference backend failed before provider fallback was applicable. This turn did not complete.",
		ProviderFailure: "codex: stream failed: request error",
		ProviderEvents: []core.ProviderEvent{
			{EventType: "provider.error", Provider: "codex", Error: "stream failed", PartialToolCalls: 1},
		},
	})

	if result.CompletionKind != "native_turn_provider_failed" || result.ProviderFailure == "" || !result.SideEffects {
		t.Fatalf("result = %#v, want failed native turn marked with provider failure and side effects", result)
	}
	if len(result.ProviderEvents) != 1 || result.ProviderEvents[0].PartialToolCalls != 1 {
		t.Fatalf("provider events = %#v, want captured provider event evidence", result.ProviderEvents)
	}
	err := nativeWorkResultTerminalError(result)
	if err == nil || !strings.Contains(err.Error(), "inference backend failed") {
		t.Fatalf("nativeWorkResultTerminalError() err = %v, want provider failure error", err)
	}
	var providerErr nativeWorkProviderFailureError
	if !errors.As(err, &providerErr) {
		t.Fatalf("nativeWorkResultTerminalError() err = %T, want nativeWorkProviderFailureError", err)
	}
}

func TestNativeWorkResultClassifiesBudgetRecovery(t *testing.T) {
	t.Parallel()

	recovery := nativeWorkBudgetRecoveryTestRecovery()
	result := nativeWorkResultFromTurnResult(&core.TurnResult{
		Text: turnBudgetRecoveryHandoffText(recovery) + "\n\nNo edits/commits/pushes completed.",
	})

	if result.CompletionKind != "native_turn_budget_recovery" || result.RecoveryKind != string(core.TurnRecoveryTokenBudgetExhausted) || !result.SideEffects {
		t.Fatalf("result = %#v, want failed native turn marked with budget recovery and side effects", result)
	}
	if !strings.Contains(result.Summary, "No edits/commits/pushes completed") {
		t.Fatalf("summary = %q, want recovery handoff evidence preserved", result.Summary)
	}
	err := nativeWorkResultTerminalError(result)
	if err == nil || !strings.Contains(err.Error(), "token_budget_exhausted") {
		t.Fatalf("nativeWorkResultTerminalError() err = %v, want budget recovery work failure", err)
	}
	var recoveryErr nativeWorkRecoveryError
	if !errors.As(err, &recoveryErr) {
		t.Fatalf("nativeWorkResultTerminalError() err = %T, want nativeWorkRecoveryError", err)
	}
}

func TestWorkResultHasSubstantiveCompletionEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result WorkResult
		want   bool
	}{
		{name: "empty", result: WorkResult{}, want: false},
		{name: "summary", result: WorkResult{Summary: "patched tests"}, want: true},
		{name: "changed files", result: WorkResult{ChangedFiles: []string{"runtime/work_executor.go"}}, want: true},
		{name: "commands", result: WorkResult{Commands: []string{"go test ./runtime"}}, want: true},
		{name: "codex events", result: WorkResult{CodexEvents: []session.WorkCodexEvent{{Kind: "file_change"}}}, want: true},
		{name: "patch preview", result: WorkResult{PatchPreview: "@@ patched"}, want: true},
		{name: "commit lane", result: WorkResult{CommitLaneStatus: "commit_requires_separate_lease"}, want: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := workResultHasSubstantiveCompletionEvidence(tt.result); got != tt.want {
				t.Fatalf("workResultHasSubstantiveCompletionEvidence() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkResultCompletionEvidenceForRequestRequiresMaterialEvidenceForAuthorityWork(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	readOnlyReq := WorkRequest{
		Mode: WorkModeReadOnly,
		State: session.ContinuationState{
			ActionProposal: session.ActionProposal{RiskClass: "read_only_review", Status: session.ProposalStatusApproved},
			ContinuationLease: session.ContinuationLease{
				ID:             "lease-read-only",
				Status:         session.ContinuationLeaseStatusActive,
				AllowedActions: []string{"read_only"},
				ExpiresAt:      now.Add(time.Hour),
			},
		},
	}
	if !workResultHasSubstantiveCompletionEvidenceForRequest(readOnlyReq, WorkResult{Summary: "Read-only inspection completed."}) {
		t.Fatal("read-only summary-only result should remain valid completion evidence")
	}

	writeReq := readOnlyReq
	writeReq.Mode = WorkModeWorkspaceWrite
	writeReq.State.ActionProposal.RiskClass = "workspace_write"
	writeReq.State.ContinuationLease.AllowedActions = []string{"workspace_write", "edit_files"}
	if workResultHasSubstantiveCompletionEvidenceForRequest(writeReq, WorkResult{Summary: "Patched it."}) {
		t.Fatal("workspace-write summary-only result completed, want material evidence required")
	}
	if !workResultHasSubstantiveCompletionEvidenceForRequest(writeReq, WorkResult{Commands: []string{"sed -i 's/a/b/' runtime/example.go"}}) {
		t.Fatal("workspace-write mutating command should count as material completion evidence")
	}

	commitReq := readOnlyReq
	commitReq.Mode = WorkModeCommit
	commitReq.State.ActionProposal.RiskClass = "commit"
	commitReq.State.ContinuationLease.AllowedActions = []string{"git_commit", "report_commit_evidence"}
	plainCommit := `git commit -m "Add XPVENTA reconstruction packet artifacts"`
	if !workResultHasSubstantiveCompletionEvidenceForRequest(commitReq, WorkResult{Commands: []string{plainCommit}}) {
		t.Fatal("commit-mode git commit command should count as material completion evidence")
	}
	compoundCommit := `set -euo pipefail
git commit -m "Add XPVENTA reconstruction packet artifacts" >/tmp/imexx_commit.out
cat /tmp/imexx_commit.out
printf '\nCOMMIT\n'; git rev-parse --short HEAD`
	if workResultHasSubstantiveCompletionEvidenceForRequest(commitReq, WorkResult{Commands: []string{compoundCommit}}) {
		t.Fatal("commit-mode redirected shell script completed from result evidence, want effect-plan/verification path")
	}

	grantReq := readOnlyReq
	grantReq.Mode = WorkModeReadOnly
	grantReq.State.ActionProposal.RiskClass = "external_account_pr_create"
	grantReq.State.ActionProposal.AllowedActions = []string{"github_pr_create", "report_pr_link"}
	grantReq.State.ContinuationLease.LeaseClass = session.ContinuationLeaseClassCapabilityGrant
	grantReq.State.ContinuationLease.RequiredCapabilityGrants = []session.CapabilityGrantSpec{{
		RequestID:      "cap-release-pr",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github",
		GrantedTo:      "telegram:1001",
		AllowedActions: []string{"write"},
	}}
	if workResultHasSubstantiveCompletionEvidenceForRequest(grantReq, WorkResult{Summary: "I opened the PR."}) {
		t.Fatal("external-account summary-only result completed, want material evidence required")
	}
	if workResultHasSubstantiveCompletionEvidenceForRequest(grantReq, WorkResult{Commands: []string{"gh auth status"}}) {
		t.Fatal("external-account status check completed mutation phase, want actual external-account effect evidence")
	}
	if !workResultHasSubstantiveCompletionEvidenceForRequest(grantReq, WorkResult{Commands: []string{"gh pr create --base release/v0.2.5 --head main --fill"}}) {
		t.Fatal("successful GitHub PR creation command should count as material external-account evidence")
	}
	if !workResultHasSubstantiveCompletionEvidenceForRequest(grantReq, WorkResult{
		Commands:      []string{"gh pr create --base release/v0.2.5 --head main --fill"},
		ToolSuccesses: 1,
		ToolFailures:  1,
		ToolFailure:   "exec failed: ls missing-follow-up",
	}) {
		t.Fatal("successful material action plus incidental later failure should remain completion evidence")
	}
	if workResultHasSubstantiveCompletionEvidenceForRequest(grantReq, WorkResult{
		Commands:         []string{"gh pr create --base release/v0.2.5 --head main --fill"},
		ToolSuccesses:    1,
		ToolFailures:     2,
		ToolFailure:      "exec failed: ls missing-follow-up",
		ToolFailureTexts: []string{"exec failed: ls missing-follow-up", "AUTHORITY_REJECTED: AskForGrant"},
	}) {
		t.Fatal("later authority rejection should invalidate material completion even when first failure was incidental")
	}
	if workResultHasSubstantiveCompletionEvidenceForRequest(grantReq, WorkResult{
		Summary:      "I drafted the PR body but could not create the PR.",
		Commands:     []string{"gh pr create --base release/v0.2.5 --head main --fill"},
		ToolFailures: 1,
		ToolFailure:  "AUTHORITY_REJECTED: AskForGrant",
	}) {
		t.Fatal("failed authority/tool evidence completed external-account phase, want blocked/retry path")
	}
}

func TestWorkOutcomeReconciliationBlocksUnverifiedExternalAccountSideEffects(t *testing.T) {
	t.Parallel()

	req := WorkRequest{
		Mode:    WorkModeReadOnly,
		Workdir: t.TempDir(),
		State: session.ContinuationState{
			ActionProposal: session.ActionProposal{
				RiskClass:      "external_account_pr_create",
				AllowedActions: []string{"github_pr_create", "report_pr_link"},
				Status:         session.ProposalStatusApproved,
			},
			ContinuationLease: session.ContinuationLease{
				ID:         "lease-external-unverified",
				Status:     session.ContinuationLeaseStatusActive,
				LeaseClass: session.ContinuationLeaseClassCapabilityGrant,
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					RequestID:      "cap-release-pr",
					Kind:           session.CapabilityKindExternalAccount,
					TargetResource: "github",
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"write"},
				}},
				ExpiresAt: time.Now().UTC().Add(time.Hour),
			},
		},
	}
	result := WorkResult{
		Summary:       "A GitHub wrapper ran, but no PR URL or typed external-account evidence was captured.",
		Commands:      []string{"custom-gh-wrapper --create-pr"},
		SideEffects:   true,
		ToolSuccesses: 1,
	}

	now := time.Now().UTC()
	got, decision := (*Runtime)(nil).resolveWorkOutcomeAfterMissingEvidence(context.Background(), session.SessionKey{ChatID: 1}, req, result, now, now)
	if decision.Kind != workOutcomeResolutionBlockedUnverified || !decision.blocksRetry() || !errors.Is(decision.Err, errWorkExecutorOutcomeUnverified) {
		t.Fatalf("resolution decision = %#v result=%#v, want blocked unverified outcome", decision, got)
	}
}

func TestRecordWorkResultEffectAttemptRefusesFirstWriteAfterResult(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7003, UserID: 0, Scope: telegramDMScopeRef(7003)}
	now := time.Now().UTC()
	req := WorkRequest{
		OperationID: "op-effect-commit",
		Mode:        WorkModeCommit,
		LeaseID:     "lease-effect-commit",
		Key:         key,
		State: session.ContinuationState{
			ActionProposal: session.ActionProposal{ID: "aprop-effect-commit", RiskClass: "commit"},
		},
		Operation: session.OperationState{
			ID: "op-effect-commit",
			PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{{
				ID:      "phase-commit",
				LeaseID: "lease-effect-commit",
			}}},
		},
	}
	command := `git commit -m "Add evidence"`
	attempts, err := rt.recordWorkResultEffectAttempts(key, req, WorkResult{ExecutorName: "native", Commands: []string{command}}, nil, now, now.Add(time.Second))
	if err == nil {
		t.Fatalf("recordWorkResultEffectAttempts() err = nil attempts=%#v, want missing pre-dispatch attempt rejection", attempts)
	}
	if len(attempts) != 0 {
		t.Fatalf("attempts = %#v, want no first write after result", attempts)
	}
}

func TestExecEffectAttemptIsWrittenAheadOnToolStart(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7004, UserID: 0, Scope: telegramDMScopeRef(7004)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "run command", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	defer monitor.Finish(context.Background(), context.Canceled)

	command := "git push origin main"
	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(fmt.Sprintf(`{"cmd":%q}`, command)))

	attempts, err := store.EffectAttemptsByTurnRun(key, monitor.runID)
	if err != nil {
		t.Fatalf("EffectAttemptsByTurnRun() err = %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want one write-ahead attempt", attempts)
	}
	if attempts[0].Status != session.EffectAttemptStatusAttempted || attempts[0].CompletedAt.IsZero() == false {
		t.Fatalf("attempt = %#v, want attempted without completion", attempts[0])
	}
}

func TestWorkResultEffectAttemptConvergesWithMonitorStartRow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7005, UserID: 0, Scope: telegramDMScopeRef(7005)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "run command", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	defer monitor.Finish(context.Background(), nil)

	command := "gh pr create --base main --head fix/effect-attempt-ledger --fill"
	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(fmt.Sprintf(`{"command":%q}`, command)))
	before, err := store.EffectAttemptsByTurnRun(key, monitor.runID)
	if err != nil {
		t.Fatalf("EffectAttemptsByTurnRun(before) err = %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("before attempts = %#v, want one monitor attempt", before)
	}

	req := WorkRequest{
		OperationID: "op-effect-converge",
		Mode:        WorkModeCommit,
		LeaseID:     "lease-effect-converge",
		Key:         key,
		State: session.ContinuationState{
			ActionProposal: session.ActionProposal{ID: "aprop-effect-converge", RiskClass: "commit"},
		},
		Operation: session.OperationState{
			ID: "op-effect-converge",
			PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{{
				ID:      "phase-effect-converge",
				LeaseID: "lease-effect-converge",
			}}},
		},
	}
	now := time.Now().UTC()
	attempts, err := rt.recordWorkResultEffectAttempts(key, req, WorkResult{ExecutorName: "native", TurnRunID: monitor.runID, Commands: []string{command}}, nil, now, now.Add(time.Second))
	if err != nil {
		t.Fatalf("recordWorkResultEffectAttempts() err = %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want one projected attempt", attempts)
	}
	if attempts[0].AttemptID != before[0].AttemptID {
		t.Fatalf("attempt id = %s, want monitor id %s", attempts[0].AttemptID, before[0].AttemptID)
	}
	after, err := store.EffectAttemptsByTurnRun(key, monitor.runID)
	if err != nil {
		t.Fatalf("EffectAttemptsByTurnRun(after) err = %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("after attempts = %#v, want one converged row", after)
	}
	if after[0].OperationID != "op-effect-converge" || after[0].PhaseID != "phase-effect-converge" || after[0].Status != session.EffectAttemptStatusExecuted {
		t.Fatalf("after attempt = %#v, want work metadata and executed status", after[0])
	}
}

func TestWorkResultEffectAttemptsAdvanceDuplicateCommandOccurrences(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 7006, UserID: 0, Scope: telegramDMScopeRef(7006)}
	req := WorkRequest{
		OperationID: "op-effect-duplicates",
		Mode:        WorkModeWorkspaceWrite,
		LeaseID:     "lease-effect-duplicates",
		Key:         key,
		State: session.ContinuationState{
			ActionProposal: session.ActionProposal{ID: "aprop-effect-duplicates", RiskClass: "workspace_write"},
		},
		Operation: session.OperationState{
			ID: "op-effect-duplicates",
			PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{{
				ID:      "phase-effect-duplicates",
				LeaseID: "lease-effect-duplicates",
			}}},
		},
	}
	command := "touch generated.txt"
	now := time.Now().UTC()
	for i, id := range []string{"eff-duplicate-1", "eff-duplicate-2"} {
		if _, err := store.UpsertEffectAttempt(session.EffectAttemptInput{
			AttemptID:    id,
			Key:          key,
			OperationID:  req.OperationID,
			PhaseID:      "phase-effect-duplicates",
			LeaseID:      req.LeaseID,
			ProposalID:   req.State.ActionProposal.ID,
			WorkMode:     string(req.Mode),
			Executor:     "codex",
			Tool:         "codex_command_approval",
			Command:      command,
			EffectKind:   "workspace_mutation",
			EffectReason: "touch filesystem mutation",
			Status:       session.EffectAttemptStatusAttempted,
			StartedAt:    now.Add(time.Duration(i) * time.Second),
			UpdatedAt:    now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("UpsertEffectAttempt(%s) err = %v", id, err)
		}
	}
	attempts, err := rt.recordWorkResultEffectAttempts(key, req, WorkResult{ExecutorName: "codex", Commands: []string{command, command}}, nil, now, now.Add(time.Second))
	if err != nil {
		t.Fatalf("recordWorkResultEffectAttempts() err = %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %#v, want both duplicate command occurrences advanced", attempts)
	}
	if attempts[0].AttemptID != "eff-duplicate-1" || attempts[1].AttemptID != "eff-duplicate-2" {
		t.Fatalf("attempt ids = %q, %q; want FIFO by started_at", attempts[0].AttemptID, attempts[1].AttemptID)
	}
	got, err := store.EffectAttemptsForWork(key, req.OperationID, "phase-effect-duplicates", req.LeaseID, req.State.ActionProposal.ID)
	if err != nil {
		t.Fatalf("EffectAttemptsForWork() err = %v", err)
	}
	for _, attempt := range got {
		if attempt.Status != session.EffectAttemptStatusExecuted {
			t.Fatalf("attempt = %#v, want every duplicate attempt executed", attempt)
		}
	}
}

func TestConsumedWorkPhaseDoesNotCompleteWithoutMatchingWorkEvidence(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	opState := session.OperationState{
		ID:        "op-missing-work-evidence",
		Objective: "Patch and push a branch.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-missing-work-evidence",
			CurrentPhaseID: "commit-push",
			Phases: []session.OperationPhase{{
				ID:             "commit-push",
				Summary:        "Commit and push inspected changes",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "commit",
				BoundedEffect:  "Commit and push only the approved branch changes.",
				AllowedActions: []string{"git_commit", "git_push", "report_commit_evidence"},
				LeaseID:        "lease-commit-push",
			}},
		},
		Work: session.WorkOperationMetadata{
			LastOperationID:       "op-missing-work-evidence",
			LastLeaseID:           "different-lease",
			LastWorkMode:          string(WorkModeCommit),
			LastCompletedAt:       now,
			LastExecutorUpdatedAt: now,
		},
	}
	proposalID := operationPhaseProposalID(opState, opState.PhasePlan.Phases[0])
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusIdle,
		DecisionID:     proposalID,
		Objective:      opState.Objective,
		StageSummary:   "Commit and push inspected changes",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:          "aprop-" + proposalID,
			OperationID: proposalID,
			RiskClass:   "commit",
			Status:      session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-commit-push",
			ProposalID:     "aprop-" + proposalID,
			Status:         session.ContinuationLeaseStatusConsumed,
			MaxTurns:       1,
			RemainingTurns: 0,
			AllowedActions: []string{"git_commit", "git_push", "report_commit_evidence"},
			ConsumedAt:     now,
		},
	}

	got, completed := operationStateWithConsumedWorkContinuationPhaseCompleted(opState, state, now)
	if completed {
		t.Fatalf("completed = true, want false without matching work evidence")
	}
	if got.PhasePlan.Phases[0].Status != session.PlanStatusInProgress {
		t.Fatalf("phase status = %q, want in_progress", got.PhasePlan.Phases[0].Status)
	}
}

func TestConsumedWorkPhaseUsesCompletedBundlePhaseModeEvidence(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	opState := session.OperationState{
		ID:        "op-mixed-authority-bundle",
		Objective: "Inspect the branch, then commit the accepted patch.",
		Status:    session.OperationStatusActive,
		Stage:     "phase_approval",
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-mixed-authority-bundle",
			CurrentPhaseID: "inspect",
			Phases: []session.OperationPhase{
				{
					ID:             "inspect",
					Summary:        "Inspect the branch diff",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "read_only",
					BoundedEffect:  "Read the branch diff and report findings.",
					AllowedActions: []string{"inspect_code", "report_findings"},
					LeaseID:        "lease-mixed-authority-bundle",
				},
				{
					ID:             "commit",
					Summary:        "Commit the accepted patch",
					Status:         session.PlanStatusPending,
					AuthorityClass: "commit",
					BoundedEffect:  "Commit and push only the accepted patch.",
					AllowedActions: []string{"git_commit", "git_push", "report_commit_evidence"},
					LeaseID:        "lease-mixed-authority-bundle",
				},
			},
		},
	}
	inspectProposalID := operationPhaseProposalID(opState, opState.PhasePlan.Phases[0])
	commitProposalID := operationPhaseProposalID(opState, opState.PhasePlan.Phases[1])
	opState.Work = session.WorkOperationMetadata{
		LastOperationID:       "op-mixed-authority-bundle",
		LastActionProposalID:  "aprop-mixed-authority-bundle",
		LastActionOperationID: inspectProposalID,
		LastLeaseID:           "lease-mixed-authority-bundle",
		LastWorkMode:          string(WorkModeReadOnly),
		LastCompletedAt:       now,
		LastExecutorUpdatedAt: now,
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusIdle,
		DecisionID:     "mixed-authority-bundle",
		Objective:      opState.Objective,
		StageSummary:   "Inspect the branch diff, then commit the accepted patch",
		RemainingTurns: 0,
		ActionProposal: session.ActionProposal{
			ID:          "aprop-mixed-authority-bundle",
			OperationID: "mixed-authority-bundle",
			RiskClass:   "approval_bundle",
			Status:      session.ProposalStatusApproved,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-mixed-authority-bundle",
			ProposalID:     "aprop-mixed-authority-bundle",
			Status:         session.ContinuationLeaseStatusConsumed,
			MaxTurns:       1,
			RemainingTurns: 0,
			AllowedActions: []string{"inspect_code", "git_commit", "git_push", "report_commit_evidence"},
			ConsumedAt:     now,
		},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             "mixed-authority-bundle",
			OperationID:    "op-mixed-authority-bundle",
			PhasePlanID:    "plan-mixed-authority-bundle",
			Status:         session.ContinuationLeaseStatusActive,
			CurrentPhaseID: commitProposalID,
			Phases: []session.ContinuationApprovalBundlePhase{
				{
					ID:               inspectProposalID,
					OperationPhaseID: "inspect",
					Status:           session.ContinuationLeaseStatusConsumed,
					AuthorityClass:   "read_only",
					AllowedActions:   []string{"inspect_code", "report_findings"},
					ConsumedAt:       now,
				},
				{
					ID:               commitProposalID,
					OperationPhaseID: "commit",
					Status:           session.ContinuationLeaseStatusActive,
					AuthorityClass:   "commit",
					AllowedActions:   []string{"git_commit", "git_push", "report_commit_evidence"},
					ActivatedAt:      now,
				},
			},
		},
	}

	got, completed := operationStateWithConsumedWorkContinuationPhaseCompleted(opState, state, now)
	if !completed {
		t.Fatalf("completed = false, want true for matching consumed read_only bundle phase evidence")
	}
	if got.PhasePlan.Phases[0].Status != session.PlanStatusCompleted {
		t.Fatalf("first phase status = %q, want completed", got.PhasePlan.Phases[0].Status)
	}
	if got.PhasePlan.Phases[1].Status == session.PlanStatusCompleted {
		t.Fatalf("second phase status = %q, want not completed by first phase evidence", got.PhasePlan.Phases[1].Status)
	}
}
