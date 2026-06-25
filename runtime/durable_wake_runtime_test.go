//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool/sandbox"
	"github.com/idolum-ai/aphelion/turn"
)

type testDurableWakeAdapter struct {
	channelKind  string
	queueReview  bool
	prepareCalls int
	finalized    bool
	finalizeErr  error
	lastSummary  string
}

func TestDurableWakeChildBlockerClassification(t *testing.T) {
	t.Parallel()

	agent := core.DurableAgent{
		AgentID:     "child-classifier",
		ChannelKind: "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Adapter: "gog_cli",
		}},
	}
	cases := []struct {
		name           string
		status         session.ChildTaskResultStatus
		summary        string
		blocker        string
		wantKind       string
		wantState      session.NextActionState
		wantOp         string
		wantRetry      string
		wantProbe      bool
		wantDiagnostic bool
	}{
		{
			name:           "missing executable",
			status:         session.ChildTaskResultBlocked,
			summary:        "Runtime check: gog_cli=missing_or_not_executable.\nREVIEW_STATUS: blocked",
			blocker:        "child_reported_blocked",
			wantKind:       "tool_runtime_not_executable",
			wantState:      session.NextActionBlockedNeedsResourceRepair,
			wantOp:         "child_tool_runtime_repair",
			wantRetry:      "retry_after_tool_runtime_repair",
			wantProbe:      true,
			wantDiagnostic: true,
		},
		{
			name:           "lifecycle unregistered",
			status:         session.ChildTaskResultBlocked,
			summary:        "child_runtime_blocked: preflight_failed adapter=gog_cli failure_code=lifecycle_unregistered",
			blocker:        "child_reported_blocked",
			wantKind:       "tool_lifecycle_unregistered",
			wantState:      session.NextActionBlockedNeedsResourceRepair,
			wantOp:         "child_tool_lifecycle_repair",
			wantRetry:      "retry_after_tool_lifecycle_repair",
			wantDiagnostic: true,
		},
		{
			name:           "grant missing",
			status:         session.ChildTaskResultBlocked,
			summary:        "EXTERNAL_CHANNEL_OUTCOME blocked reason_code=missing_grant",
			blocker:        "missing_grant",
			wantKind:       "grant_missing_or_stale",
			wantState:      session.NextActionBlockedNeedsAuthority,
			wantOp:         "child_authority_repair",
			wantRetry:      "retry_after_authority_repair",
			wantDiagnostic: true,
		},
		{
			name:           "permission denied",
			status:         session.ChildTaskResultBlocked,
			summary:        "write failed: permission denied",
			blocker:        "child_reported_blocked",
			wantKind:       "resource_permission_denied",
			wantState:      session.NextActionBlockedNeedsResourceRepair,
			wantOp:         "child_resource_repair",
			wantRetry:      "retry_after_resource_repair",
			wantDiagnostic: true,
		},
		{
			name:           "credential unverified",
			status:         session.ChildTaskResultBlocked,
			summary:        "auth_status probe needed before account work",
			blocker:        "child_reported_blocked",
			wantKind:       "credential_unverified",
			wantState:      session.NextActionWaitingForOperator,
			wantOp:         "child_credential_probe",
			wantRetry:      "retry_after_credential_verification",
			wantProbe:      true,
			wantDiagnostic: true,
		},
		{
			name:           "external transient",
			status:         session.ChildTaskResultBlocked,
			summary:        "temporary provider timeout; retry later",
			blocker:        "child_reported_blocked",
			wantKind:       "external_transient",
			wantState:      session.NextActionScheduledRetry,
			wantOp:         "child_retry",
			wantRetry:      "bounded_backoff",
			wantDiagnostic: true,
		},
		{
			name:           "unknown blocked",
			status:         session.ChildTaskResultBlocked,
			summary:        "blocked on a child-local condition that needs review",
			blocker:        "",
			wantKind:       "child_reported_blocked",
			wantState:      session.NextActionWaitingForOperator,
			wantOp:         "child_blocker_disambiguation",
			wantRetry:      "operator_disambiguation_required",
			wantDiagnostic: true,
		},
		{
			name:      "update",
			status:    session.ChildTaskResultUpdate,
			summary:   "intermediate progress",
			wantKind:  "child_task_update",
			wantState: session.NextActionWaitingForChild,
			wantOp:    "child_task_continue",
			wantRetry: "continue_after_child_update",
		},
		{
			name:           "missing terminal review status",
			status:         session.ChildTaskResultUpdate,
			summary:        "I checked some state but did not provide a review status marker.",
			blocker:        "missing_terminal_review_status",
			wantKind:       "missing_terminal_review_status",
			wantState:      session.NextActionWaitingForOperator,
			wantOp:         "child_terminal_status_disambiguation",
			wantRetry:      "operator_disambiguation_required",
			wantDiagnostic: true,
		},
		{
			name:           "failed wake",
			status:         session.ChildTaskResultFailed,
			summary:        "wake failed before completion",
			blocker:        "",
			wantKind:       "wake_failed",
			wantState:      session.NextActionBlockedNeedsResourceRepair,
			wantOp:         "child_wake_repair",
			wantRetry:      "retry_after_wake_repair",
			wantDiagnostic: true,
		},
		{
			name:      "completed",
			status:    session.ChildTaskResultCompleted,
			summary:   "done",
			wantState: session.NextActionTerminal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := durableWakeChildTaskBlockerClassification(agent, session.ChildTaskResultInput{
				PacketID:    "child_task:test",
				ResultID:    "child_result:test",
				AgentID:     agent.AgentID,
				Status:      tc.status,
				BlockerKind: tc.blocker,
				Summary:     tc.summary,
			})
			if got.Kind != tc.wantKind || got.State != tc.wantState || got.OperationKind != tc.wantOp || got.RetryPolicy != tc.wantRetry || got.NoContentProbe != tc.wantProbe || got.DiagnosticOnly != tc.wantDiagnostic {
				t.Fatalf("classification = %#v, want kind=%s state=%s op=%s retry=%s probe=%t diagnostic=%t", got, tc.wantKind, tc.wantState, tc.wantOp, tc.wantRetry, tc.wantProbe, tc.wantDiagnostic)
			}
			if tc.wantKind == "" {
				if got.OperationInputJSON != "" {
					t.Fatalf("OperationInputJSON = %q, want empty for terminal completion", got.OperationInputJSON)
				}
				return
			}
			var input map[string]any
			if err := json.Unmarshal([]byte(got.OperationInputJSON), &input); err != nil {
				t.Fatalf("operation input JSON = %q err=%v", got.OperationInputJSON, err)
			}
			if input["agent_id"] != agent.AgentID || input["blocker_kind"] != tc.wantKind || input["task_packet_id"] != "child_task:test" || input["child_result_id"] != "child_result:test" {
				t.Fatalf("operation input = %#v, want exact agent/blocker/task/result refs", input)
			}
			if input["no_content_probe"] != tc.wantProbe || input["diagnostic_only"] != tc.wantDiagnostic {
				t.Fatalf("operation input = %#v, want probe=%t diagnostic=%t", input, tc.wantProbe, tc.wantDiagnostic)
			}
		})
	}
}

