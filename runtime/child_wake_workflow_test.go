//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestChildWakeRecoveryJourneyRunsRealDurableWakeAfterApprovals(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Processed one no-content readiness check.\nREVIEW_STATUS: completed"

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

	key := session.SessionKey{ChatID: 9082, UserID: 0, Scope: telegramDMScopeRef(9082)}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	parentMessageID := seedRealChildWakeAgent(t, store, "idolum-email")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-full-wake-recovery",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-idolum-email-full-wake-recovery", Goal: "Recover idolum-email child readiness"},
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
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") {
		t.Fatalf("wake_once without grant err = %v, want missing capability grant", err)
	}
	grantAction := singleOpenWorkflowAction(t, store, key, session.NextActionBlockedNeedsAuthority, "capability_authority")
	grantInput := nextActionInputMap(t, grantAction)
	requestID, _ := grantInput["request_id"].(string)
	if strings.TrimSpace(requestID) == "" {
		t.Fatalf("grant action input = %#v, want request_id", grantInput)
	}
	if _, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"capability_authority",
		json.RawMessage(`{"action":"request_review","request_id":"`+requestID+`","review_status":"approved","rationale":"operator approved one exact child wake grant"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_review) err = %v", err)
	}
	grantOut, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		grantAction.OperationTool,
		json.RawMessage(grantAction.OperationInputJSON),
	)
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(grant_set handoff) err = %v", err)
	}
	if !strings.Contains(grantOut, "[CAPABILITY_GRANT]") || !strings.Contains(grantOut, "status: active") {
		t.Fatalf("grant handoff output = %q, want active capability grant", grantOut)
	}

	_, err = tools.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"idolum-email"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") || !strings.Contains(err.Error(), "lease request recorded") {
		t.Fatalf("wake_once without lease err = %v, want recorded child_wake lease blocker", err)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("provider prompts before lease approval = %#v, want no child turn before child_wake authority", provider.seenGovernorSystem)
	}
	leaseAction := singleOpenWorkflowAction(t, store, key, session.NextActionBlockedNeedsAuthority, "request_approval")
	if leaseAction.OperationKind != "continuation_lease_request" {
		t.Fatalf("lease action = %#v, want continuation lease request", leaseAction)
	}
	firstLeaseContract := continuationRecoveryContractForNextAction(t, store, leaseAction)

	materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9082, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 201},
		"continue",
	)
	if err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	}
	if !materialized {
		t.Fatal("materialized = false, want child_wake approval card")
	}
	pending, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(pending) err = %v", err)
	}
	if pending.ContinuationLease.LeaseClass != session.ContinuationLeaseClassChildWake ||
		pending.ContinuationLease.Status != session.ContinuationLeaseStatusPending ||
		strings.TrimSpace(pending.ContinuationLease.Constraints["agent_id"]) != "idolum-email" {
		t.Fatalf("pending continuation = %#v, want child_wake approval bound to idolum-email", pending)
	}
	retry := session.NormalizeContinuationRetryOperation(pending.ContinuationLease.RetryOperation)
	if !retry.Active() || retry.Tool != "durable_agent" || retry.OperationKind != "durable_agent_wake_once" || !strings.Contains(retry.InputJSON, `"agent_id":"idolum-email"`) {
		t.Fatalf("pending retry = %#v, want exact durable_agent wake_once retry", retry)
	}

	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       9082,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    202,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(continue approved child_wake) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(continue approved child_wake) result = %#v, want approved continuation dispatch acknowledgement", result)
	}

	current, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState(after child wake) err = %v", err)
	}
	if current.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed || current.RemainingTurns != 0 {
		t.Fatalf("continuation after child wake = %#v, want consumed one-turn lease", current)
	}
	pendingParent, err := rt.pendingDurableAgentParentConversation("idolum-email", 10)
	if err != nil {
		t.Fatalf("pendingDurableAgentParentConversation() err = %v", err)
	}
	if len(pendingParent) != 0 {
		t.Fatalf("pending parent messages = %#v, want child wake to consume and acknowledge them", pendingParent)
	}
	if len(provider.seenGovernorSystem) == 0 || !strings.Contains(strings.Join(provider.seenGovernorSystem, "\n"), "parent conversation wake") {
		t.Fatalf("provider governor prompts = %#v, want real durable parent conversation wake", provider.seenGovernorSystem)
	}
	for _, def := range provider.lastGovernorTools {
		if def.Name == "durable_agent" {
			t.Fatalf("child wake tool palette exposed parent-control durable_agent tool: %#v", provider.lastGovernorTools)
		}
	}
	wakeClaimID := session.DurableAgentWakeClaimID(session.DurableAgentWakeClaimInput{
		LeaseID:          current.ContinuationLease.ID,
		AgentID:          "idolum-email",
		MessageBatchHash: session.DurableAgentWakeMessageBatchHash("idolum-email", []string{parentMessageID}),
		MessageIDs:       []string{parentMessageID},
	})
	taskPacketID := durableWakeTaskPacketIDForWakeClaim("idolum-email", []string{parentMessageID}, wakeClaimID)
	packet, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(%q) err = %v", taskPacketID, err)
	}
	if !ok || packet.Status != session.ChildTaskPacketCompleted || packet.ResultID == "" || packet.TerminalAt.IsZero() {
		t.Fatalf("ChildTaskPacket(%q) = %#v ok=%t, want completed child task evidence", taskPacketID, packet, ok)
	}
	resultRecord, ok, err := store.ChildTaskResult(packet.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskResult(%q) err = %v", packet.ResultID, err)
	}
	if !ok || resultRecord.PacketID != taskPacketID || resultRecord.Status != session.ChildTaskResultCompleted || resultRecord.NextState != session.NextActionTerminal {
		t.Fatalf("ChildTaskResult(%q) = %#v ok=%t, want terminal result for claimed parent message", packet.ResultID, resultRecord, ok)
	}
	agent, err := store.DurableAgent("idolum-email")
	if err != nil {
		t.Fatalf("DurableAgent(idolum-email) err = %v", err)
	}
	if agent == nil {
		t.Fatal("DurableAgent(idolum-email) = nil")
	}
	wakeEvents, err := store.ExecutionEventsBySession(session.SessionKey{
		ChatID: durableWakeSyntheticChatID("idolum-email"),
		Scope:  durableAgentScopeRef(*agent),
	}, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(wake) err = %v", err)
	}
	assertHasEventType(t, wakeEvents, core.ExecutionEventDurableWakeStarted)
	assertHasEventType(t, wakeEvents, core.ExecutionEventDurableWakeCompleted)
	assertHasEventType(t, wakeEvents, core.ExecutionEventDurableChildTaskResult)
	providerPromptCountAfterWake := len(provider.seenGovernorSystem)
	open, err := store.OpenNextActionsBySession(key, 20)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(after wake) err = %v", err)
	}
	for _, action := range open {
		if action.SubjectKind == "capability_request" || action.SubjectKind == "continuation_lease_request" {
			t.Fatalf("open next actions after wake = %#v, want grant and lease blockers closed", open)
		}
	}
	events, err := store.ExecutionEventsBySession(key, 0, 300)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if hasExecutionEventPayload(events, core.ExecutionEventWorkflowNextState, "invalid_recovery_handoff") {
		t.Fatalf("events = %#v, did not want invalid recovery handoff during real child wake journey", events)
	}
	if !hasExecutionEventPayload(events, core.ExecutionEventContinuationConsumed, "child_wake") {
		t.Fatalf("events = %#v, want consumed child_wake continuation evidence", events)
	}

	if _, _, err := store.UpdateDurableAgentContinuity("idolum-email", func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.WithConversationMessage("parent", "Run a second no-content readiness check.", time.Now().UTC()), nil
	}); err != nil {
		t.Fatalf("UpdateDurableAgentContinuity(second parent request) err = %v", err)
	}
	_, err = tools.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"durable_agent",
		json.RawMessage(`{"action":"wake_once","agent_id":"idolum-email"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") {
		t.Fatalf("second wake_once after consumed lease err = %v, want fresh child_wake lease blocker", err)
	}
	if strings.Contains(err.Error(), "already recorded") {
		t.Fatalf("second wake_once err = %v, want fresh request after consumed lease rather than stale already-recorded handoff", err)
	}
	if len(provider.seenGovernorSystem) != providerPromptCountAfterWake {
		t.Fatalf("provider prompts after second blocked wake = %#v, want no child turn without fresh child_wake authority", provider.seenGovernorSystem)
	}
	secondLeaseAction := singleOpenWorkflowAction(t, store, key, session.NextActionBlockedNeedsAuthority, "request_approval")
	secondLeaseContract := continuationRecoveryContractForNextAction(t, store, secondLeaseAction)
	if secondLeaseContract.RequestInstanceID == firstLeaseContract.RequestInstanceID || secondLeaseContract.ContractID == firstLeaseContract.ContractID {
		t.Fatalf("second lease contract = %#v first = %#v, want a new request instance for a later identical wake", secondLeaseContract, firstLeaseContract)
	}
}

