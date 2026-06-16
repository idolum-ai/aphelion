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
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "finish the current phase")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	fakeGitHubToken := "ghp_" + "secret123456789"
	fakeGitHubPAT := "github_pat_" + "secret123456789"
	sensitiveInput := `{"cmd":"go test ./runtime","` + "to" + `ken":"` + fakeGitHubToken + `"}`
	if err := store.NoteTurnRunToolStart(run.ID, "exec", sensitiveInput); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	if err := store.NoteTurnRunToolFinish(run.ID, "ok "+fakeGitHubPAT, ""); err != nil {
		t.Fatalf("NoteTurnRunToolFinish() err = %v", err)
	}
	rt.recordExecutionEvent(key, core.ExecutionEventToolSucceeded, "tool", "succeeded", map[string]any{
		"run_id":         run.ID,
		"tool":           "exec",
		"result_preview": "ok " + fakeGitHubPAT,
	}, time.Now().UTC())
	hookCalls := 0
	port := &turnDeliveryPort{
		runtime:     rt,
		key:         key,
		msg:         msg,
		runIDSource: staticTurnRunIDSource(run.ID),
		deliver:     true,
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
	if !strings.Contains(recorder.input.Msg.Text, "discard pending tool calls") || !strings.Contains(recorder.input.Msg.Text, "Reconcile the current input and working objective") {
		t.Fatalf("recovery prompt = %q, want re-decision instruction", recorder.input.Msg.Text)
	}
	if !strings.Contains(recorder.input.Msg.Text, "Evidence digest") || !strings.Contains(recorder.input.Msg.Text, "last_tool=exec") || !strings.Contains(recorder.input.Msg.Text, "last_tool_result") {
		t.Fatalf("recovery prompt = %q, want compact evidence digest", recorder.input.Msg.Text)
	}
	if strings.Contains(recorder.input.Msg.Text, fakeGitHubToken) || strings.Contains(recorder.input.Msg.Text, fakeGitHubPAT) {
		t.Fatalf("recovery prompt leaked secret-shaped digest material: %q", recorder.input.Msg.Text)
	}

	events, err := store.LatestExecutionEventsBySession(key, 20)
	if err != nil {
		t.Fatalf("LatestExecutionEventsBySession() err = %v", err)
	}
	assertBudgetRecoveryEventStatus(t, events, "scheduled")
	assertBudgetRecoveryEventStatus(t, events, "resuming")
	assertBudgetRecoveryEventStatus(t, events, "resumed")
	if !scheduledBudgetRecoveryEventHasDigest(events, run.ID) {
		t.Fatalf("events = %#v, want scheduled recovery event with digest for run %d", events, run.ID)
	}
}

type staticTurnRunIDSource int64

func (s staticTurnRunIDSource) turnRunID() int64 {
	return int64(s)
}

func TestTurnBudgetRecoveryDigestIsBoundedAndRunScoped(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9719, UserID: 0, Scope: telegramDMScopeRef(9719)}
	target, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "target run")
	if err != nil {
		t.Fatalf("BeginTurnRun(target) err = %v", err)
	}
	other, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "other run")
	if err != nil {
		t.Fatalf("BeginTurnRun(other) err = %v", err)
	}
	if err := store.NoteTurnRunToolStart(target.ID, "exec", `{"cmd":"go test ./runtime"}`); err != nil {
		t.Fatalf("NoteTurnRunToolStart() err = %v", err)
	}
	for i := 0; i < turnBudgetRecoveryDigestLines+5; i++ {
		rt.recordExecutionEvent(key, core.ExecutionEventToolSucceeded, "tool", "succeeded", map[string]any{
			"run_id":         target.ID,
			"tool":           "exec",
			"result_preview": "target evidence",
		}, time.Now().UTC())
	}
	rt.recordExecutionEvent(key, core.ExecutionEventToolSucceeded, "tool", "succeeded", map[string]any{
		"run_id":         other.ID,
		"tool":           "exec",
		"result_preview": "other run should not appear",
	}, time.Now().UTC())

	digest := rt.turnBudgetRecoveryDigest(key, target.ID)
	if digest.RunID != target.ID {
		t.Fatalf("digest.RunID = %d, want %d", digest.RunID, target.ID)
	}
	if len(digest.Lines) == 0 || len(digest.Lines) > turnBudgetRecoveryDigestLines {
		t.Fatalf("digest lines len = %d, want 1..%d: %#v", len(digest.Lines), turnBudgetRecoveryDigestLines, digest.Lines)
	}
	joined := strings.Join(digest.Lines, "\n")
	if strings.Contains(joined, "other run should not appear") {
		t.Fatalf("digest included another run's evidence: %#v", digest.Lines)
	}
	if !strings.Contains(joined, "target evidence") || !strings.Contains(joined, "turn_run=") {
		t.Fatalf("digest = %#v, want target evidence and run summary", digest.Lines)
	}
}

