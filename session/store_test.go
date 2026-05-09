//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
			{Kind: "file_path", Ref: "docs/architecture/organic-agent-owned-tools-proposal.md", Label: "design doc"},
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

func TestDurableAgentRegistryAndStateRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentAgentID:      "house",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:              "help the family group without mutating the house",
			CapabilityEnvelope:   []string{"read_channel", "draft_reply", "synthesize_review"},
			OutboundMode:         "draft_only",
			DriftPolicy:          "admin_ratified",
			PublicSurfaceMode:    "explicit_parent_relay_only",
			TailnetMode:          "tsnet",
			TailnetHostname:      "family-child",
			TailnetTags:          []string{"tag:aphelion-child", "tag:family"},
			TailnetSurfacePolicy: "private_status",
		},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"read_channel", "draft_reply", "synthesize_review", "bounded_review_artifact"},
			AllowedOutboundModes:         []string{"draft_only", "read_only"},
			AllowedPublicSurfaceModes:    []string{"explicit_parent_relay_only", "none"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			BaseURL:        "https://openrouter.example.test",
			Model:          "openrouter/group-model",
			MaxTokens:      256,
		},
		ControlPlaneSecret:     "group-control-secret",
		LocalStorageRoots:      []string{"/tmp/family-group"},
		NetworkPolicy:          "restricted",
		WakeupMode:             "event",
		SecretScopes:           []string{"telegram_bot"},
		AllowedTelegramUserIDs: []int64{2002, 2001, 2001},
		Status:                 "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.AgentID != agent.AgentID || got.ChannelKind != agent.ChannelKind {
		t.Fatalf("DurableAgent() = %#v, want agent %q kind %q", got, agent.AgentID, agent.ChannelKind)
	}
	if len(got.LivePolicy.CapabilityEnvelope) != 3 || got.LivePolicy.CapabilityEnvelope[2] != "synthesize_review" {
		t.Fatalf("CapabilityEnvelope = %#v, want preserved capabilities", got.LivePolicy.CapabilityEnvelope)
	}
	if len(got.SecretScopes) != 1 || got.SecretScopes[0] != "telegram_bot" {
		t.Fatalf("SecretScopes = %#v, want telegram_bot", got.SecretScopes)
	}
	if len(got.AllowedTelegramUserIDs) != 2 || got.AllowedTelegramUserIDs[0] != 2001 || got.AllowedTelegramUserIDs[1] != 2002 {
		t.Fatalf("AllowedTelegramUserIDs = %#v, want [2001 2002]", got.AllowedTelegramUserIDs)
	}
	if len(got.BootstrapCeiling.AllowedOutboundModes) != 2 || got.BootstrapCeiling.AllowedOutboundModes[0] != "draft_only" {
		t.Fatalf("BootstrapCeiling.AllowedOutboundModes = %#v, want preserved ceiling", got.BootstrapCeiling.AllowedOutboundModes)
	}
	if got.LivePolicy.OutboundMode != "draft_only" {
		t.Fatalf("OutboundMode = %q, want draft_only", got.LivePolicy.OutboundMode)
	}
	if got.LivePolicy.TailnetMode != "tsnet" || got.LivePolicy.TailnetHostname != "family-child" || got.LivePolicy.TailnetSurfacePolicy != "private_status" {
		t.Fatalf("tailnet declaration = %#v, want tsnet family-child private_status", got.LivePolicy)
	}
	if len(got.LivePolicy.TailnetTags) != 2 || got.LivePolicy.TailnetTags[0] != "tag:aphelion-child" || got.LivePolicy.TailnetTags[1] != "tag:family" {
		t.Fatalf("TailnetTags = %#v, want persisted tags", got.LivePolicy.TailnetTags)
	}
	if got.BootstrapLLM.Backend != "native" {
		t.Fatalf("BootstrapLLM.Backend = %q, want native", got.BootstrapLLM.Backend)
	}
	if got.BootstrapLLM.NativeProvider != "openrouter" {
		t.Fatalf("BootstrapLLM.NativeProvider = %q, want openrouter", got.BootstrapLLM.NativeProvider)
	}
	if got.BootstrapLLM.APIKey != "sk-or-group" {
		t.Fatalf("BootstrapLLM.APIKey = %q, want sk-or-group", got.BootstrapLLM.APIKey)
	}
	if got.ControlPlaneSecret != "group-control-secret" {
		t.Fatalf("ControlPlaneSecret = %q, want group-control-secret", got.ControlPlaneSecret)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want 1", got.PolicyVersion)
	}
	if got.PolicyHash == "" {
		t.Fatal("PolicyHash is empty, want derived policy hash")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = created:%v updated:%v, want populated", got.CreatedAt, got.UpdatedAt)
	}

	if err := store.SetDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "ratified updated charter",
		CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
		OutboundMode:       "read_only",
		DriftPolicy:        "admin_review",
	}); err != nil {
		t.Fatalf("SetDurableAgentLivePolicy() err = %v", err)
	}
	updated, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent(updated) err = %v", err)
	}
	if updated.LivePolicy.Charter != "ratified updated charter" {
		t.Fatalf("updated charter = %q, want ratified updated charter", updated.LivePolicy.Charter)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated PolicyVersion = %d, want 2", updated.PolicyVersion)
	}
	if updated.PolicyHash == got.PolicyHash {
		t.Fatal("updated PolicyHash did not change after live policy update")
	}
	if updated.BootstrapCeiling.AllowedSharedInferenceReuse[0] != "disabled" {
		t.Fatalf("updated BootstrapCeiling.AllowedSharedInferenceReuse = %#v, want preserved disabled ceiling", updated.BootstrapCeiling.AllowedSharedInferenceReuse)
	}

	listed, err := store.ListDurableAgents()
	if err != nil {
		t.Fatalf("ListDurableAgents() err = %v", err)
	}
	if len(listed) != 1 || listed[0].AgentID != agent.AgentID {
		t.Fatalf("ListDurableAgents() = %#v, want single family-group agent", listed)
	}

	state := core.DurableAgentState{
		AgentID:      agent.AgentID,
		Cursor:       "msg-42",
		Status:       "dormant",
		StateJSON:    `{"last_sender":"alice"}`,
		LastWakeAt:   time.Now().UTC().Add(-5 * time.Minute).Round(0),
		LastReviewAt: time.Now().UTC().Round(0),
	}
	if err := store.SaveDurableAgentState(state); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	gotState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if gotState.Cursor != state.Cursor || gotState.Status != state.Status {
		t.Fatalf("DurableAgentState() = %#v, want cursor/status preserved", gotState)
	}
	if gotState.StateJSON != state.StateJSON {
		t.Fatalf("StateJSON = %q, want %q", gotState.StateJSON, state.StateJSON)
	}

	if err := store.DeleteDurableAgent(agent.AgentID); err != nil {
		t.Fatalf("DeleteDurableAgent() err = %v", err)
	}
	if _, err := store.DurableAgent(agent.AgentID); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("DurableAgent() after delete err = %v, want no rows", err)
	}
	if _, err := store.DurableAgentState(agent.AgentID); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Fatalf("DurableAgentState() after delete err = %v, want no rows", err)
	}
}

func TestDurableAgentRegistryRejectsInvalidAgentID(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:     "../escape",
		ChannelKind: "external_channel",
		Status:      "active",
	})
	if err == nil {
		t.Fatal("UpsertDurableAgent() err = nil, want invalid agent id error")
	}
	if !strings.Contains(err.Error(), "path separators") {
		t.Fatalf("UpsertDurableAgent() err = %v, want path separator context", err)
	}
}

func TestDurableAgentExternalChannelConfigRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review an external child channel and surface important items.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact", "summarize_pdf"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		ChannelConfig: core.DurableAgentChannelConfig{
			External: &core.DurableAgentExternalChannelConfig{
				Address:          "idolum@example.com",
				Account:          "idolum@example.com",
				Adapter:          "child_adapter",
				Query:            "topic:important newer_than:7d",
				PollInterval:     "5m",
				SurfaceRules:     []string{"job opportunity", "external inquiry"},
				SummarizePDFs:    true,
				SynthesisCadence: "4h",
				NeverRetain:      []string{"oauth_token", "password"},
			},
		},
		WakeupMode: "poll",
		Status:     "draft",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	external := got.ChannelConfig.ExternalConfig()
	if external == nil {
		t.Fatal("ChannelConfig.ExternalConfig() = nil, want persisted external channel config")
	}
	if external.Address != "idolum@example.com" {
		t.Fatalf("ChannelConfig.ExternalConfig().Address = %q, want idolum@example.com", external.Address)
	}
	if external.Adapter != "child_adapter" {
		t.Fatalf("ChannelConfig.ExternalConfig().Adapter = %q, want child_adapter", external.Adapter)
	}
	if external.PollInterval != "5m" {
		t.Fatalf("ChannelConfig.ExternalConfig().PollInterval = %q, want 5m", external.PollInterval)
	}
	if !external.SummarizePDFs {
		t.Fatal("ChannelConfig.ExternalConfig().SummarizePDFs = false, want true")
	}
	if len(external.SurfaceRules) != 2 || external.SurfaceRules[0] != "job opportunity" {
		t.Fatalf("ChannelConfig.ExternalConfig().SurfaceRules = %#v, want persisted surface rules", external.SurfaceRules)
	}
	if len(external.NeverRetain) != 2 || external.NeverRetain[1] != "password" {
		t.Fatalf("ChannelConfig.ExternalConfig().NeverRetain = %#v, want persisted never-retain classes", external.NeverRetain)
	}
}

func TestDurableAgentStateSplitRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "state-split-agent",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Observe and report bounded research updates.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-test",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	wakeAt := time.Now().UTC().Add(-10 * time.Minute).Round(0)
	reviewAt := time.Now().UTC().Add(-2 * time.Minute).Round(0)
	runtimeState := core.DurableAgentRuntimeState{
		AgentID:         agent.AgentID,
		Cursor:          "message-42",
		Status:          "awake",
		StateJSON:       `{"continuity":"ok"}`,
		LastApplyStatus: "pending",
		LastApplyError:  "",
		LastWakeAt:      wakeAt,
		LastReviewAt:    reviewAt,
	}
	if err := store.SaveDurableAgentRuntimeState(runtimeState); err != nil {
		t.Fatalf("SaveDurableAgentRuntimeState() err = %v", err)
	}

	issuedAt := time.Now().UTC().Add(-30 * time.Minute).Round(0)
	identityState := core.DurableAgentIdentityState{
		AgentID:                       agent.AgentID,
		LastOfferedPolicyVersion:      3,
		LastOfferedPolicyHash:         "hash-offered",
		LastOfferedPolicyAt:           issuedAt,
		LastAcknowledgedPolicyVersion: 3,
		LastAcknowledgedPolicyHash:    "hash-ack",
		LastAcknowledgedPolicyAt:      issuedAt.Add(2 * time.Minute),
		LastAppliedPolicyVersion:      3,
		LastAppliedPolicyHash:         "hash-applied",
		LastAppliedPolicyAt:           issuedAt.Add(3 * time.Minute),
	}
	if err := store.SaveDurableAgentIdentityState(identityState); err != nil {
		t.Fatalf("SaveDurableAgentIdentityState() err = %v", err)
	}

	gotRuntime, err := store.DurableAgentRuntimeState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRuntimeState() err = %v", err)
	}
	if gotRuntime.Cursor != runtimeState.Cursor || gotRuntime.Status != runtimeState.Status {
		t.Fatalf("DurableAgentRuntimeState() = %#v, want cursor/status preserved", gotRuntime)
	}
	if gotRuntime.LastApplyStatus != "pending" {
		t.Fatalf("DurableAgentRuntimeState().LastApplyStatus = %q, want pending", gotRuntime.LastApplyStatus)
	}

	gotIdentity, err := store.DurableAgentIdentityState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentIdentityState() err = %v", err)
	}
	if gotIdentity.LastAppliedPolicyVersion != 3 {
		t.Fatalf("DurableAgentIdentityState().LastAppliedPolicyVersion = %d, want 3", gotIdentity.LastAppliedPolicyVersion)
	}
	if gotIdentity.LastAppliedPolicyHash != "hash-applied" {
		t.Fatalf("DurableAgentIdentityState().LastAppliedPolicyHash = %q, want hash-applied", gotIdentity.LastAppliedPolicyHash)
	}

	combined, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if combined.Cursor != runtimeState.Cursor {
		t.Fatalf("DurableAgentState().Cursor = %q, want %q", combined.Cursor, runtimeState.Cursor)
	}
	if combined.LastAppliedPolicyVersion != identityState.LastAppliedPolicyVersion {
		t.Fatalf("DurableAgentState().LastAppliedPolicyVersion = %d, want %d", combined.LastAppliedPolicyVersion, identityState.LastAppliedPolicyVersion)
	}

	runtimeState.Status = "dormant"
	runtimeState.DormantAt = time.Now().UTC().Round(0)
	runtimeState.LastApplyStatus = "failed"
	runtimeState.LastApplyError = "child runtime unavailable"
	if err := store.SaveDurableAgentRuntimeState(runtimeState); err != nil {
		t.Fatalf("SaveDurableAgentRuntimeState(update) err = %v", err)
	}

	identityAfterRuntimeUpdate, err := store.DurableAgentIdentityState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentIdentityState(after runtime update) err = %v", err)
	}
	if identityAfterRuntimeUpdate.LastAppliedPolicyVersion != identityState.LastAppliedPolicyVersion {
		t.Fatalf("identity changed by runtime update: got %d want %d", identityAfterRuntimeUpdate.LastAppliedPolicyVersion, identityState.LastAppliedPolicyVersion)
	}

	identityState.LastOfferedPolicyVersion = 4
	identityState.LastOfferedPolicyHash = "hash-offered-4"
	identityState.LastOfferedPolicyAt = time.Now().UTC().Round(0)
	if err := store.SaveDurableAgentIdentityState(identityState); err != nil {
		t.Fatalf("SaveDurableAgentIdentityState(update) err = %v", err)
	}

	runtimeAfterIdentityUpdate, err := store.DurableAgentRuntimeState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRuntimeState(after identity update) err = %v", err)
	}
	if runtimeAfterIdentityUpdate.Status != runtimeState.Status || runtimeAfterIdentityUpdate.LastApplyStatus != runtimeState.LastApplyStatus {
		t.Fatalf("runtime changed by identity update: got %#v want status=%q apply_status=%q", runtimeAfterIdentityUpdate, runtimeState.Status, runtimeState.LastApplyStatus)
	}
}

func TestApplyDurableAgentLivePolicyRejectsBootstrapCeilingWidening(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DurableAgentBootstrapCeiling{
			CapabilityEnvelope:           []string{"group_reply", "bounded_review_artifact"},
			AllowedOutboundModes:         []string{"read_only", "draft_only"},
			AllowedPublicSurfaceModes:    []string{"none"},
			AllowedSharedInferenceReuse:  []string{"disabled"},
			AllowedSharedInferenceScopes: []string{"public_prefix_only"},
		},
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, _, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface bounded family coordination.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "reply_with_policy_authorization",
		DriftPolicy:        "admin_review",
	}, 0, "attempted widening")
	if err == nil {
		t.Fatal("ApplyDurableAgentLivePolicy() err = nil, want bootstrap ceiling violation")
	}
	if !strings.Contains(err.Error(), "bootstrap ceiling") {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v, want bootstrap ceiling violation", err)
	}

	got, err := store.DurableAgent(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if got.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("LivePolicy.OutboundMode = %q, want unchanged read_only", got.LivePolicy.OutboundMode)
	}
	if got.PolicyVersion != 1 {
		t.Fatalf("PolicyVersion = %d, want unchanged 1", got.PolicyVersion)
	}
}

