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
