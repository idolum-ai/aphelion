//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})

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

func TestDurableAgentWakeOnceMissingGrantMaterializesReviewableRequest(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-needs-grant", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha","reason":"one no-content readiness attempt"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "queued review request") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once missing grant) err = %v, want queued review request", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake before grant", runner.calls)
	}
	requests, err := store.CapabilityRequests(10, session.CapabilityReviewStatusProposed, session.CapabilityKindTool, "telegram:1001")
	if err != nil {
		t.Fatalf("CapabilityRequests() err = %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("CapabilityRequests() len = %d, want 1", len(requests))
	}
	request := requests[0]
	if request.TargetResource != "durable_agent" || request.RequestedFor != "telegram:1001" {
		t.Fatalf("request = %#v, want exact durable_agent request for telegram:1001", request)
	}
	for _, want := range []string{`"wake_once"`, `"agent_id":["child-alpha"]`, `"required_selectors":["agent_id"]`} {
		if !strings.Contains(request.Constraints, want) {
			t.Fatalf("request constraints = %s, want %s", request.Constraints, want)
		}
	}
	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].MetadataJSON, request.RequestID) {
		t.Fatalf("PendingReviewEvents() = %#v, want one capability review event for %s", pending, request.RequestID)
	}
	open, err := store.OpenNextActionsBySession(adminSessionKey(), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionBlockedNeedsAuthority || open[0].SubjectRef != request.RequestID {
		t.Fatalf("open next actions = %#v, want blocked_needs_authority for %s", open, request.RequestID)
	}
	if !strings.Contains(open[0].OperationInputJSON, `"action":"grant_set"`) || !strings.Contains(open[0].OperationInputJSON, `"request_id":"`+request.RequestID+`"`) {
		t.Fatalf("next action operation input = %s, want grant_set for request", open[0].OperationInputJSON)
	}
}

func TestDurableAgentWakeOnceBroadGrantDoesNotSatisfyExactWakeGrant(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-durable-agent-broad",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindTool,
		TargetResource: "durable_agent",
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(broad) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-broad-grant", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "queued review request") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once broad grant) err = %v, want exact missing grant review", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake under broad grant", runner.calls)
	}
}

func TestDurableAgentWakeOnceExpiredGrantDoesNotAuthorizeWake(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	requirement := durableAgentWakeOnceMissingGrantRequirement("child-alpha", actor)
	expiredGrant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-durable-agent-wake-once-expired",
		GrantedBy:      "telegram:1001",
		GrantedTo:      requirement.GrantedTo,
		Kind:           requirement.Kind,
		TargetResource: requirement.TargetResource,
		AllowedActions: requirement.AllowedActions,
		Contract:       requirement.Contract,
		Constraints:    requirement.Constraints,
		Status:         session.CapabilityGrantStatusActive,
		ExpiresAt:      time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant(expired) err = %v", err)
	}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-expired-grant", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err = registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "queued review request") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once expired grant) err = %v, want missing grant review", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no wake under expired grant", runner.calls)
	}
	invocations, err := store.CapabilityInvocationsByGrant(expiredGrant.GrantID, 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(expired) err = %v", err)
	}
	if len(invocations) != 0 {
		t.Fatalf("expired grant invocations = %#v, want none", invocations)
	}
}

func TestDurableAgentWakeOnceFindsValidGrantPastNoisyGrantRows(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", actor)
	for i := 0; i < 250; i++ {
		if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
			GrantID:        fmt.Sprintf("grant-noisy-durable-agent-%03d", i),
			GrantedBy:      "telegram:1001",
			GrantedTo:      "telegram:1001",
			Kind:           session.CapabilityKindTool,
			TargetResource: "durable_agent",
			AllowedActions: []string{"invoke"},
			Status:         session.CapabilityGrantStatusActive,
		}); err != nil {
			t.Fatalf("UpsertCapabilityGrant(noise %d) err = %v", i, err)
		}
	}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-noisy-grants", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	out, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once noisy grants) err = %v", err)
	}
	if !strings.Contains(out, "wake_status: skipped_no_pending_parent_message") {
		t.Fatalf("wake_once output = %q, want authorized skip without missing-grant request", out)
	}
	requests, err := store.CapabilityRequests(10, session.CapabilityReviewStatusProposed, session.CapabilityKindTool, "telegram:1001")
	if err != nil {
		t.Fatalf("CapabilityRequests() err = %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("CapabilityRequests() = %#v, want no unnecessary missing-grant request", requests)
	}
}

func TestMissingGrantMaterializationConcurrentDenialsLeaveOnePendingReview(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := adminSessionKey()
	requirement := durableAgentWakeOnceMissingGrantRequirement("child-alpha", actor)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, _, err := registry.materializeMissingGrantRequirement(context.Background(), key, actor, requirement, time.Now().UTC())
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("materializeMissingGrantRequirement concurrent err = %v", err)
		}
	}
	pending, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("PendingReviewEvents() len = %d, want exactly one pending card: %#v", len(pending), pending)
	}
	if got := reviewEventRequestIDForWakeTest(pending[0]); got == "" || got != stableMissingGrantRequestID(requirement) {
		t.Fatalf("pending review request id = %q, want %q", got, stableMissingGrantRequestID(requirement))
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionBlockedNeedsAuthority {
		t.Fatalf("open next actions = %#v, want exactly one authority blocker", open)
	}
}

func TestDurableAgentWakeOnceRequiresChildWakeAuthority(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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

func grantDurableAgentWakeOnceInvoke(t *testing.T, store *session.SQLiteStore, agentID string, actor principal.Principal) session.CapabilityGrant {
	t.Helper()

	requirement := durableAgentWakeOnceMissingGrantRequirement(agentID, actor)
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-durable-agent-wake-once-" + strings.TrimSpace(agentID) + "-" + strings.ReplaceAll(requirement.GrantedTo, ":", "-"),
		GrantedBy:      "telegram:1001",
		GrantedTo:      requirement.GrantedTo,
		Kind:           requirement.Kind,
		TargetResource: requirement.TargetResource,
		AllowedActions: requirement.AllowedActions,
		Contract:       requirement.Contract,
		Constraints:    requirement.Constraints,
		Status:         session.CapabilityGrantStatusActive,
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant(wake_once) err = %v", err)
	}
	return grant
}

func reviewEventRequestIDForWakeTest(event session.ReviewEvent) string {
	var metadata map[string]any
	if err := json.Unmarshal([]byte(event.MetadataJSON), &metadata); err != nil {
		return ""
	}
	if requestID, ok := metadata["request_id"].(string); ok {
		return strings.TrimSpace(requestID)
	}
	return ""
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
