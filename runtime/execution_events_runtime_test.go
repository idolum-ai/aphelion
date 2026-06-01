//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/pipeline"
	"github.com/idolum-ai/aphelion/router"
	"github.com/idolum-ai/aphelion/session"
)

func TestRouterAndRuntimeEmitExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	router := router.NewRouter(rt.AgentFunc())
	router.SetEventHandler(rt.RouterEventHandler())
	router.Route(context.Background(), core.InboundMessage{
		ChatID:     99101,
		ChatType:   "private",
		SenderID:   1001,
		SenderName: "admin",
		MessageID:  77,
		Text:       "hello",
	})

	key := session.SessionKey{ChatID: 99101, UserID: 0, Scope: telegramDMScopeRef(99101)}
	events, err := store.ExecutionEventsBySession(key, 0, 500)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("execution events are empty")
	}

	assertHasEventType(t, events, core.ExecutionEventIngressAccepted)
	assertHasEventType(t, events, core.ExecutionEventIngressSelected)
	assertHasEventType(t, events, core.ExecutionEventTurnStarted)
	assertHasEventType(t, events, core.ExecutionEventTurnCompleted)
	assertHasEventType(t, events, core.ExecutionEventTurnSidecarsCaptured)
	assertHasEventType(t, events, core.ExecutionEventProviderAttemptStarted)
	assertHasEventType(t, events, core.ExecutionEventProviderAttemptSucceeded)
	assertHasEventType(t, events, core.ExecutionEventTurnStageChanged)
	assertPayloadNonNegativeInt64(t, payloadForEventType(events, core.ExecutionEventIngressSelected), "ingress_queue_wait_ms")
	assertPayloadNonNegativeInt64(t, payloadForEventType(events, core.ExecutionEventProviderAttemptSucceeded), "provider_duration_ms")
	assertPayloadNonNegativeInt64(t, payloadForEventType(events, core.ExecutionEventTurnCompleted), "turn_duration_ms")
}

func assertPayloadNonNegativeInt64(t *testing.T, payload map[string]any, key string) {
	t.Helper()
	value, ok := payloadInt64(payload, key)
	if !ok {
		t.Fatalf("payload[%q] missing in %#v", key, payload)
	}
	if value < 0 {
		t.Fatalf("payload[%q] = %d, want non-negative", key, value)
	}
}

func TestRuntimeRecordsProviderRetryAndFailoverEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 99103, UserID: 0, Scope: telegramDMScopeRef(99103)}
	observedAt := time.Date(2026, 5, 4, 20, 30, 0, 0, time.UTC)
	rt.recordProviderAttemptEvents(key, pipeline.TurnExecutionContract{
		Backend:      "native",
		ProviderName: "failover",
		ModelName:    "test-model",
		ProviderPath: []string{"codex", "native"},
	}, &core.TurnResult{ProviderEvents: []core.ProviderEvent{
		{EventType: core.ExecutionEventProviderAttemptRetried, Provider: "codex", Attempt: 1, MaxRetries: 3, Error: "503"},
		{EventType: core.ExecutionEventProviderPartial, Provider: "codex", ResponseID: "resp-partial", Reason: "incomplete", PartialContentChars: 17, PartialToolCalls: 1},
		{EventType: core.ExecutionEventProviderFailoverEngaged, ObservedAt: observedAt, FromProvider: "codex", ToProvider: "native", Error: "codex incomplete"},
	}})

	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventProviderAttemptRetried)
	assertHasEventType(t, events, core.ExecutionEventProviderPartial)
	assertHasEventType(t, events, core.ExecutionEventProviderFailoverEngaged)
	var foundObservedAt bool
	for _, event := range events {
		if event.EventType == core.ExecutionEventProviderFailoverEngaged {
			foundObservedAt = event.CreatedAt.Equal(observedAt)
			break
		}
	}
	if !foundObservedAt {
		t.Fatalf("events = %#v, want failover event created_at to use provider observed_at %s", events, observedAt.Format(time.RFC3339))
	}
}

func TestProviderNameAfterProviderEventsUsesFallbackTarget(t *testing.T) {
	t.Parallel()

	got := providerNameAfterProviderEvents("codex", []core.ProviderEvent{
		{EventType: core.ExecutionEventProviderFailoverEngaged, FromProvider: "codex", ToProvider: "native"},
	})
	if got != "native" {
		t.Fatalf("providerNameAfterProviderEvents() = %q, want native", got)
	}
}

func TestRuntimeWarnsProviderFailoverInOneLine(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 99104, UserID: 0, Scope: telegramDMScopeRef(99104)}
	rt.warnProviderFailovers(context.Background(), key, []core.ProviderEvent{{
		EventType:    core.ExecutionEventProviderFailoverEngaged,
		FromProvider: "openai:gpt-5.5",
		ToProvider:   "anthropic",
		Error:        "tool result rejected",
	}})

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if got := sender.sent[0].Text; got != "Provider fallback: openai:gpt-5.5 failed; trying anthropic." || strings.Contains(got, "\n") {
		t.Fatalf("warning = %q, want one-line provider fallback warning", got)
	}
}

