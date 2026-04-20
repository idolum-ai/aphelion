//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundHandlesDurableTelegramDM(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "dm child ok"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "ops-child",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_dm",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help operators in a bounded direct-message lane.",
			OutboundMode: "reply_with_policy_authorization",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:         777,
		ChatType:       "private",
		SenderID:       555,
		SenderName:     "operator",
		Text:           "status?",
		MessageID:      21,
		DurableAgentID: "ops-child",
		Timestamp:      time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 777 {
		t.Fatalf("reply chat id = %d, want 777", sender.sent[0].ChatID)
	}

	key := session.SessionKey{
		ChatID: 777,
		Scope: session.ScopeRef{
			Kind:            session.ScopeKindDurableAgent,
			ID:              "ops-child",
			DurableAgentID:  "ops-child",
			ParentScopeKind: session.ScopeKindHeartbeat,
			ParentScopeID:   "admin-house",
		},
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.Scope.Kind != session.ScopeKindDurableAgent {
		t.Fatalf("session scope kind = %q, want durable_agent", sess.Scope.Kind)
	}
	if sess.ChatType != "private" {
		t.Fatalf("chat type = %q, want private", sess.ChatType)
	}
}

func TestHandleInboundDurableTelegramDMRejectsNonPrivateChat(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "dm child ok"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "ops-child",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_dm",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help operators in a bounded direct-message lane.",
			OutboundMode: "reply_with_policy_authorization",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:         -100200,
		ChatType:       "group",
		SenderID:       555,
		SenderName:     "operator",
		Text:           "status?",
		MessageID:      22,
		DurableAgentID: "ops-child",
		Timestamp:      time.Now(),
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want telegram_dm private-chat validation error")
	}
	if !strings.Contains(err.Error(), "private") {
		t.Fatalf("err = %v, want private-chat hint", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent messages = %d, want 0 on rejected chat type", len(sender.sent))
	}
}

func TestHandleInboundDurableTelegramDMReplyWithParentReviewQueuesArtifact(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I drafted a response but I am holding for review."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "ops-child",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_dm",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Hold direct responses for parent review.",
			OutboundMode: "reply_with_parent_review",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:         777,
		ChatType:       "private",
		SenderID:       555,
		SenderName:     "operator",
		Text:           "Can you send the update now?",
		MessageID:      23,
		DurableAgentID: "ops-child",
		Timestamp:      time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent messages = %d, want 0 when parent review is required", len(sender.sent))
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].Summary, "child-local subject") {
		t.Fatalf("summary = %q, want child-local subject framing", events[0].Summary)
	}
	for _, needle := range []string{
		`"channel_kind":"telegram_dm"`,
		`"sender_name":"operator"`,
		`"source_excerpt":"Can you send the update now?"`,
		`"trigger_kinds":"direct_question,withheld_local_reply"`,
		`"draft_response":"I drafted a response but I am holding for review."`,
	} {
		if !strings.Contains(events[0].MetadataJSON, needle) {
			t.Fatalf("metadata = %q, want substring %q", events[0].MetadataJSON, needle)
		}
	}
}

func TestHandleInboundDurableTelegramDMDraftOnlyQueuesArtifactWithoutQuestion(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Drafted a concise response."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "ops-child",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_dm",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Draft only in direct-message lane.",
			OutboundMode: "draft_only",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:         777,
		ChatType:       "private",
		SenderID:       555,
		SenderName:     "operator",
		Text:           "thanks for checking this",
		MessageID:      24,
		DurableAgentID: "ops-child",
		Timestamp:      time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent messages = %d, want 0 for draft_only", len(sender.sent))
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events = %d, want 1", len(events))
	}
	for _, needle := range []string{
		`"channel_kind":"telegram_dm"`,
		`"trigger_kinds":"withheld_local_reply"`,
		`"draft_response":"Drafted a concise response."`,
	} {
		if !strings.Contains(events[0].MetadataJSON, needle) {
			t.Fatalf("metadata = %q, want substring %q", events[0].MetadataJSON, needle)
		}
	}
}
