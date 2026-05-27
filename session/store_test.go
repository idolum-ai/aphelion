//go:build linux

package session

import (
	"github.com/idolum-ai/aphelion/core"
	"testing"
	"time"
)

func TestSQLiteStoreCreatesReviewEventsTable(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	var count int
	err := store.db.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'review_events'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 1 {
		t.Fatalf("review_events table count = %d, want 1", count)
	}
}

func TestSearchMessagesFiltersByScopeAndReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	for _, tc := range []struct {
		key      SessionKey
		turn     int
		userText string
		reply    string
	}{
		{SessionKey{ChatID: 1, UserID: 0}, 1, "alpha first", "reply one"},
		{SessionKey{ChatID: 1, UserID: 0}, 2, "alpha second", "reply two"},
		{SessionKey{ChatID: 2, UserID: 0}, 1, "beta alpha", "reply three"},
	} {
		sess, err := store.Load(tc.key)
		if err != nil {
			t.Fatalf("Load(%v) err = %v", tc.key, err)
		}
		sess.TurnCount = tc.turn
		if err := store.Save(sess, []Message{
			{Role: "user", Content: tc.userText, TurnIndex: tc.turn},
			{Role: "assistant", Content: tc.reply, FloorContent: tc.reply, TurnIndex: tc.turn},
		}, core.TokenUsage{}); err != nil {
			t.Fatalf("Save(%v) err = %v", tc.key, err)
		}
	}

	allHits, err := store.SearchMessages("alpha", 10, nil)
	if err != nil {
		t.Fatalf("SearchMessages(all) err = %v", err)
	}
	if len(allHits) != 3 {
		t.Fatalf("all hits len = %d, want 3", len(allHits))
	}
	if allHits[0].ChatID != 2 || allHits[1].TurnIndex != 2 {
		t.Fatalf("all hits ordering = %#v, want newest first", allHits)
	}

	scope := SessionKey{ChatID: 1, UserID: 0}
	scopedHits, err := store.SearchMessages("alpha", 10, &scope)
	if err != nil {
		t.Fatalf("SearchMessages(scoped) err = %v", err)
	}
	if len(scopedHits) != 2 {
		t.Fatalf("scoped hits len = %d, want 2", len(scopedHits))
	}
	for _, hit := range scopedHits {
		if hit.ChatID != 1 {
			t.Fatalf("scoped hit chat id = %d, want 1", hit.ChatID)
		}
	}
}

func TestMessagesInWindowReturnsChronologicalEntries(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "window-early", TurnIndex: 1},
		{Role: "user", Content: "window-mid", TurnIndex: 1},
		{Role: "user", Content: "window-late", TurnIndex: 1},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	times := map[string]time.Time{
		"window-early": time.Date(2026, time.April, 20, 9, 0, 0, 0, time.UTC),
		"window-mid":   time.Date(2026, time.April, 20, 13, 30, 0, 0, time.UTC),
		"window-late":  time.Date(2026, time.April, 21, 10, 0, 0, 0, time.UTC),
	}
	for content, at := range times {
		if _, err := store.db.Exec(`UPDATE messages SET created_at = ? WHERE content = ?`, at.Format(time.RFC3339Nano), content); err != nil {
			t.Fatalf("retime message %q err = %v", content, err)
		}
	}

	start := time.Date(2026, time.April, 20, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.April, 21, 0, 0, 0, 0, time.UTC)
	hits, err := store.MessagesInWindow(start, end, 10)
	if err != nil {
		t.Fatalf("MessagesInWindow() err = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("MessagesInWindow() len = %d, want 2", len(hits))
	}
	if hits[0].Content != "window-early" || hits[1].Content != "window-mid" {
		t.Fatalf("MessagesInWindow() ordering/content = %#v, want early then mid", hits)
	}
	if !hits[0].CreatedAt.Before(hits[1].CreatedAt) {
		t.Fatalf("MessagesInWindow() created_at ordering = %s then %s, want ascending", hits[0].CreatedAt, hits[1].CreatedAt)
	}
}

func TestPlanStateRoundTripAndUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 77, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "77"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.PlanState = PlanState{
		Explanation: "Inspect before editing.",
		Steps: []PlanStep{
			{Step: "Inspect the relevant files.", Status: PlanStatusInProgress},
			{Step: "Patch the bug.", Status: PlanStatusPending},
		},
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "planned", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.PlanState.Explanation != "Inspect before editing." {
		t.Fatalf("Explanation = %q, want persisted explanation", reloaded.PlanState.Explanation)
	}
	if len(reloaded.PlanState.Steps) != 2 {
		t.Fatalf("steps len = %d, want 2", len(reloaded.PlanState.Steps))
	}
	if reloaded.PlanState.Steps[0].Status != PlanStatusInProgress {
		t.Fatalf("first step status = %q, want in_progress", reloaded.PlanState.Steps[0].Status)
	}

	updated := PlanState{
		Explanation: "Execution complete.",
		Steps: []PlanStep{
			{Step: "Inspect the relevant files.", Status: PlanStatusCompleted},
			{Step: "Patch the bug.", Status: PlanStatusCompleted},
		},
	}
	if err := store.UpdatePlanState(key, updated); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}

	planState, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState() err = %v", err)
	}
	if planState.Explanation != "Execution complete." {
		t.Fatalf("updated explanation = %q, want updated value", planState.Explanation)
	}
	if len(planState.Steps) != 2 || planState.Steps[1].Status != PlanStatusCompleted {
		t.Fatalf("updated steps = %#v, want completed steps", planState.Steps)
	}
}

func TestContinuationStateRoundTripAndUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	key := SessionKey{ChatID: 901, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.ContinuationState = ContinuationState{
		Status:         ContinuationStatusPending,
		Objective:      "implement continuation controls",
		StageSummary:   "Attach approval UI",
		RemainingTurns: 1,
		ApprovedBy:     1002,
		PersonaIntent: ContinuationIntent{
			Decision:  ContinuationIntentDecisionContinue,
			Rationale: "persona asks to continue",
		},
		GovernorIntent: ContinuationIntent{
			Decision:  ContinuationIntentDecisionContinue,
			Rationale: "governor ratified the next step",
			Ratified:  true,
		},
	}
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.ContinuationState.Status != ContinuationStatusPending {
		t.Fatalf("status = %q, want pending", reloaded.ContinuationState.Status)
	}
	updated := ContinuationState{
		Status:         ContinuationStatusApproved,
		Objective:      "implement continuation controls",
		RemainingTurns: 1,
		ApprovedBy:     1002,
		PersonaIntent: ContinuationIntent{
			Decision:   ContinuationIntentDecisionContinue,
			Rationale:  "persona asks to continue",
			Confidence: "high",
		},
		GovernorIntent: ContinuationIntent{
			Decision:    ContinuationIntentDecisionContinue,
			Rationale:   "governor ratified the next step",
			Constraints: "bounded to this turn",
			Confidence:  "high",
			Ratified:    true,
		},
		HandshakeBlockedReason: " ",
	}
	if err := store.UpdateContinuationState(key, updated); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != ContinuationStatusApproved {
		t.Fatalf("status = %q, want approved", got.Status)
	}
	if got.ApprovedBy != 1002 {
		t.Fatalf("approved_by = %d, want 1002", got.ApprovedBy)
	}
	if got.PersonaIntent.Decision != ContinuationIntentDecisionContinue {
		t.Fatalf("persona intent decision = %q, want continue", got.PersonaIntent.Decision)
	}
	if got.GovernorIntent.Decision != ContinuationIntentDecisionContinue {
		t.Fatalf("governor intent decision = %q, want continue", got.GovernorIntent.Decision)
	}
	if !got.GovernorIntent.Ratified {
		t.Fatal("governor intent ratified = false, want true")
	}
	if got.HandshakeBlockedReason != "" {
		t.Fatalf("handshake blocked reason = %q, want empty after normalize", got.HandshakeBlockedReason)
	}
}

func TestActiveApprovalWindowOfferForSourceExcludesUsedOffers(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-source-used",
		ChatID:             7002,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7002",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-source-used",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	seedApprovalWindowOfferUsedForTest(t, store, offer.ID, now.Add(time.Second))
	if got, ok, err := store.ActiveApprovalWindowOfferForSource(7002, ApprovalWindowOfferSourceDecision, "decision-source-used", now.Add(2*time.Second)); err != nil || ok {
		t.Fatalf("ActiveApprovalWindowOfferForSource() = %#v, %t, %v; want no used offer", got, ok, err)
	}
}

