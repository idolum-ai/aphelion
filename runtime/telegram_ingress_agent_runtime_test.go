//go:build linux

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/router"
	"github.com/idolum-ai/aphelion/session"
)

func TestAgentFuncCompletesQueuedIngressHandledBeforeTurnMonitor(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.interactiveDMAssembler = &recordingInteractiveDMTurnAssembler{result: &core.TurnResult{Text: "handled before monitor"}}

	now := time.Date(2026, time.June, 29, 16, 2, 42, 0, time.UTC)
	msg := core.InboundMessage{
		ChatID:          1601,
		ChatType:        "private",
		SenderID:        1001,
		SenderName:      "admin",
		Text:            "approval-style fast path",
		MessageID:       704,
		IngressSurface:  "telegram:primary",
		IngressUpdateID: 505,
		IngressQueuedAt: now,
	}
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     msg.IngressSurface,
		UpdateID:    msg.IngressUpdateID,
		UpdateKind:  "message",
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   session.SessionIDForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}),
		Status:      session.TelegramIngressUpdateQueued,
		InboundJSON: mustMarshalInboundMessageForTest(t, msg),
		PayloadJSON: `{"update_id":505}`,
		AcceptedAt:  now.Add(-time.Second),
		QueuedAt:    now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	r := router.NewRouter(rt.AgentFunc())
	r.SetEventHandler(rt.RouterEventHandler())
	r.Route(context.Background(), msg)

	record, ok, err := store.TelegramIngressUpdate(msg.IngressSurface, msg.IngressUpdateID)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateCompleted || record.TurnRunID != 0 || record.CompletedAt.IsZero() {
		t.Fatalf("ingress record = %#v, want completed without turn run", record)
	}
	pending, err := store.PendingTelegramIngressUpdates("telegram:primary", 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending ingress = %#v, want selected fast path terminalized", pending)
	}
	events, err := store.ExecutionEventsBySession(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !hasExecutionEvent(events, core.ExecutionEventIngressSelected) {
		t.Fatalf("events = %#v, want ingress.selected before fast-path completion", events)
	}
}
