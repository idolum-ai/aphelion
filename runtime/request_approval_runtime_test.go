//go:build linux

package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
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
		json.RawMessage(runtimeChildWakeApprovalRequestJSON(t, store, key, "idolum-email", "test-idolum-email-wake-request-1")),
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

func TestOperationPhaseChildWakeCompilesRecoveryContractAndRunsApprovedRetry(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9058, UserID: 0, Scope: telegramDMScopeRef(9058)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-phase-wake",
		Objective: "Recover idolum-email readiness through an approved child wake.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-idolum-email-phase-wake",
			Goal:           "Recover idolum-email readiness.",
			CurrentPhaseID: "phase-child-wake-idolum-email",
			Phases: []session.OperationPhase{{
				ID:               "phase-child-wake-idolum-email",
				Summary:          "Wake idolum-email exactly once to consume pending parent guidance.",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "child_wake",
				GateLevel:        "escalated_operator_approval",
				GateReasonCode:   "child_wake",
				WhyNow:           "The child has pending parent guidance and needs one bounded wake.",
				BoundedEffect:    "Invoke durable_agent wake_once for idolum-email exactly once.",
				AllowedActions:   []string{"wake_named_child"},
				ForbiddenActions: []string{"wake_unnamed_child", "unbounded_retry_loop", "access_mailbox_contents", "read_secret_values"},
				ValidationPlan:   []string{"verify one child wake result or typed pre-child failure"},
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					GrantID:        grant.GrantID,
					Kind:           session.CapabilityKindGenericDelegation,
					TargetResource: grant.TargetResource,
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"invoke"},
					Constraints:    `{"agent_id":"idolum-email"}`,
				}},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed phase) err = %v", err)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9058, SenderID: 1001, Text: "continue", MessageID: 1},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(phase child_wake) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want child_wake phase approval")
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(pending phase) err = %v", err)
	}
	if pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		pending.ContinuationLease.RecoveryContractID == "" ||
		pending.ContinuationLease.Constraints["agent_id"] != "idolum-email" {
		t.Fatalf("pending phase continuation = %#v, want child_wake lease with recovery contract for idolum-email", pending)
	}
	contract, ok, err := store.ContinuationRecoveryContract(pending.ContinuationLease.RecoveryContractID)
	if err != nil {
		t.Fatalf("ContinuationRecoveryContract(%q) err = %v", pending.ContinuationLease.RecoveryContractID, err)
	}
	if !ok || contract.AgentID != "idolum-email" || contract.GrantID != grant.GrantID || !contract.RetryOperation.Active() {
		t.Fatalf("phase recovery contract = %#v ok=%v, want retryable child_wake contract", contract, ok)
	}
	if retry := session.NormalizeContinuationRetryOperation(pending.ContinuationLease.RetryOperation); !retry.Active() || retry.Tool != "durable_agent" || !strings.Contains(retry.InputJSON, `"agent_id":"idolum-email"`) || !strings.Contains(retry.InputJSON, "Wake idolum-email exactly once to consume pending parent guidance.") {
		t.Fatalf("phase retry operation = %#v, want durable_agent wake_once retry", retry)
	}

	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey(phase child_wake) err = %v", err)
	}
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       9058,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    2,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(phase approved retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(phase approved retry) result = %#v, want approved continuation acknowledgement", result)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "idolum-email" {
		t.Fatalf("runner calls = %#v, want one idolum-email wake from phase retry", runner.calls)
	}
}

func TestRequestApprovalChildWakePhaseInfersExactWakeGrantFromDurableFacts(t *testing.T) {
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
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9076, UserID: 0, Scope: telegramDMScopeRef(9076)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-freeform-approval",
		Objective: "Finish idolum-email setup.",
		Status:    session.OperationStatusBlocked,
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}
	input := map[string]any{
		"objective": "Finish idolum-email setup.",
		"phase": map[string]any{
			"id":                "phase-idolum-email-one-wake",
			"summary":           "Approve one bounded idolum-email readiness wake.",
			"authority_class":   "child_wake",
			"gate_reason_code":  "child_wake",
			"why_now":           "idolum-email has pending parent guidance and needs one bounded wake.",
			"bounded_effect":    "Invoke durable_agent wake_once for idolum-email exactly once and stop after one child result or typed blocker.",
			"allowed_actions":   []string{"wake_named_child"},
			"forbidden_actions": []string{"wake_unnamed_child", "unbounded_retry_loop", "credentials_or_tokens", "mailbox_content"},
			"validation_plan":   []string{"verify one child wake result or typed pre-child failure"},
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal(request_approval input) err = %v", err)
	}
	out, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		raw,
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval inferred child_wake phase) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval marker", out)
	}

	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if pending.Status != session.ContinuationStatusPending || pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake {
		t.Fatalf("pending continuation = %#v, want pending child_wake continuation", pending)
	}
	if got := strings.TrimSpace(pending.ContinuationLease.Constraints["agent_id"]); got != "idolum-email" {
		t.Fatalf("pending agent_id = %q, want idolum-email", got)
	}
	contract, ok, err := store.ContinuationRecoveryContract(pending.ContinuationLease.RecoveryContractID)
	if err != nil {
		t.Fatalf("ContinuationRecoveryContract() err = %v", err)
	}
	if !ok {
		t.Fatalf("ContinuationRecoveryContract(%q) not found", pending.ContinuationLease.RecoveryContractID)
	}
	if contract.AgentID != "idolum-email" || contract.GrantID != grant.GrantID || contract.GrantTargetResource != grant.TargetResource {
		t.Fatalf("contract = %#v, want inferred exact child/grant target", contract)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9076, SenderID: 1001, Text: "show the approval card", MessageID: 1},
		"show the approval card",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want card")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one card", inlineCount)
	}
	if !strings.Contains(inlineText, "idolum-email") || !strings.Contains(inlineText, "wake only idolum-email once") {
		t.Fatalf("inline text = %q, want exact child wake card", inlineText)
	}
}

