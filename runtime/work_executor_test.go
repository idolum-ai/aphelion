//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/runtime/codex"
	"github.com/idolum-ai/aphelion/session"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeWorkExecutor struct {
	name      string
	ready     bool
	reason    string
	err       error
	calls     int
	lastReq   WorkRequest
	lastAvail WorkRequest
	result    WorkResult
	runHook   func(WorkRequest)
}

func (f *fakeWorkExecutor) Name() string {
	if strings.TrimSpace(f.name) == "" {
		return "fake"
	}
	return f.name
}

func (f *fakeWorkExecutor) Available(_ context.Context, req WorkRequest) WorkAvailability {
	f.lastAvail = req
	return WorkAvailability{Available: f.ready, Reason: f.reason}
}

func (f *fakeWorkExecutor) Run(_ context.Context, req WorkRequest) (WorkResult, error) {
	f.calls++
	f.lastReq = req
	if f.runHook != nil {
		f.runHook(req)
	}
	if f.err != nil {
		return WorkResult{}, f.err
	}
	out := f.result
	out.ExecutorName = f.Name()
	if strings.TrimSpace(out.Summary) == "" {
		out.Summary = "work complete"
	}
	return out, nil
}

func TestWorkExecutorSelectorAutoCanPreferCodexAndFallBackNative(t *testing.T) {
	t.Parallel()

	codex := &fakeWorkExecutor{name: "codex", ready: false, reason: "app-server unreachable"}
	native := &fakeWorkExecutor{name: "native", ready: true}
	selector := newWorkExecutorSelector(config.WorkConfig{
		Executor:  "auto",
		AutoOrder: []string{"codex", "native"},
	}, []WorkExecutor{codex, native})

	result, err := selector.Run(context.Background(), WorkRequest{Prompt: "patch the bug", Mode: WorkModeWorkspaceWrite})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if result.ExecutorName != "native" || native.calls != 1 || codex.calls != 0 {
		t.Fatalf("result=%#v codex_calls=%d native_calls=%d, want native fallback only", result, codex.calls, native.calls)
	}
	status := selector.Status()
	if status.Configured != "auto" || status.Active != "native" || status.Preferred != "codex" {
		t.Fatalf("status = %#v, want auto active native preferred codex", status)
	}
	if !strings.Contains(status.FallbackReason, "codex unavailable: app-server unreachable") {
		t.Fatalf("fallback reason = %q, want codex unavailable detail", status.FallbackReason)
	}
}

func TestWorkExecutorSelectorEmptyAutoOrderDefaultsNativeFirst(t *testing.T) {
	t.Parallel()

	codex := &fakeWorkExecutor{name: "codex", ready: true}
	native := &fakeWorkExecutor{name: "native", ready: true}
	selector := newWorkExecutorSelector(config.WorkConfig{Executor: "auto"}, []WorkExecutor{codex, native})

	result, err := selector.Run(context.Background(), WorkRequest{Prompt: "patch the bug", Mode: WorkModeWorkspaceWrite})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if result.ExecutorName != "native" || native.calls != 1 || codex.calls != 0 {
		t.Fatalf("result=%#v codex_calls=%d native_calls=%d, want default native first", result, codex.calls, native.calls)
	}
	status := selector.Status()
	if status.Configured != "auto" || status.Active != "native" || status.Preferred != "native" {
		t.Fatalf("status = %#v, want auto active native preferred native", status)
	}
}

func TestWorkExecutorSelectorStrictCodexDoesNotFallback(t *testing.T) {
	t.Parallel()

	codex := &fakeWorkExecutor{name: "codex", ready: false, reason: "missing address"}
	native := &fakeWorkExecutor{name: "native", ready: true}
	selector := newWorkExecutorSelector(config.WorkConfig{
		Executor:  "codex",
		AutoOrder: []string{"codex", "native"},
	}, []WorkExecutor{codex, native})

	_, err := selector.Run(context.Background(), WorkRequest{Prompt: "patch the bug", Mode: WorkModeWorkspaceWrite})
	if err == nil || !strings.Contains(err.Error(), "codex unavailable: missing address") {
		t.Fatalf("Run() err = %v, want strict codex unavailable error", err)
	}
	if native.calls != 0 {
		t.Fatalf("native calls = %d, want no fallback in strict codex mode", native.calls)
	}
}

