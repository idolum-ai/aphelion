//go:build linux

package session

import "testing"

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

func TestDismissPendingCapabilityReviewEventsTracksDeliveryMessageID(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	oldID, err := store.InsertReviewEvent(ReviewEvent{
		SourceChatID:      101,
		SourceUserID:      202,
		SourceRole:        "capability_request",
		TargetAdminChatID: 9001,
		Summary:           "old capability request",
		MetadataJSON:      `{"request_id":"cap-stale","request_via":"capability_request"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent(old) err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(oldID, 777); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}
	newID, err := store.InsertReviewEvent(ReviewEvent{
		SourceChatID:      101,
		SourceUserID:      202,
		SourceRole:        "capability_request",
		TargetAdminChatID: 9001,
		Summary:           "new capability request",
		MetadataJSON:      `{"request_id":"cap-stale","request_via":"capability_request"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent(new) err = %v", err)
	}

	stale, err := store.DismissPendingCapabilityReviewEvents(9001, "cap-stale", newID)
	if err != nil {
		t.Fatalf("DismissPendingCapabilityReviewEvents() err = %v", err)
	}
	if len(stale) != 1 || stale[0].ID != oldID || stale[0].DeliveryMessageID != 777 {
		t.Fatalf("stale events = %#v, want old event with delivery_message_id 777", stale)
	}
	oldEvent, err := store.ReviewEventByID(oldID)
	if err != nil {
		t.Fatalf("ReviewEventByID(old) err = %v", err)
	}
	if oldEvent.Status != "dismissed" {
		t.Fatalf("old status = %q, want dismissed", oldEvent.Status)
	}
	newEvent, err := store.ReviewEventByID(newID)
	if err != nil {
		t.Fatalf("ReviewEventByID(new) err = %v", err)
	}
	if newEvent.Status != "pending" {
		t.Fatalf("new status = %q, want pending", newEvent.Status)
	}
}
