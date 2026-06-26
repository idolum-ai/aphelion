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
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin},
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") || !strings.Contains(err.Error(), "lease request recorded") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want materialized child_wake lease request", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake before lease", runner.calls)
	}
	open, err := store.OpenNextActionsBySession(adminSessionKey(), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionBlockedNeedsAuthority || open[0].SubjectKind != "continuation_lease_request" {
		t.Fatalf("open next actions = %#v, want child_wake lease authority blocker", open)
	}
	if open[0].RequiredAuthority != string(session.ContinuationLeaseClassChildWake) || open[0].ResourceBlocker != "missing_continuation_lease" {
		t.Fatalf("open next action authority/blocker = %q/%q, want child_wake/missing_continuation_lease", open[0].RequiredAuthority, open[0].ResourceBlocker)
	}
	assertRecoveryContractProjectionForWakeTest(t, open[0].OperationInputJSON)
	contract := recoveryContractForWakeTest(t, store, open[0].OperationInputJSON)
	if contract.AgentID != "child-alpha" || contract.GrantID != grant.GrantID || contract.LeaseClass != session.ContinuationLeaseClassChildWake {
		t.Fatalf("recovery contract = %#v, want child-alpha child_wake bound to grant", contract)
	}
}

func TestDurableAgentWakeOnceLeaseRequestOperationCreatesExactPendingLease(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001})
	key := adminSessionKey()

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want missing child_wake lease", err)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open next actions = %#v, want one lease request", open)
	}
	if open[0].OperationTool != "request_approval" || !strings.Contains(open[0].OperationInputJSON, `"action":"request_continuation_lease"`) {
		t.Fatalf("open next action = %#v, want executable request_approval continuation lease payload", open[0])
	}

	out, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		open[0].OperationTool,
		json.RawMessage(open[0].OperationInputJSON),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval lease request) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval request render", out)
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending exact child_wake lease", cont)
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake {
		t.Fatalf("lease class = %q, want child_wake", cont.ContinuationLease.LeaseClass)
	}
	if !operationStringSliceContains(cont.ContinuationLease.AllowedActions, durableAgentWakeOnceAction) {
		t.Fatalf("lease allowed actions = %#v, want %q", cont.ContinuationLease.AllowedActions, durableAgentWakeOnceAction)
	}
	if got := strings.TrimSpace(cont.ContinuationLease.Constraints["agent_id"]); got != "child-alpha" {
		t.Fatalf("lease agent_id constraint = %q, want child-alpha", got)
	}
	if cont.ContinuationLease.MaxTurns != 1 || cont.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("lease turns = max %d remaining %d, want one bounded turn", cont.ContinuationLease.MaxTurns, cont.ContinuationLease.RemainingTurns)
	}
	if !strings.Contains(cont.ActionProposal.BoundedEffect, "child-alpha") || !strings.Contains(cont.ActionProposal.BoundedEffect, "once") {
		t.Fatalf("proposal bounded effect = %q, want exact child wake boundary", cont.ActionProposal.BoundedEffect)
	}
	if grant.GrantID == "" {
		t.Fatal("test grant id unexpectedly empty")
	}
}

func TestDurableAgentWakeOnceRefreshesStaleLeaseRequestAction(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", actor)
	key := adminSessionKey()
	requirement := durableAgentWakeOnceLeaseRequirement("child-alpha", grant, actor)
	legacyInput := map[string]any{
		"action":                  "request_continuation_lease",
		"lease_class":             string(session.ContinuationLeaseClassChildWake),
		"principal":               "telegram:1001",
		"allowed_actions":         []string{durableAgentWakeOnceAction},
		"constraints":             map[string]string{"agent_id": "child-alpha"},
		"tool":                    "durable_agent",
		"tool_action":             "wake_once",
		"grant_id":                grant.GrantID,
		"grant_target_resource":   grant.TargetResource,
		"agent_id":                "child-alpha",
		"retry_after_lease":       true,
		"recovery_contract":       recoveryHandoffContractVersion,
		"recovery_operation_kind": "continuation_lease_request",
	}
	legacy := seedMissingContinuationLeaseActionForWakeTest(t, store, key, requirement, "legacy-child-wake-lease-request", legacyInput, time.Now().Add(-time.Minute))

	_, err := registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") || !strings.Contains(err.Error(), "lease request recorded") || strings.Contains(err.Error(), "already recorded") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want stale request refreshed", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake before lease", runner.calls)
	}
	open := openMissingLeaseActionsForWakeTest(t, store, key, requirement)
	if len(open) != 1 {
		t.Fatalf("open matching actions = %#v, want refreshed singleton", open)
	}
	if open[0].RecordID == legacy.RecordID {
		t.Fatalf("open record id = %q, want legacy record superseded", open[0].RecordID)
	}
	assertRecoveryContractRequestInstanceForWakeTest(t, store, open[0].OperationInputJSON, true)
	if err := ValidateRecoveryHandoffToolInput(open[0].State, open[0].OperationTool, open[0].OperationInputJSON); err != nil {
		t.Fatalf("validate refreshed recovery handoff err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, open[0].OperationTool, json.RawMessage(open[0].OperationInputJSON))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval refreshed lease request) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval request render", out)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending ||
		cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(cont.ContinuationLease.Constraints["agent_id"]) != "child-alpha" ||
		!operationStringSliceContains(cont.ContinuationLease.AllowedActions, durableAgentWakeOnceAction) ||
		len(cont.ContinuationLease.RequiredCapabilityGrants) != 1 ||
		cont.ContinuationLease.RequiredCapabilityGrants[0].GrantID != grant.GrantID {
		t.Fatalf("continuation = %#v, want exact pending child_wake lease bound to grant %s", cont, grant.GrantID)
	}
}