func TestWorkExecutorSelectorFallsBackAfterCodexPreEffectFailure(t *testing.T) {
	t.Parallel()

	codex := &fakeWorkExecutor{name: "codex", ready: true, err: errors.New("connect failed")}
	native := &fakeWorkExecutor{name: "native", ready: true}
	selector := newWorkExecutorSelector(config.WorkConfig{
		Executor:  "auto",
		AutoOrder: []string{"codex", "native"},
	}, []WorkExecutor{codex, native})

	result, err := selector.Run(context.Background(), WorkRequest{Prompt: "patch the bug", Mode: WorkModeWorkspaceWrite})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if result.ExecutorName != "native" || codex.calls != 1 || native.calls != 1 {
		t.Fatalf("result=%#v codex_calls=%d native_calls=%d, want native after codex failure", result, codex.calls, native.calls)
	}
	if got := selector.Status().FallbackReason; !strings.Contains(got, "codex failed before side effects") {
		t.Fatalf("fallback reason = %q, want pre-effect failure detail", got)
	}
}

func TestWorkExecutorSelectorFallsBackAfterReadOnlyCodexApprovalFailure(t *testing.T) {
	requireLocalTCPListener(t, "localhost:0")
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		write := func(payload map[string]any) bool {
			raw, err := json.Marshal(payload)
			if err != nil {
				return false
			}
			return conn.Write(context.Background(), websocket.MessageText, raw) == nil
		}
		for {
			_, raw, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(raw, &msg); err != nil {
				return
			}
			id, hasID := msg["id"].(string)
			if !hasID {
				continue
			}
			method, _ := msg["method"].(string)
			switch method {
			case "initialize":
				if !write(map[string]any{"id": id, "result": map[string]any{}}) {
					return
				}
			case "thread/start":
				if !write(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thread-readonly-fallback"}}}) {
					return
				}
			case "turn/start":
				if !write(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-readonly-fallback"}}}) {
					return
				}
				if !write(map[string]any{
					"id":     "approval-readonly",
					"method": "item/commandExecution/requestApproval",
					"params": map[string]any{
						"command": "git status --short",
						"reason":  "inspect worktree",
					},
				}) {
					return
				}
			default:
				if id == "approval-readonly" {
					_ = conn.Close(websocket.StatusInternalError, "simulated upstream read failure")
					return
				}
			}
		}
	}))
	defer server.Close()

	codex := codexWorkExecutor{
		address:                  "ws://" + strings.TrimPrefix(server.URL, "http://"),
		rpcTimeout:               time.Second,
		firstNotificationTimeout: time.Second,
	}
	native := &fakeWorkExecutor{name: "native", ready: true}
	selector := newWorkExecutorSelector(config.WorkConfig{
		Executor:  "auto",
		AutoOrder: []string{"codex", "native"},
	}, []WorkExecutor{codex, native})

	result, err := selector.Run(context.Background(), WorkRequest{Prompt: "inspect status", Mode: WorkModeReadOnly})
	if err != nil {
		t.Fatalf("Run() err = %v, want native fallback after read-only Codex approval failure", err)
	}
	if result.ExecutorName != "native" || native.calls != 1 {
		t.Fatalf("result=%#v native_calls=%d, want native fallback", result, native.calls)
	}
	if got := selector.Status().FallbackReason; !strings.Contains(got, "codex failed before side effects") {
		t.Fatalf("fallback reason = %q, want before-side-effects fallback", got)
	}
}

func TestCodexApprovalLogSideEffectsIgnoreReadOnlyApprovals(t *testing.T) {
	t.Parallel()

	readOnly := []codex.ApprovalDecision{
		{Method: "item/commandExecution/requestApproval", Decision: "accept", Command: "git status --short"},
		{Method: "item/commandExecution/requestApproval", Decision: "accept", Command: "rg doctor runtime"},
	}
	if codex.ApprovalLogHasSideEffects(readOnly) {
		t.Fatalf("read-only approvals were classified as side effects: %#v", readOnly)
	}
	for _, log := range [][]codex.ApprovalDecision{
		{{Method: "item/fileChange/requestApproval", Decision: "accept"}},
		{{Method: "item/commandExecution/requestApproval", Decision: "accept", Command: "apply_patch < patch.diff"}},
		{{Method: "item/commandExecution/requestApproval", Decision: "accept", Command: "git commit -am fix"}},
		{{Method: "item/commandExecution/requestApproval", Decision: "accept", Command: "go test ./runtime"}},
		{{Method: "item/commandExecution/requestApproval", Decision: "accept", Command: "systemctl --user restart aphelion"}},
	} {
		if !codex.ApprovalLogHasSideEffects(log) {
			t.Fatalf("mutating approval was not classified as side effect: %#v", log)
		}
	}
}