func TestApplyDurableAgentLivePolicyTracksOfferedStateAndRatifiedOutcome(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		},
		BootstrapCeiling: core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DurableAgentLivePolicy{
			Charter:            "Observe and surface bounded family coordination.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-or-group",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	updated, update, err := store.ApplyDurableAgentLivePolicy(agent.AgentID, core.DurableAgentLivePolicy{
		Charter:            "Observe and surface family coordination, but allow reviewed drafting.",
		CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
		OutboundMode:       "draft_only",
		DriftPolicy:        "admin_review",
	}, 42, "ratified family-group narrowing")
	if err != nil {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v", err)
	}
	if update == nil {
		t.Fatal("ApplyDurableAgentLivePolicy() update = nil, want policy update record")
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated.PolicyVersion = %d, want 2", updated.PolicyVersion)
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastOfferedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastOfferedPolicyVersion = %d, want %d", state.LastOfferedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastOfferedPolicyHash != updated.PolicyHash {
		t.Fatalf("LastOfferedPolicyHash = %q, want %q", state.LastOfferedPolicyHash, updated.PolicyHash)
	}
	if state.LastApplyStatus != "pending" {
		t.Fatalf("LastApplyStatus = %q, want pending", state.LastApplyStatus)
	}
	if state.LastApplyError != "" {
		t.Fatalf("LastApplyError = %q, want empty", state.LastApplyError)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if len(continuity.RatifiedOutcomes) != 1 {
		t.Fatalf("RatifiedOutcomes len = %d, want 1", len(continuity.RatifiedOutcomes))
	}
	if continuity.RatifiedOutcomes[0].PolicyVersion != updated.PolicyVersion {
		t.Fatalf("RatifiedOutcomes[0].PolicyVersion = %d, want %d", continuity.RatifiedOutcomes[0].PolicyVersion, updated.PolicyVersion)
	}
	if continuity.RatifiedOutcomes[0].SourceReviewEventID != 42 {
		t.Fatalf("RatifiedOutcomes[0].SourceReviewEventID = %d, want 42", continuity.RatifiedOutcomes[0].SourceReviewEventID)
	}
	if !strings.Contains(continuity.RatifiedOutcomes[0].Summary, "ratified family-group narrowing") {
		t.Fatalf("RatifiedOutcomes[0].Summary = %q, want operator reason", continuity.RatifiedOutcomes[0].Summary)
	}
}

func TestInitRejectsUnsupportedLegacySessionSchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (1)`,
		`CREATE TABLE sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			system_prompt TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			chat_type TEXT NOT NULL DEFAULT 'dm',
			chat_title TEXT,
			user_name TEXT,
			cache_last_write_block INTEGER NOT NULL DEFAULT 0,
			cache_blocks_since INTEGER NOT NULL DEFAULT 0,
			cache_last_write_time TEXT,
			cache_hit_rate REAL NOT NULL DEFAULT 0.0,
			cache_consecutive_misses INTEGER NOT NULL DEFAULT 0,
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_read INTEGER NOT NULL DEFAULT 0,
			total_cache_write INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			active_tool_calls INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			PRIMARY KEY (chat_id, user_id)
		)`,
	}
	for _, ddl := range legacyDDL {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply legacy ddl: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	_, err = NewSQLiteStore(dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore() err = nil, want unsupported legacy schema error")
	}
	if !strings.Contains(err.Error(), "unsupported legacy database schema version 1") {
		t.Fatalf("NewSQLiteStore() err = %v, want unsupported legacy schema version message", err)
	}
}

func TestInitRejectsUnsupportedLegacySessionIdentitySchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-plan-events.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	chatID := int64(321)
	sessionID := SessionIDFromParts(chatID, 0, ScopeRef{})

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (9)`,
		`CREATE TABLE sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			system_prompt TEXT,
			last_floor_text TEXT,
			last_floor_metadata TEXT,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			chat_type TEXT NOT NULL DEFAULT 'dm',
			chat_title TEXT,
			user_name TEXT,
			cache_last_write_block INTEGER NOT NULL DEFAULT 0,
			cache_blocks_since INTEGER NOT NULL DEFAULT 0,
			cache_last_write_time TEXT,
			cache_hit_rate REAL NOT NULL DEFAULT 0.0,
			cache_consecutive_misses INTEGER NOT NULL DEFAULT 0,
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_read INTEGER NOT NULL DEFAULT 0,
			total_cache_write INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			active_tool_calls INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			PRIMARY KEY (chat_id, user_id)
		)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			floor_content TEXT,
			floor_metadata TEXT,
			tool_calls TEXT,
			tool_id TEXT,
			tool_name TEXT,
			thinking TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE review_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_session_id TEXT,
			source_chat_id INTEGER NOT NULL DEFAULT 0,
			source_user_id INTEGER NOT NULL DEFAULT 0,
			source_role TEXT NOT NULL,
			source_scope_kind TEXT NOT NULL DEFAULT '',
			source_scope_id TEXT NOT NULL DEFAULT '',
			source_durable_agent_id TEXT NOT NULL DEFAULT '',
			target_session_id TEXT,
			target_chat_id INTEGER NOT NULL DEFAULT 0,
			target_scope_kind TEXT NOT NULL DEFAULT '',
			target_scope_id TEXT NOT NULL DEFAULT '',
			target_durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_from INTEGER,
			turn_to INTEGER,
			summary TEXT NOT NULL,
			metadata_json TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			delivered_at TEXT
		)`,
		`CREATE TABLE turn_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			request_text TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT,
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_tool_name TEXT,
			last_tool_preview TEXT,
			tool_calls_started INTEGER NOT NULL DEFAULT 0,
			tool_calls_finished INTEGER NOT NULL DEFAULT 0,
			last_tool_result_preview TEXT,
			last_tool_error TEXT,
			progress_message_id INTEGER,
			error_text TEXT,
			recovery_summary TEXT,
			recovery_logged_at TEXT
		)`,
		`CREATE TABLE compaction_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			timestamp TEXT NOT NULL DEFAULT (datetime('now')),
			turns_before INTEGER,
			turns_after INTEGER,
			tokens_before INTEGER,
			tokens_after INTEGER,
			summary TEXT,
			strategy TEXT NOT NULL DEFAULT 'summarize'
		)`,
		`CREATE TABLE plan_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			event_kind TEXT NOT NULL,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
		)`,
	}
	for _, ddl := range legacyDDL {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply legacy ddl: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO sessions(session_id, chat_id, user_id, system_prompt)
		VALUES (?, ?, 0, 'legacy prompt')
	`, sessionID, chatID); err != nil {
		t.Fatalf("insert legacy session: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO plan_events(session_id, event_kind, plan_state_json)
		VALUES (?, 'update_plan', '{"steps":[{"step":"repair startup","status":"pending"}]}')
	`, sessionID); err != nil {
		t.Fatalf("insert legacy plan event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	_, err = NewSQLiteStore(dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore() err = nil, want unsupported legacy schema error")
	}
	if !strings.Contains(err.Error(), "unsupported legacy database schema version 9") {
		t.Fatalf("NewSQLiteStore() err = %v, want unsupported legacy schema version message", err)
	}
}

func TestInitRejectsUnsupportedLegacyDurableAgentSchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-durable.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (10)`,
		`CREATE TABLE durable_agents (
			agent_id TEXT PRIMARY KEY,
			parent_agent_id TEXT,
			parent_scope_kind TEXT,
			parent_scope_id TEXT,
			review_target_chat_id INTEGER NOT NULL DEFAULT 0,
			channel_kind TEXT NOT NULL,
			charter TEXT NOT NULL DEFAULT '',
			capability_envelope_json TEXT NOT NULL DEFAULT '[]',
			local_storage_roots_json TEXT NOT NULL DEFAULT '[]',
			network_policy TEXT,
			wakeup_mode TEXT,
			outbound_mode TEXT,
			drift_policy TEXT,
			secret_scopes_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE durable_agent_state (
			agent_id TEXT PRIMARY KEY,
			cursor TEXT,
			status TEXT,
			state_json TEXT,
			last_wake_at TEXT,
			last_review_at TEXT,
			dormant_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO durable_agents(
			agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id, channel_kind, charter,
			capability_envelope_json, local_storage_roots_json, wakeup_mode, outbound_mode, drift_policy, secret_scopes_json, status,
			created_at, updated_at
		) VALUES (
			'family-group', 'heartbeat', 'admin-house', 1001, 'telegram_group', 'legacy charter',
			'["group_reply","bounded_review_artifact"]', '["/tmp/family-group"]', 'telegram_update', 'reply_within_charter', 'admin_review', '["telegram_bot"]', 'active',
			'2026-04-12T00:00:00Z', '2026-04-12T00:10:00Z'
		)`,
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy durable stmt %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	_, err = NewSQLiteStore(dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore() err = nil, want unsupported legacy schema error")
	}
	if !strings.Contains(err.Error(), "unsupported legacy database schema version 10") {
		t.Fatalf("NewSQLiteStore() err = %v, want unsupported legacy schema version message", err)
	}
}

func TestRecordOutboundAndQueryAfterTurn(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 77, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 2
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	if err := store.RecordOutbound(key, 1, 100, "text"); err != nil {
		t.Fatalf("RecordOutbound(turn=1) err = %v", err)
	}
	if err := store.RecordOutbound(key, 3, 101, "voice"); err != nil {
		t.Fatalf("RecordOutbound(turn=3) err = %v", err)
	}

	got, err := store.OutboundAfterTurn(key, 1)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(got) != 1 || got[0] != 101 {
		t.Fatalf("OutboundAfterTurn() = %#v, want [101]", got)
	}
}

func TestTurnRunLifecycleAndRecovery(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 900, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "inspect repo")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if run.Status != TurnRunStatusRunning {
		t.Fatalf("begin status = %q, want running", run.Status)
	}

	if err := store.NoteTurnRunToolStart(run.ID, "exec", `{"command":"rg foo"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, "stdout:\nmatch", ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 12345); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	interrupted, err := store.InterruptRunningTurnRuns()
	if err != nil {
		t.Fatalf("InterruptRunningTurnRuns() err = %v", err)
	}
	if len(interrupted) != 1 {
		t.Fatalf("interrupted len = %d, want 1", len(interrupted))
	}
	if interrupted[0].ID != run.ID {
		t.Fatalf("interrupted run id = %d, want %d", interrupted[0].ID, run.ID)
	}
	if interrupted[0].ToolCallsStarted != 1 {
		t.Fatalf("tool_calls_started = %d, want 1", interrupted[0].ToolCallsStarted)
	}
	if interrupted[0].ToolCallsFinished != 1 {
		t.Fatalf("tool_calls_finished = %d, want 1", interrupted[0].ToolCallsFinished)
	}
	if interrupted[0].LastToolResultPreview != "stdout:\nmatch" {
		t.Fatalf("last_tool_result_preview = %q, want stdout match", interrupted[0].LastToolResultPreview)
	}
	if interrupted[0].ProgressMessageID != 12345 {
		t.Fatalf("progress_message_id = %d, want 12345", interrupted[0].ProgressMessageID)
	}

	pending, err := store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	if pending[0].Status != TurnRunStatusInterrupted {
		t.Fatalf("pending status = %q, want interrupted", pending[0].Status)
	}

	if err := store.MarkTurnRunsRecovered([]int64{run.ID}, "check logs before retry"); err != nil {
		t.Fatalf("MarkTurnRunsRecovered() err = %v", err)
	}

	pending, err = store.PendingRecoveryTurnRuns(10)
	if err != nil {
		t.Fatalf("PendingRecoveryTurnRuns() after recovery err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len after recovery = %d, want 0", len(pending))
	}
}

func TestStaleRunningTurnRuns(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 905, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	staleRun, err := store.BeginTurnRun(key, TurnRunKindInteractive, "stale run")
	if err != nil {
		t.Fatalf("BeginTurnRun(stale) err = %v", err)
	}
	freshRun, err := store.BeginTurnRun(key, TurnRunKindInteractive, "fresh run")
	if err != nil {
		t.Fatalf("BeginTurnRun(fresh) err = %v", err)
	}
	if err := store.CompleteTurnRun(freshRun.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun(fresh) err = %v", err)
	}

	staleAt := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`UPDATE turn_runs SET last_activity_at = ? WHERE id = ?`, staleAt, staleRun.ID); err != nil {
		t.Fatalf("mark stale run activity: %v", err)
	}

	cutoff := time.Now().UTC().Add(-5 * time.Minute)
	runs, err := store.StaleRunningTurnRuns(cutoff, 10)
	if err != nil {
		t.Fatalf("StaleRunningTurnRuns() err = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("stale runs len = %d, want 1", len(runs))
	}
	if runs[0].ID != staleRun.ID {
		t.Fatalf("stale run id = %d, want %d", runs[0].ID, staleRun.ID)
	}
	if runs[0].Status != TurnRunStatusRunning {
		t.Fatalf("stale run status = %q, want running", runs[0].Status)
	}
}

func TestStaleRunningTurnRunsDetectsUnmatchedToolStartDespiteHeartbeat(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1905, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1905"}}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "stuck tool run")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "capability_authority", `{"action":"grant_set"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	old := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := store.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   core.ExecutionEventToolStarted,
		Stage:       "tool",
		Status:      "started",
		PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"capability_authority"}`, run.ID),
		CreatedAt:   old,
	}); err != nil {
		t.Fatalf("AppendExecutionEvent(tool.started) err = %v", err)
	}
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity() err = %v", err)
	}

	activityCutoff := time.Now().UTC().Add(-5 * time.Minute)
	toolCutoff := time.Now().UTC().Add(-5 * time.Minute)
	runs, err := store.StaleRunningTurnRunsWithUnmatchedToolCutoff(activityCutoff, toolCutoff, 10)
	if err != nil {
		t.Fatalf("StaleRunningTurnRunsWithUnmatchedToolCutoff() err = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("stale runs = %#v, want run %d despite fresh heartbeat", runs, run.ID)
	}
}

func TestStaleRunningTurnRunsIgnoresMatchedToolStartWithFreshHeartbeat(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1906, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1906"}}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "matched tool run")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(run.ID, "capability_authority", `{"action":"grant_set"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, "[CAPABILITY_GRANT]", ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	old := time.Now().UTC().Add(-10 * time.Minute)
	if _, err := store.AppendExecutionEvents(key, []ExecutionEventInput{
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"capability_authority"}`, run.ID),
			CreatedAt:   old,
		},
		{
			EventType:   core.ExecutionEventToolSucceeded,
			Stage:       "tool",
			Status:      "succeeded",
			PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"capability_authority"}`, run.ID),
			CreatedAt:   old.Add(time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(tool lifecycle) err = %v", err)
	}
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity() err = %v", err)
	}

	activityCutoff := time.Now().UTC().Add(-5 * time.Minute)
	toolCutoff := time.Now().UTC().Add(-5 * time.Minute)
	runs, err := store.StaleRunningTurnRunsWithUnmatchedToolCutoff(activityCutoff, toolCutoff, 10)
	if err != nil {
		t.Fatalf("StaleRunningTurnRunsWithUnmatchedToolCutoff() err = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("stale runs = %#v, want none for matched tool lifecycle", runs)
	}
}

func TestTouchTurnRunActivity(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 906, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, TurnRunKindInteractive, "long running turn")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	before, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(before) err = %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity() err = %v", err)
	}
	after, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(after) err = %v", err)
	}
	if !after.LastActivityAt.After(before.LastActivityAt) {
		t.Fatalf("last_activity_at = %s, want > %s", after.LastActivityAt.Format(time.RFC3339Nano), before.LastActivityAt.Format(time.RFC3339Nano))
	}

	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}
	completed, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(completed) err = %v", err)
	}
	lastActivity := completed.LastActivityAt

	time.Sleep(2 * time.Millisecond)
	if err := store.TouchTurnRunActivity(run.ID); err != nil {
		t.Fatalf("TouchTurnRunActivity(completed) err = %v", err)
	}
	completedAfterTouch, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(completedAfterTouch) err = %v", err)
	}
	if !completedAfterTouch.LastActivityAt.Equal(lastActivity) {
		t.Fatalf("completed last_activity_at changed from %s to %s; expected unchanged for non-running turns", lastActivity.Format(time.RFC3339Nano), completedAfterTouch.LastActivityAt.Format(time.RFC3339Nano))
	}
}

func TestContinuationStateIfExistsDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1906, UserID: 0}
	state, exists, err := store.ContinuationStateIfExists(key)
	if err != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("ContinuationStateIfExists() = %#v, exists=%v; want no pre-existing continuation state", state, exists)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&count); err != nil {
		t.Fatalf("query sessions count: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions row count = %d, want 0", count)
	}
}

func TestPlanAndOperationStateIfExistsDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1907, UserID: 0}
	plan, operation, exists, err := store.PlanAndOperationStateIfExists(key)
	if err != nil {
		t.Fatalf("PlanAndOperationStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("PlanAndOperationStateIfExists() = (%#v, %#v, %v), want no pre-existing state", plan, operation, exists)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&count); err != nil {
		t.Fatalf("query sessions count: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions row count = %d, want 0", count)
	}
}

func TestPlanAndOperationStateIfExistsReturnsPersistedState(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1908, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.PlanState = PlanState{
		Steps: []PlanStep{{
			Step:   "Await admin approval",
			Status: PlanStatusInProgress,
		}},
	}
	sess.OperationState = OperationState{
		Status:  OperationStatusBlocked,
		Stage:   "approval_wait",
		Summary: "Waiting for admin review",
	}
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	plan, operation, exists, err := store.PlanAndOperationStateIfExists(key)
	if err != nil {
		t.Fatalf("PlanAndOperationStateIfExists() err = %v", err)
	}
	if !exists {
		t.Fatal("PlanAndOperationStateIfExists() exists = false, want true")
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Step != "Await admin approval" || plan.Steps[0].Status != PlanStatusInProgress {
		t.Fatalf("plan state = %#v, want persisted in-progress step", plan)
	}
	if operation.Status != OperationStatusBlocked || operation.Stage != "approval_wait" || operation.Summary != "Waiting for admin review" {
		t.Fatalf("operation state = %#v, want persisted blocked operation state", operation)
	}
}

func TestStatusStateIfExistsDoesNotCreateSession(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1909, UserID: 0}
	state, exists, err := store.StatusStateIfExists(key)
	if err != nil {
		t.Fatalf("StatusStateIfExists() err = %v", err)
	}
	if exists {
		t.Fatalf("StatusStateIfExists() = (%#v, %v), want no pre-existing state", state, exists)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, SessionIDForKey(key)).Scan(&count); err != nil {
		t.Fatalf("query sessions count: %v", err)
	}
	if count != 0 {
		t.Fatalf("sessions row count = %d, want 0", count)
	}
}

func TestStatusStateIfExistsReturnsPersistedStateAndOutboundCount(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 1910, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.PlanState = PlanState{
		Steps: []PlanStep{{
			Step:   "Await admin approval",
			Status: PlanStatusInProgress,
		}},
	}
	sess.OperationState = OperationState{
		Status:  OperationStatusBlocked,
		Stage:   "approval_wait",
		Summary: "Waiting for admin review",
	}
	sess.LastFloorMetadata = `{"hidden_inputs":[{"category":"unresolved_memory_state","summary":"follow-up question still open"}],"provenance_summary":"latent unresolved memory persists"}`
	sess.TurnCount = 3
	if err := store.Save(sess, nil, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	if err := store.RecordOutbound(key, sess.TurnCount, 4501, "message"); err != nil {
		t.Fatalf("RecordOutbound() err = %v", err)
	}

	state, exists, err := store.StatusStateIfExists(key)
	if err != nil {
		t.Fatalf("StatusStateIfExists() err = %v", err)
	}
	if !exists {
		t.Fatal("StatusStateIfExists() exists = false, want true")
	}
	if len(state.PlanState.Steps) != 1 || state.PlanState.Steps[0].Step != "Await admin approval" {
		t.Fatalf("plan state = %#v, want persisted plan step", state.PlanState)
	}
	if state.OperationState.Status != OperationStatusBlocked || state.OperationState.Stage != "approval_wait" {
		t.Fatalf("operation state = %#v, want persisted blocked operation", state.OperationState)
	}
	if state.LastFloorMetadata == "" {
		t.Fatalf("LastFloorMetadata = %q, want persisted metadata", state.LastFloorMetadata)
	}
	if state.TurnCount != 3 {
		t.Fatalf("TurnCount = %d, want 3", state.TurnCount)
	}
	if state.OutboundCountAtTurn != 1 {
		t.Fatalf("OutboundCountAtTurn = %d, want 1", state.OutboundCountAtTurn)
	}
}

func TestCompleteTurnRun(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 901, UserID: 0}
	if _, err := store.Load(key); err != nil {
		t.Fatalf("Load() err = %v", err)
	}

	run, err := store.BeginTurnRun(key, TurnRunKindCron, "cron work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}

	rows, err := store.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE id = ?
	`, run.ID)
	if err != nil {
		t.Fatalf("query completed turn run: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected completed turn run row")
	}
	got, err := scanTurnRun(rows)
	if err != nil {
		t.Fatalf("scanTurnRun() err = %v", err)
	}
	if got.Status != TurnRunStatusCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("completed_at is zero, want populated timestamp")
	}
}

func TestArtifactIndexRoundTripAndSearch(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 55, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	sess.LastFloorMetadata = `{"artifacts":[{"artifact_id":"doc-1","kind":"document","source_type":"document","summary":"roadmap.txt","handling":"extract_text","retention":"child_local","fetch_state":"fetched_local","materialized_path":"/tmp/roadmap.txt"}]}`
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	hits, err := store.SearchArtifacts("roadmap", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("artifact hits len = %d, want 1", len(hits))
	}
	if hits[0].ArtifactID != "doc-1" {
		t.Fatalf("ArtifactID = %q, want doc-1", hits[0].ArtifactID)
	}
	if hits[0].Retention != "child_local" {
		t.Fatalf("Retention = %q, want child_local", hits[0].Retention)
	}
	if hits[0].MaterializedPath != "/tmp/roadmap.txt" {
		t.Fatalf("MaterializedPath = %q, want /tmp/roadmap.txt", hits[0].MaterializedPath)
	}

	scoped, err := store.SearchArtifacts("roadmap", 10, &key)
	if err != nil {
		t.Fatalf("SearchArtifacts(scoped) err = %v", err)
	}
	if len(scoped) != 1 || scoped[0].SessionID != SessionIDForKey(key) {
		t.Fatalf("scoped hits = %#v, want one hit in session", scoped)
	}
}

func TestInitBackfillsArtifactIndexFromExistingSessionFloorMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-artifact-index.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (20)`,
		`CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			last_floor_metadata TEXT,
			turn_count INTEGER NOT NULL DEFAULT 0
		)`,
		`INSERT INTO sessions(session_id, chat_id, user_id, last_floor_metadata, turn_count) VALUES (
			'telegram_dm:777', 777, 0,
			'{"artifacts":[{"artifact_id":"legacy-doc-1","kind":"document","source_type":"document","summary":"legacy-roadmap.pdf","handling":"extract_text","retention":"child_local","fetch_state":"fetched_local","materialized_path":"/tmp/legacy-roadmap.pdf"}]}'
			, 12
		)`,
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy artifact stmt %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration err = %v", err)
	}
	defer store.Close()

	hits, err := store.SearchArtifacts("legacy-roadmap", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("artifact hits len = %d, want 1", len(hits))
	}
	if hits[0].ArtifactID != "legacy-doc-1" {
		t.Fatalf("ArtifactID = %q, want legacy-doc-1", hits[0].ArtifactID)
	}
	if hits[0].SessionID != "telegram_dm:777" {
		t.Fatalf("SessionID = %q, want telegram_dm:777", hits[0].SessionID)
	}
	if hits[0].TurnIndex != 12 {
		t.Fatalf("TurnIndex = %d, want 12", hits[0].TurnIndex)
	}
	if hits[0].MaterializedPath != "/tmp/legacy-roadmap.pdf" {
		t.Fatalf("MaterializedPath = %q, want /tmp/legacy-roadmap.pdf", hits[0].MaterializedPath)
	}
}

func TestInitBackfillsDurableAgentIdentityStateFromLegacyDurableAgentStateColumns(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-durable-identity-state.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(seed) err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "legacy-family-group",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Observe and report.",
			CapabilityEnvelope: []string{"group_reply", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "openrouter",
			APIKey:         "sk-test",
			Model:          "openrouter/test-model",
		},
		Status: "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	if err := store.SaveDurableAgentRuntimeState(core.DurableAgentRuntimeState{
		AgentID:         agent.AgentID,
		Cursor:          "legacy-cursor",
		Status:          "dormant",
		LastApplyStatus: "failed",
		LastApplyError:  "legacy runtime failure",
	}); err != nil {
		t.Fatalf("SaveDurableAgentRuntimeState(seed) err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(seed store) err = %v", err)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	legacyMutations := []string{
		`DROP TABLE durable_agent_identity_state`,
		`DELETE FROM schema_version`,
		`INSERT INTO schema_version(version) VALUES (25)`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_offered_policy_version INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_offered_policy_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_offered_policy_at TEXT`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_acknowledged_policy_version INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_acknowledged_policy_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_acknowledged_policy_at TEXT`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_applied_policy_version INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_applied_policy_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE durable_agent_state ADD COLUMN last_applied_policy_at TEXT`,
		`UPDATE durable_agent_state
			SET last_offered_policy_version = 7,
				last_offered_policy_hash = 'legacy-offered-hash',
				last_offered_policy_at = '2026-04-21T10:00:00Z',
				last_acknowledged_policy_version = 7,
				last_acknowledged_policy_hash = 'legacy-ack-hash',
				last_acknowledged_policy_at = '2026-04-21T10:01:00Z',
				last_applied_policy_version = 6,
				last_applied_policy_hash = 'legacy-applied-hash',
				last_applied_policy_at = '2026-04-21T10:02:00Z'
			WHERE agent_id = 'legacy-family-group'`,
	}
	for _, stmt := range legacyMutations {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy mutation %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite db: %v", err)
	}

	migrated, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(migrated) err = %v", err)
	}
	defer migrated.Close()

	identity, err := migrated.DurableAgentIdentityState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentIdentityState() err = %v", err)
	}
	if identity.LastOfferedPolicyVersion != 7 {
		t.Fatalf("LastOfferedPolicyVersion = %d, want 7", identity.LastOfferedPolicyVersion)
	}
	if identity.LastAcknowledgedPolicyVersion != 7 {
		t.Fatalf("LastAcknowledgedPolicyVersion = %d, want 7", identity.LastAcknowledgedPolicyVersion)
	}
	if identity.LastAppliedPolicyVersion != 6 {
		t.Fatalf("LastAppliedPolicyVersion = %d, want 6", identity.LastAppliedPolicyVersion)
	}
	if identity.LastAppliedPolicyHash != "legacy-applied-hash" {
		t.Fatalf("LastAppliedPolicyHash = %q, want legacy-applied-hash", identity.LastAppliedPolicyHash)
	}

	runtimeState, err := migrated.DurableAgentRuntimeState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentRuntimeState() err = %v", err)
	}
	if runtimeState.LastApplyStatus != "failed" || runtimeState.LastApplyError != "legacy runtime failure" {
		t.Fatalf("runtime state = %#v, want preserved runtime apply posture", runtimeState)
	}
}

func TestArtifactIndexPreservesRepeatedArtifactIDsAcrossTurnsAndSessions(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	keyA := SessionKey{ChatID: 57, UserID: 0}
	sessA, err := store.Load(keyA)
	if err != nil {
		t.Fatalf("Load(keyA) err = %v", err)
	}
	sessA.TurnCount = 1
	sessA.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"first location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessA, []Message{{Role: "assistant", Content: "first", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessA first) err = %v", err)
	}
	sessA.TurnCount = 2
	sessA.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"second location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessA, []Message{{Role: "assistant", Content: "second", TurnIndex: 2}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessA second) err = %v", err)
	}

	keyB := SessionKey{ChatID: 58, UserID: 0}
	sessB, err := store.Load(keyB)
	if err != nil {
		t.Fatalf("Load(keyB) err = %v", err)
	}
	sessB.TurnCount = 1
	sessB.LastFloorMetadata = `{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"third location","handling":"inspect_metadata","retention":"session_reference"}]}`
	if err := store.Save(sessB, []Message{{Role: "assistant", Content: "third", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(sessB) err = %v", err)
	}

	hits, err := store.SearchArtifacts("location", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("artifact hits len = %d, want 3", len(hits))
	}

	seen := map[string]bool{}
	for _, hit := range hits {
		seen[hit.SessionID+"#"+hit.Summary] = true
	}
	if !seen[SessionIDForKey(keyA)+"#first location"] {
		t.Fatalf("missing first occurrence in hits: %#v", hits)
	}
	if !seen[SessionIDForKey(keyA)+"#second location"] {
		t.Fatalf("missing second occurrence in hits: %#v", hits)
	}
	if !seen[SessionIDForKey(keyB)+"#third location"] {
		t.Fatalf("missing third occurrence in hits: %#v", hits)
	}
}

func TestInitRebuildsArtifactIndexFromMessageFloorMetadata(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "legacy-artifact-index-occurrence.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	legacyDDL := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (21)`,
		`CREATE TABLE sessions (
			session_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			last_floor_metadata TEXT,
			turn_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			floor_content TEXT,
			floor_metadata TEXT,
			tool_calls TEXT,
			tool_id TEXT,
			tool_name TEXT,
			thinking TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE artifact_index (
			artifact_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL DEFAULT 0,
			source_type TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			handling TEXT NOT NULL DEFAULT '',
			retention TEXT NOT NULL DEFAULT '',
			fetch_state TEXT NOT NULL DEFAULT '',
			materialized_path TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO sessions(session_id, chat_id, user_id, last_floor_metadata, turn_count) VALUES ('telegram_dm:777', 777, 0, '', 2)`,
		`INSERT INTO messages(session_id, chat_id, user_id, role, content, floor_metadata, turn_index, content_chars, compacted) VALUES
			('telegram_dm:777', 777, 0, 'assistant', 'one', '{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"legacy one","handling":"inspect_metadata","retention":"session_reference"}]}', 1, 3, 0),
			('telegram_dm:777', 777, 0, 'assistant', 'two', '{"artifacts":[{"artifact_id":"telegram:location","kind":"structured","source_type":"location","summary":"legacy two","handling":"inspect_metadata","retention":"session_reference"}]}', 2, 3, 0)`,
	}
	for _, stmt := range legacyDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec legacy artifact stmt %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() migration err = %v", err)
	}
	defer store.Close()

	hits, err := store.SearchArtifacts("legacy", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("artifact hits len = %d, want 2", len(hits))
	}
	if hits[0].TurnIndex == hits[1].TurnIndex {
		t.Fatalf("turn indexes = %d and %d, want distinct occurrences", hits[0].TurnIndex, hits[1].TurnIndex)
	}
}