func markDurableWakeExternalAdapterReady(t *testing.T, store *session.SQLiteStore, agentID string, adapterName string) {
	t.Helper()
	now := time.Now().UTC()
	materialRoot := t.TempDir()
	if _, err := store.UpsertRegisteredTool(session.RegisteredTool{ToolName: adapterName, ImplementationRef: "external:" + adapterName, Registered: true}); err != nil {
		t.Fatalf("UpsertRegisteredTool(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolInstallRecord(session.ToolInstallRecord{ToolName: adapterName, Status: session.ToolInstallStatusVerified, InstalledAt: now, AttestedAt: now}); err != nil {
		t.Fatalf("UpsertToolInstallRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolAuditRecord(session.ToolAuditRecord{ToolName: adapterName, Status: session.ToolAuditStatusPassed, AuditedAt: now}); err != nil {
		t.Fatalf("UpsertToolAuditRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertToolProbeRecord(session.ToolProbeRecord{ToolName: adapterName, Status: session.ToolProbeStatusPassed, ProbedAt: now}); err != nil {
		t.Fatalf("UpsertToolProbeRecord(%s) err = %v", adapterName, err)
	}
	if _, err := store.UpsertCapabilityGrant(session.CapabilityGrant{
		GrantID:        "capg-" + agentID + "-" + adapterName,
		Kind:           session.CapabilityKindTool,
		TargetResource: adapterName,
		GrantedTo:      core.DurableAgentPrincipal(agentID),
		AllowedActions: []string{"invoke"},
		Status:         session.CapabilityGrantStatusActive,
		Contract:       `{"child_runtime":{"readonly_paths":["` + materialRoot + `"]}}`,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(%s) err = %v", adapterName, err)
	}
}

func (a *testDurableWakeAdapter) Name() string {
	return "test_adapter"
}

func (a *testDurableWakeAdapter) Supports(agent core.DurableAgent) bool {
	return strings.TrimSpace(agent.ChannelKind) == strings.TrimSpace(a.channelKind)
}

func (a *testDurableWakeAdapter) Prepare(_ context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	a.prepareCalls++
	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	return &durableWakeTurnPlan{
		Channel:      strings.TrimSpace(a.channelKind),
		AuditChannel: strings.TrimSpace(a.channelKind),
		Key:          key,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       strings.TrimSpace(a.channelKind),
			ChatTitle:      "durable-wake-test",
			SenderName:     "adapter",
			Text:           "Summarize the adapter wake payload.",
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: agent.AgentID,
			Timestamp:      now,
		},
		SessionChatType:      strings.TrimSpace(a.channelKind),
		SessionUserName:      "adapter",
		PromptContextErrHint: "load durable wake prompt context",
		PolicyReason:         "mapped from interactive face policy for durable wake channels",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert durable wake messages",
			LoadPlanState:   "load durable wake plan state before save",
			LoadOperation:   "load durable wake operation state before save",
			SaveSession:     "save durable wake session",
			RecordOutbound:  "record durable wake outbound reply",
		},
		SendErrCtx:   "send durable wake reply",
		RecordErrCtx: "record durable wake outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, msg core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			_ = policy
			return fmt.Sprintf("You are handling a durable-agent wake through a pluggable adapter.\nAgent: %s\nPayload: %s\nPending: %d", agent.AgentID, msg.Text, len(pending))
		},
		Finalize: func(turnSummary string) error {
			a.finalized = true
			a.lastSummary = strings.TrimSpace(turnSummary)
			if a.finalizeErr != nil {
				return a.finalizeErr
			}
			if !a.queueReview {
				return nil
			}
			_, err := durableagent.NewRuntime(rt.store).QueueReviewArtifact(agent, core.DurableReviewArtifact{
				AgentID:       strings.TrimSpace(agent.AgentID),
				Summary:       strings.TrimSpace(turnSummary),
				IntervalLabel: now.UTC().Format(time.RFC3339),
				LocalActions:  []string{"Processed durable wake payload through child-turn substrate."},
				Metadata: map[string]string{
					"channel_kind": strings.TrimSpace(agent.ChannelKind),
				},
			})
			return err
		},
	}, nil
}

func TestDurableWakeCommitsChildOutcomeBeforeFinalizeFailure(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = sender
	provider.replyText = "Completed before finalizer fails.\nREVIEW_STATUS: completed"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "idolum-finalize-fail",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Prove outcome commits before finalization.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	adapter := &testDurableWakeAdapter{
		channelKind: "test_adapter",
		finalizeErr: errors.New("test finalizer failed after outcome"),
	}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	rt.durableWakeChild = nil

	err = rt.pollDurableWakeAgents(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v, want committed outcome with non-durable finalizer warning", err)
	}
	if !adapter.finalized {
		t.Fatal("adapter finalize was not called")
	}
	key := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	events, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	var packetID string
	var resultID string
	for _, event := range events {
		if event.EventType != core.ExecutionEventDurableChildTaskResult {
			continue
		}
		var payload struct {
			PacketID string `json:"packet_id"`
			ResultID string `json:"result_id"`
		}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			t.Fatalf("unmarshal child task result payload: %v", err)
		}
		packetID = payload.PacketID
		resultID = payload.ResultID
	}
	if packetID == "" || resultID == "" {
		t.Fatalf("events = %#v, want child task result payload", events)
	}
	packet, ok, err := store.ChildTaskPacket(packetID)
	if err != nil {
		t.Fatalf("ChildTaskPacket() err = %v", err)
	}
	if !ok || packet.Status != session.ChildTaskPacketCompleted || packet.ResultID != resultID || packet.TerminalAt.IsZero() {
		t.Fatalf("packet = %#v ok=%t result_id=%s, want committed terminal outcome before finalizer error", packet, ok, resultID)
	}
	intents, err := store.ChildTaskOutcomeIntentsForResult(resultID)
	if err != nil {
		t.Fatalf("ChildTaskOutcomeIntentsForResult() err = %v", err)
	}
	for _, intent := range intents {
		if intent.Kind == session.ChildTaskOutcomeIntentGenericFinalize {
			t.Fatalf("intents = %#v, want no restart-irreparable generic finalizer intent", intents)
		}
	}
	if len(intents) != 1 || intents[0].Kind != session.ChildTaskOutcomeIntentPolicyApplied || intents[0].Status != session.ChildTaskOutcomeIntentApplied {
		t.Fatalf("intents = %#v, want applied durable policy intent only", intents)
	}
}

