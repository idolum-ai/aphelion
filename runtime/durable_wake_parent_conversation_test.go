//go:build linux

package runtime

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"strings"
	"testing"
	"time"
)

func TestParentConversationAckSuppressedWhenChildQueuesConcreteReview(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Concrete child report from the wake."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-reporting",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent requests and report concrete findings.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Inspect runtime grants.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeAdapters = []durableWakeIngressAdapter{&testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true}}
	rt.durableWakeChild = nil
	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want only concrete child review", got)
	}
	if !strings.Contains(sender.inline[0].text, "Concrete child report from the wake.") {
		t.Fatalf("inline text = %q, want concrete child report", sender.inline[0].text)
	}
	if strings.Contains(sender.inline[0].text, "Processed pending parent guidance") {
		t.Fatalf("inline text = %q, want parent ack wrapper suppressed", sender.inline[0].text)
	}
}

func TestRunDurableAgentChildWakeProcessesPendingParentBeforeExternalCadence(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Processed pending parent image job."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            "image2",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter:      "codex_image_generation",
			PollInterval: "168h",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Generate one image artifact when parent asks.",
			CapabilityEnvelope: []string{"image_brief_refinement", "codex_image_generation_probe", "artifact_return", "blocker_report"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Generate exactly one image artifact.", time.Now().UTC().Add(-time.Minute))
	continuity.ExternalChannel = encodeGenericExternalChannelState(core.DurableAgentExternalChannelRuntimeState{
		Adapter:       "codex_image_generation",
		LastAttemptAt: time.Now().UTC(),
		LastStatus:    "wake_completed",
	}, "codex_image_generation")
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agent.AgentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	if err := rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC()); err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v", err)
	}
	pending, err := rt.pendingDurableAgentParentConversation(agent.AgentID, 5)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending parent messages = %d, want acked by forced parent wake", len(pending))
	}
	if len(provider.seenGovernorSystem) == 0 || !strings.Contains(strings.Join(provider.seenGovernorSystem, "\n"), "parent conversation wake") {
		t.Fatalf("governor prompts = %#v, want parent conversation wake", provider.seenGovernorSystem)
	}
}

func TestRunDurableAgentChildWakeSkipsWithoutPendingParentConversation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Unsupported channel should not run"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-unsupported-channel",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "unsupported_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Attempt unsupported wake channel.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	err = rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v, want nil for empty parent queue", err)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("governor prompts = %#v, want no child turn without pending parent conversation", provider.seenGovernorSystem)
	}
}

func TestPollDurableWakeAgentsBacksOffExpiredGrantChildRuntimeBlock(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "unused because child runtime blocks before inference"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "image2",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address:      "local://image2",
			Adapter:      "codex_image_generation",
			PollInterval: "168h",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Generate images only when a concrete parent request and active grant exist.",
			CapabilityEnvelope: []string{"image_brief_refinement", "codex_image_generation_probe", "artifact_return", "blocker_report"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "codex_image_generation")
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	childRuns := 0
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
		childRuns++
		return fmt.Errorf("child_runtime_blocked: grant_expired grant_id=capg-image2")
	}}

	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents(first) err = %v, want suppressed blocked wake", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns = %d, want first blocked child attempt", childRuns)
	}
	cont := loadExternalChannelContinuity(t, store, "image2")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want blocked wake state")
	}
	if cont.ExternalChannel.LastStatus != "wake_blocked" || !strings.Contains(cont.ExternalChannel.LastError, "grant_expired") {
		t.Fatalf("external channel state = %#v, want grant_expired wake_blocked", cont.ExternalChannel)
	}
	if cont.ExternalChannel.BackoffUntil.Before(now.Add(29 * time.Minute)) {
		t.Fatalf("backoff_until = %v, want recorded backoff", cont.ExternalChannel.BackoffUntil)
	}
	sender.mu.Lock()
	compact := ""
	if len(sender.inline) > 0 {
		compact = sender.inline[len(sender.inline)-1].text
	}
	sender.mu.Unlock()
	if !strings.Contains(compact, "PAUSED") || strings.Contains(compact, "capg-image2") || strings.Contains(compact, "child_runtime_blocked") || strings.Contains(compact, "risk: adapter_dispatch") {
		t.Fatalf("compact review = %q, want paused operator summary without raw runtime details", compact)
	}

	if err := rt.pollDurableWakeAgents(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("pollDurableWakeAgents(backoff) err = %v, want quiet skip", err)
	}
	if childRuns != 1 {
		t.Fatalf("childRuns after suppressed retry = %d, want 1", childRuns)
	}
}

func TestPollDurableWakeAgentsPreflightsExternalChannelMaterialBeforeChildWake(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "unused because preflight blocks before child wake"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "mail-child",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address:      "local://mailbox",
			Adapter:      "mailbox_adapter",
			Query:        "label:inbox",
			PollInterval: "30m",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Poll the external channel only when grants and material are ready.",
			CapabilityEnvelope: []string{"external_channel_poll", "blocker_report"},
			OutboundMode:       "draft_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{newGenericExternalChannelWakeAdapter()}
	childRuns := 0
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
		childRuns++
		return nil
	}}

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v, want preflight block recorded without hard failure", err)
	}
	if childRuns != 0 {
		t.Fatalf("childRuns = %d, want preflight to block before child wake", childRuns)
	}
	cont := loadExternalChannelContinuity(t, store, "mail-child")
	if cont.ExternalChannel == nil {
		t.Fatal("ExternalChannel = nil, want preflight wake_blocked state")
	}
	if cont.ExternalChannel.LastStatus != "wake_blocked" || !strings.Contains(cont.ExternalChannel.LastError, "child_runtime_blocked") || !strings.Contains(cont.ExternalChannel.LastError, "mailbox_adapter") {
		t.Fatalf("external channel state = %#v, want generic adapter preflight blocker", cont.ExternalChannel)
	}
	sender.mu.Lock()
	compact := ""
	if len(sender.inline) > 0 {
		compact = sender.inline[len(sender.inline)-1].text
	}
	sender.mu.Unlock()
	if !strings.Contains(compact, "BLOCKED") || strings.Contains(compact, "label:inbox") {
		t.Fatalf("compact review = %q, want blocked operator summary without query leak", compact)
	}
}

func TestParentConversationWakeAdapterSkipsScheduledReviewChannel(t *testing.T) {
	t.Parallel()

	adapter := newDurableParentConversationWakeAdapter()
	if adapter.Supports(core.DurableAgent{AgentID: "scheduled", ChannelKind: scheduledReviewChannelKind}) {
		t.Fatal("parent conversation adapter supports scheduled_review; want skipped so the scheduled adapter owns its wake")
	}
	if !adapter.Supports(core.DurableAgent{AgentID: "external", ChannelKind: "external_channel"}) {
		t.Fatal("parent conversation adapter does not support ordinary external_channel child")
	}
}