func TestDurableAgentWakeOnceRefreshesTerminalLeaseRequestInstance(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", actor)
	key := adminSessionKey()
	requirement := durableAgentWakeOnceLeaseRequirement("child-alpha", grant, actor)
	const terminalInstanceID = "terminal-child-wake-request-instance"
	current := seedCurrentMissingContinuationLeaseActionForWakeTest(t, store, key, requirement, "terminal-child-wake-request", terminalInstanceID, time.Now().Add(-time.Minute))

	if _, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, current.OperationTool, json.RawMessage(current.OperationInputJSON)); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval current lease request) err = %v", err)
	}
	terminal, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	terminal.Status = session.ContinuationStatusRevoked
	terminal.ActionProposal.Status = session.ProposalStatusDenied
	terminal.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
	terminal.RemainingTurns = 0
	terminal.ContinuationLease.RemainingTurns = 0
	if err := store.UpdateContinuationState(key, terminal); err != nil {
		t.Fatalf("UpdateContinuationState(terminal request instance) err = %v", err)
	}

	_, err = registry.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "lease request recorded") || strings.Contains(err.Error(), "already recorded") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want terminal request instance refreshed", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake before lease", runner.calls)
	}
	open := openMissingLeaseActionsForWakeTest(t, store, key, requirement)
	if len(open) != 1 {
		t.Fatalf("open matching actions = %#v, want refreshed singleton", open)
	}
	if open[0].RecordID == current.RecordID {
		t.Fatalf("open record id = %q, want terminal request action superseded", open[0].RecordID)
	}
	if got := recoveryContractForWakeTest(t, store, open[0].OperationInputJSON).RequestInstanceID; got == terminalInstanceID || strings.TrimSpace(got) == "" {
		t.Fatalf("refreshed request_instance_id = %q, want fresh non-empty id different from terminal instance", got)
	}
}

