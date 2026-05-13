//go:build linux

package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
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

func TestPendingDecisionRoundTripAndReload(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "pending-decisions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}

	record := PendingDecisionRecord{
		ID:        "decision-abc123",
		Sequence:  42,
		OwnerKey:  "chat:7:sender:99",
		Kind:      "proposal_approval",
		ChatID:    7,
		SenderID:  99,
		MessageID: 1001,
		Prompt:    "Approve this proposal?",
		Details:   "Install one dependency.",
		Rationale: "Dependency install is needed before the tool can be audited and verified.",
		ArtifactRefs: []RecordReference{
			{Kind: "file_path", Ref: "docs/architecture/external-tools-pilot.md", Label: "design doc"},
			{Kind: "telegram_message", Ref: "chat:7:message:1001", Label: "operator request"},
		},
		ChoicesJSON:       `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
		DefaultChoice:     "deny",
		TimeoutNanos:      int64((30 * time.Second).Nanoseconds()),
		DeliveryMessageID: 5001,
	}
	if err := store.UpsertPendingDecision(record); err != nil {
		t.Fatalf("UpsertPendingDecision(insert) err = %v", err)
	}

	record.DeliveryMessageID = 5002
	if err := store.UpsertPendingDecision(record); err != nil {
		t.Fatalf("UpsertPendingDecision(update) err = %v", err)
	}

	pending, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	if pending[0].DeliveryMessageID != 5002 {
		t.Fatalf("DeliveryMessageID = %d, want 5002", pending[0].DeliveryMessageID)
	}
	if pending[0].Rationale != record.Rationale {
		t.Fatalf("Rationale = %q, want %q", pending[0].Rationale, record.Rationale)
	}
	if len(pending[0].ArtifactRefs) != 2 || pending[0].ArtifactRefs[0].Kind != "file_path" || pending[0].ArtifactRefs[1].Ref != "chat:7:message:1001" {
		t.Fatalf("ArtifactRefs = %#v, want file_path + telegram_message refs", pending[0].ArtifactRefs)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	store, err = NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer store.Close()

	pending, err = store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions(reload) err = %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "decision-abc123" {
		t.Fatalf("pending after reload = %#v, want decision-abc123", pending)
	}
	if pending[0].Rationale != record.Rationale || len(pending[0].ArtifactRefs) != 2 {
		t.Fatalf("pending after reload = %#v, want rationale + two artifact refs", pending[0])
	}

	if err := store.DeletePendingDecision("decision-abc123"); err != nil {
		t.Fatalf("DeletePendingDecision() err = %v", err)
	}
	pending, err = store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions(after delete) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after delete = %d, want 0", len(pending))
	}
}

func TestDeletePendingDecisionsByOwnerAndAll(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	records := []PendingDecisionRecord{
		{
			ID:            "decision-a",
			Sequence:      1,
			OwnerKey:      "chat:7:sender:99",
			Kind:          "proposal_approval",
			ChatID:        7,
			SenderID:      99,
			Prompt:        "A",
			ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
			DefaultChoice: "deny",
		},
		{
			ID:            "decision-b",
			Sequence:      2,
			OwnerKey:      "chat:7:sender:99",
			Kind:          "proposal_approval",
			ChatID:        7,
			SenderID:      99,
			Prompt:        "B",
			ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
			DefaultChoice: "deny",
		},
		{
			ID:            "decision-c",
			Sequence:      3,
			OwnerKey:      "chat:8:sender:100",
			Kind:          "proposal_approval",
			ChatID:        8,
			SenderID:      100,
			Prompt:        "C",
			ChoicesJSON:   `[{"id":"approve","label":"Approve"},{"id":"deny","label":"Deny"}]`,
			DefaultChoice: "deny",
		},
	}
	for _, record := range records {
		if err := store.UpsertPendingDecision(record); err != nil {
			t.Fatalf("UpsertPendingDecision(%s) err = %v", record.ID, err)
		}
	}

	removed, err := store.DeletePendingDecisionsByOwner("chat:7:sender:99")
	if err != nil {
		t.Fatalf("DeletePendingDecisionsByOwner() err = %v", err)
	}
	if removed != 2 {
		t.Fatalf("DeletePendingDecisionsByOwner() removed = %d, want 2", removed)
	}
	pending, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "decision-c" {
		t.Fatalf("pending after owner detach = %#v, want only decision-c", pending)
	}

	removed, err = store.DeleteAllPendingDecisions()
	if err != nil {
		t.Fatalf("DeleteAllPendingDecisions() err = %v", err)
	}
	if removed != 1 {
		t.Fatalf("DeleteAllPendingDecisions() removed = %d, want 1", removed)
	}
	pending, err = store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions(after all delete) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after all delete = %d, want 0", len(pending))
	}
}

func TestOperationStateRoundTripAndUpdate(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 80, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "80"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.OperationState = OperationState{
		ID:        "op-1",
		Objective: "Investigate the current internet footprint.",
		Status:    OperationStatusActive,
		Stage:     "assessment",
		Summary:   "Collecting public traces before requesting external access.",
		Proposal: OperationProposal{
			ID:            "proposal-1",
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        ProposalStatusPending,
		},
		Findings: []OperationFinding{
			{Claim: "A browser is not currently available.", Confidence: FindingConfidenceHigh, Basis: "No browser tool is exposed in the active manifest."},
		},
		Artifacts: []OperationArtifact{
			{Label: "working-note", Ref: "tmp/notes.md"},
		},
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "operating", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.OperationState.Objective != "Investigate the current internet footprint." {
		t.Fatalf("Objective = %q, want persisted objective", reloaded.OperationState.Objective)
	}
	if reloaded.OperationState.Proposal.Status != ProposalStatusPending {
		t.Fatalf("Proposal status = %q, want pending", reloaded.OperationState.Proposal.Status)
	}
	if len(reloaded.OperationState.Findings) != 1 {
		t.Fatalf("findings len = %d, want 1", len(reloaded.OperationState.Findings))
	}

	updated := OperationState{
		ID:        "op-1",
		Objective: "Investigate the current internet footprint.",
		Status:    OperationStatusActive,
		Stage:     "execution",
		Summary:   "Proposal approved and screenshot capture is underway.",
		Proposal: OperationProposal{
			ID:            "proposal-1",
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        ProposalStatusApproved,
		},
		Findings: []OperationFinding{
			{Claim: "Browser automation can be acquired locally.", Confidence: FindingConfidenceHigh, Basis: "Admin execution can install local dependencies."},
		},
		Artifacts: []OperationArtifact{
			{Label: "screenshot", Ref: "tmp/reddit.png"},
		},
	}
	if err := store.UpdateOperationState(key, updated); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	operationState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if operationState.Stage != "execution" {
		t.Fatalf("updated stage = %q, want execution", operationState.Stage)
	}
	if operationState.Proposal.Status != ProposalStatusApproved {
		t.Fatalf("updated proposal status = %q, want approved", operationState.Proposal.Status)
	}
	if len(operationState.Artifacts) != 1 || operationState.Artifacts[0].Ref != "tmp/reddit.png" {
		t.Fatalf("artifacts = %#v, want updated screenshot artifact", operationState.Artifacts)
	}
}

func TestRegisteredToolRecordsRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	registered, err := store.UpsertRegisteredTool(RegisteredTool{
		ToolName:          "browse_page",
		ImplementationRef: "external-tools/browse_page/manifest.json",
		Registered:        true,
	})
	if err != nil {
		t.Fatalf("UpsertRegisteredTool(insert) err = %v", err)
	}
	if !registered.Registered {
		t.Fatal("registered.Registered = false, want true")
	}

	loadedRegistered, ok, err := store.RegisteredTool("browse_page")
	if err != nil {
		t.Fatalf("RegisteredTool() err = %v", err)
	}
	if !ok {
		t.Fatal("RegisteredTool() ok = false, want true")
	}
	if loadedRegistered.ImplementationRef != "external-tools/browse_page/manifest.json" {
		t.Fatalf("loaded registered implementation_ref = %q, want external manifest ref", loadedRegistered.ImplementationRef)
	}

	registeredList, err := store.RegisteredTools(10)
	if err != nil {
		t.Fatalf("RegisteredTools() err = %v", err)
	}
	if len(registeredList) != 1 {
		t.Fatalf("RegisteredTools len = %d, want 1", len(registeredList))
	}

}

func TestToolProbeRecordRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	record, err := store.UpsertToolProbeRecord(ToolProbeRecord{
		ToolName:                     "browse_page",
		Status:                       ToolProbeStatusPassed,
		ProbeOutput:                  "stdout: probe ok",
		Rationale:                    "Probe passed against the latest installed baseline.",
		ArtifactRefs:                 []RecordReference{{Kind: "execution_event", Ref: "tool.probe.updated:123", Label: "probe event"}},
		BaselineFingerprint:          "sha256:probe-baseline",
		CurrentFingerprint:           "sha256:probe-current",
		BaselineInstallRef:           "workspace:tooling-v1",
		CurrentInstallRef:            "workspace:tooling-v2",
		BaselineManifestHash:         "sha256:manifest-baseline",
		CurrentManifestHash:          "sha256:manifest-current",
		BaselineWorkspaceFingerprint: "sha256:workspace-baseline",
		CurrentWorkspaceFingerprint:  "sha256:workspace-current",
		StaleReason:                  "workspace_drift: baseline=sha256:workspace-baseline current=sha256:workspace-current",
		DriftSource:                  ToolDriftSourceWorkspaceDrift,
		ProbedAt:                     time.Now().UTC(),
		ConsecutiveFailures:          0,
	})
	if err != nil {
		t.Fatalf("UpsertToolProbeRecord(insert) err = %v", err)
	}
	if record.Status != ToolProbeStatusPassed {
		t.Fatalf("record.Status = %q, want passed", record.Status)
	}
	loaded, ok, err := store.ToolProbeRecord("browse_page")
	if err != nil {
		t.Fatalf("ToolProbeRecord() err = %v", err)
	}
	if !ok {
		t.Fatal("ToolProbeRecord() ok = false, want true")
	}
	if loaded.Status != ToolProbeStatusPassed {
		t.Fatalf("loaded.Status = %q, want passed", loaded.Status)
	}
	if loaded.Rationale != record.Rationale || len(loaded.ArtifactRefs) != 1 || loaded.ArtifactRefs[0].Kind != "execution_event" {
		t.Fatalf("loaded traceability = (%q, %#v), want rationale + execution_event ref", loaded.Rationale, loaded.ArtifactRefs)
	}
	if loaded.BaselineManifestHash != "sha256:manifest-baseline" || loaded.CurrentWorkspaceFingerprint != "sha256:workspace-current" || loaded.DriftSource != ToolDriftSourceWorkspaceDrift {
		t.Fatalf("loaded probe anchors = %#v, want persisted anchor diagnostics", loaded)
	}
	list, err := store.ToolProbeRecords(ToolProbeStatusPassed, 10)
	if err != nil {
		t.Fatalf("ToolProbeRecords() err = %v", err)
	}
	if len(list) != 1 || list[0].ToolName != "browse_page" {
		t.Fatalf("ToolProbeRecords(passed) = %#v, want one browse_page record", list)
	}
	if list[0].Rationale != record.Rationale || len(list[0].ArtifactRefs) != 1 {
		t.Fatalf("ToolProbeRecords traceability = (%q, %#v), want persisted rationale + refs", list[0].Rationale, list[0].ArtifactRefs)
	}
}

func TestToolAuditRecordRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	record, err := store.UpsertToolAuditRecord(ToolAuditRecord{
		ToolName:                     "browse_page",
		Status:                       ToolAuditStatusPassed,
		AuditOutput:                  "entry_path: /tmp/run.sh",
		Rationale:                    "Runtime resolution succeeded for the declared entrypoint.",
		ArtifactRefs:                 []RecordReference{{Kind: "file_path", Ref: "/tmp/run.sh", Label: "entry path"}},
		BaselineFingerprint:          "sha256:audit-baseline",
		CurrentFingerprint:           "sha256:audit-current",
		BaselineInstallRef:           "workspace:tooling-v1",
		CurrentInstallRef:            "workspace:tooling-v2",
		BaselineManifestHash:         "sha256:audit-manifest-baseline",
		CurrentManifestHash:          "sha256:audit-manifest-current",
		BaselineWorkspaceFingerprint: "sha256:audit-workspace-baseline",
		CurrentWorkspaceFingerprint:  "sha256:audit-workspace-current",
		StaleReason:                  "manifest_drift: baseline=sha256:audit-manifest-baseline current=sha256:audit-manifest-current",
		DriftSource:                  ToolDriftSourceManifestDrift,
		AuditedAt:                    time.Now().UTC(),
		ConsecutiveFailures:          0,
	})
	if err != nil {
		t.Fatalf("UpsertToolAuditRecord(insert) err = %v", err)
	}
	if record.Status != ToolAuditStatusPassed {
		t.Fatalf("record.Status = %q, want passed", record.Status)
	}
	loaded, ok, err := store.ToolAuditRecord("browse_page")
	if err != nil {
		t.Fatalf("ToolAuditRecord() err = %v", err)
	}
	if !ok {
		t.Fatal("ToolAuditRecord() ok = false, want true")
	}
	if loaded.Status != ToolAuditStatusPassed {
		t.Fatalf("loaded.Status = %q, want passed", loaded.Status)
	}
	if loaded.Rationale != record.Rationale || len(loaded.ArtifactRefs) != 1 || loaded.ArtifactRefs[0].Ref != "/tmp/run.sh" {
		t.Fatalf("loaded traceability = (%q, %#v), want rationale + file ref", loaded.Rationale, loaded.ArtifactRefs)
	}
	if loaded.BaselineFingerprint != "sha256:audit-baseline" || loaded.CurrentFingerprint != "sha256:audit-current" {
		t.Fatalf("loaded fingerprints = %q/%q, want persisted audit fingerprints", loaded.BaselineFingerprint, loaded.CurrentFingerprint)
	}
	if loaded.BaselineInstallRef != "workspace:tooling-v1" || loaded.CurrentManifestHash != "sha256:audit-manifest-current" || loaded.DriftSource != ToolDriftSourceManifestDrift {
		t.Fatalf("loaded audit anchors = %#v, want persisted anchor diagnostics", loaded)
	}
	list, err := store.ToolAuditRecords(ToolAuditStatusPassed, 10)
	if err != nil {
		t.Fatalf("ToolAuditRecords() err = %v", err)
	}
	if len(list) != 1 || list[0].ToolName != "browse_page" {
		t.Fatalf("ToolAuditRecords(passed) = %#v, want one browse_page record", list)
	}
	if list[0].Rationale != record.Rationale || len(list[0].ArtifactRefs) != 1 {
		t.Fatalf("ToolAuditRecords traceability = (%q, %#v), want persisted rationale + refs", list[0].Rationale, list[0].ArtifactRefs)
	}
}

func TestToolInstallRecordRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	record, err := store.UpsertToolInstallRecord(ToolInstallRecord{
		ToolName:    "browse_page",
		Installer:   "aphelion",
		InstallRef:  "workspace:tooling-v1",
		Status:      ToolInstallStatusVerified,
		ProbeStatus: ToolProbeStatusPassed,
		ProbeOutput: "self-check ok",
		Rationale:   "Install attested after successful bounded setup and probe.",
		ArtifactRefs: []RecordReference{
			{Kind: "git_commit", Ref: "a3336ad", Label: "traceability slice"},
			{Kind: "telegram_message", Ref: "chat:7:message:1001", Label: "approval prompt"},
		},
		InstalledAt:                  time.Now().UTC(),
		LastProbedAt:                 time.Now().UTC(),
		AttestedAt:                   time.Now().UTC(),
		BaselineFingerprint:          "sha256:install-baseline",
		CurrentFingerprint:           "sha256:install-current",
		BaselineInstallRef:           "workspace:tooling-v1",
		CurrentInstallRef:            "workspace:tooling-v1",
		BaselineManifestHash:         "sha256:install-manifest-baseline",
		CurrentManifestHash:          "sha256:install-manifest-current",
		BaselineWorkspaceFingerprint: "sha256:install-workspace-baseline",
		CurrentWorkspaceFingerprint:  "sha256:install-workspace-current",
		ConsecutiveFailures:          0,
	})
	if err != nil {
		t.Fatalf("UpsertToolInstallRecord(insert) err = %v", err)
	}
	if record.Status != ToolInstallStatusVerified {
		t.Fatalf("record.Status = %q, want verified", record.Status)
	}
	loaded, ok, err := store.ToolInstallRecord("browse_page")
	if err != nil {
		t.Fatalf("ToolInstallRecord() err = %v", err)
	}
	if !ok {
		t.Fatal("ToolInstallRecord() ok = false, want true")
	}
	if loaded.ProbeStatus != ToolProbeStatusPassed {
		t.Fatalf("loaded.ProbeStatus = %q, want passed", loaded.ProbeStatus)
	}
	if loaded.Rationale != record.Rationale || len(loaded.ArtifactRefs) != 2 || loaded.ArtifactRefs[0].Kind != "git_commit" {
		t.Fatalf("loaded traceability = (%q, %#v), want rationale + refs", loaded.Rationale, loaded.ArtifactRefs)
	}
	if loaded.BaselineFingerprint != "sha256:install-baseline" || loaded.CurrentFingerprint != "sha256:install-current" {
		t.Fatalf("loaded fingerprints = %q/%q, want persisted install fingerprints", loaded.BaselineFingerprint, loaded.CurrentFingerprint)
	}
	if loaded.BaselineManifestHash != "sha256:install-manifest-baseline" || loaded.CurrentWorkspaceFingerprint != "sha256:install-workspace-current" {
		t.Fatalf("loaded install anchors = %#v, want persisted anchor diagnostics", loaded)
	}
	record.Status = ToolInstallStatusStale
	record.ProbeStatus = ToolProbeStatusFailed
	record.ProbeOutput = "missing shared libs"
	record.Rationale = "Workspace drift invalidated the previous verification."
	record.ArtifactRefs = []RecordReference{{Kind: "file_path", Ref: "tool-manifest.json", Label: "manifest"}}
	record.StaleReason = "fingerprint drift: baseline=sha256:install-baseline current=sha256:install-current-2"
	record.CurrentFingerprint = "sha256:install-current-2"
	record.DriftSource = ToolDriftSourceWorkspaceDrift
	if _, err := store.UpsertToolInstallRecord(record); err != nil {
		t.Fatalf("UpsertToolInstallRecord(update) err = %v", err)
	}
	list, err := store.ToolInstallRecords(ToolInstallStatusStale, 10)
	if err != nil {
		t.Fatalf("ToolInstallRecords() err = %v", err)
	}
	if len(list) != 1 || list[0].ToolName != "browse_page" {
		t.Fatalf("ToolInstallRecords(stale) = %#v, want one browse_page record", list)
	}
	if list[0].Rationale != record.Rationale || len(list[0].ArtifactRefs) != 1 || list[0].ArtifactRefs[0].Ref != "tool-manifest.json" {
		t.Fatalf("ToolInstallRecords traceability = (%q, %#v), want updated rationale + manifest ref", list[0].Rationale, list[0].ArtifactRefs)
	}
	if list[0].StaleReason != record.StaleReason || list[0].CurrentFingerprint != "sha256:install-current-2" || list[0].DriftSource != ToolDriftSourceWorkspaceDrift {
		t.Fatalf("ToolInstallRecords stale diagnostics = (%q, %q, %q), want persisted stale reason + current fingerprint + drift source", list[0].StaleReason, list[0].CurrentFingerprint, list[0].DriftSource)
	}
}

func TestPlanEventsRoundTripAndRehydrateFromLatestEvent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 78, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "78"}}
	state := PlanState{
		Explanation: "Track long-running work durably.",
		Steps: []PlanStep{
			{Step: "Inspect the current runtime.", Status: PlanStatusInProgress},
			{Step: "Patch the missing event log.", Status: PlanStatusPending},
		},
	}

	if err := store.UpdatePlanStateWithEvent(key, state, PlanEventKindToolUpdated); err != nil {
		t.Fatalf("UpdatePlanStateWithEvent() err = %v", err)
	}

	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("plan events len = %d, want 1", len(events))
	}
	if events[0].Kind != PlanEventKindToolUpdated {
		t.Fatalf("plan event kind = %q, want %q", events[0].Kind, PlanEventKindToolUpdated)
	}
	if events[0].PlanState.Explanation != state.Explanation {
		t.Fatalf("event explanation = %q, want %q", events[0].PlanState.Explanation, state.Explanation)
	}

	if _, err := store.db.Exec(`UPDATE sessions SET plan_state_json = '{}' WHERE session_id = ?`, SessionIDForKey(key)); err != nil {
		t.Fatalf("clear plan_state_json err = %v", err)
	}

	rehydrated, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState(rehydrated) err = %v", err)
	}
	if rehydrated.Explanation != state.Explanation {
		t.Fatalf("rehydrated explanation = %q, want %q", rehydrated.Explanation, state.Explanation)
	}
	if len(rehydrated.Steps) != 2 || rehydrated.Steps[0].Status != PlanStatusInProgress {
		t.Fatalf("rehydrated steps = %#v, want original state", rehydrated.Steps)
	}

	events, err = store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents(after rehydrate) err = %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("plan events len after rehydrate = %d, want >= 2", len(events))
	}
	if events[0].Kind != PlanEventKindRehydrated {
		t.Fatalf("latest plan event kind = %q, want %q", events[0].Kind, PlanEventKindRehydrated)
	}
}

func TestPlanStateRehydratesFromTranscriptWhenEventLogMissing(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 79, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "79"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "work through this", TurnIndex: 1},
		{
			Role:     "tool",
			ToolName: "update_plan",
			Content: strings.Join([]string{
				"[PLAN_UPDATED]",
				"active: true",
				"explanation: Recover from transcript state.",
				"- [in_progress] Inspect the relevant files.",
				"- [pending] Patch the bug.",
			}, "\n"),
			TurnIndex: 1,
		},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if _, err := store.db.Exec(`UPDATE sessions SET plan_state_json = '{}' WHERE session_id = ?`, SessionIDForKey(key)); err != nil {
		t.Fatalf("clear plan_state_json err = %v", err)
	}

	rehydrated, err := store.PlanState(key)
	if err != nil {
		t.Fatalf("PlanState(rehydrated) err = %v", err)
	}
	if rehydrated.Explanation != "Recover from transcript state." {
		t.Fatalf("rehydrated explanation = %q, want transcript-derived explanation", rehydrated.Explanation)
	}
	if len(rehydrated.Steps) != 2 || rehydrated.Steps[1].Status != PlanStatusPending {
		t.Fatalf("rehydrated steps = %#v, want transcript-derived plan", rehydrated.Steps)
	}

	events, err := store.PlanEvents(key, 10)
	if err != nil {
		t.Fatalf("PlanEvents() err = %v", err)
	}
	if len(events) == 0 || events[0].Kind != PlanEventKindRehydrated {
		t.Fatalf("plan events = %#v, want transcript rehydration event", events)
	}
}

func TestSaveUpdatesCacheTotalsAndState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 91, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "first", TurnIndex: 1}}, core.TokenUsage{
		InputTokens:      10,
		OutputTokens:     2,
		CacheWriteTokens: 100,
	}); err != nil {
		t.Fatalf("Save(first) err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if reloaded.TotalCacheWrite != 100 {
		t.Fatalf("TotalCacheWrite = %d, want 100", reloaded.TotalCacheWrite)
	}
	if reloaded.CacheState.LastWriteBlock != 1 || reloaded.CacheState.BlocksSinceWrite != 0 {
		t.Fatalf("cache state after write = %#v", reloaded.CacheState)
	}

	reloaded.TurnCount = 2
	if err := store.Save(reloaded, []Message{{Role: "assistant", Content: "second", TurnIndex: 2}}, core.TokenUsage{
		InputTokens:     8,
		OutputTokens:    3,
		CacheReadTokens: 80,
	}); err != nil {
		t.Fatalf("Save(second) err = %v", err)
	}

	finalSession, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(final) err = %v", err)
	}
	if finalSession.TotalCacheRead != 80 {
		t.Fatalf("TotalCacheRead = %d, want 80", finalSession.TotalCacheRead)
	}
	if finalSession.CacheState.BlocksSinceWrite != 1 {
		t.Fatalf("BlocksSinceWrite = %d, want 1", finalSession.CacheState.BlocksSinceWrite)
	}
	if finalSession.CacheState.ConsecutiveMisses != 0 {
		t.Fatalf("ConsecutiveMisses = %d, want 0", finalSession.CacheState.ConsecutiveMisses)
	}
	if finalSession.CacheState.HitRate <= 0 {
		t.Fatalf("HitRate = %f, want positive", finalSession.CacheState.HitRate)
	}
}

func TestCompactMarksOldMessagesAndResetsCacheState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 99, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 3
	sess.CacheState.LastWriteBlock = 3
	sess.CacheState.BlocksSinceWrite = 2
	sess.CacheState.HitRate = 0.5
	sess.CacheState.ConsecutiveMisses = 2
	if err := store.Save(sess, []Message{
		{Role: "user", Content: "turn 1", TurnIndex: 1},
		{Role: "assistant", Content: "reply 1", TurnIndex: 1},
		{Role: "user", Content: "turn 2", TurnIndex: 2},
		{Role: "assistant", Content: "reply 2", TurnIndex: 2},
		{Role: "user", Content: "turn 3", TurnIndex: 3},
		{Role: "assistant", Content: "reply 3", TurnIndex: 3},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if err := store.Compact(key, "summary block", 3); err != nil {
		t.Fatalf("Compact() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if len(reloaded.CompactionLog) != 1 {
		t.Fatalf("compaction log len = %d, want 1", len(reloaded.CompactionLog))
	}
	if reloaded.CompactionLog[0].Strategy != "summarize" {
		t.Fatalf("compaction strategy = %q, want summarize", reloaded.CompactionLog[0].Strategy)
	}
	if reloaded.CacheState.LastWriteBlock != 0 || reloaded.CacheState.BlocksSinceWrite != 0 || reloaded.CacheState.HitRate != 0 || reloaded.CacheState.ConsecutiveMisses != 0 {
		t.Fatalf("cache state after compact = %#v, want reset", reloaded.CacheState)
	}

	compacted := 0
	for _, msg := range reloaded.Messages {
		if msg.Compacted {
			compacted++
		}
	}
	if compacted == 0 {
		t.Fatal("compacted message count = 0, want old messages soft-deleted")
	}
}

func TestRhizomeEventRecordingAndProjectionEdges(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"governor", "memory", "reflection"}); err != nil {
		t.Fatalf("RecordRhizomeEvent(1) err = %v", err)
	}
	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"memory", "reflection"}); err != nil {
		t.Fatalf("RecordRhizomeEvent(2) err = %v", err)
	}

	edges, err := store.TopRhizomeEdges("shared", 10)
	if err != nil {
		t.Fatalf("TopRhizomeEdges() err = %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("TopRhizomeEdges() returned no edges, want at least one")
	}
	if edges[0].LeftConcept != "memory" || edges[0].RightConcept != "reflection" {
		t.Fatalf("top edge = %#v, want memory/reflection strongest edge", edges[0])
	}
	if edges[0].RecurrenceCount != 2 {
		t.Fatalf("top edge recurrence = %d, want 2", edges[0].RecurrenceCount)
	}
}

func TestResetAllRhizomeClearsGraph(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	if err := store.RecordRhizomeEvent("shared", "heartbeat", 1.0, []string{"a", "b"}); err != nil {
		t.Fatalf("RecordRhizomeEvent() err = %v", err)
	}
	if err := store.ResetAllRhizome(); err != nil {
		t.Fatalf("ResetAllRhizome() err = %v", err)
	}

	edges, err := store.TopRhizomeEdges("shared", 10)
	if err != nil {
		t.Fatalf("TopRhizomeEdges() err = %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("edges len = %d, want 0 after reset", len(edges))
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	return store
}

func TestSaveAndLoadFloorSidecar(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1234, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.TurnCount = 1
	sess.LastFloorText = "governor canonical"
	if err := store.Save(sess, []Message{
		{
			Role:      "user",
			Content:   "hello",
			TurnIndex: 1,
		},
		{
			Role:         "assistant",
			Content:      "idolum rendered",
			FloorContent: "governor canonical",
			TurnIndex:    1,
		},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() after save err = %v", err)
	}
	if got.LastFloorText != "governor canonical" {
		t.Fatalf("LastFloorText = %q, want governor canonical", got.LastFloorText)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[1].Content != "idolum rendered" {
		t.Fatalf("assistant visible content = %q, want idolum rendered", got.Messages[1].Content)
	}
	if got.Messages[1].FloorContent != "governor canonical" {
		t.Fatalf("assistant floor content = %q, want governor canonical", got.Messages[1].FloorContent)
	}
}

func TestSavePersistsSessionScopeMetadata(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{
		ChatID: 5001,
		Scope: ScopeRef{
			Kind: ScopeKindHeartbeat,
			ID:   "admin-house",
		},
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	sess.Scope = key.Scope
	sess.TurnCount = 1
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if got.Scope.Kind != ScopeKindHeartbeat || got.Scope.ID != "admin-house" {
		t.Fatalf("Scope = %#v, want heartbeat admin-house", got.Scope)
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
