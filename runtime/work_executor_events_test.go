//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

func TestCodexWorkExecutorRetainsTruncatedOversizedEvidence(t *testing.T) {
	t.Parallel()

	largeOutput := strings.Repeat("x", 40*1024)
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
				if !write(map[string]any{"id": id, "result": map[string]any{"thread": map[string]any{"id": "thread-evidence"}}}) {
					return
				}
			case "turn/start":
				if !write(map[string]any{"id": id, "result": map[string]any{"turn": map[string]any{"id": "turn-evidence"}}}) {
					return
				}
				if !write(map[string]any{
					"method": "item/commandExecution/completed",
					"params": map[string]any{
						"threadId": "thread-evidence",
						"turnId":   "turn-evidence",
						"command":  "rg doctor runtime",
						"status":   "completed",
						"stdout":   largeOutput,
					},
				}) {
					return
				}
				if !write(map[string]any{
					"method": "turn/completed",
					"params": map[string]any{
						"threadId": "thread-evidence",
						"turn":     map[string]any{"id": "turn-evidence"},
					},
				}) {
					return
				}
			}
		}
	}))
	defer server.Close()

	executor := codexWorkExecutor{
		address:                  "ws://" + strings.TrimPrefix(server.URL, "http://"),
		rpcTimeout:               time.Second,
		firstNotificationTimeout: time.Second,
	}
	result, err := executor.Run(context.Background(), WorkRequest{Prompt: "collect evidence", Mode: WorkModeReadOnly})
	if err != nil {
		t.Fatalf("Run() err = %v, want oversized evidence retained without breaking turn", err)
	}
	var event session.WorkCodexEvent
	for _, candidate := range result.CodexEvents {
		if candidate.Kind == "command" {
			event = candidate
			break
		}
	}
	if event.Kind == "" {
		t.Fatalf("CodexEvents = %#v, want retained command event", result.CodexEvents)
	}
	if event.Command != "rg doctor runtime" {
		t.Fatalf("event command = %q, want rg command", event.Command)
	}
	if len(event.Preview) > 1000 {
		t.Fatalf("event preview length = %d, want normalized truncated evidence", len(event.Preview))
	}
	if len(result.Commands) != 1 || result.Commands[0] != "rg doctor runtime" {
		t.Fatalf("commands = %#v, want command evidence retained", result.Commands)
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

func TestExternalAccountStatusPlanLeaseRunsReadOnlyWorkExecutor(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	now := time.Now().UTC()
	expiresAt := now.Add(time.Hour)
	key := session.SessionKey{ChatID: 8291, UserID: 0, Scope: telegramDMScopeRef(8291)}
	actions := []string{"mint_github_app_jwt_in_memory", "mint_installation_token_in_memory", "call_github_actions_read_api", "call_github_release_read_api", "report_release_status"}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "plan-lease-external-status",
		Objective:      "Verify release workflow status.",
		StageSummary:   "Use the GitHub App credential to verify Actions/release status.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-plan-lease-external-status",
			OperationID:    "plan-budget-external-status",
			Summary:        "Approve plan budget: external status check",
			BoundedEffect:  "lane phase-auth external_account_status_check 1 turn: verify status_check only",
			RiskClass:      "plan_lease",
			AllowedActions: append([]string{"approve_operation_plan_lease"}, actions...),
			Status:         session.ProposalStatusApproved,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:external-status",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-plan-lease-external-status",
			ProposalID:     "aprop-plan-lease-external-status",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			ApprovedBy:     1001,
			ApprovedAt:     now,
			AllowedActions: append([]string{"approve_operation_plan_lease"}, actions...),
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:external-status",
		},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             "plan-lease-external-status",
			Status:         session.ContinuationLeaseStatusActive,
			CurrentPhaseID: "phase-external-status",
			ApprovedBy:     1001,
			ApprovedAt:     now,
			Phases: []session.ContinuationApprovalBundlePhase{{
				ID:               "phase-external-status",
				OperationPhaseID: "phase-auth",
				AuthorityClass:   "external_account_status_check",
				AllowedActions:   actions,
				Status:           session.ContinuationLeaseStatusActive,
			}},
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := store.UpdateOperationState(key, session.OperationState{ID: "op-external-status", Objective: "Verify release workflow status.", Status: session.OperationStatusActive}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8291); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if work.calls != 1 || work.lastReq.Mode != WorkModeReadOnly {
		t.Fatalf("work calls=%d mode=%q, want one read_only call", work.calls, work.lastReq.Mode)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventContinuationConsumed) || !hasExecutionEvent(events, core.ExecutionEventWorkExecutorStarted) {
		t.Fatalf("events = %#v, want consumed and work started", events)
	}
}

