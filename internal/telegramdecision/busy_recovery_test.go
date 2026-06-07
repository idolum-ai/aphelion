//go:build linux

package telegramdecision

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestResumePendingBusyDecisionRecordsSyntheticIngressAndClearsPending(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	msg := core.InboundMessage{
		ChatID:           7,
		SenderID:         42,
		MessageID:        99,
		TelegramThreadID: 3,
		IngressSurface:   telegramruntime.PrimaryIngressSurface,
		IngressUpdateID:  701,
		Text:             "next task",
	}
	ownerKey := telegramruntime.SessionOwnerKey(msg)
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	if err := store.UpsertPendingBusyDecision(session.PendingBusyDecisionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		SessionID:          core.SessionIDForInboundMessage(msg),
		ScopeKind:          string(session.ScopeKindTelegramThread),
		ScopeID:            "7:3",
		MessageID:          msg.MessageID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		t.Fatalf("UpsertPendingBusyDecision() err = %v", err)
	}

	router := &decisionAcceptedTestRouter{decisionTestRouter: &decisionTestRouter{}}
	handler := NewHandler(&decisionTestSender{}, router, decision.NewBroker(nil), store)
	if err := handler.ResumePendingBusyDecision(context.Background(), ownerKey, decision.Result{Choice: "queue"}); err != nil {
		t.Fatalf("ResumePendingBusyDecision() err = %v", err)
	}

	if len(router.accepted) != 1 {
		t.Fatalf("accepted = %#v, want one synthetic ingress route", router.accepted)
	}
	routed := router.accepted[0]
	if routed.IngressSurface != telegramruntime.BusyDecisionResumeIngressSurface || routed.IngressUpdateID != 701 || routed.TelegramThreadID != 3 || routed.Text != "next task" {
		t.Fatalf("routed = %#v, want thread-scoped busy decision resume ingress", routed)
	}
	record, ok, err := store.TelegramIngressUpdate(telegramruntime.BusyDecisionResumeIngressSurface, 701)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateAccepted || record.SessionID != "telegram_thread:7:3" || !strings.Contains(record.InboundJSON, `"TelegramThreadID":3`) {
		t.Fatalf("record = %#v, want recoverable accepted thread ingress", record)
	}
	if _, err := store.PendingBusyDecision(ownerKey); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("PendingBusyDecision() err = %v, want sql.ErrNoRows after successful synthetic accept", err)
	}
}

func TestResumePendingBusyDecisionKeepsPendingWhenSyntheticRouteFails(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	msg := core.InboundMessage{
		ChatID:          7,
		SenderID:        42,
		MessageID:       99,
		IngressSurface:  telegramruntime.PrimaryIngressSurface,
		IngressUpdateID: 702,
		Text:            "next task",
	}
	ownerKey := telegramruntime.SessionOwnerKey(msg)
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	if err := store.UpsertPendingBusyDecision(session.PendingBusyDecisionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		SessionID:          core.SessionIDForInboundMessage(msg),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "7",
		MessageID:          msg.MessageID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		t.Fatalf("UpsertPendingBusyDecision() err = %v", err)
	}

	routeErr := errors.New("route unavailable")
	router := &decisionAcceptedTestRouter{decisionTestRouter: &decisionTestRouter{}, acceptedErr: routeErr}
	handler := NewHandler(&decisionTestSender{}, router, decision.NewBroker(nil), store)
	if err := handler.ResumePendingBusyDecision(context.Background(), ownerKey, decision.Result{Choice: "queue"}); !errors.Is(err, routeErr) {
		t.Fatalf("ResumePendingBusyDecision() err = %v, want route error", err)
	}
	if _, err := store.PendingBusyDecision(ownerKey); err != nil {
		t.Fatalf("PendingBusyDecision() err = %v, want pending row retained for retry", err)
	}
	record, ok, err := store.TelegramIngressUpdate(telegramruntime.BusyDecisionResumeIngressSurface, 702)
	if err != nil || !ok {
		t.Fatalf("TelegramIngressUpdate() ok=%t err=%v", ok, err)
	}
	if record.Status != session.TelegramIngressUpdateAccepted {
		t.Fatalf("record status = %s, want accepted recoverable ingress despite route failure", record.Status)
	}
}

