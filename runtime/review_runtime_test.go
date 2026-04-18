//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundDeliversPendingReviewEventsForAdmin(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      44,
		SourceRole:        "approved_user",
		TargetAdminChatID: 42,
		TurnFrom:          1,
		TurnTo:            3,
		Summary:           "user requested package install in isolated workspace",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     42,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "status",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want 2", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "ok" {
		t.Fatalf("first message = %q, want model reply", finalText)
	}
	if !strings.Contains(sender.sent[1].Text, "Review digest.") {
		t.Fatalf("second message missing digest label: %q", sender.sent[1].Text)
	}
	if !strings.Contains(sender.sent[1].Text, "source_chat=7001") {
		t.Fatalf("second message missing source chat: %q", sender.sent[1].Text)
	}

	pending, err := store.PendingReviewEvents(42, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending len = %d, want 0 after delivery", len(pending))
	}

	adminSession, err := store.Load(session.SessionKey{ChatID: 42, UserID: 0})
	if err != nil {
		t.Fatalf("Load(admin session) err = %v", err)
	}
	if len(adminSession.Messages) != 3 {
		t.Fatalf("admin session messages len = %d, want 3", len(adminSession.Messages))
	}
	if !strings.Contains(adminSession.Messages[2].Content, "Review digest.") {
		t.Fatalf("admin digest content = %q, want persisted review digest", adminSession.Messages[2].Content)
	}
}

func TestHandleInboundDoesNotDeliverReviewEventsForApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.EnqueueReviewEvent(session.ReviewEvent{
		SourceChatID:      8001,
		SourceUserID:      77,
		SourceRole:        "approved_user",
		TargetAdminChatID: 42,
		TurnFrom:          3,
		TurnTo:            4,
		Summary:           "requires admin review",
	}); err != nil {
		t.Fatalf("EnqueueReviewEvent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     99,
		SenderID:   1002,
		SenderName: "member",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1 (only model reply)", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "ok" {
		t.Fatalf("message = %q, want ok", finalText)
	}

	pending, err := store.PendingReviewEvents(42, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1 (not delivered in non-admin turn)", len(pending))
	}
}

func TestHandleInboundGeneratesReviewEventForApprovedUser(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     222,
		SenderID:   1002,
		SenderName: "member",
		Text:       "please summarize what happened",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending len = %d, want 1", len(pending))
	}
	event := pending[0]
	if event.SourceChatID != 222 {
		t.Fatalf("source chat = %d, want 222", event.SourceChatID)
	}
	if event.SourceUserID != 1002 {
		t.Fatalf("source user = %d, want 1002", event.SourceUserID)
	}
	if event.SourceRole != "approved_user" {
		t.Fatalf("source role = %q, want approved_user", event.SourceRole)
	}
	if event.SourceScope.Kind != session.ScopeKindTelegramDM || event.SourceScope.ID != "222" {
		t.Fatalf("source scope = %#v, want telegram_dm 222", event.SourceScope)
	}
	if event.TargetScope.Kind != session.ScopeKindTelegramDM || event.TargetScope.ID != "1001" {
		t.Fatalf("target scope = %#v, want telegram_dm 1001", event.TargetScope)
	}
	if event.TurnFrom != 1 || event.TurnTo != 1 {
		t.Fatalf("turn range = %d-%d, want 1-1", event.TurnFrom, event.TurnTo)
	}
	if !strings.Contains(event.Summary, "provenance chat=222 user=1002 role=approved_user turn=1") {
		t.Fatalf("summary missing provenance: %q", event.Summary)
	}
	if !strings.Contains(event.Summary, "scope=telegram_dm:222") {
		t.Fatalf("summary missing source scope: %q", event.Summary)
	}
	if !strings.Contains(event.Summary, "reply: idolum rendered") {
		t.Fatalf("summary missing rendered reply text: %q", event.Summary)
	}
	if strings.Contains(event.Summary, "reply: governor canonical") {
		t.Fatalf("summary used governor floor text instead of rendered scene: %q", event.Summary)
	}
	if len([]rune(event.Summary)) > session.DefaultReviewSummaryMaxChars {
		t.Fatalf("summary len = %d, want <= %d", len([]rune(event.Summary)), session.DefaultReviewSummaryMaxChars)
	}
}

func TestShouldGenerateReviewEvent(t *testing.T) {
	t.Parallel()

	if !shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleApprovedUser}, session.SessionKey{ChatID: 1, UserID: 0}) {
		t.Fatal("approved_user should generate review event")
	}
	if shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleAdmin}, session.SessionKey{ChatID: 1, UserID: 0}) {
		t.Fatal("admin top-level session should not generate review event")
	}
	if !shouldGenerateReviewEvent(principal.Principal{Role: principal.RoleAdmin}, session.SessionKey{ChatID: 1, UserID: 7}) {
		t.Fatal("admin subordinate session should generate review event")
	}
}