func TestRequestApprovalChildWakePhaseInferenceRequiresActiveWakeGrant(t *testing.T) {
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
	setFakeBubblewrapRunnerForRegistry(t, tools)
	key := session.SessionKey{ChatID: 9077, UserID: 0, Scope: telegramDMScopeRef(9077)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-no-grant",
		Objective: "Finish idolum-email setup.",
		Status:    session.OperationStatusBlocked,
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"objective": "Finish idolum-email setup.",
		"phase": map[string]any{
			"id":               "phase-idolum-email-no-grant",
			"summary":          "Approve one bounded idolum-email readiness wake.",
			"authority_class":  "child_wake",
			"gate_reason_code": "child_wake",
			"bounded_effect":   "Invoke durable_agent wake_once for idolum-email exactly once.",
			"allowed_actions":  []string{"wake_named_child"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal(request_approval input) err = %v", err)
	}
	if _, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		raw,
	); err == nil || !strings.Contains(err.Error(), "requires an active exact durable_agent wake_once grant") {
		t.Fatalf("ExecuteForSessionPrincipal() err = %v, want active exact grant requirement", err)
	}
}

func TestRequestApprovalChildWakePhaseDoesNotReuseClaimedParentPhaseLease(t *testing.T) {
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

	key := session.SessionKey{ChatID: 9074, UserID: 0, Scope: telegramDMScopeRef(9074)}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-gog-smoke-test",
		Objective: "Create a fresh bounded approval for one idolum-email child_wake.",
		Status:    session.OperationStatusActive,
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}

	now := time.Now().UTC()
	parentPhaseID := "phase-idolum-email-gog-smoke-test-idolum-email-fresh-readonly-host-report-wake-v1"
	parentLeaseID := "lease-" + parentPhaseID
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusIdle,
		DecisionID:     parentPhaseID,
		Objective:      "Create a fresh bounded approval for one idolum-email child_wake.",
		StageSummary:   "Create a fresh bounded approval.",
		RemainingTurns: 0,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-" + parentPhaseID,
			Summary:        "Create a fresh bounded approval.",
			RiskClass:      "capability_grant",
			AllowedActions: []string{"request_approval"},
			Status:         session.ProposalStatusApproved,
			PlanHash:       "parent-phase-plan-hash",
			ExpiresAt:      now.Add(time.Hour),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             parentLeaseID,
			ProposalID:     "aprop-" + parentPhaseID,
			Status:         session.ContinuationLeaseStatusConsumed,
			MaxTurns:       1,
			RemainingTurns: 0,
			ApprovedBy:     1001,
			AllowedActions: []string{"request_approval"},
			LeaseClass:     session.ContinuationLeaseClassCapabilityGrant,
			PlanHash:       "parent-phase-plan-hash",
			ApprovedAt:     now,
			ExpiresAt:      now.Add(time.Hour),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}); err != nil {
		t.Fatalf("UpdateContinuationState(parent consumed phase) err = %v", err)
	}
	parentRun, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "parent request_approval phase turn")
	if err != nil {
		t.Fatalf("BeginTurnRun(parent) err = %v", err)
	}
	if _, err := store.UpsertExecutionRunAuthority(session.ExecutionRunAuthority{
		TurnRunID:           parentRun.ID,
		SessionID:           parentRun.SessionID,
		ChatID:              parentRun.ChatID,
		UserID:              parentRun.UserID,
		Scope:               parentRun.Scope,
		Principal:           "telegram:1001",
		PrincipalRole:       string(principal.RoleAdmin),
		ExecutionSpecies:    "direct_continuation",
		LeaseKind:           session.ExecutionAuthorityLeaseKindContinuation,
		ContinuationLeaseID: parentLeaseID,
		LeaseStatus:         string(session.ContinuationLeaseStatusActive),
		LeaseRemainingTurns: 1,
		LeaseClass:          session.ContinuationLeaseClassCapabilityGrant,
		LeaseAllowedActions: []string{"request_approval"},
		LeaseExpiresAt:      now.Add(time.Hour),
		AdmittedAt:          now,
	}); err != nil {
		t.Fatalf("UpsertExecutionRunAuthority(parent claimed lease) err = %v", err)
	}
	if err := store.CompleteTurnRun(parentRun.ID, session.TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun(parent) err = %v", err)
	}

	phaseRequest := fmt.Sprintf(`{
		"objective":"Create a fresh bounded approval for one idolum-email child_wake.",
		"phase":{
			"id":"%s",
			"summary":"Invoke durable_agent wake_once for idolum-email exactly once.",
			"authority_class":"child_wake",
			"why_now":"The child has pending parent guidance and needs one bounded wake.",
			"bounded_effect":"Invoke durable_agent wake_once for idolum-email exactly once; stop after one result or typed blocker.",
			"allowed_actions":["wake_named_child"],
			"forbidden_actions":["wake_unnamed_child","unbounded_retry_loop","access_mailbox_contents","read_secret_values"],
			"validation_plan":["verify one child wake result or typed pre-child failure"],
			"required_capability_grants":[{
				"grant_id":%q,
				"kind":"generic_delegation",
				"target_resource":%q,
				"granted_to":"telegram:1001",
				"allowed_actions":["invoke"],
				"constraints":{"agent_id":"idolum-email"}
			}]
		}
	}`, parentPhaseID, grant.GrantID, grant.TargetResource)
	toolCtx := toolpkg.WithToolInvocationRef(context.Background(), toolpkg.ToolInvocationRef{
		TurnRunID:    parentRun.ID,
		InvocationID: fmt.Sprintf("turn:%d:tool:request_approval", parentRun.ID),
	})
	out, err := tools.ExecuteForSessionPrincipal(
		toolCtx,
		principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		key,
		"request_approval",
		json.RawMessage(phaseRequest),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_approval child_wake phase) err = %v", err)
	}
	if !strings.Contains(out, "[APPROVAL_REQUESTED]") {
		t.Fatalf("request_approval output = %q, want approval requested", out)
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(child_wake pending) err = %v", err)
	}
	if pending.Status != session.ContinuationStatusPending ||
		pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		pending.ContinuationLease.RecoveryContractID == "" ||
		strings.TrimSpace(pending.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("pending continuation = %#v, want contract-backed child_wake approval", pending)
	}
	if pending.ContinuationLease.ID == parentLeaseID {
		t.Fatalf("child_wake lease id reused claimed parent lease %q", parentLeaseID)
	}

	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey(child_wake phase request) err = %v", err)
	}
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       9074,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    2,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(approved child_wake retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(approved child_wake retry) result = %#v, want running acknowledgement", result)
	}
	if got := fmt.Sprint(runner.calls); got != "[idolum-email]" {
		t.Fatalf("wake runner calls = %s, want [idolum-email]", got)
	}
}

