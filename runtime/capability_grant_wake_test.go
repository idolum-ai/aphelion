//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func capabilityGrantWakeOperationInputForTest(t *testing.T, raw string) map[string]any {
	t.Helper()
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("operation input JSON = %q err=%v", raw, err)
	}
	return input
}

func TestQueueCapabilityGrantWakeAddsParentConversation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Grant incorporated.\nREVIEW_STATUS: completed"
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
	wantTaskPacketID := capabilityGrantTaskPacketID("child-alpha", grant)
	if pending[0].MessageID != wantTaskPacketID {
		t.Fatalf("pending message id = %q, want stable task packet id %q", pending[0].MessageID, wantTaskPacketID)
	}
	packet, ok, err := store.ChildTaskPacket(wantTaskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(queue) err = %v", err)
	}
	if !ok || packet.Status != session.ChildTaskPacketQueued || packet.TaskLeaseID == "" || packet.GrantID != "capg-child-alpha" || packet.RequiredAction != "invoke" {
		t.Fatalf("queued child task packet = %#v ok=%t, want queued grant-bound task", packet, ok)
	}
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey("child-alpha"), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(queue) err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionWaitingForChild || open[0].SubjectKind != "task_packet" || open[0].SubjectRef != wantTaskPacketID {
		t.Fatalf("open next actions after queue = %#v, want one waiting_for_child task packet", open)
	}
	events, err := store.ExecutionEventsBySession(rt.durableAgentExecutionKey("child-alpha"), 0, 60)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventCapabilityGrantWakeQueued)

	if err := rt.runCapabilityGrantWake(context.Background(), "child-alpha", grant); err != nil {
		t.Fatalf("runCapabilityGrantWake() err = %v", err)
	}
	open, err = store.OpenNextActionsBySession(rt.durableAgentExecutionKey("child-alpha"), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(completed) err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions after wake completion = %#v, want closed waiting_for_child", open)
	}
	packet, ok, err = store.ChildTaskPacket(wantTaskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(completed) err = %v", err)
	}
	if !ok || packet.Status != session.ChildTaskPacketCompleted || packet.ResultID == "" || packet.TerminalAt.IsZero() {
		t.Fatalf("completed child task packet = %#v ok=%t, want completed terminal packet", packet, ok)
	}
	result, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult() err = %v", err)
	}
	if !ok || result.AttemptID == "" || result.PacketID != wantTaskPacketID || result.Status != session.ChildTaskResultCompleted || result.NextState != session.NextActionTerminal {
		t.Fatalf("child task result = %#v ok=%t, want completed terminal result", result, ok)
	}
	events, err = store.ExecutionEventsBySession(rt.durableAgentExecutionKey("child-alpha"), 0, 80)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(completed) err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventDurableWakeCompleted)
	assertHasEventType(t, events, core.ExecutionEventDurableChildTaskQueued)
	assertHasEventType(t, events, core.ExecutionEventDurableChildTaskResult)
}

func TestCapabilityGrantWakeTimeoutProducesTransientRetryState(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.Join([]string{
		"I incorporated the new `host@idolum.ai` job-triage approval and validated the active authorities.",
		"",
		"Active now:",
		"- `host@idolum.ai` read access",
		"- `host@idolum.ai` archive / mark-processed approval exists, but this wake was dry-run/no-mutation, so I did not use it",
		"- `gog_cli` invoke",
		"- `gog_cli:host@idolum.ai` invoke",
		"",
		"I made exactly one read-only dry-run report attempt using `search_unread_jobs` for `host@idolum.ai`.",
		"",
		"Result: blocked by tool timeout.",
		"",
		"Non-secret status:",
		"- failure class: `timeout`",
		"REVIEW_STATUS: blocked",
	}, "\n")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "mail-timeout",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter: "gog_cli",
		}},
		WakeupMode: "manual",
		Status:     "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Read mailbox opportunities and report bounded findings.",
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
		GrantID:        "capg-mail-timeout",
		RequestID:      "cap-mail-timeout",
		GrantedTo:      "durable_agent:mail-timeout",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "host@idolum.ai",
		AllowedActions: []string{"read", "archive", "mark_processed"},
		Status:         session.CapabilityGrantStatusActive,
	}

	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake() err = %v", err)
	}

	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)
	packet, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(%q) err = %v", taskPacketID, err)
	}
	if !ok || packet.Status != session.ChildTaskPacketBlocked || packet.ResultID == "" {
		t.Fatalf("ChildTaskPacket(%q) = %#v ok=%t, want blocked packet with result", taskPacketID, packet, ok)
	}
	result, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult(%q) err = %v", packet.ResultID, err)
	}
	if !ok || result.Status != session.ChildTaskResultBlocked || result.BlockerKind != "external_transient" || result.NextState != session.NextActionScheduledRetry {
		t.Fatalf("ChildTaskResult(%q) = %#v ok=%t, want external_transient scheduled retry", packet.ResultID, result, ok)
	}
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionScheduledRetry || open[0].ResourceBlocker != "external_transient" || open[0].RetryPolicy != "bounded_backoff" {
		t.Fatalf("open next actions = %#v, want transient scheduled retry rather than resource repair", open)
	}
	if strings.Contains(open[0].OperatorProjection, "resource boundary denied") {
		t.Fatalf("operator projection = %q, want timeout/transient framing", open[0].OperatorProjection)
	}
}