func TestRuntimeWarnsProviderFailoverQuotaInOneLine(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 99105, UserID: 0, Scope: telegramDMScopeRef(99105)}
	rt.warnProviderFailovers(context.Background(), key, []core.ProviderEvent{{
		EventType:    core.ExecutionEventProviderFailoverEngaged,
		FromProvider: "openai:gpt-5.5",
		ToProvider:   "anthropic",
		Error:        `openai: status 429: {"error":{"message":"You exceeded your current quota, please check your plan and billing details.","type":"insufficient_quota","code":"insufficient_quota"}}`,
	}})

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if got := sender.sent[0].Text; got != "Provider fallback: openai:gpt-5.5 quota exceeded; trying anthropic." || strings.Contains(got, "\n") {
		t.Fatalf("warning = %q, want one-line quota fallback warning", got)
	}
}

func TestRuntimeRecordsTelegramCallbackErrorEvent(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	chatID := int64(99104)
	rt.RecordTelegramCallbackError(chatID, "continuation.approve", errors.New("continuation proposal expired"))

	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventTelegramCallbackFailed)
}

func TestChatStatusSnapshotUsesExecutionEventPhase(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	chatID := int64(99102)
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	startedAt := time.Now().UTC()
	_, err = store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventTurnStarted,
		Stage:       "turn",
		Status:      "running",
		PayloadJSON: `{"turn_kind":"interactive"}`,
		CreatedAt:   startedAt,
	}, {
		EventType:   core.ExecutionEventTurnStageChanged,
		Stage:       "governor",
		Status:      "active",
		PayloadJSON: `{"summary":"running governor loop"}`,
		CreatedAt:   startedAt.Add(time.Second),
	}})
	if err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	snapshot, err := rt.ChatStatusSnapshot(chatID, core.RouterStatusSnapshot{})
	if err != nil {
		t.Fatalf("ChatStatusSnapshot() err = %v", err)
	}
	if snapshot.TurnPhase != "governor" {
		t.Fatalf("TurnPhase = %q, want governor from execution events", snapshot.TurnPhase)
	}
	if snapshot.TurnPhaseSummary != "running governor loop" {
		t.Fatalf("TurnPhaseSummary = %q, want execution-event summary", snapshot.TurnPhaseSummary)
	}
}

func assertHasEventType(t *testing.T, events []session.ExecutionEvent, eventType string) {
	t.Helper()
	for _, event := range events {
		if event.EventType == eventType {
			return
		}
	}
	t.Fatalf("events missing type %q; got %#v", eventType, events)
}

func TestDecisionObserverEmitsExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	observer := rt.DecisionEventObserver()
	if observer == nil {
		t.Fatal("DecisionEventObserver() = nil")
	}

	now := time.Now().UTC()
	pending := decision.PendingDecision{
		ID: "dec-1",
		Request: decision.Request{
			Kind:          decision.KindProposalApproval,
			ChatID:        7001,
			SenderID:      1001,
			MessageID:     22,
			Prompt:        "approve?",
			Details:       "details",
			DefaultChoice: "deny",
			Choices:       []decision.Choice{{ID: "deny"}, {ID: "approve"}},
		},
	}
	observer(context.Background(), decision.Event{
		Type:      decision.EventTypeOpened,
		Decision:  pending,
		OwnerKey:  decision.OwnerKey(7001, 1001),
		Seq:       1,
		CreatedAt: now,
	})
	observer(context.Background(), decision.Event{
		Type:      decision.EventTypeResolved,
		Decision:  pending,
		OwnerKey:  decision.OwnerKey(7001, 1001),
		Seq:       1,
		Choice:    "approve",
		Reason:    "callback",
		CreatedAt: now.Add(time.Second),
	})

	key := session.SessionKey{ChatID: 7001, UserID: 0, Scope: telegramDMScopeRef(7001)}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventDecisionOpened)
	assertHasEventType(t, events, core.ExecutionEventDecisionResolved)
}

func TestStartupRecoveryEmitsExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Recovery summary."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9909, UserID: 0, Scope: telegramDMScopeRef(9909)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "interrupted work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if run.Status != session.TurnRunStatusRunning {
		t.Fatalf("run status = %q, want running", run.Status)
	}

	if err := rt.runStartupRecoveryOnce(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("runStartupRecoveryOnce() err = %v", err)
	}

	recoveryKey := session.SessionKey{ChatID: heartbeatSessionChatID, UserID: 0, Scope: heartbeatScopeRef()}
	events, err := store.ExecutionEventsBySession(recoveryKey, 0, 200)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(recovery) err = %v", err)
	}
	assertHasEventType(t, events, core.ExecutionEventRecoveryDetected)
	assertHasEventType(t, events, core.ExecutionEventRecoveryIssued)
	assertHasEventType(t, events, core.ExecutionEventRecoveryCompleted)
}
