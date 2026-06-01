//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/router"
	"github.com/idolum-ai/aphelion/session"
)

func TestNewTurnContextWithoutTimeoutHasNoDeadlineAndIsCancelable(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTurnContext(context.Background(), 0)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("Deadline() ok = true, want false when timeout is disabled")
	}

	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context was not canceled")
	}
}

func TestNewTurnContextWithTimeoutHasDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := newTurnContext(context.Background(), time.Second)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("Deadline() ok = false, want true when timeout is set")
	}
}

func TestQueueReinstallUsesTemplatedInboundMessage(t *testing.T) {
	t.Parallel()
	if reinstallTemplateMessage == "" {
		t.Fatal("reinstallTemplateMessage empty")
	}
	if reinstallTemplateMessage == "/reinstall" {
		t.Fatal("reinstallTemplateMessage collapsed to command text")
	}
}

func TestTelegramCommandControlQueueReinstallUsesDurableAcceptedRoute(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     telegramruntime.PrimaryIngressSurface,
		UpdateID:    701,
		UpdateKind:  "message",
		ChatID:      1001,
		SenderID:    2002,
		MessageID:   3003,
		SessionID:   "telegram_dm:1001",
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: `{"ChatID":1001,"SenderID":2002,"Text":"/reinstall","MessageID":3003}`,
		AcceptedAt:  now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	var routed core.InboundMessage
	router := router.NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		routed = msg
		return &core.TurnResult{}, nil
	})
	control := telegramCommandControl{store: store, router: router}
	if err := control.QueueReinstall(context.Background(), core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		Text:            "/reinstall",
		IngressSurface:  telegramruntime.PrimaryIngressSurface,
		IngressUpdateID: 701,
	}); err != nil {
		t.Fatalf("QueueReinstall() err = %v", err)
	}
	if routed.Text != reinstallTemplateMessage || routed.Raw != nil {
		t.Fatalf("routed reinstall = %#v, want templated durable message without raw Telegram payload", routed)
	}
	record, ok, err := store.TelegramIngressUpdate(telegramruntime.PrimaryIngressSurface, 701)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateQueued {
		t.Fatalf("ingress status = %s, want queued before worker processing", record.Status)
	}
}

func TestTelegramCommandControlRouteAcceptedDoesNotRedispatchTerminalIngress(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 16, 12, 5, 0, 0, time.UTC)
	if err := store.RecordTelegramIngressTerminal(session.TelegramIngressUpdateRecord{
		Surface:     telegramruntime.PrimaryIngressSurface,
		UpdateID:    702,
		UpdateKind:  "message",
		ChatID:      1001,
		SenderID:    2002,
		MessageID:   3004,
		Status:      session.TelegramIngressUpdateCompleted,
		CompletedAt: now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressTerminal() err = %v", err)
	}

	routed := false
	router := router.NewRouter(func(_ context.Context, _ *core.SessionState, _ core.InboundMessage) (*core.TurnResult, error) {
		routed = true
		return &core.TurnResult{}, nil
	})
	control := telegramCommandControl{store: store, router: router}
	if err := control.RouteAccepted(context.Background(), core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3004,
		Text:            "redelivered",
		IngressSurface:  telegramruntime.PrimaryIngressSurface,
		IngressUpdateID: 702,
	}); err != nil {
		t.Fatalf("RouteAccepted() err = %v", err)
	}
	if routed {
		t.Fatal("terminal ingress row was dispatched")
	}
}

func TestTelegramCommandControlEnsureDoctorIngressQueuedCreatesRecoverableCallbackWork(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	control := telegramCommandControl{store: store}
	dispatch, err := control.ensureDoctorIngressQueued(core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       77,
		Text:            "/health diagnose",
		IngressSurface:  telegramruntime.DoctorIngressSurface,
		IngressUpdateID: 703,
	})
	if err != nil {
		t.Fatalf("ensureDoctorIngressQueued() err = %v", err)
	}
	if !dispatch {
		t.Fatal("dispatch = false, want newly accepted callback work dispatchable")
	}
	record, ok, err := store.TelegramIngressUpdate(telegramruntime.DoctorIngressSurface, 703)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate(doctor) ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateQueued || record.SessionID != "telegram_dm:1001" || record.InboundJSON == "" {
		t.Fatalf("doctor ingress = %#v, want queued recoverable callback work", record)
	}
}