func TestFreshCapabilityGrantWakeSupersedesStaleChildResourceRepair(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.Join([]string{
		"I made exactly one read-only dry-run report attempt using `search_unread_jobs` for `host@idolum.ai`.",
		"Result: blocked by tool timeout.",
		"Non-secret status:",
		"- failure class: `timeout`",
		"REVIEW_STATUS: blocked",
	}, "\n")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "mail-stale-repair",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter: "gog_cli",
		}},
		WakeupMode: "manual",
		Status:     "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Read mailbox opportunities and report bounded findings.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	key := rt.durableAgentExecutionKey(agent.AgentID)
	staleAt := time.Now().UTC().Add(-time.Hour)
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "durable_wake",
		State:              session.NextActionBlockedNeedsResourceRepair,
		SubjectKind:        "task_packet",
		SubjectRef:         "child_task:old-resource-repair",
		CausalRefs:         []string{"task_packet:child_task:old-resource-repair", "child_task_result:child_result:old-resource-repair"},
		NextAction:         "repair the child-local resource permission boundary before retrying",
		ResourceBlocker:    "resource_permission_denied",
		RetryPolicy:        "retry_after_resource_repair",
		OperationKind:      session.NextActionOperationKindDurableChildRecovery,
		OperationTool:      "update_operation",
		OperationInputJSON: `{"recovery_contract":"aphelion.recovery_handoff.v1","recovery_operation_kind":"durable_child_recovery","durable_agent_id":"mail-stale-repair","tool":"gog_cli","blocker_kind":"resource_permission_denied"}`,
		OperatorProjection: "Stale pre-fix resource repair blocker.",
		CreatedAt:          staleAt,
	}); err != nil {
		t.Fatalf("RecordNextAction(stale resource repair) err = %v", err)
	}

	grant := session.CapabilityGrant{
		GrantID:        "capg-mail-stale-repair",
		RequestID:      "cap-mail-stale-repair",
		GrantedTo:      "durable_agent:mail-stale-repair",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "host@idolum.ai",
		AllowedActions: []string{"read", "archive", "mark_processed"},
		Status:         session.CapabilityGrantStatusActive,
	}
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake() err = %v", err)
	}

	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.ResourceBlocker == "resource_permission_denied" {
			t.Fatalf("open next actions = %#v, want fresh timeout result to supersede stale resource repair blockers", open)
		}
	}
	var foundTransient bool
	for _, action := range open {
		if action.State == session.NextActionScheduledRetry && action.ResourceBlocker == "external_transient" {
			foundTransient = true
		}
	}
	if !foundTransient {
		t.Fatalf("open next actions = %#v, want fresh transient retry state", open)
	}
}

