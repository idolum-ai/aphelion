//go:build linux

package session

import (
	"encoding/json"
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

func TestOpenNextActionsBySessionSubjectIsScopedAndNotHiddenBySessionVolume(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: 91005, UserID: 1001}
	otherKey := SessionKey{ChatID: 91006, UserID: 1001}
	subjectKind := "continuation_lease_request"
	subjectRef := "child_wake:child-alpha:grant-alpha:tool=durable_agent:action=wake_once"
	target, err := store.RecordNextAction(NextActionInput{
		RecordID:          "target-subject-action",
		Key:               key,
		Owner:             "test",
		State:             NextActionBlockedNeedsAuthority,
		SubjectKind:       subjectKind,
		SubjectRef:        subjectRef,
		NextAction:        "approve child wake",
		RequiredAuthority: "child_wake",
		CreatedAt:         time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RecordNextAction(target) err = %v", err)
	}
	if _, err := store.RecordNextAction(NextActionInput{
		RecordID:          "other-session-same-subject",
		Key:               otherKey,
		Owner:             "test",
		State:             NextActionBlockedNeedsAuthority,
		SubjectKind:       subjectKind,
		SubjectRef:        subjectRef,
		NextAction:        "approve child wake elsewhere",
		RequiredAuthority: "child_wake",
		CreatedAt:         time.Date(2026, 6, 23, 10, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("RecordNextAction(other session) err = %v", err)
	}
	for i := 0; i < 125; i++ {
		if _, err := store.RecordNextAction(NextActionInput{
			RecordID:          "unrelated-session-action-" + time.Date(2026, 6, 23, 10, i+2, 0, 0, time.UTC).Format("150405"),
			Key:               key,
			Owner:             "test",
			State:             NextActionBlockedNeedsAuthority,
			SubjectKind:       "unrelated",
			SubjectRef:        time.Date(2026, 6, 23, 10, i+2, 0, 0, time.UTC).Format("150405"),
			NextAction:        "unrelated",
			RequiredAuthority: "test",
			CreatedAt:         time.Date(2026, 6, 23, 10, i+2, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("RecordNextAction(unrelated %d) err = %v", i, err)
		}
	}

	sessionWide, err := store.OpenNextActionsBySession(key, 100)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range sessionWide {
		if action.RecordID == target.RecordID {
			t.Fatalf("session-wide newest 100 unexpectedly included old target action")
		}
	}
	matches, err := store.OpenNextActionsBySessionSubject(key, subjectKind, subjectRef, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionSubject() err = %v", err)
	}
	if len(matches) != 1 || matches[0].RecordID != target.RecordID {
		t.Fatalf("session subject matches = %#v, want only %q", matches, target.RecordID)
	}
	otherMatches, err := store.OpenNextActionsBySessionSubject(otherKey, subjectKind, subjectRef, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionSubject(other) err = %v", err)
	}
	if len(otherMatches) != 1 || otherMatches[0].RecordID != "other-session-same-subject" {
		t.Fatalf("other session subject matches = %#v, want other session action", otherMatches)
	}
}

func TestOpenNextActionsBySessionOperationFindsRecoveryHandoffBehindSessionVolume(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: 91007, UserID: 1001}
	otherKey := SessionKey{ChatID: 91008, UserID: 1001}
	target, err := store.RecordNextAction(NextActionInput{
		RecordID:           "target-operation-action",
		Key:                key,
		Owner:              "test",
		State:              NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "child_wake:child-alpha",
		NextAction:         "approve child wake",
		RequiredAuthority:  "child_wake",
		ResourceBlocker:    "missing_continuation_lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: `{"action":"request_continuation_lease","lease_class":"child_wake","request_instance_id":"req-1"}`,
		CreatedAt:          time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RecordNextAction(target) err = %v", err)
	}
	if _, err := store.RecordNextAction(NextActionInput{
		RecordID:           "other-session-operation-action",
		Key:                otherKey,
		Owner:              "test",
		State:              NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "child_wake:child-alpha",
		NextAction:         "approve child wake elsewhere",
		RequiredAuthority:  "child_wake",
		ResourceBlocker:    "missing_continuation_lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: `{"action":"request_continuation_lease","lease_class":"child_wake","request_instance_id":"req-other"}`,
		CreatedAt:          time.Date(2026, 6, 24, 10, 1, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("RecordNextAction(other session) err = %v", err)
	}
	for i := 0; i < 125; i++ {
		if _, err := store.RecordNextAction(NextActionInput{
			RecordID:           "unrelated-operation-action-" + time.Date(2026, 6, 24, 10, i+2, 0, 0, time.UTC).Format("150405"),
			Key:                key,
			Owner:              "test",
			State:              NextActionBlockedNeedsAuthority,
			SubjectKind:        "unrelated",
			SubjectRef:         time.Date(2026, 6, 24, 10, i+2, 0, 0, time.UTC).Format("150405"),
			NextAction:         "unrelated",
			RequiredAuthority:  "test",
			OperationKind:      "capability_grant_review",
			OperationTool:      "capability_authority",
			OperationInputJSON: `{"action":"grant_set"}`,
			CreatedAt:          time.Date(2026, 6, 24, 10, i+2, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("RecordNextAction(unrelated %d) err = %v", i, err)
		}
	}

	sessionWide, err := store.OpenNextActionsBySession(key, 100)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range sessionWide {
		if action.RecordID == target.RecordID {
			t.Fatalf("session-wide newest 100 unexpectedly included old target action")
		}
	}
	matches, err := store.OpenNextActionsBySessionOperation(key, NextActionBlockedNeedsAuthority, "request_approval", "continuation_lease_request", 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionOperation() err = %v", err)
	}
	if len(matches) != 1 || matches[0].RecordID != target.RecordID {
		t.Fatalf("operation matches = %#v, want only %q", matches, target.RecordID)
	}
	otherMatches, err := store.OpenNextActionsBySessionOperation(otherKey, NextActionBlockedNeedsAuthority, "request_approval", "continuation_lease_request", 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionOperation(other) err = %v", err)
	}
	if len(otherMatches) != 1 || otherMatches[0].RecordID != "other-session-operation-action" {
		t.Fatalf("other session operation matches = %#v, want other session action", otherMatches)
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

func TestRecordNextActionPersistsStructuredOperationPayload(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: 91004, UserID: 1001}
	if _, err := store.RecordNextAction(NextActionInput{
		Key:                key,
		Owner:              "test",
		State:              NextActionReadyToExecute,
		SubjectKind:        "shell_rejection",
		SubjectRef:         "sha256:test",
		NextAction:         "read through native tool",
		OperationKind:      "native_file_read",
		OperationTool:      "read_file",
		OperationInputJSON: `{"path": "README.md", "full": true}`,
		OperatorProjection: "Use read_file.",
	}); err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open next actions = %#v, want one", open)
	}
	got := open[0]
	var gotInput map[string]any
	if err := json.Unmarshal([]byte(got.OperationInputJSON), &gotInput); err != nil {
		t.Fatalf("unmarshal stored operation input: %v", err)
	}
	if got.OperationKind != "native_file_read" || got.OperationTool != "read_file" || gotInput["path"] != "README.md" || gotInput["full"] != true {
		t.Fatalf("operation payload = kind=%q tool=%q input=%s", got.OperationKind, got.OperationTool, got.OperationInputJSON)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one workflow.next_state event", events)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("unmarshal next-action event payload: %v", err)
	}
	operation, ok := payload["operation"].(map[string]any)
	if !ok || operation["kind"] != "native_file_read" || operation["tool"] != "read_file" {
		t.Fatalf("event operation payload = %#v", payload["operation"])
	}
	input, ok := operation["input"].(map[string]any)
	if !ok || input["path"] != "README.md" || input["full"] != true {
		t.Fatalf("event operation input = %#v", operation["input"])
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