func TestOperationPhaseChildWakeMaterializesGuidanceBeforeApprovedRetry(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9061, UserID: 0, Scope: telegramDMScopeRef(9061)}
	seedRuntimeWakeAgent(t, store, "idolum-email", false)
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	const guidance = "Ask idolum-email to generate recommended jobs from already-available child-local context only, or report blocker."
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-inline-phase-wake",
		Objective: "Ask idolum-email to generate recommended jobs from child-local context.",
		Status:    session.OperationStatusBlocked,
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-idolum-email-inline-phase-wake",
			Goal:           "Run one bounded child-local planning wake.",
			CurrentPhaseID: "phase-child-wake-inline-idolum-email",
			Phases: []session.OperationPhase{{
				ID:               "phase-child-wake-inline-idolum-email",
				Summary:          guidance,
				Status:           session.PlanStatusPending,
				AuthorityClass:   "child_wake",
				GateLevel:        "escalated_operator_approval",
				GateReasonCode:   "child_wake",
				WhyNow:           "The child can use existing local context without mailbox access.",
				BoundedEffect:    "Invoke durable_agent wake_once for idolum-email exactly once with the phase guidance as pending parent work.",
				AllowedActions:   []string{"wake_named_child"},
				ForbiddenActions: []string{"unbounded_retry_loop", "access_mailbox_contents", "read_secret_values"},
				RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
					GrantID:        grant.GrantID,
					Kind:           session.CapabilityKindGenericDelegation,
					TargetResource: grant.TargetResource,
					GrantedTo:      "telegram:1001",
					AllowedActions: []string{"invoke"},
					Constraints:    `{"agent_id":"idolum-email"}`,
				}},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed phase) err = %v", err)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9061, SenderID: 1001, Text: "please continue", MessageID: 1},
		"please continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(phase child_wake) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want child_wake phase approval")
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(pending phase) err = %v", err)
	}
	retry := session.NormalizeContinuationRetryOperation(pending.ContinuationLease.RetryOperation)
	if !retry.Active() || !strings.Contains(retry.InputJSON, guidance) {
		t.Fatalf("phase retry operation = %#v, want phase guidance", retry)
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       9061,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    2,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(phase approved retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(phase approved retry) result = %#v, want approved continuation acknowledgement", result)
	}
	if got := fmt.Sprint(runner.calls); got != "[idolum-email]" {
		t.Fatalf("wake runner calls = %s, want [idolum-email]", got)
	}
	if len(runner.messageIDs) != 1 || len(runner.messageIDs[0]) != 1 || strings.TrimSpace(runner.messageIDs[0][0]) == "" {
		t.Fatalf("wake runner message IDs = %#v, want claimed inline guidance batch", runner.messageIDs)
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
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       9048,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    103,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(continue approved retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(continue approved retry) result = %#v, want approved continuation dispatch acknowledgement", result)
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

func TestApprovedChildWakeRetryFailureBlocksForRepair(t *testing.T) {
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
	runner := &runtimeWakeRunner{err: errors.New("child wake runtime unavailable after authority admission")}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, time.Second, resolver).WithSessionStore(store).WithDurableAgentWakeRunner(runner)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9068, UserID: 0, Scope: telegramDMScopeRef(9068)}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	seedRuntimeWakeAgent(t, store, "idolum-email", true)
	grant := seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-wake-failure-recovery",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-idolum-email-wake-failure-recovery", Goal: "Recover idolum-email readiness"},
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
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") {
		t.Fatalf("wake_once err = %v, want recorded missing child_wake lease blocker", err)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9068, SenderID: 1001, Text: "continue", MessageID: 301},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want child_wake approval card")
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       9068,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    302,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(continue approved retry) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(continue approved retry) result = %#v, want approved continuation dispatch acknowledgement", result)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "idolum-email" {
		t.Fatalf("runner calls = %#v, want one idolum-email wake attempt", runner.calls)
	}
	current, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(after failed retry) err = %v", err)
	}
	if current.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed || current.RemainingTurns != 0 {
		t.Fatalf("continuation after failed retry = %#v, want consumed one-turn lease", current)
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState(after failed retry) err = %v", err)
	}
	if opState.Status != session.OperationStatusBlocked || opState.Stage != "approved_retry_failed" {
		t.Fatalf("operation state after failed retry = %#v, want blocked approved_retry_failed", opState)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after failed retry) err = %v", err)
	}
	foundRepair := false
	for _, action := range open {
		if action.Owner == "approved_retry" &&
			action.State == session.NextActionBlockedNeedsResourceRepair &&
			strings.TrimSpace(action.ResourceBlocker) != "" &&
			action.OperationKind == session.NextActionOperationKindDurableChildRecovery &&
			strings.Contains(action.OperatorProjection, "ran with authority") {
			foundRepair = true
			break
		}
	}
	if !foundRepair {
		t.Fatalf("open actions after failed retry = %#v, want approved_retry resource repair blocker", open)
	}
	invocations, err := store.CapabilityInvocationsByGrant(grant.GrantID, 10)
	if err != nil {
		t.Fatalf("CapabilityInvocationsByGrant(%q) err = %v", grant.GrantID, err)
	}
	if len(invocations) != 1 || invocations[0].OutcomeStatus != "failed" || strings.TrimSpace(invocations[0].OutcomeErrorText) == "" {
		t.Fatalf("capability invocations = %#v, want failed wake outcome evidence with safe class", invocations)
	}
}

