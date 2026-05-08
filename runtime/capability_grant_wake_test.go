//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestQueueCapabilityGrantWakeAddsParentConversation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = provider
	_ = sender
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "manual_channel",
		WakeupMode:         "manual",
		Status:             "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle grant wake tests.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	grant := session.CapabilityGrant{
		GrantID:        "capg-child-alpha",
		RequestID:      "cap-child-alpha",
		GrantedTo:      "durable_agent:child-alpha",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}

	if err := rt.queueCapabilityGrantWake(context.Background(), "child-alpha", grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	pending, err := rt.pendingDurableAgentParentConversation("child-alpha", 10)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].Text, "Capability grant activated") || !strings.Contains(pending[0].Text, "capg-child-alpha") {
		t.Fatalf("pending parent conversation = %#v, want capability grant wake message", pending)
	}
	events, err := store.ExecutionEventsBySession(rt.durableAgentExecutionKey("child-alpha"), 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventCapabilityGrantWakeQueued)
}

func TestCapabilityGrantWakeFailureMarksGrantFailedAndReports(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-wake-fail",
		RequestID:      "cap-wake-fail",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "durable_agent:child-alpha",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		GrantedAt:      now,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	rt.recordCapabilityGrantWakeFailure(context.Background(), session.SessionKey{}, "child-alpha", grant, errors.New("wake substrate unavailable"))

	failed, ok, err := store.CapabilityGrant("capg-wake-fail")
	if err != nil {
		t.Fatalf("CapabilityGrant() err = %v", err)
	}
	if !ok || failed.Status != session.CapabilityGrantStatusFailed || !strings.Contains(failed.StaleReason, "wake substrate unavailable") {
		t.Fatalf("failed grant = %#v ok=%t, want failed with stale reason", failed, ok)
	}
	deadline := time.After(time.Second)
	for {
		sender.mu.Lock()
		sent := append([]core.OutboundMessage(nil), sender.sent...)
		sender.mu.Unlock()
		if len(sent) > 0 && strings.Contains(sent[len(sent)-1].Text, "request a fresh grant") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("sent operational notices = %#v, want fresh-grant warning", sent)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
