//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

func TestNativeWorkExecutorCarriesAuthorityAdmissionIntoInternalTurn(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "native work used internal turn"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 9301, UserID: 1001, Scope: telegramDMScopeRef(9301)}
	state := approvedReadOnlyContinuationStateForScopeTest("native-authority-continuity", now)
	state.ContinuationLease.ID = "lease-native-authority-continuity"
	op := session.OperationState{
		ID:        "op-native-authority-continuity",
		Objective: "Exercise native work continuity.",
		Status:    session.OperationStatusActive,
	}

	_, err = nativeWorkExecutor{runtime: rt}.Run(context.Background(), WorkRequest{
		Key:       key,
		ChatID:    key.ChatID,
		Actor:     principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		State:     state,
		Operation: op,
	})
	if err != nil {
		t.Fatalf("nativeWorkExecutor.Run() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("internal continuation assembler was not called")
	}
	admission, ok := toolpkg.ExecutionAuthorityAdmissionFromContext(recorder.ctx)
	if !ok {
		t.Fatal("ExecutionAuthorityAdmissionFromContext() ok=false, want native work to carry authority admission")
	}
	if admission.SessionID != session.SessionIDForKey(key) {
		t.Fatalf("admission.SessionID = %q, want %q", admission.SessionID, session.SessionIDForKey(key))
	}
	if admission.TurnRunID != 0 {
		t.Fatalf("admission.TurnRunID = %d, want pre-turn admission without run identity", admission.TurnRunID)
	}
	if admission.ContinuationLeaseID != "lease-native-authority-continuity" || admission.LeaseKind != session.ExecutionAuthorityLeaseKindContinuation {
		t.Fatalf("admission = %#v, want native continuation lease", admission)
	}
}

func TestNativeWorkExecutorDoesNotTransportStaleLeaseEvidence(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "native work without lease ref"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 9302, UserID: 1001, Scope: telegramDMScopeRef(9302)}
	state := approvedReadOnlyContinuationStateForScopeTest("native-stale-authority", now)
	state.ContinuationLease.ID = "lease-native-stale-authority"
	state.ContinuationLease.ExpiresAt = now.Add(-time.Minute)
	op := session.OperationState{
		ID:        "op-native-stale-authority",
		Objective: "Exercise stale native work continuity.",
		Status:    session.OperationStatusActive,
		PlanLease: session.OperationPlanLease{
			ID:             "plan-lease-native-stale-authority",
			Status:         session.PlanLeaseStatusRevoked,
			TurnBudget:     1,
			RemainingTurns: 1,
			ExpiresAt:      now.Add(time.Hour),
		},
	}

	_, err = nativeWorkExecutor{runtime: rt}.Run(context.Background(), WorkRequest{
		Key:       key,
		ChatID:    key.ChatID,
		Actor:     principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		State:     state,
		Operation: op,
	})
	if err != nil {
		t.Fatalf("nativeWorkExecutor.Run() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("internal continuation assembler was not called")
	}
	if ref, ok := toolpkg.AuthorityUseRefFromContext(recorder.ctx); ok {
		t.Fatalf("AuthorityUseRefFromContext() = %#v, want stale lease evidence not transported", ref)
	}
}

func TestStartTurnMonitorFailsRunWhenAuthorityBindingFails(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, &fakeProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9303, UserID: 1001, Scope: telegramDMScopeRef(9303)}
	ctx := toolpkg.WithExecutionAuthorityAdmission(context.Background(), session.ExecutionRunAuthority{
		Principal:           "telegram:1001",
		PrincipalRole:       string(principal.RoleAdmin),
		ExecutionSpecies:    "native_continuation",
		LeaseKind:           session.ExecutionAuthorityLeaseKindContinuation,
		ContinuationLeaseID: "missing-authority-lease",
		LeaseStatus:         string(session.ContinuationLeaseStatusActive),
		LeaseRemainingTurns: 1,
		LeaseExpiresAt:      time.Now().UTC().Add(time.Hour),
	})

	monitor, err := rt.startTurnMonitor(ctx, key, session.TurnRunKindInteractive, "bind missing authority", nil, nil, core.InboundMessage{ChatID: key.ChatID, SenderID: 1001})
	if err == nil {
		t.Fatal("startTurnMonitor() err = nil, want authority binding failure")
	}
	if monitor != nil {
		t.Fatalf("startTurnMonitor() monitor = %#v, want nil on authority binding failure", monitor)
	}
	if !strings.Contains(err.Error(), "not durable for run authority") {
		t.Fatalf("startTurnMonitor() err = %v, want missing lease binding failure", err)
	}
	run, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun() err = %v", err)
	}
	if run == nil || run.Status != session.TurnRunStatusFailed {
		t.Fatalf("latest run = %#v, want failed admission run", run)
	}
	if !strings.Contains(run.ErrorText, "missing-authority-lease") {
		t.Fatalf("run error = %q, want authority binding reason", run.ErrorText)
	}
}

