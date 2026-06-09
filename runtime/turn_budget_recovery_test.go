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
	"github.com/idolum-ai/aphelion/turn"
)

func TestBudgetRecoveryDeliverySuppressesFinalReplyAndSchedulesInternalContinuation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "recovered final"}}
	rt.interactiveDMAssembler = recorder

	key := session.SessionKey{ChatID: 9711, UserID: 0, Scope: telegramDMScopeRef(9711)}
	msg := core.InboundMessage{
		ChatID:       9711,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "finish the current phase",
		MessageID:    42,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	}
	opState := budgetRecoveryTestOperationState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	hookCalls := 0
	port := &turnDeliveryPort{
		runtime: rt,
		key:     key,
		msg:     msg,
		deliver: true,
		hooks: turnCommitHooks{
			QueueReviewEvents: func(*turn.Result) error {
				hookCalls++
				return nil
			},
			PostReplyContinuationUI: func(context.Context, *turn.Result) error {
				hookCalls++
				return nil
			},
		},
	}
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}

	got, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: msg.ChatID, Text: turnBudgetRecoveryHandoffText(recovery)},
		Result: &turn.Result{
			Turn:           &core.TurnResult{Text: turnBudgetRecoveryHandoffText(recovery), Recovery: recovery},
			VisibleReply:   turnBudgetRecoveryHandoffText(recovery),
			OperationState: opState,
		},
	})
	if err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}
	if got == nil || got.Kind != "budget_recovery_scheduled" {
		t.Fatalf("Deliver() = %#v, want budget recovery scheduled", got)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.WaitForBackgroundLoops(waitCtx); err != nil {
		t.Fatalf("WaitForBackgroundLoops() err = %v", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %#v, want no visible blocker before recovery turn", sender.sent)
	}
	if hookCalls != 0 {
		t.Fatalf("hookCalls = %d, want normal post-commit hooks suppressed", hookCalls)
	}
	if recorder.callCount != 1 {
		t.Fatalf("recovery assembler calls = %d, want 1", recorder.callCount)
	}
	if recorder.input.Msg.Origin != core.InboundOriginTurnAuthorization || recorder.input.Msg.OriginDetail != turnBudgetRecoveryOriginDetail {
		t.Fatalf("recovery origin = %q/%q, want turn authorization budget recovery", recorder.input.Msg.Origin, recorder.input.Msg.OriginDetail)
	}
	if !strings.Contains(recorder.input.Msg.Text, "do not replay pending calls") {
		t.Fatalf("recovery prompt = %q, want re-decision instruction", recorder.input.Msg.Text)
	}

	events, err := store.LatestExecutionEventsBySession(key, 20)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	assertBudgetRecoveryEventStatus(t, events, "scheduled")
	assertBudgetRecoveryEventStatus(t, events, "resuming")
	assertBudgetRecoveryEventStatus(t, events, "resumed")
}

func TestBudgetRecoveryFromWorkExecutorContinuationDefersToManualRetry(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "hidden recovery should not run"}}
	rt.interactiveDMAssembler = recorder

	key := session.SessionKey{ChatID: 9718, UserID: 0, Scope: telegramDMScopeRef(9718)}
	msg := core.InboundMessage{
		ChatID:       9718,
		SenderID:     1001,
		SenderName:   "admin",
		Text:         "approved work continuation",
		MessageID:    47,
		Origin:       core.InboundOriginTurnAuthorization,
		OriginDetail: string(session.TurnAuthorizationKindContinuation),
	}
	opState := budgetRecoveryTestOperationState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}
	port := &turnDeliveryPort{runtime: rt, key: key, msg: msg, deliver: true, deferBudgetRecoveryToWorkFailureRetry: true}

	got, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: msg.ChatID, Text: turnBudgetRecoveryHandoffText(recovery)},
		Result: &turn.Result{
			Turn:           &core.TurnResult{Text: turnBudgetRecoveryHandoffText(recovery), Recovery: recovery},
			VisibleReply:   turnBudgetRecoveryHandoffText(recovery),
			OperationState: opState,
		},
	})
	if err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}
	if got == nil || got.Kind != "budget_recovery_deferred_to_work_retry" {
		t.Fatalf("Deliver() = %#v, want budget recovery deferred to work retry", got)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.WaitForBackgroundLoops(waitCtx); err != nil {
		t.Fatalf("WaitForBackgroundLoops() err = %v", err)
	}
	if recorder.callCount != 0 {
		t.Fatalf("recovery assembler calls = %d, want no hidden recovery turn", recorder.callCount)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %#v, want no visible budget recovery message from deferred lane", sender.sent)
	}

	events, err := store.LatestExecutionEventsBySession(key, 20)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	assertBudgetRecoveryEventStatus(t, events, "deferred")
	assertNoBudgetRecoveryEventStatus(t, events, "scheduled")
	assertNoBudgetRecoveryEventStatus(t, events, "resuming")
	assertNoBudgetRecoveryEventStatus(t, events, "resumed")
	if !budgetRecoveryEventPayloadContains(events, core.ExecutionEventTurnBudgetRecovery, "reason", "work_executor_retry_path") {
		t.Fatalf("events = %#v, want deferred recovery reason", events)
	}
}