func TestDurableAgentWakeOnceRefreshesMalformedLeaseRequestActions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		toolName  string
		operation string
		mutate    func(map[string]any) string
	}{
		{
			name: "non object json",
			mutate: func(map[string]any) string {
				return `["request_continuation_lease"]`
			},
		},
		{
			name: "wrong recovery contract",
			mutate: func(payload map[string]any) string {
				payload["recovery_contract"] = "aphelion.recovery_handoff.v0"
				return marshalWakeTestJSON(payload)
			},
		},
		{
			name: "wrong recovery operation",
			mutate: func(payload map[string]any) string {
				payload["recovery_operation_kind"] = "capability_grant_review"
				return marshalWakeTestJSON(payload)
			},
		},
		{
			name:     "wrong tool",
			toolName: "update_operation",
			mutate: func(payload map[string]any) string {
				return marshalWakeTestJSON(payload)
			},
		},
		{
			name: "wrong agent",
			mutate: func(payload map[string]any) string {
				payload["agent_id"] = "child-beta"
				payload["constraints"] = map[string]string{"agent_id": "child-beta"}
				return marshalWakeTestJSON(payload)
			},
		},
		{
			name: "wrong grant",
			mutate: func(payload map[string]any) string {
				payload["grant_id"] = "grant-other"
				return marshalWakeTestJSON(payload)
			},
		},
		{
			name: "wrong target resource",
			mutate: func(payload map[string]any) string {
				payload["grant_target_resource"] = "durable_agent:child-beta:wake_once"
				return marshalWakeTestJSON(payload)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry, store := newDurableAgentToolRegistry(t)
			registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
			upsertDurableAgentWakeTestAgent(t, store)
			actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
			grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", actor)
			key := session.SessionKey{ChatID: 9100 + int64(len(tc.name)), UserID: 1001}
			requirement := durableAgentWakeOnceLeaseRequirement("child-alpha", grant, actor)
			currentPayload := legacyMissingLeasePayloadForWakeTest(requirement, "stale-"+strings.ReplaceAll(tc.name, " ", "-"))
			raw := tc.mutate(currentPayload)
			toolName := firstNonEmpty(tc.toolName, "request_approval")
			operationKind := firstNonEmpty(tc.operation, "continuation_lease_request")
			legacy := seedMissingContinuationLeaseActionRawForWakeTest(t, store, key, requirement, "stale-"+strings.ReplaceAll(tc.name, " ", "-"), toolName, operationKind, raw, time.Now().Add(-time.Minute))

			_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`))
			if err == nil || !strings.Contains(err.Error(), "lease request recorded") || strings.Contains(err.Error(), "already recorded") {
				t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want malformed request refreshed", err)
			}
			open := openMissingLeaseActionsForWakeTest(t, store, key, requirement)
			if len(open) != 1 || open[0].RecordID == legacy.RecordID {
				t.Fatalf("open matching actions = %#v, legacy = %#v, want refreshed singleton", open, legacy)
			}
			assertRecoveryContractRequestInstanceForWakeTest(t, store, open[0].OperationInputJSON, true)
			if err := ValidateRecoveryHandoffToolInput(open[0].State, open[0].OperationTool, open[0].OperationInputJSON); err != nil {
				t.Fatalf("validate refreshed recovery handoff err = %v", err)
			}
		})
	}
}

func TestDurableAgentWakeOnceCurrentLeaseRequestActionIsIdempotentPastPagination(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", actor)
	key := adminSessionKey()
	requirement := durableAgentWakeOnceLeaseRequirement("child-alpha", grant, actor)
	current := seedCurrentMissingContinuationLeaseActionForWakeTest(t, store, key, requirement, "current-child-wake-request", "current-child-wake-request", time.Now().Add(-2*time.Hour))
	for i := 0; i < 125; i++ {
		_, err := store.RecordNextAction(session.NextActionInput{
			RecordID:          fmt.Sprintf("unrelated-action-%03d", i),
			Key:               key,
			Owner:             "test",
			State:             session.NextActionBlockedNeedsAuthority,
			SubjectKind:       "unrelated",
			SubjectRef:        fmt.Sprintf("subject-%03d", i),
			NextAction:        "unrelated action",
			RequiredAuthority: "test",
			CreatedAt:         time.Now().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("RecordNextAction(unrelated %d) err = %v", i, err)
		}
	}

	_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`))
	if err == nil || !strings.Contains(err.Error(), "lease request already recorded") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want compatible request found beyond session pagination", err)
	}
	open := openMissingLeaseActionsForWakeTest(t, store, key, requirement)
	if len(open) != 1 || open[0].RecordID != current.RecordID {
		t.Fatalf("open matching actions = %#v, want original current action %q", open, current.RecordID)
	}
}