func TestDurableWakeScheduledReviewIntentRepairsAfterStoreReopen(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = provider
	_ = sender
	agent := core.DurableAgent{
		AgentID:            "idolum-scheduled-restart",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        scheduledReviewChannelKind,
		ChannelConfig: core.DurableAgentChannelConfig{
			ScheduledReview: &core.DurableAgentScheduledReviewChannelConfig{
				Title:        "Restart Review",
				TimeUTC:      "00:10",
				Window:       "previous_day",
				ArtifactKind: "scheduled_check_in",
			},
		},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Verify restart repair.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	key := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	now := time.Date(2026, 6, 24, 0, 20, 0, 0, time.UTC)
	packet, err := store.RecordChildTaskPacket(session.ChildTaskPacketInput{
		PacketID:  "child_task:scheduled_restart",
		AgentID:   agent.AgentID,
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claim, err := store.ClaimChildTaskAttempt(session.ChildTaskAttemptClaimInput{
		PacketID:       packet.PacketID,
		AttemptID:      "child_attempt:scheduled_restart",
		LeaseOwner:     "test_worker:scheduled_restart",
		AgentID:        agent.AgentID,
		Key:            key,
		ClaimedAt:      now.Add(time.Second),
		LeaseExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ClaimChildTaskAttempt() err = %v", err)
	}
	payload := scheduledReviewOutcomeIntentPayload{
		AgentID:        agent.AgentID,
		Config:         scheduledReviewConfigForAgent(agent),
		ReviewDate:     "2026-06-23",
		WindowStart:    time.Date(2026, 6, 23, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		WindowEnd:      time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
		MessageCount:   3,
		TranscriptPath: ".aphelion/scheduled-review/2026-06-23.md",
		TurnSummary:    "Restarted handler applied this scheduled review.",
		Status:         "completed",
		CreatedAt:      now.Format(time.RFC3339Nano),
	}
	raw, _ := json.Marshal(payload)
	result, err := store.CommitChildTaskOutcome(session.ChildTaskOutcomeCommitInput{
		Result: session.NormalizeChildTaskResultInput(session.ChildTaskResultInput{
			PacketID:        packet.PacketID,
			AttemptID:       claim.ActiveAttemptID,
			LeaseOwner:      claim.LeaseOwner,
			LeaseGeneration: claim.LeaseGeneration,
			FencingToken:    claim.FencingToken,
			AgentID:         agent.AgentID,
			Key:             key,
			Status:          session.ChildTaskResultCompleted,
			Summary:         "Restarted handler applied this scheduled review.",
			CreatedAt:       now.Add(2 * time.Second),
		}),
		OutcomeIntents: []session.ChildTaskOutcomeIntentInput{{
			Kind:        session.ChildTaskOutcomeIntentScheduledReview,
			Sequence:    10,
			PayloadJSON: string(raw),
			ResultRef:   "scheduled_review:" + agent.AgentID,
			CreatedAt:   now.Add(2 * time.Second),
		}},
		ResolvedAt: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CommitChildTaskOutcome() err = %v", err)
	}
	if result.ResultID == "" {
		t.Fatal("CommitChildTaskOutcome() returned empty result id")
	}
	intents, err := store.ChildTaskOutcomeIntentsForResult(result.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskOutcomeIntentsForResult(before reopen) err = %v", err)
	}
	if len(intents) != 1 {
		t.Fatalf("intents before reopen = %#v, want one scheduled-review intent", intents)
	}
	if _, ok, err := store.ClaimChildTaskOutcomeIntent(session.ChildTaskOutcomeIntentClaimInput{
		IntentID:       intents[0].IntentID,
		LeaseOwner:     "test_worker:crashed_after_claim",
		ClaimedAt:      time.Now().UTC().Add(-2 * time.Second),
		LeaseExpiresAt: time.Now().UTC().Add(-time.Second),
	}); err != nil || !ok {
		t.Fatalf("ClaimChildTaskOutcomeIntent(crashed) ok=%t err=%v", ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	reopened, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer reopened.Close()
	rt, err := New(cfg, reopened, provider, nil, sender)
	if err != nil {
		t.Fatalf("New(reopened) err = %v", err)
	}
	if err := rt.processPendingDurableWakeOutcomeIntents(context.Background(), 100); err != nil {
		t.Fatalf("processPendingDurableWakeOutcomeIntents() err = %v", err)
	}
	events, err := reopened.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Summary, "Restarted handler applied") {
		t.Fatalf("PendingReviewEvents() = %#v, want replayed scheduled review artifact", events)
	}
	intents, err = reopened.ChildTaskOutcomeIntentsForResult(result.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskOutcomeIntentsForResult() err = %v", err)
	}
	if len(intents) != 1 || intents[0].Status != session.ChildTaskOutcomeIntentApplied {
		t.Fatalf("intents = %#v, want applied scheduled review intent after restart", intents)
	}
	if err := rt.applyScheduledReviewOutcomeIntent(intents[0]); err != nil {
		t.Fatalf("applyScheduledReviewOutcomeIntent(replay) err = %v", err)
	}
	events, err = reopened.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents(after replay) err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("PendingReviewEvents(after replay) = %#v, want idempotent single artifact", events)
	}
	state, err := reopened.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if len(continuity.ReviewRefs) != 1 || continuity.ReviewRefs[0].ReviewEventID != events[0].ID {
		t.Fatalf("continuity review refs = %#v, want one idempotent review ref for event %d", continuity.ReviewRefs, events[0].ID)
	}
}

func TestDurableWakeFailedOutcomeRecordsPolicyFailureAfterStoreReopen(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	_ = provider
	_ = sender
	agent := core.DurableAgent{
		AgentID:            "idolum-policy-failure-restart",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Verify failed outcome policy state is repairable.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	key := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	now := time.Date(2026, 6, 24, 1, 20, 0, 0, time.UTC)
	packet, err := store.RecordChildTaskPacket(session.ChildTaskPacketInput{
		PacketID:  "child_task:policy_failure_restart",
		AgentID:   agent.AgentID,
		Key:       key,
		TaskKind:  "durable_wake",
		InputJSON: `{}`,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RecordChildTaskPacket() err = %v", err)
	}
	claim, err := store.ClaimChildTaskAttempt(session.ChildTaskAttemptClaimInput{
		PacketID:       packet.PacketID,
		AttemptID:      "child_attempt:policy_failure_restart",
		LeaseOwner:     "test_worker:policy_failure_restart",
		AgentID:        agent.AgentID,
		Key:            key,
		ClaimedAt:      now.Add(time.Second),
		LeaseExpiresAt: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ClaimChildTaskAttempt() err = %v", err)
	}
	wakeErr := errors.New("provider failed after child admission")
	result, err := store.CommitChildTaskOutcome(session.ChildTaskOutcomeCommitInput{
		Result: session.NormalizeChildTaskResultInput(session.ChildTaskResultInput{
			PacketID:        packet.PacketID,
			AttemptID:       claim.ActiveAttemptID,
			LeaseOwner:      claim.LeaseOwner,
			LeaseGeneration: claim.LeaseGeneration,
			FencingToken:    claim.FencingToken,
			AgentID:         agent.AgentID,
			Key:             key,
			Status:          session.ChildTaskResultFailed,
			NextState:       session.NextActionBlockedNeedsResourceRepair,
			Summary:         "Child wake failed before model completion.",
			ErrorText:       wakeErr.Error(),
			CreatedAt:       now.Add(2 * time.Second),
		}),
		OutcomeIntents: durableWakeOutcomeIntentInputs(agent, durableWakeTurnPlan{}, nil, session.ChildTaskResultFailed, "Child wake failed before model completion.", wakeErr, now.Add(2*time.Second)),
		ResolvedAt:     now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CommitChildTaskOutcome() err = %v", err)
	}
	if result.ResultID == "" {
		t.Fatal("CommitChildTaskOutcome() returned empty result id")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() err = %v", err)
	}
	reopened, err := session.NewSQLiteStore(cfg.Sessions.DBPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen) err = %v", err)
	}
	defer reopened.Close()
	rt, err := New(cfg, reopened, provider, nil, sender)
	if err != nil {
		t.Fatalf("New(reopened) err = %v", err)
	}
	if err := rt.processPendingDurableWakeOutcomeIntents(context.Background(), 100); err != nil {
		t.Fatalf("processPendingDurableWakeOutcomeIntents() err = %v", err)
	}
	state, err := reopened.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if state.LastApplyStatus != "failed" || !strings.Contains(state.LastApplyError, "provider failed") {
		t.Fatalf("DurableAgentState() = %#v, want recovered policy failure marker", state)
	}
	intents, err := reopened.ChildTaskOutcomeIntentsForResult(result.ResultID)
	if err != nil {
		t.Fatalf("ChildTaskOutcomeIntentsForResult() err = %v", err)
	}
	if len(intents) != 1 || intents[0].Kind != session.ChildTaskOutcomeIntentPolicyApplyFailed || intents[0].Status != session.ChildTaskOutcomeIntentApplied {
		t.Fatalf("intents = %#v, want applied policy failure intent", intents)
	}
}

func TestPollDurableWakeAgentsUsesPluggableIngressAdapter(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Pluggable adapter wake summary."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-test-adapter",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle test adapter wakes.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	adapter := &testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	rt.durableWakeChild = nil

	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}
	if adapter.prepareCalls != 1 {
		t.Fatalf("adapter prepare calls = %d, want 1", adapter.prepareCalls)
	}
	if !adapter.finalized {
		t.Fatal("adapter finalize was not called")
	}
	if !strings.Contains(adapter.lastSummary, "Pluggable adapter wake summary.") {
		t.Fatalf("adapter last summary = %q, want provider summary", adapter.lastSummary)
	}

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate durable review relay", got)
	}
	if sender.inline[0].chatID != 1001 {
		t.Fatalf("inline chat_id = %d, want 1001", sender.inline[0].chatID)
	}
	if !strings.Contains(sender.inline[0].text, "**Review: idolum-test-adapter**") {
		t.Fatalf("inline text = %q, want review digest relay", sender.inline[0].text)
	}
	sender.mu.Unlock()

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents() len = %d, want 0 after immediate relay", len(events))
	}

	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(durable wake session) err = %v", err)
	}
	if sess.TurnCount == 0 {
		t.Fatalf("durable wake session turn_count = %d, want > 0", sess.TurnCount)
	}
	eventsBySession, err := store.ExecutionEventsBySession(key, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(durable wake session) err = %v", err)
	}
	if !containsExecutionEventType(eventsBySession, core.ExecutionEventDurableWakeStarted) {
		t.Fatalf("durable wake events missing started signal: %#v", eventsBySession)
	}
	if !containsExecutionEventType(eventsBySession, core.ExecutionEventDurableWakeCompleted) {
		t.Fatalf("durable wake events missing completed signal: %#v", eventsBySession)
	}
}

func TestPollDurableWakeAgentsUsesChildExecutorWhenBootstrapConfigured(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Child-executor wake summary."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-child-executor",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle test adapter wakes.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	adapter := &testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true}
	childRuns := 0
	rt.durableWakeAdapters = []durableWakeIngressAdapter{adapter}
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, scope sandbox.Scope, child core.DurableAgent, now time.Time) error {
		_ = scope
		_ = now
		if strings.TrimSpace(child.AgentID) != agent.AgentID {
			t.Fatalf("child executor agent_id = %q, want %q", child.AgentID, agent.AgentID)
		}
		childRuns++
		return nil
	}}

	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}
	if childRuns != 1 {
		t.Fatalf("child executor runs = %d, want 1", childRuns)
	}
	if adapter.prepareCalls != 0 {
		t.Fatalf("adapter prepare calls = %d, want 0 when child executor handles wake", adapter.prepareCalls)
	}
}

func TestPollDurableWakeAgentsStillReturnsGenericChildExecutorErrors(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "unused because child executor fails"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	agent := core.DurableAgent{
		AgentID:            "generic-child-executor-failure",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle test adapter wakes.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{&testDurableWakeAdapter{channelKind: "test_adapter"}}
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, _ core.DurableAgent, _ time.Time) error {
		return fmt.Errorf("generic child executor failed")
	}}

	err = rt.pollDurableWakeAgents(context.Background(), time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "generic child executor failed") {
		t.Fatalf("pollDurableWakeAgents() err = %v, want generic child executor failure", err)
	}
}

