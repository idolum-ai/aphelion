//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

type durableWakeAuthorityBundleRequestingProvider struct {
	mu        sync.Mutex
	requested bool
}

func (p *durableWakeAuthorityBundleRequestingProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if resp, ok := fakeInterpretationResponse(messages, "", core.TokenUsage{}); ok {
		return resp, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.requested && durableWakeProviderHasTool(tools, "authority_bundle") {
		p.requested = true
		return &agent.Response{ToolCalls: []agent.ToolCall{{
			ID:   "child-authority-bundle",
			Name: "authority_bundle",
			Input: json.RawMessage(`{
				"action":"propose",
				"request_instance_id":"child-bundle-live-shape-1",
				"objective":"Finish one bounded email opportunity report cycle.",
				"summary":"Use child-local gog_cli read/search metadata to produce one report and stop.",
				"allowed_actions":["wake_named_child","gog_cli_search","gog_cli_read_metadata","rank_opportunities","produce_report"],
				"forbidden_actions":["credential_or_token_output","send_email","delete_email","unbounded_retry_loop","deploy_or_restart"],
				"stop_conditions":["stop after one report","stop on any typed blocker requiring new authority"],
				"required_capability_grants":[{
					"grant_id":"grant-idolum-email-gog-cli-read-search",
					"kind":"tool",
					"target_resource":"gog_cli:job-search-mailbox",
					"granted_to":"durable_agent:idolum-email",
					"allowed_actions":["invoke","search","read","metadata"],
					"contract":"{\"bounded_effect\":\"Allow idolum-email to search/read metadata needed for one opportunity report.\"}",
					"constraints":"{\"account\":\"job-search-mailbox\",\"content_scope\":\"unread_job_opportunities\"}"
				}]
			}`),
		}}}, nil
	}
	return &agent.Response{Content: "I need the bounded gog_cli grant before I can produce the report.\nREVIEW_STATUS: blocked"}, nil
}

func (p *durableWakeAuthorityBundleRequestingProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Complete(ctx, messages, tools)
}

func durableWakeProviderHasTool(tools []agent.ToolDef, name string) bool {
	for _, def := range tools {
		if strings.TrimSpace(def.Name) == name {
			return true
		}
	}
	return false
}

func TestChildAuthoredAuthorityBundleIsPromotedToParentApprovalCard(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &durableWakeAuthorityBundleRequestingProvider{}
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
	rt.durableWakeChild = nil
	tools.WithDurableAgentWakeRunner(rt)

	agentID := "idolum-email"
	parentKey := session.SessionKey{ChatID: 1001, UserID: 1001, Scope: telegramDMScopeRef(1001)}
	seedRealChildWakeAgent(t, store, agentID)
	seedRuntimeWakeGrant(t, store, agentID, "telegram:1001")
	seedNaturalChildWakeContinuation(t, store, parentKey, agentID)
	if err := store.UpdateOperationState(parentKey, session.OperationState{
		ID:        "op-idolum-email-authority-bundle-promotion",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-idolum-email-authority-bundle-promotion", Goal: "Finish idolum-email setup and report"},
	}); err != nil {
		t.Fatalf("UpdateOperationState(parent) err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), parentKey); err != nil {
		t.Fatalf("TriggerContinuationForKey(approved child wake) err = %v", err)
	}
	provider.mu.Lock()
	requested := provider.requested
	provider.mu.Unlock()
	if !requested {
		t.Fatal("child provider did not request authority_bundle")
	}

	agentRecord, err := store.DurableAgent(agentID)
	if err != nil {
		t.Fatalf("DurableAgent(%s) err = %v", agentID, err)
	}
	if agentRecord == nil {
		t.Fatalf("DurableAgent(%s) = nil", agentID)
	}
	childKey := session.SessionKey{ChatID: durableWakeSyntheticChatID(agentID), Scope: durableAgentScopeRef(*agentRecord)}
	childOpen, err := store.OpenNextActionsBySession(childKey, 50)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(child) err = %v", err)
	}
	for _, action := range childOpen {
		if action.SubjectKind == "authority_bundle_request" {
			t.Fatalf("child open actions = %#v, authority bundle request should be promoted to parent session", childOpen)
		}
	}

	parentState, err := store.ContinuationState(parentKey)
	if err != nil {
		t.Fatalf("ContinuationState(parent) err = %v", err)
	}
	if parentState.Status != session.ContinuationStatusPending ||
		parentState.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		parentState.ActionProposal.RiskClass != "authority_bundle" ||
		parentState.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		parentState.ContinuationLease.Constraints["agent_id"] != agentID {
		t.Fatalf("parent continuation = %#v, want pending authority-bundle child_wake approval", parentState)
	}
	if len(parentState.ContinuationLease.RequiredCapabilityGrants) != 1 {
		t.Fatalf("required grants = %#v, want child-requested gog_cli grant", parentState.ContinuationLease.RequiredCapabilityGrants)
	}
	grant := parentState.ContinuationLease.RequiredCapabilityGrants[0]
	if grant.Kind != session.CapabilityKindTool ||
		grant.TargetResource != "gog_cli:job-search-mailbox" ||
		grant.GrantedTo != "durable_agent:idolum-email" ||
		!stringSliceContains(grant.AllowedActions, "search") ||
		!stringSliceContains(grant.AllowedActions, "read") {
		t.Fatalf("required grant = %#v, want exact child-requested gog_cli read/search grant", grant)
	}
	if parentState.ContinuationLease.Constraints["authority_bundle_id"] == "" {
		t.Fatalf("parent lease constraints = %#v, want authority_bundle_id", parentState.ContinuationLease.Constraints)
	}
	if len(sender.inline) == 0 {
		t.Fatal("sender.inline empty, want parent approval card")
	}
	last := sender.inline[len(sender.inline)-1]
	if last.chatID != 1001 ||
		!strings.Contains(last.text, "Requires:") ||
		!strings.Contains(last.text, "gog_cli:job-search-mailbox") ||
		!strings.Contains(last.text, "Stop: stop after one report") {
		t.Fatalf("last inline = %#v, want authority bundle approval card in parent chat", last)
	}
	events, err := store.ExecutionEventsBySession(parentKey, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(parent) err = %v", err)
	}
	if !hasExecutionEventStatus(events, core.ExecutionEventWorkflowNextState, "authority_bundle_promoted") {
		t.Fatalf("parent events = %#v, want authority_bundle_promoted evidence", events)
	}
}

func hasExecutionEventStatus(events []session.ExecutionEvent, eventType string, status string) bool {
	for _, event := range events {
		if event.EventType == eventType && event.Status == status {
			return true
		}
	}
	return false
}

func TestChildAuthorityBundlePromotionLeavesParentHandoffWhenCardDeliveryFails(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	sender.inlineErr = context.Canceled
	provider := &durableWakeAuthorityBundleRequestingProvider{}
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
	rt.durableWakeChild = nil
	tools.WithDurableAgentWakeRunner(rt)

	agentID := "idolum-email"
	parentKey := session.SessionKey{ChatID: 1001, UserID: 1001, Scope: telegramDMScopeRef(1001)}
	seedRealChildWakeAgent(t, store, agentID)
	seedRuntimeWakeGrant(t, store, agentID, "telegram:1001")
	seedNaturalChildWakeContinuation(t, store, parentKey, agentID)
	if err := rt.TriggerContinuationForKey(context.Background(), parentKey); err != nil {
		t.Fatalf("TriggerContinuationForKey(approved child wake) err = %v", err)
	}

	open, err := store.OpenNextActionsBySession(parentKey, 50)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(parent) err = %v", err)
	}
	var parentBundleAction session.NextActionRecord
	for _, action := range open {
		if action.SubjectKind == "authority_bundle_request" &&
			action.OperationTool == "request_approval" &&
			action.OperationKind == "authority_bundle_request" {
			parentBundleAction = action
			break
		}
	}
	if parentBundleAction.RecordID == "" {
		t.Fatalf("open parent actions = %#v, want retryable authority bundle handoff after delivery failure", open)
	}
	if consumable, invalid := recoveryApprovalNextActionConsumable(parentBundleAction); !consumable || invalid {
		t.Fatalf("parent bundle action consumable=%v invalid=%v action=%#v, want retryable handoff", consumable, invalid, parentBundleAction)
	}
}