func TestRestartLoadedBusyDecisionCallbackResumesPendingMessage(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	durable := NewDurableStore(store)
	senderBeforeRestart := &decisionTestSender{}
	brokerBeforeRestart := NewBroker(senderBeforeRestart, decision.WithDurableStore(durable))
	handlerBeforeRestart := NewHandler(senderBeforeRestart, &decisionTestRouter{status: core.SessionStatus{Active: true}}, brokerBeforeRestart, store)
	handlerBeforeRestart.interruptTimeout = time.Hour

	msg := core.InboundMessage{
		ChatID:          7,
		SenderID:        42,
		MessageID:       99,
		IngressSurface:  telegramruntime.PrimaryIngressSurface,
		IngressUpdateID: 9001,
		Text:            "queue this after restart",
	}
	handled, err := handlerBeforeRestart.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	pending := waitForStoredPendingDecision(t, store, decision.KindInterrupt)

	senderAfterRestart := &decisionTestSender{}
	routerAfterRestart := &decisionAcceptedTestRouter{decisionTestRouter: &decisionTestRouter{}}
	brokerAfterRestart := NewBroker(senderAfterRestart, decision.WithDurableStore(durable))
	if err := brokerAfterRestart.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	loaded, ok := brokerAfterRestart.Peek(pending.ID)
	if !ok || !loaded.LoadedFromDurable {
		t.Fatalf("loaded pending = %#v, ok=%t; want restart-loaded decision", loaded, ok)
	}
	handlerAfterRestart := NewHandler(senderAfterRestart, routerAfterRestart, brokerAfterRestart, store)

	err = handlerAfterRestart.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:       "cb-restart-busy",
		Data:     decision.EncodeCallbackData(pending.ID, "queue"),
		UpdateID: 9002,
		From:     &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: pending.DeliveryMessageID,
			Chat:      &telegram.Chat{ID: 7, Type: "private"},
		},
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	if len(senderAfterRestart.answers) != 1 || senderAfterRestart.answers[0].text != "" {
		t.Fatalf("answers = %#v, want one success acknowledgement", senderAfterRestart.answers)
	}
	if len(routerAfterRestart.accepted) != 1 {
		t.Fatalf("accepted = %#v, want one resumed synthetic ingress", routerAfterRestart.accepted)
	}
	routed := routerAfterRestart.accepted[0]
	if routed.IngressSurface != telegramruntime.BusyDecisionResumeIngressSurface || routed.IngressUpdateID != 9001 || routed.Text != msg.Text {
		t.Fatalf("routed = %#v, want original busy message on synthetic surface", routed)
	}
	if _, err := store.PendingBusyDecision(telegramruntime.SessionOwnerKey(msg)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("PendingBusyDecision() err = %v, want cleared after restart callback resume", err)
	}
	records, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	for _, record := range records {
		if record.ID == pending.ID {
			t.Fatalf("pending decision %s survived restart callback resolution", pending.ID)
		}
	}
}

func TestRestartReconciliationReissuesLoadedBusyPrompt(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	msg := core.InboundMessage{ChatID: 7, SenderID: 42, MessageID: 99, IngressSurface: telegramruntime.PrimaryIngressSurface, IngressUpdateID: 9201, Text: "resume after restart"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	ownerKey := telegramruntime.SessionOwnerKey(msg)
	if err := store.UpsertPendingBusyDecision(session.PendingBusyDecisionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		SessionID:          core.SessionIDForInboundMessage(msg),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "7",
		MessageID:          msg.MessageID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		t.Fatalf("UpsertPendingBusyDecision() err = %v", err)
	}
	choicesJSON, err := json.Marshal([]decision.Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}})
	if err != nil {
		t.Fatalf("Marshal(choices) err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:                "old-busy",
		Sequence:          50,
		OwnerKey:          ownerKey,
		SessionID:         core.SessionIDForInboundMessage(msg),
		ScopeKind:         string(session.ScopeKindTelegramDM),
		ScopeID:           "7",
		Kind:              string(decision.KindInterrupt),
		ChatID:            msg.ChatID,
		SenderID:          msg.SenderID,
		MessageID:         msg.MessageID,
		Prompt:            "I'm still working on the previous request. What would you like to do?",
		ChoicesJSON:       string(choicesJSON),
		DefaultChoice:     "queue",
		TimeoutNanos:      int64(time.Hour),
		DeliveryMessageID: 7004,
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}

	sender := &decisionTestSender{}
	broker := NewBroker(sender, decision.WithDurableStore(NewDurableStore(store)))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	handler := NewHandler(sender, &decisionAcceptedTestRouter{decisionTestRouter: &decisionTestRouter{}}, broker, store)
	handler.interruptTimeout = time.Hour
	if err := handler.ReconcileRestartLoadedDecisions(context.Background()); err != nil {
		t.Fatalf("ReconcileRestartLoadedDecisions() err = %v", err)
	}
	_ = waitForDecisionInline(t, sender)
	reissued := waitForStoredPendingDecision(t, store, decision.KindInterrupt)
	if reissued.ID == "old-busy" || reissued.DeliveryMessageID == 0 {
		t.Fatalf("reissued pending decision = %#v, want fresh delivered prompt", reissued)
	}
	if _, ok := broker.Peek("old-busy"); ok {
		t.Fatal("old restart-loaded busy decision remained pending after reissue")
	}
}

