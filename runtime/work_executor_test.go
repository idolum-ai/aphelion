//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	if len(op.Work.CodexEvents) != 2 || op.Work.CodexEvents[0].Kind != "file_change" || op.Work.PatchPreview != "@@ patched" || op.Work.CommitLaneStatus != "commit_requires_separate_lease" {
		t.Fatalf("operation codex work metadata = %#v, want captured Codex interface evidence", op.Work)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || !strings.Contains(sender.sent[0].Text, "patched tests") || !strings.Contains(sender.sent[0].Text, "runtime/work_executor.go") || !strings.Contains(sender.sent[0].Text, "commit_requires_separate_lease") {
		t.Fatalf("sent = %#v, want visible work executor summary", sender.sent)
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

func TestCodexWorkEventFromNotificationCapturesCoreInterfaces(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		method  string
		params  map[string]any
		kind    string
		subject string
	}{
		{
			name:    "file change",
			method:  "item/fileChange/completed",
			params:  map[string]any{"path": "runtime/work_executor.go", "diff": "@@ changed", "status": "completed"},
			kind:    "file_change",
			subject: "runtime/work_executor.go",
		},
		{
			name:    "command",
			method:  "item/commandExecution/completed",
			params:  map[string]any{"command": "go test ./runtime", "exitCode": 0, "status": "completed"},
			kind:    "command",
			subject: "go test ./runtime",
		},
		{
			name:    "user input",
			method:  "tool/requestUserInput",
			params:  map[string]any{"prompt": "Pick the next test"},
			kind:    "user_input",
			subject: "Pick the next test",
		},
		{
			name:    "subagent",
			method:  "agent/spawned",
			params:  map[string]any{"agentId": "agent-1", "name": "reviewer"},
			kind:    "subagent",
			subject: "agent-1",
		},
		{
			name:    "mcp",
			method:  "mcp/tool/called",
			params:  map[string]any{"server": "github", "tool": "pull_request_read"},
			kind:    "mcp",
			subject: "github/pull_request_read",
		},
		{
			name:    "auto review",
			method:  "autoReview/completed",
			params:  map[string]any{"summary": "needs tests", "status": "completed"},
			kind:    "auto_review",
			subject: "needs tests",
		},
		{
			name:    "rollout history",
			method:  "rollout/history/synced",
			params:  map[string]any{"threadId": "thread-1", "turnId": "turn-1"},
			kind:    "rollout_history",
			subject: "thread-1/turn-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, ok := codexWorkEventFromNotification(tt.method, tt.params)
			if !ok {
				t.Fatalf("codexWorkEventFromNotification(%q) ok=false", tt.method)
			}
			if event.Kind != tt.kind || event.Subject != tt.subject {
				t.Fatalf("event = %#v, want kind=%q subject=%q", event, tt.kind, tt.subject)
			}
		})
	}
}

func TestCodexAppServerClientRecordsServerRequestEvents(t *testing.T) {
	t.Parallel()

	client := newCodexAppServerClient("ws://127.0.0.1:1", codexWorkApprovalHandler(WorkRequest{Mode: WorkModeWorkspaceWrite}))
	response := client.handleServerRequest("tool/requestUserInput", map[string]any{"prompt": "Pick a branch", "status": "pending"})
	if len(response) != 0 {
		t.Fatalf("response = %#v, want empty safe response for unsupported user input request", response)
	}
	events := client.WorkEvents()
	if len(events) != 1 || events[0].Kind != "user_input" || events[0].Subject != "Pick a branch" {
		t.Fatalf("work events = %#v, want user_input request event", events)
	}
	log := client.ApprovalLog()
	if len(log) != 1 || log[0].Method != "tool/requestUserInput" || log[0].Decision != "cancel" {
		t.Fatalf("approval log = %#v, want canceled user input request recorded", log)
	}
}

