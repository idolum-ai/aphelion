//go:build linux

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcontrol"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestThreadSummaryCallbackQueuesMainThreadWork(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadSummaryReturn: "Analysis queued."}
	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, telegram.CallbackQuery{
		ID:       "cb-thread-summary",
		From:     &telegram.User{ID: 2002},
		Data:     telegramThreadSummaryCallbackData,
		UpdateID: 808,
		Message: &telegram.Message{
			MessageID: 3003,
			Chat:      &telegram.Chat{ID: 1001, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.threadSummaryMsg == nil || router.threadSummaryMsg.ChatID != 1001 || router.threadSummaryMsg.SenderID != 2002 || router.threadSummaryMsg.TelegramThreadID != 0 {
		t.Fatalf("threadSummaryMsg = %#v, want main-thread routed work", router.threadSummaryMsg)
	}
	if router.threadSummaryMsg.IngressSurface != telegramThreadSummaryIngressSurface || router.threadSummaryMsg.IngressUpdateID != 808 {
		t.Fatalf("threadSummaryMsg ingress = %s/%d, want durable callback surface/update", router.threadSummaryMsg.IngressSurface, router.threadSummaryMsg.IngressUpdateID)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "Analysis queued." {
		t.Fatalf("answers = %#v, want queued acknowledgement", sender.answers)
	}
	if len(sender.editClear) != 0 || len(sender.edits) != 0 || len(sender.editInline) != 0 {
		t.Fatalf("edits clear=%#v edits=%#v inline=%#v, want no callback message edit", sender.editClear, sender.edits, sender.editInline)
	}
}

func TestQueueTelegramThreadSummaryRoutesMainThreadEvidence(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	openThread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 101, 3001, "open child setup", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(open) err = %v", err)
	}
	closedThread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 102, 3002, "closed child setup", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(closed) err = %v", err)
	}
	if _, closed, err := store.CloseTelegramThread(1001, closedThread.ThreadID, "closed already", now); err != nil || !closed {
		t.Fatalf("CloseTelegramThread() closed=%t err=%v", closed, err)
	}

	openKey := session.SessionKey{ChatID: 1001, UserID: 0, Scope: session.TelegramThreadScopeRef(1001, openThread.ThreadID)}
	openSession, err := store.Load(openKey)
	if err != nil {
		t.Fatalf("Load(open thread) err = %v", err)
	}
	openSession.TurnCount = 1
	if err := store.Save(openSession, []session.Message{
		{Role: "user", Content: "Open user request", TurnIndex: 1},
		{Role: "assistant", Content: "Open assistant result", TurnIndex: 1},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(open thread) err = %v", err)
	}

	var routed core.InboundMessage
	router := core.NewRouter(func(_ context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		routed = msg
		return &core.TurnResult{}, nil
	})
	control := telegramCommandControl{store: store, router: router}
	text, err := control.QueueTelegramThreadSummary(context.Background(), core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		IngressSurface:  telegramThreadSummaryIngressSurface,
		IngressUpdateID: 909,
		Text:            "/threads analyze",
	})
	if err != nil {
		t.Fatalf("QueueTelegramThreadSummary() err = %v", err)
	}
	if text != "Analysis queued." {
		t.Fatalf("QueueTelegramThreadSummary() text = %q, want queued ack", text)
	}
	if routed.TelegramThreadID != 0 || core.SessionIDForInboundMessage(routed) != "telegram_dm:1001" {
		t.Fatalf("routed = %#v, want main chat session", routed)
	}
	if !strings.Contains(routed.Text, "Thread-board evidence") || !strings.Contains(routed.Text, "Quick read:") || !strings.Contains(routed.Text, "Needs action:") {
		t.Fatalf("routed text = %q, want structured analysis prompt", routed.Text)
	}
	if !strings.Contains(routed.Text, "Thread 1") || !strings.Contains(routed.Text, "display_thread: 1") || !strings.Contains(routed.Text, "internal_thread_id: 1") {
		t.Fatalf("routed text = %q, want display and internal thread identifiers", routed.Text)
	}
	if !strings.Contains(routed.Text, "last_active:") || !strings.Contains(routed.Text, "turn_count: 1") || !strings.Contains(routed.Text, "Open assistant result") {
		t.Fatalf("routed text = %q, want enriched open thread evidence", routed.Text)
	}
	if strings.Contains(routed.Text, "Thread 2") || strings.Contains(routed.Text, "closed child setup") {
		t.Fatalf("routed text = %q, want closed thread excluded", routed.Text)
	}
	pending, err := store.PendingTelegramIngressUpdates(telegramThreadSummaryIngressSurface, 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates(summary surface) err = %v", err)
	}
	if len(pending) != 1 || pending[0].UpdateID != 909 || pending[0].Status != session.TelegramIngressUpdateQueued || pending[0].SessionID != "telegram_dm:1001" {
		t.Fatalf("pending summary ingress = %#v, want queued callback-work row", pending)
	}
	if !strings.Contains(pending[0].InboundJSON, "Thread-board evidence") || !strings.Contains(pending[0].InboundJSON, "display_thread") {
		t.Fatalf("pending inbound json = %q, want durable analysis quest payload", pending[0].InboundJSON)
	}
}

func TestQueueTelegramThreadSummarySuppressesDuplicateCallbackWork(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 101, 3001, "open child setup", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadKey := session.SessionKey{ChatID: 1001, UserID: 0, Scope: session.TelegramThreadScopeRef(1001, thread.ThreadID)}
	threadSession, err := store.Load(threadKey)
	if err != nil {
		t.Fatalf("Load(thread) err = %v", err)
	}
	threadSession.TurnCount = 1
	if err := store.Save(threadSession, []session.Message{
		{Role: "user", Content: "Open user request", TurnIndex: 1},
		{Role: "assistant", Content: "Open assistant result", TurnIndex: 1},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(thread) err = %v", err)
	}

	started := make(chan core.InboundMessage, 3)
	release := make(chan struct{})
	router := core.NewRouter(func(ctx context.Context, _ *core.SessionState, msg core.InboundMessage) (*core.TurnResult, error) {
		started <- msg
		select {
		case <-ctx.Done():
		case <-release:
		}
		return &core.TurnResult{}, nil
	})
	ingress := telegramcontrol.NewIngressSequencer(router, time.Minute)
	t.Cleanup(ingress.Close)
	control := telegramCommandControl{store: store, router: router, ingress: ingress}

	msg := core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		IngressSurface:  telegramThreadSummaryIngressSurface,
		IngressUpdateID: 910,
		Text:            "/threads analyze",
	}
	if text, err := control.QueueTelegramThreadSummary(context.Background(), msg); err != nil || text != "Analysis queued." {
		t.Fatalf("first QueueTelegramThreadSummary() text=%q err=%v, want queued ack", text, err)
	}
	select {
	case first := <-started:
		if first.IngressSurface != telegramThreadSummaryIngressSurface || first.IngressUpdateID != 910 || !strings.Contains(first.Text, "Thread-board evidence") || !strings.Contains(first.Text, "display_thread") {
			t.Fatalf("first started = %#v, want callback-work analysis quest", first)
		}
	case <-time.After(time.Second):
		t.Fatal("first summary callback work did not start")
	}

	duplicate := msg
	duplicate.MessageID = 3004
	if text, err := control.QueueTelegramThreadSummary(context.Background(), duplicate); err != nil || text != "Analysis queued." {
		t.Fatalf("duplicate QueueTelegramThreadSummary() text=%q err=%v, want idempotent queued ack", text, err)
	}
	if status := ingress.Status(1001); status.QueueDepth != 0 {
		t.Fatalf("ingress status = %#v, want duplicate callback work suppressed instead of queued", status)
	}

	close(release)
	select {
	case got := <-started:
		t.Fatalf("unexpected duplicate callback work started: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestThreadSummaryCallbackWorkReplaysStoredQuest(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 101, 3001, "open child setup", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadKey := session.SessionKey{ChatID: 1001, UserID: 0, Scope: session.TelegramThreadScopeRef(1001, thread.ThreadID)}
	threadSession, err := store.Load(threadKey)
	if err != nil {
		t.Fatalf("Load(thread) err = %v", err)
	}
	threadSession.TurnCount = 1
	if err := store.Save(threadSession, []session.Message{
		{Role: "user", Content: "Open user request", TurnIndex: 1},
		{Role: "assistant", Content: "Open assistant result", TurnIndex: 1},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(thread) err = %v", err)
	}

	control := telegramCommandControl{store: store}
	if _, err := control.QueueTelegramThreadSummary(context.Background(), core.InboundMessage{
		ChatID:          1001,
		SenderID:        2002,
		MessageID:       3003,
		IngressSurface:  telegramThreadSummaryIngressSurface,
		IngressUpdateID: 909,
		Text:            "/threads analyze",
	}); err != nil {
		t.Fatalf("QueueTelegramThreadSummary() err = %v", err)
	}

	var replayed core.InboundMessage
	checkpoint := newTelegramIngressCheckpoint(store, telegramThreadSummaryIngressSurface)
	err = replayPendingTelegramIngress(context.Background(), store, checkpoint, func(_ context.Context, msg core.InboundMessage) error {
		replayed = msg
		run, err := store.BeginTurnRunForTelegramIngress(
			session.SessionKey{ChatID: msg.ChatID, UserID: 0},
			session.TurnRunKindInteractive,
			msg.Text,
			msg.IngressSurface,
			msg.IngressUpdateID,
		)
		if err != nil {
			return err
		}
		return store.MarkTelegramIngressCompleted(msg.IngressSurface, msg.IngressUpdateID, run.ID, session.TelegramIngressUpdateCompleted, "", time.Now().UTC())
	}, telegramThreadSummaryIngressSurface, 10, nil)
	if err != nil {
		t.Fatalf("replayPendingTelegramIngress() err = %v", err)
	}
	if replayed.ChatID != 1001 || replayed.TelegramThreadID != 0 || replayed.IngressSurface != telegramThreadSummaryIngressSurface || replayed.IngressUpdateID != 909 {
		t.Fatalf("replayed = %#v, want main-chat callback-work ingress", replayed)
	}
	if !strings.Contains(replayed.Text, "Thread-board evidence") || !strings.Contains(replayed.Text, "display_thread: 1") || !strings.Contains(replayed.Text, "Open assistant result") {
		t.Fatalf("replayed text = %q, want stored side-thread analysis evidence", replayed.Text)
	}
	pending, err := store.PendingTelegramIngressUpdates(telegramThreadSummaryIngressSurface, 10)
	if err != nil {
		t.Fatalf("PendingTelegramIngressUpdates() err = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want replayed summary work terminal", pending)
	}
	if next, err := store.TelegramIngressNextUpdateID(telegramThreadSummaryIngressSurface); err != nil || next != 910 {
		t.Fatalf("TelegramIngressNextUpdateID() = %d err=%v, want 910", next, err)
	}
}