func TestDurableAgentWakeOnceLeaseRequestSubjectIsSessionScoped(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	registry.WithDurableAgentWakeRunner(&fakeDurableAgentWakeRunner{store: store})
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", actor)
	requirement := durableAgentWakeOnceLeaseRequirement("child-alpha", grant, actor)
	otherKey := session.SessionKey{ChatID: 9201, UserID: 1001}
	key := session.SessionKey{ChatID: 9202, UserID: 1001}
	seedCurrentMissingContinuationLeaseActionForWakeTest(t, store, otherKey, requirement, "other-session-current-child-wake-request", "other-session-current-child-wake-request", time.Now().Add(-time.Minute))

	_, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`))
	if err == nil || !strings.Contains(err.Error(), "lease request recorded") || strings.Contains(err.Error(), "already recorded") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want new request in current session", err)
	}
	if open := openMissingLeaseActionsForWakeTest(t, store, key, requirement); len(open) != 1 {
		t.Fatalf("current session open matching actions = %#v, want one new action", open)
	}
	if open := openMissingLeaseActionsForWakeTest(t, store, otherKey, requirement); len(open) != 1 {
		t.Fatalf("other session open matching actions = %#v, want other session action preserved", open)
	}
}

func TestRequestApprovalContinuationLeaseRejectsConflictingChildWakeConstraint(t *testing.T) {
	t.Parallel()

	_, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "test-conflicting-child-wake-request",
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{durableAgentWakeOnceAction},
		Constraints:         map[string]string{"agent_id": "child-beta"},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             "child-alpha",
		GrantID:             "grant-child-alpha-wake",
		GrantTargetResource: "durable_agent:child-alpha:wake_once",
		CreatedAt:           time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "agent_id constraint mismatch") {
		t.Fatalf("CompileContinuationRecoveryContract(conflicting child_wake) err = %v, want constraint mismatch", err)
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
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") || !strings.Contains(err.Error(), "review request queued") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once missing grant) err = %v, want queued review request", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake before grant", runner.calls)
	}
	requests, err := store.CapabilityRequests(10, session.CapabilityReviewStatusProposed, session.CapabilityKindGenericDelegation, "telegram:1001")
	if err != nil {
		t.Fatalf("CapabilityRequests() err = %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("CapabilityRequests() len = %d, want 1", len(requests))
	}
	request := requests[0]
	if request.TargetResource != "durable_agent:child-alpha:wake_once" || request.RequestedFor != "telegram:1001" {
		t.Fatalf("request = %#v, want exact durable_agent child wake request for telegram:1001", request)
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
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") || !strings.Contains(err.Error(), "review request queued") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once broad grant) err = %v, want exact missing grant review", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake under broad grant", runner.calls)
	}
}

func TestDurableAgentWakeOnceAcceptsLiveStyleExactWakeGrant(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Use the live-style approved wake grant.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-live-style-wake-once",
		RequestID:      "req-live-style-wake-once",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "durable_agent:child-alpha:wake_once",
		AllowedActions: []string{"invoke"},
		Constraints:    `{"agent_id":"child-alpha","consume_existing_parent_guidance_only":true,"max_wake_count":1,"no_retry":true}`,
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(live style) err = %v", err)
	}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-live-style-grant", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	out, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once live style grant) err = %v", err)
	}
	if got := fmt.Sprint(runner.calls); got != "[child-alpha]" {
		t.Fatalf("wake runner calls = %s, want [child-alpha]", got)
	}
	if !strings.Contains(out, "wake_status: completed") {
		t.Fatalf("wake_once output = %q, want completed", out)
	}
}

func TestDurableAgentWakeOnceAcceptsDispatcherWakeGrantWithAgentConstraint(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Use the dispatcher-style approved wake grant.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
	}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-dispatcher-wake-once",
		RequestID:      "req-dispatcher-wake-once",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "durable_agent:wake_once",
		AllowedActions: []string{"invoke"},
		Constraints:    `{"agent_id":"child-alpha","consume_existing_parent_guidance_only":true,"max_wake_count":1,"no_retry":true}`,
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(dispatcher wake) err = %v", err)
	}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-dispatcher-grant", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	out, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once dispatcher grant) err = %v", err)
	}
	if got := fmt.Sprint(runner.calls); got != "[child-alpha]" {
		t.Fatalf("wake runner calls = %s, want [child-alpha]", got)
	}
	if !strings.Contains(out, "wake_status: completed") {
		t.Fatalf("wake_once output = %q, want completed", out)
	}
}

func TestDurableAgentWakeOnceGrantContractMatchesMaterializedRequirement(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	contract := durableAgentWakeOnceGrantContract("child-alpha", actor)
	requirement := normalizeMissingGrantContract(contract).Requirement
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-roundtrip-wake-once-contract",
		RequestID:      requirement.RequestID,
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
		t.Fatalf("UpsertCapabilityGrant(materialized requirement) err = %v", err)
	}

	got, ok, err := registry.activeGrantForMissingGrantContract(contract, json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`))
	if err != nil {
		t.Fatalf("activeGrantForMissingGrantContract() err = %v", err)
	}
	if !ok || got.GrantID != grant.GrantID {
		t.Fatalf("activeGrantForMissingGrantContract() = (%#v, %t), want %s", got, ok, grant.GrantID)
	}
}

func TestDurableAgentWakeOnceRejectsExactGrantWithConflictingAgentConstraint(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-conflicting-agent-wake-once",
		RequestID:      "req-conflicting-agent-wake-once",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "durable_agent:child-alpha:wake_once",
		AllowedActions: []string{"invoke"},
		Constraints:    `{"agent_id":"child-beta","consume_existing_parent_guidance_only":true,"max_wake_count":1}`,
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(conflicting agent) err = %v", err)
	}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-conflicting-grant", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") || !strings.Contains(err.Error(), "review request queued") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once conflicting exact grant) err = %v, want missing grant review", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake under conflicting exact grant", runner.calls)
	}
}