func TestApprovedChildWakeRetryRecordsTerminalChildBlockerInParentSession(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Processed active grants and non-secret config. Runtime check: gog_cli=missing_or_not_executable.\nREVIEW_STATUS: blocked"

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

	key := session.SessionKey{ChatID: 9083, UserID: 0, Scope: telegramDMScopeRef(9083)}
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}
	seedRealChildWakeAgent(t, store, "idolum-email")
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-idolum-email-blocked-wake-recovery",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{ID: "plan-idolum-email-blocked-wake-recovery", Goal: "Recover idolum-email child readiness"},
	}); err != nil {
		t.Fatalf("UpdateOperationState(seed operation) err = %v", err)
	}

	_, err = tools.ExecuteForSessionPrincipal(context.Background(), actor, key, "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"idolum-email"}`))
	if err == nil || !strings.Contains(err.Error(), "missing capability grant") {
		t.Fatalf("wake_once without grant err = %v, want missing capability grant", err)
	}
	grantAction := singleOpenWorkflowAction(t, store, key, session.NextActionBlockedNeedsAuthority, "capability_authority")
	grantInput := nextActionInputMap(t, grantAction)
	requestID, _ := grantInput["request_id"].(string)
	if strings.TrimSpace(requestID) == "" {
		t.Fatalf("grant action input = %#v, want request_id", grantInput)
	}
	if _, err := tools.ExecuteForSessionPrincipal(
		context.Background(),
		actor,
		key,
		"capability_authority",
		json.RawMessage(`{"action":"request_review","request_id":"`+requestID+`","review_status":"approved","rationale":"operator approved one exact child wake grant"}`),
	); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(request_review) err = %v", err)
	}
	if _, err := tools.ExecuteForSessionPrincipal(context.Background(), actor, key, grantAction.OperationTool, json.RawMessage(grantAction.OperationInputJSON)); err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(grant_set handoff) err = %v", err)
	}

	_, err = tools.ExecuteForSessionPrincipal(context.Background(), actor, key, "durable_agent", json.RawMessage(`{"action":"wake_once","agent_id":"idolum-email"}`))
	if err == nil || !strings.Contains(err.Error(), "missing child_wake continuation lease") || !strings.Contains(err.Error(), "lease request recorded") {
		t.Fatalf("wake_once without lease err = %v, want recorded child_wake lease blocker", err)
	}
	if materialized, err := rt.MaterializeRequestedApproval(
		context.Background(),
		key,
		core.InboundMessage{ChatID: 9083, SenderID: 1001, SenderName: "admin", Text: "continue", MessageID: 301},
		"continue",
	); err != nil {
		t.Fatalf("MaterializeRequestedApproval() err = %v", err)
	} else if !materialized {
		t.Fatal("materialized = false, want child_wake approval card")
	}
	if _, err := rt.ApproveContinuationForKey(key, 1001); err != nil {
		t.Fatalf("ApproveContinuationForKey() err = %v", err)
	}
	result, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:       9083,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "[user pressed continue button: resume the previous task]",
		MessageID:    302,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	})
	if err != nil {
		t.Fatalf("HandleInbound(continue approved child_wake) err = %v", err)
	}
	if result == nil || !strings.Contains(result.Text, "Running approved continuation") {
		t.Fatalf("HandleInbound(continue approved child_wake) result = %#v, want approved continuation dispatch acknowledgement", result)
	}

	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState(parent) err = %v", err)
	}
	if opState.Status != session.OperationStatusBlocked || opState.Stage != "approved_retry_child_blocked" {
		t.Fatalf("parent operation state = %#v, want blocked approved_retry_child_blocked", opState)
	}
	open, err := store.OpenNextActionsBySession(key, 50)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(parent) err = %v", err)
	}
	var foundProbe bool
	for _, action := range open {
		if action.Owner == "approved_retry" && action.State == session.NextActionWaitingForChild {
			t.Fatalf("open parent actions = %#v, waiting_for_child should be replaced by typed child blocker", open)
		}
		if action.Owner == "approved_retry" &&
			action.State == session.NextActionNeedsVerification &&
			action.ResourceBlocker == "tool_runtime_probe_missing" &&
			action.OperationKind == session.NextActionOperationKindDurableChildRecovery &&
			action.OperationTool == "update_operation" {
			foundProbe = true
		}
	}
	if !foundProbe {
		t.Fatalf("open parent actions = %#v, want approved_retry child runtime probe-required blocker", open)
	}
}