func TestLeaseActionDeniedOffersOneShotRepairProposal(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8292, UserID: 0, Scope: telegramDMScopeRef(8292)}
	state := session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "read-only-shape-mismatch",
		Objective:         "Check status.",
		StageSummary:      "Run a status_check and report.",
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    session.ActionProposal{ID: "aprop-read-only-shape-mismatch", Summary: "Run a status_check", BoundedEffect: "Inspect status_check only.", RiskClass: "continuation", AllowedActions: []string{"inspect_status"}, Status: session.ProposalStatusApproved, ExpiresAt: expiresAt},
		ContinuationLease: session.ContinuationLease{ID: "lease-read-only-shape-mismatch", ProposalID: "aprop-read-only-shape-mismatch", Status: session.ContinuationLeaseStatusActive, MaxTurns: 1, RemainingTurns: 1, ApprovedBy: 1001, ApprovedAt: expiresAt.Add(-time.Hour), AllowedActions: []string{"inspect_status"}, ExpiresAt: expiresAt},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8292); err != nil {
		t.Fatalf("TriggerContinuation() err = %v", err)
	}
	if work.calls != 0 {
		t.Fatalf("work calls = %d, want denied before executor", work.calls)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusPending || !actionListContains(got.ContinuationLease.AllowedActions, "read_only") {
		t.Fatalf("continuation = %#v, want fresh pending repair lease with read_only", got)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one repair approval prompt", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventRecoveryIssued) || !hasExecutionEvent(events, core.ExecutionEventContinuationOffered) {
		t.Fatalf("events = %#v, want repair issued/offered", events)
	}
	if !eventsContainLeaseDenialRepairMarker(events) {
		t.Fatalf("events = %#v, want structured lease_denial_repair marker", events)
	}
}

func TestLeaseActionDeniedDoesNotRepairAuthorityWidening(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8293, UserID: 0, Scope: telegramDMScopeRef(8293)}
	prior := session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "read-only-no-widening",
		Objective:         "Inspect status.",
		StageSummary:      "Run a status_check and report.",
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    session.ActionProposal{ID: "aprop-read-only-no-widening", Summary: "Inspect status", BoundedEffect: "Inspect status only.", RiskClass: "read_only_review", AllowedActions: []string{"inspect_status"}, Status: session.ProposalStatusApproved, ExpiresAt: expiresAt},
		ContinuationLease: session.ContinuationLease{ID: "lease-read-only-no-widening", ProposalID: "aprop-read-only-no-widening", Status: session.ContinuationLeaseStatusActive, MaxTurns: 1, RemainingTurns: 1, ApprovedBy: 1001, ApprovedAt: expiresAt.Add(-time.Hour), AllowedActions: []string{"inspect_status"}, ExpiresAt: expiresAt},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	rt.offerLeaseActionDeniedRepair(context.Background(), key, 8293, prior, session.ContinuationLeaseAccessDecision{LeaseID: prior.ContinuationLease.ID, Action: string(WorkModeWorkspaceWrite), Reason: "action_not_allowed"}, time.Now().UTC())
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusApproved || got.ActionProposal.ID != prior.ActionProposal.ID {
		t.Fatalf("continuation = %#v, want unchanged approved state", got)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no authority-widening repair prompt", inlineCount)
	}
}

func TestLeaseActionDeniedRepairSuppressesRepeatedSameCause(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	work := &fakeWorkExecutor{name: "codex", ready: true}
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "auto", AutoOrder: []string{"codex"}}, []WorkExecutor{work})

	expiresAt := time.Now().UTC().Add(time.Hour)
	key := session.SessionKey{ChatID: 8294, UserID: 0, Scope: telegramDMScopeRef(8294)}
	prior := session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "read-only-repeat-shape-mismatch",
		Objective:         "Check status.",
		StageSummary:      "Run a status_check and report.",
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    session.ActionProposal{ID: "aprop-read-only-repeat-shape-mismatch", Summary: "Run a status_check", BoundedEffect: "Inspect status_check only.", RiskClass: "continuation", AllowedActions: []string{"inspect_status"}, Status: session.ProposalStatusApproved, ExpiresAt: expiresAt},
		ContinuationLease: session.ContinuationLease{ID: "lease-read-only-repeat-shape-mismatch", ProposalID: "aprop-read-only-repeat-shape-mismatch", Status: session.ContinuationLeaseStatusActive, MaxTurns: 1, RemainingTurns: 1, ApprovedBy: 1001, ApprovedAt: expiresAt.Add(-time.Hour), AllowedActions: []string{"inspect_status"}, ExpiresAt: expiresAt},
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState(first) err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8294); err != nil {
		t.Fatalf("TriggerContinuation(first) err = %v", err)
	}
	if err := store.UpdateContinuationState(key, prior); err != nil {
		t.Fatalf("UpdateContinuationState(second) err = %v", err)
	}
	if err := rt.TriggerContinuation(context.Background(), 8294); err != nil {
		t.Fatalf("TriggerContinuation(second) err = %v", err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want exactly one repair approval prompt for repeated same cause", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventRecoveryIssued); got != 1 {
		t.Fatalf("recovery issued count = %d, want one; events=%#v", got, events)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationBlocked); got != 2 {
		t.Fatalf("blocked count = %d, want two blocked denials; events=%#v", got, events)
	}
}

func eventsContainLeaseDenialRepairMarker(events []session.ExecutionEvent) bool {
	for _, event := range events {
		marker, ok := leaseDenialRepairMarkerFromPayload(event.PayloadJSON)
		if ok && marker.Kind == leaseDenialRepairKind && marker.CauseHash != "" {
			return true
		}
	}
	return false
}

func countEventsByType(events []session.ExecutionEvent, eventType string) int {
	count := 0
	for _, event := range events {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}