func TestParentConversationAckClosesQueuedGrantTaskWaiterForClaimScopedPacket(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-claim-scoped-grant",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "manual_channel",
		WakeupMode:         "manual",
		Status:             "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle claim-scoped grant wake tests.",
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
		GrantID:        "capg-child-claim-scoped-grant",
		RequestID:      "cap-child-claim-scoped-grant",
		GrantedTo:      "durable_agent:child-claim-scoped-grant",
		Kind:           session.CapabilityKindTool,
		TargetResource: "gog_cli",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(queue) err = %v", err)
	}
	if len(open) != 1 || open[0].SubjectRef != taskPacketID || open[0].State != session.NextActionWaitingForChild {
		t.Fatalf("open next actions after queue = %#v, want one grant task waiter", open)
	}
	pending, err := rt.pendingDurableAgentParentConversation(agent.AgentID, 10)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	messageIDs := core.DurableAgentConversationMessageIDs(pending)
	if len(messageIDs) != 1 || messageIDs[0] != taskPacketID {
		t.Fatalf("pending message IDs = %#v, want queued grant task %q", messageIDs, taskPacketID)
	}
	claimPacketID := "child_task:claim-scoped-grant-task"
	if claimPacketID == taskPacketID {
		t.Fatalf("claim packet id = %q, want distinct from queued grant task", claimPacketID)
	}
	payloadRaw, _ := json.Marshal(map[string]any{
		"agent_id":      agent.AgentID,
		"message_ids":   messageIDs,
		"message_count": len(messageIDs),
		"summary":       "Grant incorporated through approved direct wake.",
	})
	if err := rt.applyDurableWakeParentConversationAckIntent(agent, session.ChildTaskOutcomeIntent{
		PacketID:    claimPacketID,
		ResultID:    "child_result:claim-scoped-grant-task",
		Kind:        session.ChildTaskOutcomeIntentParentConversationAck,
		PayloadJSON: string(payloadRaw),
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("applyDurableWakeParentConversationAckIntent(claim-scoped grant task) err = %v", err)
	}
	open, err = store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after claim ack) err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions after claim-scoped ack = %#v, want consumed grant task waiter closed", open)
	}
	pending, err = rt.pendingDurableAgentParentConversation(agent.AgentID, 10)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation(after ack) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending parent messages after ack = %#v, want consumed", pending)
	}
}

func TestCapabilityGrantWakeRepeatedAttemptsRecordDistinctResults(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	provider.replyText = "Grant incorporated on this attempt, but more work remains.\nREVIEW_STATUS: update"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-retry",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "manual_channel",
		WakeupMode:         "manual",
		Status:             "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle repeated grant wake attempts.",
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
		GrantID:        "capg-child-retry",
		RequestID:      "cap-child-retry",
		GrantedTo:      "durable_agent:child-retry",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}
	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)

	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake(first) err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake(first) err = %v", err)
	}
	firstPacket, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(first) err = %v", err)
	}
	if !ok || firstPacket.Status != session.ChildTaskPacketInProgress || firstPacket.ResultID == "" {
		t.Fatalf("first packet = %#v ok=%t, want in-progress packet", firstPacket, ok)
	}
	firstResult, ok, err := store.ChildTaskResult(firstPacket.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult(first) err = %v", err)
	}
	if !ok || firstResult.AttemptID == "" || firstResult.PacketID != taskPacketID {
		t.Fatalf("first result = %#v ok=%t, want attempt-linked result", firstResult, ok)
	}

	time.Sleep(time.Millisecond)
	provider.replyText = "Grant incorporated on retry.\nREVIEW_STATUS: completed"
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake(second) err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake(second) err = %v", err)
	}
	secondPacket, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(second) err = %v", err)
	}
	if !ok || secondPacket.PacketID != firstPacket.PacketID || secondPacket.ResultID == "" || secondPacket.ResultID == firstPacket.ResultID {
		t.Fatalf("second packet = %#v first = %#v ok=%t, want same packet with new result", secondPacket, firstPacket, ok)
	}
	if secondPacket.Status != session.ChildTaskPacketCompleted {
		t.Fatalf("second packet status = %s, want completed", secondPacket.Status)
	}
	secondResult, ok, err := store.ChildTaskResult(secondPacket.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult(second) err = %v", err)
	}
	if !ok || secondResult.PacketID != taskPacketID || secondResult.AttemptID == "" || secondResult.AttemptID == firstResult.AttemptID || secondResult.ResultID == firstResult.ResultID {
		t.Fatalf("second result = %#v first = %#v ok=%t, want distinct attempt/result", secondResult, firstResult, ok)
	}
	if _, ok, err := store.ChildTaskResult(firstResult.ResultID); err != nil || !ok {
		t.Fatalf("ChildTaskResult(first after retry) ok=%t err=%v, want first result retained", ok, err)
	}

	events, err := store.ExecutionEventsBySession(rt.durableAgentExecutionKey(agent.AgentID), 0, 120)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableChildTaskQueued, taskPacketID, "")
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableWakeStarted, taskPacketID, firstResult.AttemptID)
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableWakeCompleted, taskPacketID, firstResult.AttemptID)
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableChildTaskResult, taskPacketID, firstResult.AttemptID)
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableWakeStarted, taskPacketID, secondResult.AttemptID)
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableWakeCompleted, taskPacketID, secondResult.AttemptID)
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableChildTaskResult, taskPacketID, secondResult.AttemptID)
}

