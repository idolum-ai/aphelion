//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
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

	rt.setChatTurnPhase(chatID, statusTurnPhase{
		Phase:     "render",
		Summary:   "fallback phase",
		UpdatedAt: time.Now().UTC(),
	})

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
