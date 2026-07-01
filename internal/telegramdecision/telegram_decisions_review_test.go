//go:build linux

package telegramdecision

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

// These tests stay in package telegramdecision because review-event callback
// behavior is owned by this boundary; root only assembles transport/control
// dependencies and dispatches into it.
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
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
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

func TestHandleReactionMessageApprovesDeliveredCapabilityReviewEvent(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-reaction-approve",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "approve from reaction",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-approve",
		MetadataJSON:      `{"request_id":"cap-reaction-approve","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 77); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 77,
		Reaction:  &core.InboundReaction{MessageID: 77, New: []string{"👍"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want true")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-approve")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("ReviewStatus = %q, want approved", updated.ReviewStatus)
	}
	if len(sender.edits) != 1 || sender.edits[0].chatID != 1001 || sender.edits[0].messageID != 77 || !strings.Contains(sender.edits[0].text, "Capability request approved.") {
		t.Fatalf("edits = %#v, want approved card edit for reacted message", sender.edits)
	}
	if len(sender.answers) != 0 {
		t.Fatalf("answers = %#v, want no callback-query answers for message reactions", sender.answers)
	}
}

func TestHandleReactionMessageParentApprovesDeliveredCapabilityReviewEvent(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:       "cap-reaction-parent-approve",
		RequestedBy:     "telegram:2002",
		RequestedFor:    "telegram:2002",
		ParentPrincipal: "telegram:2002",
		AdminPrincipal:  "telegram:1001",
		Kind:            session.CapabilityKindGenericDelegation,
		TargetResource:  "child-task",
		Purpose:         "parent approval from reaction",
		ReviewStatus:    session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      2002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 2002,
		Summary:           "Capability request cap-reaction-parent-approve",
		MetadataJSON:      `{"request_id":"cap-reaction-parent-approve","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 177); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}
	before, ok, err := store.CapabilityRequest("cap-reaction-parent-approve")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest(before reaction) ok=%t err=%v", ok, err)
	}
	if before.ParentPrincipal != "telegram:2002" || before.ReviewStatus != session.CapabilityReviewStatusProposed {
		t.Fatalf("before reaction request = %#v, want parent principal and proposed state", before)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    2002,
		SenderID:  2002,
		MessageID: 177,
		Reaction:  &core.InboundReaction{MessageID: 177, New: []string{"👍"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want true")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-parent-approve")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusParentApproved {
		t.Fatalf("ReviewStatus = %q, want parent_approved", updated.ReviewStatus)
	}
	reviews, err := store.CapabilityReviews("cap-reaction-parent-approve", 10)
	if err != nil {
		t.Fatalf("CapabilityReviews() err = %v", err)
	}
	if len(reviews) != 1 || reviews[0].Status != session.CapabilityReviewStatusParentApproved {
		t.Fatalf("reviews = %#v, want one parent-approved review", reviews)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "parent-approved") {
		t.Fatalf("edits = %#v, want parent-approved card edit", sender.edits)
	}
}

func TestHandleReactionMessageAdminApprovesAfterParentApproval(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:       "cap-reaction-admin-after-parent",
		RequestedBy:     "telegram:2002",
		RequestedFor:    "telegram:2002",
		ParentPrincipal: "telegram:2002",
		AdminPrincipal:  "telegram:1001",
		Kind:            session.CapabilityKindGenericDelegation,
		TargetResource:  "child-task",
		Purpose:         "admin approval after parent reaction",
		ReviewStatus:    session.CapabilityReviewStatusParentApproved,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      2002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-admin-after-parent",
		MetadataJSON:      `{"request_id":"cap-reaction-admin-after-parent","review_status":"parent_approved"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 178); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 178,
		Reaction:  &core.InboundReaction{MessageID: 178, New: []string{"👍"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want true")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-admin-after-parent")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("ReviewStatus = %q, want approved", updated.ReviewStatus)
	}
}

func TestHandleReactionMessageRejectsDeliveredCapabilityReviewEvent(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-reaction-reject",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "reject from reaction",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-reject",
		MetadataJSON:      `{"request_id":"cap-reaction-reject","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 78); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 78,
		Reaction:  &core.InboundReaction{MessageID: 78, New: []string{"👎"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want true")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-reject")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusRejected {
		t.Fatalf("ReviewStatus = %q, want rejected", updated.ReviewStatus)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Capability request rejected.") {
		t.Fatalf("edits = %#v, want rejected card edit", sender.edits)
	}
}

func TestHandleReactionRemovalWithoutReviewEventReachesConversation(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	handler := newDecisionHandlerForTest(&decisionTestSender{}, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 188,
		Reaction:  &core.InboundReaction{MessageID: 188, New: nil},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if handled {
		t.Fatal("HandleReactionMessage() handled = true, want non-review reaction removal to fall through")
	}
}

func TestHandleReactionRemovalForDeliveredReviewEventIsConsumed(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-removal",
		MetadataJSON:      `{"request_id":"cap-reaction-removal","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 189); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}

	handler := newDecisionHandlerForTest(&decisionTestSender{}, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 189,
		Reaction:  &core.InboundReaction{MessageID: 189, New: nil},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want delivered review-card removal consumed")
	}
}

func TestHandleReactionMessageLetsOrdinaryMessageReachConversation(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-reaction-no-card",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "ordinary message reaction must not approve",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 88,
		Reaction:  &core.InboundReaction{MessageID: 88, New: []string{"👍"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if handled {
		t.Fatal("HandleReactionMessage() handled = true, want ordinary thumbs-up to fall through to conversation")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-no-card")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusProposed {
		t.Fatalf("ReviewStatus = %q, want proposed", updated.ReviewStatus)
	}
	if len(sender.edits) != 0 || len(sender.answers) != 0 {
		t.Fatalf("sender edits/answers = %#v/%#v, want no authority side effect", sender.edits, sender.answers)
	}
}

func TestHandleReactionMessageWrongUserDoesNotApprove(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-reaction-wrong-user",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "wrong user reaction must not approve",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-wrong-user",
		MetadataJSON:      `{"request_id":"cap-reaction-wrong-user","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 79); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  2002,
		MessageID: 79,
		Reaction:  &core.InboundReaction{MessageID: 79, New: []string{"👍"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want true")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-wrong-user")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusProposed {
		t.Fatalf("ReviewStatus = %q, want proposed", updated.ReviewStatus)
	}
	reviews, err := store.CapabilityReviews("cap-reaction-wrong-user", 10)
	if err != nil {
		t.Fatalf("CapabilityReviews() err = %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("reviews = %#v, want no review from wrong user", reviews)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Only the admin") {
		t.Fatalf("edits = %#v, want observable authorization rejection", sender.edits)
	}
}

func TestHandleReactionMessageDuplicateApprovalIsIdempotent(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-reaction-duplicate",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "duplicate reactions must be idempotent",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-duplicate",
		MetadataJSON:      `{"request_id":"cap-reaction-duplicate","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 80); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	msg := core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 80,
		Reaction:  &core.InboundReaction{MessageID: 80, New: []string{"👍"}},
	}
	if handled, err := handler.HandleReactionMessage(context.Background(), msg); err != nil || !handled {
		t.Fatalf("HandleReactionMessage(first) handled=%t err=%v", handled, err)
	}
	if handled, err := handler.HandleReactionMessage(context.Background(), msg); err != nil || !handled {
		t.Fatalf("HandleReactionMessage(second) handled=%t err=%v", handled, err)
	}
	reviews, err := store.CapabilityReviews("cap-reaction-duplicate", 10)
	if err != nil {
		t.Fatalf("CapabilityReviews() err = %v", err)
	}
	if len(reviews) != 1 {
		t.Fatalf("reviews = %#v, want one review after duplicate thumbs-up", reviews)
	}
}

func TestHandleReactionMessageDismissedCardDoesNotApprove(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-reaction-dismissed",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "dismissed card reaction must not approve",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-dismissed",
		MetadataJSON:      `{"request_id":"cap-reaction-dismissed","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 81); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}
	if _, err := store.DismissPendingCapabilityReviewEvents(1001, "cap-reaction-dismissed", 0); err != nil {
		t.Fatalf("DismissPendingCapabilityReviewEvents() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 81,
		Reaction:  &core.InboundReaction{MessageID: 81, New: []string{"👍"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want true")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-dismissed")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusProposed {
		t.Fatalf("ReviewStatus = %q, want proposed", updated.ReviewStatus)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Stale approval card") {
		t.Fatalf("edits = %#v, want stale-card edit", sender.edits)
	}
}

func TestHandleReactionMessageSurvivesStoreReopen(t *testing.T) {
	t.Parallel()

	dbPath := t.TempDir() + "/sessions.db"
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-reaction-restart",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "reaction after restart",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-reaction-restart",
		MetadataJSON:      `{"request_id":"cap-reaction-restart","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if err := store.MarkReviewDeliveredWithMessage(eventID, 82); err != nil {
		t.Fatalf("MarkReviewDeliveredWithMessage() err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	store, err = session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer store.Close()

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	handled, err := handler.HandleReactionMessage(context.Background(), core.InboundMessage{
		ChatID:    1001,
		SenderID:  1001,
		MessageID: 82,
		Reaction:  &core.InboundReaction{MessageID: 82, New: []string{"👍"}},
	})
	if err != nil {
		t.Fatalf("HandleReactionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("HandleReactionMessage() handled = false, want true")
	}
	updated, ok, err := store.CapabilityRequest("cap-reaction-restart")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("ReviewStatus = %q, want approved after restart-delivered reaction", updated.ReviewStatus)
	}
}

func TestHandleReviewEventCallbackActivatesCompiledCapabilityGrant(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "mail-child",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		Status:             "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	const requestID = "cap-mail-read"
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      requestID,
		RequestedBy:    "telegram:1002",
		RequestedFor:   "durable_agent:mail-child",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "account-primary",
		Purpose:        "read account metadata for one bounded task",
		Contract:       `{"surface":"account_read"}`,
		Constraints:    `{"max_items":25}`,
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	key := session.SessionKey{
		ChatID: 7001,
		UserID: 1002,
		Scope:  session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "7001"},
	}
	handoff := map[string]any{
		"action":          "grant_set",
		"request_id":      requestID,
		"kind":            "external_account",
		"target_resource": "account-primary",
		"principal":       "durable_agent:mail-child",
		"allowed_actions": []string{"read"},
		"contract":        json.RawMessage(`{"surface":"account_read"}`),
		"constraints":     json.RawMessage(`{"max_items":25}`),
		"grant_status":    "active",
	}
	rawHandoff, err := json.Marshal(handoff)
	if err != nil {
		t.Fatalf("Marshal(handoff) err = %v", err)
	}
	blocker, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "next-cap-mail-read",
		Key:                key,
		Owner:              "capability_request",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "capability_request",
		SubjectRef:         requestID,
		NextAction:         "Approve the requested bounded account read grant.",
		RequiredAuthority:  "capability_grant",
		ResourceBlocker:    "missing_capability_grant",
		OperationKind:      "capability_grant_review",
		OperationTool:      "capability_authority",
		OperationInputJSON: string(rawHandoff),
	})
	if err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Grant mail-child bounded account read access",
		MetadataJSON:      `{"request_id":"cap-mail-read","review_status":"proposed"}`,
		Status:            "delivered",
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-review-grant",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionApprove),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}

	updated, ok, err := store.CapabilityRequest(requestID)
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("ReviewStatus = %q, want approved", updated.ReviewStatus)
	}
	if strings.TrimSpace(updated.GrantID) == "" {
		t.Fatalf("GrantID is empty after callback approval: %#v", updated)
	}
	grant, ok, err := store.CapabilityGrant(updated.GrantID)
	if err != nil || !ok {
		t.Fatalf("CapabilityGrant(%q) ok=%t err=%v", updated.GrantID, ok, err)
	}
	if grant.RequestID != requestID || grant.GrantedTo != "durable_agent:mail-child" || grant.Kind != session.CapabilityKindExternalAccount || grant.TargetResource != "account-primary" || grant.Status != session.CapabilityGrantStatusActive {
		t.Fatalf("grant = %#v, want active linked external_account grant for mail-child/account-primary", grant)
	}
	if len(grant.AllowedActions) != 1 || grant.AllowedActions[0] != "read" {
		t.Fatalf("grant actions = %#v, want [read]", grant.AllowedActions)
	}
	open, err := store.OpenNextActionsBySessionSubject(key, "capability_request", requestID, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionSubject() err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions = %#v, want blocker %s resolved", open, blocker.RecordID)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEvents() err = %v", err)
	}
	foundGrantEvent := false
	for _, event := range events {
		if event.EventType == core.ExecutionEventCapabilityGrantChanged && strings.Contains(event.PayloadJSON, grant.GrantID) {
			foundGrantEvent = true
			break
		}
	}
	if !foundGrantEvent {
		t.Fatalf("execution events = %#v, want capability grant changed event for %s", events, grant.GrantID)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Grant activation: active") || !strings.Contains(sender.edits[0].text, grant.GrantID) {
		t.Fatalf("edits = %#v, want approved card to mention active grant", sender.edits)
	}
}

func TestHandleReviewEventCallbackDoesNotActivateUncompiledGrant(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-review-only",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "review-only approval",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-review-only",
		MetadataJSON:      `{"request_id":"cap-review-only","review_status":"proposed"}`,
		Status:            "delivered",
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-review-only",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionApprove),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	updated, ok, err := store.CapabilityRequest("cap-review-only")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("ReviewStatus = %q, want approved", updated.ReviewStatus)
	}
	if updated.GrantID != "" {
		t.Fatalf("GrantID = %q, want no grant for review-only approval", updated.GrantID)
	}
	if len(sender.edits) != 1 || strings.Contains(sender.edits[0].text, "Grant activation: active") {
		t.Fatalf("edits = %#v, want approval without grant activation", sender.edits)
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
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)

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
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)

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
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)

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

func TestHandleReviewEventCallbackRejectsDismissedCapabilityRequest(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-dismissed-card",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "stale callback should not approve",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-dismissed-card",
		MetadataJSON:      `{"request_id":"cap-dismissed-card","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	if _, err := store.DismissPendingCapabilityReviewEvents(1001, "cap-dismissed-card", 0); err != nil {
		t.Fatalf("DismissPendingCapabilityReviewEvents() err = %v", err)
	}

	sender := &decisionTestSender{}
	handler := newDecisionHandlerForTest(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-dismissed",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionApprove),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	updated, ok, err := store.CapabilityRequest("cap-dismissed-card")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusProposed {
		t.Fatalf("ReviewStatus = %q, want still proposed", updated.ReviewStatus)
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "stale") {
		t.Fatalf("answers = %#v, want stale callback answer", sender.answers)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Stale approval card") {
		t.Fatalf("editClear = %#v, want stale card edit", sender.edits)
	}
}
