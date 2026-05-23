//go:build linux

package telegramruntime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestReplayPendingIngressDropsClosedTelegramThread(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "ingress-replay.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 21, 15, 0, 0, 0, time.UTC)
	thread, _, err := store.CreateTelegramThreadForUpdate(9001, 42, 301, 401, "closed work", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, _, err := store.CloseTelegramThread(9001, thread.ThreadID, "done", now.Add(time.Minute)); err != nil {
		t.Fatalf("CloseTelegramThread() err = %v", err)
	}
	msg := core.InboundMessage{
		ChatID:           9001,
		ChatType:         "private",
		SenderID:         42,
		MessageID:        501,
		Text:             "stale work",
		TelegramThreadID: thread.ThreadID,
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal inbound err = %v", err)
	}
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     telegramPrimaryIngressSurface,
		UpdateID:    777,
		UpdateKind:  "message",
		ChatID:      9001,
		SenderID:    42,
		MessageID:   501,
		SessionID:   session.SessionIDForKey(session.SessionKey{ChatID: 9001, Scope: session.TelegramThreadScopeRef(9001, thread.ThreadID)}),
		Status:      session.TelegramIngressUpdateQueued,
		InboundJSON: string(raw),
		AcceptedAt:  now.Add(2 * time.Minute),
		QueuedAt:    now.Add(2 * time.Minute),
		UpdatedAt:   now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	called := false
	checkpoint := newTelegramIngressCheckpoint(store, telegramPrimaryIngressSurface)
	err = replayPendingTelegramIngress(context.Background(), store, checkpoint, func(context.Context, core.InboundMessage) error {
		called = true
		return nil
	}, telegramPrimaryIngressSurface, 10, nil)
	if err != nil {
		t.Fatalf("replayPendingTelegramIngress() err = %v", err)
	}
	if called {
		t.Fatal("handler called for closed-thread ingress, want dropped before dispatch")
	}
	record, ok, err := store.TelegramIngressUpdate(telegramPrimaryIngressSurface, 777)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate(777) ok=%v err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateDropped || record.ErrorText != session.TelegramIngressDropReasonTelegramThreadClosed || record.CompletedAt.IsZero() {
		t.Fatalf("record = %#v, want dropped closed-thread ingress", record)
	}
	next, err := store.TelegramIngressNextUpdateID(telegramPrimaryIngressSurface)
	if err != nil {
		t.Fatalf("TelegramIngressNextUpdateID() err = %v", err)
	}
	if next != 778 {
		t.Fatalf("next update id = %d, want 778", next)
	}
}