func TestCapabilityGrantWakeRestartSpanningTaskProtocolAndAuthorityFailClosed(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	provider.replyText = "Grant incorporated through managed child wake.\nREVIEW_STATUS: completed"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-restart",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "manual_channel",
		WakeupMode:         "manual",
		Status:             "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle restart-spanning child task protocol tests.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	now := time.Now().UTC()
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-child-restart",
		RequestID:      "cap-child-restart",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "durable_agent:child-restart",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant() err = %v", err)
	}

	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake() err = %v", err)
	}
	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)
	dbPath := cfg.Sessions.DBPath
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	reopened, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store err = %v", err)
	}
	defer reopened.Close()

	packet, ok, err := reopened.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("reopened ChildTaskPacket() err = %v", err)
	}
	if !ok || packet.Status != session.ChildTaskPacketCompleted || packet.TaskLeaseID == "" || packet.ResultID == "" {
		t.Fatalf("reopened packet = %#v ok=%t, want completed leased packet", packet, ok)
	}
	result, ok, err := reopened.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("reopened ChildTaskResult() err = %v", err)
	}
	if !ok || result.Status != session.ChildTaskResultCompleted || len(result.EvidenceRefs) == 0 {
		t.Fatalf("reopened result = %#v ok=%t, want persisted terminal evidence", result, ok)
	}

	if _, err := recordRepresentativeManagedInvocationForTest(reopened, session.CapabilityKindTool, "codex", "durable_agent:child-restart", "invoke", result.SessionID); err != nil {
		t.Fatalf("active reopened grant invocation err = %v", err)
	}
	active, ok, err := reopened.CapabilityGrant(grant.GrantID)
	if err != nil {
		t.Fatalf("CapabilityGrant(active) err = %v", err)
	}
	if !ok || active.InvocationCount != 1 {
		t.Fatalf("active grant after invocation = %#v ok=%t, want one invocation", active, ok)
	}

	active.Status = session.CapabilityGrantStatusRevoked
	active.RevokedAt = time.Now().UTC()
	active.UpdatedAt = active.RevokedAt
	if _, err := reopened.UpsertCapabilityGrant(active); err != nil {
		t.Fatalf("UpsertCapabilityGrant(revoked) err = %v", err)
	}
	if _, err := recordRepresentativeManagedInvocationForTest(reopened, session.CapabilityKindTool, "codex", "durable_agent:child-restart", "invoke", result.SessionID); err == nil {
		t.Fatal("revoked grant invocation err = nil, want fail closed")
	}

	expired := grant
	expired.GrantID = "capg-child-restart-expired"
	expired.Status = session.CapabilityGrantStatusActive
	expired.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	expired.RevokedAt = time.Time{}
	expired.InvocationCount = 0
	expired.FailureCount = 0
	expired.CreatedAt = time.Now().UTC().Add(-time.Hour)
	expired.UpdatedAt = time.Now().UTC()
	if _, err := reopened.UpsertCapabilityGrant(expired); err != nil {
		t.Fatalf("UpsertCapabilityGrant(expired) err = %v", err)
	}
	if _, err := recordRepresentativeManagedInvocationForTest(reopened, session.CapabilityKindTool, "codex", "durable_agent:child-restart", "invoke", result.SessionID); err == nil {
		t.Fatal("expired grant invocation err = nil, want fail closed")
	}
}

