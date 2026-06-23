//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecordNextActionResolvesPriorOpenActionForSameSubject(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: 91001, UserID: 1001}
	first, err := store.RecordNextAction(NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              NextActionWaitingForOperator,
		SubjectKind:        "phase",
		SubjectRef:         "phase-a",
		NextAction:         "ask for authority",
		OperatorProjection: "Waiting for operator.",
		CreatedAt:          time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RecordNextAction(first) err = %v", err)
	}
	second, err := store.RecordNextAction(NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              NextActionReadyToExecute,
		SubjectKind:        "phase",
		SubjectRef:         "phase-a",
		NextAction:         "execute approved step",
		OperatorProjection: "Ready.",
		CreatedAt:          time.Date(2026, 6, 23, 10, 1, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RecordNextAction(second) err = %v", err)
	}

	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].RecordID != second.RecordID {
		t.Fatalf("open next actions = %#v, want only second action %q", open, second.RecordID)
	}
	if first.RecordID == second.RecordID {
		t.Fatalf("record ids should be occurrence-specific, both = %q", first.RecordID)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if len(events) != 2 || events[0].EventType != "workflow.next_state" || events[1].EventType != "workflow.next_state" {
		t.Fatalf("next-action events = %#v, want two workflow.next_state events", events)
	}
}

func TestRecordResourcePreflightCreatesRepairNextAction(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: 91002, UserID: 1001}
	if err := store.RecordResourcePreflight(key, 42, "/workspace/blocked", "host_mode_denied", "outside native root", time.Now().UTC()); err != nil {
		t.Fatalf("RecordResourcePreflight() err = %v", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open next actions = %#v, want one resource blocker", open)
	}
	if open[0].State != NextActionBlockedNeedsResourceRepair || open[0].ResourceBlocker != "host_mode_denied" || open[0].TurnRunID != 42 {
		t.Fatalf("resource next action = %#v, want blocked resource repair tied to turn 42", open[0])
	}
}

func TestResolveNextActionClosesOpenAction(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: 91003, UserID: 1001}
	if _, err := store.RecordNextAction(NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              NextActionWaitingForChild,
		SubjectKind:        "task_packet",
		SubjectRef:         "task-a",
		NextAction:         "wait for child",
		OperatorProjection: "Waiting.",
	}); err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}
	if err := store.ResolveNextAction(NextActionResolutionInput{
		Key:         key,
		Owner:       "test",
		SubjectKind: "task_packet",
		SubjectRef:  "task-a",
		Reason:      "child_completed",
	}); err != nil {
		t.Fatalf("ResolveNextAction() err = %v", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions = %#v, want resolved action closed", open)
	}
}