func TestPollDurableWakeAgentsRunsIndependentChildrenConcurrently(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	for _, agentID := range []string{"slow-child", "ready-child"} {
		if err := store.UpsertDurableAgent(core.DurableAgent{
			AgentID:      agentID,
			ChannelKind:  "test_adapter",
			BootstrapLLM: durableGroupTestBootstrapLLM(),
			WakeupMode:   "poll",
			Status:       "active",
		}); err != nil {
			t.Fatalf("UpsertDurableAgent(%s) err = %v", agentID, err)
		}
	}
	rt.durableWakeAdapters = []durableWakeIngressAdapter{&testDurableWakeAdapter{channelKind: "test_adapter"}}
	slowStarted := make(chan struct{})
	readyRan := make(chan struct{})
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(ctx context.Context, _ sandbox.Scope, child core.DurableAgent, _ time.Time) error {
		switch child.AgentID {
		case "slow-child":
			close(slowStarted)
			<-ctx.Done()
			return ctx.Err()
		case "ready-child":
			close(readyRan)
			return nil
		default:
			return fmt.Errorf("unexpected child %s", child.AgentID)
		}
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- rt.pollDurableWakeAgents(ctx, time.Now().UTC())
	}()
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow child did not start")
	}
	select {
	case <-readyRan:
	case <-time.After(time.Second):
		t.Fatal("ready child was starved behind slow child")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pollDurableWakeAgents did not return after context cancellation")
	}
}

