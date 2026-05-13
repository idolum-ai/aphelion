//go:build linux

package session

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestContinuationStateIfExistsDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1906, UserID: 0}
	state, exists, err := store.ContinuationStateIfExists(key)
	if err != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("ContinuationStateIfExists() = %#v, exists=%v; want no pre-existing continuation state", state, exists)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&count); err != nil {
		t.Fatalf("query sessions count: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions row count = %d, want 0", count)
	}
}

func TestContinuationStateIfExistsRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 2592, UserID: 0}
	if err := store.UpdateContinuationState(key, ContinuationState{Status: ContinuationStatusPending, DecisionID: "decision-invalid-json", RemainingTurns: 1}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE sessions SET continuation_state_json = ? WHERE session_id = ?`, "{", SessionIDForKey(key)); err != nil {
		t.Fatalf("corrupt continuation state: %v", err)
	}

	_, exists, err := store.ContinuationStateIfExists(key)
	if err == nil || !exists || !strings.Contains(err.Error(), "decode continuation state") {
		t.Fatalf("ContinuationStateIfExists() exists=%v err=%v, want decode error on existing row", exists, err)
	}
}

func TestPlanAndOperationStateIfExistsDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1907, UserID: 0}
	plan, operation, exists, err := store.PlanAndOperationStateIfExists(key)
	if err != nil {
		t.Fatalf("PlanAndOperationStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("PlanAndOperationStateIfExists() = (%#v, %#v, %v), want no pre-existing state", plan, operation, exists)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&count); err != nil {
		t.Fatalf("query sessions count: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions row count = %d, want 0", count)
	}
}

func TestPlanAndOperationStateIfExistsReturnsPersistedState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1908, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.PlanState = PlanState{
		Steps: []PlanStep{{
			Step:   "Await admin approval",
			Status: PlanStatusInProgress,
		}},
	}
	sess.OperationState = OperationState{
		Status:  OperationStatusBlocked,
		Stage:   "approval_wait",
		Summary: "Waiting for admin review",
	}
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	plan, operation, exists, err := store.PlanAndOperationStateIfExists(key)
	if err != nil {
		t.Fatalf("PlanAndOperationStateIfExists() err = %v", err)
	}
	if !exists {
		t.Fatal("PlanAndOperationStateIfExists() exists = false, want true")
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Step != "Await admin approval" || plan.Steps[0].Status != PlanStatusInProgress {
		t.Fatalf("plan state = %#v, want persisted in-progress step", plan)
	}
	if operation.Status != OperationStatusBlocked || operation.Stage != "approval_wait" || operation.Summary != "Waiting for admin review" {
		t.Fatalf("operation state = %#v, want persisted blocked operation state", operation)
	}
}

func TestStatusStateIfExistsDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1909, UserID: 0}
	state, exists, err := store.StatusStateIfExists(key)
	if err != nil {
		t.Fatalf("StatusStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("StatusStateIfExists() = (%#v, %v), want no pre-existing state", state, exists)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&count); err != nil {
		t.Fatalf("query sessions count: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions row count = %d, want 0", count)
	}
}

func TestStatusStateIfExistsReturnsPersistedStateAndOutboundCount(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1910, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.PlanState = PlanState{
		Steps: []PlanStep{{
			Step:   "Await admin approval",
			Status: PlanStatusInProgress,
		}},
	}
	sess.OperationState = OperationState{
		Status:  OperationStatusBlocked,
		Stage:   "approval_wait",
		Summary: "Waiting for admin review",
	}
	sess.LastFloorMetadata = `{"hidden_inputs":[{"category":"unresolved_memory_state","summary":"follow-up question still open"}],"provenance_summary":"latent unresolved memory persists"}`
	sess.TurnCount = 3
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	if err := store.RecordOutbound(key, sess.TurnCount, 4501, "message"); err != nil {
		t.Fatalf("RecordOutbound() err = %v", err)
	}

	state, exists, err := store.StatusStateIfExists(key)
	if err != nil {
		t.Fatalf("StatusStateIfExists() err = %v", err)
	}
	if !exists {
		t.Fatal("StatusStateIfExists() exists = false, want true")
	}
	if len(state.PlanState.Steps) != 1 || state.PlanState.Steps[0].Step != "Await admin approval" {
		t.Fatalf("plan state = %#v, want persisted plan step", state.PlanState)
	}
	if state.OperationState.Status != OperationStatusBlocked || state.OperationState.Stage != "approval_wait" {
		t.Fatalf("operation state = %#v, want persisted blocked operation", state.OperationState)
	}
	if state.LastFloorMetadata == "" {
		t.Fatalf("LastFloorMetadata = %q, want persisted metadata", state.LastFloorMetadata)
	}
	if state.TurnCount != 3 {
		t.Fatalf("TurnCount = %d, want 3", state.TurnCount)
	}
	if state.OutboundCountAtTurn != 1 {
		t.Fatalf("OutboundCountAtTurn = %d, want 1", state.OutboundCountAtTurn)
	}
}

func TestLatestDoctorReportReturnsMostRecentSyntheticDoctorTurn(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1911, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	firstAt := time.Date(2026, 5, 10, 6, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Hour)
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "/health diagnose", TurnIndex: 1, CreatedAt: firstAt},
		{Role: "assistant", Content: "old full report", FloorContent: "old telegram report", FloorMetadata: "doctor_full_report_chars=15", TurnIndex: 1, CreatedAt: firstAt},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(first doctor) err = %v", err)
	}
	sess.TurnCount = 2
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "ordinary turn", TurnIndex: 2, CreatedAt: firstAt.Add(30 * time.Minute)},
		{Role: "assistant", Content: "ordinary report", TurnIndex: 2, CreatedAt: firstAt.Add(30 * time.Minute)},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(ordinary turn) err = %v", err)
	}
	sess.TurnCount = 3
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "/health diagnose", TurnIndex: 3, CreatedAt: secondAt},
		{Role: "assistant", Content: "new full report", FloorContent: "new telegram report", FloorMetadata: "doctor_full_report_chars=15", TurnIndex: 3, CreatedAt: secondAt},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(second doctor) err = %v", err)
	}

	report, ok, err := store.LatestDoctorReport(key)
	if err != nil {
		t.Fatalf("LatestDoctorReport() err = %v", err)
	}
	if !ok {
		t.Fatal("LatestDoctorReport() ok = false, want true")
	}
	if report.FullReport != "new full report" || report.TelegramReport != "new telegram report" || report.TurnIndex != 3 {
		t.Fatalf("LatestDoctorReport() = %#v, want newest doctor report", report)
	}
	if !report.CreatedAt.Equal(secondAt) {
		t.Fatalf("CreatedAt = %s, want %s", report.CreatedAt, secondAt)
	}
}

func TestLatestDoctorReportDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1912, UserID: 0}
	report, ok, err := store.LatestDoctorReport(key)
	if err != nil {
		t.Fatalf("LatestDoctorReport() err = %v", err)
	}
	if ok {
		t.Fatalf("LatestDoctorReport() = %#v, ok=true; want no report", report)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&count); err != nil {
		t.Fatalf("query sessions count: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions row count = %d, want 0", count)
	}
}