func TestApprovedRetryWakeReconciliationAdvancesReleasedChildUpdate(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agentID := "idolum-email"
	seedRealChildWakeAgent(t, store, agentID)

	key := session.SessionKey{ChatID: 9084, UserID: 0, Scope: telegramDMScopeRef(9084)}
	now := time.Now().UTC()
	leaseID := "lease-approved-child-update"
	messageIDs := []string{"parent-message-approved-child-update"}
	claim, err := store.ClaimDurableAgentWakeOnce(session.DurableAgentWakeClaimInput{
		LeaseID:          leaseID,
		AgentID:          agentID,
		TurnRunID:        41,
		MessageBatchHash: session.DurableAgentWakeMessageBatchHash(agentID, messageIDs),
		MessageIDs:       messageIDs,
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatalf("ClaimDurableAgentWakeOnce() err = %v", err)
	}
	packetID := durableWakeTaskPacketIDForWakeClaim(agentID, messageIDs, claim.ClaimID)
	if _, err := store.RecordChildTaskPacket(session.ChildTaskPacketInput{
		PacketID:         packetID,
		TaskLeaseID:      leaseID,
		AgentID:          agentID,
		Key:              key,
		TaskKind:         "parent_conversation_wake",
		Status:           session.ChildTaskPacketQueued,
		AuthorityKind:    "continuation",
		AuthorityID:      leaseID,
		TargetResource:   "durable_agent:" + agentID + ":wake_once",
		RequiredAction:   "wake_once",
		InputJSON:        `{"action":"wake_once","agent_id":"idolum-email"}`,
		InputFingerprint: session.EffectAttemptCommandHash(`{"action":"wake_once","agent_id":"idolum-email"}`),
		CreatedAt:        now,
	}); err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claimed, err := store.ClaimChildTaskAttempt(session.ChildTaskAttemptClaimInput{
		PacketID:       packetID,
		AttemptID:      "attempt-approved-child-update",
		LeaseOwner:     "runtime-test",
		AgentID:        agentID,
		Key:            key,
		ClaimedAt:      now.Add(time.Second),
		LeaseExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ClaimChildTaskAttempt() err = %v", err)
	}
	result, err := store.CommitChildTaskOutcome(session.ChildTaskOutcomeCommitInput{
		Result: session.ChildTaskResultInput{
			ResultID:        "child_result:approved-child-update",
			PacketID:        packetID,
			AttemptID:       claimed.ActiveAttemptID,
			LeaseOwner:      claimed.LeaseOwner,
			LeaseGeneration: claimed.LeaseGeneration,
			FencingToken:    claimed.FencingToken,
			TaskLeaseID:     leaseID,
			AgentID:         agentID,
			Key:             key,
			Status:          session.ChildTaskResultUpdate,
			ResultKind:      "child_progress_update",
			Summary:         "Processed setup guidance and needs one bounded continuation.",
			NextState:       session.NextActionWaitingForChild,
			CreatedAt:       now.Add(2 * time.Second),
		},
		ResolvedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CommitChildTaskOutcome(update) err = %v", err)
	}
	if result.Status != session.ChildTaskResultUpdate {
		t.Fatalf("child result = %#v, want update", result)
	}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:      "op-approved-child-update",
		Status:  session.OperationStatusActive,
		Stage:   "approved_retry_waiting_for_child",
		Summary: "Approved retry is waiting for the child.",
		PhasePlan: session.OperationPhasePlan{
			ID:   "plan-approved-child-update",
			Goal: "Recover idolum-email child setup.",
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	subjectRef := session.ContinuationRecoverySubjectRef(session.ContinuationLeaseClassChildWake, agentID, "grant-idolum-email-wake", "durable_agent", "wake_once", "")
	if _, err := store.RecordNextAction(session.NextActionInput{
		Key:                key,
		Owner:              "approved_retry",
		State:              session.NextActionWaitingForChild,
		SubjectKind:        "continuation_lease_request",
		SubjectRef:         subjectRef,
		CausalRefs:         []string{"continuation:" + leaseID, "durable_agent:" + agentID},
		NextAction:         "wait for the child wake result before retrying",
		OperationKind:      "durable_agent_wake_once",
		OperationTool:      "durable_agent",
		OperationInputJSON: `{"action":"wake_once","agent_id":"idolum-email"}`,
		OperatorProjection: "The approved child wake retry started and is waiting for a child result.",
		CreatedAt:          now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("RecordNextAction(parent waiter) err = %v", err)
	}

	if err := rt.reconcileApprovedRetryWakeWaitersForSession(key); err != nil {
		t.Fatalf("reconcileApprovedRetryWakeWaitersForSession() err = %v", err)
	}

	open, err := store.OpenNextActionsBySession(key, 50)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	var foundUpdate bool
	for _, action := range open {
		if action.Owner == "approved_retry" && action.OperationKind == "durable_agent_wake_once" {
			t.Fatalf("open actions = %#v, stale approved_retry wake waiter should be resolved", open)
		}
		if action.Owner == "approved_retry" &&
			action.OperationKind == session.NextActionOperationKindDurableChildRecovery &&
			action.OperationTool == "update_operation" &&
			action.State == session.NextActionWaitingForChild &&
			strings.Contains(strings.Join(action.CausalRefs, "\n"), "child_task_result:"+result.ResultID) {
			foundUpdate = true
		}
	}
	if !foundUpdate {
		t.Fatalf("open actions = %#v, want approved_retry child-update continuation action", open)
	}
	opState, err := store.OperationState(key)
	if err != nil {
		t.Fatalf("OperationState() err = %v", err)
	}
	if opState.Stage != "approved_retry_child_blocked" || opState.Status != session.OperationStatusBlocked {
		t.Fatalf("operation state = %#v, want approved retry moved out of plain waiting state", opState)
	}
}

func seedRealChildWakeAgent(t *testing.T, store *session.SQLiteStore, agentID string) string {
	t.Helper()

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            agentID,
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter:      "codex_image_generation",
			PollInterval: "168h",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Consume one pending parent guidance item when explicitly woken.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "blocker_report"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent(%s) err = %v", agentID, err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Run one no-content readiness check and report the result.", time.Now().UTC().Add(-time.Minute))
	pending := continuity.PendingParentConversationMessages(1)
	if len(pending) != 1 || strings.TrimSpace(pending[0].MessageID) == "" {
		t.Fatalf("seed continuity pending messages = %#v, want one stable parent message id", pending)
	}
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{AgentID: agentID, StateJSON: raw}); err != nil {
		t.Fatalf("SaveDurableAgentState(%s) err = %v", agentID, err)
	}
	return strings.TrimSpace(pending[0].MessageID)
}

func singleOpenWorkflowAction(t *testing.T, store *session.SQLiteStore, key session.SessionKey, state session.NextActionState, toolName string) session.NextActionRecord {
	t.Helper()

	open, err := store.OpenNextActionsBySession(key, 50)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession() err = %v", err)
	}
	var matches []session.NextActionRecord
	for _, action := range open {
		if action.State == state && strings.TrimSpace(action.OperationTool) == toolName {
			matches = append(matches, action)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("open actions = %#v, want exactly one state=%s tool=%s", open, state, toolName)
	}
	return matches[0]
}

func nextActionInputMap(t *testing.T, action session.NextActionRecord) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal([]byte(action.OperationInputJSON), &decoded); err != nil {
		t.Fatalf("unmarshal next action %s input %q: %v", action.RecordID, action.OperationInputJSON, err)
	}
	return decoded
}

func continuationRecoveryContractForNextAction(t *testing.T, store *session.SQLiteStore, action session.NextActionRecord) session.ContinuationRecoveryContract {
	t.Helper()

	input := nextActionInputMap(t, action)
	contractID, _ := input["contract_id"].(string)
	if strings.TrimSpace(contractID) == "" {
		t.Fatalf("next action %s input = %#v, want contract_id", action.RecordID, input)
	}
	contract, ok, err := store.ContinuationRecoveryContract(contractID)
	if err != nil {
		t.Fatalf("ContinuationRecoveryContract(%q) err = %v", contractID, err)
	}
	if !ok {
		t.Fatalf("ContinuationRecoveryContract(%q) ok=false", contractID)
	}
	return contract
}
