//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

type testDurableWakeAdapter struct {
	channelKind  string
	queueReview  bool
	prepareCalls int
	finalized    bool
	lastSummary  string
}

func markDurableWakeExternalAdapterReady(t *testing.T, store *session.SQLiteStore, agentID string, adapterName string) {
	t.Helper()
	now := time.Now().UTC()
	materialRoot := t.TempDir()
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: adapterName, ImplementationRef: "external:" + adapterName, Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{ToolName: adapterName, Status: session.ToolInstallStatusVerified, InstalledAt: now, AttestedAt: now}); err != nil {
		t.Fatalf("UpsertToolInstallRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{ToolName: adapterName, Status: session.ToolAuditStatusPassed, AuditedAt: now}); err != nil {
		t.Fatalf("UpsertToolAuditRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{ToolName: adapterName, Status: session.ToolProbeStatusPassed, ProbedAt: now}); err != nil {
		t.Fatalf("UpsertToolProbeRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-" + agentID + "-" + adapterName,
		Kind:           session.CapabilityKindTool,
		TargetResource: adapterName,
		GrantedTo:      core.DurableAgentPrincipal(agentID),
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + materialRoot + `"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(%s) err = %v", adapterName, err)
	}
}

func (a *testDurableWakeAdapter) Name() string {
	return "test_adapter"
}

func (a *testDurableWakeAdapter) Supports(agent core.DurableAgent) bool {
	return strings.TrimSpace(agent.ChannelKind) == strings.TrimSpace(a.channelKind)
}

func (a *testDurableWakeAdapter) Prepare(_ context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	a.prepareCalls++
	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	return &durableWakeTurnPlan{
		Channel:      strings.TrimSpace(a.channelKind),
		AuditChannel: strings.TrimSpace(a.channelKind),
		Key:          key,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       strings.TrimSpace(a.channelKind),
			ChatTitle:      "durable-wake-test",
			SenderName:     "adapter",
			Text:           "Summarize the adapter wake payload.",
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: agent.AgentID,
			Timestamp:      now,
		},
		SessionChatType:      strings.TrimSpace(a.channelKind),
		SessionUserName:      "adapter",
		PromptContextErrHint: "load durable wake prompt context",
		PolicyReason:         "mapped from interactive face policy for durable wake channels",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert durable wake messages",
			LoadPlanState:   "load durable wake plan state before save",
			LoadOperation:   "load durable wake operation state before save",
			SaveSession:     "save durable wake session",
			RecordOutbound:  "record durable wake outbound reply",
		},
		SendErrCtx:   "send durable wake reply",
		RecordErrCtx: "record durable wake outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, msg core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			_ = policy
			return fmt.Sprintf("You are handling a durable-agent wake through a pluggable adapter.\nAgent: %s\nPayload: %s\nPending: %d", agent.AgentID, msg.Text, len(pending))
		},
		Finalize: func(turnSummary string) error {
			a.finalized = true
			a.lastSummary = strings.TrimSpace(turnSummary)
			if !a.queueReview {
				return nil
			}
			_, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				AgentID:       strings.TrimSpace(agent.AgentID),
				Summary:       strings.TrimSpace(turnSummary),
				IntervalLabel: now.UTC().Format(time.RFC3339),
				LocalActions:  []string{"Processed durable wake payload through child-turn substrate."},
				Metadata: map[string]string{
					"channel_kind": strings.TrimSpace(agent.ChannelKind),
				},
			})
			return err
		},
	}, nil
}

func TestPollDurableWakeAgentsUsesPluggableIngressAdapter(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Pluggable adapter wake summary."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-test-adapter",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle test adapter wakes.",
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

	adapter := &testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	rt.durableWakeChild = nil

	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}
	if adapter.prepareCalls != 1 {
		t.Fatalf("adapter prepare calls = %d, want 1", adapter.prepareCalls)
	}
	if !adapter.finalized {
		t.Fatal("adapter finalize was not called")
	}
	if !strings.Contains(adapter.lastSummary, "Pluggable adapter wake summary.") {
		t.Fatalf("adapter last summary = %q, want provider summary", adapter.lastSummary)
	}

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate durable review relay", got)
	}
	if sender.inline[0].chatID != 1001 {
		t.Fatalf("inline chat_id = %d, want 1001", sender.inline[0].chatID)
	}
	if !strings.Contains(sender.inline[0].text, "**Review: idolum-test-adapter**") {
		t.Fatalf("inline text = %q, want review digest relay", sender.inline[0].text)
	}
	sender.mu.Unlock()

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents() len = %d, want 0 after immediate relay", len(events))
	}

	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(durable wake session) err = %v", err)
	}
	if sess.TurnCount == 0 {
		t.Fatalf("durable wake session turn_count = %d, want > 0", sess.TurnCount)
	}
	eventsBySession, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(durable wake session) err = %v", err)
	}
	if !containsExecutionEventType(eventsBySession, core.ExecutionEventDurableWakeStarted) {
		t.Fatalf("durable wake events missing started signal: %#v", eventsBySession)
	}
	if !containsExecutionEventType(eventsBySession, core.ExecutionEventDurableWakeCompleted) {
		t.Fatalf("durable wake events missing completed signal: %#v", eventsBySession)
	}
}

func TestPollDurableWakeAgentsUsesChildExecutorWhenBootstrapConfigured(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Child-executor wake summary."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-child-executor",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle test adapter wakes.",
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

	adapter := &testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true}
	childRuns := 0
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, scope sandbox.Scope, child core.DurableAgent, now time.Time) error {
		_ = scope
		_ = now
		if strings.TrimSpace(child.AgentID) != agent.AgentID {
			t.Fatalf("child executor agent_id = %q, want %q", child.AgentID, agent.AgentID)
		}
		childRuns++
		return nil
	}}

	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}
	if childRuns != 1 {
		t.Fatalf("child executor runs = %d, want 1", childRuns)
	}
	if adapter.prepareCalls != 0 {
		t.Fatalf("adapter prepare calls = %d, want 0 when child executor handles wake", adapter.prepareCalls)
	}
}

func TestPollDurableWakeAgentsDeliversReviewEventsAfterChildExecutorWake(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "unused in child executor path"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-child-relay",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Relay child review artifacts upward immediately.",
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

	rt.durableWakeAdapters = []durableWakeIngressAdapter{
		&testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true},
	}
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, child core.DurableAgent, now time.Time) error {
		_, queueErr := durableagent.NewRuntime(store).QueueReviewArtifact(child, core.DurableReviewArtifact{
			AgentID:       child.AgentID,
			Summary:       "child executor completed a bounded review",
			IntervalLabel: now.UTC().Format(time.RFC3339),
			LocalActions:  []string{"Processed one parent message."},
		})
		return queueErr
	}}

	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate relay after child wake", got)
	}
	if sender.inline[0].chatID != 1001 {
		t.Fatalf("inline chat_id = %d, want 1001", sender.inline[0].chatID)
	}
	if !strings.Contains(sender.inline[0].text, "**Review: idolum-child-relay**") {
		t.Fatalf("inline text = %q, want review digest relay", sender.inline[0].text)
	}
	sender.mu.Unlock()

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents() len = %d, want 0 after immediate relay", len(events))
	}
}

func TestDurableTurnInferenceUnavailableUsesProviderFailure(t *testing.T) {
	result := &turn.Result{Turn: &core.TurnResult{ProviderFailure: "codex: server_is_overloaded"}}
	if !durableTurnInferenceUnavailable(result, "ordinary visible text") {
		t.Fatal("durableTurnInferenceUnavailable() = false, want provider failure to count structurally")
	}
	if durableTurnInferenceUnavailable(&turn.Result{Turn: &core.TurnResult{}}, "ordinary visible text") {
		t.Fatal("durableTurnInferenceUnavailable() = true, want false without provider failure or legacy visible signal")
	}
}

func TestPollDurableWakeAgentsKeepsParentConversationPendingOnInferenceFailure(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Inference backend is unavailable. This turn did not complete. You can /stop to cancel current work and try again."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-retry",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Apply parent guidance when inference is available.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
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
	continuity = continuity.WithConversationMessage("parent", "Please process the latest parent note.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   agent.AgentID,
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeChild = nil
	err = rt.pollDurableWakeAgents(context.Background(), time.Now().UTC())
	if err == nil {
		t.Fatal("pollDurableWakeAgents() err = nil, want durable wake inference unavailable")
	}
	if !strings.Contains(err.Error(), "durable wake inference unavailable") {
		t.Fatalf("pollDurableWakeAgents() err = %v, want durable wake inference unavailable", err)
	}

	updatedState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	updatedContinuity, err := core.ParseDurableAgentContinuityState(updatedState.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if pending := updatedContinuity.PendingParentConversationMessages(10); len(pending) != 1 {
		t.Fatalf("pending parent messages = %d, want 1 after transient inference failure", len(pending))
	}
	if updatedState.LastApplyStatus != "failed" {
		t.Fatalf("last_apply_status = %q, want failed", updatedState.LastApplyStatus)
	}
	if strings.TrimSpace(updatedState.LastApplyError) == "" {
		t.Fatalf("last_apply_error = %q, want non-empty failure reason", updatedState.LastApplyError)
	}

	sender.mu.Lock()
	if got := len(sender.sent); got != 0 {
		t.Fatalf("sent len = %d, want 0 review digests when wake failed before ack", got)
	}
	sender.mu.Unlock()
}

func TestPollDurableWakeAgentsDispatchesGenericExternalChannelWithoutSpecializedParentSemantics(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "The configured adapter runtime material is unavailable; I need a child_runtime grant."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address:      "external-endpoint",
			Adapter:      "child_adapter",
			Query:        "topic:important",
			PollInterval: "5m",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle the external channel and summarize important findings upward.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
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
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")

	rt.durableWakeChild = nil
	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}
	if len(provider.lastGovernorMsgs) == 0 {
		t.Fatal("governor messages empty, want generic external-channel wake")
	}
	joined := strings.ToLower(fmt.Sprint(provider.lastGovernorMsgs))
	if !strings.Contains(joined, "generic external_channel adapter dispatcher") {
		t.Fatalf("governor messages = %#v, want generic dispatcher context", provider.lastGovernorMsgs)
	}
	for _, forbidden := range []string{"gmail", "gog", "recruiter", "job"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("governor messages = %#v, should not contain specialized term %q", provider.lastGovernorMsgs, forbidden)
		}
	}
}

func TestPollDurableWakeAgentsConsumesPendingParentConversationForAnyChannel(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Processed the parent guidance and compiled the requested summary."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent requests over channel artifacts and summarize upward.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
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
	continuity = continuity.WithConversationMessage("parent", "Summarize the most relevant job links.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   agent.AgentID,
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeChild = nil
	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	foundParentContext := false
	for _, systemPrompt := range provider.seenGovernorSystem {
		if strings.Contains(systemPrompt, "Parent note 1: Summarize the most relevant job links.") {
			foundParentContext = true
			break
		}
	}
	if !foundParentContext {
		t.Fatalf("governor prompts = %#v, want pending parent note context", provider.seenGovernorSystem)
	}

	updatedState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	updatedContinuity, err := core.ParseDurableAgentContinuityState(updatedState.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if pending := updatedContinuity.PendingParentConversationMessages(10); len(pending) != 0 {
		t.Fatalf("pending parent messages = %d, want 0 after wake", len(pending))
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents() len = %d, want 0 after immediate relay", len(events))
	}

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate parent-conversation review relay", got)
	}
	if !strings.Contains(sender.inline[0].text, "**Review: child-alpha**") || !strings.Contains(sender.inline[0].text, "Processed pending parent guidance") {
		t.Fatalf("inline text = %q, want parent conversation ack summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "headless") || strings.Contains(sender.inline[0].text, "channel=headless") {
		t.Fatalf("inline text = %q, want human channel context without raw metadata", sender.inline[0].text)
	}
	sender.mu.Unlock()
}

func TestRunDurableAgentChildWakeSkipsWhenAgentAlreadyAwake(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "This should not run while another wake owns the agent."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-awake",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent requests over channel artifacts and summarize upward.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
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
	continuity = continuity.WithConversationMessage("parent", "Handle exactly once.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:    agent.AgentID,
		Status:     "awake",
		StateJSON:  raw,
		LastWakeAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeChild = nil
	if err := rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC()); err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v", err)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("governor prompts = %#v, want no child turn while agent is already awake", provider.seenGovernorSystem)
	}
	updatedState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	updatedContinuity, err := core.ParseDurableAgentContinuityState(updatedState.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if pending := updatedContinuity.PendingParentConversationMessages(10); len(pending) != 1 {
		t.Fatalf("pending parent messages = %d, want still pending after skipped wake", len(pending))
	}
	events, err := store.ExecutionEventsBySession(rt.durableAgentExecutionKey(agent.AgentID), 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !containsExecutionEventType(events, core.ExecutionEventDurableWakeSkipped) {
		t.Fatalf("execution events = %#v, want durable wake skipped event", events)
	}
}

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