func TestParentMaterializationPromotesExistingChildAuthorityBundleInCurrentDMSession(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &durableWakeAuthorityBundleRequestingProvider{}
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

	agentID := "idolum-email"
	parentKey := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	seedRealChildWakeAgent(t, store, agentID)
	agentRecord, err := store.DurableAgent(agentID)
	if err != nil {
		t.Fatalf("DurableAgent(%s) err = %v", agentID, err)
	}
	if agentRecord == nil {
		t.Fatalf("DurableAgent(%s) = nil", agentID)
	}
	childKey := session.SessionKey{ChatID: durableWakeSyntheticChatID(agentID), Scope: durableAgentScopeRef(*agentRecord)}
	seedChildAuthorityBundleRequest(t, store, childKey, agentID)

	msg := core.InboundMessage{
		ChatID:     parentKey.ChatID,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "continue",
		MessageID:  42,
		Timestamp:  time.Now().UTC(),
	}
	if err := rt.materializePromotedDurableChildAuthorityBundle(context.Background(), parentKey, msg, time.Now().UTC()); err != nil {
		t.Fatalf("materializePromotedDurableChildAuthorityBundle() err = %v", err)
	}

	parentState, err := store.ContinuationState(parentKey)
	if err != nil {
		t.Fatalf("ContinuationState(parent) err = %v", err)
	}
	if parentState.Status != session.ContinuationStatusPending ||
		parentState.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		parentState.ActionProposal.RiskClass != "authority_bundle" ||
		parentState.ContinuationLease.Constraints["agent_id"] != agentID {
		t.Fatalf("parent continuation = %#v, want promoted authority-bundle approval in current DM session", parentState)
	}
	if got := session.SessionIDForKey(parentKey); parentState.ContinuationLease.RecoveryContractID == "" || got != "telegram_dm:1001" {
		t.Fatalf("parent session id = %q recovery_contract=%q, want current non-user-scoped DM session", got, parentState.ContinuationLease.RecoveryContractID)
	}
	if len(sender.inline) == 0 {
		t.Fatal("sender.inline empty, want parent approval card")
	}
	last := sender.inline[len(sender.inline)-1]
	if last.chatID != 1001 || !strings.Contains(last.text, "gog_cli:job-search-mailbox") {
		t.Fatalf("last inline = %#v, want authority bundle approval in parent DM", last)
	}
	childOpen, err := store.OpenNextActionsBySession(childKey, 50)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(child) err = %v", err)
	}
	for _, action := range childOpen {
		if action.SubjectKind == "authority_bundle_request" {
			t.Fatalf("child open actions = %#v, authority bundle request should be promoted and resolved", childOpen)
		}
	}
}