func TestCapabilityGrantWakeBlockedResultCreatesTypedNextState(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	provider.replyText = "Processed active grants and non-secret config. Runtime check: gog_cli=missing_or_not_executable.\nREVIEW_STATUS: blocked"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-blocked",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "manual_channel",
		WakeupMode:         "manual",
		Status:             "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle blocked grant wake tests.",
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
		GrantID:        "capg-child-blocked",
		RequestID:      "cap-child-blocked",
		GrantedTo:      "durable_agent:child-blocked",
		Kind:           session.CapabilityKindTool,
		TargetResource: "gog_cli",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake() err = %v", err)
	}
	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)
	packet, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket() err = %v", err)
	}
	if !ok || packet.Status != session.ChildTaskPacketBlocked || packet.ResultID == "" {
		t.Fatalf("blocked packet = %#v ok=%t, want blocked terminal packet", packet, ok)
	}
	result, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult() err = %v", err)
	}
	if !ok || result.Status != session.ChildTaskResultBlocked || result.NextState != session.NextActionNeedsVerification || result.BlockerKind != "tool_runtime_probe_missing" {
		t.Fatalf("blocked result = %#v ok=%t, want child runtime probe-required blocker", result, ok)
	}
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].SubjectKind != "task_packet" || open[0].SubjectRef != taskPacketID || open[0].State != session.NextActionNeedsVerification || open[0].ResourceBlocker != "tool_runtime_probe_missing" {
		t.Fatalf("open next actions after blocked child task = %#v, want one child runtime probe-required next state", open)
	}
	if open[0].OperationKind != session.NextActionOperationKindDurableChildRecovery || open[0].OperationTool != "update_operation" {
		t.Fatalf("open next action operation = kind %q tool %q, want child tool runtime probe", open[0].OperationKind, open[0].OperationTool)
	}
	opInput := capabilityGrantWakeOperationInputForTest(t, open[0].OperationInputJSON)
	if opInput["durable_agent_id"] != agent.AgentID || opInput["child_blocker_kind"] != "tool_runtime_probe_missing" || opInput["status"] != "blocked" || opInput["stage"] != "durable_child_blocker" || opInput["tool"] != "gog_cli" || opInput["no_content_probe"] != true || opInput["diagnostic_only"] != true || opInput["recovery_contract"] != "aphelion.recovery_handoff.v1" || opInput["recovery_operation_kind"] != session.NextActionOperationKindDurableChildRecovery || opInput["recovery_action"] != "tool_runtime_probe_missing" {
		t.Fatalf("operation input = %#v, want exact gog_cli diagnostic no-content probe", opInput)
	}
	handoff, ok := opInput["recovery_handoff"].(map[string]any)
	if !ok || handoff["contract"] != "aphelion.recovery_handoff.v1" || handoff["durable_agent_id"] != agent.AgentID || handoff["blocker_kind"] != "tool_runtime_probe_missing" || handoff["tool"] != "gog_cli" || handoff["no_content_probe"] != true || handoff["diagnostic_only"] != true || handoff["recovery_operation_kind"] != session.NextActionOperationKindDurableChildRecovery || handoff["recovery_action"] != "tool_runtime_probe_missing" {
		t.Fatalf("operation recovery_handoff = %#v, want typed durable child handoff", opInput["recovery_handoff"])
	}
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver(operation tools) err = %v", err)
	}
	operationTools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, operationTools)
	if _, err := operationTools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		rt.durableAgentExecutionKey(agent.AgentID),
		"update_operation",
		json.RawMessage(open[0].OperationInputJSON),
	); err != nil {
		t.Fatalf("execute generated update_operation handoff err = %v", err)
	}
	opState, err := store.OperationState(rt.durableAgentExecutionKey(agent.AgentID))
	if err != nil {
		t.Fatalf("OperationState(child execution key) err = %v", err)
	}
	if opState.RecoveryHandoff.Contract != "aphelion.recovery_handoff.v1" ||
		opState.RecoveryHandoff.DurableAgentID != agent.AgentID ||
		opState.RecoveryHandoff.BlockerKind != "tool_runtime_probe_missing" ||
		opState.RecoveryHandoff.OperationKind != session.NextActionOperationKindDurableChildRecovery ||
		opState.RecoveryHandoff.Tool != "gog_cli" ||
		!opState.RecoveryHandoff.NoContentProbe ||
		!opState.RecoveryHandoff.DiagnosticOnly {
		t.Fatalf("persisted recovery handoff = %#v, want typed durable child blocker metadata", opState.RecoveryHandoff)
	}
	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending review events = %#v, want immediate child blocker review card delivery", pending)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline review cards = %#v, want one immediate child blocker card", sender.inline)
	}
	if sender.inline[0].chatID != 1001 ||
		!strings.Contains(sender.inline[0].text, "tool runtime failure") ||
		!strings.Contains(sender.inline[0].text, "deterministic child-side") {
		t.Fatalf("inline review card = %#v, want precise probe-required blocker delivery", sender.inline[0])
	}
}