func TestMigratedLegacyRecoveryHandoffMaterializesApproval(t *testing.T) {
	t.Parallel()

	cfg, seedStore, provider, sender := buildRuntimeFixtures(t)
	key := session.SessionKey{ChatID: 9058, UserID: 0, Scope: telegramDMScopeRef(9058)}
	legacyRaw := `{
		"action":"request_continuation_lease",
		"lease_class":"child_wake",
		"principal":"telegram:1001",
		"allowed_actions":["wake_named_child"],
		"constraints":{"agent_id":"child-migrated"},
		"tool":"durable_agent",
		"tool_action":"wake_once",
		"grant_id":"grant-child-migrated-wake",
		"grant_target_resource":"durable_agent:child-migrated:wake_once",
		"request_instance_id":"runtime-migrated-child-request-1",
		"agent_id":"child-migrated",
		"recovery_contract":"aphelion.recovery_handoff.v1",
		"recovery_operation_kind":"continuation_lease_request",
		"retry_after_lease":true
	}`
	if _, err := seedStore.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-migrated-legacy-child-wake-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         "child_wake:child-migrated",
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request migrated child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: legacyRaw,
		CreatedAt:          time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("RecordNextAction(legacy handoff) err = %v", err)
	}
	dbPath := seedStore.DBPath()
	if err := seedStore.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db for v82 marker: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_version`); err != nil {
		t.Fatalf("delete schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES (82)`); err != nil {
		t.Fatalf("insert v82 schema marker: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v82 marker db: %v", err)
	}

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v82 migrated) err = %v", err)
	}
	defer store.Close()
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(migrated) err = %v", err)
	}
	if len(open) != 1 || open[0].RecordID != "runtime-migrated-legacy-child-wake-handoff" {
		t.Fatalf("open migrated actions = %#v, want migrated legacy handoff", open)
	}
	var pointer map[string]any
	if err := json.Unmarshal([]byte(open[0].OperationInputJSON), &pointer); err != nil {
		t.Fatalf("unmarshal migrated operation input: %v", err)
	}
	recoveryContract, _ := pointer["recovery_contract"].(string)
	recoveryOperationKind, _ := pointer["recovery_operation_kind"].(string)
	contractID, _ := pointer["contract_id"].(string)
	if strings.TrimSpace(recoveryContract) != "aphelion.recovery_handoff.v1" ||
		strings.TrimSpace(recoveryOperationKind) != "continuation_lease_request" ||
		strings.TrimSpace(contractID) == "" {
		t.Fatalf("migrated operation input = %#v, want executable contract-pointer handoff", pointer)
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
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9058, SenderID: 1001, Text: "continue", MessageID: 801},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(migrated) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want migrated contract-pointer handoff to produce approval card")
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(migrated) err = %v", err)
	}
	if state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		state.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "child-migrated" {
		t.Fatalf("continuation state = %#v, want pending child_wake approval for migrated child", state)
	}
	open, err = store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after materialized) err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == "runtime-migrated-legacy-child-wake-handoff" {
			t.Fatalf("open actions after materialization = %#v, want migrated handoff resolved", open)
		}
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(migrated) err = %v", err)
	}
	if hasExecutionEventPayload(events, core.ExecutionEventWorkflowNextState, "invalid_recovery_handoff") {
		t.Fatalf("events = %#v, did not want migrated handoff invalidated", events)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventWorkflowNextState, "recovery_handoff_materialized") {
		t.Fatalf("events = %#v, want migrated handoff materialized event", events)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card", inlineCount)
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
		json.RawMessage(runtimeChildWakeApprovalRequestJSON(t, store, key, "child-alpha", "direct-child-alpha-request-1")),
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
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "child-alpha", "active-conflict-child-alpha-request-1"),
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
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "child-beta", "grant-child-beta-wake", "durable_agent", "wake_once", ""),
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "child-beta", "older-child-beta-request-1"),
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
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "older-child", "grant-older-child-wake", "durable_agent", "wake_once", ""),
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request older child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "older-child", "older-child-request-1"),
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
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "newer-child", "grant-newer-child-wake", "durable_agent", "wake_once", ""),
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "request newer child wake approval",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "newer-child", "newer-child-request-1"),
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
	raw := runtimeDataAccessApprovalRequestJSON(t, store, key, "test-runtime-bin-read-request-1", "capg-runtime-bin-read", "/child/runtime-bin", "list_dir")
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
		RecordID:           "runtime-valid-recovery-handoff-behind-malformed",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "valid handoff",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "child-alpha", "valid-child-alpha-request-1"),
		CreatedAt:          now.Add(time.Second),
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

func TestRecoveryHandoffMaterializationRejectsWrongSessionContractAndContinues(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9056, UserID: 0, Scope: telegramDMScopeRef(9056)}
	otherKey := session.SessionKey{ChatID: 9057, UserID: 0, Scope: telegramDMScopeRef(9057)}
	now := time.Now().UTC()
	wrongSessionSubject := session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "child-beta", "grant-child-beta-wake", "durable_agent", "wake_once", "")
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-wrong-session-recovery-handoff",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         wrongSessionSubject,
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "wrong-session handoff",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, otherKey, "child-beta", "wrong-session-child-beta-request-1"),
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(wrong-session handoff) err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           "runtime-valid-recovery-handoff-behind-wrong-session",
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, "child-alpha", "grant-child-alpha-wake", "durable_agent", "wake_once", ""),
		RequiredAuthority:  string(session.ContinuationLeaseClassChildWake),
		ResourceBlocker:    "missing_continuation_lease",
		NextAction:         "valid handoff",
		OperationKind:      "continuation_lease_request",
		OperationTool:      "request_approval",
		OperationInputJSON: runtimeChildWakeApprovalRequestJSON(t, store, key, "child-alpha", "valid-after-wrong-session-request-1"),
		CreatedAt:          now.Add(time.Second),
	}); err != nil {
		t.Fatalf("RecordNextAction(valid handoff) err = %v", err)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9056, SenderID: 1001, Text: "continue", MessageID: 321},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(wrong-session) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want valid same-session handoff behind wrong-session row to materialize")
	}
	if state, ok, err := store.ContinuationStateIfExists(key); err != nil {
		t.Fatalf("ContinuationStateIfExists() err = %v", err)
	} else if !ok || state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake || strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "child-alpha" {
		t.Fatalf("continuation state = %#v ok=%v, want child-alpha approval from same-session contract", state, ok)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == "runtime-wrong-session-recovery-handoff" || action.RecordID == "runtime-valid-recovery-handoff-behind-wrong-session" {
			t.Fatalf("open actions = %#v, want wrong-session invalidated and valid handoff resolved", open)
		}
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card from valid same-session handoff", inlineCount)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 300)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventWorkflowNextState, "invalid_continuation_recovery_contract") {
		t.Fatalf("events = %#v, want wrong-session contract terminalized as invalid", events)
	}
}

func TestRecoveryHandoffMaterializationResolvesExpiredAuthorityBundleAndContinues(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9058, UserID: 0, Scope: telegramDMScopeRef(9058)}
	expiredRecordID := seedExpiredAuthorityBundleHandoff(t, store, key, "expired-only", time.Now().UTC().Add(-2*time.Hour))

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9058, SenderID: 1001, Text: "go for it", MessageID: 341},
		"go for it",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(expired authority bundle) err = %v", err)
	}
	if materialized {
		t.Fatal("materialized = true, want expired handoff resolved and ordinary turn path left open")
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == expiredRecordID {
			t.Fatalf("open actions = %#v, want expired authority-bundle handoff resolved", open)
		}
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 0 {
		t.Fatalf("inline count = %d, want no approval card for expired bundle", inlineCount)
	}
}