func TestRestartReconciliationAppliesExpiredBusyDefaultThroughSyntheticIngress(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	createdAt := time.Now().UTC().Add(-time.Minute)
	msg := core.InboundMessage{ChatID: 7, SenderID: 42, MessageID: 99, IngressSurface: telegramruntime.PrimaryIngressSurface, IngressUpdateID: 9301, Text: "expired busy message"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	ownerKey := telegramruntime.SessionOwnerKey(msg)
	if err := store.UpsertPendingBusyDecision(session.PendingBusyDecisionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		SessionID:          core.SessionIDForInboundMessage(msg),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "7",
		MessageID:          msg.MessageID,
		InboundMessageJSON: string(raw),
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
	}); err != nil {
		t.Fatalf("UpsertPendingBusyDecision() err = %v", err)
	}

	router := &decisionAcceptedTestRouter{decisionTestRouter: &decisionTestRouter{}}
	handler := NewHandler(&decisionTestSender{}, router, NewBroker(&decisionTestSender{}, decision.WithDurableStore(NewDurableStore(store))), store)
	handler.interruptTimeout = time.Millisecond
	if err := handler.ReconcileRestartLoadedDecisions(context.Background()); err != nil {
		t.Fatalf("ReconcileRestartLoadedDecisions() err = %v", err)
	}
	if len(router.accepted) != 1 {
		t.Fatalf("accepted = %#v, want expired default routed through synthetic ingress", router.accepted)
	}
	if router.accepted[0].IngressSurface != telegramruntime.BusyDecisionResumeIngressSurface || router.accepted[0].IngressUpdateID != 9301 {
		t.Fatalf("accepted[0] = %#v, want busy decision resume surface", router.accepted[0])
	}
	if _, err := store.PendingBusyDecision(ownerKey); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("PendingBusyDecision() err = %v, want cleared after expired default", err)
	}
}

func TestRestartReconciliationDetachesLoadedBusyPromptWhenResumeIngressOwnsWork(t *testing.T) {
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	msg := core.InboundMessage{ChatID: 7, SenderID: 42, MessageID: 99, IngressSurface: telegramruntime.PrimaryIngressSurface, IngressUpdateID: 9401, Text: "already accepted after callback"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal() err = %v", err)
	}
	ownerKey := telegramruntime.SessionOwnerKey(msg)
	if err := store.UpsertPendingBusyDecision(session.PendingBusyDecisionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		SessionID:          core.SessionIDForInboundMessage(msg),
		ScopeKind:          string(session.ScopeKindTelegramDM),
		ScopeID:            "7",
		MessageID:          msg.MessageID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		t.Fatalf("UpsertPendingBusyDecision() err = %v", err)
	}
	choicesJSON, err := json.Marshal([]decision.Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}})
	if err != nil {
		t.Fatalf("Marshal(choices) err = %v", err)
	}
	if err := store.UpsertPendingDecision(session.PendingDecisionRecord{
		ID:                "old-busy-owned-by-replay",
		Sequence:          60,
		OwnerKey:          ownerKey,
		SessionID:         core.SessionIDForInboundMessage(msg),
		ScopeKind:         string(session.ScopeKindTelegramDM),
		ScopeID:           "7",
		Kind:              string(decision.KindInterrupt),
		ChatID:            msg.ChatID,
		SenderID:          msg.SenderID,
		MessageID:         msg.MessageID,
		Prompt:            "I'm still working on the previous request. What would you like to do?",
		ChoicesJSON:       string(choicesJSON),
		DefaultChoice:     "queue",
		TimeoutNanos:      int64(time.Hour),
		DeliveryMessageID: 7005,
	}); err != nil {
		t.Fatalf("UpsertPendingDecision() err = %v", err)
	}
	if _, err := store.RecordTelegramIngressAccepted(session.TelegramIngressUpdateRecord{
		Surface:     telegramruntime.BusyDecisionResumeIngressSurface,
		UpdateID:    DecisionResumeUpdateID(msg, telegramruntime.BusyDecisionResumeIngressSurface),
		UpdateKind:  "decision_resume_busy",
		ChatID:      msg.ChatID,
		SenderID:    msg.SenderID,
		MessageID:   msg.MessageID,
		SessionID:   core.SessionIDForInboundMessage(msg),
		Status:      session.TelegramIngressUpdateAccepted,
		InboundJSON: string(raw),
		AcceptedAt:  time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordTelegramIngressAccepted() err = %v", err)
	}

	broker := NewBroker(&decisionTestSender{}, decision.WithDurableStore(NewDurableStore(store)))
	if err := broker.Load(context.Background()); err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	handler := NewHandler(&decisionTestSender{}, &decisionAcceptedTestRouter{decisionTestRouter: &decisionTestRouter{}}, broker, store)
	if err := handler.ReconcileRestartLoadedDecisions(context.Background()); err != nil {
		t.Fatalf("ReconcileRestartLoadedDecisions() err = %v", err)
	}
	if _, ok := broker.Peek("old-busy-owned-by-replay"); ok {
		t.Fatal("restart-loaded busy prompt remained active while resume ingress owned the work")
	}
	if _, err := store.PendingBusyDecision(ownerKey); err != nil {
		t.Fatalf("PendingBusyDecision() err = %v, want retained until resume ingress terminalizes", err)
	}
}

