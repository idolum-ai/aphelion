//go:build linux

package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestRecordOutboundAndQueryAfterTurn(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 77, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 2
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if err := store.RecordOutbound(key, 1, 100, "text"); err != nil {
		t.Fatalf("RecordOutbound(turn=1) err = %v", err)
	}
	if err := store.RecordOutbound(key, 3, 101, "voice"); err != nil {
		t.Fatalf("RecordOutbound(turn=3) err = %v", err)
	}

	got, err := store.OutboundAfterTurn(key, 1)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(got) != 1 || got[0] != 101 {
		t.Fatalf("OutboundAfterTurn() = %#v, want [101]", got)
	}
}

func TestTurnRunLifecycleAndRecovery(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 900, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "inspect repo")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if run.Status != TurnRunStatusRunning {
		t.Fatalf("begin status = %q, want running", run.Status)
	}

	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"rg foo"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, "stdout:\nmatch", ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 12345); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	interrupted, err := store.InterruptRunningTurnRuns()
	if err != nil {
		t.Fatalf("InterruptRunningTurnRuns() err = %v", err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("interrupted len = %d, want 1", len(interrupted))
	}
	if interrupted[0].ID != run.ID {
		t.Fatalf("interrupted run id = %d, want %d", interrupted[0].ID, run.ID)
	}
	if interrupted[0].ToolCallsStarted != 1 {
		t.Fatalf("tool_calls_started = %d, want 1", interrupted[0].ToolCallsStarted)
	}
	if interrupted[0].ToolCallsFinished != 1 {
		t.Fatalf("tool_calls_finished = %d, want 1", interrupted[0].ToolCallsFinished)
	}
	if interrupted[0].LastToolResultPreview != "stdout:\nmatch" {
		t.Fatalf("last_tool_result_preview = %q, want stdout match", interrupted[0].LastToolResultPreview)
	}
	if interrupted[0].ProgressMessageID != 12345 {
		t.Fatalf("progress_message_id = %d, want 12345", interrupted[0].ProgressMessageID)
	}

	pending, err := store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	if pending[0].Status != TurnRunStatusInterrupted {
		t.Fatalf("pending status = %q, want interrupted", pending[0].Status)
	}

	if err := store.MarkTurnRunsRecovered([]int64{run.ID}, "check logs before retry"); err != nil {
		t.Fatalf("MarkTurnRunsRecovered() err = %v", err)
	}

	pending, err = store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() after recovery err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after recovery = %d, want 0", len(pending))
	}
}

func TestCompleteTurnRunDoesNotOverwriteTerminalTurn(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1900, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "cancel stale work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	interrupted, err := store.InterruptRunningTurnRunIDs([]int64{run.ID}, "watchdog interrupted scoped turn")
	if err != nil {
		t.Fatalf("InterruptRunningTurnRunIDs() err = %v", err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("interrupted len = %d, want 1", len(interrupted))
	}

	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, "late success"); err != nil {
		t.Fatalf("CompleteTurnRun(late) err = %v", err)
	}
	loaded, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}
	if loaded.Status != TurnRunStatusInterrupted {
		t.Fatalf("status = %q, want interrupted after late completion attempt", loaded.Status)
	}
	if loaded.ErrorText != "watchdog interrupted scoped turn" {
		t.Fatalf("error_text = %q, want original interruption reason", loaded.ErrorText)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	foundLate := false
	for _, event := range events {
		if event.EventType == "late_completion_after_interrupt" && strings.Contains(event.PayloadJSON, "late success") {
			foundLate = true
		}
	}
	if !foundLate {
		t.Fatalf("events = %#v, want late completion event", events)
	}
}