func TestDurableAgentWakeOnceRejectsGrantWithConflictingTopLevelAndNestedSelectors(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-conflicting-dual-agent-wake-once",
		RequestID:      "req-conflicting-dual-agent-wake-once",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "durable_agent:child-alpha:wake_once",
		AllowedActions: []string{"invoke"},
		Constraints: compactJSON(map[string]any{
			"agent_id": "child-beta",
			"tool_invocation": map[string]any{
				"actions": map[string]any{
					"wake_once": map[string]any{
						"selectors": map[string]any{
							"agent_id": []string{"child-alpha"},
						},
						"required_selectors":      []string{"agent_id"},
						"allowed_fields":          []string{"reason"},
						"allow_additional_fields": false,
					},
				},
			},
		}),
		Status: session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(conflicting dual agent) err = %v", err)
	}
	ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-conflicting-dual-grant", session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

	_, err := registry.ExecuteForSessionPrincipal(
		ctx,
		actor,
		adminSessionKey(),
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") || !strings.Contains(err.Error(), "review request queued") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once conflicting dual exact grant) err = %v, want missing grant review", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake under conflicting dual exact grant", runner.calls)
	}
}

func TestMissingGrantContractEvaluatesLaterShapesForSameGrantQuery(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-same-query-second-shape",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "durable_agent:child-alpha:wake_once",
		AllowedActions: []string{"invoke"},
		Constraints:    `{"agent_id":"child-alpha"}`,
		Status:         session.CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(same query second shape) err = %v", err)
	}
	contract := missingGrantContract{
		Requirement:        durableAgentWakeOnceMissingGrantRequirement("child-alpha", principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}),
		AcceptedPrincipals: []string{"telegram:1001"},
		AcceptedGrantShapes: []missingGrantAcceptedShape{
			{
				Kind:                session.CapabilityKindGenericDelegation,
				TargetResource:      "durable_agent:child-alpha:wake_once",
				Action:              "invoke",
				ToolInvocationScope: missingGrantToolInvocationScopeRequired,
			},
			{
				Kind:                session.CapabilityKindGenericDelegation,
				TargetResource:      "durable_agent:child-alpha:wake_once",
				Action:              "invoke",
				ToolInvocationScope: missingGrantToolInvocationScopeIgnored,
				RequiredConstraints: map[string]string{"agent_id": "child-alpha"},
			},
		},
	}

	grant, ok, err := registry.activeGrantForMissingGrantContract(contract, json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`))
	if err != nil {
		t.Fatalf("activeGrantForMissingGrantContract() err = %v", err)
	}
	if !ok || grant.GrantID != "grant-same-query-second-shape" {
		t.Fatalf("activeGrantForMissingGrantContract() = (%#v, %t), want second-shape grant", grant, ok)
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
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") || !strings.Contains(err.Error(), "review request queued") {
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
	requests, err := store.CapabilityRequests(10, session.CapabilityReviewStatusProposed, session.CapabilityKindGenericDelegation, "telegram:1001")
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

func TestMissingContinuationLeaseRefreshesStaleDataAccessAction(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	key := session.SessionKey{ChatID: 9301, UserID: 1001}
	requirement := normalizeMissingContinuationLeaseRequirement(missingContinuationLeaseRequirement{
		Resource:            "/child/runtime-bin",
		GrantID:             "capg-runtime-read",
		GrantTargetResource: "/child/runtime-bin",
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassDataAccess,
		AllowedActions:      []string{"read_approved_resource"},
		Constraints: map[string]string{
			"grant_id":              "capg-runtime-read",
			"grant_target_resource": "/child/runtime-bin",
			"target_resource":       "/child/runtime-bin",
			"resource":              "/child/runtime-bin",
			"tool":                  "list_dir",
			"tool_action":           "list_dir",
		},
		Tool:       "list_dir",
		ToolAction: "list_dir",
	})
	legacyInput := map[string]any{
		"action":                  "request_continuation_lease",
		"lease_class":             string(session.ContinuationLeaseClassDataAccess),
		"principal":               "telegram:1001",
		"allowed_actions":         []string{"read_approved_resource"},
		"constraints":             requirement.Constraints,
		"tool":                    "list_dir",
		"tool_action":             "list_dir",
		"grant_id":                "capg-runtime-read",
		"grant_target_resource":   "/child/runtime-bin",
		"resource":                "/child/runtime-bin",
		"retry_after_lease":       true,
		"recovery_contract":       recoveryHandoffContractVersion,
		"recovery_operation_kind": "continuation_lease_request",
	}
	legacy := seedMissingContinuationLeaseActionForWakeTest(t, store, key, requirement, "legacy-data-access-lease-request", legacyInput, time.Now().Add(-time.Minute))

	err := registry.materializeMissingContinuationLeaseError(context.Background(), key, actor, missingContinuationLeaseError{
		requirement: requirement,
		cause:       fmt.Errorf("missing data_access continuation lease"),
	})
	if err == nil || !strings.Contains(err.Error(), "lease request recorded") || strings.Contains(err.Error(), "already recorded") {
		t.Fatalf("materializeMissingContinuationLeaseError() err = %v, want stale data_access request refreshed", err)
	}
	open := openMissingLeaseActionsForWakeTest(t, store, key, requirement)
	if len(open) != 1 || open[0].RecordID == legacy.RecordID {
		t.Fatalf("open matching actions = %#v, legacy = %#v, want refreshed singleton", open, legacy)
	}
	assertRecoveryContractRequestInstanceForWakeTest(t, store, open[0].OperationInputJSON, true)
	if err := ValidateRecoveryHandoffToolInput(open[0].State, open[0].OperationTool, open[0].OperationInputJSON); err != nil {
		t.Fatalf("validate refreshed data_access handoff err = %v", err)
	}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, open[0].OperationTool, json.RawMessage(open[0].OperationInputJSON))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval data_access) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval request render", out)
	}
	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDataAccess ||
		len(cont.ContinuationLease.RequiredCapabilityGrants) != 1 ||
		cont.ContinuationLease.RequiredCapabilityGrants[0].GrantID != "capg-runtime-read" ||
		cont.ContinuationLease.RequiredCapabilityGrants[0].TargetResource != "/child/runtime-bin" {
		t.Fatalf("continuation = %#v, want exact data_access lease bound to capg-runtime-read", cont)
	}
}

func TestDurableAgentWakeOnceRequiresChildWakeAuthority(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{store: store}
	registry.WithDurableAgentWakeRunner(runner)
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
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") || !strings.Contains(err.Error(), "lease request recorded") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want materialized child_wake lease request", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("wake runner calls = %v, want no child wake under wrong lease class", runner.calls)
	}
	open, err := store.OpenNextActionsBySession(adminSessionKey(), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].State != session.NextActionBlockedNeedsAuthority || open[0].RequiredAuthority != string(session.ContinuationLeaseClassChildWake) {
		t.Fatalf("open next actions = %#v, want child_wake authority blocker", open)
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
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") || !strings.Contains(err.Error(), "lease request recorded") {
		t.Fatalf("ExecuteForSessionPrincipal(wake_once) err = %v, want materialized exact child_wake lease request", err)
	}
	open, err := store.OpenNextActionsBySession(adminSessionKey(), 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 || open[0].RequiredAuthority != string(session.ContinuationLeaseClassChildWake) {
		t.Fatalf("open next actions = %#v, want exact child-alpha child_wake lease request", open)
	}
	if got := recoveryContractForWakeTest(t, store, open[0].OperationInputJSON).AgentID; got != "child-alpha" {
		t.Fatalf("recovery contract agent_id = %q, want child-alpha", got)
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

	out, err := registry.ExecuteForSessionPrincipal(ctx, actor, adminSessionKey(), "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"child-alpha"}`))
	if err != nil {
		t.Fatalf("second ExecuteForSessionPrincipal(wake_once) err = %v, want typed one-time claim failure", err)
	}
	for _, want := range []string{"wake_status: failed", "failure_class: grant_check_failed", "next: repair_child_wake_failure"} {
		if !strings.Contains(out, want) {
			t.Fatalf("second wake_once output = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "already claimed") {
		t.Fatalf("second wake_once output = %q, want sanitized claim failure", out)
	}
}

func TestDurableAgentWakeOnceReportsFailedWakeWithoutThrowing(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	runner := &fakeDurableAgentWakeRunner{err: fmt.Errorf("child wake deferred")}
	registry.WithDurableAgentWakeRunner(runner)
	upsertDurableAgentWakeTestAgent(t, store)
	grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
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
	for _, want := range []string{
		"wake_status: failed",
		"failure_class: runner_start_failed",
		"retry_policy: retry_after_wake_runtime_repair",
		"next_repair: inspect the durable-agent wake runtime",
		"next: repair_child_wake_failure",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("wake_once output = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "child wake deferred") || strings.Contains(out, "error:") {
		t.Fatalf("wake_once output = %q, want sanitized failure class without raw runner error", out)
	}
	invocations, err := store.CapabilityInvocationsByGrant(grant.GrantID, 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(wake failure) err = %v", err)
	}
	if len(invocations) != 1 || invocations[0].OutcomeStatus != "failed" || !strings.Contains(invocations[0].OutcomeErrorText, "runner_start_failed") {
		t.Fatalf("wake_once invocations = %#v, want failed outcome with sanitized class", invocations)
	}
}

func TestDurableAgentWakeOnceClassifiesRunnerFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		errText    string
		wantClass  string
		notContain string
	}{
		{
			name:       "adapter lifecycle",
			errText:    "child_runtime_blocked: preflight_failed adapter=gog_cli failure_code=lifecycle_unregistered next_repair=install/audit/probe",
			wantClass:  "adapter_lifecycle_failed",
			notContain: "lifecycle_unregistered",
		},
		{
			name:       "schema mismatch",
			errText:    "schema mismatch: store has migration 81 but binary expects 82",
			wantClass:  "schema_mismatch",
			notContain: "migration 81",
		},
		{
			name:       "sandbox exec",
			errText:    "sandbox exec failed: permission denied opening child runtime helper",
			wantClass:  "sandbox_exec_failed",
			notContain: "permission denied",
		},
		{
			name:       "grant check",
			errText:    "child_runtime_blocked: grant_expired grant_id=capg-child-secret",
			wantClass:  "grant_check_failed",
			notContain: "capg-child-secret",
		},
		{
			name:       "child runtime",
			errText:    "child_runtime_blocked: preflight_failed adapter=gog_cli failure_code=unknown next_repair=inspect",
			wantClass:  "child_runtime_blocked",
			notContain: "failure_code=unknown",
		},
		{
			name:       "transient please retry",
			errText:    "temporarily unavailable; please retry",
			wantClass:  "external_transient",
			notContain: "please retry",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			registry, store := newDurableAgentToolRegistry(t)
			runner := &fakeDurableAgentWakeRunner{err: fmt.Errorf("%s", tc.errText)}
			registry.WithDurableAgentWakeRunner(runner)
			upsertDurableAgentWakeTestAgent(t, store)
			grant := grantDurableAgentWakeOnceInvoke(t, store, "child-alpha", principal.Principal{Role: principal.RoleAdmin})
			if _, _, err := store.UpdateDurableAgentContinuity("child-alpha", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
				return continuity.WithConversationMessage("parent", "Please retry the approved wake.", time.Now().UTC()), nil
			}); err != nil {
				t.Fatalf("UpdateDurableAgentContinuity(parent) err = %v", err)
			}
			actor := principal.Principal{Role: principal.RoleAdmin}
			ctx := contextWithDurableAgentWakeAuthority(t, store, adminSessionKey(), actor, "lease-child-wake-failed-"+strings.ReplaceAll(tc.name, " ", "-"), session.ContinuationLeaseClassChildWake, []string{durableAgentWakeOnceAction})

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
			if !strings.Contains(out, "failure_class: "+tc.wantClass) || !strings.Contains(out, "next_repair: ") {
				t.Fatalf("wake_once output = %q, want class %s and next_repair", out, tc.wantClass)
			}
			if strings.Contains(out, tc.notContain) || strings.Contains(out, "error:") {
				t.Fatalf("wake_once output = %q, want no raw failure fragment %q", out, tc.notContain)
			}
			invocations, err := store.CapabilityInvocationsByGrant(grant.GrantID, 10)
			if err != nil {
				t.Fatalf("CapabilityInvocationsByGrant(%s) err = %v", tc.name, err)
			}
			if len(invocations) != 1 || invocations[0].OutcomeStatus != "failed" || !strings.Contains(invocations[0].OutcomeErrorText, tc.wantClass) {
				t.Fatalf("wake_once invocations = %#v, want failed outcome with %s", invocations, tc.wantClass)
			}
		})
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

