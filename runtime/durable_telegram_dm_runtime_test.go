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