func TestStaleRunningTurnRuns(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 905, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	staleRun, err := store.BeginTurnRun(key, TurnRunKindInteractive, "stale run")
	if err != nil {
		t.Fatalf("BeginTurnRun(stale) err = %v", err)
	}
	freshRun, err := store.BeginTurnRun(key, TurnRunKindInteractive, "fresh run")
	if err != nil {
		t.Fatalf("BeginTurnRun(fresh) err = %v", err)
	}
	if err := store.CompleteTurnRun(freshRun.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun(fresh) err = %v", err)
	}

	staleAt := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE turn_runs SET last_activity_at = ? WHERE id = ?`, staleAt, staleRun.ID); err != nil {
		t.Fatalf("mark stale run activity: %v", err)
	}

	cutoff := time.Now().UTC().Add(-5 * time.Minute)
	runs, err := store.StaleRunningTurnRuns(cutoff, 10)
	if err != nil {
		t.Fatalf("StaleRunningTurnRuns() err = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("stale runs len = %d, want 1", len(runs))
	}
	if runs[0].ID != staleRun.ID {
		t.Fatalf("stale run id = %d, want %d", runs[0].ID, staleRun.ID)
	}
	if runs[0].Status != TurnRunStatusRunning {
		t.Fatalf("stale run status = %q, want running", runs[0].Status)
	}
}

func TestStaleRunningTurnRunsDetectsUnmatchedToolStartDespiteHeartbeat(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1905, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1905"}}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "stuck tool run")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "capability_authority", `{"action":"grant_set"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	old := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := store.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   core.ExecutionEventToolStarted,
		Stage:       "tool",
		Status:      "started",
		PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"capability_authority"}`, run.ID),
		CreatedAt:   old,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(tool.started) err = %v", err)
	}
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity() err = %v", err)
	}

	activityCutoff := time.Now().UTC().Add(-5 * time.Minute)
	toolCutoff := time.Now().UTC().Add(-5 * time.Minute)
	runs, err := store.StaleRunningTurnRunsWithUnmatchedToolCutoff(activityCutoff, toolCutoff, 10)
	if err != nil {
		t.Fatalf("StaleRunningTurnRunsWithUnmatchedToolCutoff() err = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("stale runs = %#v, want run %d despite fresh heartbeat", runs, run.ID)
	}
}

func TestStaleRunningTurnRunsIgnoresMatchedToolStartWithFreshHeartbeat(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1906, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1906"}}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "matched tool run")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "capability_authority", `{"action":"grant_set"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, "[CAPABILITY_GRANT]", ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	old := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := store.AppendExecutionEvents(key, []ExecutionEventInput{
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"capability_authority"}`, run.ID),
			CreatedAt:   old,
		},
		{
			EventType:   core.ExecutionEventToolSucceeded,
			Stage:       "tool",
			Status:      "succeeded",
			PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"capability_authority"}`, run.ID),
			CreatedAt:   old.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(tool lifecycle) err = %v", err)
	}
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity() err = %v", err)
	}

	activityCutoff := time.Now().UTC().Add(-5 * time.Minute)
	toolCutoff := time.Now().UTC().Add(-5 * time.Minute)
	runs, err := store.StaleRunningTurnRunsWithUnmatchedToolCutoff(activityCutoff, toolCutoff, 10)
	if err != nil {
		t.Fatalf("StaleRunningTurnRunsWithUnmatchedToolCutoff() err = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("stale runs = %#v, want none for matched tool lifecycle", runs)
	}
}

func TestTouchTurnRunActivity(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 906, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "long running turn")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	before, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(before) err = %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity() err = %v", err)
	}
	after, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(after) err = %v", err)
	}
	if !after.LastActivityAt.After(before.LastActivityAt) {
		t.Fatalf("last_activity_at = %s, want > %s", after.LastActivityAt.Format(time.RFC3339Nano), before.LastActivityAt.Format(time.RFC3339Nano))
	}

	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}
	completed, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(completed) err = %v", err)
	}
	lastActivity := completed.LastActivityAt

	time.Sleep(2 * time.Millisecond)
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity(completed) err = %v", err)
	}
	completedAfterTouch, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(completedAfterTouch) err = %v", err)
	}
	if !completedAfterTouch.LastActivityAt.Equal(lastActivity) {
		t.Fatalf("completed last_activity_at changed from %s to %s; expected unchanged for non-running turns", lastActivity.Format(time.RFC3339Nano), completedAfterTouch.LastActivityAt.Format(time.RFC3339Nano))
	}
}

