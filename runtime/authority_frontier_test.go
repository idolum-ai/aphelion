//go:build linux

package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
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

func TestAuthorityFrontierStatusProjectsCadenceEvents(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 3, 13, 50, 0, 0, time.UTC)
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventAuthorityFrontierDelta,
		Stage:       "authority_frontier",
		Status:      "reserved",
		PayloadJSON: `{"target_admin_chat_id":1001,"allowance_id":"lookahead:one","shape_hash":"shape:repeat","next_action_record_id":"next:one"}`,
		CreatedAt:   now.Add(-20 * time.Second),
	}, {
		EventType:   core.ExecutionEventAuthorityFrontierDelta,
		Stage:       "authority_frontier",
		Status:      "open",
		PayloadJSON: `{"target_admin_chat_id":1001,"allowance_id":"lookahead:two","shape_hash":"shape:repeat","next_action_record_id":"next:two"}`,
		CreatedAt:   now.Add(-10 * time.Second),
	}, {
		EventType:   core.ExecutionEventAuthorityFrontierDelta,
		Stage:       "authority_frontier",
		Status:      "open",
		PayloadJSON: `{"target_admin_chat_id":2002,"allowance_id":"lookahead:other","shape_hash":"shape:repeat"}`,
		CreatedAt:   now.Add(-5 * time.Second),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}
	rt := &Runtime{store: store, authorityDiscoveryClock: func() time.Time { return now }}
	snapshot, err := rt.AuthorityFrontierStatus(context.Background(), 1001)
	if err != nil {
		t.Fatalf("AuthorityFrontierStatus() err = %v", err)
	}
	if len(snapshot.Recent) != 2 {
		t.Fatalf("recent = %#v, want only admin 1001 frontier beats", snapshot.Recent)
	}
	if snapshot.Recent[0].Status != "open" || snapshot.Recent[0].InterArrivalSeconds != 10 || snapshot.Recent[0].RepeatedShapeOrdinal != 2 {
		t.Fatalf("latest repeat event = %#v, want 10s repeated-shape cadence", snapshot.Recent[0])
	}
}

func TestAuthorityFrontierStatusStampsExpiredUnreviewedSilence(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := session.SessionKey{ChatID: 1001, UserID: 0, Scope: telegramDMScopeRef(1001)}
	now := time.Date(2026, 7, 3, 14, 0, 0, 0, time.UTC)
	entry, err := store.RecordIdentificationLedgerEntry(session.IdentificationLedgerEntryInput{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		StepRef:     "step:sleeping-operator",
		ShapeHash:   "shape:sleep",
		LabelRef:    "authbundle-sleep",
		Status:      session.IdentificationLedgerStatusProposed,
		ExpiresAt:   now.Add(-time.Minute),
		CreatedAt:   now.Add(-time.Hour),
		UpdatedAt:   now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("RecordIdentificationLedgerEntry() err = %v", err)
	}
	allowance, reserved, err := store.ReserveLookaheadAllowance(1001, 301, session.SessionIDForKey(key), session.SessionIDForKey(key), MaxOutstandingLookaheadApprovalFrontiers, now.Add(-time.Hour), now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ReserveLookaheadAllowance() err = %v", err)
	}
	if !reserved {
		t.Fatal("ReserveLookaheadAllowance() reserved = false")
	}
	if err := store.BindLookaheadAllowance(allowance.AllowanceID, "next:sleep", entry.EntryID, now.Add(-59*time.Minute)); err != nil {
		t.Fatalf("BindLookaheadAllowance() err = %v", err)
	}

	rt := &Runtime{store: store, authorityDiscoveryClock: func() time.Time { return now }}
	snapshot, err := rt.AuthorityFrontierStatus(context.Background(), 1001)
	if err != nil {
		t.Fatalf("AuthorityFrontierStatus() err = %v", err)
	}
	if snapshot.Expired != 1 || len(snapshot.Recent) == 0 || snapshot.Recent[0].Status != "expired_unreviewed" {
		t.Fatalf("snapshot = %#v, want expired slot and silence beat", snapshot)
	}
	projection := authorityFrontierLedgerProjectionByEntryID(t, store, key, entry.EntryID)
	if projection.Entry.Status != session.IdentificationLedgerStatusExpired {
		t.Fatalf("entry status = %q, want expired", projection.Entry.Status)
	}
	found := false
	for _, observation := range projection.Observations {
		if observation.ActorKind == "operator_absence" &&
			observation.ActorPrincipal == "silence" &&
			observation.ActorAction == "expired_unreviewed" &&
			strings.Contains(observation.EvidenceRef, allowance.AllowanceID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("observations = %#v, want expired_unreviewed silence actor", projection.Observations)
	}
}

func authorityFrontierLedgerProjectionByEntryID(t *testing.T, store *session.SQLiteStore, key session.SessionKey, entryID string) session.IdentificationLedgerProjection {
	t.Helper()
	projections, err := store.IdentificationLedgerEntries(session.IdentificationLedgerQuery{
		PlanID:      session.IdentificationPlanIDForSession(session.SessionIDForKey(key)),
		PlanVersion: session.IdentificationDefaultPlanVersion,
		SessionID:   session.SessionIDForKey(key),
		Limit:       20,
	})
	if err != nil {
		t.Fatalf("IdentificationLedgerEntries() err = %v", err)
	}
	for _, projection := range projections {
		if projection.Entry.EntryID == entryID {
			return projection
		}
	}
	t.Fatalf("entry %s not found in %#v", entryID, projections)
	return session.IdentificationLedgerProjection{}
}
