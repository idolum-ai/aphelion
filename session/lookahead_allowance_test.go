//go:build linux

package session

import (
	"testing"
	"time"
)

func TestReserveLookaheadAllowanceUsesUniqueIDsForSameInstant(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	first, reserved, err := store.ReserveLookaheadAllowance(1234, 77, "session:source", "session:target", 5, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(first) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(first) reserved = false")
	}
	second, reserved, err := store.ReserveLookaheadAllowance(1234, 77, "session:source", "session:target", 5, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(second) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(second) reserved = false")
	}
	if first.AllowanceID == second.AllowanceID {
		t.Fatalf("allowance IDs are equal for same-instant reservations: %q", first.AllowanceID)
	}
	count, err := store.OutstandingLookaheadApprovalFrontierCountAt(1234, now)
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCountAt() err = %v", err)
	}
	if count != 2 {
		t.Fatalf("outstanding allowances = %d, want 2", count)
	}
}

func TestBindLookaheadAllowanceOrReleaseOnFailureFreesSlot(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 7, 3, 12, 10, 0, 0, time.UTC)
	allowance, reserved, err := store.ReserveLookaheadAllowance(4321, 88, "session:source", "session:target", 1, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance() err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance() reserved = false")
	}
	if err := store.BindLookaheadAllowanceOrReleaseOnFailure(allowance.AllowanceID, "next-action:one", "", "bind_error", now.Add(time.Second)); err == nil {
		t.Fatal("BindLookaheadAllowanceOrReleaseOnFailure() err = nil, want bind failure")
	}

	count, err := store.OutstandingLookaheadApprovalFrontierCountAt(4321, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCountAt() err = %v", err)
	}
	if count != 0 {
		t.Fatalf("outstanding allowances after bind failure = %d, want 0", count)
	}
	var status, reason string
	if err := store.db.QueryRow(`
		SELECT status, reason
		FROM authority_lookahead_allowances
		WHERE allowance_id = ?
	`, allowance.AllowanceID).Scan(&status, &reason); err != nil {
		t.Fatalf("query allowance status err = %v", err)
	}
	if status != string(LookaheadAllowanceReleased) || reason != "bind_error" {
		t.Fatalf("allowance status/reason = %q/%q, want released/bind_error", status, reason)
	}

	if _, reserved, err := store.ReserveLookaheadAllowance(4321, 89, "session:source", "session:target", 1, now.Add(3*time.Second), now.Add(time.Hour)); err != nil {
		t.Fatalf("ReserveLookaheadAllowance(after release) err = %v", err)
	} else if !reserved {
		t.Fatal("ReserveLookaheadAllowance(after release) reserved = false, leaked slot")
	}
}

func TestLookaheadAllowancesForAdminProjectsUnresolvedRows(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 7, 3, 12, 20, 0, 0, time.UTC)
	open, reserved, err := store.ReserveLookaheadAllowance(5555, 101, "session:source", "session:target", 5, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(open) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(open) reserved = false")
	}
	if err := store.BindLookaheadAllowance(open.AllowanceID, "next-action:open", "ident:open", now.Add(time.Second)); err != nil {
		t.Fatalf("BindLookaheadAllowance() err = %v", err)
	}
	expired, reserved, err := store.ReserveLookaheadAllowance(5555, 102, "session:source", "session:target", 5, now.Add(2*time.Second), now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(expired) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(expired) reserved = false")
	}
	released, reserved, err := store.ReserveLookaheadAllowance(5555, 103, "session:source", "session:target", 5, now.Add(3*time.Second), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(released) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(released) reserved = false")
	}
	if err := store.ReleaseLookaheadAllowance(released.AllowanceID, "test_release", now.Add(4*time.Second)); err != nil {
		t.Fatalf("ReleaseLookaheadAllowance() err = %v", err)
	}

	records, err := store.LookaheadAllowancesForAdmin(5555, 10)
	if err != nil {
		t.Fatalf("LookaheadAllowancesForAdmin() err = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %#v, want open plus expired unresolved rows", records)
	}
	if records[0].AllowanceID != open.AllowanceID || records[0].Status != LookaheadAllowanceOpen {
		t.Fatalf("records[0] = %#v, want bound open allowance", records[0])
	}
	if records[1].AllowanceID != expired.AllowanceID || records[1].Status != LookaheadAllowanceReserved {
		t.Fatalf("records[1] = %#v, want unresolved expired allowance", records[1])
	}
}
