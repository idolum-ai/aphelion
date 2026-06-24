//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type fakeDurableAgentWakeRunner struct {
	store *session.SQLiteStore
	err   error
	calls []string
}

func (f *fakeDurableAgentWakeRunner) RunDurableAgentChildWake(_ context.Context, agentID string, now time.Time) error {
	f.calls = append(f.calls, agentID)
	if f.err != nil {
		return f.err
	}
	if f.store == nil {
		return nil
	}
	_, _, err := f.store.UpdateDurableAgentContinuity(agentID, func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		updated, err := acknowledgeAllParentConversationMessagesForWakeTest(continuity, now)
		if err != nil {
			return continuity, err
		}
		return updated.WithConversationMessage("child", "acknowledged parent guidance", now), nil
	})
	return err
}

func acknowledgeAllParentConversationMessagesForWakeTest(continuity core.DurableAgentContinuityState, at time.Time) (core.DurableAgentContinuityState, error) {
	pending := continuity.PendingParentConversationMessages(0)
	if len(pending) == 0 {
		return continuity, nil
	}
	return continuity.AcknowledgeParentConversationMessageIDs(core.DurableAgentConversationMessageIDs(pending), at)
}

func TestDurableAgentToolDefinitionIncludesWakeOnce(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	defs := registry.Definitions()
	var durableDef []byte
	for _, def := range defs {
		if def.Name == "durable_agent" {
			durableDef = def.Parameters
			break
		}
	}
	if len(durableDef) == 0 {
		t.Fatal("durable_agent definition not found")
	}
	if !strings.Contains(string(durableDef), `"wake_once"`) {
		t.Fatalf("durable_agent schema = %s, want wake_once action", string(durableDef))
	}
}

func TestDurableAgentConversationSendDoesNotWake(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"conversation_send","agent_id":"child-alpha","message":"Please run a no-content health check."}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(conversation_send) err = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("conversation_send woke agent calls = %v, want none", runner.calls)
	}
	if !strings.Contains(out, "thread_state: awaiting_child_pickup") {
		t.Fatalf("conversation_send output = %q, want awaiting_child_pickup", out)
	}
}

func TestDurableAgentWakeOnceSkipsWithoutPendingParentMessage(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), principal.Principal{Role: principal.RoleAdmin}, "lease-child-wake-skip", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	out, err := registry.ExecuteForSessionPrincipal(
		ctx,
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake_once calls = %v, want skipped without runner call", runner.calls)
	}
	for _, want := range []string{
		"wake_status: skipped_no_pending_parent_message",
		"pending_parent_before: 0",
		"pending_parent_after: 0",
		"next: conversation_send",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("wake_once output = %q, want %q", out, want)
		}
	}
}

func TestDurableAgentWakeOnceRequiresRuntimeRunner(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	upsertDurableAgentWakeTestAgent(t, store)
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), principal.Principal{Role: principal.RoleAdmin}, "lease-child-wake-no-runner", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "requires durable child wake runtime") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want missing runner denial", err)
	}
}

func TestDurableAgentWakeOnceRequiresDurableRunAuthority(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "requires durable run authority evidence") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want durable run authority denial", err)
	}
}

func TestDurableAgentWakeOnceRequiresChildWakeAuthority(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-data-access", session.ContinuationLeaseClassDataAccess, []string{"read_approved_resource"})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "requires child_wake lease class") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want child_wake authority denial", err)
	}
}

func TestDurableAgentWakeOnceCallsRunnerForPendingParentMessage(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Please perform the approved no-content check.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-run", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	out, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v", err)
	}
	if got := fmt.Sprint(runner.calls); got != "[child-alpha]" {
		t.Fatalf("wake runner calls = %s, want [child-alpha]", got)
	}
	for _, want := range []string{
		"wake_status: completed",
		"pending_parent_before: 1",
		"pending_parent_after: 0",
		"thread_state_after: awaiting_parent_guidance",
		"next: conversation_show",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("wake_once output = %q, want %q", out, want)
		}
	}
}

func TestDurableAgentWakeOnceReportsFailedWakeWithoutThrowing(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{err: fmt.Errorf("child wake deferred")}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Please retry the approved wake.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-failed", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	out, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v", err)
	}
	if !strings.Contains(out, "wake_status: failed") || !strings.Contains(out, "next: inspect_child_runtime") {
		t.Fatalf("wake_once output = %q, want failed status and repair next step", out)
	}
}

func upsertDurableAgentWakeTestAgent(t *testing.T, store *session.SQLiteStore) {
	t.Helper()

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Run bounded checks and report concise outcomes.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
}

func contextWithDurableAgentWakeAuthority(t *testing.T, store *session.SQLiteStore, key session.SessionKey, actor principal.Principal, leaseID string, class session.ContinuationLeaseClass, actions []string) context.Context {
	t.Helper()

	now := time.Now().UTC()
	storeContinuationLeaseForMatrix(t, store, key, session.ContinuationLease{
		ID:             leaseID,
		Status:         session.ContinuationLeaseStatusActive,
		MaxTurns:       1,
		RemainingTurns: 1,
		ExpiresAt:      now.Add(time.Hour),
		ApprovedAt:     now.Add(-time.Minute),
		ApprovedBy:     1001,
		LeaseClass:     class,
		AllowedActions: actions,
	})
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, leaseID, session.ContinuationLeaseStatusActive, 1, now.Add(time.Hour), "durable_child_wake_test")
	return ctx
}
