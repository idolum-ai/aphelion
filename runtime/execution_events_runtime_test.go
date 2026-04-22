//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

func TestRouterAndRuntimeEmitExecutionEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	router := core.NewRouter(rt.AgentFunc())
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
	assertHasEventType(t, events, core.ExecutionEventProviderAttemptStarted)
	assertHasEventType(t, events, core.ExecutionEventProviderAttemptSucceeded)
	assertHasEventType(t, events, core.ExecutionEventTurnStageChanged)
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
	_, err = store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   core.ExecutionEventTurnStageChanged,
		Stage:       "governor",
		Status:      "active",
		PayloadJSON: `{"summary":"running governor loop"}`,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("AppendExecutionEvent() err = %v", err)
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