func legacyMissingLeasePayloadForWakeTest(requirement missingContinuationLeaseRequirement, requestInstanceID string) map[string]any {
	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	requirement.RequestInstanceID = requestInstanceID
	payload := map[string]any{
		"action":                  "request_continuation_lease",
		"lease_class":             string(requirement.LeaseClass),
		"principal":               requirement.Principal,
		"allowed_actions":         requirement.AllowedActions,
		"constraints":             requirement.Constraints,
		"tool":                    requirement.Tool,
		"tool_action":             requirement.ToolAction,
		"grant_id":                requirement.GrantID,
		"grant_target_resource":   requirement.GrantTargetResource,
		"request_instance_id":     requirement.RequestInstanceID,
		"agent_id":                requirement.AgentID,
		"resource":                requirement.Resource,
		"retry_after_lease":       true,
		"recovery_contract":       recoveryHandoffContractVersion,
		"recovery_operation_kind": "continuation_lease_request",
	}
	if requirement.RetryOperation.Active() {
		payload["retry_operation"] = requirement.RetryOperation
	}
	return payload
}

func currentMissingLeasePayloadForWakeTest(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requirement missingContinuationLeaseRequirement, requestInstanceID string, createdAt time.Time) map[string]any {
	t.Helper()

	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	requirement.RequestInstanceID = requestInstanceID
	contract, err := continuationRecoveryContractFromMissingLeaseRequirement(key, requirement, createdAt)
	if err != nil {
		t.Fatalf("continuationRecoveryContractFromMissingLeaseRequirement() err = %v", err)
	}
	contract, err = store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract() err = %v", err)
	}
	op, err := compileContinuationLeaseRecoveryHandoff(contract)
	if err != nil {
		t.Fatalf("compileContinuationLeaseRecoveryHandoff() err = %v", err)
	}
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(op.InputJSON), &payload); err != nil {
		t.Fatalf("unmarshal recovery handoff input err = %v", err)
	}
	return payload
}