func TestCodexWorkExecutorReadinessUsesHealthz(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, "upgrade required", http.StatusBadRequest)
	}))
	defer server.Close()

	address := "ws://" + strings.TrimPrefix(server.URL, "http://")
	if err := checkCodexWorkAppServerReady(context.Background(), address); err != nil {
		t.Fatalf("checkCodexWorkAppServerReady() err = %v", err)
	}
	if len(paths) != 1 || paths[0] != "/healthz" {
		t.Fatalf("probed paths = %#v, want only /healthz", paths)
	}
}

func TestCodexWorkExecutorReadinessFallsBackToHealth(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	address := "ws://" + strings.TrimPrefix(server.URL, "http://")
	if err := checkCodexWorkAppServerReady(context.Background(), address); err != nil {
		t.Fatalf("checkCodexWorkAppServerReady() err = %v", err)
	}
	if len(paths) != 2 || paths[0] != "/healthz" || paths[1] != "/health" {
		t.Fatalf("probed paths = %#v, want /healthz then /health", paths)
	}
}

func TestCodexWorkExecutorTimesOutSilentTurnBeforeSideEffects(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "done")
		for {
			_, raw, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(raw, &msg); err != nil {
				return
			}
			id, hasID := msg["id"]
			if !hasID {
				continue
			}
			method, _ := msg["method"].(string)
			result := map[string]any{}
			switch method {
			case "thread/start":
				result = map[string]any{"thread": map[string]any{"id": "thread-silent"}}
			case "turn/start":
				result = map[string]any{"turn": map[string]any{"id": "turn-silent"}}
			}
			rawResponse, err := json.Marshal(map[string]any{"id": id, "result": result})
			if err != nil {
				return
			}
			if err := conn.Write(context.Background(), websocket.MessageText, rawResponse); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	executor := codexWorkExecutor{
		address:                  "ws://" + strings.TrimPrefix(server.URL, "http://"),
		rpcTimeout:               time.Second,
		firstNotificationTimeout: 25 * time.Millisecond,
	}
	started := time.Now()
	result, err := executor.Run(context.Background(), WorkRequest{Prompt: "diagnose the live lease", Mode: WorkModeReadOnly})
	if err == nil {
		t.Fatal("Run() err = nil, want silent turn timeout")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Run() elapsed = %s, want bounded timeout", elapsed)
	}
	if !strings.Contains(err.Error(), "produced no notifications") {
		t.Fatalf("Run() err = %v, want first-notification timeout", err)
	}
	if result.SideEffects {
		t.Fatalf("result.SideEffects = true, want safe pre-effect failure for native fallback")
	}
	if result.ThreadID != "thread-silent" || result.TurnID != "turn-silent" {
		t.Fatalf("result thread/turn = %q/%q, want partial ids preserved", result.ThreadID, result.TurnID)
	}
}

func TestCodexWorkResultDerivesEvidenceAndCommitLane(t *testing.T) {
	t.Parallel()

	events := []session.WorkCodexEvent{
		{Kind: "file_change", Path: "runtime/work_executor.go", Preview: "@@ diff"},
		{Kind: "command", Command: "go test ./runtime", Status: "completed"},
	}
	result := codexWorkResultFromAppServer(WorkRequest{Mode: WorkModeWorkspaceWrite}, "thread-1", "turn-1", codexAppServerResult{
		ThreadID:    "thread-1",
		TurnID:      "turn-1",
		Text:        "done",
		CodexEvents: events,
	})
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != "runtime/work_executor.go" {
		t.Fatalf("changed files = %#v, want file evidence derived from Codex event", result.ChangedFiles)
	}
	if len(result.Commands) != 1 || result.Commands[0] != "go test ./runtime" {
		t.Fatalf("commands = %#v, want command evidence derived from Codex event", result.Commands)
	}
	if !strings.Contains(result.PatchPreview, "@@ diff") || result.CommitLaneStatus != "commit_requires_separate_lease" {
		t.Fatalf("result = %#v, want patch preview and separate commit lane", result)
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