func TestCodexWorkReadOnlyModeAllowsOnlyReadOnlyCommandTaxonomy(t *testing.T) {
	t.Parallel()

	req := WorkRequest{Mode: WorkModeReadOnly}
	allowed := []string{
		"git status --short",
		"git diff -- runtime/codex_work_lane.go",
		"rg doctor runtime",
		"sed -n '1,40p' runtime/codex_work_lane.go",
		"hostname",
		"go env GOPATH",
	}
	for _, command := range allowed {
		if !codexWorkCommandAllowed(req, command) {
			t.Fatalf("codexWorkCommandAllowed(read_only %q) = false, want true", command)
		}
	}
	denied := []string{
		"go test ./runtime",
		"go build ./...",
		"npm test",
		"pytest",
		"make build",
		"git fetch origin",
		"git checkout -b branch",
		"curl https://example.com",
		"mkdir out",
		"sed -i s/a/b/ file.txt",
		"sqlite3 state.db 'delete from runs'",
		"systemctl --user restart aphelion",
		"unknown-tool --flag",
	}
	for _, command := range denied {
		if codexWorkCommandAllowed(req, command) {
			t.Fatalf("codexWorkCommandAllowed(read_only %q) = true, want false", command)
		}
	}
}

func TestCodexApprovedCommandSideEffectTaxonomy(t *testing.T) {
	t.Parallel()

	readOnly := []string{
		"git status --short",
		"rg doctor runtime",
		"sed -n '1,40p' runtime/codex_work_lane.go",
		"go list ./runtime",
	}
	for _, command := range readOnly {
		if codex.ApprovedCommandHasSideEffects(command) {
			t.Fatalf("codex.ApprovedCommandHasSideEffects(%q) = true, want false", command)
		}
	}
	mutating := []string{
		"go test ./runtime",
		"go build ./...",
		"npm install",
		"git fetch origin",
		"git commit -am fix",
		"curl https://example.com",
		"mkdir out",
		"cat README.md > out.txt",
		"sqlite3 state.db 'drop table runs'",
		"systemctl --user restart aphelion",
		"unknown-tool --flag",
	}
	for _, command := range mutating {
		if !codex.ApprovedCommandHasSideEffects(command) {
			t.Fatalf("codex.ApprovedCommandHasSideEffects(%q) = false, want true", command)
		}
	}
}

func TestContinuationWorkModeDoesNotPromoteRestartRecoverySmokeTestToDeploy(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Run restart recovery confirmation smoke test",
		ActionProposal: session.ActionProposal{
			Summary:       "Run restart recovery confirmation smoke test",
			BoundedEffect: "Run tests only; do not restart services or deploy.",
			AllowedActions: []string{
				"run_tests",
			},
			ForbiddenActions: []string{
				"restart_service",
				"deploy",
			},
		},
	}

	if got := continuationWorkMode(state); got != WorkModeWorkspaceWrite {
		t.Fatalf("continuationWorkMode() = %q, want %q", got, WorkModeWorkspaceWrite)
	}
}

func TestContinuationWorkModeDoesNotGrantWorkspaceWriteFromProseOnly(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Patch prompt handling and validate it.",
		ActionProposal: session.ActionProposal{
			Summary:       "Patch prompt handling",
			BoundedEffect: "Edit prompt code and run tests; stop before deploy.",
		},
	}

	if got := continuationWorkMode(state); got == WorkModeWorkspaceWrite {
		t.Fatalf("continuationWorkMode() = %q, want prose not to grant workspace_write", got)
	}
}

func TestContinuationWorkModeTrustsExplicitReadOnlyRiskClassOverRestartText(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Review restart recovery status",
		ActionProposal: session.ActionProposal{
			RiskClass:     "read_only_review",
			Summary:       "Review restart recovery status",
			BoundedEffect: "Inspect evidence only; do not restart the service.",
			AllowedActions: []string{
				"inspect_readonly_state",
			},
			ForbiddenActions: []string{
				"restart_service",
			},
		},
	}

	if got := continuationWorkMode(state); got != WorkModeReadOnly {
		t.Fatalf("continuationWorkMode() = %q, want %q", got, WorkModeReadOnly)
	}
}

