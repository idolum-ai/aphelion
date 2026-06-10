//go:build linux

package main

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcontrol"
	"github.com/idolum-ai/aphelion/router"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestReplayPendingTelegramIngressRequeuesAcceptedWorkBeforeOffset(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 16, 11, 0, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     telegramruntime.PrimaryIngressSurface,
		UpdateID:    77,
		UpdateKind:  "message",
		ChatID:      7001,
		SenderID:    9,
		MessageID:   200,
		SessionID:   "telegram_dm:7001",
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: `{"ChatID":7001,"SenderID":9,"Text":"hello","MessageID":200}`,
		PayloadJSON: `{"update_id":77}`,
		AcceptedAt:  now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	var handled []core.InboundMessage
	checkpoint := telegramruntime.NewIngressCheckpoint(store, telegramruntime.PrimaryIngressSurface)
	err = telegramruntime.ReplayPendingIngress(context.Background(), store, checkpoint, func(_ context.Context, msg core.InboundMessage) error {
		handled = append(handled, msg)
		_, err := store.MarkTelegramIngressQueued(msg.IngressSurface, msg.IngressUpdateID, time.Now().UTC())
		return err
	}, telegramruntime.PrimaryIngressSurface, 10, nil)
	if err != nil {
		t.Fatalf("telegramruntime.ReplayPendingIngress() err = %v", err)
	}
	if len(handled) != 1 || handled[0].IngressSurface != telegramruntime.PrimaryIngressSurface || handled[0].IngressUpdateID != 77 {
		t.Fatalf("handled = %#v, want replayed update 77 with ingress identity", handled)
	}
	if next, err := store.TelegramIngressNextUpdateID(telegramruntime.PrimaryIngressSurface); err != nil || next != 78 {
		t.Fatalf("TelegramIngressNextUpdateID() = %d, err=%v, want 78", next, err)
	}
	pending, err := store.PendingTelegramIngressUpdates(telegramruntime.PrimaryIngressSurface, 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 1 || pending[0].Status != session.TelegramIngressUpdateQueued {
		t.Fatalf("pending = %#v, want queued update waiting for worker", pending)
	}
}

func TestStoppedQueuedTelegramIngressIsDroppedInsteadOfReplayed(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	started := make(chan string, 2)
	release := make(chan struct{})
	router := router.NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		started <- msg.Text
		if msg.Text == "active" {
			select {
			case <-ctx.Done():
			case <-release:
			}
		}
		return nil, nil
	})
	ingress := telegramcontrol.NewIngressSequencer(router, time.Minute)
	defer ingress.Close()
	control := telegramCommandControl{store: store, ingress: ingress, router: router}
	ingress.SetDropHandler(control.MarkDroppedIngress)

	if err := ingress.Enqueue(context.Background(), core.InboundMessage{
		ChatID:    7009,
		ChatType:  "private",
		SenderID:  1001,
		MessageID: 1,
		Text:      "active",
	}); err != nil {
		t.Fatalf("enqueue active: %v", err)
	}
	select {
	case got := <-started:
		if got != "active" {
			t.Fatalf("started = %q, want active", got)
		}
	case <-time.After(time.Second):
		t.Fatal("active message did not start")
	}

	now := time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     telegramruntime.PrimaryIngressSurface,
		UpdateID:    99,
		UpdateKind:  "message",
		ChatID:      7009,
		SenderID:    1001,
		MessageID:   2,
		SessionID:   "telegram_dm:7009",
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: `{"ChatID":7009,"SenderID":1001,"Text":"queued","MessageID":2}`,
		AcceptedAt:  now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}
	if err := control.RouteAccepted(context.Background(), core.InboundMessage{
		ChatID:          7009,
		ChatType:        "private",
		SenderID:        1001,
		MessageID:       2,
		Text:            "queued",
		IngressSurface:  telegramruntime.PrimaryIngressSurface,
		IngressUpdateID: 99,
	}); err != nil {
		t.Fatalf("RouteAccepted() err = %v", err)
	}

	stopped := control.Stop(7009)
	if !stopped.ActiveCanceled || !stopped.QueuedDropped {
		t.Fatalf("Stop() = %#v, want active canceled and queued dropped", stopped)
	}
	close(release)

	record, ok, err := store.TelegramIngressUpdate(telegramruntime.PrimaryIngressSurface, 99)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%v err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateDropped {
		t.Fatalf("ingress status = %s, want dropped", record.Status)
	}
	replayed := false
	checkpoint := telegramruntime.NewIngressCheckpoint(store, telegramruntime.PrimaryIngressSurface)
	err = telegramruntime.ReplayPendingIngress(context.Background(), store, checkpoint, func(context.Context, core.InboundMessage) error {
		replayed = true
		return nil
	}, telegramruntime.PrimaryIngressSurface, 10, nil)
	if err != nil {
		t.Fatalf("telegramruntime.ReplayPendingIngress() err = %v", err)
	}
	if replayed {
		t.Fatal("dropped queued ingress replayed")
	}
}