func TestBudgetRecoveryWithStaleTelegramIngressRunsDefaultAssembler(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Recovered final response."
	provider.faceReplyText = "Recovered final response."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9713, UserID: 0, Scope: telegramDMScopeRef(9713)}
	msg := core.InboundMessage{
		ChatID:          9713,
		SenderID:        1001,
		SenderName:      "admin",
		Text:            "finish the current phase",
		MessageID:       44,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 385539578,
	}
	opState := budgetRecoveryTestOperationState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}
	port := &turnDeliveryPort{runtime: rt, key: key, msg: msg, deliver: true}

	if _, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: msg.ChatID, Text: turnBudgetRecoveryHandoffText(recovery)},
		Result: &turn.Result{
			Turn:           &core.TurnResult{Text: turnBudgetRecoveryHandoffText(recovery), Recovery: recovery},
			VisibleReply:   turnBudgetRecoveryHandoffText(recovery),
			OperationState: opState,
		},
	}); err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rt.WaitForBackgroundLoops(waitCtx); err != nil {
		t.Fatalf("WaitForBackgroundLoops() err = %v", err)
	}
	for _, msg := range sender.sent {
		if strings.Contains(msg.Text, "automatic recovery turn failed") || strings.Contains(msg.Text, "telegram ingress update") {
			t.Fatalf("sent = %#v, want no budget recovery failure notice", sender.sent)
		}
	}
	events, err := store.LatestExecutionEventsBySession(key, 30)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	assertBudgetRecoveryEventStatus(t, events, "scheduled")
	assertBudgetRecoveryEventStatus(t, events, "resuming")
	assertBudgetRecoveryEventStatus(t, events, "resumed")
	assertNoBudgetRecoveryEventStatus(t, events, "failed")
}

func TestBudgetRecoveryDeliveryBlocksAfterThreeSameScopeAttempts(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9712, UserID: 0, Scope: telegramDMScopeRef(9712)}
	msg := core.InboundMessage{ChatID: 9712, SenderID: 1001, SenderName: "admin", Text: "finish the current phase", MessageID: 43}
	opState := budgetRecoveryTestOperationState()
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}
	result := &turn.Result{
		Turn:           &core.TurnResult{Text: turnBudgetRecoveryHandoffText(recovery), Recovery: recovery},
		VisibleReply:   turnBudgetRecoveryHandoffText(recovery),
		OperationState: opState,
	}
	scope, scopePayload := rt.turnBudgetRecoveryScope(key, msg, result)
	for hop := 1; hop <= 3; hop++ {
		rt.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", "scheduled", turnBudgetRecoveryPayload(recovery, scope, scopePayload, hop, 3), time.Now().UTC())
	}
	port := &turnDeliveryPort{runtime: rt, key: key, msg: msg, deliver: true}

	got, err := port.Deliver(context.Background(), turn.DeliveryRequest{
		Message: core.OutboundMessage{ChatID: msg.ChatID, Text: turnBudgetRecoveryHandoffText(recovery)},
		Result:  result,
	})
	if err != nil {
		t.Fatalf("Deliver() err = %v", err)
	}
	if got == nil || got.MessageID == 0 {
		t.Fatalf("Deliver() = %#v, want visible blocked message", got)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want blocked notice only", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].Text, "stopped after 3 recovery attempts") {
		t.Fatalf("blocked text = %q", sender.sent[0].Text)
	}
	if err := rt.WaitForBackgroundLoops(context.Background()); err != nil {
		t.Fatalf("WaitForBackgroundLoops() err = %v", err)
	}
}

