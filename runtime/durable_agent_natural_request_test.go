//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestNaturalDurableAgentDirectivePersistsThenRecommendationWakesChild(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Recommendations: use X, Y, Z. Apply to Job A first.\nREVIEW_STATUS: completed"
	rt, _ := newNaturalDurableAgentRequestRuntime(t, cfg, store, provider, sender)

	seedRealChildWakeAgent(t, store, "child-alpha")
	seedRuntimeWakeGrant(t, store, "child-alpha", "telegram:1001")
	key := session.SessionKey{ChatID: 9401, UserID: 0, Scope: telegramDMScopeRef(9401)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-natural-child-alpha",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-natural-child-alpha", Goal: "Use child-alpha naturally"},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	behaviorText := "Change child-alpha behavior so anytime it is asked for recommendations it uses tools X, Y, Z automatically within approved bounds."
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9401,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       behaviorText,
		MessageID:  1,
		Origin:     core.InboundOriginUser,
	})
	if err != nil {
		t.Fatalf("HandleInbound(behavior directive) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Updated child-alpha") {
		t.Fatalf("behavior result = %#v, want policy update acknowledgement", result)
	}
	updated, err := store.DurableAgent("child-alpha")
	if err != nil {
		t.Fatalf("DurableAgent(child-alpha) err = %v", err)
	}
	if !strings.Contains(updated.LivePolicy.Charter, "tools X, Y, Z") {
		t.Fatalf("updated charter = %q, want behavior directive", updated.LivePolicy.Charter)
	}

	seedNaturalChildWakeContinuation(t, store, key, "child-alpha")
	result, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9401,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "what does child-alpha recommend now?",
		MessageID:  2,
		Origin:     core.InboundOriginUser,
	})
	if err != nil {
		t.Fatalf("HandleInbound(recommendation request) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Recommendations: use X, Y, Z") {
		t.Fatalf("recommendation result = %#v, want child reply", result)
	}
	if len(provider.seenGovernorSystem) == 0 || !strings.Contains(strings.Join(provider.seenGovernorSystem, "\n"), "tools X, Y, Z") {
		t.Fatalf("provider governor prompts = %#v, want updated charter included in child wake", provider.seenGovernorSystem)
	}
	pending, err := rt.pendingDurableAgentParentConversation("child-alpha", 20)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending parent messages = %#v, want child wake to consume queued guidance", pending)
	}
}