func TestContinuationWorkModeDoesNotPromoteChildInspectionNegatedDeployText(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Inspect mail-child adapter environment/path metadata.",
		ActionProposal: session.ActionProposal{
			RiskClass: "read_only_child_adapter_environment_inspection",
			Summary:   "Inspect child adapter metadata",
			BoundedEffect: "Read local non-secret child state, adapter config, execution events, binary path metadata, and sanitized command metadata. " +
				"No mailbox content/query, OAuth, file mutation, credential exposure, config edits, deploy, or restart.",
			AllowedActions: []string{
				"inspect_durable_agent_state",
				"inspect_external_channel_adapter_state",
				"inspect_execution_events_for_mailbox_adapter_command",
				"inspect_binary_path_metadata",
				"inspect_nonsecret_environment_metadata",
				"report_mismatch_and_repair_options",
			},
			ForbiddenActions: []string{
				"read_mailbox_contents",
				"deploy",
				"restart",
			},
		},
	}

	if got := continuationWorkMode(state); got == WorkModeDeploy {
		t.Fatalf("continuationWorkMode() = %q, want not deploy for negated deploy text", got)
	}
}

func TestContinuationWorkModeDoesNotPromoteCredentialRecoveryNegatedDeployText(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Repair child-scoped mailbox adapter credential materialization.",
		ActionProposal: session.ActionProposal{
			RiskClass: "credential_recovery",
			Summary:   "Repair child-scoped mailbox adapter credential materialization",
			BoundedEffect: "May create or adjust child-scoped mailbox adapter credential materialization, wrapper/env, or grant contract. " +
				"No mailbox content/label/inbox/message query, no OAuth, no account mutation, no public/external contact, no email actions, no deploy/restart unless separately approved.",
			AllowedActions: []string{
				"create_child_scoped_mailbox_adapter_materialization_if_approved",
				"copy_or_bind_existing_host_mailbox_credentials_without_printing_values",
				"adjust_child_mailbox_adapter_wrapper_or_grant_contract_if_needed",
				"run_child_sandbox_external_account_auth_status_only",
				"report_repair_evidence",
			},
			ForbiddenActions: []string{
				"read_or_print_secret_values",
				"run_mailbox_adapter_query",
				"read_mailbox_contents",
				"deploy",
				"restart",
			},
		},
	}

	if got := continuationWorkMode(state); got == WorkModeDeploy {
		t.Fatalf("continuationWorkMode() = %q, want not deploy for credential-recovery negated deploy text", got)
	}
}

func TestContinuationWorkModeClassifiesExplicitRestartActionAsDeploy(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Restart the service after install",
		ActionProposal: session.ActionProposal{
			RiskClass: "system_change",
			Summary:   "Restart the service after install",
			AllowedActions: []string{
				"restart_service",
			},
		},
	}

	if got := continuationWorkMode(state); got != WorkModeDeploy {
		t.Fatalf("continuationWorkMode() = %q, want %q", got, WorkModeDeploy)
	}
}

func TestWorkPromptForContinuationIncludesOutcomeValidationAndStopRules(t *testing.T) {
	t.Parallel()

	prompt := workPromptForContinuation(session.ContinuationState{
		Objective:    "Repair the live planning lease.",
		StageSummary: "Patch prompt handling and validate it.",
		ActionProposal: session.ActionProposal{
			Summary:       "Patch prompt handling",
			BoundedEffect: "Edit prompt code and run tests; stop before deploy.",
		},
	}, session.OperationState{
		Objective: "Make Aphelion follow GPT-5.5 prompt guidance.",
		PhasePlan: session.OperationPhasePlan{
			ID:             "prompt-guidance-plan",
			CurrentPhaseID: "phase-1",
			Phases: []session.OperationPhase{
				{ID: "phase-1", Summary: "Patch prompts", Status: session.PlanStatusPending},
			},
		},
	})

	for _, want := range []string{
		"Role: You are the bounded work executor",
		"## Goal",
		"Complete only the approved next step",
		"## Success Criteria",
		"Validate meaningful edits",
		"## Constraints",
		"Do not ask for approval to make a plan.",
		"## Stop Rules",
		"Stop before any action outside the lease",
		"Phase [pending] phase-1: Patch prompts",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("work prompt missing %q: %q", want, prompt)
		}
	}
}