func TestRecoveryHandoffMaterializationWhenUserReturnsAfterExpiredAuthorityBundle(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9059, UserID: 0, Scope: telegramDMScopeRef(9059)}
	expiredRecordID := seedExpiredAuthorityBundleHandoff(t, store, key, "expired-before-valid", time.Now().UTC().Add(-2*time.Hour))
	validBundleID := seedGrantOnlyAuthorityBundleHandoff(t, store, key, "valid-behind-expired")

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9059, SenderID: 1001, Text: "go for it", MessageID: 342},
		"go for it",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(expired then valid authority bundle) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want valid authority bundle behind expired handoff to materialize")
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	validRecordID := session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", validBundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC())
	for _, action := range open {
		if action.RecordID == expiredRecordID || action.RecordID == validRecordID {
			t.Fatalf("open actions = %#v, want expired handoff resolved and valid handoff materialized", open)
		}
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card for valid bundle behind expired handoff", inlineCount)
	}
}

func TestAuthorityBundleMaterializationAllowsScopedBranchPush(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9061, UserID: 0, Scope: telegramDMScopeRef(9061)}
	now := time.Now().UTC()
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "scoped-branch-push-authority-bundle",
		SessionID:         session.SessionIDForKey(key),
		Principal:         "telegram:1001",
		Objective:         "Commit and push the validated Aphelion PR fix to the existing branch.",
		Summary:           "Exact bundle: commit only tool/capability.go capability materialization fix and push to origin/fix/child-wake-authority-context.",
		AllowedActions: []string{
			"run git diff --check on tool/capability.go",
			"stage only tool/capability.go",
			"create one local commit for the capability materialization fix",
			"mint/use configured GitHub App credentials without printing tokens",
			"push to origin/fix/child-wake-authority-context",
			"report commit hash and push result",
		},
		ForbiddenActions: []string{
			"stage/commit unrelated files",
			"edit additional files",
			"force-push",
			"push to another branch or repository",
			"deploy/restart",
			"merge PR",
			"release/tag",
			"print credentials/tokens/keys",
		},
		StopConditions: []string{"stop after one commit and push result or typed blocker"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        "grant-scoped-branch-push",
			Kind:           session.CapabilityKindExternalAccount,
			TargetResource: "github:idolum-ai/aphelion",
			GrantedTo:      "telegram:1001",
			AllowedActions: []string{"write"},
			Contract:       `{"bounded_effect":"Commit and push only the scoped branch fix."}`,
			Constraints:    `{"repo":"idolum-ai/aphelion","branch":"fix/child-wake-authority-context","files":["tool/capability.go"],"no_force_push":true,"credential_output":false}`,
		}},
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(scoped branch push) err = %v", err)
	}
	bundle, err = store.UpsertAuthorityBundleContract(bundle)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract(scoped branch push) err = %v", err)
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundle.BundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput(scoped branch push) err = %v", err)
	}
	recordID := session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", bundle.BundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC())
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundle.BundleID,
		CausalRefs:         []string{"authority_bundle:" + bundle.BundleID},
		NextAction:         "review the bounded authority bundle and approve only if the allowed, forbidden, and stop boundaries match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review scoped branch push authority bundle.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(scoped branch push authority bundle) err = %v", err)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9061, SenderID: 1001, Text: "approved", MessageID: 343},
		"approved",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(scoped branch push) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want approval card for scoped branch push bundle")
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == recordID {
			t.Fatalf("open actions = %#v, want scoped branch push handoff materialized", open)
		}
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending || state.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation state = %#v, want pending approval", state)
	}
	if len(state.ContinuationLease.RequiredCapabilityGrants) != 1 {
		t.Fatalf("required grants = %#v, want scoped branch-push grant preserved on pending lease", state.ContinuationLease.RequiredCapabilityGrants)
	}
	pendingGrant := state.ContinuationLease.RequiredCapabilityGrants[0]
	if pendingGrant.GrantID != "grant-scoped-branch-push" ||
		pendingGrant.Kind != session.CapabilityKindExternalAccount ||
		pendingGrant.TargetResource != "github:idolum-ai/aphelion" ||
		pendingGrant.GrantedTo != "telegram:1001" ||
		!actionListContains(pendingGrant.AllowedActions, "write") ||
		!strings.Contains(pendingGrant.Constraints, `"branch":"fix/child-wake-authority-context"`) ||
		!strings.Contains(pendingGrant.Constraints, `"no_force_push":true`) {
		t.Fatalf("pending required grant = %#v, want exact scoped branch-push GitHub grant", pendingGrant)
	}
	if _, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github:idolum-ai/aphelion", "telegram:1001", "write"); err != nil || ok {
		t.Fatalf("ActiveCapabilityGrant(before approval) ok=%t err=%v, want no active GitHub grant from card materialization alone", ok, err)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d, want one approval card for scoped branch push bundle", inlineCount)
	}

	approved, err := rt.ApproveContinuationForKey(key, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuationForKey(scoped branch push) err = %v", err)
	}
	if approved.Status != session.ContinuationStatusApproved ||
		approved.ContinuationLease.Status != session.ContinuationLeaseStatusActive ||
		!stringSliceContains(approved.ContinuationLease.CapabilityGrantIDs, "grant-scoped-branch-push") {
		t.Fatalf("approved continuation = %#v, want active exact scoped branch-push grant id", approved)
	}
	grant, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github:idolum-ai/aphelion", "telegram:1001", "write")
	if err != nil {
		t.Fatalf("ActiveCapabilityGrant(after approval) err = %v", err)
	}
	if !ok || grant.GrantID != "grant-scoped-branch-push" ||
		!strings.Contains(grant.Constraints, `"branch":"fix/child-wake-authority-context"`) ||
		!strings.Contains(grant.Constraints, `"no_force_push":true`) ||
		strings.Contains(grant.Constraints, `"force_push":true`) {
		t.Fatalf("active grant ok=%v grant=%#v, want exact non-force scoped branch-push grant", ok, grant)
	}
	if broad, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindExternalAccount, "github", "telegram:1001", "write"); err != nil || ok || strings.TrimSpace(broad.GrantID) != "" {
		t.Fatalf("broad ActiveCapabilityGrant() ok=%t grant=%#v err=%v, want no broad GitHub write grant", ok, broad, err)
	}
}

