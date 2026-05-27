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
	if _, ok, err := store.MarkApprovalWindowOfferUsed(offer.ID, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferUsed() ok=%t err=%v", ok, err)
	}
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
	if _, ok, err := store.MarkApprovalWindowOfferUsed(offer.ID, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferUsed() ok=%t err=%v", ok, err)
	}
	if _, ok, err := store.MarkApprovalWindowOfferOpened(offer.ID, "lease-opened", "override-opened", now.Add(2*time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferOpened() ok=%t err=%v", ok, err)
	}
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
	if _, ok, err := store.MarkApprovalWindowOfferUsed(offer.ID, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferUsed() ok=%t err=%v", ok, err)
	}
	if _, ok, err := store.MarkApprovalWindowOfferOpened(offer.ID, "lease-expired-opened", "override-expired-opened", now.Add(2*time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferOpened() ok=%t err=%v", ok, err)
	}
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
	if _, ok, err := store.MarkApprovalWindowOfferUsed(offer.ID, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferUsed() ok=%t err=%v", ok, err)
	}
	if _, ok, err := store.MarkApprovalWindowOfferOpened(offer.ID, "lease-new", "override-new", now.Add(2*time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferOpened() ok=%t err=%v", ok, err)
	}
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
	if _, ok, err := store.MarkApprovalWindowOfferUsed(opened.ID, now.Add(time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferUsed() ok=%t err=%v", ok, err)
	}
	if _, ok, err := store.MarkApprovalWindowOfferOpened(opened.ID, "lease-opened", "override-opened", now.Add(2*time.Second)); err != nil || !ok {
		t.Fatalf("MarkApprovalWindowOfferOpened() ok=%t err=%v", ok, err)
	}
	if closed, ok, err := store.CloseUnusedApprovalWindowOffer(opened.ID, now.Add(3*time.Second)); err != nil || ok {
		t.Fatalf("CloseUnusedApprovalWindowOffer(opened) = %#v, %t, %v; want no close", closed, ok, err)
	}
}

func TestApprovalWindowOfferUsedMarkIsCAS(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	offer, err := store.CreateApprovalWindowOffer(ApprovalWindowOffer{
		ID:                 "offer-cas",
		ChatID:             7001,
		AdminUserID:        1001,
		ScopeKind:          string(ScopeKindTelegramDM),
		ScopeID:            "7001",
		SourceKind:         ApprovalWindowOfferSourceDecision,
		SourceID:           "decision-cas",
		SourceDecisionKind: "proposal_approval",
		CreatedAt:          now,
		ExpiresAt:          now.Add(time.Hour),
		UpdatedAt:          now,
	})
	if err != nil {
		t.Fatalf("CreateApprovalWindowOffer() err = %v", err)
	}
	claimed, ok, err := store.MarkApprovalWindowOfferUsed(offer.ID, now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("first MarkApprovalWindowOfferUsed() = %#v, %t, %v; want claim", claimed, ok, err)
	}
	if claimed.UsedAt.IsZero() {
		t.Fatalf("claimed UsedAt is zero")
	}
	second, ok, err := store.MarkApprovalWindowOfferUsed(offer.ID, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("second MarkApprovalWindowOfferUsed() err = %v", err)
	}
	if ok {
		t.Fatalf("second MarkApprovalWindowOfferUsed() = %#v, true; want CAS miss", second)
	}
}