func TestTelegramPollerBusyMessageCallbackStarvesBehindBlockingMessageHandler(t *testing.T) {
	requireLocalTCPListener(t, "localhost:0")
	t.Parallel()

	sender := &decisionTestSender{}
	router := &decisionTestRouter{status: core.SessionStatus{Active: true}}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	var mu sync.Mutex
	getUpdatesCalls := 0
	secondGetUpdatesAt := time.Time{}
	callbackHandledAt := time.Time{}
	callbackDataReady := make(chan string, 1)
	broker := decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := RenderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, InlineButtonRows(pending), telegramcommands.ReplyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		select {
		case callbackDataReady <- decision.EncodeCallbackData(pending.ID, "stop"):
		default:
		}
		return decision.Delivery{MessageID: msgID}, nil
	})
	handler := NewHandler(sender, router, broker, store)
	handler.interruptTimeout = 3 * time.Second

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/getUpdates" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		getUpdatesCalls += 1
		call := getUpdatesCalls
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		now := time.Now().Unix()
		switch call {
		case 1:
			_ = enc.Encode(map[string]any{
				"ok": true,
				"result": []any{map[string]any{
					"update_id": 1,
					"message": map[string]any{
						"message_id": 101,
						"date":       now,
						"chat":       map[string]any{"id": int64(7), "type": "private"},
						"from":       map[string]any{"id": int64(42), "first_name": "Test"},
						"text":       "new request while busy",
					},
				}},
			})
		case 2:
			mu.Lock()
			secondGetUpdatesAt = time.Now().UTC()
			mu.Unlock()
			callbackData := ""
			select {
			case callbackData = <-callbackDataReady:
			case <-time.After(500 * time.Millisecond):
			}
			_ = enc.Encode(map[string]any{
				"ok": true,
				"result": []any{map[string]any{
					"update_id": 2,
					"callback_query": map[string]any{
						"id":   "cb-busy-1",
						"data": callbackData,
						"from": map[string]any{"id": int64(42), "first_name": "Test"},
						"message": map[string]any{
							"message_id": 1,
							"date":       now,
							"chat":       map[string]any{"id": int64(7), "type": "private"},
						},
					},
				}},
			})
		default:
			_ = enc.Encode(map[string]any{"ok": true, "result": []any{}})
		}
	}))
	defer server.Close()

	client := telegram.NewClient("TOKEN", telegram.WithBaseURL(server.URL+"/botTOKEN/"), telegram.WithHTTPClient(server.Client()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poller := telegram.NewPoller(client, func(ctx context.Context, msg core.InboundMessage) error {
		if handled, err := handler.HandleBusyMessage(ctx, msg); err != nil {
			return err
		} else if !handled {
			t.Fatal("busy message was not handled")
		}
		return nil
	}, telegram.WithCallbackHandler(func(ctx context.Context, cb telegram.CallbackQuery) error {
		mu.Lock()
		callbackHandledAt = time.Now().UTC()
		mu.Unlock()
		defer cancel()
		return handler.HandleCallbackQuery(ctx, cb)
	}))

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Poller.Run() err = %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for len(router.stopForMessage) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(router.stopForMessage) != 1 {
		t.Fatalf("stopForMessage = %#v, want one stop after callback", router.stopForMessage)
	}
	if len(router.routed) != 1 || router.routed[0].Text != "new request while busy" {
		t.Fatalf("routed = %#v, want original message re-routed after stop", router.routed)
	}
	if len(sender.deletes) != 1 {
		t.Fatalf("deletes = %#v, want prompt deleted on stop", sender.deletes)
	}
	if len(sender.answers) == 0 {
		t.Fatalf("answers = %#v, want callback acknowledgement", sender.answers)
	}
	if got := sender.answers[len(sender.answers)-1].text; got != "" {
		t.Fatalf("callback answer = %q, want empty success acknowledgement", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if secondGetUpdatesAt.IsZero() {
		t.Fatal("second getUpdates call was never observed")
	}
	if callbackHandledAt.IsZero() {
		t.Fatal("callback was never handled")
	}
	if !secondGetUpdatesAt.Before(callbackHandledAt.Add(250 * time.Millisecond)) {
		t.Fatalf("second getUpdates at %s should arrive before or near callback handling at %s once poller is unblocked", secondGetUpdatesAt, callbackHandledAt)
	}
}