func TestArtifactIndexIgnoresEphemeralArtifacts(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 56, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	sess.LastFloorMetadata = `{"artifacts":[{"artifact_id":"img-ephemeral","kind":"image","summary":"throwaway","retention":"ephemeral"}]}`
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "ok", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	hits, err := store.SearchArtifacts("throwaway", 10, nil)
	if err != nil {
		t.Fatalf("SearchArtifacts() err = %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("artifact hits len = %d, want 0", len(hits))
	}
}

func TestMessageTurnProvenanceRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 991, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if err := store.Save(sess, []Message{{
		Role:              "user",
		Content:           continuationApprovedEventTextForTest,
		ActorUserID:       1002,
		ActorRole:         "approved_user",
		EventOrigin:       "turn_authorization",
		EventOriginDetail: "continuation",
		TurnIndex:         1,
	}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	got, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() reload err = %v", err)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("message count = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].ActorUserID != 1002 || got.Messages[0].ActorRole != "approved_user" {
		t.Fatalf("actor provenance = %#v, want approved user", got.Messages[0])
	}
	if got.Messages[0].EventOrigin != "turn_authorization" || got.Messages[0].EventOriginDetail != "continuation" {
		t.Fatalf("event provenance = (%q, %q), want turn_authorization/continuation", got.Messages[0].EventOrigin, got.Messages[0].EventOriginDetail)
	}
}

func TestSQLiteStoreCreatesExecutionEventsTable(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	var count int
	err := store.db.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'execution_events'
	`).Scan(&count)
	if err != nil {
		t.Fatalf("query sqlite_master execution_events: %v", err)
	}
	if count != 1 {
		t.Fatalf("execution_events table count = %d, want 1", count)
	}
}

func TestAppendExecutionEventsMonotonicSequenceAndPayloadNormalization(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 3101, UserID: 0}

	first, err := store.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   "ingress.accepted",
		Stage:       "ingress",
		Status:      "accepted",
		PayloadJSON: `{"message_id":1}`,
	})
	if err != nil {
		t.Fatalf("AppendExecutionEvent(first) err = %v", err)
	}
	second, err := store.AppendExecutionEvent(key, ExecutionEventInput{
		EventType:   "turn.started",
		Stage:       "turn",
		Status:      "running",
		CausedBySeq: first.Seq,
		PayloadJSON: "plain payload text",
	})
	if err != nil {
		t.Fatalf("AppendExecutionEvent(second) err = %v", err)
	}
	batch, err := store.AppendExecutionEvents(key, []ExecutionEventInput{
		{EventType: "tool.started", Stage: "tool", Status: "running", CausedBySeq: second.Seq, PayloadJSON: `{}`},
		{EventType: "tool.succeeded", Stage: "tool", Status: "completed", CausedBySeq: second.Seq, PayloadJSON: `{}`},
	})
	if err != nil {
		t.Fatalf("AppendExecutionEvents(batch) err = %v", err)
	}

	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("first/second seq = (%d,%d), want (1,2)", first.Seq, second.Seq)
	}
	if len(batch) != 2 {
		t.Fatalf("batch len = %d, want 2", len(batch))
	}
	if batch[0].Seq != 3 || batch[1].Seq != 4 {
		t.Fatalf("batch seqs = (%d,%d), want (3,4)", batch[0].Seq, batch[1].Seq)
	}
	if !json.Valid([]byte(second.PayloadJSON)) {
		t.Fatalf("normalized second payload is not json: %q", second.PayloadJSON)
	}
	if !strings.Contains(second.PayloadJSON, `"text":"plain payload text"`) {
		t.Fatalf("second payload = %q, want wrapped text payload", second.PayloadJSON)
	}
}

func TestExecutionEventsQueriesBySessionAndChat(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	keyA := SessionKey{ChatID: 4101, UserID: 0}
	keyB := SessionKey{ChatID: 4102, UserID: 0}

	if _, err := store.AppendExecutionEvents(keyA, []ExecutionEventInput{
		{EventType: "ingress.accepted", Stage: "ingress", Status: "accepted", PayloadJSON: `{"message_id":1}`},
		{EventType: "turn.started", Stage: "turn", Status: "running", PayloadJSON: `{}`},
		{EventType: "turn.completed", Stage: "turn", Status: "completed", PayloadJSON: `{}`},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyA) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(keyB, []ExecutionEventInput{
		{EventType: "ingress.accepted", Stage: "ingress", Status: "accepted", PayloadJSON: `{"message_id":2}`},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyB) err = %v", err)
	}

	eventsA, err := store.ExecutionEventsBySession(keyA, 1, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(keyA) err = %v", err)
	}
	if len(eventsA) != 2 {
		t.Fatalf("eventsA len = %d, want 2", len(eventsA))
	}
	if eventsA[0].Seq != 2 || eventsA[1].Seq != 3 {
		t.Fatalf("eventsA seqs = (%d,%d), want (2,3)", eventsA[0].Seq, eventsA[1].Seq)
	}
	if eventsA[0].EventType != "turn.started" || eventsA[1].EventType != "turn.completed" {
		t.Fatalf("eventsA types = (%q,%q), want turn lifecycle", eventsA[0].EventType, eventsA[1].EventType)
	}

	chatEvents, err := store.ExecutionEventsByChat(4101, time.Time{}, 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByChat(4101) err = %v", err)
	}
	if len(chatEvents) != 3 {
		t.Fatalf("chatEvents len = %d, want 3", len(chatEvents))
	}
	if chatEvents[0].Seq != 3 {
		t.Fatalf("chatEvents first seq = %d, want latest seq 3", chatEvents[0].Seq)
	}
}

func TestLatestExecutionEventsBySessionReturnsNewestWindowInAscendingOrder(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 4103, UserID: 0}
	inputs := make([]ExecutionEventInput, 0, 5)
	for i := 0; i < 5; i++ {
		inputs = append(inputs, ExecutionEventInput{EventType: fmt.Sprintf("event.%d", i+1), Stage: "test", Status: "ok", PayloadJSON: `{}`})
	}
	if _, err := store.AppendExecutionEvents(key, inputs); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	events, err := store.LatestExecutionEventsBySession(key, 3)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events len = %d, want 3", len(events))
	}
	if events[0].Seq != 3 || events[1].Seq != 4 || events[2].Seq != 5 {
		t.Fatalf("event seqs = (%d,%d,%d), want latest window in ascending order", events[0].Seq, events[1].Seq, events[2].Seq)
	}
}

func TestExecutionEventsByTypesFiltersAndOrdersByCreatedAt(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	keyA := SessionKey{ChatID: 4201, UserID: 0}
	keyB := SessionKey{ChatID: 4202, UserID: 0}
	if _, err := store.AppendExecutionEvents(keyA, []ExecutionEventInput{
		{
			EventType:   "decision.opened",
			Stage:       "decision",
			Status:      "pending",
			PayloadJSON: `{"decision_id":"d1"}`,
			CreatedAt:   now.Add(-30 * time.Second),
		},
		{
			EventType:   "continuation.offered",
			Stage:       "continuation",
			Status:      "pending",
			PayloadJSON: `{"decision_id":"c1"}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyA) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(keyB, []ExecutionEventInput{
		{
			EventType:   "decision.resolved",
			Stage:       "decision",
			Status:      "resolved",
			PayloadJSON: `{"decision_id":"d1"}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents(keyB) err = %v", err)
	}

	events, err := store.ExecutionEventsByTypes([]string{
		"decision.opened",
		"decision.resolved",
	}, now.Add(-40*time.Second), 10)
	if err != nil {
		t.Fatalf("ExecutionEventsByTypes() err = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].EventType != "decision.resolved" || events[1].EventType != "decision.opened" {
		t.Fatalf("events order/types = (%q,%q), want desc created_at decision.resolved then decision.opened", events[0].EventType, events[1].EventType)
	}
	if events[0].ChatID != 4202 || events[1].ChatID != 4201 {
		t.Fatalf("events chat ids = (%d,%d), want (4202,4201)", events[0].ChatID, events[1].ChatID)
	}
}

func TestExecutionEventsRecentReturnsNewestFirst(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Now().UTC()
	key := SessionKey{ChatID: 4301, UserID: 0}
	if _, err := store.AppendExecutionEvents(key, []ExecutionEventInput{
		{
			EventType:   "turn.started",
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-30 * time.Second),
		},
		{
			EventType:   "tool.started",
			Stage:       "tool",
			Status:      "running",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-20 * time.Second),
		},
		{
			EventType:   "turn.completed",
			Stage:       "turn",
			Status:      "completed",
			PayloadJSON: `{}`,
			CreatedAt:   now.Add(-10 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	events, err := store.ExecutionEventsRecent(2)
	if err != nil {
		t.Fatalf("ExecutionEventsRecent() err = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].EventType != "turn.completed" || events[1].EventType != "tool.started" {
		t.Fatalf("events order/types = (%q,%q), want turn.completed then tool.started", events[0].EventType, events[1].EventType)
	}
}

func TestSQLiteStoreCapabilityRequestReviewGrantInvocationRoundTrip(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	request, err := store.UpsertCapabilityRequest(CapabilityRequest{
		RequestID:       "cap-1",
		RequestedBy:     "child-agent",
		RequestedFor:    "child-agent",
		ParentPrincipal: "telegram:200",
		Kind:            CapabilityKindPurchase,
		TargetResource:  "amazon",
		Purpose:         "order approved school supplies",
		RiskClass:       "spend",
		Contract:        `{"max_items":3}`,
		Constraints:     `{"max_usd":50}`,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if request.ReviewStatus != CapabilityReviewStatusProposed {
		t.Fatalf("ReviewStatus = %q, want proposed", request.ReviewStatus)
	}

	if _, err := store.AppendCapabilityReview(CapabilityReview{ReviewID: "capr-1", RequestID: "cap-1", Reviewer: "telegram:200", ReviewerRole: "parent", Status: CapabilityReviewStatusParentApproved, Rationale: "bounded spend"}); err != nil {
		t.Fatalf("AppendCapabilityReview(parent) err = %v", err)
	}
	if _, err := store.AppendCapabilityReview(CapabilityReview{ReviewID: "capr-2", RequestID: "cap-1", Reviewer: "telegram:1001", ReviewerRole: "admin", Status: CapabilityReviewStatusApproved, Rationale: "parent endorsed"}); err != nil {
		t.Fatalf("AppendCapabilityReview(admin) err = %v", err)
	}
	request, ok, err := store.CapabilityRequest("cap-1")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok || request.ReviewStatus != CapabilityReviewStatusApproved {
		t.Fatalf("CapabilityRequest() = %#v ok=%t, want approved", request, ok)
	}

	grant, err := store.UpsertCapabilityGrant(CapabilityGrant{
		GrantID:           "capg-1",
		RequestID:         "cap-1",
		GrantedBy:         "telegram:1001",
		GrantedTo:         "child-agent",
		Kind:              CapabilityKindPurchase,
		TargetResource:    "amazon",
		AllowedActions:    []string{"order", "summarize"},
		Contract:          `{"max_items":3}`,
		Constraints:       `{"max_usd":50}`,
		Status:            CapabilityGrantStatusActive,
		AnchorFingerprint: "sha256:test",
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}
	if grant.Status != CapabilityGrantStatusActive || len(grant.AllowedActions) != 2 {
		t.Fatalf("CapabilityGrant = %#v, want active with actions", grant)
	}
	request, _, err = store.CapabilityRequest("cap-1")
	if err != nil {
		t.Fatalf("CapabilityRequest(after grant) err = %v", err)
	}
	if request.GrantID != "capg-1" {
		t.Fatalf("GrantID = %q, want capg-1", request.GrantID)
	}

	active, ok, err := store.ActiveCapabilityGrant(CapabilityKindPurchase, "amazon", "child-agent", "order")
	if err != nil {
		t.Fatalf("ActiveCapabilityGrant() err = %v", err)
	}
	if !ok || active.GrantID != "capg-1" {
		t.Fatalf("ActiveCapabilityGrant() = %#v ok=%t, want capg-1", active, ok)
	}
	if _, ok, err := store.ActiveCapabilityGrant(CapabilityKindPurchase, "amazon", "child-agent", "refund"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant(refund) ok=%t err=%v, want false nil", ok, err)
	}

	if _, err := store.RecordCapabilityInvocation(CapabilityInvocation{GrantID: "capg-1", Principal: "child-agent", Action: "order", Status: "failed", ErrorText: "declined"}); err != nil {
		t.Fatalf("RecordCapabilityInvocation() err = %v", err)
	}
	grant, ok, err = store.CapabilityGrant("capg-1")
	if err != nil {
		t.Fatalf("CapabilityGrant(after invocation) err = %v", err)
	}
	if !ok || grant.InvocationCount != 1 || grant.FailureCount != 1 || grant.LastFailureAt.IsZero() {
		t.Fatalf("CapabilityGrant counters = %#v ok=%t, want one failed invocation", grant, ok)
	}
}

func TestMigrateCapabilityKindSystemChangeConstraint(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "capability-kind-system-change.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open seed db err = %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (36)`,
		`CREATE TABLE capability_requests (
			request_id TEXT PRIMARY KEY,
			requested_by TEXT NOT NULL DEFAULT '',
			requested_for TEXT NOT NULL DEFAULT '',
			parent_principal TEXT NOT NULL DEFAULT '',
			admin_principal TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'generic_delegation' CHECK(kind IN ('tool', 'local_device', 'external_account', 'purchase', 'public_web', 'communication', 'file_access', 'network_access', 'generic_delegation')),
			target_resource TEXT NOT NULL DEFAULT '',
			purpose TEXT NOT NULL DEFAULT '',
			risk_class TEXT NOT NULL DEFAULT '',
			contract_json TEXT NOT NULL DEFAULT '{}',
			constraints_json TEXT NOT NULL DEFAULT '{}',
			review_status TEXT NOT NULL DEFAULT 'proposed' CHECK(review_status IN ('proposed', 'parent_approved', 'approved', 'rejected')),
			grant_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE capability_grants (
			grant_id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL DEFAULT '',
			granted_by TEXT NOT NULL DEFAULT '',
			granted_to TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'generic_delegation' CHECK(kind IN ('tool', 'local_device', 'external_account', 'purchase', 'public_web', 'communication', 'file_access', 'network_access', 'generic_delegation')),
			target_resource TEXT NOT NULL DEFAULT '',
			allowed_actions_json TEXT NOT NULL DEFAULT '[]',
			contract_json TEXT NOT NULL DEFAULT '{}',
			constraints_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'active', 'stale', 'revoked', 'expired', 'failed')),
			baseline_policy_hash TEXT NOT NULL DEFAULT '',
			current_policy_hash TEXT NOT NULL DEFAULT '',
			anchor_fingerprint TEXT NOT NULL DEFAULT '',
			drift_source TEXT NOT NULL DEFAULT '',
			stale_reason TEXT NOT NULL DEFAULT '',
			invocation_count INTEGER NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			granted_at TEXT,
			expires_at TEXT,
			revoked_at TEXT,
			last_invoked_at TEXT,
			last_failure_at TEXT
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			t.Fatalf("seed db stmt err = %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db err = %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(migrated) err = %v", err)
	}
	defer store.Close()

	if _, err := store.UpsertCapabilityRequest(CapabilityRequest{
		RequestID:      "cap-system-change",
		RequestedBy:    "durable_agent:child-alpha",
		RequestedFor:   "durable_agent:child-alpha",
		Kind:           CapabilityKindSystemChange,
		TargetResource: "child-runtime-contract",
		Purpose:        "Request bounded system contract update.",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest(system_change) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(CapabilityGrant{
		GrantID:        "capg-system-change",
		RequestID:      "cap-system-change",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "durable_agent:child-alpha",
		Kind:           CapabilityKindSystemChange,
		TargetResource: "child-runtime-contract",
		AllowedActions: []string{"propose"},
		Status:         CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(system_change) err = %v", err)
	}
}

func TestMigrateDurableChildAuthorityCanonicalizesPrincipalsAndChildRuntime(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "canonicalize-durable-child.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(seed) err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{AgentID: "child-alpha", ChannelKind: "external_channel", Status: "active"}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`
		INSERT INTO capability_requests(request_id, requested_by, requested_for, kind, target_resource, purpose, contract_json, constraints_json, review_status, created_at, updated_at)
		VALUES ('cap-old', 'child-alpha', 'child-alpha', 'tool', 'mail-reader', 'legacy runtime shape', '{"runtime_materialization":{"readonly_paths":["/srv/mail"],"environment":["MAIL_TOKEN"]}}', '{}', 'approved', ?, ?)
	`, now, now); err != nil {
		t.Fatalf("insert legacy request err = %v", err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO capability_grants(grant_id, request_id, granted_by, granted_to, kind, target_resource, allowed_actions_json, contract_json, constraints_json, status, created_at, updated_at, granted_at)
		VALUES ('capg-old', 'cap-old', 'admin', 'child-alpha', 'tool', 'mail-reader', '["invoke"]', '{"runtime_materialization":{"readonly_paths":["/srv/mail"]}}', '{}', 'active', ?, ?, ?)
	`, now, now, now); err != nil {
		t.Fatalf("insert legacy grant err = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE schema_version SET version = 35`); err != nil {
		t.Fatalf("downgrade schema version err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close seed store err = %v", err)
	}

	migrated, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(migrated) err = %v", err)
	}
	defer migrated.Close()

	request, ok, err := migrated.CapabilityRequest("cap-old")
	if err != nil {
		t.Fatalf("CapabilityRequest() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityRequest(cap-old) ok=false")
	}
	if request.RequestedBy != core.DurableAgentPrincipal("child-alpha") || request.RequestedFor != core.DurableAgentPrincipal("child-alpha") {
		t.Fatalf("request principals = %q/%q, want canonical durable agent principal", request.RequestedBy, request.RequestedFor)
	}
	if strings.Contains(request.Contract, "runtime_materialization") || !strings.Contains(request.Contract, "child_runtime") || strings.Contains(request.Contract, "environment") || !strings.Contains(request.Contract, "env_from_parent") {
		t.Fatalf("request contract = %s, want canonical child_runtime without legacy aliases", request.Contract)
	}
	grant, ok, err := migrated.CapabilityGrant("capg-old")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok {
		t.Fatal("CapabilityGrant(capg-old) ok=false")
	}
	if grant.GrantedTo != core.DurableAgentPrincipal("child-alpha") {
		t.Fatalf("grant granted_to = %q, want canonical durable agent principal", grant.GrantedTo)
	}
	if strings.Contains(grant.Contract, "runtime_materialization") || !strings.Contains(grant.Contract, "child_runtime") {
		t.Fatalf("grant contract = %s, want canonical child_runtime", grant.Contract)
	}
}

func TestDurableChildAgreementTracksCapabilityReviewStatus(t *testing.T) {
	store := newTestSQLiteStore(t)

	_, err := store.UpsertDurableChildAgreement(DurableChildAgreement{
		AgreementID:         "agree-1",
		AgentID:             "child-alpha",
		ParentPrincipal:     "telegram:1001",
		ChildPrincipal:      "durable_agent:child-alpha",
		SourceSurface:       "durable_agent.delegation_request",
		SourceRequestID:     "cap-1",
		SourceReviewEventID: 42,
		Summary:             "Child requested a bounded system capability.",
		BoundedEffect:       "Grant scoped runtime access after review.",
		ArtifactRefs:        []RecordReference{{Kind: "review_event", Ref: "42", Label: "delegation request"}},
	})
	if err != nil {
		t.Fatalf("UpsertDurableChildAgreement() err = %v", err)
	}

	if _, err := store.UpsertCapabilityRequest(CapabilityRequest{
		RequestID:      "cap-1",
		RequestedBy:    "durable_agent:child-alpha",
		RequestedFor:   "durable_agent:child-alpha",
		Kind:           CapabilityKindGenericDelegation,
		TargetResource: "runtime-capability",
		Purpose:        "Needs bounded child-local runtime support.",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	if _, err := store.AppendCapabilityReview(CapabilityReview{
		ReviewID:     "review-1",
		RequestID:    "cap-1",
		Reviewer:     "telegram:1001",
		ReviewerRole: "admin",
		Status:       CapabilityReviewStatusApproved,
		Rationale:    "Approved as a bounded parent-child system-change agreement.",
	}); err != nil {
		t.Fatalf("AppendCapabilityReview() err = %v", err)
	}

	agreement, ok, err := store.DurableChildAgreement("agree-1")
	if err != nil {
		t.Fatalf("DurableChildAgreement() err = %v", err)
	}
	if !ok {
		t.Fatal("DurableChildAgreement() ok = false, want true")
	}
	if agreement.Status != DurableChildAgreementStatusApproved {
		t.Fatalf("agreement status = %q, want approved", agreement.Status)
	}
	if len(agreement.ArtifactRefs) != 1 || agreement.ArtifactRefs[0].Kind != "review_event" {
		t.Fatalf("agreement artifact refs = %#v, want persisted review_event ref", agreement.ArtifactRefs)
	}
}

func TestContinuationStatePersistsActionProposalAndLease(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	key := SessionKey{ChatID: 1901, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1901"}}
	expiresAt := time.Now().UTC().Add(2 * time.Hour).Round(0)
	state := ContinuationState{
		Status:         ContinuationStatusPending,
		DecisionID:     "decision-action-proposal",
		Objective:      "Implement ActionProposal and ContinuationLease v1.",
		StageSummary:   "Wire continuation approval to a lease.",
		RemainingTurns: 1,
		ActionProposal: ActionProposal{
			ID:               "aprop-action-lease",
			OperationID:      "op-action-lease",
			MissionID:        "mission-ledger-runtime",
			Summary:          "Implement the approval-button primitive.",
			WhyNow:           "Continuation and deploy approvals need a reusable bounded contract.",
			BoundedEffect:    "Local source/docs/tests only.",
			RiskClass:        "system_change",
			AllowedActions:   []string{"edit_repo", "run_tests", "edit_repo"},
			ForbiddenActions: []string{"external_account", "purchase"},
			ValidationPlan:   []string{"go test ./session", "go test ./runtime"},
			ExpiresAt:        expiresAt,
			PlanHash:         "sha256:test-plan",
		},
		ContinuationLease: ContinuationLease{
			ID:             "lease-action-lease",
			ProposalID:     "aprop-action-lease",
			MissionID:      "mission-ledger-runtime",
			Status:         ContinuationLeaseStatusPending,
			MaxTurns:       1,
			RemainingTurns: 1,
			ExpiresAt:      expiresAt,
			PlanHash:       "sha256:test-plan",
		},
	}
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ActionProposal.ID != "aprop-action-lease" || got.ActionProposal.Status != ProposalStatusPending {
		t.Fatalf("ActionProposal = %#v, want persisted pending proposal", got.ActionProposal)
	}
	if len(got.ActionProposal.AllowedActions) != 2 {
		t.Fatalf("AllowedActions = %#v, want deduped action list", got.ActionProposal.AllowedActions)
	}
	if got.ContinuationLease.ID != "lease-action-lease" || got.ContinuationLease.Status != ContinuationLeaseStatusPending {
		t.Fatalf("ContinuationLease = %#v, want persisted pending lease", got.ContinuationLease)
	}
	if got.ContinuationLease.MaxTurns != 1 || got.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("ContinuationLease turns = %d/%d, want 1/1", got.ContinuationLease.MaxTurns, got.ContinuationLease.RemainingTurns)
	}
	if got.ContinuationLease.ExpiresAt.IsZero() || got.ContinuationLease.ExpiresAt.UTC() != expiresAt.UTC() {
		t.Fatalf("Lease ExpiresAt = %v, want %v", got.ContinuationLease.ExpiresAt, expiresAt)
	}
}

func TestOperationPlanLeaseRoundTripAndDefaults(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	key := SessionKey{ChatID: 8080, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "8080"}}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.OperationState = OperationState{
		ID:        "op-plan-lease",
		Objective: "Reduce approval pings without widening authority.",
		Status:    OperationStatusBlocked,
		Stage:     "plan_lease_proposal",
		PlanLease: OperationPlanLease{
			ID:              "plan-lease-20260503",
			Summary:         "Low-risk coordination lease",
			Status:          PlanLeaseStatusProposed,
			TurnBudget:      5,
			CoveredPhaseIDs: []string{"phase-1", "phase-2"},
			Lanes: []OperationPlanLeaseLane{
				{ID: "readonly", Summary: "Read-only review", AuthorityClass: "read-only review", ExpectedTurns: 3, AllowedActions: []string{"inspect_status"}},
				{ID: "workspace", Summary: "Local patch", AuthorityClass: "workspace_write", ExpectedTurns: 2, ForbiddenActions: []string{"deploy"}},
			},
			EvidenceDigest: OperationPlanLeaseEvidenceDigest{
				TurnsSpent:   1,
				LanesUsed:    []string{"readonly"},
				Completed:    []string{"summarized status"},
				ResidualRisk: "Implementation not deployed.",
			},
		},
	}
	if err := store.Save(sess, []Message{{Role: "assistant", Content: "plan lease proposed", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}

	reloaded, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	lease := reloaded.PlanLease
	if lease.ID != "plan-lease-20260503" || lease.Status != PlanLeaseStatusProposed {
		t.Fatalf("plan lease = %#v, want proposed persisted lease", lease)
	}
	if lease.TurnBudget != 5 || lease.RemainingTurns != 5 || len(lease.Lanes) != 2 {
		t.Fatalf("plan lease turns/lanes = %#v", lease)
	}
	if lease.Lanes[0].AuthorityClass != "read_only_review" || lease.Lanes[0].ExpectedTurns != 3 {
		t.Fatalf("plan lease first lane = %#v, want normalized authority and expected turns", lease.Lanes[0])
	}
	if len(lease.HardInterrupts) == 0 || len(lease.ChildInitiationLanes) == 0 {
		t.Fatalf("plan lease guardrails = hard=%#v child=%#v, want defaults", lease.HardInterrupts, lease.ChildInitiationLanes)
	}
	if !stringSliceContains(lease.HardInterrupts, "policy_or_grant_change") || !stringSliceContains(lease.ChildInitiationLanes, "capability_request") {
		t.Fatalf("plan lease guardrails = hard=%#v child=%#v, want hard gates and review lanes", lease.HardInterrupts, lease.ChildInitiationLanes)
	}
	if lease.EvidenceDigest.TurnsSpent != 1 || lease.EvidenceDigest.ResidualRisk == "" {
		t.Fatalf("evidence digest = %#v, want bounded digest persisted", lease.EvidenceDigest)
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