func TestPollDurableWakeAgentsDeliversReviewEventsAfterChildExecutorWake(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "unused in child executor path"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-child-relay",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "test_adapter",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Relay child review artifacts upward immediately.",
			CapabilityEnvelope: []string{"bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	rt.durableWakeAdapters = []durableWakeIngressAdapter{
		&testDurableWakeAdapter{channelKind: "test_adapter", queueReview: true},
	}
	rt.durableWakeChild = inlineDurableWakeChildExecutor{run: func(_ context.Context, _ sandbox.Scope, child core.DurableAgent, now time.Time) error {
		_, queueErr := durableagent.NewRuntime(store).QueueReviewArtifact(child, core.DurableReviewArtifact{
			AgentID:       child.AgentID,
			Summary:       "child executor completed a bounded review",
			IntervalLabel: now.UTC().Format(time.RFC3339),
			LocalActions:  []string{"Processed one parent message."},
		})
		return queueErr
	}}

	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate relay after child wake", got)
	}
	if sender.inline[0].chatID != 1001 {
		t.Fatalf("inline chat_id = %d, want 1001", sender.inline[0].chatID)
	}
	if !strings.Contains(sender.inline[0].text, "**Review: idolum-child-relay**") {
		t.Fatalf("inline text = %q, want review digest relay", sender.inline[0].text)
	}
	sender.mu.Unlock()

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents() len = %d, want 0 after immediate relay", len(events))
	}
}

