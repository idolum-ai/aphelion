//go:build linux

package telegramcontrol

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
)

type recordingIngressRouter struct {
	enqueued []core.InboundMessage
}

func (r *recordingIngressRouter) Status(int64) core.SessionStatus { return core.SessionStatus{} }
func (r *recordingIngressRouter) StatusForMessage(core.InboundMessage) core.SessionStatus {
	return core.SessionStatus{}
}
func (r *recordingIngressRouter) Snapshot() core.RouterStatusSnapshot {
	return core.RouterStatusSnapshot{}
}
func (r *recordingIngressRouter) Stop(int64) core.StopResult { return core.StopResult{} }
func (r *recordingIngressRouter) StopForMessage(core.InboundMessage) core.StopResult {
	return core.StopResult{}
}
func (r *recordingIngressRouter) Enqueue(_ context.Context, msg core.InboundMessage) error {
	r.enqueued = append(r.enqueued, msg)
	return nil
}

func TestRouteAcceptedDropsClosedTelegramThreadIngress(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "control-ingress.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 21, 16, 0, 0, 0, time.UTC)
	thread, _, err := store.CreateTelegramThreadForUpdate(9101, 42, 301, 401, "closed work", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, _, err := store.CloseTelegramThread(9101, thread.ThreadID, "done", now.Add(time.Minute)); err != nil {
		t.Fatalf("CloseTelegramThread() err = %v", err)
	}
	threadKey := session.SessionKey{ChatID: 9101, Scope: session.TelegramThreadScopeRef(9101, thread.ThreadID)}
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     "telegram:primary",
		UpdateID:    888,
		UpdateKind:  "message",
		ChatID:      9101,
		SenderID:    42,
		MessageID:   501,
		SessionID:   session.SessionIDForKey(threadKey),
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: `{"Text":"stale","TelegramThreadID":1}`,
		AcceptedAt:  now.Add(2 * time.Minute),
		UpdatedAt:   now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	ingress := &recordingIngressRouter{}
	control := CommandControl{Store: store, Ingress: ingress}
	msg := core.InboundMessage{
		ChatID:           9101,
		ChatType:         "private",
		SenderID:         42,
		MessageID:        501,
		Text:             "stale",
		TelegramThreadID: thread.ThreadID,
		IngressSurface:   "telegram:primary",
		IngressUpdateID:  888,
	}
	if err := control.RouteAccepted(context.Background(), msg); err != nil {
		t.Fatalf("RouteAccepted() err = %v", err)
	}
	if len(ingress.enqueued) != 0 {
		t.Fatalf("enqueued = %#v, want closed-thread ingress dropped", ingress.enqueued)
	}
	record, ok, err := store.TelegramIngressUpdate("telegram:primary", 888)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate(888) ok=%v err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateDropped || record.ErrorText != session.TelegramIngressDropReasonTelegramThreadClosed {
		t.Fatalf("record = %#v, want dropped closed-thread ingress", record)
	}
}

func TestQueueClarificationRecordsRecoverableCallbackWork(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "clarification-ingress.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	ingress := &recordingIngressRouter{}
	control := CommandControl{Store: store, Ingress: ingress}
	msg := core.InboundMessage{
		ChatID:          9101,
		ChatType:        "private",
		SenderID:        42,
		MessageID:       501,
		Text:            "Ask me concise clarifying questions about memory.",
		IngressSurface:  telegramruntime.MemoryClarificationIngressSurface,
		IngressUpdateID: 888,
	}
	if err := control.QueueClarification(context.Background(), msg); err != nil {
		t.Fatalf("QueueClarification() err = %v", err)
	}
	if len(ingress.enqueued) != 1 {
		t.Fatalf("enqueued = %#v, want one clarification turn", ingress.enqueued)
	}
	if ingress.enqueued[0].IngressSurface != telegramruntime.MemoryClarificationIngressSurface || ingress.enqueued[0].IngressUpdateID != 888 {
		t.Fatalf("enqueued[0] = %#v, want durable clarification ingress identity", ingress.enqueued[0])
	}
	record, ok, err := store.TelegramIngressUpdate(telegramruntime.MemoryClarificationIngressSurface, 888)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%v err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateQueued || record.UpdateKind != "callback_memory_clarification" {
		t.Fatalf("record = %#v, want queued memory clarification callback work", record)
	}
}