func TestDurableGroupTurnDoesNotExposeParentToolAuthorityByDefault(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "group ok"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:                "authority-child-group",
		ParentScopeKind:        string(session.ScopeKindHeartbeat),
		ParentScopeID:          "admin-house",
		ReviewTargetChatID:     1001,
		ChannelKind:            "telegram_group",
		AllowedTelegramUserIDs: []int64{555},
		LivePolicy: core.DurableAgentLivePolicy{
			Charter:      "Speak in the group without inheriting parent tool authority.",
			OutboundMode: "reply_with_policy_authorization",
			DriftPolicy:  "admin_review",
		},
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		Status:       "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}
	rt.durableGroupChild = inlineDurableGroupChildExecutor{run: rt.RunDurableTelegramGroupChild}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:         -100230,
		ChatType:       "group",
		ChatTitle:      "Authority Group",
		SenderID:       555,
		SenderName:     "alice",
		Text:           "try normal durable group turn",
		MessageID:      15,
		DurableAgentID: "authority-child-group",
		Timestamp:      time.Now(),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}
	if len(provider.lastGovernorTools) != 0 {
		t.Fatalf("lastGovernorTools len = %d, want durable group turn with no tools exposed", len(provider.lastGovernorTools))
	}
	key := session.SessionKey{ChatID: -100230, Scope: durableAgentScopeRef(core.DurableAgent{
		AgentID:         "authority-child-group",
		ParentScopeKind: string(session.ScopeKindHeartbeat),
		ParentScopeID:   "admin-house",
	})}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(durable group session) err = %v", err)
	}
	if sess.Scope.Kind != session.ScopeKindDurableAgent || sess.Scope.DurableAgentID != "authority-child-group" {
		t.Fatalf("durable group session scope = %#v, want durable-agent scoped session", sess.Scope)
	}
}

func TestScheduledJobAuthorityContinuityUsesDedicatedSessionScope(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "scheduled canonical"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	job := scheduledJob{
		ID:      "authority-continuity",
		Kind:    scheduledJobKind("local_review"),
		Every:   "30m",
		Prompt:  "Inspect authority continuity posture.",
		Enabled: true,
		Delivery: scheduledJobDelivery{
			Mode: normalizeScheduledDeliveryMode("local_artifact"),
		},
	}
	if err := rt.runScheduledJobOnce(context.Background(), job); err != nil {
		t.Fatalf("runScheduledJobOnce() err = %v", err)
	}
	key := scheduledJobSessionKey(job)
	if key.ChatID >= 0 || key.Scope.Kind != scheduledJobScopeKind || key.Scope.ID != "authority-continuity" {
		t.Fatalf("scheduled key = %#v, want isolated scheduled job scope", key)
	}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(scheduled job session) err = %v", err)
	}
	if sess.Scope != key.Scope {
		t.Fatalf("scheduled session scope = %#v, want %#v", sess.Scope, key.Scope)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(scheduled) err = %v", err)
	}
	payload := payloadForEventType(events, core.ExecutionEventTurnStarted)
	requestText, _ := payload["request_text"].(string)
	if !strings.Contains(requestText, "Scheduled job run: authority-continuity") {
		t.Fatalf("turn.started request_text = %q, want scheduled job identity", requestText)
	}
}