func TestHandleInboundPromotesExistingChildAuthorityBundleAfterOrdinaryReply(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.Join([]string{
		"I can continue, but the next useful move needs approval.",
		"",
		"Approve this bounded read-only repair:",
		"- inspect only child-local runtime state",
		"- repair only non-secret read-only materialization",
		"- stop after one result or typed blocker",
	}, "\n")
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
	var audit TurnAudit
	rt.SetTurnAuditSink(func(got TurnAudit) {
		audit = got
	})

	agentID := "idolum-email"
	parentKey := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	seedRealChildWakeAgent(t, store, agentID)
	agentRecord, err := store.DurableAgent(agentID)
	if err != nil {
		t.Fatalf("DurableAgent(%s) err = %v", agentID, err)
	}
	if agentRecord == nil {
		t.Fatalf("DurableAgent(%s) = nil", agentID)
	}
	childKey := session.SessionKey{ChatID: durableWakeSyntheticChatID(agentID), Scope: durableAgentScopeRef(*agentRecord)}
	seedChildAuthorityBundleRequest(t, store, childKey, agentID)

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     parentKey.ChatID,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Please continue the child setup from where it is blocked.",
		MessageID:  77,
		Timestamp:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) == 0 {
		parentOpen, _ := store.OpenNextActionsBySession(parentKey, 20)
		childOpen, _ := store.OpenNextActionsBySession(childKey, 20)
		t.Fatalf("sender.inline empty; sent=%#v audit=%#v parent_open=%#v child_open=%#v, want approval card", sender.sent, audit, parentOpen, childOpen)
	}
	last := sender.inline[len(sender.inline)-1]
	if last.chatID != 1001 || !strings.Contains(last.text, "gog_cli:job-search-mailbox") {
		t.Fatalf("last inline = %#v, want promoted child authority-bundle approval card", last)
	}
	for _, sent := range sender.sent {
		if strings.Contains(sent.Text, "Approve this bounded read-only repair") {
			t.Fatalf("sent text leaked prose approval request: %#v", sent)
		}
	}
	if len(sender.sent) == 0 || !strings.Contains(sender.sent[len(sender.sent)-1].Text, "fresh bounded approval") {
		t.Fatalf("sent = %#v, want neutral approval-surface reply", sender.sent)
	}
}