func TestDurableTurnInferenceUnavailableUsesProviderFailure(t *testing.T) {
	result := &turn.Result{Turn: &core.TurnResult{ProviderFailure: "codex: server_is_overloaded"}}
	if !durableTurnInferenceUnavailable(result, "ordinary visible text") {
		t.Fatal("durableTurnInferenceUnavailable() = false, want provider failure to count structurally")
	}
	if durableTurnInferenceUnavailable(&turn.Result{Turn: &core.TurnResult{}}, "ordinary visible text") {
		t.Fatal("durableTurnInferenceUnavailable() = true, want false without provider failure or current visible signal")
	}
}

func TestDurableTurnInferenceUnavailableRecognizesExactCurrentFallbackBodyOnly(t *testing.T) {
	result := &turn.Result{Turn: &core.TurnResult{}}

	if !durableTurnInferenceUnavailable(result, durableWakeInferenceUnavailableFallback) {
		t.Fatalf("durableTurnInferenceUnavailable(%q) = false, want exact current fallback body to count", durableWakeInferenceUnavailableFallback)
	}

	for _, summary := range []string{
		durableWakeInferenceUnavailableSignal,
		durableWakeInferenceUnavailableSignal + " This turn did not complete.",
		durableWakeInferenceUnavailableSignal + " Child report begins with the sentinel but then continues successfully.",
	} {
		if durableTurnInferenceUnavailable(result, summary) {
			t.Fatalf("durableTurnInferenceUnavailable(%q) = true, want false unless the full fallback body matches", summary)
		}
	}
}