func TestWorkPromptForContinuationUsesCurrentApprovedBundlePhase(t *testing.T) {
	t.Parallel()

	prompt := workPromptForContinuation(session.ContinuationState{
		Objective:    "Improve continuation autonomy.",
		StageSummary: "Bundle-level planning summary.",
		ActionProposal: session.ActionProposal{
			Summary:       "Execute the broad approved bundle.",
			BoundedEffect: "Complete the whole bundle.",
		},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             "bundle-loop",
			Status:         session.ContinuationLeaseStatusActive,
			CurrentPhaseID: "phase-b",
			Phases: []session.ContinuationApprovalBundlePhase{
				{ID: "phase-a", OperationPhaseID: "op-phase-a", Summary: "Already consumed", Status: session.ContinuationLeaseStatusConsumed},
				{
					ID:               "phase-b",
					OperationPhaseID: "op-phase-b",
					Summary:          "Patch the loop driver.",
					AuthorityClass:   "local_workspace",
					BoundedEffect:    "Edit runtime loop code and run focused tests.",
					AllowedActions:   []string{"edit_repo_code", "run_go_tests"},
					ForbiddenActions: []string{"deploy", "restart_service"},
					Status:           session.ContinuationLeaseStatusActive,
				},
			},
		},
	}, session.OperationState{})

	for _, want := range []string{
		"Approved bundle phase: op-phase-b",
		"Phase authority class: local_workspace",
		"Next step: Patch the loop driver.",
		"Bounded effect: Edit runtime loop code and run focused tests.",
		"Allowed phase actions: edit_repo_code, run_go_tests",
		"Forbidden phase actions: deploy, restart_service",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("work prompt missing %q: %q", want, prompt)
		}
	}
	if strings.Contains(prompt, "Next step: Execute the broad approved bundle.") {
		t.Fatalf("work prompt used bundle summary instead of current phase: %q", prompt)
	}
}

func TestTriggerContinuationBlocksWorkExecutorWhenLeaseForbidsMode(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8187, UserID: 0, Scope: telegramDMScopeRef(8187)}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "work-lane-forbidden",
		Objective:      "Patch the work lane.",
		StageSummary:   "Edit runtime work executor files and test.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:            "aprop-work-lane-forbidden",
			Summary:       "Patch work executor",
			BoundedEffect: "Edit runtime work executor files and run focused tests.",
			RiskClass:     "workspace_write",
			Status:        session.ProposalStatusApproved,
			ExpiresAt:     expiresAt,
			PlanHash:      "sha256:work-lane-forbidden",
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-work-lane-forbidden",
			ProposalID:       "aprop-work-lane-forbidden",
			Status:           session.ContinuationLeaseStatusActive,
			MaxTurns:         1,
			RemainingTurns:   1,
			AllowedActions:   []string{"read_only"},
			ForbiddenActions: []string{"workspace_write"},
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:work-lane-forbidden",
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuation(context.Background(), 8187); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if work.calls != 0 {
		t.Fatalf("work calls = %d, want lease access denial before executor", work.calls)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusRevoked || got.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked {
		t.Fatalf("continuation = %#v, want revoked after lease action denial", got)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no repair approval prompt for action_forbidden", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	foundDenied := false
	for _, event := range events {
		if event.EventType != core.ExecutionEventContinuationBlocked {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("decode blocked payload %q: %v", event.PayloadJSON, err)
		}
		if payloadString(payload, "reason") == "lease_action_denied" && payloadString(payload, "lease_access_reason") == "action_forbidden" {
			foundDenied = true
			break
		}
	}
	if !foundDenied {
		t.Fatalf("events missing lease_action_denied block: %#v", events)
	}
}

func TestContinuationCommitModeAllowsSpecificLiveConfigForbiddenAction(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	action := session.ActionProposal{
		ID:             "aprop-local-commit-with-live-config-forbidden",
		Summary:        "Commit validated local repo slices",
		BoundedEffect:  "Review current dirty diff, run tests, commit coherent repo-only hardening, and report evidence.",
		RiskClass:      "workspace_commit_then_repo_write_bounded",
		AllowedActions: []string{"git_status", "git_diff", "run_go_tests", "git_commit_validated_slices", "edit_repo_code"},
		ForbiddenActions: []string{
			"patch_live_aphelion_toml",
			"restart_aphelion",
			"deploy_or_enable_systemd",
			"git_push",
		},
		Status:    session.ProposalStatusApproved,
		ExpiresAt: now.Add(time.Hour),
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

	if state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassLocalWorkspace {
		t.Fatalf("lease class = %q, want local workspace for explicit local commit lease", state.ContinuationLease.LeaseClass)
	}
	mode := continuationWorkMode(state)
	if mode != WorkModeCommit {
		t.Fatalf("continuationWorkMode() = %q, want commit", mode)
	}
	decision := continuationWorkModeAccessCheck(state, mode, now)
	if !decision.Allowed || (decision.Reason != "allowed" && decision.Reason != "allowed_by_structured_authority") {
		t.Fatalf("access decision = %#v, want commit allowed by explicit structured authority", decision)
	}
}