func TestActiveApprovalWindowOfferForSourceReturnsOpenedUsedOffer(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-source-opened",
		ChatID:             7003,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7003",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-source-opened",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	if _, err := store.CreateOperatorAutonomyOverride(OperatorAutonomyOverride{
		ID:          "override-opened",
		AdminUserID: 1001,
		ChatID:      7003,
		ScopeKind:   string(ScopeKindTelegramDM),
		ScopeID:     "7003",
		Mode:        "leased",
		Scope:       OperatorAutoApprovalScopeAll,
		Reason:      "inline approval window",
		CreatedAt:   now.Add(time.Second),
		ExpiresAt:   now.Add(time.Hour),
		UpdatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride() err = %v", err)
	}
	if _, err := store.CreateOperatorAutoApprovalLease(OperatorAutoApprovalLease{
		ID:          "lease-opened",
		AdminUserID: 1001,
		ChatID:      7003,
		ScopeKind:   string(ScopeKindTelegramDM),
		ScopeID:     "7003",
		Scope:       OperatorAutoApprovalScopeAll,
		Reason:      "inline approval window",
		CreatedAt:   now.Add(time.Second),
		ExpiresAt:   now.Add(time.Hour),
		UpdatedAt:   now.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease() err = %v", err)
	}
	seedApprovalWindowOfferUsedForTest(t, store, offer.ID, now.Add(time.Second))
	seedApprovalWindowOfferOpenedForTest(t, store, offer.ID, "lease-opened", "override-opened", now.Add(2*time.Second))
	got, ok, err := store.ActiveApprovalWindowOfferForSource(7003, ApprovalWindowOfferSourceDecision, "decision-source-opened", now.Add(3*time.Second))
	if err != nil || !ok {
		t.Fatalf("ActiveApprovalWindowOfferForSource() = %#v, %t, %v; want opened used offer", got, ok, err)
	}
	if got.ID != offer.ID || got.OpenedLeaseID != "lease-opened" || got.OpenedOverrideID != "override-opened" {
		t.Fatalf("got = %#v, want opened offer binding", got)
	}
}

func TestActiveApprovalWindowOfferForSourceExcludesExpiredOpenedOffer(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-source-expired-opened",
		ChatID:             7004,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7004",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-source-expired-opened",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(24 * time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	if _, err := store.CreateOperatorAutonomyOverride(OperatorAutonomyOverride{
		ID:          "override-expired-opened",
		AdminUserID: 1001,
		ChatID:      7004,
		ScopeKind:   string(ScopeKindTelegramDM),
		ScopeID:     "7004",
		Mode:        "leased",
		Scope:       OperatorAutoApprovalScopeAll,
		Reason:      "inline approval window",
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Minute),
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride() err = %v", err)
	}
	if _, err := store.CreateOperatorAutoApprovalLease(OperatorAutoApprovalLease{
		ID:          "lease-expired-opened",
		AdminUserID: 1001,
		ChatID:      7004,
		ScopeKind:   string(ScopeKindTelegramDM),
		ScopeID:     "7004",
		Scope:       OperatorAutoApprovalScopeAll,
		Reason:      "inline approval window",
		CreatedAt:   now,
		ExpiresAt:   now.Add(time.Minute),
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease() err = %v", err)
	}
	seedApprovalWindowOfferUsedForTest(t, store, offer.ID, now.Add(time.Second))
	seedApprovalWindowOfferOpenedForTest(t, store, offer.ID, "lease-expired-opened", "override-expired-opened", now.Add(2*time.Second))
	if got, ok, err := store.ActiveApprovalWindowOfferForSource(7004, ApprovalWindowOfferSourceDecision, "decision-source-expired-opened", now.Add(2*time.Minute)); err != nil || ok {
		t.Fatalf("ActiveApprovalWindowOfferForSource(expired) = %#v, %t, %v; want no active opened offer", got, ok, err)
	}
}

func TestRevokeOperatorApprovalWindowByIDsDoesNotRevokeNewerWindow(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	seedWindow := func(leaseID, overrideID string, created time.Time) {
		t.Helper()
		if _, err := store.CreateOperatorAutonomyOverride(OperatorAutonomyOverride{
			ID:          overrideID,
			AdminUserID: 1001,
			ChatID:      7005,
			ScopeKind:   string(ScopeKindTelegramDM),
			ScopeID:     "7005",
			Mode:        "leased",
			Scope:       OperatorAutoApprovalScopeAll,
			Reason:      "inline approval window",
			CreatedAt:   created,
			ExpiresAt:   created.Add(time.Hour),
			UpdatedAt:   created,
		}); err != nil {
			t.Fatalf("CreateOperatorAutonomyOverride(%s) err = %v", overrideID, err)
		}
		if _, err := store.CreateOperatorAutoApprovalLease(OperatorAutoApprovalLease{
			ID:          leaseID,
			AdminUserID: 1001,
			ChatID:      7005,
			ScopeKind:   string(ScopeKindTelegramDM),
			ScopeID:     "7005",
			Scope:       OperatorAutoApprovalScopeAll,
			Reason:      "inline approval window",
			CreatedAt:   created,
			ExpiresAt:   created.Add(time.Hour),
			UpdatedAt:   created,
		}); err != nil {
			t.Fatalf("CreateOperatorAutoApprovalLease(%s) err = %v", leaseID, err)
		}
	}
	seedWindow("lease-old", "override-old", now)
	seedWindow("lease-new", "override-new", now.Add(time.Minute))

	leases, overrides, revoked, err := store.RevokeOperatorApprovalWindowByIDs(7005, 1001, string(ScopeKindTelegramDM), "7005", "lease-missing", "override-old", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("RevokeOperatorApprovalWindowByIDs(missing) err = %v", err)
	}
	if revoked || len(leases) != 0 || len(overrides) != 0 {
		t.Fatalf("missing revoke = leases:%#v overrides:%#v revoked:%v, want CAS miss", leases, overrides, revoked)
	}
	active, err := store.ActiveOperatorAutoApprovalLeasesForScope(7005, string(ScopeKindTelegramDM), "7005", now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("active leases after CAS miss = %#v, want both windows preserved", active)
	}

	leases, overrides, revoked, err = store.RevokeOperatorApprovalWindowByIDs(7005, 1001, string(ScopeKindTelegramDM), "7005", "lease-old", "override-old", now.Add(4*time.Minute))
	if err != nil || !revoked {
		t.Fatalf("RevokeOperatorApprovalWindowByIDs(old) = leases:%#v overrides:%#v revoked:%v err:%v, want revoke", leases, overrides, revoked, err)
	}
	active, err = store.ActiveOperatorAutoApprovalLeasesForScope(7005, string(ScopeKindTelegramDM), "7005", now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope(after revoke) err = %v", err)
	}
	if len(active) != 1 || active[0].ID != "lease-new" {
		t.Fatalf("active leases after exact revoke = %#v, want newer window only", active)
	}
}

func TestCloseApprovalWindowOfferIfOpenedRequiresExpectedBinding(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-close-binding",
		ChatID:             7006,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7006",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-close-binding",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	seedApprovalWindowOfferUsedForTest(t, store, offer.ID, now.Add(time.Second))
	seedApprovalWindowOfferOpenedForTest(t, store, offer.ID, "lease-new", "override-new", now.Add(2*time.Second))
	if closed, ok, err := store.CloseApprovalWindowOfferIfOpened(offer.ID, "lease-old", "override-old", now.Add(3*time.Second)); err != nil || ok {
		t.Fatalf("CloseApprovalWindowOfferIfOpened(stale) = %#v, %t, %v; want CAS miss", closed, ok, err)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if !stored.ClosedAt.IsZero() {
		t.Fatalf("stored.ClosedAt = %s, want open after stale close miss", stored.ClosedAt)
	}
	if closed, ok, err := store.CloseApprovalWindowOfferIfOpened(offer.ID, "lease-new", "override-new", now.Add(4*time.Second)); err != nil || !ok {
		t.Fatalf("CloseApprovalWindowOfferIfOpened(current) = %#v, %t, %v; want close", closed, ok, err)
	}
}

func TestCloseUnusedApprovalWindowOfferDoesNotCloseOpenedOffer(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	unused, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-close-unused",
		ChatID:             7007,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7007",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-close-unused",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer(unused) err = %v", err)
	}
	if _, ok, err := store.CloseUnusedApprovalWindowOffer(unused.ID, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("CloseUnusedApprovalWindowOffer(unused) ok=%t err=%v", ok, err)
	}
	opened, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-close-opened",
		ChatID:             7007,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7007",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-close-opened",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer(opened) err = %v", err)
	}
	seedApprovalWindowOfferUsedForTest(t, store, opened.ID, now.Add(time.Second))
	seedApprovalWindowOfferOpenedForTest(t, store, opened.ID, "lease-opened", "override-opened", now.Add(2*time.Second))
	if closed, ok, err := store.CloseUnusedApprovalWindowOffer(opened.ID, now.Add(3*time.Second)); err != nil || ok {
		t.Fatalf("CloseUnusedApprovalWindowOffer(opened) = %#v, %t, %v; want no close", closed, ok, err)
	}
}

func TestOpenApprovalWindowOfferWithAuthorityBindsAtomically(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-open-atomic-success",
		ChatID:             7100,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7100",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-open-atomic-success",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	lease := OperatorAutoApprovalLease{
		ID:          "lease-open-atomic-success",
		AdminUserID: 1001,
		ChatID:      offer.ChatID,
		ScopeKind:   offer.ScopeKind,
		ScopeID:     offer.ScopeID,
		Scope:       OperatorAutoApprovalScopeAll,
		Reason:      "inline approval window",
		CreatedAt:   now,
		ExpiresAt:   now.Add(15 * time.Minute),
		UpdatedAt:   now,
	}
	override := OperatorAutonomyOverride{
		ID:          "override-open-atomic-success",
		AdminUserID: 1001,
		ChatID:      offer.ChatID,
		ScopeKind:   offer.ScopeKind,
		ScopeID:     offer.ScopeID,
		Mode:        "leased",
		Scope:       OperatorAutoApprovalScopeAll,
		Reason:      "inline approval window",
		CreatedAt:   now,
		ExpiresAt:   now.Add(15 * time.Minute),
		UpdatedAt:   now,
	}
	opened, storedLease, storedOverride, ok, err := store.OpenApprovalWindowOfferWithAuthority(offer.ID, lease, override, now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("OpenApprovalWindowOfferWithAuthority() ok=%t err=%v", ok, err)
	}
	if opened.OpenedLeaseID != storedLease.ID || opened.OpenedOverrideID != storedOverride.ID || opened.UsedAt.IsZero() {
		t.Fatalf("opened offer = %#v, lease=%q override=%q; want bound used offer", opened, storedLease.ID, storedOverride.ID)
	}
	if got, ok, err := store.OperatorAutoApprovalLease(storedLease.ID); err != nil || !ok || !got.ActiveAt(now.Add(2*time.Second)) {
		t.Fatalf("OperatorAutoApprovalLease() got=%#v ok=%t err=%v, want active created lease", got, ok, err)
	}
	if got, ok, err := store.OperatorAutonomyOverride(storedOverride.ID); err != nil || !ok || !got.ActiveAt(now.Add(2*time.Second)) {
		t.Fatalf("OperatorAutonomyOverride() got=%#v ok=%t err=%v, want active created override", got, ok, err)
	}
}

func TestOpenApprovalWindowOfferWithAuthorityRollsBackWhenBindCASMisses(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-open-atomic-cas-miss",
		ChatID:             7101,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7101",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-open-atomic-cas-miss",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UsedAt:             now,
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	lease := OperatorAutoApprovalLease{ID: "lease-open-atomic-cas-miss", AdminUserID: 1001, ChatID: offer.ChatID, ScopeKind: offer.ScopeKind, ScopeID: offer.ScopeID, Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute), UpdatedAt: now}
	override := OperatorAutonomyOverride{ID: "override-open-atomic-cas-miss", AdminUserID: 1001, ChatID: offer.ChatID, ScopeKind: offer.ScopeKind, ScopeID: offer.ScopeID, Mode: "leased", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute), UpdatedAt: now}
	if opened, _, _, ok, err := store.OpenApprovalWindowOfferWithAuthority(offer.ID, lease, override, now.Add(time.Second)); err != nil || ok {
		t.Fatalf("OpenApprovalWindowOfferWithAuthority() opened=%#v ok=%t err=%v, want CAS miss", opened, ok, err)
	}
	if got, ok, err := store.OperatorAutoApprovalLease(lease.ID); err != nil || ok {
		t.Fatalf("OperatorAutoApprovalLease(created) got=%#v ok=%t err=%v, want rolled back", got, ok, err)
	}
	if got, ok, err := store.OperatorAutonomyOverride(override.ID); err != nil || ok {
		t.Fatalf("OperatorAutonomyOverride(created) got=%#v ok=%t err=%v, want rolled back", got, ok, err)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if stored.OpenedLeaseID != "" || stored.OpenedOverrideID != "" {
		t.Fatalf("stored offer = %#v, want not rebound after CAS miss", stored)
	}
}

func TestReplaceApprovalWindowOfferAuthorityByIDsRebindsAtomically(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	oldLease := OperatorAutoApprovalLease{ID: "lease-rebind-atomic-old", AdminUserID: 1001, ChatID: 7102, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7102", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute), UpdatedAt: now}
	oldOverride := OperatorAutonomyOverride{ID: "override-rebind-atomic-old", AdminUserID: 1001, ChatID: 7102, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7102", Mode: "leased", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute), UpdatedAt: now}
	if _, err := store.CreateOperatorAutoApprovalLease(oldLease); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease(old) err = %v", err)
	}
	if _, err := store.CreateOperatorAutonomyOverride(oldOverride); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride(old) err = %v", err)
	}
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{ID: "offer-rebind-atomic-success", ChatID: 7102, AdminUserID: 1001, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7102", SourceKind: ApprovalWindowOfferSourceDecision, SourceID: "decision-rebind-atomic-success", SourceDecisionKind: "proposal_approval", OpenedLeaseID: oldLease.ID, OpenedOverrideID: oldOverride.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour), UsedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	newLease := OperatorAutoApprovalLease{ID: "lease-rebind-atomic-new", AdminUserID: 1001, ChatID: 7102, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7102", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(30 * time.Minute), UpdatedAt: now.Add(time.Second)}
	newOverride := OperatorAutonomyOverride{ID: "override-rebind-atomic-new", AdminUserID: 1001, ChatID: 7102, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7102", Mode: "leased", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(30 * time.Minute), UpdatedAt: now.Add(time.Second)}
	opened, revokedLease, revokedOverride, storedLease, storedOverride, ok, err := store.ReplaceApprovalWindowOfferAuthorityByIDs(offer.ID, offer.ChatID, offer.AdminUserID, offer.ScopeKind, offer.ScopeID, oldLease.ID, oldOverride.ID, newLease, newOverride, now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("ReplaceApprovalWindowOfferAuthorityByIDs() ok=%t err=%v", ok, err)
	}
	if opened.OpenedLeaseID != storedLease.ID || opened.OpenedOverrideID != storedOverride.ID {
		t.Fatalf("opened = %#v stored lease=%q override=%q, want rebound", opened, storedLease.ID, storedOverride.ID)
	}
	if revokedLease.RevokedAt.IsZero() || revokedOverride.RevokedAt.IsZero() {
		t.Fatalf("revoked lease=%#v override=%#v, want old authority revoked", revokedLease, revokedOverride)
	}
	if got, ok, err := store.OperatorAutoApprovalLease(storedLease.ID); err != nil || !ok || !got.ActiveAt(now.Add(2*time.Second)) {
		t.Fatalf("OperatorAutoApprovalLease(new) got=%#v ok=%t err=%v, want active replacement", got, ok, err)
	}
}

func TestReplaceApprovalWindowOfferAuthorityByIDsRollsBackWhenRebindCASMisses(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	oldLease := OperatorAutoApprovalLease{ID: "lease-rebind-atomic-rollback-old", AdminUserID: 1001, ChatID: 7103, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7103", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute), UpdatedAt: now}
	oldOverride := OperatorAutonomyOverride{ID: "override-rebind-atomic-rollback-old", AdminUserID: 1001, ChatID: 7103, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7103", Mode: "leased", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute), UpdatedAt: now}
	if _, err := store.CreateOperatorAutoApprovalLease(oldLease); err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease(old) err = %v", err)
	}
	if _, err := store.CreateOperatorAutonomyOverride(oldOverride); err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride(old) err = %v", err)
	}
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{ID: "offer-rebind-atomic-rollback", ChatID: 7103, AdminUserID: 1001, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7103", SourceKind: ApprovalWindowOfferSourceDecision, SourceID: "decision-rebind-atomic-rollback", SourceDecisionKind: "proposal_approval", OpenedLeaseID: "lease-other-binding", OpenedOverrideID: "override-other-binding", CreatedAt: now, ExpiresAt: now.Add(time.Hour), UsedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	newLease := OperatorAutoApprovalLease{ID: "lease-rebind-atomic-rollback-new", AdminUserID: 1001, ChatID: 7103, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7103", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(30 * time.Minute), UpdatedAt: now.Add(time.Second)}
	newOverride := OperatorAutonomyOverride{ID: "override-rebind-atomic-rollback-new", AdminUserID: 1001, ChatID: 7103, ScopeKind: string(ScopeKindTelegramDM), ScopeID: "7103", Mode: "leased", Scope: OperatorAutoApprovalScopeAll, Reason: "inline approval window", CreatedAt: now.Add(time.Second), ExpiresAt: now.Add(30 * time.Minute), UpdatedAt: now.Add(time.Second)}
	if opened, _, _, _, _, ok, err := store.ReplaceApprovalWindowOfferAuthorityByIDs(offer.ID, offer.ChatID, offer.AdminUserID, offer.ScopeKind, offer.ScopeID, oldLease.ID, oldOverride.ID, newLease, newOverride, now.Add(time.Second)); err != nil || ok {
		t.Fatalf("ReplaceApprovalWindowOfferAuthorityByIDs() opened=%#v ok=%t err=%v, want rebind CAS miss", opened, ok, err)
	}
	if got, ok, err := store.OperatorAutoApprovalLease(oldLease.ID); err != nil || !ok || !got.ActiveAt(now.Add(2*time.Second)) {
		t.Fatalf("OperatorAutoApprovalLease(old) got=%#v ok=%t err=%v, want old lease still active", got, ok, err)
	}
	if got, ok, err := store.OperatorAutoApprovalLease(newLease.ID); err != nil || ok {
		t.Fatalf("OperatorAutoApprovalLease(new) got=%#v ok=%t err=%v, want replacement rolled back", got, ok, err)
	}
	stored, ok, err := store.ApprovalWindowOffer(offer.ID)
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer() ok=%t err=%v", ok, err)
	}
	if stored.OpenedLeaseID != "lease-other-binding" || stored.OpenedOverrideID != "override-other-binding" {
		t.Fatalf("stored offer = %#v, want original rebound target unchanged", stored)
	}
}

func seedApprovalWindowOfferUsedForTest(t *testing.T, store *SQLiteStore, offerID string, usedAt time.Time) {
	t.Helper()
	stamp := usedAt.UTC().Format(time.RFC3339Nano)
	res, err := store.db.Exec(`
		UPDATE approval_window_offers
		SET used_at = ?, updated_at = ?
		WHERE offer_id = ?
	`, stamp, stamp, offerID)
	if err != nil {
		t.Fatalf("seed approval window offer used: %v", err)
	}
	if rows, err := res.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("seed approval window offer used rows=%d err=%v, want one row", rows, err)
	}
}

func seedApprovalWindowOfferOpenedForTest(t *testing.T, store *SQLiteStore, offerID string, leaseID string, overrideID string, openedAt time.Time) {
	t.Helper()
	stamp := openedAt.UTC().Format(time.RFC3339Nano)
	res, err := store.db.Exec(`
		UPDATE approval_window_offers
		SET used_at = COALESCE(used_at, ?), opened_lease_id = ?, opened_override_id = ?, updated_at = ?
		WHERE offer_id = ?
	`, stamp, leaseID, overrideID, stamp, offerID)
	if err != nil {
		t.Fatalf("seed approval window offer opened: %v", err)
	}
	if rows, err := res.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("seed approval window offer opened rows=%d err=%v, want one row", rows, err)
	}
}