func TestBudgetRecoveryFailureIssuesRecoveryDecisionForActiveLease(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.interactiveDMAssembler = &recordingInteractiveDMTurnAssembler{err: errors.New("begin turn run kind=interactive chat_id=9716: telegram ingress update telegram:primary/385539578 is not accepted or queued")}

	key := session.SessionKey{ChatID: 9716, UserID: 0, Scope: telegramDMScopeRef(9716)}
	msg := core.InboundMessage{
		ChatID:    9716,
		SenderID:  1001,
		Text:      "finish the current phase",
		MessageID: 46,
	}
	opState := budgetRecoveryTestOperationState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	cont := approvedContinuation("budget-recovery-active-lease", "workspace_write", time.Now().UTC(), []string{"inspect", "edit_workspace", "run_tests"}, []string{"git_push", "deploy", "restart"})
	cont.RemainingTurns = 2
	cont.ContinuationLease.RemainingTurns = 2
	if err := store.UpdateContinuationState(key, cont); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}
	scope, _ := rt.turnBudgetRecoveryScope(key, msg, &turn.Result{OperationState: opState})

	if err := rt.runTurnBudgetRecoveryContinuation(context.Background(), key, msg, principalForBudgetRecoveryTest(1001), recovery, scope, 1, 3); err == nil {
		t.Fatal("runTurnBudgetRecoveryContinuation() err = nil, want simulated failure")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want one recovery decision notice", len(sender.sent))
	}
	text := sender.sent[0].Text
	for _, want := range []string{"could not complete the recovery check cleanly", "Saved state still shows this work is approved", "continue with inspect"} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure notice missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"automatic recovery turn failed", "active approved continuation", "continue under the active boundary"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("failure notice leaked internal copy %q:\n%s", forbidden, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "dead end") {
		t.Fatalf("failure notice should not dead-end:\n%s", text)
	}

	events, err := store.LatestExecutionEventsBySession(key, 30)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	if !budgetRecoveryEventPayloadContains(events, core.ExecutionEventRecoveryIssued, "recovery_action", string(recoveryDecisionContinueUnderActiveLease)) {
		t.Fatalf("events missing recovery decision action: %#v", events)
	}
}

func TestInternalContinuationDetachesStaleTelegramIngress(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "internal recovered"
	provider.faceReplyText = "internal recovered"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.handleInternalContinuation(context.Background(), principalForBudgetRecoveryTest(1001), core.InboundMessage{
		ChatID:          9714,
		SenderID:        1001,
		SenderName:      "admin",
		Text:            "continue internally",
		MessageID:       45,
		Origin:          core.InboundOriginTurnAuthorization,
		OriginDetail:    turnBudgetRecoveryOriginDetail,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 385539579,
	})
	if err != nil {
		t.Fatalf("handleInternalContinuation() err = %v, want stale ingress detached", err)
	}
}

func TestRecoveryDecisionContinuesUnderActiveLease(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9717, UserID: 0, Scope: telegramDMScopeRef(9717)}
	if err := store.UpdateOperationState(key, budgetRecoveryTestOperationState()); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	cont := approvedContinuation("recovery-decision-active", "workspace_write", time.Now().UTC(), []string{"inspect", "edit_workspace", "run_tests"}, []string{"deploy", "restart"})
	if err := store.UpdateContinuationState(key, cont); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	decision := rt.recoveryDecisionForInterruption(key, "provider_failure", "status_503", time.Now().UTC())
	if decision.Action != recoveryDecisionContinueUnderActiveLease || decision.Reason != "active_operation_and_active_lease" {
		t.Fatalf("decision = %#v, want continue under active lease", decision)
	}
	if got := recoveryDecisionVisibleText(decision); !strings.Contains(got, "continue with inspect") || !strings.Contains(got, "approved and in progress") {
		t.Fatalf("visible decision text = %q", got)
	}
}