func seedCurrentMissingContinuationLeaseActionForWakeTest(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requirement missingContinuationLeaseRequirement, recordID string, requestInstanceID string, createdAt time.Time) session.NextActionRecord {
	t.Helper()

	return seedMissingContinuationLeaseActionForWakeTest(t, store, key, requirement, recordID, currentMissingLeasePayloadForWakeTest(t, store, key, requirement, requestInstanceID, createdAt), createdAt)
}

func seedMissingContinuationLeaseActionForWakeTest(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requirement missingContinuationLeaseRequirement, recordID string, input map[string]any, createdAt time.Time) session.NextActionRecord {
	t.Helper()

	return seedMissingContinuationLeaseActionRawForWakeTest(t, store, key, requirement, recordID, "request_approval", "continuation_lease_request", marshalWakeTestJSON(input), createdAt)
}

func seedMissingContinuationLeaseActionRawForWakeTest(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requirement missingContinuationLeaseRequirement, recordID string, operationTool string, operationKind string, raw string, createdAt time.Time) session.NextActionRecord {
	t.Helper()

	requirement = normalizeMissingContinuationLeaseRequirement(requirement)
	record, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         missingContinuationLeaseSubjectRef(requirement),
		CausalRefs:         missingContinuationLeaseCausalRefs(requirement),
		NextAction:         "approve a bounded continuation lease before retrying",
		RequiredAuthority:  string(requirement.LeaseClass),
		ResourceBlocker:    "missing_continuation_lease",
		RetryPolicy:        "retry_after_lease",
		OperationKind:      operationKind,
		OperationTool:      operationTool,
		OperationInputJSON: raw,
		OperatorProjection: requirement.OperatorProjection,
		CreatedAt:          createdAt,
	})
	if err != nil {
		t.Fatalf("RecordNextAction(%s) err = %v", recordID, err)
	}
	return record
}

