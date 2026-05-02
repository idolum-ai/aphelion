//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
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

func TestWorkExecutorSelectorDefaultsToCodexAndFallsBackNative(t *testing.T) {
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
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "patched tests") || !strings.Contains(sender.sent[0].Text, "runtime/work_executor.go") {
		t.Fatalf("sent = %#v, want visible work executor summary", sender.sent)
	}
}

func TestDoctorRuntimeConfigReportsWorkExecutorStatus(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Work = config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex", "native"}, Codex: config.WorkCodexConfig{AppServerAddress: "ws://127.0.0.1:3333"}}
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.workExecutor = newWorkExecutorSelector(cfg.Work, []WorkExecutor{
		&fakeWorkExecutor{name: "codex", ready: false, reason: "app-server unreachable"},
		&fakeWorkExecutor{name: "native", ready: true},
	})
	_, _ = rt.workExecutor.Run(context.Background(), WorkRequest{Prompt: "trigger status", Mode: WorkModeReadOnly})

	var b strings.Builder
	rt.writeDoctorRuntimeConfig(&b, rt.executionForTurn(testPreparedContract("doctor")), mustTestScope(t, rt, principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}))
	report := b.String()
	for _, want := range []string{
		`work_executor_configured="auto"`,
		`work_executor_preferred="codex"`,
		`work_executor_active="native"`,
		`codex_work_app_server="ws://127.0.0.1:3333"`,
		`work_executor_fallback_reason="codex unavailable: app-server unreachable"`,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("doctor runtime report missing %s:\n%s", want, report)
		}
	}
}

func testPreparedContract(text string) pipeline.TurnPrepareContract {
	return pipeline.TurnPrepareContract{UserText: text, LedgerText: text}
}

func mustTestScope(t *testing.T, rt *Runtime, p principal.Principal) sandbox.Scope {
	t.Helper()
	scope, err := rt.scopeForPrincipal(p)
	if err != nil {
		t.Fatalf("scopeForPrincipal() err = %v", err)
	}
	return scope
}
