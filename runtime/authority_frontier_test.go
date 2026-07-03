//go:build linux

package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestAuthorityFrontierStatusProjectsFiveSlotMeter(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 3, 13, 20, 0, 0, time.UTC)
	open, reserved, err := store.ReserveLookaheadAllowance(1001, 201, "telegram_dm:1001", "telegram_dm:1001", MaxOutstandingLookaheadApprovalFrontiers, now.Add(-time.Minute), now.Add(29*time.Minute))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance(open) err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance(open) reserved = false")
	}
	if err := store.BindLookaheadAllowance(open.AllowanceID, "next-action:frontier", "ident:frontier", now.Add(-30*time.Second)); err != nil {
		t.Fatalf("BindLookaheadAllowance() err = %v", err)
	}
	if _, reserved, err := store.ReserveLookaheadAllowance(1001, 202, "telegram_dm:1001", "telegram_dm:1001", MaxOutstandingLookaheadApprovalFrontiers, now.Add(-2*time.Minute), now.Add(-time.Second)); err != nil {
		t.Fatalf("ReserveLookaheadAllowance(expired) err = %v", err)
	} else if !reserved {
		t.Fatal("ReserveLookaheadAllowance(expired) reserved = false")
	}

	rt := &Runtime{
		store: store,
		authorityDiscoveryClock: func() time.Time {
			return now
		},
	}
	snapshot, err := rt.AuthorityFrontierStatus(context.Background(), 1001)
	if err != nil {
		t.Fatalf("AuthorityFrontierStatus() err = %v", err)
	}
	if snapshot.Budget != MaxOutstandingLookaheadApprovalFrontiers || len(snapshot.Slots) != MaxOutstandingLookaheadApprovalFrontiers {
		t.Fatalf("budget/slots = %d/%d, want %d slots", snapshot.Budget, len(snapshot.Slots), MaxOutstandingLookaheadApprovalFrontiers)
	}
	if snapshot.Used != 1 || snapshot.Open != 1 || snapshot.Expired != 1 || snapshot.Empty != 3 {
		t.Fatalf("snapshot counts = used:%d open:%d expired:%d empty:%d, want 1/1/1/3", snapshot.Used, snapshot.Open, snapshot.Expired, snapshot.Empty)
	}
	if snapshot.Slots[0].Status != "expired" || snapshot.Slots[0].TTLSeconds != 0 {
		t.Fatalf("slot[0] = %#v, want expired first unresolved row", snapshot.Slots[0])
	}
	if snapshot.Slots[1].Status != string(session.LookaheadAllowanceOpen) ||
		snapshot.Slots[1].NextActionRecordID != "next-action:frontier" ||
		snapshot.Slots[1].EntryID != "ident:frontier" ||
		snapshot.Slots[1].TTLSeconds <= 0 {
		t.Fatalf("slot[1] = %#v, want open bound allowance with ttl", snapshot.Slots[1])
	}
	for i := 2; i < len(snapshot.Slots); i++ {
		if snapshot.Slots[i].Status != "empty" {
			t.Fatalf("slot[%d] = %#v, want empty", i, snapshot.Slots[i])
		}
	}
}
