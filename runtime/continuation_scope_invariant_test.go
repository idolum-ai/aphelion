//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestContinuationPromptInboundForKeyPreservesThreadScope(t *testing.T) {
	t.Parallel()

	key := session.SessionKey{ChatID: 9101, UserID: 0, Scope: telegramThreadScopeRef(9101, 7)}
	msg := continuationPromptInboundForKey(key, "refresh scoped prompt", core.InboundOriginTurnAuthorization, string(session.TurnAuthorizationKindContinuation))
	if msg.ChatID != 9101 || msg.TelegramThreadID != 7 {
		t.Fatalf("message scope = chat:%d thread:%d, want 9101/7", msg.ChatID, msg.TelegramThreadID)
	}
	if msg.Text != "refresh scoped prompt" || msg.Origin != core.InboundOriginTurnAuthorization || msg.OriginDetail != string(session.TurnAuthorizationKindContinuation) {
		t.Fatalf("message = %#v, want prompt text and turn-authorization origin", msg)
	}
}

func TestContinuationPromptInboundForKeyUsesDMWhenNoThreadScope(t *testing.T) {
	t.Parallel()

	key := session.SessionKey{ChatID: 9102, UserID: 0, Scope: telegramDMScopeRef(9102)}
	msg := continuationPromptInboundForKey(key, "refresh dm prompt", core.InboundOriginTurnAuthorization, "")
	if msg.ChatID != 9102 || msg.TelegramThreadID != 0 {
		t.Fatalf("message scope = chat:%d thread:%d, want 9102/default", msg.ChatID, msg.TelegramThreadID)
	}
}

func TestTriggerContinuationNativeWorkExecutorPreservesThreadScope(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued in thread"}}
	rt.interactiveDMAssembler = recorder
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "native"}, []WorkExecutor{nativeWorkExecutor{runtime: rt}})

	thread, _, err := store.CreateTelegramThreadForUpdate(9103, 1001, 301, 401, "thread continuation work", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9103, UserID: 0, Scope: telegramThreadScopeRef(9103, thread.ThreadID)}
	state := approvedReadOnlyContinuationStateForScopeTest("thread-native-work", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called")
	}
	if recorder.input.Msg.TelegramThreadID != thread.ThreadID {
		t.Fatalf("continued message thread = %d, want %d", recorder.input.Msg.TelegramThreadID, thread.ThreadID)
	}
	if recorder.input.Key.Scope.Kind != session.ScopeKindTelegramThread || recorder.input.Key.Scope.ID != key.Scope.ID {
		t.Fatalf("continued key scope = %#v, want thread scope %#v", recorder.input.Key.Scope, key.Scope)
	}
	if recorder.input.Msg.Origin != core.InboundOriginTurnAuthorization || recorder.input.Msg.OriginDetail != string(session.TurnAuthorizationKindContinuation) {
		t.Fatalf("continued origin = (%q,%q), want turn_authorization/continuation", recorder.input.Msg.Origin, recorder.input.Msg.OriginDetail)
	}
}

func TestTriggerContinuationNativeWorkExecutorPreservesDMScope(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued in dm"}}
	rt.interactiveDMAssembler = recorder
	rt.workExecutor = newWorkExecutorSelector(config.WorkConfig{Executor: "native"}, []WorkExecutor{nativeWorkExecutor{runtime: rt}})

	key := session.SessionKey{ChatID: 9104, UserID: 0, Scope: telegramDMScopeRef(9104)}
	state := approvedReadOnlyContinuationStateForScopeTest("dm-native-work", time.Now().UTC())
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called")
	}
	if recorder.input.Msg.TelegramThreadID != 0 {
		t.Fatalf("continued message thread = %d, want default DM", recorder.input.Msg.TelegramThreadID)
	}
	if recorder.input.Key.Scope.Kind != session.ScopeKindTelegramDM || recorder.input.Key.Scope.ID != key.Scope.ID {
		t.Fatalf("continued key scope = %#v, want DM scope %#v", recorder.input.Key.Scope, key.Scope)
	}
}

func TestNativeWorkExecutorFallsBackToDMScopeOnlyWithoutRequestKey(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "continued"}}
	rt.interactiveDMAssembler = recorder

	_, err = nativeWorkExecutor{runtime: rt}.Run(context.Background(), WorkRequest{
		ChatID: 9105,
		Actor:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		State:  approvedReadOnlyContinuationStateForScopeTest("legacy-native-work", time.Now().UTC()),
	})
	if err != nil {
		t.Fatalf("nativeWorkExecutor.Run() err = %v", err)
	}
	if !recorder.called {
		t.Fatal("interactive assembler not called")
	}
	if recorder.input.Msg.ChatID != 9105 || recorder.input.Msg.TelegramThreadID != 0 {
		t.Fatalf("legacy message scope = chat:%d thread:%d, want DM fallback", recorder.input.Msg.ChatID, recorder.input.Msg.TelegramThreadID)
	}
	if recorder.input.Key.Scope.Kind != session.ScopeKindTelegramDM {
		t.Fatalf("legacy key scope = %#v, want DM fallback", recorder.input.Key.Scope)
	}
}

func approvedReadOnlyContinuationStateForScopeTest(id string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	action := session.ActionProposal{
		ID:             "aprop-" + id,
		Summary:        "Run scoped read-only continuation",
		RiskClass:      "read_only_review",
		AllowedActions: []string{"read_only"},
		Status:         session.ProposalStatusApproved,
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, 1, now)
	lease.Status = session.ContinuationLeaseStatusActive
	lease.RemainingTurns = 1
	lease.ApprovedBy = 1001
	lease.ApprovedAt = now
	return session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        id,
		Objective:         "Preserve conversation scope.",
		StageSummary:      "Run scoped read-only continuation.",
		RemainingTurns:    1,
		ApprovedBy:        1001,
		ActionProposal:    action,
		ContinuationLease: lease,
	}
}