func TestReplayPendingTelegramIngressCompletesAcceptedCommandLikeUpdate(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 16, 11, 30, 0, 0, time.UTC)
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     telegramruntime.PrimaryIngressSurface,
		UpdateID:    88,
		UpdateKind:  "message",
		ChatID:      7002,
		SenderID:    10,
		MessageID:   201,
		SessionID:   "telegram_dm:7002",
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: `{"ChatID":7002,"SenderID":10,"Text":"/status","MessageID":201}`,
		PayloadJSON: `{"update_id":88}`,
		AcceptedAt:  now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	checkpoint := telegramruntime.NewIngressCheckpoint(store, telegramruntime.PrimaryIngressSurface)
	err = telegramruntime.ReplayPendingIngress(context.Background(), store, checkpoint, func(context.Context, core.InboundMessage) error {
		return nil
	}, telegramruntime.PrimaryIngressSurface, 10, nil)
	if err != nil {
		t.Fatalf("telegramruntime.ReplayPendingIngress() err = %v", err)
	}
	pending, err := store.PendingTelegramIngressUpdates(telegramruntime.PrimaryIngressSurface, 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want command-like update completed", pending)
	}
}

func TestTelegramIngressCheckpointRecordsTerminalUpdateWithoutAcceptedRow(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	checkpoint := telegramruntime.NewIngressCheckpoint(store, telegramruntime.PrimaryIngressSurface)
	now := time.Date(2026, 5, 16, 12, 15, 0, 0, time.UTC)
	if err := checkpoint.RecordTerminal(context.Background(), telegram.PollerTerminal{
		UpdateID:   901,
		UpdateKind: "callback_query",
		Status:     telegram.PollerTerminalCompleted,
		Reason:     "callback_handled",
		ChatID:     7007,
		SenderID:   1001,
		MessageID:  701,
		Payload:    `{"update_id":901}`,
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordTerminal() err = %v", err)
	}
	if err := checkpoint.RecordFailure(context.Background(), telegram.PollerFailure{
		UpdateID:   902,
		UpdateKind: "callback_query",
		ChatID:     7007,
		SenderID:   1001,
		MessageID:  702,
		ErrorText:  "callback failed",
		Payload:    `{"update_id":902}`,
		CreatedAt:  now.Add(time.Second),
	}); err != nil {
		t.Fatalf("RecordFailure() err = %v", err)
	}

	recent, err := store.RecentTelegramIngressUpdates(10)
	if err != nil {
		t.Fatalf("RecentTelegramIngressUpdates() err = %v", err)
	}
	byID := make(map[int64]session.TelegramIngressUpdateRecord, len(recent))
	for _, row := range recent {
		byID[row.UpdateID] = row
	}
	completed := byID[901]
	if completed.Status != session.TelegramIngressUpdateCompleted || completed.UpdateKind != "callback_query" || completed.ErrorText != "callback_handled" || completed.PayloadJSON == "" {
		t.Fatalf("completed terminal = %#v, want callback completed evidence", completed)
	}
	failed := byID[902]
	if failed.Status != session.TelegramIngressUpdateFailed || failed.UpdateKind != "callback_query" || failed.ErrorText != "callback failed" || failed.PayloadJSON == "" {
		t.Fatalf("failed terminal = %#v, want callback failed evidence", failed)
	}
	failures, err := store.RecentTelegramIngressFailures(10)
	if err != nil {
		t.Fatalf("RecentTelegramIngressFailures() err = %v", err)
	}
	if len(failures) != 1 || failures[0].UpdateID != 902 {
		t.Fatalf("failures = %#v, want update 902 failure side ledger", failures)
	}
}

func TestStartupTelegramIngressReplaysEveryTypedWorkSurface(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	surfaces := telegramruntime.StartupWorkSurfaces()
	if len(surfaces) != 9 {
		t.Fatalf("startup work surfaces = %#v, want primary, thread summary, doctor, context/memory/mission/re-entry callback work, busy resume, artifact resume", surfaces)
	}
	seenDefinitions := make(map[string]telegramruntime.WorkSurface)
	for i, workSurface := range surfaces {
		if workSurface.Surface == "" || workSurface.Kind == "" || workSurface.Name == "" {
			t.Fatalf("work surface[%d] = %#v, want named typed surface", i, workSurface)
		}
		if _, exists := seenDefinitions[workSurface.Surface]; exists {
			t.Fatalf("duplicate startup work surface %q", workSurface.Surface)
		}
		seenDefinitions[workSurface.Surface] = workSurface

		updateID := int64(800 + i)
		msg := core.InboundMessage{
			ChatID:    int64(7000 + i),
			SenderID:  int64(1000 + i),
			MessageID: int64(2000 + i),
			Text:      "stored " + workSurface.Name + " work",
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("Marshal(%s) err = %v", workSurface.Name, err)
		}
		now := time.Date(2026, 5, 16, 19, i, 0, 0, time.UTC)
		if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
			Surface:     workSurface.Surface,
			UpdateID:    updateID,
			UpdateKind:  string(workSurface.Kind),
			ChatID:      msg.ChatID,
			SenderID:    msg.SenderID,
			MessageID:   msg.MessageID,
			SessionID:   core.SessionIDForInboundMessage(msg),
			Status:      session.TelegramIngressUpdateAccepted,
			InboundJSON: string(raw),
			AcceptedAt:  now,
			UpdatedAt:   now,
		}); err != nil {
			t.Fatalf("RecordTelegramIngressAccepted(%s) err = %v", workSurface.Surface, err)
		}
	}

	seenReplay := make(map[string]core.InboundMessage)
	checkpoint, err := telegramruntime.ReplayStartupIngress(context.Background(), store, func(_ context.Context, msg core.InboundMessage) error {
		seenReplay[msg.IngressSurface] = msg
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("telegramruntime.ReplayStartupIngress() err = %v", err)
	}
	if checkpoint == nil {
		t.Fatal("telegramruntime.ReplayStartupIngress() checkpoint = nil")
	}
	if next, err := checkpoint.NextUpdateID(context.Background()); err != nil || next != 801 {
		t.Fatalf("primary checkpoint next = %d err=%v, want 801", next, err)
	}

	for i, workSurface := range surfaces {
		msg, ok := seenReplay[workSurface.Surface]
		if !ok {
			t.Fatalf("surface %s was not replayed; seen=%#v", workSurface.Surface, seenReplay)
		}
		if got, want := msg.IngressUpdateID, int64(800+i); got != want {
			t.Fatalf("surface %s replay update_id = %d, want %d", workSurface.Surface, got, want)
		}
		if msg.Text != "stored "+workSurface.Name+" work" {
			t.Fatalf("surface %s replay text = %q, want stored typed payload", workSurface.Surface, msg.Text)
		}
		if next, err := store.TelegramIngressNextUpdateID(workSurface.Surface); err != nil || next != int64(801+i) {
			t.Fatalf("surface %s next_update_id = %d err=%v, want %d", workSurface.Surface, next, err, 801+i)
		}
		pending, err := store.PendingTelegramIngressUpdates(workSurface.Surface, 10)
		if err != nil {
			t.Fatalf("PendingTelegramIngressUpdates(%s) err = %v", workSurface.Surface, err)
		}
		if len(pending) != 0 {
			t.Fatalf("surface %s pending = %#v, want replay completed", workSurface.Surface, pending)
		}
	}
}