func TestHandleInboundToolCreatedAuthorityBundleApprovalSurfacesCard(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	key := session.SessionKey{ChatID: 9063, UserID: 0, Scope: telegramDMScopeRef(9063)}
	bundle := seedGrantOnlyAuthorityBundleContract(t, store, key, "tool-created-authority-bundle-card")
	provider := &requestApprovalToolCallProvider{
		input: json.RawMessage(fmt.Sprintf(`{"action":"request_authority_bundle","contract_id":%q}`, bundle.BundleID)),
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
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     key.ChatID,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "approved",
		MessageID:  346,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil {
		t.Fatal("HandleInbound() result = nil")
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		state.ActionProposal.RiskClass != "authority_bundle" ||
		state.ContinuationLease.Constraints["authority_bundle_id"] != bundle.BundleID {
		t.Fatalf("continuation state = %#v, want pending authority-bundle approval for %s", state, bundle.BundleID)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[inlineCount-1].text
	}
	sender.mu.Unlock()
	if countApprovalCardInlineMessages(sender, "Grant-only authority bundle") != 1 {
		t.Fatalf("inline count = %d text=%q result=%q, want one approval card for tool-created authority bundle", inlineCount, inlineText, result.Text)
	}
	if !strings.Contains(inlineText, "Approve:") ||
		!strings.Contains(inlineText, bundle.Summary) ||
		!strings.Contains(inlineText, "Requires:") {
		t.Fatalf("inline text = %q, want authority bundle approval card for %q", inlineText, bundle.Summary)
	}
	if deliveredContinuationOfferCount(t, store, key) != 1 {
		t.Fatalf("delivered offers = %d, want one delivered offer for tool-created approval", deliveredContinuationOfferCount(t, store, key))
	}
}

func TestApprovedContinuationToolCreatedAuthorityBundleApprovalSurfacesCard(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	key := session.SessionKey{ChatID: 9064, UserID: 0, Scope: telegramDMScopeRef(9064)}
	bundle := seedGrantOnlyAuthorityBundleContract(t, store, key, "approved-continuation-authority-bundle-card")
	provider := &requestApprovalToolCallProvider{
		input: json.RawMessage(fmt.Sprintf(`{"action":"request_authority_bundle","contract_id":%q}`, bundle.BundleID)),
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
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-approved-continuation-tool-created-approval",
		Objective: "Run one approved continuation that requests follow-up authority.",
		Status:    session.OperationStatusActive,
		Stage:     "approved_continuation_running",
		Proposal: session.OperationProposal{
			ID:            "aprop-approved-continuation-tool-created-approval",
			Kind:          "capability_grant",
			Summary:       "Use the approved turn to request a bounded authority bundle.",
			BoundedEffect: "Run one approved continuation turn and stop at a new approval if required.",
			Status:        session.ProposalStatusApproved,
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	now := time.Now().UTC()
	if err := store.UpdateContinuationState(key, session.NormalizeContinuationState(session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "decision-approved-continuation-tool-created-approval",
		Objective:      "Run one approved continuation that requests follow-up authority.",
		StageSummary:   "Request a bounded authority bundle if the approved work needs it.",
		RemainingTurns: 1,
		ApprovedBy:     1001,
		ActionProposal: session.ActionProposal{
			ID:             "aprop-approved-continuation-tool-created-approval",
			Summary:        "Use the approved turn to request a bounded authority bundle.",
			BoundedEffect:  "Run one approved continuation turn and stop at a new approval if required.",
			RiskClass:      "capability_grant",
			AllowedActions: []string{"request_authority_bundle"},
			Status:         session.ProposalStatusApproved,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-approved-continuation-tool-created-approval",
			ProposalID:     "aprop-approved-continuation-tool-created-approval",
			Status:         session.ContinuationLeaseStatusActive,
			MaxTurns:       1,
			RemainingTurns: 1,
			LeaseClass:     session.ContinuationLeaseClassCapabilityGrant,
			AllowedActions: []string{"request_authority_bundle"},
			PlanHash:       "plan-approved-continuation-tool-created-approval",
			ExpiresAt:      now.Add(30 * time.Minute),
			ApprovedAt:     now,
			ApprovedBy:     1001,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		UpdatedAt: now,
	})); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		state.ActionProposal.RiskClass != "authority_bundle" ||
		state.ContinuationLease.Constraints["authority_bundle_id"] != bundle.BundleID {
		t.Fatalf("continuation state = %#v, want pending authority-bundle approval for %s", state, bundle.BundleID)
	}
	if countApprovalCardInlineMessages(sender, "Grant-only authority bundle") != 1 {
		sender.mu.Lock()
		inlineCount := len(sender.inline)
		inlineText := ""
		if inlineCount > 0 {
			inlineText = sender.inline[inlineCount-1].text
		}
		sender.mu.Unlock()
		t.Fatalf("inline count=%d last=%q, want approval card after approved continuation tool-created request", inlineCount, inlineText)
	}
	if deliveredContinuationOfferCount(t, store, key) != 1 {
		t.Fatalf("delivered offers = %d, want one delivered offer for approved-continuation tool-created approval", deliveredContinuationOfferCount(t, store, key))
	}
}

func countApprovalCardInlineMessages(sender *fakeSender, needle string) int {
	if sender == nil {
		return 0
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	count := 0
	for _, call := range sender.inline {
		if strings.Contains(call.text, "Approve:") && strings.Contains(call.text, needle) {
			count++
		}
	}
	return count
}

type requestApprovalToolCallProvider struct {
	input json.RawMessage
	calls int
}

func (p *requestApprovalToolCallProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if resp, ok := fakeInterpretationResponse(messages, "", core.TokenUsage{}); ok {
		return resp, nil
	}
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Before the main turn executes, ratify how this turn should proceed.") {
			return &agent.Response{Content: "INSPECT: no\nQUESTION: no\nANSWER: yes\nRATIFICATION: accept\nPLAN:\n- Execute the bounded approval request."}, nil
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			return &agent.Response{Content: "I retried exactly once. The approval card is now pending."}, nil
		}
	}
	for _, def := range tools {
		if def.Name == "request_approval" {
			p.calls++
			return &agent.Response{ToolCalls: []agent.ToolCall{{
				ID:    "request-approval-tool-call",
				Name:  "request_approval",
				Input: append(json.RawMessage(nil), p.input...),
			}}}, nil
		}
	}
	return &agent.Response{Content: "request_approval tool unavailable"}, nil
}

func (p *requestApprovalToolCallProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Complete(ctx, messages, tools)
}

func seedGrantOnlyAuthorityBundleContract(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requestToken string) session.AuthorityBundleContract {
	t.Helper()

	requestToken = strings.TrimSpace(requestToken)
	if requestToken == "" {
		requestToken = "grant-only-authority-bundle"
	}
	now := time.Now().UTC()
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "authority-bundle-" + requestToken,
		SessionID:         session.SessionIDForKey(key),
		Principal:         "telegram:1001",
		Objective:         "Approve one bounded grant-only authority bundle.",
		Summary:           "Grant-only authority bundle for a generic approval recovery test.",
		AllowedActions:    []string{"invoke_test_resource"},
		ForbiddenActions:  []string{"expand_authority_without_new_approval", "unbounded_retry_loop"},
		StopConditions:    []string{"stop after one approval or typed blocker"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        "grant-" + requestToken,
			Kind:           session.CapabilityKindGenericDelegation,
			TargetResource: "generic:test-resource:" + requestToken,
			GrantedTo:      "telegram:1001",
			AllowedActions: []string{"invoke"},
			Contract:       `{"bounded_effect":"Allow one generic delegated action for an authority-bundle approval test."}`,
			Constraints:    `{"scope":"authority_bundle_test"}`,
		}},
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(grant-only) err = %v", err)
	}
	bundle, err = store.UpsertAuthorityBundleContract(bundle)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract(grant-only) err = %v", err)
	}
	return bundle
}