func TestDurableTurnInferenceUnavailableIgnoresQuotedHistoricalSentinel(t *testing.T) {
	result := &turn.Result{Turn: &core.TurnResult{}}
	summary := strings.Join([]string{
		"Daily review succeeded.",
		"",
		"Prior failure evidence:",
		`"` + durableWakeInferenceUnavailableSignal + ` This turn did not complete."`,
	}, "\n")

	if durableTurnInferenceUnavailable(result, summary) {
		t.Fatalf("durableTurnInferenceUnavailable() = true for quoted historical sentinel; visible evidence must not become control state")
	}
}

func TestPollDurableWakeAgentsKeepsParentConversationPendingOnInferenceFailure(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Inference backend is unavailable. This turn did not complete. You can /stop to cancel current work and try again."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "idolum-retry",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Apply parent guidance when inference is available.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Please process the latest parent note.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   agent.AgentID,
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeChild = nil
	err = rt.pollDurableWakeAgents(context.Background(), time.Now().UTC())
	if err == nil {
		t.Fatal("pollDurableWakeAgents() err = nil, want durable wake inference unavailable")
	}
	if !strings.Contains(err.Error(), "durable wake inference unavailable") {
		t.Fatalf("pollDurableWakeAgents() err = %v, want durable wake inference unavailable", err)
	}

	updatedState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	updatedContinuity, err := core.ParseDurableAgentContinuityState(updatedState.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if pending := updatedContinuity.PendingParentConversationMessages(10); len(pending) != 1 {
		t.Fatalf("pending parent messages = %d, want 1 after transient inference failure", len(pending))
	}
	if updatedState.LastApplyStatus != "failed" {
		t.Fatalf("last_apply_status = %q, want failed", updatedState.LastApplyStatus)
	}
	if strings.TrimSpace(updatedState.LastApplyError) == "" {
		t.Fatalf("last_apply_error = %q, want non-empty failure reason", updatedState.LastApplyError)
	}

	sender.mu.Lock()
	if got := len(sender.sent); got != 0 {
		t.Fatalf("sent len = %d, want 0 review digests when wake failed before ack", got)
	}
	sender.mu.Unlock()
}

func TestPollDurableWakeAgentsDispatchesGenericExternalChannelWithoutSpecializedParentSemantics(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	useTrustedDurableAgentSandboxForWakeTest(t, cfg)
	provider.replyText = "The configured adapter runtime material is unavailable; I need a child_runtime grant."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "external_channel",
		ChannelConfig: core.DurableAgentChannelConfig{External: &core.DurableAgentExternalChannelConfig{
			Address:      "external-endpoint",
			Adapter:      "child_adapter",
			Query:        "topic:important",
			PollInterval: "5m",
		}},
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Handle the external channel and summarize important findings upward.",
			CapabilityEnvelope: []string{"read_channel", "bounded_review_artifact"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	markDurableWakeExternalAdapterReady(t, store, agent.AgentID, "child_adapter")

	rt.durableWakeChild = nil
	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}
	if len(provider.lastGovernorMsgs) == 0 {
		t.Fatal("governor messages empty, want generic external-channel wake")
	}
	joined := strings.ToLower(fmt.Sprint(provider.lastGovernorMsgs))
	if !strings.Contains(joined, "generic external_channel adapter dispatcher") {
		t.Fatalf("governor messages = %#v, want generic dispatcher context", provider.lastGovernorMsgs)
	}
	for _, forbidden := range []string{"gmail", "gog", "recruiter", "job"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("governor messages = %#v, should not contain specialized term %q", provider.lastGovernorMsgs, forbidden)
		}
	}
}

