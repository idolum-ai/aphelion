//go:build linux

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestHandleReviewEventCallbackApprovesCapabilityRequest(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-button-approve",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "approve from callback",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-button-approve",
		MetadataJSON:      `{"request_id":"cap-button-approve","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	sender := &decisionTestSender{}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-review-1",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionApprove),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	updated, ok, err := store.CapabilityRequest("cap-button-approve")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("ReviewStatus = %q, want approved", updated.ReviewStatus)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Capability request approved.") || !strings.Contains(sender.edits[0].text, "Request: cap-button-approve") || !strings.Contains(sender.edits[0].text, "Review event:") || !strings.Contains(sender.edits[0].text, "Capability request cap-button-approve") {
		t.Fatalf("edits = %#v, want durable approved review-event copy", sender.edits)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "" {
		t.Fatalf("answers = %#v, want empty ack", sender.answers)
	}
}

func TestReviewEventCallbackTimeoutIsThirtyMinutes(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{CreatedAt: time.Now().Add(-29 * time.Minute)}
	if reviewEventCallbackExpired(event, time.Now()) {
		t.Fatal("reviewEventCallbackExpired() = true before 30 minutes")
	}
	event.CreatedAt = time.Now().Add(-31 * time.Minute)
	if !reviewEventCallbackExpired(event, time.Now()) {
		t.Fatal("reviewEventCallbackExpired() = false after 30 minutes")
	}
}

func TestHandleReviewEventCallbackExpandAndHideIsReadOnly(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "mail-child", DurableAgentID: "mail-child"},
		TargetAdminChatID: 1001,
		Summary:           "durable_agent=mail-child channel=external_channel interval=2026-04-30T02:38:20Z\nsummary: External-channel wake wake_blocked from child mail-child via adapter mailbox_adapter. EXTERNAL_CHANNEL_STATUS: blocked EXTERNAL_CHANNEL_ERROR: runtime sandbox/tool execution is unavailable.\nlocal: External-channel wake blocked; recorded explicit failure/backoff instead of success.\nrisks: external_channel; adapter_dispatch",
		MetadataJSON:      `{"agent_id":"mail-child","summary":"External-channel wake wake_blocked from child mail-child via adapter mailbox_adapter.","interval_label":"2026-04-30T02:38:20Z","local_actions":["External-channel wake blocked; recorded explicit failure/backoff instead of success."],"risk_flags":["external_channel","adapter_dispatch"],"metadata":{"channel_kind":"external_channel","external_channel_status":"wake_blocked","external_channel_error":"runtime sandbox/tool execution is unavailable in this turn"}}`,
		Status:            "delivered",
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	before, err := store.ReviewEventByID(eventID)
	if err != nil {
		t.Fatalf("ReviewEventByID(before) err = %v", err)
	}
	sender := &decisionTestSender{}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)

	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-expand-review",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionExpand),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery(expand) err = %v", err)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits after expand = %d, want 1", len(sender.edits))
	}
	if !strings.Contains(sender.edits[0].text, "**Metadata**") || !strings.Contains(sender.edits[0].text, "Use Hide details") {
		t.Fatalf("expanded text = %q, want full details", sender.edits[0].text)
	}
	if len(sender.edits[0].rows) != 1 || sender.edits[0].rows[0][0].Text != "Hide details" {
		t.Fatalf("expanded rows = %#v, want Hide details", sender.edits[0].rows)
	}

	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-hide-review",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionHide),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery(hide) err = %v", err)
	}
	if len(sender.edits) != 2 {
		t.Fatalf("edits after hide = %d, want 2", len(sender.edits))
	}
	if !strings.Contains(sender.edits[1].text, "Details has the full child update.") || strings.Contains(sender.edits[1].text, "**Metadata**") {
		t.Fatalf("hidden text = %q, want compact summary", sender.edits[1].text)
	}
	if len(sender.edits[1].rows) != 1 || sender.edits[1].rows[0][0].Text != "Details" {
		t.Fatalf("hidden rows = %#v, want Details", sender.edits[1].rows)
	}
	after, err := store.ReviewEventByID(eventID)
	if err != nil {
		t.Fatalf("ReviewEventByID(after) err = %v", err)
	}
	if before.Status != after.Status || before.MetadataJSON != after.MetadataJSON || before.Summary != after.Summary {
		t.Fatalf("review event mutated: before=%#v after=%#v", before, after)
	}
	if len(sender.answers) != 2 || sender.answers[0].text != "" || sender.answers[1].text != "" {
		t.Fatalf("answers = %#v, want empty callback acknowledgements", sender.answers)
	}
}

func TestHandleReviewEventCallbackExpandRequiresTargetReviewer(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "mail-child", DurableAgentID: "mail-child"},
		TargetAdminChatID: 1001,
		Summary:           "review detail summary",
		MetadataJSON:      `{"metadata":{"debug":"full detail"}}`,
		Status:            "delivered",
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	sender := &decisionTestSender{}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)

	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-expand-review-denied",
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionExpand),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery(expand denied) err = %v", err)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits = %#v, want none for unauthorized detail expansion", sender.edits)
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "target admin") {
		t.Fatalf("answers = %#v, want target admin denial", sender.answers)
	}
}

func TestHandleReviewEventCallbackExpandAllowsCapabilityParent(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:       "cap-parent-details",
		RequestedBy:     "durable_agent:mail-child",
		RequestedFor:    "durable_agent:mail-child",
		ParentPrincipal: "telegram:2002",
		AdminPrincipal:  "telegram:1001",
		Kind:            session.CapabilityKindGenericDelegation,
		TargetResource:  "mailbox",
		Purpose:         "show review detail authorization",
		ReviewStatus:    session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceRole:        "durable_agent",
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "mail-child", DurableAgentID: "mail-child"},
		TargetAdminChatID: 1001,
		Summary:           "capability detail summary",
		MetadataJSON:      `{"request_id":"cap-parent-details","review_status":"proposed","metadata":{"debug":"full detail"}}`,
		Status:            "delivered",
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	sender := &decisionTestSender{}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)

	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-expand-parent",
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionExpand),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery(expand parent) err = %v", err)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "**Metadata**") {
		t.Fatalf("edits = %#v, want authorized expanded details", sender.edits)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "" {
		t.Fatalf("answers = %#v, want empty acknowledgement", sender.answers)
	}
}