func TestAuthorityBundleMaterializationRefreshesRevokedSameBundleState(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9062, UserID: 0, Scope: telegramDMScopeRef(9062)}
	bundleID := seedGrantOnlyAuthorityBundleHandoff(t, store, key, "refresh-revoked-same-bundle")
	if materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9062, SenderID: 1001, Text: "go for it", MessageID: 344},
		"go for it",
	); err != nil || !materialized {
		t.Fatalf("initial MaterializeRequestedApproval() materialized=%v err=%v, want card", materialized, err)
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(initial) err = %v", err)
	}
	state = session.NormalizeContinuationState(state)
	state.Status = session.ContinuationStatusRevoked
	state.RemainingTurns = 0
	state.ActionProposal.Status = session.ProposalStatusSuperseded
	state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
	state.ContinuationLease.RemainingTurns = 0
	state.ContinuationLease.RevokedAt = time.Now().UTC()
	state.HandshakeBlockedReason = "invalid_authority_contract"
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState(revoked) err = %v", err)
	}
	now := time.Now().UTC().Add(time.Second)
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput(refresh) err = %v", err)
	}
	recordID := session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", bundleID, session.NextActionBlockedNeedsAuthority, now)
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundleID,
		CausalRefs:         []string{"authority_bundle:" + bundleID},
		NextAction:         "review the bounded authority bundle and approve only if the allowed, forbidden, and stop boundaries match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review grant-only authority bundle.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(refresh) err = %v", err)
	}

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9062, SenderID: 1001, Text: "approved", MessageID: 345},
		"approved",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(refresh revoked) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want refreshed approval card for revoked same bundle state")
	}
	state, err = store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(refresh) err = %v", err)
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending || state.ContinuationLease.Status != session.ContinuationLeaseStatusPending {
		t.Fatalf("continuation state = %#v, want refreshed pending approval", state)
	}
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	for _, action := range open {
		if action.RecordID == recordID {
			t.Fatalf("open actions = %#v, want refreshed handoff resolved", open)
		}
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sender.mu.Unlock()
	if inlineCount != 2 {
		t.Fatalf("inline count = %d, want a new approval card after revoked same-bundle state", inlineCount)
	}
}

