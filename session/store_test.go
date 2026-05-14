//go:build linux

package session

import (
	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
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

func TestOperatorAutoApprovalLeaseLifecycle(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC)
	created, err := store.CreateOperatorAutoApprovalLease(OperatorAutoApprovalLease{
		ID:          "auto-test",
		AdminUserID: 1001,
		ChatID:      7001,
		Scope:       "workspace",
		MaxUses:     1,
		CreatedAt:   now,
		ExpiresAt:   now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateOperatorAutoApprovalLease() err = %v", err)
	}
	if created.Scope != OperatorAutoApprovalScopeWorkspace || !created.ActiveAt(now) {
		t.Fatalf("created lease = %#v, want active workspace lease", created)
	}

	active, err := store.ActiveOperatorAutoApprovalLeases(7001, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(active) != 1 || active[0].ID != "auto-test" {
		t.Fatalf("active leases = %#v, want auto-test", active)
	}
	listed, err := store.OperatorAutoApprovalLeases(10, now.Add(time.Minute), true)
	if err != nil {
		t.Fatalf("OperatorAutoApprovalLeases(activeOnly) err = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "auto-test" {
		t.Fatalf("listed active leases = %#v, want auto-test", listed)
	}
	used, ok, err := store.IncrementOperatorAutoApprovalUse("auto-test", now.Add(2*time.Minute))
	if err != nil || !ok {
		t.Fatalf("IncrementOperatorAutoApprovalUse() = lease:%#v ok:%v err:%v, want ok", used, ok, err)
	}
	if used.UsedCount != 1 || used.ActiveAt(now.Add(3*time.Minute)) {
		t.Fatalf("used lease = %#v, want exhausted after one use", used)
	}
	active, err = store.ActiveOperatorAutoApprovalLeases(7001, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases(after use) err = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active leases after use = %#v, want none", active)
	}
	allListed, err := store.OperatorAutoApprovalLeases(10, now.Add(3*time.Minute), false)
	if err != nil {
		t.Fatalf("OperatorAutoApprovalLeases(all) err = %v", err)
	}
	if len(allListed) != 1 || allListed[0].ID != "auto-test" || allListed[0].UsedCount != 1 {
		t.Fatalf("all listed leases = %#v, want exhausted auto-test", allListed)
	}

	revoked, err := store.RevokeOperatorAutoApprovalLeases(7001, 1001, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("RevokeOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(revoked) != 1 || revoked[0].ID != "auto-test" {
		t.Fatalf("revoked = %#v, want auto-test", revoked)
	}
}

func TestOperatorAutonomyOverrideLifecycle(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	created, err := store.CreateOperatorAutonomyOverride(OperatorAutonomyOverride{
		ID:          "mode-test",
		AdminUserID: 1001,
		ChatID:      7002,
		Mode:        "leased",
		Scope:       "deploy",
		Reason:      "bounded release",
		CreatedAt:   now,
		ExpiresAt:   now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateOperatorAutonomyOverride() err = %v", err)
	}
	if created.Scope != OperatorAutoApprovalScopeDeploy || created.Mode != "leased" || !created.ActiveAt(now) {
		t.Fatalf("created override = %#v, want active deploy leased override", created)
	}

	active, err := store.ActiveOperatorAutonomyOverrides(7002, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverrides() err = %v", err)
	}
	if len(active) != 1 || active[0].ID != "mode-test" {
		t.Fatalf("active overrides = %#v, want mode-test", active)
	}
	latest, ok, err := store.LatestOperatorAutonomyOverride(7002, 1001)
	if err != nil || !ok {
		t.Fatalf("LatestOperatorAutonomyOverride() = override:%#v ok:%v err:%v, want ok", latest, ok, err)
	}
	if latest.ID != "mode-test" {
		t.Fatalf("latest override = %#v, want mode-test", latest)
	}

	revoked, err := store.RevokeOperatorAutonomyOverrides(7002, 1001, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("RevokeOperatorAutonomyOverrides() err = %v", err)
	}
	if len(revoked) != 1 || revoked[0].ID != "mode-test" {
		t.Fatalf("revoked = %#v, want mode-test", revoked)
	}
	active, err = store.ActiveOperatorAutonomyOverrides(7002, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverrides(after revoke) err = %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("active overrides after revoke = %#v, want none", active)
	}
}

func TestReviewEventsPendingOrderingAndFiltering(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	target := int64(700)
	otherTarget := int64(701)
	for _, event := range []ReviewEvent{
		{
			SourceChatID:      10,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          1,
			TurnTo:            3,
			Summary:           "first",
		},
		{
			SourceChatID:      11,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: otherTarget,
			TurnFrom:          4,
			TurnTo:            5,
			Summary:           "wrong target",
		},
		{
			SourceChatID:      12,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          6,
			TurnTo:            8,
			Summary:           "second",
		},
		{
			SourceChatID:      13,
			SourceUserID:      0,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			TurnFrom:          9,
			TurnTo:            10,
			Summary:           "already delivered",
			Status:            "delivered",
		},
	} {
		if err := store.EnqueueReviewEvent(event); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	pending, err := store.PendingReviewEvents(target, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}

	if len(pending) != 2 {
		t.Fatalf("pending len = %d, want 2", len(pending))
	}
	if pending[0].Summary != "first" || pending[1].Summary != "second" {
		t.Fatalf("pending summaries = [%q, %q], want [first, second]", pending[0].Summary, pending[1].Summary)
	}
	if pending[0].ID >= pending[1].ID {
		t.Fatalf("pending IDs not ordered: first=%d second=%d", pending[0].ID, pending[1].ID)
	}
}

func TestReviewEventsWithRedactedSummaryAndUpdateProjection(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	id, err := store.InsertReviewEvent(ReviewEvent{
		SourceChatID:      10,
		SourceUserID:      0,
		SourceRole:        "durable_agent",
		TargetAdminChatID: 700,
		Summary:           "summary: [REDACTED: summary]",
		MetadataJSON:      `{"summary":"[REDACTED: summary]","metadata":{"redacted_fields":"summary"}}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}

	events, err := store.ReviewEventsWithRedactedSummary(10)
	if err != nil {
		t.Fatalf("ReviewEventsWithRedactedSummary() err = %v", err)
	}
	if len(events) != 1 || events[0].ID != id {
		t.Fatalf("redacted events = %#v, want event %d", events, id)
	}

	if err := store.UpdateReviewEventProjection(id, "summary: repaired", `{"summary":"repaired"}`); err != nil {
		t.Fatalf("UpdateReviewEventProjection() err = %v", err)
	}
	updated, err := store.ReviewEventByID(id)
	if err != nil {
		t.Fatalf("ReviewEventByID() err = %v", err)
	}
	if updated.Summary != "summary: repaired" || updated.MetadataJSON != `{"summary":"repaired"}` {
		t.Fatalf("updated event = %#v, want repaired projection", updated)
	}
}

func TestReviewEventsLimitAndMarkDelivered(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	target := int64(800)
	for _, summary := range []string{"one", "two", "three"} {
		if err := store.EnqueueReviewEvent(ReviewEvent{
			SourceChatID:      20,
			SourceRole:        "approved_user",
			TargetAdminChatID: target,
			Summary:           summary,
		}); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	firstBatch, err := store.PendingReviewEvents(target, 2)
	if err != nil {
		t.Fatalf("PendingReviewEvents(limit=2) err = %v", err)
	}
	if len(firstBatch) != 2 {
		t.Fatalf("first batch len = %d, want 2", len(firstBatch))
	}

	if err := store.MarkReviewDelivered([]int64{firstBatch[0].ID}); err != nil {
		t.Fatalf("MarkReviewDelivered() err = %v", err)
	}

	var status string
	var deliveredAt string
	if err := store.db.QueryRow(`
		SELECT status, COALESCE(delivered_at, '')
		FROM review_events
		WHERE id = ?
	`, firstBatch[0].ID).Scan(&status, &deliveredAt); err != nil {
		t.Fatalf("query delivered review event: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("status = %q, want delivered", status)
	}
	if deliveredAt == "" {
		t.Fatal("delivered_at is empty, want populated timestamp")
	}

	remaining, err := store.PendingReviewEvents(target, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining pending len = %d, want 2", len(remaining))
	}
	if remaining[0].Summary != "two" || remaining[1].Summary != "three" {
		t.Fatalf("remaining summaries = [%q, %q], want [two, three]", remaining[0].Summary, remaining[1].Summary)
	}
}

func TestReviewEventsPreserveScopeAndMetadata(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	event := ReviewEvent{
		SourceChatID:      0,
		SourceRole:        "durable_agent",
		TargetAdminChatID: 9001,
		SourceScope: ScopeRef{
			Kind:            ScopeKindDurableAgent,
			ID:              "family-group",
			DurableAgentID:  "family-group",
			ParentScopeKind: ScopeKindTelegramDM,
			ParentScopeID:   "1001",
		},
		TargetScope: ScopeRef{
			Kind: ScopeKindTelegramDM,
			ID:   "9001",
		},
		Summary:      "bounded child synthesis",
		MetadataJSON: `{"risk_flags":["tone drift"],"questions":["approve charter change?"]}`,
	}
	if err := store.EnqueueReviewEvent(event); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	pending, err := store.PendingReviewEvents(9001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	got := pending[0]
	if got.SourceScope.Kind != ScopeKindDurableAgent || got.SourceScope.ID != "family-group" {
		t.Fatalf("source scope = %#v, want durable_agent family-group", got.SourceScope)
	}
	if got.SourceScope.DurableAgentID != "family-group" {
		t.Fatalf("source durable agent id = %q, want family-group", got.SourceScope.DurableAgentID)
	}
	if got.TargetScope.Kind != ScopeKindTelegramDM || got.TargetScope.ID != "9001" {
		t.Fatalf("target scope = %#v, want telegram_dm 9001", got.TargetScope)
	}
	if got.MetadataJSON != event.MetadataJSON {
		t.Fatalf("MetadataJSON = %q, want %q", got.MetadataJSON, event.MetadataJSON)
	}
}

func TestPendingReviewEventsAllExcludesDeliveredRows(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	for _, event := range []ReviewEvent{
		{
			SourceChatID:      100,
			SourceRole:        "approved_user",
			TargetAdminChatID: 9001,
			Summary:           "pending-a",
		},
		{
			SourceChatID:      101,
			SourceRole:        "approved_user",
			TargetAdminChatID: 9002,
			Summary:           "pending-b",
		},
		{
			SourceChatID:      102,
			SourceRole:        "approved_user",
			TargetAdminChatID: 9002,
			Summary:           "delivered-c",
			Status:            "delivered",
		},
	} {
		if err := store.EnqueueReviewEvent(event); err != nil {
			t.Fatalf("EnqueueReviewEvent() err = %v", err)
		}
	}

	events, err := store.PendingReviewEventsAll(10)
	if err != nil {
		t.Fatalf("PendingReviewEventsAll() err = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("pending review events len = %d, want 2", len(events))
	}
	if events[0].Summary != "pending-a" || events[1].Summary != "pending-b" {
		t.Fatalf("pending review summaries = [%q, %q], want [pending-a, pending-b]", events[0].Summary, events[1].Summary)
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