func TestTurnBudgetRecoveryDigestEventLinePrefersToolOutputDigest(t *testing.T) {
	t.Parallel()

	line := turnBudgetRecoveryDigestEventLine(session.ExecutionEvent{
		Seq:       7,
		EventType: core.ExecutionEventToolSucceeded,
		Status:    "succeeded",
	}, map[string]any{
		"tool":           "exec",
		"result_preview": "preview-only",
		"result_digest": map[string]any{
			"sha256":        "sha256:abc123",
			"evidence_ref":  "ev:tool_output:abc",
			"bytes":         24000,
			"lines":         80,
			"omitted_bytes": 12000,
		},
	})

	if !strings.Contains(line, "result_digest=") || !strings.Contains(line, "sha256:abc123") || !strings.Contains(line, "evidence_ref=ev:tool_output:abc") || !strings.Contains(line, "omitted_bytes=12000") {
		t.Fatalf("digest line = %q, want compact typed output digest", line)
	}
	if strings.Contains(line, "preview-only") {
		t.Fatalf("digest line = %q, want digest metadata instead of lossy preview", line)
	}
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

func TestBudgetRecoveryScopeIgnoresTerminalStoredOperation(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9744, UserID: 0, Scope: telegramDMScopeRef(9744)}
	msg := core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, Text: "how should durable children separate resources?", MessageID: 51}
	stale := budgetRecoveryTestOperationState()
	stale.ID = "stale-imexx-operation"
	stale.Objective = "Document stale thread work."
	stale.Status = session.OperationStatusCompleted
	stale.Stage = "completed"
	stale.PhasePlan.CurrentPhaseID = "phase-implementation"
	stale.PhasePlan.Phases[0].Status = session.PlanStatusCompleted
	if err := store.UpdateOperationState(key, stale); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}

	scope, payload := rt.turnBudgetRecoveryScope(key, msg, nil)
	if !strings.HasPrefix(scope, "request:") {
		t.Fatalf("scope = %q, want request scope for terminal stored operation", scope)
	}
	if _, ok := payload["operation_id"]; ok {
		t.Fatalf("payload = %#v, want no terminal operation payload", payload)
	}
}

func TestBudgetRecoveryScopeUsesActiveStoredOperationWhenResultIsTerminal(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9745, UserID: 0, Scope: telegramDMScopeRef(9745)}
	msg := core.InboundMessage{ChatID: key.ChatID, SenderID: 1001, Text: "finish the current phase", MessageID: 52}
	stored := budgetRecoveryTestOperationState()
	stored.ID = "active-stored-operation"
	if err := store.UpdateOperationState(key, stored); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	result := budgetRecoveryTestOperationState()
	result.ID = "terminal-result-operation"
	result.Status = session.OperationStatusCompleted
	result.PhasePlan.Phases[0].Status = session.PlanStatusCompleted

	scope, payload := rt.turnBudgetRecoveryScope(key, msg, &turn.Result{OperationState: result})
	if !strings.Contains(scope, "operation:active-stored-operation") {
		t.Fatalf("scope = %q, want active stored operation", scope)
	}
	if got, want := payload["operation_id"], "active-stored-operation"; got != want {
		t.Fatalf("operation_id = %#v, want %q", got, want)
	}
}