func TestAuthorityBundleMaterializationRedeliversSameBundleAfterOldOfferInstance(t *testing.T) {
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
	key := session.SessionKey{ChatID: 9065, UserID: 0, Scope: telegramDMScopeRef(9065)}
	bundleID := seedGrantOnlyAuthorityBundleHandoff(t, store, key, "redeliver-same-bundle-after-old-offer")
	if materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, Text: "show card", MessageID: 401},
		"show card",
	); err != nil || !materialized {
		t.Fatalf("first MaterializeRequestedApproval() materialized=%v err=%v, want card", materialized, err)
	}
	if countApprovalCardInlineMessages(sender, "Grant-only authority bundle") != 1 {
		t.Fatalf("approval cards = %d, want first card delivered", countApprovalCardInlineMessages(sender, "Grant-only authority bundle"))
	}
	if deliveredContinuationOfferCount(t, store, key) != 1 {
		t.Fatalf("delivered offers = %d, want first delivered offer", deliveredContinuationOfferCount(t, store, key))
	}

	now := time.Now().UTC().Add(time.Second)
	unrelated := runtimePendingContinuationState("unrelated-intervening-approval", session.ContinuationLeaseClassDataAccess, now)
	unrelated.Status = session.ContinuationStatusApproved
	unrelated.RemainingTurns = 0
	unrelated.ActionProposal.Status = session.ProposalStatusApproved
	unrelated.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	unrelated.ContinuationLease.RemainingTurns = 0
	unrelated.ContinuationLease.ConsumedAt = now
	if err := store.UpdateContinuationState(key, unrelated); err != nil {
		t.Fatalf("UpdateContinuationState(unrelated consumed) err = %v", err)
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput(redeliver) err = %v", err)
	}
	recordID := session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", bundleID, session.NextActionBlockedNeedsAuthority, now)
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundleID,
		CausalRefs:         []string{"authority_bundle:" + bundleID},
		NextAction:         "review the bounded authority bundle and approve only if the allowed, forbidden, and stop boundaries match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review grant-only authority bundle again.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(redeliver) err = %v", err)
	}

	if materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, Text: "show the new card", MessageID: 402},
		"show the new card",
	); err != nil || !materialized {
		t.Fatalf("second MaterializeRequestedApproval() materialized=%v err=%v, want card", materialized, err)
	}
	if countApprovalCardInlineMessages(sender, "Grant-only authority bundle") != 2 {
		t.Fatalf("approval cards = %d, want second card for new pending state with same bundle", countApprovalCardInlineMessages(sender, "Grant-only authority bundle"))
	}
	if deliveredContinuationOfferCount(t, store, key) != 2 {
		t.Fatalf("delivered offers = %d, want second delivered offer for new pending state", deliveredContinuationOfferCount(t, store, key))
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
		json.RawMessage(runtimeChildWakeApprovalRequestJSON(t, store, key, "child-alpha", "test-child-alpha-delivery-retry-request-1")),
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

func seedExpiredAuthorityBundleHandoff(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requestToken string, createdAt time.Time) string {
	t.Helper()

	createdAt = createdAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC().Add(-2 * time.Hour)
	}
	expiresAt := createdAt.Add(time.Hour)
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "expired-authority-bundle-" + strings.TrimSpace(requestToken),
		SessionID:         session.SessionIDForKey(key),
		Principal:         "telegram:1001",
		Objective:         "Expired authority bundle should not wedge later user input.",
		Summary:           "Expired authority bundle for recovery handoff terminalization regression.",
		AllowedActions:    []string{"invoke_expired_resource"},
		ForbiddenActions:  []string{"expand_authority_without_new_approval", "unbounded_retry_loop"},
		StopConditions:    []string{"stop after one approval or typed blocker"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        "grant-expired-" + strings.TrimSpace(requestToken),
			Kind:           session.CapabilityKindGenericDelegation,
			TargetResource: "expired authority bundle test resource",
			GrantedTo:      "telegram:1001",
			AllowedActions: []string{"invoke"},
			Contract:       `{"bounded_effect":"Expired grant-only approval should be terminalized before execution."}`,
			Constraints:    `{"scope":"expired_authority_bundle_test"}`,
		}},
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(expired) err = %v", err)
	}
	bundle, err = store.UpsertAuthorityBundleContract(bundle)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract(expired) err = %v", err)
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundle.BundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput(expired) err = %v", err)
	}
	recordID := session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", bundle.BundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC())
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           recordID,
		Key:                key,
		Owner:              "test",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundle.BundleID,
		CausalRefs:         []string{"authority_bundle:" + bundle.BundleID},
		NextAction:         "review the bounded authority bundle and approve only if still current",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Expired authority bundle should be terminalized.",
		CreatedAt:          createdAt,
	}); err != nil {
		t.Fatalf("RecordNextAction(expired authority bundle) err = %v", err)
	}
	return recordID
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
	firstInput := json.RawMessage(runtimeChildWakeApprovalRequestJSON(t, store, key, "child-alpha", "repeat-contract-instance-1"))
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

	secondInput := json.RawMessage(runtimeChildWakeApprovalRequestJSON(t, store, key, "child-alpha", "repeat-contract-instance-2"))
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
	if second.ContinuationLease.PlanHash == first.ContinuationLease.PlanHash {
		t.Fatalf("second contract hash = %q, want fresh retry-instance-bound contract hash distinct from first", second.ContinuationLease.PlanHash)
	}
	if second.ContinuationLease.LeaseClass != first.ContinuationLease.LeaseClass ||
		second.ContinuationLease.Constraints["agent_id"] != first.ContinuationLease.Constraints["agent_id"] ||
		!equalStringSlices(second.ContinuationLease.AllowedActions, first.ContinuationLease.AllowedActions) {
		t.Fatalf("second lease = %#v, want same bounded child/action shape as first %#v", second.ContinuationLease, first.ContinuationLease)
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
	firstInput := json.RawMessage(runtimeChildWakeApprovalRequestJSON(t, store, key, "child-beta", "repeat-contract-denied-instance-1"))
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

	secondInput := json.RawMessage(runtimeChildWakeApprovalRequestJSON(t, store, key, "child-beta", "repeat-contract-denied-instance-2"))
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
	if second.ContinuationLease.PlanHash == first.ContinuationLease.PlanHash {
		t.Fatalf("second contract hash = %q, want fresh retry-instance-bound contract hash distinct from first", second.ContinuationLease.PlanHash)
	}
	if second.ContinuationLease.LeaseClass != first.ContinuationLease.LeaseClass ||
		second.ContinuationLease.Constraints["agent_id"] != first.ContinuationLease.Constraints["agent_id"] ||
		!equalStringSlices(second.ContinuationLease.AllowedActions, first.ContinuationLease.AllowedActions) {
		t.Fatalf("second lease = %#v, want same bounded child/action shape as first %#v", second.ContinuationLease, first.ContinuationLease)
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

func runtimeChildWakeApprovalRequestJSON(t *testing.T, store *session.SQLiteStore, key session.SessionKey, agentID string, requestInstanceID string) string {
	t.Helper()

	agentID = strings.TrimSpace(agentID)
	requestInstanceID = strings.TrimSpace(requestInstanceID)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   requestInstanceID,
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, agentID, "grant-"+agentID+"-wake", "durable_agent", "wake_once", ""),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": agentID},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             agentID,
		GrantID:             "grant-" + agentID + "-wake",
		GrantTargetResource: "durable_agent:" + agentID + ":wake_once",
		CreatedAt:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(child_wake) err = %v", err)
	}
	contract, err = store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(child_wake) err = %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"action":                  "request_continuation_lease",
		"contract_id":             contract.ContractID,
		"objective":               "Wake " + agentID + " exactly once.",
		"recovery_contract":       "aphelion.recovery_handoff.v1",
		"recovery_operation_kind": "continuation_lease_request",
	})
	if err != nil {
		t.Fatalf("marshal child_wake approval request err = %v", err)
	}
	return string(raw)
}

func runtimeDataAccessApprovalRequestJSON(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requestInstanceID string, grantID string, resource string, toolName string) string {
	t.Helper()

	requestInstanceID = strings.TrimSpace(requestInstanceID)
	grantID = strings.TrimSpace(grantID)
	resource = strings.TrimSpace(resource)
	toolName = strings.TrimSpace(toolName)
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   requestInstanceID,
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassDataAccess, "", grantID, toolName, toolName, resource),
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassDataAccess,
		AllowedActions:      []string{"read_approved_resource"},
		Constraints:         map[string]string{"capability_kind": string(session.CapabilityKindFileAccess), "grant_id": grantID, "grant_target_resource": resource, "operation": toolName, "resource": resource, "target_resource": resource, "tool": toolName, "tool_action": toolName},
		Tool:                toolName,
		ToolAction:          toolName,
		GrantID:             grantID,
		GrantTargetResource: resource,
		Resource:            resource,
		CreatedAt:           time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(data_access) err = %v", err)
	}
	contract, err = store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(data_access) err = %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"action":                  "request_continuation_lease",
		"contract_id":             contract.ContractID,
		"objective":               "Read the approved resource once.",
		"recovery_contract":       "aphelion.recovery_handoff.v1",
		"recovery_operation_kind": "continuation_lease_request",
	})
	if err != nil {
		t.Fatalf("marshal data_access approval request err = %v", err)
	}
	return string(raw)
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
	err        error
}

func (r *runtimeWakeRunner) RunDurableAgentParentConversationWake(_ context.Context, agentID string, messageIDs []string, _ string, _ time.Time) error {
	r.calls = append(r.calls, agentID)
	r.messageIDs = append(r.messageIDs, append([]string(nil), messageIDs...))
	return r.err
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