func TestRecoveryDecisionDoesNotContinueUnderInactiveLease(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()

	for i, tc := range []struct {
		name  string
		setup func(session.SessionKey)
		want  recoveryDecisionAction
	}{
		{
			name: "consumed lease repairs retry route",
			setup: func(key session.SessionKey) {
				cont := approvedContinuation("recovery-decision-consumed", "workspace_write", now.Add(-10*time.Minute), []string{"inspect"}, []string{"deploy", "restart"})
				cont.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
				cont.ContinuationLease.ConsumedAt = now.Add(-5 * time.Minute)
				if err := store.UpdateContinuationState(key, cont); err != nil {
					t.Fatalf("UpdateContinuationState(consumed) err = %v", err)
				}
			},
			want: recoveryDecisionRepairAndRetry,
		},
		{
			name: "expired lease repairs retry route",
			setup: func(key session.SessionKey) {
				cont := approvedContinuation("recovery-decision-expired", "workspace_write", now.Add(-2*time.Hour), []string{"inspect"}, []string{"deploy", "restart"})
				cont.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
				cont.ContinuationLease.ExpiresAt = now.Add(-time.Hour)
				if err := store.UpdateContinuationState(key, cont); err != nil {
					t.Fatalf("UpdateContinuationState(expired) err = %v", err)
				}
			},
			want: recoveryDecisionRepairAndRetry,
		},
		{
			name:  "missing lease asks bounded approval",
			setup: func(session.SessionKey) {},
			want:  recoveryDecisionAskBoundedApproval,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chatID := int64(9720 + i)
			key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
			if err := store.UpdateOperationState(key, budgetRecoveryTestOperationState()); err != nil {
				t.Fatalf("UpdateOperationState() err = %v", err)
			}
			tc.setup(key)

			decision := rt.recoveryDecisionForInterruption(key, "provider_failure", "status_503", now)
			if decision.Action != tc.want {
				t.Fatalf("decision = %#v, want %s", decision, tc.want)
			}
			if decision.Action == recoveryDecisionContinueUnderActiveLease {
				t.Fatalf("decision unexpectedly continued under inactive/missing lease: %#v", decision)
			}
		})
	}
}

func TestTurnMonitorIgnoresIngressForTurnAuthorization(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9715, UserID: 0, Scope: telegramDMScopeRef(9715)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "internal recovery", nil, nil, core.InboundMessage{
		ChatID:          9715,
		SenderID:        1001,
		Origin:          core.InboundOriginTurnAuthorization,
		OriginDetail:    turnBudgetRecoveryOriginDetail,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 385539580,
	})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v, want normal turn run for turn authorization", err)
	}
	if monitor.ingressSurface != "" || monitor.ingressUpdateID != 0 {
		t.Fatalf("monitor ingress = %q/%d, want detached", monitor.ingressSurface, monitor.ingressUpdateID)
	}
	monitor.Finish(monitor.Context(), nil)
}

func budgetRecoveryEventPayloadContains(events []session.ExecutionEvent, eventType string, key string, want string) bool {
	for _, event := range events {
		if event.EventType != eventType {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		if strings.TrimSpace(key) != "" && strings.TrimSpace(want) == strings.TrimSpace(stringValue(payload[key])) {
			return true
		}
	}
	return false
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func budgetRecoveryTestOperationState() session.OperationState {
	return session.OperationState{
		ID:        "op-budget-recovery",
		Objective: "Finish the current bounded implementation phase.",
		Status:    session.OperationStatusActive,
		PhasePlan: session.OperationPhasePlan{
			ID:             "plan-budget-recovery",
			Goal:           "Finish the current bounded implementation phase.",
			CurrentPhaseID: "phase-implementation",
			Phases: []session.OperationPhase{{
				ID:             "phase-implementation",
				Summary:        "Implement the bounded recovery behavior.",
				Status:         session.PlanStatusInProgress,
				AuthorityClass: "workspace_write",
				BoundedEffect:  "Patch local code and run focused tests.",
				AllowedActions: []string{"edit_repo_code", "run_go_tests"},
			}},
		},
	}
}

func principalForBudgetRecoveryTest(userID int64) principal.Principal {
	return principal.Principal{TelegramUserID: userID, Role: principal.RoleAdmin}
}

func assertBudgetRecoveryEventStatus(t *testing.T, events []session.ExecutionEvent, status string) {
	t.Helper()
	for _, event := range events {
		if event.EventType == core.ExecutionEventTurnBudgetRecovery && event.Status == status {
			return
		}
	}
	t.Fatalf("missing %s event in %#v", status, events)
}

func assertNoBudgetRecoveryEventStatus(t *testing.T, events []session.ExecutionEvent, status string) {
	t.Helper()
	for _, event := range events {
		if event.EventType == core.ExecutionEventTurnBudgetRecovery && event.Status == status {
			t.Fatalf("unexpected %s event in %#v", status, events)
		}
	}
}