func TestBudgetRecoveryCurrentPhaseFallsThroughWhenCompleted(t *testing.T) {
	opState := budgetRecoveryTestOperationState()
	opState.PhasePlan.CurrentPhaseID = "phase-completed"
	opState.PhasePlan.Phases = []session.OperationPhase{
		{
			ID:             "phase-completed",
			Summary:        "Completed stale phase.",
			Status:         session.PlanStatusCompleted,
			AuthorityClass: "read_only_review",
		},
		{
			ID:             "phase-next",
			Summary:        "Continue current live request.",
			Status:         session.PlanStatusPending,
			AuthorityClass: "read_only_review",
		},
	}

	phase, index, ok := currentOperationPhaseForBudgetRecovery(opState)
	if !ok {
		t.Fatal("currentOperationPhaseForBudgetRecovery() ok = false, want true")
	}
	if index != 1 || phase.ID != "phase-next" {
		t.Fatalf("phase = (%q, %d), want phase-next at index 1", phase.ID, index)
	}
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

func TestContinuationBudgetRecoveryPendingTracksHighestHop(t *testing.T) {
	type hopEvent struct {
		hop    int
		status string
		at     time.Duration
	}
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}

	for i, tc := range []struct {
		name   string
		events []hopEvent
		want   bool
	}{
		{
			name: "higher hop pending survives late lower hop terminal event",
			events: []hopEvent{
				{hop: 1, status: "scheduled", at: -5 * time.Second},
				{hop: 2, status: "scheduled", at: -4 * time.Second},
				{hop: 2, status: "resuming", at: -3 * time.Second},
				{hop: 1, status: "resumed", at: -2 * time.Second},
			},
			want: true,
		},
		{
			name: "highest hop terminal clears pending",
			events: []hopEvent{
				{hop: 2, status: "scheduled", at: -5 * time.Second},
				{hop: 2, status: "resuming", at: -4 * time.Second},
				{hop: 2, status: "resumed", at: -3 * time.Second},
			},
			want: false,
		},
		{
			name: "lower hop failure does not clear higher hop pending",
			events: []hopEvent{
				{hop: 2, status: "resuming", at: -5 * time.Second},
				{hop: 1, status: "failed", at: -4 * time.Second},
			},
			want: true,
		},
		{
			name: "expired pending event is ignored",
			events: []hopEvent{
				{hop: 2, status: "resuming", at: -turnBudgetRecoveryTimeout - time.Minute},
			},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, store, provider, sender := buildRuntimeFixtures(t)
			rt, err := New(cfg, store, provider, nil, sender)
			if err != nil {
				t.Fatalf("New() err = %v", err)
			}
			now := time.Now().UTC()
			chatID := int64(9730 + i)
			key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
			opState := budgetRecoveryTestOperationState()
			if err := store.UpdateOperationState(key, opState); err != nil {
				t.Fatalf("UpdateOperationState() err = %v", err)
			}
			state := budgetRecoveryApprovedContinuationForTest(now, 2)
			if err := store.UpdateContinuationState(key, state); err != nil {
				t.Fatalf("UpdateContinuationState() err = %v", err)
			}
			msg := core.InboundMessage{ChatID: key.ChatID, SenderID: 1001}
			scope, _ := rt.turnBudgetRecoveryScope(key, msg, &turn.Result{OperationState: opState})
			for _, event := range tc.events {
				recordBudgetRecoveryHopEventForTest(t, rt, key, recovery, scope, event.hop, event.status, now.Add(event.at))
			}

			if got := rt.continuationBudgetRecoveryPending(key, state, now); got != tc.want {
				t.Fatalf("continuationBudgetRecoveryPending() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTriggerContinuationDoesNotConsumeLeaseDuringPendingBudgetRecovery(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	recorder := &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "should not run"}}
	rt.interactiveDMAssembler = recorder

	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 9739, UserID: 0, Scope: telegramDMScopeRef(9739)}
	opState := budgetRecoveryTestOperationState()
	if err := store.UpdateOperationState(key, opState); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	state := budgetRecoveryApprovedContinuationForTest(now, 1)
	if err := store.UpdateContinuationState(key, state); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	recovery := &core.TurnRecovery{
		Kind:           core.TurnRecoveryTokenBudgetExhausted,
		Recoverable:    true,
		ReplanRequired: true,
		Summary:        "Token budget exhausted before a final response.",
		MaxAutoHops:    3,
	}
	msg := core.InboundMessage{ChatID: key.ChatID, SenderID: 1001}
	scope, _ := rt.turnBudgetRecoveryScope(key, msg, &turn.Result{OperationState: opState})
	recordBudgetRecoveryHopEventForTest(t, rt, key, recovery, scope, 1, "scheduled", now.Add(-5*time.Second))
	recordBudgetRecoveryHopEventForTest(t, rt, key, recovery, scope, 2, "scheduled", now.Add(-4*time.Second))
	recordBudgetRecoveryHopEventForTest(t, rt, key, recovery, scope, 2, "resuming", now.Add(-3*time.Second))
	recordBudgetRecoveryHopEventForTest(t, rt, key, recovery, scope, 1, "resumed", now.Add(-2*time.Second))

	if err := rt.TriggerContinuationForKey(context.Background(), key); err != nil {
		t.Fatalf("TriggerContinuationForKey() err = %v", err)
	}
	if recorder.CallCount() != 0 {
		t.Fatalf("assembler calls = %d, want no continuation turn while recovery is pending", recorder.CallCount())
	}
	got, err := store.ContinuationState(key)
	if err != nil {
		t.Fatalf("ContinuationState() err = %v", err)
	}
	if got.Status != session.ContinuationStatusApproved || got.RemainingTurns != 1 || got.ContinuationLease.Status != session.ContinuationLeaseStatusActive || got.ContinuationLease.RemainingTurns != 1 {
		t.Fatalf("continuation = %#v, want approved lease unchanged while recovery is pending", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if got := countEventsByType(events, core.ExecutionEventContinuationConsumed); got != 0 {
		t.Fatalf("consumed events = %d, want none while recovery is pending", got)
	}
	if !hasContinuationBlockedEventForTest(events, "recovery_pending", "recovery_pending") {
		t.Fatalf("events = %#v, want recovery_pending continuation blocked event", events)
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

	if err := rt.runTurnBudgetRecoveryContinuation(context.Background(), key, msg, principalForBudgetRecoveryTest(1001), recovery, scope, 1, 3, turnBudgetRecoveryDigest{}); err == nil {
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

func TestRecoveryDecisionParksActiveLeaseWithoutOperationWithSpecificCopy(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	now := time.Now().UTC()
	key := session.SessionKey{ChatID: 9735, UserID: 0, Scope: telegramDMScopeRef(9735)}
	cont := approvedContinuation("recovery-decision-orphan-lease", "workspace_write", now, []string{"inspect", "edit_workspace"}, []string{"deploy", "restart"})
	cont.RemainingTurns = 1
	cont.ContinuationLease.RemainingTurns = 1
	if err := store.UpdateContinuationState(key, cont); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}

	decision := rt.recoveryDecisionForInterruption(key, "provider_failure", "status_503", now)
	if decision.Action != recoveryDecisionPark || decision.Reason != "active_lease_without_operation" {
		t.Fatalf("decision = %#v, want parked active lease without operation", decision)
	}
	if got := recoveryDecisionVisibleText(decision); !strings.Contains(got, "approval exists") || !strings.Contains(got, "cannot find the durable operation") {
		t.Fatalf("visible decision text = %q, want specific orphan-lease copy", got)
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

func scheduledBudgetRecoveryEventHasDigest(events []session.ExecutionEvent, runID int64) bool {
	for _, event := range events {
		if event.EventType != core.ExecutionEventTurnBudgetRecovery || event.Status != "scheduled" {
			continue
		}
		payload := map[string]any{}
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		raw, ok := payload["recovery_digest"].(map[string]any)
		if !ok {
			continue
		}
		gotRunID, ok := payloadInt64(raw, "run_id")
		if !ok || gotRunID != runID {
			continue
		}
		lines := payloadStringSlice(raw, "lines")
		if len(lines) == 0 {
			continue
		}
		joined := strings.Join(lines, "\n")
		if strings.Contains(joined, "last_tool") && !strings.Contains(joined, "ghp_secret") && !strings.Contains(joined, "github_pat_secret") {
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

func budgetRecoveryApprovedContinuationForTest(now time.Time, turns int) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if turns <= 0 {
		turns = 1
	}
	action := session.ActionProposal{
		ID:             "aprop-budget-recovery-hop-gate",
		Summary:        "Continue the approved budget recovery phase.",
		BoundedEffect:  "Continue only the active approved phase and report evidence.",
		RiskClass:      "workspace_write",
		AllowedActions: []string{"continue_one_turn", "use_existing_authority_only", "report_evidence"},
		Status:         session.ProposalStatusApproved,
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	action.PlanHash = actionProposalHash(action)
	lease := buildContinuationLease(action, turns, now)
	lease.Status = session.ContinuationLeaseStatusActive
	lease.RemainingTurns = turns
	lease.ApprovedBy = 1001
	lease.ApprovedAt = now
	return session.ContinuationState{
		Kind:              session.TurnAuthorizationKindContinuation,
		Status:            session.ContinuationStatusApproved,
		DecisionID:        "budget-recovery-hop-gate",
		Objective:         "Continue the approved budget recovery phase.",
		StageSummary:      action.Summary,
		RemainingTurns:    turns,
		ApprovedBy:        1001,
		ActionProposal:    action,
		ContinuationLease: lease,
	}
}

func recordBudgetRecoveryHopEventForTest(t *testing.T, rt *Runtime, key session.SessionKey, recovery *core.TurnRecovery, scope string, hop int, status string, at time.Time) {
	t.Helper()
	rt.recordExecutionEvent(key, core.ExecutionEventTurnBudgetRecovery, "turn", status, turnBudgetRecoveryPayload(recovery, scope, nil, hop, 3), at)
}

func hasContinuationBlockedEventForTest(events []session.ExecutionEvent, status string, reason string) bool {
	for _, event := range events {
		if event.EventType != core.ExecutionEventContinuationBlocked || event.Status != status {
			continue
		}
		payload := executionEventPayload(event.PayloadJSON)
		if payloadString(payload, "reason") == reason {
			return true
		}
	}
	return false
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
