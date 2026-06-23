//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestQueueCapabilityGrantWakeAddsParentConversation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
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
	if !ok || result.Status != session.ChildTaskResultCompleted || result.NextState != session.NextActionTerminal {
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
	provider.replyText = "The child needs a narrower runtime repair before using this grant.\nREVIEW_STATUS: blocked"
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
	if !ok || packet.Status != session.ChildTaskPacketBlocked || packet.ResultID == "" {
		t.Fatalf("blocked packet = %#v ok=%t, want blocked terminal packet", packet, ok)
	}
	result, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult() err = %v", err)
	}
	if !ok || result.Status != session.ChildTaskResultBlocked || result.NextState != session.NextActionBlockedNeedsAuthority {
		t.Fatalf("blocked result = %#v ok=%t, want typed authority blocker", result, ok)
	}
	open, err := store.OpenNextActionsBySession(rt.durableAgentExecutionKey(agent.AgentID), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].SubjectKind != "child_task_result" || open[0].SubjectRef != result.ResultID || open[0].State != session.NextActionBlockedNeedsAuthority {
		t.Fatalf("open next actions after blocked child task = %#v, want one typed blocker next state", open)
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