func TestPendingAuthorityBundleHandoffMaterializesThroughGenericApprovalSurface(t *testing.T) {
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

	parentKey := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	seedParentAuthorityBundleHandoff(t, store, parentKey, "idolum-email", "generic-surface")

	handled, result, err := rt.maybeHandleApprovedContinuationRunIntent(
		context.Background(),
		core.InboundMessage{
			ChatID:     parentKey.ChatID,
			ChatType:   "private",
			SenderID:   1001,
			SenderName: "admin",
			Text:       "show the approval card",
			MessageID:  90,
			Timestamp:  time.Now().UTC(),
		},
		principalAdminForTest(),
	)
	if err != nil {
		t.Fatalf("maybeHandleApprovedContinuationRunIntent() err = %v", err)
	}
	if !handled || result == nil || !strings.Contains(result.Text, "surfaced") {
		t.Fatalf("handled=%v result=%#v, want pending authority bundle approval surfaced", handled, result)
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
	for _, want := range []string{"Requires:", "idolum-email", "gog_cli:job-search-mailbox", "Stop: stop after one report"} {
		if !strings.Contains(inlineText, want) {
			t.Fatalf("inline text = %q, want %q", inlineText, want)
		}
	}
	state, err := store.ContinuationState(parentKey)
	if err != nil {
		t.Fatalf("ContinuationState(parent) err = %v", err)
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		state.ActionProposal.RiskClass != "authority_bundle" ||
		state.ContinuationLease.Constraints["agent_id"] != "idolum-email" ||
		state.ContinuationLease.Constraints["authority_bundle_id"] == "" {
		t.Fatalf("continuation state = %#v, want pending authority-bundle child_wake approval", state)
	}
	if len(state.ContinuationLease.RequiredCapabilityGrants) != 1 ||
		state.ContinuationLease.RequiredCapabilityGrants[0].TargetResource != "gog_cli:job-search-mailbox" {
		t.Fatalf("required grants = %#v, want gog_cli bundle grant", state.ContinuationLease.RequiredCapabilityGrants)
	}
}

func TestPlainApprovalTextMaterializesAuthorityBundleHandoffBeforeModelTurn(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I will need approval before I can do that."
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

	parentKey := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	bundleID := seedParentAuthorityBundleHandoff(t, store, parentKey, "idolum-email", "plain-approval")
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     parentKey.ChatID,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "I approve " + bundleID,
		MessageID:  91,
		Timestamp:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "surfaced") {
		t.Fatalf("HandleInbound result = %#v, want card-surface response before model turn", result)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	sentCount := len(sender.sent)
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline count = %d sent=%d, want one approval card", inlineCount, sentCount)
	}
}

func TestGrantOnlyAuthorityBundleHandoffMaterializesApprovalCarrier(t *testing.T) {
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

	key := session.SessionKey{ChatID: 1002, Scope: telegramDMScopeRef(1002)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-grant-only-authority-bundle",
		Status:    session.OperationStatusBlocked,
		Stage:     "authority_bundle_request",
		Objective: "Approve a grant-only bundle without a separate retry operation.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	bundleID := seedGrantOnlyAuthorityBundleHandoff(t, store, key, "generic-approval-carrier")

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: key.ChatID, ChatType: "private", SenderID: 1001, SenderName: "admin", Text: "show approval card", MessageID: 92, Timestamp: time.Now().UTC()},
		"show approval card",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval(grant-only bundle) err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want grant-only authority bundle to surface approval card")
	}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassCapabilityGrant ||
		state.ContinuationLease.Constraints["authority_bundle_id"] != bundleID ||
		state.ActionProposal.RiskClass != "authority_bundle" {
		t.Fatalf("continuation state = %#v, want pending authority-bundle capability-grant carrier", state)
	}
	if len(state.ContinuationLease.RequiredCapabilityGrants) != 1 ||
		state.ContinuationLease.RequiredCapabilityGrants[0].GrantID != "grant-generic-approval-carrier" {
		t.Fatalf("required grants = %#v, want grant-only bundle grant preserved", state.ContinuationLease.RequiredCapabilityGrants)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 || !strings.Contains(inlineText, "grant-only test resource") {
		t.Fatalf("inline count=%d text=%q, want one grant-only authority bundle approval card", inlineCount, inlineText)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if hasExecutionEventPayload(events, core.ExecutionEventWorkflowNextState, "invalid_authority_bundle_handoff") {
		t.Fatalf("events = %#v, grant-only bundle must not be terminalized as invalid", events)
	}

	approved, err := rt.ApproveContinuationForKey(key, 1001)
	if err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	if approved.Status != session.ContinuationStatusApproved ||
		approved.ContinuationLease.Status != session.ContinuationLeaseStatusActive ||
		!stringSliceContains(approved.ContinuationLease.CapabilityGrantIDs, "grant-generic-approval-carrier") {
		t.Fatalf("approved continuation = %#v, want active carrier with minted grant id", approved)
	}
	grant, ok, err := store.ActiveCapabilityGrant(session.CapabilityKindGenericDelegation, "grant-only test resource", "telegram:1001", "invoke")
	if err != nil {
		t.Fatalf("ActiveCapabilityGrant() err = %v", err)
	}
	if !ok || grant.GrantID != "grant-generic-approval-carrier" {
		t.Fatalf("active grant ok=%v grant=%#v, want minted generic delegation", ok, grant)
	}
}

func TestExplicitAuthorityBundleReferenceReopensTerminalizedGrantOnlyHandoff(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "I still need approval."
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

	key := session.SessionKey{ChatID: 1003, Scope: telegramDMScopeRef(1003)}
	bundleID := seedGrantOnlyAuthorityBundleHandoff(t, store, key, "terminalized-reference")
	open, err := store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open actions = %#v, want seeded bundle handoff", open)
	}
	if err := store.ResolveNextAction(session.NextActionResolutionInput{
		RecordID:    open[0].RecordID,
		Key:         key,
		Owner:       "runtime",
		SubjectKind: open[0].SubjectKind,
		SubjectRef:  open[0].SubjectRef,
		Reason:      "invalid_authority_bundle_handoff",
		ResolvedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("ResolveNextAction(invalid) err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     key.ChatID,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "show the approval card for " + bundleID,
		MessageID:  93,
		Timestamp:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "surfaced") {
		t.Fatalf("HandleInbound result = %#v, want referenced authority-bundle card surface", result)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 || !strings.Contains(inlineText, "grant-only test resource") {
		t.Fatalf("inline count=%d text=%q, want one reopened grant-only approval card", inlineCount, inlineText)
	}
	open, err = store.OpenNextActionsBySession(key, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after) err = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open actions after materialization = %#v, want referenced handoff resolved after card delivery", open)
	}
}

func seedChildAuthorityBundleRequest(t *testing.T, store *session.SQLiteStore, childKey session.SessionKey, agentID string) {
	t.Helper()

	now := time.Now().UTC()
	childBundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "legacy-child-bundle-live-shape-1",
		SessionID:         session.SessionIDForKey(childKey),
		Principal:         "durable_agent:" + strings.TrimSpace(agentID),
		Objective:         "Finish one bounded email opportunity report cycle.",
		Summary:           "Use child-local gog_cli read/search metadata to produce one report and stop.",
		AllowedActions:    []string{"wake_named_child", "gog_cli_search", "gog_cli_read_metadata", "rank_opportunities", "produce_report"},
		ForbiddenActions:  []string{"credential_or_token_output", "send_email", "delete_email", "unbounded_retry_loop", "deploy_or_restart"},
		StopConditions:    []string{"stop after one report", "stop on any typed blocker requiring new authority"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        "grant-idolum-email-gog-cli-read-search",
			Kind:           session.CapabilityKindTool,
			TargetResource: "gog_cli:job-search-mailbox",
			GrantedTo:      "durable_agent:" + strings.TrimSpace(agentID),
			AllowedActions: []string{"invoke", "search", "read", "metadata"},
			Contract:       `{"bounded_effect":"Allow idolum-email to search/read metadata needed for one opportunity report."}`,
			Constraints:    `{"account":"job-search-mailbox","content_scope":"unread_job_opportunities"}`,
		}},
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(child) err = %v", err)
	}
	childBundle, err = store.UpsertAuthorityBundleContract(childBundle)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract(child) err = %v", err)
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(childBundle.BundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput() err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           session.NextActionRecordID(session.SessionIDForKey(childKey), "authority_bundle_request", childBundle.BundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC()),
		Key:                childKey,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         childBundle.BundleID,
		CausalRefs:         []string{"authority_bundle:" + childBundle.BundleID},
		NextAction:         "review the bounded authority bundle and approve only if the allowed, forbidden, and stop boundaries match the objective",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review child-authored bounded authority bundle.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(child authority bundle) err = %v", err)
	}
}

func seedParentAuthorityBundleHandoff(t *testing.T, store *session.SQLiteStore, parentKey session.SessionKey, agentID string, requestToken string) string {
	t.Helper()

	now := time.Now().UTC()
	continuationContract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID: "parent-bundle-child-wake-" + strings.TrimSpace(requestToken),
		SessionID:         session.SessionIDForKey(parentKey),
		SubjectKind:       "continuation_lease_request",
		Principal:         "telegram:1001",
		LeaseClass:        session.ContinuationLeaseClassChildWake,
		AllowedActions:    []string{"wake_named_child"},
		Constraints:       map[string]string{"agent_id": strings.TrimSpace(agentID)},
		Tool:              "durable_agent",
		ToolAction:        "wake_once",
		AgentID:           strings.TrimSpace(agentID),
		CreatedAt:         now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract(parent) err = %v", err)
	}
	continuationContract, err = store.UpsertContinuationRecoveryContract(continuationContract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract(parent) err = %v", err)
	}
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID:             "parent-authority-bundle-" + strings.TrimSpace(requestToken),
		SessionID:                     session.SessionIDForKey(parentKey),
		Principal:                     "telegram:1001",
		Objective:                     "Finish one bounded child setup and report cycle.",
		Summary:                       "Use bounded child-local gog_cli read/search access to produce one report and stop.",
		AllowedActions:                []string{"wake_named_child", "gog_cli_search", "gog_cli_read_metadata", "rank_opportunities", "produce_report"},
		ForbiddenActions:              []string{"credential_or_token_output", "send_email", "delete_email", "unbounded_retry_loop", "deploy_or_restart"},
		StopConditions:                []string{"stop after one report", "stop on any typed blocker requiring new authority"},
		PrimaryContinuationContractID: continuationContract.ContractID,
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        "grant-" + strings.TrimSpace(requestToken) + "-gog-cli-read-search",
			Kind:           session.CapabilityKindTool,
			TargetResource: "gog_cli:job-search-mailbox",
			GrantedTo:      "durable_agent:" + strings.TrimSpace(agentID),
			AllowedActions: []string{"invoke", "search", "read", "metadata"},
			Contract:       `{"bounded_effect":"Allow the child to search/read metadata needed for one opportunity report."}`,
			Constraints:    `{"account":"job-search-mailbox","content_scope":"unread_job_opportunities"}`,
		}},
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CompileAuthorityBundleContract(parent) err = %v", err)
	}
	bundle, err = store.UpsertAuthorityBundleContract(bundle)
	if err != nil {
		t.Fatalf("UpsertAuthorityBundleContract(parent) err = %v", err)
	}
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundle.BundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput(parent) err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           session.NextActionRecordID(session.SessionIDForKey(parentKey), "authority_bundle_request", bundle.BundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC()),
		Key:                parentKey,
		Owner:              "tool",
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
		OperatorProjection: "Review bounded authority bundle.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(parent authority bundle) err = %v", err)
	}
	return bundle.BundleID
}

