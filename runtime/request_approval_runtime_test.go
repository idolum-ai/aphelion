//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestRequestApprovalToolMaterializesVisibleContinuationWithCapabilityDependency(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if _, err := rt.ConfigureAutonomy(context.Background(), 9044, 1001, "leased 15m all"); err != nil {
		t.Fatalf("ConfigureAutonomy() err = %v", err)
	}
	if _, err := rt.ConfigureAutoApproval(context.Background(), 9044, 1001, "15m all"); err != nil {
		t.Fatalf("ConfigureAutoApproval() err = %v", err)
	}
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-imexx-github-runtime",
		RequestedBy:    "telegram:1001",
		RequestedFor:   "telegram:1001",
		Kind:           session.CapabilityKindExternalAccount,
		TargetResource: "github:imexx/processes",
		Purpose:        "Push Imexx process scaffold after approval.",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)

	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-imexx-request-approval",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-imexx-request-approval", Goal: "Ship Imexx scaffold safely"},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}
	out, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		json.RawMessage(`{
			"objective":"Make approval cards first-class.",
			"phase":{
				"id":"phase-request-approval-runtime",
				"summary":"Prepare Imexx process scaffold",
				"authority_class":"workspace_write",
				"why_now":"The operator approved a narrow scaffold-preparation phase that depends on GitHub access metadata.",
				"bounded_effect":"Prepare only the non-secret Imexx process scaffold and stop before commit, deploy, restart, or unrelated external effects.",
				"allowed_actions":["edit_files","run_tests"],
				"forbidden_actions":["commit","deploy","restart_service","external_send_or_contact"],
				"validation_plan":["request_approval materializes visible buttons"],
				"required_capability_grants":[{
					"request_id":"cap-imexx-github-runtime",
					"kind":"external_account",
					"target_resource":"github:imexx/processes",
					"granted_to":"telegram:1001",
					"allowed_actions":["contents:write","pull_requests:write"]
				}]
			}
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval requested marker", out)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "show approval buttons", MessageID: 77},
		"show approval buttons",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want visible approval prompt")
	}

	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	var labels []string
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
		labels = continuationButtonLabels(sender.inline[0].rows)
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
	}
	for _, want := range []string{"Prepare Imexx process scaffold", "external_account", "github:imexx/processes", "cap-imexx-github-runtime", "contents:write"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	if got, want := labels, []string{"Start", "Details", "Change", "Pause", "Stop"}; !equalStringSlices(got, want) {
		t.Fatalf("inline labels = %#v, want %#v", got, want)
	}

	cont, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if cont.Status != session.ContinuationStatusPending || cont.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending manual lease", cont)
	}
	if cont.ActionProposal.AutoApproveEligible == nil || *cont.ActionProposal.AutoApproveEligible {
		t.Fatalf("autoapprove_eligible = %#v, want manual button-backed request", cont.ActionProposal.AutoApproveEligible)
	}
	if cont.ActionProposal.RiskClass != "workspace_write" || !actionListContains(cont.ActionProposal.AllowedActions, "edit_files") {
		t.Fatalf("action proposal = %#v, want workspace_write edit_files", cont.ActionProposal)
	}
	if len(cont.ContinuationLease.RequiredCapabilityGrants) != 1 {
		t.Fatalf("continuation lease grants = %#v, want required capability grant preserved", cont.ContinuationLease.RequiredCapabilityGrants)
	}
	grantSpec := cont.ContinuationLease.RequiredCapabilityGrants[0]
	if grantSpec.RequestID != "cap-imexx-github-runtime" || grantSpec.TargetResource != "github:imexx/processes" || !actionListContains(grantSpec.AllowedActions, "contents:write") {
		t.Fatalf("grant spec = %#v, want Imexx GitHub dependency", grantSpec)
	}
	leases, err := store.ActiveOperatorAutoApprovalLeases(9044, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(leases) != 1 || leases[0].UsedCount != 0 {
		t.Fatalf("autoapproval leases = %#v, want visible request to bypass autoapproval", leases)
	}
}

func TestRequestApprovalContinuationLeaseRequestMaterializesAndApprovesExactChildWakeLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)

	key := session.SessionKey{ChatID: 9044, UserID: 0, Scope: telegramDMScopeRef(9044)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-child-wake-request",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-child-wake-request", Goal: "Recover child wake readiness"},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}

	out, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		json.RawMessage(`{
			"action":"request_continuation_lease",
			"objective":"Wake idolum-email exactly once to consume pending parent guidance.",
			"lease_class":"child_wake",
			"principal":"telegram:1001",
			"allowed_actions":["wake_named_child"],
			"constraints":{"agent_id":"idolum-email"},
			"tool":"durable_agent",
			"tool_action":"wake_once",
			"grant_id":"grant-idolum-email-direct-no-content-wake-readiness",
			"grant_target_resource":"durable_agent:idolum-email:wake_once",
			"request_instance_id":"test-idolum-email-wake-request-1",
			"agent_id":"idolum-email",
			"retry_after_lease":true
		}`),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval child_wake lease request) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval requested marker", out)
	}

	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(pending) err = %v", err)
	}
	if pending.Status != session.ContinuationStatusPending || pending.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("pending continuation = %#v, want pending lease", pending)
	}
	if pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake {
		t.Fatalf("pending lease class = %q, want child_wake", pending.ContinuationLease.LeaseClass)
	}
	if got := strings.TrimSpace(pending.ContinuationLease.Constraints["agent_id"]); got != "idolum-email" {
		t.Fatalf("pending lease agent_id = %q, want idolum-email", got)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "show approval buttons", MessageID: 88},
		"show approval buttons",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want visible approval prompt")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
	}
	for _, want := range []string{"idolum-email", "wake only idolum-email once", "up to 1 turn"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}

	materialized, err = rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9044, SenderID: 1001, Text: "show approval buttons again", MessageID: 89},
		"show approval buttons again",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(second) err = %v", err)
	}
	if !materialized {
		t.Fatal("second materialized = false, want idempotent handled approval prompt")
	}
	sender.mu.Lock()
	inlineCount = len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count after second materialize = %d, want no duplicate card", inlineCount)
	}

	approved, err := rt.ApproveContinuationForKey(key, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	if approved.Status != session.ContinuationStatusApproved || approved.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("approved continuation = %#v, want active lease", approved)
	}
	if approved.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake {
		t.Fatalf("approved lease class = %q, want child_wake", approved.ContinuationLease.LeaseClass)
	}
	if !actionListContains(approved.ContinuationLease.AllowedActions, "wake_named_child") {
		t.Fatalf("approved lease allowed actions = %#v, want wake_named_child", approved.ContinuationLease.AllowedActions)
	}
	if got := strings.TrimSpace(approved.ContinuationLease.Constraints["agent_id"]); got != "idolum-email" {
		t.Fatalf("approved lease agent_id = %q, want idolum-email", got)
	}
	if approved.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("approved lease remaining turns = %d, want one wake allowance", approved.ContinuationLease.RemainingTurns)
	}
}

func TestRecoveryHandoffMaterializationCreatesChildWakeApprovalAndConsumableLease(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	runner := &runtimeWakeRunner{}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(runner)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9048, UserID: 0, Scope: telegramDMScopeRef(9048)}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-wake-recovery",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-idolum-email-wake-recovery", Goal: "Recover idolum-email readiness"},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}

	_, err = tools.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"idolum-email"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") || !strings.Contains(err.Error(), "lease request recorded") {
		t.Fatalf("wake_once err = %v, want recorded missing child_wake lease blocker", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want no wake before lease approval", runner.calls)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(blocker) err = %v", err)
	}
	if len(open) != 1 || open[0].OperationTool != "request_approval" || open[0].OperationKind != "continuation_lease_request" {
		t.Fatalf("open actions = %#v, want request_approval continuation handoff", open)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9048, SenderID: 1001, Text: "continue", MessageID: 101},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want recovery handoff to produce visible approval card")
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(pending) err = %v", err)
	}
	if pending.Status != session.ContinuationStatusPending || pending.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("pending continuation = %#v, want pending child_wake lease", pending)
	}
	if pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake || strings.TrimSpace(pending.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("pending lease = %#v, want child_wake bound to idolum-email", pending.ContinuationLease)
	}
	retry := session.NormalizeContinuationRetryOperation(pending.ContinuationLease.RetryOperation)
	if !retry.Active() || retry.Tool != "durable_agent" || retry.OperationKind != "durable_agent_wake_once" || !strings.Contains(retry.InputJSON, `"agent_id":"idolum-email"`) {
		t.Fatalf("pending retry operation = %#v, want exact durable_agent wake_once retry", retry)
	}
	open, err = store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after materialize) err = %v", err)
	}
	for _, action := range open {
		if action.SubjectKind == "continuation_lease_request" {
			t.Fatalf("open actions after materialization = %#v, want recovery blocker resolved", open)
		}
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
	}
	for _, want := range []string{"idolum-email", "wake only idolum-email once", "up to 1 turn"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}

	materialized, err = rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9048, SenderID: 1001, Text: "continue again", MessageID: 102},
		"continue again",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(second) err = %v", err)
	}
	if !materialized {
		t.Fatal("second materialized = false, want idempotent handled approval")
	}
	sender.mu.Lock()
	inlineCount = len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count after retry = %d, want no duplicate card", inlineCount)
	}

	approved, err := rt.ApproveContinuationForKey(key, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	if approved.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("approved continuation = %#v, want active child_wake lease", approved)
	}
	approvedText := approvedContinuationEventTextForState(approved)
	if !strings.Contains(approvedText, "Invoke durable_agent wake_once for idolum-email exactly once") {
		t.Fatalf("approved continuation text = %q, want executable wake_once retry", approvedText)
	}
	if strings.Contains(approvedText, "Next:\nApprove one no-content") || strings.Contains(approvedText, "request_approval") {
		t.Fatalf("approved continuation text = %q, must not ask for approval again", approvedText)
	}
	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "idolum-email" {
		t.Fatalf("runner calls = %#v, want one idolum-email wake", runner.calls)
	}
	current, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(after trigger) err = %v", err)
	}
	if current.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed || current.RemainingTurns != 0 {
		t.Fatalf("continuation after trigger = %#v, want consumed one-turn retry", current)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	for _, event := range events {
		if event.EventType == core.ExecutionEventToolStarted && strings.Contains(event.PayloadJSON, `"tool":"request_approval"`) {
			t.Fatalf("events include request_approval after child_wake approval: %#v", event)
		}
	}
}

func TestRecoveryHandoffMaterializationSupersedesStalePendingContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(&runtimeWakeRunner{})
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9051, UserID: 0, Scope: telegramDMScopeRef(9051)}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	now := time.Now().UTC()
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:      "op-stale-data-access-then-child-wake",
		Status:  session.OperationStatusActive,
		Stage:   "approval_request",
		Summary: "Prior data access approval is waiting.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	stale := runtimePendingContinuationState("stale-data-access", session.ContinuationLeaseClassDataAccess, now)
	if err := store.UpdateContinuationState(key, stale); err != nil {
		t.Fatalf("UpdateContinuationState(stale) err = %v", err)
	}
	if err := store.RecordTelegramCallbackMessage(9051, 77, 0, continuationCallbackSurface, now); err != nil {
		t.Fatalf("RecordTelegramCallbackMessage(stale) err = %v", err)
	}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")

	_, err = tools.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"idolum-email"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") {
		t.Fatalf("wake_once err = %v, want child_wake blocker", err)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9051, SenderID: 1001, Text: "continue", MessageID: 401},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want stale pending projection adjudicated into child_wake approval")
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		pending.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		strings.TrimSpace(pending.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("pending continuation = %#v, want child_wake approval for idolum-email", pending)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.SubjectKind == "continuation_lease_request" {
			t.Fatalf("open next actions = %#v, want child_wake handoff resolved", open)
		}
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	retiredCount := len(sender.editClear)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one new approval card", inlineCount)
	}
	if retiredCount == 0 {
		t.Fatal("retired card count = 0, want stale approval card retired")
	}
	events, err := store.ExecutionEventsBySession(key, 0, 300)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventContinuationAdjudicated, "stale_pending_superseded") {
		t.Fatalf("events = %#v, want stale_pending_superseded adjudication", events)
	}
}

func TestDirectRequestApprovalConflictDoesNotSupersedePendingContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, _, _ := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	key := session.SessionKey{ChatID: 9052, UserID: 0, Scope: telegramDMScopeRef(9052)}
	stale := runtimePendingContinuationState("direct-stale-data-access", session.ContinuationLeaseClassDataAccess, time.Now().UTC())
	if err := store.UpdateContinuationState(key, stale); err != nil {
		t.Fatalf("UpdateContinuationState(stale) err = %v", err)
	}
	_, err = tools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		json.RawMessage(runtimeChildWakeApprovalRequestJSON("child-alpha", "direct-child-alpha-request-1")),
	)
	var conflict toolpkg.RequestApprovalContinuationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("request_approval err = %v, want typed continuation conflict", err)
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.ID != stale.ContinuationLease.ID ||
		got.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		got.ActionProposal.Status != session.ProposalStatusPending {
		t.Fatalf("continuation after direct conflict = %#v, want stale pending unchanged", got)
	}
}

func TestRecoveryHandoffMaterializationBlocksOnActiveIncompatibleContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9053, UserID: 0, Scope: telegramDMScopeRef(9053)}
	active := runtimePendingContinuationState("active-data-access", session.ContinuationLeaseClassDataAccess, time.Now().UTC())
	active.Status = session.ContinuationStatusApproved
	active.ActionProposal.Status = session.ProposalStatusApproved
	active.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	active.ContinuationLease.ApprovedBy = 1001
	active.ContinuationLease.ApprovedAt = time.Now().UTC()
	if err := store.UpdateContinuationState(key, active); err != nil {
		t.Fatalf("UpdateContinuationState(active) err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-active-conflict-child-wake-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "child_wake:child-alpha",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON("child-alpha", "active-conflict-child-alpha-request-1"),
	}); err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9053, SenderID: 1001, Text: "continue", MessageID: 501},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want live conflict handled as typed blocker")
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.ID != active.ContinuationLease.ID || got.ContinuationLease.Status != session.ContinuationLeaseStatusActive {
		t.Fatalf("continuation = %#v, want active authority unchanged", got)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	foundConflict := false
	for _, action := range open {
		if action.RecordID == "runtime-active-conflict-child-wake-handoff" {
			t.Fatalf("open next actions = %#v, want original handoff resolved", open)
		}
		if action.SubjectKind == "continuation_approval_conflict" && action.ResourceBlocker == "live_continuation_conflict" {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Fatalf("open next actions = %#v, want live continuation conflict blocker", open)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no replacement approval card for active authority", inlineCount)
	}
}

func TestRecoveryHandoffMaterializationDoesNotSupersedeNewerPendingContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9054, UserID: 0, Scope: telegramDMScopeRef(9054)}
	actionCreated := time.Now().UTC()
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-older-child-wake-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "child_wake:child-beta",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON("child-beta", "older-child-beta-request-1"),
		CreatedAt:          actionCreated,
	}); err != nil {
		t.Fatalf("RecordNextAction() err = %v", err)
	}
	newerPending := runtimePendingContinuationState("newer-data-access", session.ContinuationLeaseClassDataAccess, actionCreated.Add(time.Second))
	if err := store.UpdateContinuationState(key, newerPending); err != nil {
		t.Fatalf("UpdateContinuationState(newerPending) err = %v", err)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9054, SenderID: 1001, Text: "continue", MessageID: 601},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want older handoff handled as typed conflict")
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.ID != newerPending.ContinuationLease.ID || got.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want newer pending approval unchanged", got)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	foundConflict := false
	for _, action := range open {
		if action.RecordID == "runtime-older-child-wake-handoff" {
			t.Fatalf("open next actions = %#v, want older handoff resolved", open)
		}
		if action.SubjectKind == "continuation_approval_conflict" && action.ResourceBlocker == "live_continuation_conflict" {
			foundConflict = true
		}
	}
	if !foundConflict {
		t.Fatalf("open next actions = %#v, want conflict blocker for older handoff", open)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no replacement approval card for older handoff", inlineCount)
	}
}

func TestRecoveryHandoffMaterializationScansPastOlderBlockedHandoff(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9055, UserID: 0, Scope: telegramDMScopeRef(9055)}
	t0 := time.Now().UTC()
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-older-non-superseding-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "child_wake:older-child",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request older child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON("older-child", "older-child-request-1"),
		CreatedAt:          t0,
	}); err != nil {
		t.Fatalf("RecordNextAction(older) err = %v", err)
	}
	current := runtimePendingContinuationState("queue-current-data-access", session.ContinuationLeaseClassDataAccess, t0.Add(time.Second))
	if err := store.UpdateContinuationState(key, current); err != nil {
		t.Fatalf("UpdateContinuationState(current) err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-newer-superseding-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "child_wake:newer-child",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request newer child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON("newer-child", "newer-child-request-1"),
		CreatedAt:          t0.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("RecordNextAction(newer) err = %v", err)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9055, SenderID: 1001, Text: "continue", MessageID: 701},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want newer handoff to win after older conflict is deferred")
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(got.ContinuationLease.Constraints["agent_id"]) != "newer-child" ||
		got.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation = %#v, want pending child_wake approval for newer-child", got)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == "runtime-older-non-superseding-handoff" || action.RecordID == "runtime-newer-superseding-handoff" {
			t.Fatalf("open actions = %#v, want both handoffs resolved", open)
		}
		if action.SubjectKind == "continuation_approval_conflict" {
			t.Fatalf("open actions = %#v, did not want conflict blocker when newer handoff materialized", open)
		}
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card for newer handoff", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 300)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventContinuationAdjudicated, "superseded_by_later_recovery_handoff") {
		t.Fatalf("events = %#v, want older handoff superseded by later recovery handoff", events)
	}
}

func TestRecoveryHandoffMaterializationConsumesDataAccessApprovalRequest(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9049, UserID: 0, Scope: telegramDMScopeRef(9049)}
	raw := `{
		"action":"request_continuation_lease",
		"objective":"Read the approved child-local runtime-bin directory once.",
		"lease_class":"data_access",
		"principal":"telegram:1001",
		"allowed_actions":["read_approved_resource"],
		"constraints":{
			"capability_kind":"file_access",
			"grant_id":"capg-runtime-bin-read",
			"grant_target_resource":"/child/runtime-bin",
			"operation":"list_dir",
			"resource":"/child/runtime-bin",
			"target_resource":"/child/runtime-bin",
			"tool":"list_dir",
			"tool_action":"list_dir"
		},
		"tool":"list_dir",
		"tool_action":"list_dir",
		"grant_id":"capg-runtime-bin-read",
		"grant_target_resource":"/child/runtime-bin",
		"request_instance_id":"test-runtime-bin-read-request-1",
		"resource":"/child/runtime-bin",
		"recovery_contract":"aphelion.recovery_handoff.v1",
		"recovery_operation_kind":"continuation_lease_request",
		"retry_after_lease":true
	}`
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-data-access-recovery-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "data_access:/child/runtime-bin:list_dir",
		RequiredAuthority:  string(session.ContinuationLeaseClassDataAccess),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "approve a bounded data_access lease",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: raw,
	}); err != nil {
		t.Fatalf("RecordNextAction(data access handoff) err = %v", err)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9049, SenderID: 1001, Text: "continue", MessageID: 201},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(data access) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want data_access approval card")
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(data access) err = %v", err)
	}
	if pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassDataAccess || pending.ContinuationLease.Constraints["grant_id"] != "capg-runtime-bin-read" {
		t.Fatalf("pending data access lease = %#v, want exact grant-bound data_access lease", pending.ContinuationLease)
	}
}

func TestRecoveryHandoffMaterializationSkipsMalformedApprovalNextAction(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9050, UserID: 0, Scope: telegramDMScopeRef(9050)}
	now := time.Now().UTC()
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-malformed-recovery-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "malformed",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "malformed handoff",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: `{"action":"request_continuation_lease","lease_class":"child_wake","request_instance_id":"bad"}`,
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(malformed handoff) err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:          "runtime-valid-recovery-handoff-behind-malformed",
		Key:               key,
		Owner:             "test",
		State:             session.NextActionBlockedNeedsAuthority,
		SubjectKind:       "continuation_lease_request",
		SubjectRef:        "valid-child-wake",
		RequiredAuthority: string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:   "missing_continuation_lease",
		NextAction:        "valid handoff",
		OperationKind:     "continuation_lease_request",
		OperationTool:     "request_approval",
		OperationInputJSON: `{
			"action":"request_continuation_lease",
			"objective":"Wake child-alpha exactly once.",
			"lease_class":"child_wake",
			"principal":"telegram:1001",
			"allowed_actions":["wake_named_child"],
			"constraints":{"agent_id":"child-alpha"},
			"tool":"durable_agent",
			"tool_action":"wake_once",
			"grant_id":"grant-child-alpha-wake",
			"grant_target_resource":"durable_agent:child-alpha:wake_once",
			"request_instance_id":"valid-child-alpha-request-1",
			"agent_id":"child-alpha",
			"recovery_contract":"aphelion.recovery_handoff.v1",
			"recovery_operation_kind":"continuation_lease_request",
			"retry_after_lease":true
		}`,
		CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("RecordNextAction(valid handoff) err = %v", err)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9050, SenderID: 1001, Text: "continue", MessageID: 301},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(malformed) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want valid recovery handoff behind malformed row to materialize")
	}
	if state, ok, err := store.ContinuationStateIfExists(key); err != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", err)
	} else if !ok || session.NormalizeContinuationState(state).Status != session.ContinuationStatusPending {
		t.Fatalf("continuation state = %#v ok=%v, want pending continuation created from valid handoff", state, ok)
	} else if state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake || strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "child-alpha" {
		t.Fatalf("continuation lease = %#v, want child_wake bound to child-alpha", state.ContinuationLease)
	}
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(malformed) err = %v", err)
	}
	foundMalformed := false
	foundValid := false
	for _, action := range open {
		if action.RecordID == "runtime-malformed-recovery-handoff" {
			foundMalformed = true
		}
		if action.RecordID == "runtime-valid-recovery-handoff-behind-malformed" {
			foundValid = true
		}
	}
	if foundMalformed || foundValid {
		t.Fatalf("open actions = %#v, want malformed terminalized and valid resolved", open)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card from valid handoff", inlineCount)
	}
}

func TestRequestApprovalMaterializeRetriesAfterFailedDelivery(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	key := session.SessionKey{ChatID: 9045, UserID: 0, Scope: telegramDMScopeRef(9045)}
	if _, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		json.RawMessage(`{
			"action":"request_continuation_lease",
			"objective":"Wake child-alpha exactly once.",
			"lease_class":"child_wake",
			"principal":"telegram:1001",
			"allowed_actions":["wake_named_child"],
			"constraints":{"agent_id":"child-alpha"},
			"tool":"durable_agent",
			"tool_action":"wake_once",
			"grant_id":"grant-child-alpha-wake",
			"grant_target_resource":"durable_agent:child-alpha:wake_once",
			"request_instance_id":"test-child-alpha-delivery-retry-request-1",
			"agent_id":"child-alpha",
			"retry_after_lease":true
		}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval) err = %v", err)
	}

	sender.inlineErr = errors.New("telegram transient delivery failure")
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9045, SenderID: 1001, Text: "continue", MessageID: 1},
		"continue",
	)
	if err == nil || !strings.Contains(err.Error(), "telegram transient delivery failure") {
		t.Fatalf("first MaterializeRequestedApproval err = %v, want delivery failure", err)
	}
	if materialized {
		t.Fatal("first materialized = true, want failed delivery to report false")
	}
	if deliveredContinuationOfferCount(t, store, key) != 0 {
		t.Fatalf("delivered offers = %d, want none after failed send", deliveredContinuationOfferCount(t, store, key))
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count after failed send = %d, want 0", inlineCount)
	}

	sender.inlineErr = nil
	materialized, err = rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9045, SenderID: 1001, Text: "continue again", MessageID: 2},
		"continue again",
	)
	if err != nil {
		t.Fatalf("retry MaterializeRequestedApproval err = %v", err)
	}
	if !materialized {
		t.Fatal("retry materialized = false, want delivered card")
	}
	if deliveredContinuationOfferCount(t, store, key) != 1 {
		t.Fatalf("delivered offers = %d, want one after retry", deliveredContinuationOfferCount(t, store, key))
	}
	sender.mu.Lock()
	inlineCount = len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count after retry = %d, want 1", inlineCount)
	}

	materialized, err = rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9045, SenderID: 1001, Text: "continue third", MessageID: 3},
		"continue third",
	)
	if err != nil {
		t.Fatalf("third MaterializeRequestedApproval err = %v", err)
	}
	if !materialized {
		t.Fatal("third materialized = false, want idempotent handled state")
	}
	sender.mu.Lock()
	inlineCount = len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count after delivered retry = %d, want no duplicate", inlineCount)
	}
}

