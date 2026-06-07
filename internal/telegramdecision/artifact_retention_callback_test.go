//go:build linux

package telegramdecision

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTelegramPollerArtifactRetentionCallbackStarvesBehindBlockingMessageHandler(t *testing.T) {
	requireLocalTCPListener(t, "localhost:0")
	t.Parallel()

	sender := &decisionTestSender{}
	router := &decisionTestRouter{}
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
		case callbackDataReady <- decision.EncodeCallbackData(pending.ID, "local"):
		default:
		}
		return decision.Delivery{MessageID: msgID}, nil
	})
	handler := NewHandler(sender, router, broker, store)
	handler.artifactRetentionTimeout = 3 * time.Second

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
						"message_id": 99,
						"date":       now,
						"chat":       map[string]any{"id": int64(7), "type": "private"},
						"from":       map[string]any{"id": int64(42), "first_name": "Test"},
						"document": map[string]any{
							"file_id":        "file-1",
							"file_unique_id": "file-1u",
							"file_name":      "bundle.zip",
							"mime_type":      "application/zip",
							"file_size":      12,
						},
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
						"id":   "cb-1",
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
		if handled, err := handler.HandleArtifactRetentionMessage(ctx, msg); err != nil {
			return err
		} else if !handled {
			t.Fatal("retention message was not handled")
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
	for len(router.routed) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed message after callback", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "local" {
		t.Fatalf("retention choice = %q, want local callback choice", got)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "save the file locally") {
		t.Fatalf("edits = %#v, want local-save confirmation", sender.edits)
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
	if len(sender.edits) == 0 {
		t.Fatalf("edits = %#v, want one retention-resolution edit", sender.edits)
	}
	if callbackHandledAt.After(sender.edits[0].at.Add(250 * time.Millisecond)) {
		t.Fatalf("callback handled at %s far after edit at %s; want prompt callback processed promptly", callbackHandledAt, sender.edits[0].at)
	}
	if !secondGetUpdatesAt.Before(sender.edits[0].at) {
		t.Fatalf("second getUpdates at %s should arrive before retention-resolution edit at %s once poller is unblocked", secondGetUpdatesAt, sender.edits[0].at)
	}
}

func TestInlineButtonRowsNormalizesAffirmativeNegativePairOrder(t *testing.T) {
	t.Parallel()

	rows := InlineButtonRows(decision.PendingDecision{
		ID:      "decision-1",
		Request: decision.Request{Choices: []decision.Choice{{ID: "approve", Label: "Approve"}, {ID: "deny", Label: "Deny"}}},
	})
	if len(rows) != 1 {
		t.Fatalf("rows = %#v, want one row", rows)
	}
	if len(rows[0]) != 2 {
		t.Fatalf("buttons = %#v, want two buttons", rows[0])
	}
	if rows[0][0].Text != "Deny" || rows[0][1].Text != "Approve" {
		t.Fatalf("choice order = %#v, want [Deny, Approve]", rows[0])
	}
}

func TestInlineButtonRowsPreservesStopQueueOrder(t *testing.T) {
	t.Parallel()

	rows := InlineButtonRows(decision.PendingDecision{
		ID:      "decision-2",
		Request: decision.Request{Choices: []decision.Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}}},
	})
	if len(rows) != 1 {
		t.Fatalf("rows = %#v, want one row", rows)
	}
	if len(rows[0]) != 2 {
		t.Fatalf("buttons = %#v, want two buttons", rows[0])
	}
	if rows[0][0].Text != "Stop" || rows[0][1].Text != "Queue" {
		t.Fatalf("choice order = %#v, want [Stop, Queue]", rows[0])
	}
}

func TestTelegramUserApprovalTimeoutDefaultsToThirtyMinutes(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := decision.NewBroker(nil)
	handler := NewHandler(sender, &decisionTestRouter{}, broker, nil)
	execApprover := NewExecApprover(sender, broker, DefaultExecApprovalTimeout)
	memoryApprover := NewDurableMemoryDelegationApprover(sender, broker, DefaultMemoryDelegationTimeout)
	snapshotApprover := NewDurableSnapshotRestoreApprover(sender, broker, DefaultSnapshotRestoreTimeout)

	want := 30 * time.Minute
	if DefaultUserApprovalTimeout != want {
		t.Fatalf("DefaultUserApprovalTimeout = %s, want %s", DefaultUserApprovalTimeout, want)
	}
	if execApprover.Timeout() != want {
		t.Fatalf("exec approval timeout = %s, want %s", execApprover.Timeout(), want)
	}
	if handler.artifactRetentionTimeout != want {
		t.Fatalf("artifact retention timeout = %s, want %s", handler.artifactRetentionTimeout, want)
	}
	if memoryApprover.Timeout() != want {
		t.Fatalf("memory delegation timeout = %s, want %s", memoryApprover.Timeout(), want)
	}
	if snapshotApprover.Timeout() != want {
		t.Fatalf("snapshot restore timeout = %s, want %s", snapshotApprover.Timeout(), want)
	}
}