func TestPollDurableWakeAgentsConsumesPendingParentConversationForAnyChannel(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Processed the parent guidance and compiled the requested summary.\nREVIEW_STATUS: completed"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-alpha",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent requests over channel artifacts and summarize upward.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Summarize the most relevant job links.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:   agent.AgentID,
		StateJSON: raw,
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeChild = nil
	if err := rt.pollDurableWakeAgents(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	foundParentContext := false
	for _, systemPrompt := range provider.seenGovernorSystem {
		if strings.Contains(systemPrompt, "Parent note 1: Summarize the most relevant job links.") {
			foundParentContext = true
			break
		}
	}
	if !foundParentContext {
		t.Fatalf("governor prompts = %#v, want pending parent note context", provider.seenGovernorSystem)
	}

	updatedState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	updatedContinuity, err := core.ParseDurableAgentContinuityState(updatedState.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if pending := updatedContinuity.PendingParentConversationMessages(10); len(pending) != 0 {
		t.Fatalf("pending parent messages = %d, want 0 after wake", len(pending))
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents() len = %d, want 0 after immediate relay", len(events))
	}

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate parent-conversation review relay", got)
	}
	if !strings.Contains(sender.inline[0].text, "**Review: child-alpha**") || !strings.Contains(sender.inline[0].text, "Processed pending parent guidance") {
		t.Fatalf("inline text = %q, want parent conversation ack summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "headless") || strings.Contains(sender.inline[0].text, "channel=headless") {
		t.Fatalf("inline text = %q, want human channel context without raw metadata", sender.inline[0].text)
	}
	sender.mu.Unlock()
}

func TestRunDurableAgentChildWakeSkipsWhenAgentAlreadyAwake(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "This should not run while another wake owns the agent."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:            "child-awake",
		ParentScopeKind:    "telegram_dm",
		ParentScopeID:      "1001",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Process parent requests over channel artifacts and summarize upward.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	continuity := core.DurableAgentContinuityState{}
	continuity = continuity.WithConversationMessage("parent", "Handle exactly once.", time.Now().UTC().Add(-time.Minute))
	raw, err := continuity.Marshal()
	if err != nil {
		t.Fatalf("continuity.Marshal() err = %v", err)
	}
	if err := store.SaveDurableAgentState(core.DurableAgentState{
		AgentID:    agent.AgentID,
		Status:     "awake",
		StateJSON:  raw,
		LastWakeAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveDurableAgentState() err = %v", err)
	}

	rt.durableWakeChild = nil
	if err := rt.RunDurableAgentChildWake(context.Background(), agent.AgentID, time.Now().UTC()); err != nil {
		t.Fatalf("RunDurableAgentChildWake() err = %v", err)
	}
	if len(provider.seenGovernorSystem) != 0 {
		t.Fatalf("governor prompts = %#v, want no child turn while agent is already awake", provider.seenGovernorSystem)
	}
	updatedState, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	updatedContinuity, err := core.ParseDurableAgentContinuityState(updatedState.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	pending := updatedContinuity.PendingParentConversationMessages(10)
	if len(pending) != 1 {
		t.Fatalf("pending parent messages = %d, want still pending after skipped wake", len(pending))
	}
	taskPacketID := pending[0].MessageID
	packet, ok, err := store.ChildTaskPacket(taskPacketID)
	if err != nil {
		t.Fatalf("ChildTaskPacket(skipped wake) err = %v", err)
	}
	if !ok {
		t.Fatalf("ChildTaskPacket(%s) ok=false, want queued/claimed packet", taskPacketID)
	}
	if packet.Status != session.ChildTaskPacketInProgress || packet.LeaseReleasedAt.IsZero() {
		t.Fatalf("skipped wake packet = %#v, want in-progress packet with released attempt lease", packet)
	}
	wakeKey := session.SessionKey{ChatID: durableWakeSyntheticChatID(agent.AgentID), Scope: durableAgentScopeRef(agent)}
	open, err := store.OpenNextActionsBySession(wakeKey, 10)
	if err != nil {
		t.Fatalf("OpenNextActionsBySession(skipped wake) err = %v", err)
	}
	for _, action := range open {
		if action.SubjectKind == "task_packet" && action.SubjectRef == taskPacketID && action.State == session.NextActionWaitingForChild {
			t.Fatalf("open next actions = %#v, want no stale waiting_for_child action for skipped packet", open)
		}
	}
	claimAt := time.Now().UTC()
	secondClaim, err := store.ClaimChildTaskAttempt(session.ChildTaskAttemptClaimInput{
		PacketID:       taskPacketID,
		AttemptID:      "child_attempt:post-skip-claim",
		LeaseOwner:     "test_worker:post-skip",
		Key:            wakeKey,
		ClaimedAt:      claimAt,
		LeaseExpiresAt: claimAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ClaimChildTaskAttempt(after skipped release) err = %v", err)
	}
	if secondClaim.LeaseGeneration <= packet.LeaseGeneration || secondClaim.LeaseOwner != "test_worker:post-skip" || secondClaim.LeaseReleasedAt.IsZero() == false {
		t.Fatalf("second claim after skip = %#v previous = %#v, want later owner to claim released packet", secondClaim, packet)
	}
	events, err := store.ExecutionEventsBySession(rt.durableAgentExecutionKey(agent.AgentID), 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !containsExecutionEventType(events, core.ExecutionEventDurableWakeSkipped) {
		t.Fatalf("execution events = %#v, want durable wake skipped event", events)
	}
}