func TestRequestApprovalSameContractNewInstanceAfterConsumedDeliversNewCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	key := session.SessionKey{ChatID: 9046, UserID: 0, Scope: telegramDMScopeRef(9046)}
	base := `{
		"action":"request_continuation_lease",
		"objective":"Wake child-alpha exactly once.",
		"lease_class":"child_wake",
		"principal":"telegram:1001",
		"allowed_actions":["wake_named_child"],
		"constraints":{"agent_id":"child-alpha"},
		"tool":"durable_agent",
		"tool_action":"wake_once",
		"grant_id":"grant-child-alpha-wake",
		"grant_target_resource":"durable_agent:child-alpha:wake_once",
		"request_instance_id":"REQUEST_INSTANCE",
		"agent_id":"child-alpha",
		"retry_after_lease":true
	}`
	firstInput := json.RawMessage(strings.ReplaceAll(base, "REQUEST_INSTANCE", "repeat-contract-instance-1"))
	if _, err := tools.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "request_approval", firstInput); err != nil {
		t.Fatalf("first request_approval err = %v", err)
	}
	if materialized, err := rt.MaterializeRequestedApproval(context.Background(), key, core.InboundMessage{ChatID: 9046, SenderID: 1001, Text: "continue", MessageID: 1}, "continue"); err != nil || !materialized {
		t.Fatalf("first MaterializeRequestedApproval materialized=%v err=%v, want delivered", materialized, err)
	}
	first, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(first) err = %v", err)
	}
	first.Status = session.ContinuationStatusApproved
	first.RemainingTurns = 0
	first.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	first.ContinuationLease.RemainingTurns = 0
	if err := store.UpdateContinuationState(key, first); err != nil {
		t.Fatalf("UpdateContinuationState(consumed first) err = %v", err)
	}

	secondInput := json.RawMessage(strings.ReplaceAll(base, "REQUEST_INSTANCE", "repeat-contract-instance-2"))
	if _, err := tools.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "request_approval", secondInput); err != nil {
		t.Fatalf("second request_approval err = %v", err)
	}
	second, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(second) err = %v", err)
	}
	if second.ContinuationLease.ID == first.ContinuationLease.ID || second.DecisionID == first.DecisionID {
		t.Fatalf("second continuation = %#v reused first consumed identity %#v", second, first)
	}
	if second.ContinuationLease.PlanHash != first.ContinuationLease.PlanHash {
		t.Fatalf("second contract hash = %q, want same contract hash as first %q", second.ContinuationLease.PlanHash, first.ContinuationLease.PlanHash)
	}
	if materialized, err := rt.MaterializeRequestedApproval(context.Background(), key, core.InboundMessage{ChatID: 9046, SenderID: 1001, Text: "continue second", MessageID: 2}, "continue second"); err != nil || !materialized {
		t.Fatalf("second MaterializeRequestedApproval materialized=%v err=%v, want delivered", materialized, err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 2 {
		t.Fatalf("inline count = %d, want one delivered card per request instance", inlineCount)
	}
	if deliveredContinuationOfferCount(t, store, key) != 2 {
		t.Fatalf("delivered offers = %d, want two distinct delivered request instances", deliveredContinuationOfferCount(t, store, key))
	}

	if materialized, err := rt.MaterializeRequestedApproval(context.Background(), key, core.InboundMessage{ChatID: 9046, SenderID: 1001, Text: "continue second again", MessageID: 3}, "continue second again"); err != nil || !materialized {
		t.Fatalf("second retry MaterializeRequestedApproval materialized=%v err=%v, want idempotent handled", materialized, err)
	}
	sender.mu.Lock()
	inlineCount = len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 2 {
		t.Fatalf("inline count after second retry = %d, want no duplicate for same request instance", inlineCount)
	}
}

func TestRequestApprovalSameContractNewInstanceAfterDeniedDeliversNewCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
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
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store)
	key := session.SessionKey{ChatID: 9047, UserID: 0, Scope: telegramDMScopeRef(9047)}
	base := `{
		"action":"request_continuation_lease",
		"objective":"Wake child-beta exactly once.",
		"lease_class":"child_wake",
		"principal":"telegram:1001",
		"allowed_actions":["wake_named_child"],
		"constraints":{"agent_id":"child-beta"},
		"tool":"durable_agent",
		"tool_action":"wake_once",
		"grant_id":"grant-child-beta-wake",
		"grant_target_resource":"durable_agent:child-beta:wake_once",
		"request_instance_id":"REQUEST_INSTANCE",
		"agent_id":"child-beta",
		"retry_after_lease":true
	}`
	firstInput := json.RawMessage(strings.ReplaceAll(base, "REQUEST_INSTANCE", "repeat-contract-denied-instance-1"))
	if _, err := tools.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "request_approval", firstInput); err != nil {
		t.Fatalf("first request_approval err = %v", err)
	}
	if materialized, err := rt.MaterializeRequestedApproval(context.Background(), key, core.InboundMessage{ChatID: 9047, SenderID: 1001, Text: "continue", MessageID: 1}, "continue"); err != nil || !materialized {
		t.Fatalf("first MaterializeRequestedApproval materialized=%v err=%v, want delivered", materialized, err)
	}
	first, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(first) err = %v", err)
	}
	first.Status = session.ContinuationStatusRevoked
	first.ActionProposal.Status = session.ProposalStatusDenied
	first.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
	first.RemainingTurns = 0
	first.ContinuationLease.RemainingTurns = 0
	if err := store.UpdateContinuationState(key, first); err != nil {
		t.Fatalf("UpdateContinuationState(denied first) err = %v", err)
	}

	if _, err := tools.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "request_approval", firstInput); err != nil {
		t.Fatalf("same denied request_approval replay err = %v", err)
	}
	replayed, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(replayed denied) err = %v", err)
	}
	if replayed.ContinuationLease.Status != session.ContinuationLeaseStatusRevoked || replayed.ActionProposal.Status != session.ProposalStatusDenied {
		t.Fatalf("replayed denied continuation = %#v, want terminal denial preserved", replayed)
	}
	replayedOp, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState(replayed denied) err = %v", err)
	}
	if replayedOp.Status != session.OperationStatusBlocked || replayedOp.Stage != "approval_revoked" || replayedOp.Proposal.Status != session.ProposalStatusDenied {
		t.Fatalf("replayed denied operation = %#v, want denied projection without pending rewind", replayedOp)
	}

	secondInput := json.RawMessage(strings.ReplaceAll(base, "REQUEST_INSTANCE", "repeat-contract-denied-instance-2"))
	if _, err := tools.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, key, "request_approval", secondInput); err != nil {
		t.Fatalf("second request_approval err = %v", err)
	}
	second, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(second) err = %v", err)
	}
	if second.ContinuationLease.ID == first.ContinuationLease.ID || second.DecisionID == first.DecisionID {
		t.Fatalf("second continuation = %#v reused first denied identity %#v", second, first)
	}
	if second.ContinuationLease.PlanHash != first.ContinuationLease.PlanHash {
		t.Fatalf("second contract hash = %q, want same contract hash as first %q", second.ContinuationLease.PlanHash, first.ContinuationLease.PlanHash)
	}
	if materialized, err := rt.MaterializeRequestedApproval(context.Background(), key, core.InboundMessage{ChatID: 9047, SenderID: 1001, Text: "continue second", MessageID: 2}, "continue second"); err != nil || !materialized {
		t.Fatalf("second MaterializeRequestedApproval materialized=%v err=%v, want delivered", materialized, err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 2 {
		t.Fatalf("inline count = %d, want one delivered card per request instance", inlineCount)
	}
	if deliveredContinuationOfferCount(t, store, key) != 2 {
		t.Fatalf("delivered offers = %d, want two distinct delivered request instances", deliveredContinuationOfferCount(t, store, key))
	}
}

func runtimePendingContinuationState(token string, class session.ContinuationLeaseClass, now time.Time) session.ContinuationState {
	token = strings.TrimSpace(token)
	if token == "" {
		token = "runtime-pending"
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	proposal := session.ActionProposal{
		ID:             "aprop-" + token,
		Summary:        "Approve " + string(class) + " continuation",
		BoundedEffect:  "Permit one bounded " + string(class) + " continuation.",
		RiskClass:      string(class),
		AllowedActions: []string{"read_approved_resource"},
		Status:         session.ProposalStatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	lease := session.ContinuationLease{
		ID:             "lease-" + token,
		ProposalID:     proposal.ID,
		Status:         session.ContinuationLeaseStatusPending,
		MaxTurns:       1,
		RemainingTurns: 1,
		LeaseClass:     class,
		Constraints:    map[string]string{"resource": "/child/runtime-bin"},
		AllowedActions: []string{"read_approved_resource"},
		PlanHash:       "plan-" + token,
		ExpiresAt:      now.Add(30 * time.Minute),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return session.NormalizeContinuationState(session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusPending,
		DecisionID:        "decision-" + token,
		Objective:         proposal.Summary,
		StageSummary:      proposal.Summary,
		RemainingTurns:    1,
		ActionProposal:    proposal,
		ContinuationLease: lease,
		UpdatedAt:         now,
	})
}

func runtimeChildWakeApprovalRequestJSON(agentID string, requestInstanceID string) string {
	agentID = strings.TrimSpace(agentID)
	requestInstanceID = strings.TrimSpace(requestInstanceID)
	return `{
		"action":"request_continuation_lease",
		"objective":"Wake ` + agentID + ` exactly once.",
		"lease_class":"child_wake",
		"principal":"telegram:1001",
		"allowed_actions":["wake_named_child"],
		"constraints":{"agent_id":"` + agentID + `"},
		"tool":"durable_agent",
		"tool_action":"wake_once",
		"grant_id":"grant-` + agentID + `-wake",
		"grant_target_resource":"durable_agent:` + agentID + `:wake_once",
		"request_instance_id":"` + requestInstanceID + `",
		"agent_id":"` + agentID + `",
		"recovery_contract":"aphelion.recovery_handoff.v1",
		"recovery_operation_kind":"continuation_lease_request",
		"retry_after_lease":true
	}`
}

func deliveredContinuationOfferCount(t *testing.T, store *session.SQLiteStore, key session.SessionKey) int {
	t.Helper()

	events, err := store.ExecutionEventsBySession(key, 0, 1000)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	count := 0
	for _, event := range events {
		if strings.TrimSpace(event.EventType) == core.ExecutionEventContinuationOffered && strings.TrimSpace(event.Status) == "delivered" {
			count++
		}
	}
	return count
}

type runtimeWakeRunner struct {
	calls      []string
	messageIDs [][]string
}

func (r *runtimeWakeRunner) RunDurableAgentParentConversationWake(_ context.Context, agentID string, messageIDs []string, _ time.Time) error {
	r.calls = append(r.calls, agentID)
	r.messageIDs = append(r.messageIDs, append([]string(nil), messageIDs...))
	return nil
}

func seedRuntimeWakeAgent(t *testing.T, store *session.SQLiteStore, agentID string, withParentMessage bool) {
	t.Helper()

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            agentID,
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		BootstrapLLM: core.NodeLLMBootstrap{
			Backend:        "native",
			NativeProvider: "anthropic",
			APIKey:         "test-key",
			Model:          "test-model",
		},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Consume parent guidance when explicitly woken.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
		}),
		Status: "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent(%s) err = %v", agentID, err)
	}
	if !withParentMessage {
		return
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Run a no-content readiness check.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState(%s) err = %v", agentID, err)
	}
}

