//go:build linux

package session

import (
	"path/filepath"
	"sync"
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

func TestReserveLookaheadAllowanceConcurrentCap(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	seed, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(seed) err = %v", err)
	}
	defer seed.Close()

	adminChatID := int64(2468)
	now := time.Date(2026, 7, 3, 12, 15, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		if _, reserved, err := seed.ReserveLookaheadAllowance(adminChatID, int64(100+i), "session:source", "session:target", 5, now.Add(time.Duration(i)*time.Millisecond), now.Add(time.Hour)); err != nil {
			t.Fatalf("ReserveLookaheadAllowance(seed %d) err = %v", i, err)
		} else if !reserved {
			t.Fatalf("ReserveLookaheadAllowance(seed %d) reserved = false", i)
		}
	}

	const contenders = 8
	stores := make([]*SQLiteStore, 0, contenders)
	for i := 0; i < contenders; i++ {
		store, err := NewSQLiteStore(dbPath)
		if err != nil {
			t.Fatalf("NewSQLiteStore(contender %d) err = %v", i, err)
		}
		defer store.Close()
		stores = append(stores, store)
	}
	start := make(chan struct{})
	results := make(chan bool, contenders)
	errs := make(chan error, contenders)
	var wg sync.WaitGroup
	for i, store := range stores {
		wg.Add(1)
		go func(i int, store *SQLiteStore) {
			defer wg.Done()
			<-start
			_, reserved, err := store.ReserveLookaheadAllowance(adminChatID, int64(200+i), "session:source", "session:target", 5, now.Add(time.Second+time.Duration(i)*time.Nanosecond), now.Add(time.Hour))
			if err != nil {
				errs <- err
				return
			}
			results <- reserved
		}(i, store)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("ReserveLookaheadAllowance(concurrent) err = %v", err)
	}
	var reserved int
	for ok := range results {
		if ok {
			reserved++
		}
	}
	if reserved != 1 {
		t.Fatalf("concurrent reservations = %d, want exactly one slot admitted from 4/5", reserved)
	}
	count, err := seed.OutstandingLookaheadApprovalFrontierCountAt(adminChatID, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("OutstandingLookaheadApprovalFrontierCountAt() err = %v", err)
	}
	if count != 5 {
		t.Fatalf("outstanding count = %d, want capped at 5", count)
	}
	if _, reserved, err := seed.ReserveLookaheadAllowance(adminChatID, 999, "session:source", "session:target", 5, now.Add(3*time.Second), now.Add(time.Hour)); err != nil {
		t.Fatalf("ReserveLookaheadAllowance(full) err = %v", err)
	} else if reserved {
		t.Fatal("ReserveLookaheadAllowance(full) reserved = true, want cap enforced")
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

func TestExpireLookaheadAllowancesForAdminStampsExpiredUnreviewed(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 7, 3, 12, 40, 0, 0, time.UTC)
	expired, reserved, err := store.ReserveLookaheadAllowance(7777, 201, "session:source", "session:target", 5, now.Add(-time.Hour), now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(expired) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(expired) reserved = false")
	}
	fresh, reserved, err := store.ReserveLookaheadAllowance(7777, 202, "session:source", "session:target", 5, now.Add(-time.Minute), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(fresh) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(fresh) reserved = false")
	}

	records, err := store.ExpireLookaheadAllowancesForAdmin(7777, now)
	if err != nil {
		t.Fatalf("ExpireLookaheadAllowancesForAdmin() err = %v", err)
	}
	if len(records) != 1 || records[0].AllowanceID != expired.AllowanceID || records[0].Status != LookaheadAllowanceExpired || records[0].Reason != "expired_unreviewed" {
		t.Fatalf("expired records = %#v, want one expired_unreviewed row", records)
	}
	all, err := store.LookaheadAllowancesForAdmin(7777, 10)
	if err != nil {
		t.Fatalf("LookaheadAllowancesForAdmin() err = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("allowances = %#v, want expired plus fresh", all)
	}
	statusByID := map[string]LookaheadAllowanceStatus{}
	for _, record := range all {
		statusByID[record.AllowanceID] = record.Status
	}
	if statusByID[expired.AllowanceID] != LookaheadAllowanceExpired || statusByID[fresh.AllowanceID] != LookaheadAllowanceReserved {
		t.Fatalf("statuses = %#v, want expired row and fresh reserved row", statusByID)
	}
}
