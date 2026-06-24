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
	store      *session.SQLiteStore
	err        error
	calls      []string
	messageIDs [][]string
}

func (f *fakeDurableAgentWakeRunner) RunDurableAgentParentConversationWake(_ context.Context, agentID string, messageIDs []string, now time.Time) error {
	f.calls = append(f.calls, agentID)
	f.messageIDs = append(f.messageIDs, append([]string(nil), messageIDs...))
	if f.err != nil {
		return f.err
	}
	if f.store == nil {
		return nil
	}
	_, _, err := f.store.UpdateDurableAgentContinuity(agentID, func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		updated, err := acknowledgeParentConversationMessagesForWakeTest(continuity, messageIDs, now)
		if err != nil {
			return continuity, err
		}
		return updated.WithConversationMessage("child", "acknowledged parent guidance", now), nil
	})
	return err
}

func acknowledgeParentConversationMessagesForWakeTest(continuity core.DurableAgentContinuityState, messageIDs []string, at time.Time) (core.DurableAgentContinuityState, error) {
	if len(messageIDs) == 0 {
		return continuity, nil
	}
	return continuity.AcknowledgeParentConversationMessageIDs(messageIDs, at)
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
	if len(runner.messageIDs) != 1 || len(runner.messageIDs[0]) != 1 || strings.TrimSpace(runner.messageIDs[0][0]) == "" {
		t.Fatalf("wake runner message IDs = %#v, want exact pending parent batch", runner.messageIDs)
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

func TestDurableAgentWakeOnceAcceptsConsumedOneTurnLeaseSnapshot(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Use the approved one-turn wake.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin}
	ctx := contextWithDurableAgentWakeAuthorityForAgent(
		t,
		store,
		adminSessionKey(),
		actor,
		"lease-child-wake-consumed-one-turn",
		session.ContinuationLeaseClassChildWake,
		[]string{durableAgentWakeOnceAction},
		"child-alpha",
		session.ContinuationLeaseStatusConsumed,
		session.ContinuationLeaseStatusActive,
		0,
		1,
	)

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
	if !strings.Contains(out, "wake_status: completed") {
		t.Fatalf("wake_once output = %q, want completed consumed one-turn wake", out)
	}
}

func TestDurableAgentWakeOnceRequiresExactAgentConstraint(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Attempt the wrong constrained wake.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin}
	ctx := contextWithDurableAgentWakeAuthorityForAgent(
		t,
		store,
		adminSessionKey(),
		actor,
		"lease-child-wake-wrong-agent",
		session.ContinuationLeaseClassChildWake,
		[]string{durableAgentWakeOnceAction},
		"child-other",
		session.ContinuationLeaseStatusActive,
		session.ContinuationLeaseStatusActive,
		1,
		1,
	)

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "not bound to agent_id") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want target constraint denial", err)
	}
}

func TestDurableAgentWakeOnceRequiresExactAgentID(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), principal.Principal{Role: principal.RoleAdmin}, "lease-child-wake-exact-id", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "requires exact agent_id") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want exact agent_id denial", err)
	}
}

func TestDurableAgentWakeOnceClaimsLeaseOnce(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{err: fmt.Errorf("wake runtime unavailable after claim")}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "First pending wake.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-claim-once", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})
	if _, err := registry.ExecuteForSessionPrincipal(ctx, actor, adminSessionKey(), "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`)); err != nil {
		t.Fatalf("first ExecuteForSessionPrincipal(wake_once) err = %v", err)
	}
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Second pending wake under same lease.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent second) err = %v", err)
	}

	_, err := registry.ExecuteForSessionPrincipal(ctx, actor, adminSessionKey(), "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`))
	if err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("second ExecuteForSessionPrincipal(wake_once) err = %v, want one-time claim denial", err)
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
	return contextWithDurableAgentWakeAuthorityForAgent(t, store, key, actor, leaseID, class, actions, "child-alpha", session.ContinuationLeaseStatusActive, session.ContinuationLeaseStatusActive, 1, 1)
}

func contextWithDurableAgentWakeAuthorityForAgent(t *testing.T, store *session.SQLiteStore, key session.SessionKey, actor principal.Principal, leaseID string, class session.ContinuationLeaseClass, actions []string, agentID string, storedStatus session.ContinuationLeaseStatus, snapshotStatus session.ContinuationLeaseStatus, storedTurns int, snapshotTurns int) context.Context {
	t.Helper()

	now := time.Now().UTC()
	storeContinuationLeaseForMatrix(t, store, key, session.ContinuationLease{
		ID:             leaseID,
		Status:         storedStatus,
		MaxTurns:       1,
		RemainingTurns: storedTurns,
		ExpiresAt:      now.Add(time.Hour),
		ApprovedAt:     now.Add(-time.Minute),
		ApprovedBy:     1001,
		LeaseClass:     class,
		AllowedActions: actions,
		Constraints:    map[string]string{"agent_id": strings.TrimSpace(agentID)},
	})
	ctx, _ := contextWithContinuationRunAuthority(t, store, key, actor, leaseID, snapshotStatus, snapshotTurns, now.Add(time.Hour), "durable_child_wake_test")
	return ctx
}