func principalAdminForTest() principal.Principal {
	return principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
}

func seedGrantOnlyAuthorityBundleHandoff(t *testing.T, store *session.SQLiteStore, key session.SessionKey, requestToken string) string {
	t.Helper()

	now := time.Now().UTC()
	bundle, err := session.CompileAuthorityBundleContract(session.AuthorityBundleContractInput{
		RequestInstanceID: "grant-only-authority-bundle-" + strings.TrimSpace(requestToken),
		SessionID:         session.SessionIDForKey(key),
		Principal:         "telegram:1001",
		Objective:         "Approve one bounded grant-only authority bundle.",
		Summary:           "Grant-only authority bundle for a generic approval recovery test.",
		AllowedActions:    []string{"invoke_grant_only_resource"},
		ForbiddenActions:  []string{"expand_authority_without_new_approval", "unbounded_retry_loop"},
		StopConditions:    []string{"stop after one approval or typed blocker"},
		RequiredCapabilityGrants: []session.CapabilityGrantSpec{{
			GrantID:        "grant-" + strings.TrimSpace(requestToken),
			Kind:           session.CapabilityKindGenericDelegation,
			TargetResource: "grant-only test resource",
			GrantedTo:      "telegram:1001",
			AllowedActions: []string{"invoke"},
			Contract:       `{"bounded_effect":"Allow one generic grant-only approval carrier test."}`,
			Constraints:    `{"scope":"grant_only_authority_bundle_test"}`,
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
	inputJSON, err := promotedDurableChildAuthorityBundleOperationInput(bundle.BundleID)
	if err != nil {
		t.Fatalf("promotedDurableChildAuthorityBundleOperationInput(grant-only) err = %v", err)
	}
	if _, err := store.RecordNextAction(session.NextActionInput{
		RecordID:           session.NextActionRecordID(session.SessionIDForKey(key), "authority_bundle_request", bundle.BundleID, session.NextActionBlockedNeedsAuthority, time.Unix(0, 0).UTC()),
		Key:                key,
		Owner:              "tool",
		State:              session.NextActionBlockedNeedsAuthority,
		SubjectKind:        "authority_bundle_request",
		SubjectRef:         bundle.BundleID,
		CausalRefs:         []string{"authority_bundle:" + bundle.BundleID},
		NextAction:         "review the bounded grant-only authority bundle and approve only if the contract matches the intended recovery",
		RequiredAuthority:  "authority_bundle",
		ResourceBlocker:    "authority_bundle_approval",
		RetryPolicy:        "retry_after_bundle_approval",
		OperationKind:      "authority_bundle_request",
		OperationTool:      "request_approval",
		OperationInputJSON: inputJSON,
		OperatorProjection: "Review grant-only authority bundle.",
		CreatedAt:          now,
	}); err != nil {
		t.Fatalf("RecordNextAction(grant-only authority bundle) err = %v", err)
	}
	return bundle.BundleID
}
