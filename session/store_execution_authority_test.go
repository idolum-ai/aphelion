//go:build linux

package session

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutionRunAuthorityIsImmutableAfterAdmission(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 9101, UserID: 1001}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "admit authority")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	record := testExecutionRunAuthorityRecord(run, "lease-immutable")
	inserted, err := store.UpsertExecutionRunAuthority(record)
	if err != nil {
		t.Fatalf("UpsertExecutionRunAuthority(insert) err = %v", err)
	}
	idempotent, err := store.UpsertExecutionRunAuthority(record)
	if err != nil {
		t.Fatalf("UpsertExecutionRunAuthority(idempotent) err = %v", err)
	}
	if !executionRunAuthoritySame(idempotent, inserted) {
		t.Fatalf("idempotent record = %#v, want %#v", idempotent, inserted)
	}

	changed := record
	changed.Principal = "telegram:2002"
	changed.ContinuationLeaseID = "lease-rewritten"
	if _, err := store.UpsertExecutionRunAuthority(changed); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("UpsertExecutionRunAuthority(rewrite) err = %v, want immutable rejection", err)
	}
	got, ok, err := store.ExecutionRunAuthority(run.ID)
	if err != nil {
		t.Fatalf("ExecutionRunAuthority() err = %v", err)
	}
	if !ok {
		t.Fatal("ExecutionRunAuthority() ok=false, want original record")
	}
	if got.Principal != record.Principal || got.ContinuationLeaseID != record.ContinuationLeaseID {
		t.Fatalf("stored authority = %#v, want original principal and lease", got)
	}
}

func TestExecutionRunAuthorityRejectsSecondRunningConsumerForLease(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 9102, UserID: 1001}
	runA, err := store.BeginTurnRun(key, TurnRunKindInteractive, "first admitted run")
	if err != nil {
		t.Fatalf("BeginTurnRun(first) err = %v", err)
	}
	runB, err := store.BeginTurnRun(key, TurnRunKindInteractive, "second admitted run")
	if err != nil {
		t.Fatalf("BeginTurnRun(second) err = %v", err)
	}

	recordA := testExecutionRunAuthorityRecord(runA, "lease-single-consumer")
	if _, err := store.UpsertExecutionRunAuthority(recordA); err != nil {
		t.Fatalf("UpsertExecutionRunAuthority(first) err = %v", err)
	}
	recordB := testExecutionRunAuthorityRecord(runB, "lease-single-consumer")
	if _, err := store.UpsertExecutionRunAuthority(recordB); err == nil || !strings.Contains(err.Error(), "already bound to running turn run") {
		t.Fatalf("UpsertExecutionRunAuthority(second) err = %v, want running lease binding rejection", err)
	}
}

func TestExecutionRunAuthorityClaimsSingleTurnLeaseConcurrently(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 9103, UserID: 1001}
	runA, err := store.BeginTurnRun(key, TurnRunKindInteractive, "first concurrent run")
	if err != nil {
		t.Fatalf("BeginTurnRun(first) err = %v", err)
	}
	runB, err := store.BeginTurnRun(key, TurnRunKindInteractive, "second concurrent run")
	if err != nil {
		t.Fatalf("BeginTurnRun(second) err = %v", err)
	}

	start := make(chan struct{})
	var successes atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for _, run := range []*TurnRun{runA, runB} {
		run := run
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := store.UpsertExecutionRunAuthority(testExecutionRunAuthorityRecord(run, "lease-concurrent-single-consumer")); err != nil {
				failures.Add(1)
				return
			}
			successes.Add(1)
		}()
	}
	close(start)
	wg.Wait()

	if successes.Load() != 1 || failures.Load() != 1 {
		t.Fatalf("concurrent claims successes=%d failures=%d, want exactly one winner", successes.Load(), failures.Load())
	}
}

func TestExecutionRunAuthorityRejectsDelayedStaleSingleTurnAdmission(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 9104, UserID: 1001}
	runA, err := store.BeginTurnRun(key, TurnRunKindInteractive, "first delayed run")
	if err != nil {
		t.Fatalf("BeginTurnRun(first) err = %v", err)
	}
	runB, err := store.BeginTurnRun(key, TurnRunKindInteractive, "stale delayed run")
	if err != nil {
		t.Fatalf("BeginTurnRun(second) err = %v", err)
	}
	recordA := testExecutionRunAuthorityRecord(runA, "lease-delayed-single-consumer")
	recordB := testExecutionRunAuthorityRecord(runB, "lease-delayed-single-consumer")
	if _, err := store.UpsertExecutionRunAuthority(recordA); err != nil {
		t.Fatalf("UpsertExecutionRunAuthority(first) err = %v", err)
	}
	if err := store.CompleteTurnRun(runA.ID, TurnRunStatusCompleted, "lease consumed by first run"); err != nil {
		t.Fatalf("CompleteTurnRun(first) err = %v", err)
	}
	if _, err := store.UpsertExecutionRunAuthority(recordB); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("UpsertExecutionRunAuthority(stale delayed) err = %v, want prior claim rejection", err)
	}
}

func testExecutionRunAuthorityRecord(run *TurnRun, leaseID string) ExecutionRunAuthority {
	return ExecutionRunAuthority{
		TurnRunID:           run.ID,
		SessionID:           run.SessionID,
		ChatID:              run.ChatID,
		UserID:              run.UserID,
		Scope:               run.Scope,
		Principal:           "telegram:1001",
		PrincipalRole:       "admin",
		ExecutionSpecies:    "test",
		LeaseKind:           ExecutionAuthorityLeaseKindContinuation,
		ContinuationLeaseID: strings.TrimSpace(leaseID),
		LeaseStatus:         string(ContinuationLeaseStatusActive),
		LeaseClass:          ContinuationLeaseClassChildWake,
		LeaseAllowedActions: []string{"wake_named_child"},
		LeaseConstraints:    map[string]string{"agent_id": "child-alpha"},
		LeaseRemainingTurns: 1,
		LeaseExpiresAt:      time.Now().UTC().Add(time.Hour),
		AdmittedAt:          time.Now().UTC(),
	}
}
