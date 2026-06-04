//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestStatusDiagnosticsIncludesLatestTurnAndContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 8111, UserID: 0, Scope: telegramDMScopeRef(8111)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "check status diagnostics")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"curl https://api.github.com/zen"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":1,"run_kind":"interactive","request_text":"check status diagnostics"}`,
			CreatedAt:   now.Add(-2 * time.Second),
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"tool":"exec","preview":"{\"command\":\"curl https://api.github.com/zen\"}"}`,
			CreatedAt:   now.Add(-time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(status diagnostics) err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(8111)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.Join(lines, "\n")
	for _, needle := range []string{
		"Latest persisted turn",
		"running",
		"interactive",
		"Last tool: exec.",
		"Continuation: pending",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("StatusDiagnostics() = %q, want substring %q", text, needle)
		}
	}
}

func TestStatusDiagnosticsReturnsEmptyWithoutSessionHistory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(9222)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("StatusDiagnostics() len = %d, want 0", len(lines))
	}

	state, exists, err := store.ContinuationStateIfExists(session.SessionKey{
		ChatID: 9222,
		UserID: 0,
		Scope:  telegramDMScopeRef(9222),
	})
	if err != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("ContinuationStateIfExists() = %#v, exists=%v; want no row after status probe", state, exists)
	}
}

func TestStatusDiagnosticsSurfacesApprovalAffordanceGap(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8122, UserID: 0, Scope: telegramDMScopeRef(8122)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "approval-gap-op",
		Objective: "Show why the operator has no buttons.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "approval-gap-plan",
			CurrentPhaseID: "phase-current",
			Phases: []session.OperationPhase{
				{ID: "phase-old", Summary: "Old live step", Status: session.PlanStatusInProgress, LeaseID: "lease-old"},
				{ID: "phase-current", Summary: "Current repo-only step", Status: session.PlanStatusPending, AuthorityClass: "workspace_write", BoundedEffect: "Patch local files and run tests.", RequiresApproval: true},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(8122)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.Join(lines, "\n")
	for _, needle := range []string{
		"Approval affordance gap",
		"current_phase=phase-current",
		"stale_in_progress_phases=1",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("StatusDiagnostics() = %q, want substring %q", text, needle)
		}
	}
}

func TestStatusDiagnosticsShowsRepairLoopNextActionAndRefs(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 8123, UserID: 0, Scope: telegramDMScopeRef(8123)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType: core.ExecutionEventContinuationAdjudicated,
		Stage:     "continuation",
		Status:    "adjudicated",
		PayloadJSON: `{
			"adjudication_kind":"continuation_approval",
			"surface":"materialization_repair",
			"subject_id":"decision-completed-followup",
			"operator_label":"Completed continuation approval repaired",
			"visible_action":"repair_completed_or_superseded_approval",
			"evidence_refs":["operation:completed-followup-op","phase:phase-commit-push","lease:lease-old"],
			"findings":[{"kind":"stale_completed_approval","claim_type":"stale_completed_approval","detail":"operation completed","required_behavior":"Do not re-offer completed work."}]
		}`,
		CreatedAt: now,
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(8123)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.Join(lines, "\n")
	for _, needle := range []string{
		"Completed continuation approval repaired",
		"action=repair_completed_or_superseded_approval",
		"subject=decision-completed-followup",
		"refs=operation:completed-followup-op,phase:phase-commit-push,lease:lease-old",
		`detail="operation completed"`,
		`next="Ask for a new bounded follow-up if more work remains."`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("StatusDiagnostics() = %q, want substring %q", text, needle)
		}
	}
}

func TestStatusDiagnosticsPrefersTurnProjectionFromExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9333, UserID: 0, Scope: telegramDMScopeRef(9333)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "old failed row")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusFailed, "old failure"); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventTurnStarted,
		Stage:       "turn",
		Status:      "running",
		PayloadJSON: `{"turn_kind":"interactive","request_text":"event timeline"}`,
		CreatedAt:   now.Add(time.Second),
	}, {
		EventType:   core.ExecutionEventTurnCompleted,
		Stage:       "turn",
		Status:      "completed",
		PayloadJSON: `{"turn_kind":"interactive","request_text":"event timeline"}`,
		CreatedAt:   now.Add(2 * time.Second),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	lines, err := rt.StatusDiagnostics(9333)
	if err != nil {
		t.Fatalf("StatusDiagnostics() err = %v", err)
	}
	text := strings.ToLower(strings.Join(lines, "\n"))
	if !strings.Contains(text, "completed") {
		t.Fatalf("StatusDiagnostics() = %q, want completed state from TES projection", text)
	}
	if strings.Contains(text, "failed") {
		t.Fatalf("StatusDiagnostics() = %q, do not want stale failed state from old row", text)
	}
}

func TestIsTelegramAdmin(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if !rt.IsTelegramAdmin(1001) {
		t.Fatal("IsTelegramAdmin(1001) = false, want true")
	}
	if rt.IsTelegramAdmin(1002) {
		t.Fatal("IsTelegramAdmin(1002) = true, want false for non-admin approved user")
	}
	if rt.IsTelegramAdmin(0) {
		t.Fatal("IsTelegramAdmin(0) = true, want false")
	}
}
