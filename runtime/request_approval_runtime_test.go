//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
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