func openMissingLeaseActionsForWakeTest(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requirement missingContinuationLeaseRequirement) []session.NextActionRecord {
	t.Helper()

	open, err := store.OpenNextActionsBySessionSubject(key, "continuation_lease_request", missingContinuationLeaseSubjectRef(requirement), 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySessionSubject() err = %v", err)
	}
	return open
}

func assertRecoveryContractRequestInstanceForWakeTest(t *testing.T, store *session.SQLiteStore, raw string, wantPresent bool) {
	t.Helper()

	got := recoveryContractForWakeTest(t, store, raw).RequestInstanceID
	if wantPresent && strings.TrimSpace(got) == "" {
		t.Fatalf("operation input = %s, want contract request_instance_id", raw)
	}
	if !wantPresent && strings.TrimSpace(got) != "" {
		t.Fatalf("operation input = %s, want no contract request_instance_id", raw)
	}
	assertRecoveryContractProjectionForWakeTest(t, raw)
}

func assertRecoveryContractProjectionForWakeTest(t *testing.T, raw string) {
	t.Helper()

	payload := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal operation input err = %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprint(payload["recovery_contract"])); got != recoveryHandoffContractVersion {
		t.Fatalf("operation input recovery_contract = %q, want %q", got, recoveryHandoffContractVersion)
	}
	if got := strings.TrimSpace(fmt.Sprint(payload["recovery_operation_kind"])); got != "continuation_lease_request" {
		t.Fatalf("operation input recovery_operation_kind = %q, want continuation_lease_request", got)
	}
	if got := strings.TrimSpace(fmt.Sprint(payload["contract_id"])); got == "" {
		t.Fatalf("operation input contract_id empty: %s", raw)
	}
}

func recoveryContractForWakeTest(t *testing.T, store *session.SQLiteStore, raw string) session.ContinuationRecoveryContract {
	t.Helper()

	payload := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal operation input err = %v", err)
	}
	contractID, _ := payload["contract_id"].(string)
	contract, ok, err := store.ContinuationRecoveryContract(strings.TrimSpace(contractID))
	if err != nil {
		t.Fatalf("ContinuationRecoveryContract(%q) err = %v", contractID, err)
	}
	if !ok {
		t.Fatalf("ContinuationRecoveryContract(%q) ok=false", contractID)
	}
	return contract
}

func marshalWakeTestJSON(input map[string]any) string {
	raw, _ := json.Marshal(input)
	return string(raw)
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