func TestCapabilityGrantWakeBlockedWithoutReviewTargetPersistsNextStateOnly(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	provider.replyText = "Processed active grants and non-secret config. Runtime check: gog_cli=missing_or_not_executable.\nREVIEW_STATUS: blocked"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:         "child-blocked-headless",
		ParentScopeKind: "durable_agent",
		ParentScopeID:   "child-blocked-headless",
		ChannelKind:     "manual_channel",
		WakeupMode:      "manual",
		Status:          "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle blocked grant wake tests without a review target.",
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
		GrantID:        "capg-child-blocked-headless",
		RequestID:      "cap-child-blocked-headless",
		GrantedTo:      "durable_agent:child-blocked-headless",
		Kind:           session.CapabilityKindTool,
		TargetResource: "gog_cli",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake() err = %v", err)
	}
	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].SubjectKind != "task_packet" || open[0].SubjectRef != taskPacketID || open[0].State != session.NextActionNeedsVerification || open[0].ResourceBlocker != "tool_runtime_probe_missing" {
		t.Fatalf("open next actions after headless blocked child task = %#v, want child runtime probe-required state", open)
	}
	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending review events = %#v, want none without review target", pending)
	}
}

func TestCapabilityGrantWakeUpdateThenCompletionResolvesPacketContinuation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	provider.replyText = "I incorporated the grant but still need another bounded pass.\nREVIEW_STATUS: update"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-update",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "manual_channel",
		WakeupMode:         "manual",
		Status:             "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle nonterminal update wake tests.",
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
		GrantID:        "capg-child-update",
		RequestID:      "cap-child-update",
		GrantedTo:      "durable_agent:child-update",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake() err = %v", err)
	}
	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)
	packet, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket() err = %v", err)
	}
	if !ok || packet.Status != session.ChildTaskPacketInProgress || packet.ResultID == "" || !packet.TerminalAt.IsZero() {
		t.Fatalf("update packet = %#v ok=%t, want in-progress nonterminal packet", packet, ok)
	}
	result, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult() err = %v", err)
	}
	if !ok || result.Status != session.ChildTaskResultUpdate || result.NextState != session.NextActionWaitingForChild || result.AttemptID == "" {
		t.Fatalf("update result = %#v ok=%t, want waiting child update", result, ok)
	}
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].SubjectKind != "task_packet" || open[0].SubjectRef != taskPacketID || open[0].State != session.NextActionWaitingForChild || open[0].RetryPolicy != "continue_after_child_update" {
		t.Fatalf("open next actions after update child task = %#v, want one bounded continuation", open)
	}
	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(update) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending review events after update = %#v, want no blocker review card", pending)
	}
	events, err := store.ExecutionEventsBySession(rt.durableAgentExecutionKey(agent.AgentID), 0, 80)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(update) err = %v", err)
	}
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableWakeCompleted, taskPacketID, result.AttemptID)
	assertEventPayloadJoin(t, events, core.ExecutionEventDurableChildTaskResult, taskPacketID, result.AttemptID)

	time.Sleep(time.Millisecond)
	provider.replyText = "The grant task is now complete.\nREVIEW_STATUS: completed"
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake(completion) err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("runCapabilityGrantWake(completion) err = %v", err)
	}
	completedPacket, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(completed) err = %v", err)
	}
	if !ok || completedPacket.Status != session.ChildTaskPacketCompleted || completedPacket.ResultID == result.ResultID || completedPacket.TerminalAt.IsZero() {
		t.Fatalf("completed packet = %#v ok=%t, want terminal packet with new result", completedPacket, ok)
	}
	completedResult, ok, err := store.ChildTaskResult(completedPacket.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult(completed) err = %v", err)
	}
	if !ok || completedResult.Status != session.ChildTaskResultCompleted || completedResult.AttemptID == result.AttemptID {
		t.Fatalf("completed result = %#v ok=%t update = %#v, want distinct terminal attempt", completedResult, ok, result)
	}
	open, err = store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(completion) err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open next actions after update completion = %#v, want none", open)
	}
}