func seedRuntimeWakeGrant(t *testing.T, store *session.SQLiteStore, agentID string, grantedTo string) session.CapabilityGrant {
	t.Helper()

	agentID = strings.TrimSpace(agentID)
	now := time.Now().UTC()
	grant, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "grant-" + agentID + "-wake-once",
		GrantedBy:      "telegram:1001",
		GrantedTo:      strings.TrimSpace(grantedTo),
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "durable_agent:" + agentID + ":wake_once",
		AllowedActions: []string{"invoke"},
		Contract:       `{"bounded_effect":"Allow invoking durable_agent wake_once for the named child only."}`,
		Constraints:    `{"agent_id":"` + agentID + `"}`,
		Status:         session.CapabilityGrantStatusActive,
		GrantedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertCapabilityGrant(wake_once) err = %v", err)
	}
	return grant
}

func runtimeContinuationAuthorityContext(t *testing.T, store *session.SQLiteStore, key session.SessionKey, actor principal.Principal, lease session.ContinuationLease) context.Context {
	t.Helper()

	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "recovery handoff continuation execution")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	principalID := "telegram:1001"
	if actor.TelegramUserID != 0 {
		principalID = "telegram:" + strconv.FormatInt(actor.TelegramUserID, 10)
	}
	_, err = store.UpsertExecutionRunAuthority(session.ExecutionRunAuthority{
		TurnRunID:           run.ID,
		SessionID:           run.SessionID,
		ChatID:              run.ChatID,
		UserID:              run.UserID,
		Scope:               run.Scope,
		Principal:           principalID,
		PrincipalRole:       string(actor.Role),
		ExecutionSpecies:    "recovery_handoff_runtime_test",
		LeaseKind:           session.ExecutionAuthorityLeaseKindContinuation,
		ContinuationLeaseID: strings.TrimSpace(lease.ID),
		LeaseStatus:         string(lease.Status),
		LeaseRemainingTurns: lease.RemainingTurns,
		LeaseClass:          lease.LeaseClass,
		LeaseAllowedActions: append([]string(nil), lease.AllowedActions...),
		LeaseConstraints:    cloneRuntimeTestStringMap(lease.Constraints),
		LeaseExpiresAt:      lease.ExpiresAt,
		AdmittedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("UpsertExecutionRunAuthority() err = %v", err)
	}
	return toolpkg.WithAuthorityUseRef(context.Background(), session.AuthorityUseRef{SessionID: run.SessionID, TurnRunID: run.ID})
}

func cloneRuntimeTestStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
