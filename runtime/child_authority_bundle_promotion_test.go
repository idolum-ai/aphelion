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
					"target_resource":"gog_cli:host@idolum.ai",
					"granted_to":"durable_agent:idolum-email",
					"allowed_actions":["invoke","search","read","metadata"],
					"contract":"{\"bounded_effect\":\"Allow idolum-email to search/read metadata needed for one opportunity report.\"}",
					"constraints":"{\"account\":\"host@idolum.ai\",\"content_scope\":\"unread_job_opportunities\"}"
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
		grant.TargetResource != "gog_cli:host@idolum.ai" ||
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
		!strings.Contains(last.text, "gog_cli:host@idolum.ai") ||
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