func TestNaturalIdolumEmailRecommendationQueuesBoundedWakeApproval(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, _ := newNaturalDurableAgentRequestRuntime(t, cfg, store, provider, sender)
	seedRealChildWakeAgent(t, store, "idolum-email")
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9402,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "tell me which jobs the email agent recommends",
		MessageID:  1,
		Origin:     core.InboundOriginUser,
	})
	if err != nil {
		t.Fatalf("HandleInbound(idolum-email recommendation) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "queued idolum-email") || !strings.Contains(result.Text, "bounded wake approval") {
		t.Fatalf("result = %#v, want queued wake approval acknowledgement", result)
	}
	if strings.Contains(result.Text, "child_wake") || strings.Contains(result.Text, "continuation lease") {
		t.Fatalf("result text = %q, want operator-facing wording without internal lease jargon", result.Text)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("provider prompts = %#v, want no child turn before approved child_wake authority", provider.seenGovernorSystem)
	}
	pending, err := rt.pendingDurableAgentParentConversation("idolum-email", 20)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	var found bool
	for _, message := range pending {
		if strings.Contains(message.Text, "tell me which jobs the email agent recommends") &&
			strings.Contains(message.Text, "current durable charter") {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending parent messages = %#v, want queued natural recommendation guidance", pending)
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline cards = %d, want one bounded approval card", inlineCount)
	}
	if !strings.Contains(inlineText, "idolum-email") || !strings.Contains(inlineText, "wake only idolum-email once") {
		t.Fatalf("inline approval = %q, want exact child wake approval", inlineText)
	}
	key := session.SessionKey{ChatID: 9402, UserID: 0, Scope: telegramDMScopeRef(9402)}
	state, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if state.Status != session.ContinuationStatusPending ||
		state.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		strings.TrimSpace(state.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("pending continuation = %#v, want bounded idolum-email child_wake approval", state)
	}
}

func TestNaturalIdolumEmailRequestWithApprovalBundleLanguageBypassesStaleBlockedPhase(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, _ := newNaturalDurableAgentRequestRuntime(t, cfg, store, provider, sender)
	seedRealChildWakeAgent(t, store, "idolum-email")
	seedRuntimeWakeGrant(t, store, "idolum-email", "telegram:1001")

	key := session.SessionKey{ChatID: 9404, UserID: 0, Scope: telegramDMScopeRef(9404)}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "idolum-email-gog-smoke-test",
		Objective: "Finish idolum-email setup.",
		Status:    session.OperationStatusActive,
		Stage:     "wide_specific_authority_contract_proposal",
		Summary:   "A prior blocked phase asked for a wide but specific authority contract.",
		PhasePlan: session.OperationPhasePlan{
			ID:             "idolum-email-finish-child-setup-less-ceremony",
			Goal:           "Finish idolum-email setup without step-by-step ceremony.",
			CurrentPhaseID: "idolum-email-wide-specific-finish-setup-contract-v1",
			Phases: []session.OperationPhase{{
				ID:               "idolum-email-wide-specific-finish-setup-contract-v1",
				Summary:          "Wide but specific authority contract for finishing idolum-email setup through one report/archive cycle.",
				Status:           session.PlanStatusPending,
				AuthorityClass:   "mailbox_report",
				RequiresApproval: true,
				RequiresConsent:  true,
				GateLevel:        "hard_consent_block",
				GateReasonCode:   "operator_consent",
				WhyNow:           "A stale phase still exists in operation state.",
				BoundedEffect:    "Do not render this stale blocked phase over a fresh natural admin request.",
				AllowedActions:   []string{"draft_authority_bundle"},
				ForbiddenActions: []string{"mailbox_mutation", "external_contact"},
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState(stale blocked phase) err = %v", err)
	}

	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     9404,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Please complete the setup of the idolum email system and then let me know which unread job or opportunity emails I should not miss. If you require broader, one-time authorization, create a bounded approval bundle with allowed actions, forbidden actions, and stop conditions.",
		MessageID:  1,
		Origin:     core.InboundOriginUser,
	})
	if err != nil {
		t.Fatalf("HandleInbound(natural request with approval-bundle language) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "queued idolum-email") || !strings.Contains(result.Text, "bounded wake approval") {
		t.Fatalf("result = %#v, want natural durable-agent request to queue child wake approval", result)
	}
	if strings.Contains(result.Text, "pending continuation approval") {
		t.Fatalf("result text = %q, stale approval materialization preempted natural request", result.Text)
	}
	pending, err := rt.pendingDurableAgentParentConversation("idolum-email", 20)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("pending parent messages = 0, want queued child guidance")
	}
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	inlineText := ""
	if inlineCount > 0 {
		inlineText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline cards = %d, want one bounded wake approval card", inlineCount)
	}
	if !strings.Contains(inlineText, "idolum-email") || !strings.Contains(inlineText, "wake only idolum-email once") {
		t.Fatalf("inline approval = %q, want exact idolum-email wake approval", inlineText)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if hasExecutionEventPayload(events, core.ExecutionEventContinuationBlocked, "waiting for explicit consent") {
		t.Fatalf("events = %#v, stale blocked phase should not preempt natural request", events)
	}
}

func TestNaturalDurableAgentRequestRequiresAdminPrivateSurface(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, _ := newNaturalDurableAgentRequestRuntime(t, cfg, store, provider, sender)
	seedRealChildWakeAgent(t, store, "child-alpha")

	key := session.SessionKey{ChatID: -9403, UserID: 0, Scope: telegramDMScopeRef(-9403)}
	admin := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	tools := rt.toolsForPrincipal(admin, key)
	handled, _, err := rt.maybeHandleNaturalDurableAgentRequest(context.Background(), key, admin, core.InboundMessage{
		ChatID:     -9403,
		ChatType:   "supergroup",
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Change child-alpha behavior so it always recommends the best jobs.",
		MessageID:  1,
		Origin:     core.InboundOriginUser,
	}, tools)
	if err != nil {
		t.Fatalf("maybeHandleNaturalDurableAgentRequest(group) err = %v", err)
	}
	if handled {
		t.Fatal("maybeHandleNaturalDurableAgentRequest(group) handled = true, want private/admin-only lane")
	}

	user := principal.Principal{Role: principal.RoleApprovedUser, TelegramUserID: 1002}
	handled, _, err = rt.maybeHandleNaturalDurableAgentRequest(context.Background(), key, user, core.InboundMessage{
		ChatID:     1002,
		ChatType:   "private",
		SenderID:   1002,
		SenderName: "approved-user",
		Text:       "tell me what child-alpha recommends",
		MessageID:  2,
		Origin:     core.InboundOriginUser,
	}, tools)
	if err != nil {
		t.Fatalf("maybeHandleNaturalDurableAgentRequest(non-admin) err = %v", err)
	}
	if handled {
		t.Fatal("maybeHandleNaturalDurableAgentRequest(non-admin) handled = true, want admin-only lane")
	}
}

func newNaturalDurableAgentRequestRuntime(t *testing.T, cfg *config.Config, store *session.SQLiteStore, provider *fakeProvider, sender *fakeSender) (*Runtime, *toolpkg.Registry) {
	t.Helper()

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
	return rt, tools
}

func seedNaturalChildWakeContinuation(t *testing.T, store *session.SQLiteStore, key session.SessionKey, agentID string) {
	t.Helper()

	now := time.Now().UTC()
	retryInput, err := json.Marshal(map[string]any{
		"action":   "wake_once",
		"agent_id": agentID,
	})
	if err != nil {
		t.Fatalf("marshal retry input: %v", err)
	}
	grantID := "grant-" + agentID + "-wake-once"
	subjectRef := session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, agentID, grantID, "durable_agent", "wake_once", "")
	contract, err := session.CompileContinuationRecoveryContract(session.ContinuationRecoveryContractInput{
		RequestInstanceID:   "natural-" + agentID + "-wake-request",
		SessionID:           session.SessionIDForKey(key),
		SubjectKind:         "continuation_lease_request",
		SubjectRef:          subjectRef,
		Principal:           "telegram:1001",
		LeaseClass:          session.ContinuationLeaseClassChildWake,
		AllowedActions:      []string{"wake_named_child"},
		Constraints:         map[string]string{"agent_id": agentID},
		Tool:                "durable_agent",
		ToolAction:          "wake_once",
		AgentID:             agentID,
		GrantID:             grantID,
		GrantTargetResource: "durable_agent:" + agentID + ":wake_once",
		RetryOperation: session.ContinuationRetryOperation{
			Contract:          session.ContinuationRecoveryRetryVersion,
			OperationKind:     "durable_agent_wake_once",
			Tool:              "durable_agent",
			InputJSON:         string(retryInput),
			SubjectKind:       "continuation_lease_request",
			SubjectRef:        subjectRef,
			RequestInstanceID: "natural-" + agentID + "-wake-request",
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("CompileContinuationRecoveryContract() err = %v", err)
	}
	contract, err = store.UpsertContinuationRecoveryContract(contract)
	if err != nil {
		t.Fatalf("UpsertContinuationRecoveryContract() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, approvedNaturalChildWakeContinuationState(agentID, contract, now)); err != nil {
		t.Fatalf("UpdateContinuationState(approved child_wake) err = %v", err)
	}
}

func approvedNaturalChildWakeContinuationState(agentID string, contract session.ContinuationRecoveryContract, now time.Time) session.ContinuationState {
	state := approvedReadOnlyContinuationStateForScopeTest("natural-"+agentID+"-wake", now)
	state.Objective = "Wake " + agentID + " exactly once for a natural parent request."
	state.StageSummary = "Run one bounded durable child wake."
	state.ActionProposal.Summary = "Wake " + agentID + " once"
	state.ActionProposal.RiskClass = "child_wake"
	state.ActionProposal.AllowedActions = []string{"wake_named_child"}
	state.ContinuationLease.ID = "lease-natural-" + agentID + "-wake"
	state.ContinuationLease.LeaseClass = session.ContinuationLeaseClassChildWake
	state.ContinuationLease.AllowedActions = []string{"wake_named_child"}
	state.ContinuationLease.Constraints = map[string]string{"agent_id": agentID}
	state.ContinuationLease.RecoveryContractID = contract.ContractID
	state.ContinuationLease.RetryOperation = contract.RetryOperation
	return session.NormalizeContinuationState(state)
}
