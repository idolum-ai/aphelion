//go:build linux

package session

import (
	"testing"
	"time"
)

func TestEffectAttemptUpsertIsIdempotentAndDedupeEvidence(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7001, UserID: 0}
	now := time.Now().UTC()
	input := EffectAttemptInput{
		Key:          key,
		TurnRunID:    11,
		OperationID:  "op-effect",
		PhaseID:      "phase-a",
		LeaseID:      "lease-a",
		ProposalID:   "aprop-a",
		WorkMode:     "commit",
		Executor:     "native",
		Tool:         "exec",
		Command:      "git commit -m test",
		EffectKind:   "repo_or_history_mutation",
		EffectReason: "git commit",
		Status:       EffectAttemptStatusAttempted,
		EvidenceRefs: []string{"turn_run:11"},
		StartedAt:    now,
		UpdatedAt:    now,
	}
	first, err := store.UpsertEffectAttempt(input)
	if err != nil {
		t.Fatalf("UpsertEffectAttempt(first) err = %v", err)
	}
	input.Status = EffectAttemptStatusExecuted
	input.EvidenceRefs = []string{"turn_run:11", "execution_event:2"}
	input.CompletedAt = now.Add(time.Second)
	second, err := store.UpsertEffectAttempt(input)
	if err != nil {
		t.Fatalf("UpsertEffectAttempt(second) err = %v", err)
	}
	if first.AttemptID != second.AttemptID {
		t.Fatalf("attempt id changed from %q to %q", first.AttemptID, second.AttemptID)
	}
	if second.Status != EffectAttemptStatusExecuted || second.CompletedAt.IsZero() {
		t.Fatalf("attempt = %#v, want executed with completion timestamp", second)
	}
	if len(second.EvidenceRefs) != 2 {
		t.Fatalf("evidence refs = %#v, want deduped two refs", second.EvidenceRefs)
	}
}

func TestEffectAttemptTerminalStatusIsSticky(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7002, UserID: 0}
	now := time.Now().UTC()
	input := EffectAttemptInput{
		Key:         key,
		TurnRunID:   12,
		OperationID: "op-effect",
		PhaseID:     "phase-a",
		LeaseID:     "lease-a",
		ProposalID:  "aprop-a",
		WorkMode:    "workspace_write",
		Executor:    "native",
		Tool:        "exec",
		Command:     "sed -i s/a/b/ file.txt",
		EffectKind:  "workspace_mutation",
		Status:      EffectAttemptStatusVerified,
		StartedAt:   now,
		CompletedAt: now,
		UpdatedAt:   now,
	}
	verified, err := store.UpsertEffectAttempt(input)
	if err != nil {
		t.Fatalf("UpsertEffectAttempt(verified) err = %v", err)
	}
	input.Status = EffectAttemptStatusExecuted
	input.ErrorText = "late replay"
	got, err := store.UpsertEffectAttempt(input)
	if err != nil {
		t.Fatalf("UpsertEffectAttempt(replay) err = %v", err)
	}
	if got.Status != EffectAttemptStatusVerified || got.ErrorText != verified.ErrorText {
		t.Fatalf("attempt = %#v, want verified terminal status preserved", got)
	}
}

func TestUncertainEffectAttemptNextActionReplayIsIdempotent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7003, UserID: 1001}
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	input := EffectAttemptInput{
		AttemptID:   "attempt-uncertain-replay",
		Key:         key,
		TurnRunID:   13,
		OperationID: "op-effect",
		PhaseID:     "phase-a",
		LeaseID:     "lease-a",
		Executor:    "exec",
		Tool:        "exec",
		Command:     "git push origin main",
		EffectKind:  "external_account_command",
		Status:      EffectAttemptStatusUncertain,
		ErrorText:   "timeout after side effect",
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := store.UpsertEffectAttempt(input); err != nil {
		t.Fatalf("UpsertEffectAttempt(first) err = %v", err)
	}
	if _, err := store.UpsertEffectAttempt(input); err != nil {
		t.Fatalf("UpsertEffectAttempt(exact replay) err = %v", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != NextActionNeedsVerification || open[0].SubjectRef != input.AttemptID {
		t.Fatalf("open next actions = %#v, want one needs_verification action for attempt", open)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	nextStateEvents := 0
	for _, event := range events {
		if event.EventType == "workflow.next_state" {
			nextStateEvents++
		}
	}
	if nextStateEvents != 1 {
		t.Fatalf("workflow.next_state event count = %d, want exactly one for exact replay", nextStateEvents)
	}
}

func TestUncertainEffectAttemptNextActionClosesWhenResolved(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7004, UserID: 1001}
	now := time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC)
	input := EffectAttemptInput{
		AttemptID:   "attempt-uncertain-verified",
		Key:         key,
		TurnRunID:   14,
		OperationID: "op-effect",
		PhaseID:     "phase-a",
		LeaseID:     "lease-a",
		Executor:    "exec",
		Tool:        "exec",
		Command:     "git push origin main",
		EffectKind:  "external_account_command",
		Status:      EffectAttemptStatusUncertain,
		ErrorText:   "timeout after side effect",
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := store.UpsertEffectAttempt(input); err != nil {
		t.Fatalf("UpsertEffectAttempt(uncertain) err = %v", err)
	}
	input.Status = EffectAttemptStatusVerified
	input.ErrorText = ""
	input.CompletedAt = now.Add(time.Minute)
	input.UpdatedAt = now.Add(time.Minute)
	if _, err := store.UpsertEffectAttempt(input); err != nil {
		t.Fatalf("UpsertEffectAttempt(verified) err = %v", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions = %#v, want verification action closed after verified", open)
	}
}