func TestCompleteTurnRun(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 901, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindCron, "cron work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	rows, err := store.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE id = ?
	`, run.ID)
	if err != nil {
		t.Fatalf("query completed turn run: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected completed turn run row")
	}
	got, err := scanTurnRun(rows)
	if err != nil {
		t.Fatalf("scanTurnRun() err = %v", err)
	}
	if got.Status != TurnRunStatusCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("completed_at is zero, want populated timestamp")
	}
}

func TestSQLiteStoreCreatesExecutionEventsTable(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	var count int
	err := store.db.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'execution_events'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master execution_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("execution_events table count = %d, want 1", count)
	}
}

func TestAppendExecutionEventsMonotonicSequenceAndPayloadNormalization(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 3101, UserID: 0}

	first, err := store.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   "ingress.accepted",
		Stage:       "ingress",
		Status:      "accepted",
		PayloadJSON: `{"message_id":1}`,
	})
	if err != nil {
		t.Fatalf("AppendExecutionEvent(first) err = %v", err)
	}
	second, err := store.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   "turn.started",
		Stage:       "turn",
		Status:      "running",
		CausedBySeq: first.Seq,
		PayloadJSON: "plain payload text",
	})
	if err != nil {
		t.Fatalf("AppendExecutionEvent(second) err = %v", err)
	}
	batch, err := store.AppendExecutionEvents(key, []ExecutionEventInput{
		{EventType: "tool.started", Stage: "tool", Status: "running", CausedBySeq: second.Seq, PayloadJSON: `{}`},
		{EventType: "tool.succeeded", Stage: "tool", Status: "completed", CausedBySeq: second.Seq, PayloadJSON: `{}`},
	})
	if err != nil {
		t.Fatalf("AppendExecutionEvents(batch) err = %v", err)
	}

	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("first/second seq = (%d,%d), want (1,2)", first.Seq, second.Seq)
	}
	if len(batch) != 2 {
		t.Fatalf("batch len = %d, want 2", len(batch))
	}
	if batch[0].Seq != 3 || batch[1].Seq != 4 {
		t.Fatalf("batch seqs = (%d,%d), want (3,4)", batch[0].Seq, batch[1].Seq)
	}
	if !json.Valid([]byte(second.PayloadJSON)) {
		t.Fatalf("normalized second payload is not json: %q", second.PayloadJSON)
	}
	if !strings.Contains(second.PayloadJSON, `"text":"plain payload text"`) {
		t.Fatalf("second payload = %q, want wrapped text payload", second.PayloadJSON)
	}
}

func TestExecutionEventsQueriesBySessionAndChat(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	keyA := SessionKey{ChatID: 4101, UserID: 0}
	keyB := SessionKey{ChatID: 4102, UserID: 0}

	if _, err := store.AppendExecutionEvents(keyA, []ExecutionEventInput{
		{EventType: "ingress.accepted", Stage: "ingress", Status: "accepted", PayloadJSON: `{"message_id":1}`},
		{EventType: "turn.started", Stage: "turn", Status: "running", PayloadJSON: `{}`},
		{EventType: "turn.completed", Stage: "turn", Status: "completed", PayloadJSON: `{}`},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyA) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(keyB, []ExecutionEventInput{
		{EventType: "ingress.accepted", Stage: "ingress", Status: "accepted", PayloadJSON: `{"message_id":2}`},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyB) err = %v", err)
	}

	eventsA, err := store.ExecutionEventsBySession(keyA, 1, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(keyA) err = %v", err)
	}
	if len(eventsA) != 2 {
		t.Fatalf("eventsA len = %d, want 2", len(eventsA))
	}
	if eventsA[0].Seq != 2 || eventsA[1].Seq != 3 {
		t.Fatalf("eventsA seqs = (%d,%d), want (2,3)", eventsA[0].Seq, eventsA[1].Seq)
	}
	if eventsA[0].EventType != "turn.started" || eventsA[1].EventType != "turn.completed" {
		t.Fatalf("eventsA types = (%q,%q), want turn lifecycle", eventsA[0].EventType, eventsA[1].EventType)
	}

	chatEvents, err := store.ExecutionEventsByChat(4101, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByChat(4101) err = %v", err)
	}
	if len(chatEvents) != 3 {
		t.Fatalf("chatEvents len = %d, want 3", len(chatEvents))
	}
	if chatEvents[0].Seq != 3 {
		t.Fatalf("chatEvents first seq = %d, want latest seq 3", chatEvents[0].Seq)
	}
}

func TestLatestExecutionEventsBySessionReturnsNewestWindowInAscendingOrder(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 4103, UserID: 0}
	inputs := make([]ExecutionEventInput, 0, 5)
	for i := 0; i < 5; i++ {
		inputs = append(inputs, ExecutionEventInput{EventType: fmt.Sprintf("event.%d", i+1), Stage: "test", Status: "ok", PayloadJSON: `{}`})
	}
	if _, err := store.AppendExecutionEvents(key, inputs); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	events, err := store.LatestExecutionEventsBySession(key, 3)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events len = %d, want 3", len(events))
	}
	if events[0].Seq != 3 || events[1].Seq != 4 || events[2].Seq != 5 {
		t.Fatalf("event seqs = (%d,%d,%d), want latest window in ascending order", events[0].Seq, events[1].Seq, events[2].Seq)
	}
}

func TestExecutionEventsByTypesFiltersAndOrdersByCreatedAt(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	keyA := SessionKey{ChatID: 4201, UserID: 0}
	keyB := SessionKey{ChatID: 4202, UserID: 0}
	if _, err := store.AppendExecutionEvents(keyA, []ExecutionEventInput{
		{
			EventType:   "decision.opened",
			Stage:       "decision",
			Status:      "pending",
			PayloadJSON: `{"decision_id":"d1"}`,
			CreatedAt:   now.Add(-30 * time.Second),
		},
		{
			EventType:   "continuation.offered",
			Stage:       "continuation",
			Status:      "pending",
			PayloadJSON: `{"decision_id":"c1"}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyA) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(keyB, []ExecutionEventInput{
		{
			EventType:   "decision.resolved",
			Stage:       "decision",
			Status:      "resolved",
			PayloadJSON: `{"decision_id":"d1"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyB) err = %v", err)
	}

	events, err := store.ExecutionEventsByTypes([]string{
		"decision.opened",
		"decision.resolved",
	}, now.Add(-40*time.Second), 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes() err = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].EventType != "decision.resolved" || events[1].EventType != "decision.opened" {
		t.Fatalf("events order/types = (%q,%q), want desc created_at decision.resolved then decision.opened", events[0].EventType, events[1].EventType)
	}
	if events[0].ChatID != 4202 || events[1].ChatID != 4201 {
		t.Fatalf("events chat ids = (%d,%d), want (4202,4201)", events[0].ChatID, events[1].ChatID)
	}
}

func TestExecutionEventsRecentReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	key := SessionKey{ChatID: 4301, UserID: 0}
	if _, err := store.AppendExecutionEvents(key, []ExecutionEventInput{
		{
			EventType:   "turn.started",
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-30 * time.Second),
		},
		{
			EventType:   "tool.started",
			Stage:       "tool",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
		{
			EventType:   "turn.completed",
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	events, err := store.ExecutionEventsRecent(2)
	if err != nil {
		t.Fatalf("ExecutionEventsRecent() err = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].EventType != "turn.completed" || events[1].EventType != "tool.started" {
		t.Fatalf("events order/types = (%q,%q), want turn.completed then tool.started", events[0].EventType, events[1].EventType)
	}
}