func TestCapabilityGrantWakeFailureCreatesPacketRepairNextAction(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	provider.err = context.DeadlineExceeded
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "child-failure",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "manual_channel",
		WakeupMode:         "manual",
		Status:             "active",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle failed grant wake tests.",
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
		GrantID:        "capg-child-failure",
		RequestID:      "cap-child-failure",
		GrantedTo:      "durable_agent:child-failure",
		Kind:           session.CapabilityKindTool,
		TargetResource: "codex",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}
	if err := rt.queueCapabilityGrantWake(context.Background(), agent.AgentID, grant); err != nil {
		t.Fatalf("queueCapabilityGrantWake() err = %v", err)
	}
	if err := rt.runCapabilityGrantWake(context.Background(), agent.AgentID, grant); err == nil {
		t.Fatal("runCapabilityGrantWake() err = nil, want provider failure")
	}
	taskPacketID := capabilityGrantTaskPacketID(agent.AgentID, grant)
	packet, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket() err = %v", err)
	}
	if !ok || packet.Status != session.ChildTaskPacketFailed || packet.ResultID == "" || packet.TerminalAt.IsZero() {
		t.Fatalf("failed packet = %#v ok=%t, want terminal failed packet", packet, ok)
	}
	result, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult() err = %v", err)
	}
	if !ok || result.Status != session.ChildTaskResultFailed || result.NextState != session.NextActionBlockedNeedsResourceRepair || result.ErrorText == "" {
		t.Fatalf("failed result = %#v ok=%t, want resource repair failed result", result, ok)
	}
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].SubjectKind != "task_packet" || open[0].SubjectRef != taskPacketID || open[0].State != session.NextActionBlockedNeedsResourceRepair || open[0].ResourceBlocker != "wake_failed" || open[0].OperationKind != session.NextActionOperationKindDurableChildRecovery {
		t.Fatalf("open next actions after failed child task = %#v, want packet wake-repair next state", open)
	}
	opInput := capabilityGrantWakeOperationInputForTest(t, open[0].OperationInputJSON)
	if opInput["agent_id"] != agent.AgentID || opInput["blocker_kind"] != "wake_failed" || opInput["task_packet_id"] != taskPacketID || opInput["child_result_id"] != result.ResultID || opInput["diagnostic_only"] != true || opInput["no_content_probe"] != false {
		t.Fatalf("failure operation input = %#v, want exact wake-repair refs", opInput)
	}
	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(failure) err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending review events after failed child task = %#v, want no child-authored blocker card", pending)
	}
}

func recordRepresentativeManagedInvocationForTest(store *session.SQLiteStore, kind session.CapabilityKind, target string, principal string, action string, sessionID string) (session.CapabilityInvocation, error) {
	grant, ok, err := store.ActiveCapabilityGrant(kind, target, principal, action)
	if err != nil {
		return session.CapabilityInvocation{}, err
	}
	if !ok {
		return session.CapabilityInvocation{}, fmt.Errorf("no active grant for %s %s %s", kind, target, action)
	}
	return store.RecordCapabilityInvocation(session.CapabilityInvocation{
		GrantID:         grant.GrantID,
		Principal:       principal,
		Action:          action,
		Status:          "allowed",
		OutcomeStatus:   "completed",
		SessionID:       sessionID,
		AuthoritySource: "capability_grant",
		CreatedAt:       time.Now().UTC(),
		CompletedAt:     time.Now().UTC(),
	})
}

func assertEventPayloadJoin(t *testing.T, events []session.ExecutionEvent, eventType string, packetID string, attemptID string) {
	t.Helper()
	for _, event := range events {
		if event.EventType != eventType {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("decode %s payload %q: %v", eventType, event.PayloadJSON, err)
		}
		eventPacketID, _ := payload["task_packet_id"].(string)
		if eventPacketID == "" {
			eventPacketID, _ = payload["packet_id"].(string)
		}
		if eventPacketID != packetID {
			continue
		}
		if attemptID == "" {
			return
		}
		eventAttemptID, _ := payload["attempt_id"].(string)
		if eventAttemptID == attemptID {
			return
		}
	}
	t.Fatalf("missing %s payload join packet_id=%q attempt_id=%q in events %#v", eventType, packetID, attemptID, events)
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
