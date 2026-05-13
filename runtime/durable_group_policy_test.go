//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
)

func TestHandleInboundDurableTelegramGroupPolicyAuthorizationSurfacesFamilyUpdate(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Thanks. I’ll keep that in mind."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "family-group",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_group",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help locally in the family group while surfacing important continuity updates upward.",
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
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "Heads up: grandma's appointment was rescheduled to tomorrow morning.",
		MessageID:      9,
		DurableAgentID: "family-group",
		Timestamp:      time.Now(),
		Raw:            json.RawMessage(`{"source":"telegram-group"}`),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1 local reply", len(sender.sent))
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending review events = %d, want 1", len(events))
	}
	if !strings.Contains(events[0].Summary, "Family-relevant update") {
		t.Fatalf("summary = %q, want family-relevant update summary", events[0].Summary)
	}
	if !strings.Contains(events[0].MetadataJSON, "family_relevant_update") {
		t.Fatalf("metadata = %q, want family update trigger", events[0].MetadataJSON)
	}
	if !strings.Contains(events[0].MetadataJSON, "local_response") {
		t.Fatalf("metadata = %q, want local response excerpt", events[0].MetadataJSON)
	}
}

func TestHandleInboundDurableTelegramGroupReplyWithParentReviewQueuesDraftWithoutReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I can draft a response, but I should wait for parent review."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "family-group",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_group",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Hold direct group questions for parent review before replying.",
			OutboundMode: "reply_with_parent_review",
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
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "Can you remind everyone about dinner?",
		MessageID:      10,
		DurableAgentID: "family-group",
		Timestamp:      time.Now(),
		Raw:            json.RawMessage(`{"source":"telegram-group"}`),
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
	if !strings.Contains(events[0].Summary, "awaiting parent review") {
		t.Fatalf("summary = %q, want held-question summary", events[0].Summary)
	}
	if !strings.Contains(events[0].MetadataJSON, "draft_response") {
		t.Fatalf("metadata = %q, want draft response excerpt", events[0].MetadataJSON)
	}
	if !strings.Contains(events[0].MetadataJSON, "\"policy_outbound\":\"reply_with_parent_review\"") {
		t.Fatalf("metadata = %q, want policy_outbound", events[0].MetadataJSON)
	}
}

func TestHandleInboundDurableTelegramGroupDeliveredReviewStaysOutOfPendingQueue(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I can draft a response, but I should wait for parent review."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "family-group",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_group",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Hold direct group questions for parent review before replying.",
			OutboundMode: "reply_with_parent_review",
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
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "Can you remind everyone about dinner?",
		MessageID:      30,
		DurableAgentID: "family-group",
		Timestamp:      time.Now(),
		Raw:            json.RawMessage(`{"source":"telegram-group"}`),
	})
	if err != nil {
		t.Fatalf("HandleInbound(first) err = %v", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent messages = %d, want 0 when parent review is required", len(sender.sent))
	}

	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(first) err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending review events = %d, want 1 after first queued draft", len(pending))
	}
	if err := store.MarkReviewDelivered([]int64{pending[0].ID}); err != nil {
		t.Fatalf("MarkReviewDelivered() err = %v", err)
	}
	pending, err = store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(after deliver) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending review events = %d, want 0 after delivery", len(pending))
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:         -100200,
		ChatType:       "group",
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "ok",
		MessageID:      31,
		DurableAgentID: "family-group",
		Timestamp:      time.Now(),
		Raw:            json.RawMessage(`{"source":"telegram-group"}`),
	})
	if err != nil {
		t.Fatalf("HandleInbound(second) err = %v", err)
	}
	pending, err = store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(second) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending review events = %d, want 0 after non-escalating follow-up", len(pending))
	}
}

func TestHandleInboundDurableTelegramGroupRecordsAppliedPolicyState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I can help with that."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "family-group",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_group",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help locally in the family group while surfacing important continuity updates upward.",
			OutboundMode: "read_only",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	updated, _, err := store.ApplyDurableAgentLivePolicy("family-group", core.DurableAgentLivePolicy{
		Charter:      "Help locally in the family group while surfacing important continuity updates upward.",
		OutboundMode: "reply_with_policy_authorization",
		DriftPolicy:  "admin_review",
	}, 0, "allow local family-group replies")
	if err != nil {
		t.Fatalf("ApplyDurableAgentLivePolicy() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:         -100200,
		ChatType:       "group",
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "Can you remind everyone about dinner?",
		MessageID:      11,
		DurableAgentID: "family-group",
		Timestamp:      time.Now(),
		Raw:            json.RawMessage(`{"source":"telegram-group"}`),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	state, err := store.DurableAgentState("family-group")
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastOfferedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastOfferedPolicyVersion = %d, want %d", state.LastOfferedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastAcknowledgedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastAcknowledgedPolicyVersion = %d, want %d", state.LastAcknowledgedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastAppliedPolicyVersion != updated.PolicyVersion {
		t.Fatalf("LastAppliedPolicyVersion = %d, want %d", state.LastAppliedPolicyVersion, updated.PolicyVersion)
	}
	if state.LastApplyStatus != "applied" {
		t.Fatalf("LastApplyStatus = %q, want applied", state.LastApplyStatus)
	}
	if state.LastApplyError != "" {
		t.Fatalf("LastApplyError = %q, want empty", state.LastApplyError)
	}
}

func TestHandleInboundDurableTelegramGroupRecordsPolicyApplyFailure(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I can help with that."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{
		run: func(context.Context, core.InboundMessage) (*DurableGroupChildResult, error) {
			return nil, fmt.Errorf("child policy bootstrap failed")
		},
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "family-group",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_group",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Help locally in the family group while surfacing important continuity updates upward.",
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
		ChatTitle:      "Family",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "Can you remind everyone about dinner?",
		MessageID:      12,
		DurableAgentID: "family-group",
		Timestamp:      time.Now(),
		Raw:            json.RawMessage(`{"source":"telegram-group"}`),
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want durable child failure")
	}

	state, err := store.DurableAgentState("family-group")
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastOfferedPolicyVersion != 1 {
		t.Fatalf("LastOfferedPolicyVersion = %d, want 1", state.LastOfferedPolicyVersion)
	}
	if state.LastAppliedPolicyVersion != 0 {
		t.Fatalf("LastAppliedPolicyVersion = %d, want 0 after failed child run", state.LastAppliedPolicyVersion)
	}
	if state.LastApplyStatus != "failed" {
		t.Fatalf("LastApplyStatus = %q, want failed", state.LastApplyStatus)
	}
	if !strings.Contains(state.LastApplyError, "child policy bootstrap failed") {
		t.Fatalf("LastApplyError = %q, want child failure message", state.LastApplyError)
	}
}

func containsExecutionEventType(events []session.ExecutionEvent, eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == eventType {
			return true
		}
	}
	return false
}
