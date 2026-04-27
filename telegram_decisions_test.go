//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	toolpkg "github.com/idolum-ai/aphelion/tool"
)

type decisionTestSender struct {
	inline    []decisionInlineCall
	edits     []decisionEditCall
	deletes   []decisionDeleteCall
	answers   []decisionAnswerCall
	answerErr error
}

type decisionInlineCall struct {
	chatID  int64
	text    string
	rows    [][]telegram.InlineButton
	replyTo *int64
}

type decisionEditCall struct {
	chatID    int64
	messageID int64
	text      string
	rows      [][]telegram.InlineButton
	at        time.Time
}

type decisionDeleteCall struct {
	chatID    int64
	messageID int64
}

type decisionAnswerCall struct {
	id   string
	text string
	at   time.Time
}

func (s *decisionTestSender) SendInlineKeyboard(_ context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error) {
	s.inline = append(s.inline, decisionInlineCall{chatID: chatID, text: text, rows: rows, replyTo: replyTo})
	return int64(len(s.inline)), nil
}

func (s *decisionTestSender) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, _ string) error {
	s.edits = append(s.edits, decisionEditCall{chatID: chatID, messageID: messageID, text: text, at: time.Now().UTC()})
	return nil
}

func (s *decisionTestSender) EditMessageTextWithInlineKeyboard(_ context.Context, chatID int64, messageID int64, text string, _ string, rows [][]telegram.InlineButton) error {
	s.edits = append(s.edits, decisionEditCall{chatID: chatID, messageID: messageID, text: text, rows: rows, at: time.Now().UTC()})
	return nil
}

func (s *decisionTestSender) DeleteMessage(_ context.Context, chatID int64, messageID int64) error {
	s.deletes = append(s.deletes, decisionDeleteCall{chatID: chatID, messageID: messageID})
	return nil
}

func (s *decisionTestSender) AnswerCallbackQuery(_ context.Context, id string, text string) error {
	s.answers = append(s.answers, decisionAnswerCall{id: id, text: text, at: time.Now().UTC()})
	return s.answerErr
}

type decisionTestRouter struct {
	status             core.SessionStatus
	statusForMessageFn func(core.InboundMessage) core.SessionStatus
	stopCalls          []int64
	stopForMessage     []core.InboundMessage
	routed             []core.InboundMessage
}

func (r *decisionTestRouter) Status(chatID int64) core.SessionStatus {
	return r.status
}

func (r *decisionTestRouter) StatusForMessage(msg core.InboundMessage) core.SessionStatus {
	if r.statusForMessageFn != nil {
		return r.statusForMessageFn(msg)
	}
	return r.status
}

func (r *decisionTestRouter) Stop(chatID int64) core.StopResult {
	r.stopCalls = append(r.stopCalls, chatID)
	return core.StopResult{ActiveCanceled: true}
}

func (r *decisionTestRouter) StopForMessage(msg core.InboundMessage) core.StopResult {
	r.stopForMessage = append(r.stopForMessage, msg)
	return core.StopResult{ActiveCanceled: true}
}

func (r *decisionTestRouter) Route(_ context.Context, msg core.InboundMessage) {
	r.routed = append(r.routed, msg)
}

func TestHandleBusyTelegramMessageQueuesMessageOnTimeout(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		return decision.Delivery{MessageID: 41}, nil
	})
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{status: core.SessionStatus{Active: true}}, broker, nil)
	handler.interruptTimeout = 10 * time.Millisecond
	handler.stopWordTimeout = 10 * time.Millisecond

	router := &decisionTestRouter{status: core.SessionStatus{Active: true}}
	handler.router = router
	msg := core.InboundMessage{ChatID: 7, MessageID: 99, Text: "next task"}

	handled, err := handler.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 || router.routed[0].Text != "next task" {
		t.Fatalf("routed = %#v, want queued message", router.routed)
	}
	if len(router.stopCalls) != 0 {
		t.Fatalf("stopCalls = %#v, want none", router.stopCalls)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits = %#v, want one timeout edit", sender.edits)
	}
}

func TestHandleBusyTelegramMessageStopWordOnlyCancelsWithoutRouting(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		go broker.Resolve(pending.ID, "stop")
		return decision.Delivery{MessageID: 11}, nil
	})
	router := &decisionTestRouter{status: core.SessionStatus{Active: true}}
	handler := newTelegramDecisionHandler(sender, router, broker, nil)

	handled, err := handler.HandleBusyMessage(context.Background(), core.InboundMessage{
		ChatID:    7,
		MessageID: 15,
		Text:      "wait",
	})
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.stopForMessage) != 1 {
		t.Fatalf("stopForMessage = %#v, want one scoped stop", router.stopForMessage)
	}
	if len(router.stopCalls) != 0 {
		t.Fatalf("stopCalls = %#v, want no chat-wide stop", router.stopCalls)
	}
	if len(router.routed) != 0 {
		t.Fatalf("routed = %#v, want no routed follow-up", router.routed)
	}
	if len(sender.deletes) != 1 {
		t.Fatalf("deletes = %#v, want prompt deleted", sender.deletes)
	}
}

func TestHandleBusyTelegramMessageStopWordWithContentRoutesAfterStop(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		go broker.Resolve(pending.ID, "stop")
		return decision.Delivery{MessageID: 22}, nil
	})
	router := &decisionTestRouter{status: core.SessionStatus{Active: true}}
	handler := newTelegramDecisionHandler(sender, router, broker, nil)

	msg := core.InboundMessage{ChatID: 7, MessageID: 15, Text: "wait, do X instead"}
	handled, err := handler.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.stopForMessage) != 1 {
		t.Fatalf("stopForMessage = %#v, want one scoped stop", router.stopForMessage)
	}
	if len(router.stopCalls) != 0 {
		t.Fatalf("stopCalls = %#v, want no chat-wide stop", router.stopCalls)
	}
	if len(router.routed) != 1 || router.routed[0].Text != msg.Text {
		t.Fatalf("routed = %#v, want original follow-up message", router.routed)
	}
}

func TestHandleBusyTelegramMessageUsesStatusForMessageWhenAvailable(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	router := &decisionTestRouter{
		status: core.SessionStatus{Active: false},
		statusForMessageFn: func(msg core.InboundMessage) core.SessionStatus {
			if msg.DurableAgentID == "agent-a" {
				return core.SessionStatus{Active: true}
			}
			return core.SessionStatus{Active: false}
		},
	}
	broker := decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		return decision.Delivery{MessageID: 41}, nil
	})
	handler := newTelegramDecisionHandler(sender, router, broker, nil)
	handler.interruptTimeout = 10 * time.Millisecond
	handler.stopWordTimeout = 10 * time.Millisecond

	msg := core.InboundMessage{
		ChatID:         7,
		MessageID:      99,
		DurableAgentID: "agent-a",
		Text:           "next task",
	}
	handled, err := handler.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed message", router.routed)
	}
}

func TestTelegramPollerBusyMessageCallbackStarvesBehindBlockingMessageHandler(t *testing.T) {
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
		text := renderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		select {
		case callbackDataReady <- decision.EncodeCallbackData(pending.ID, "stop"):
		default:
		}
		return decision.Delivery{MessageID: msgID}, nil
	})
	handler := newTelegramDecisionHandler(sender, router, broker, store)
	handler.interruptTimeout = 25 * time.Millisecond

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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

func TestHandleBusyTelegramMessageUsesStopForMessageWhenAvailable(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		go broker.Resolve(pending.ID, "stop")
		return decision.Delivery{MessageID: 22}, nil
	})
	router := &decisionTestRouter{
		status: core.SessionStatus{Active: false},
		statusForMessageFn: func(msg core.InboundMessage) core.SessionStatus {
			if msg.DurableAgentID == "agent-a" {
				return core.SessionStatus{Active: true}
			}
			return core.SessionStatus{Active: false}
		},
	}
	handler := newTelegramDecisionHandler(sender, router, broker, nil)

	msg := core.InboundMessage{
		ChatID:         7,
		MessageID:      15,
		DurableAgentID: "agent-a",
		Text:           "wait",
	}
	handled, err := handler.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.stopForMessage) != 1 {
		t.Fatalf("stopForMessage = %#v, want one scoped stop call", router.stopForMessage)
	}
	if len(router.stopCalls) != 0 {
		t.Fatalf("stopCalls = %#v, want no chat-wide stop calls", router.stopCalls)
	}
}

func TestTelegramExecApproverKeepsApprovalConfirmation(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := renderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		go broker.Resolve(pending.ID, "approve")
		return decision.Delivery{MessageID: msgID}, nil
	})
	approver := newTelegramExecApprover(sender, broker)

	decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
		Principal:  principal.Principal{Role: principal.RoleAdmin},
		SessionKey: session.SessionKey{ChatID: 7},
		Command:    "rm -rf build",
		Reason:     "recursive delete",
		Proposal: session.OperationProposal{
			Kind:          "destructive_mutation",
			Summary:       "Perform a destructive change",
			WhyNow:        "The requested command deletes existing local state.",
			BoundedEffect: "Remove the targeted files and continue the operation.",
			Status:        session.ProposalStatusPending,
		},
	})
	if err != nil {
		t.Fatalf("ConfirmExec() err = %v", err)
	}
	if !decisionResult.Approved {
		t.Fatal("Approved = false, want true")
	}
	if len(sender.deletes) != 0 {
		t.Fatalf("deletes = %#v, want no prompt delete on approval", sender.deletes)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits = %#v, want durable approval confirmation", sender.edits)
	}
	if !strings.Contains(sender.edits[0].text, "Proposal approved.") || !strings.Contains(sender.edits[0].text, "Decision:") || !strings.Contains(sender.edits[0].text, "Perform a destructive change") {
		t.Fatalf("approval edit = %q, want proposal confirmation with decision id and summary", sender.edits[0].text)
	}
	if !hasInlineButton(sender.edits[0].rows, "Expand details") {
		t.Fatalf("approval rows = %#v, want retained expand details button", sender.edits[0].rows)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one proposal prompt", sender.inline)
	}
	if !strings.Contains(sender.inline[0].text, "Perform a destructive change") {
		t.Fatalf("inline text = %q, want proposal summary", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("rows = %#v, want button rows", sender.inline[0].rows)
	}
	choiceRow := sender.inline[0].rows[len(sender.inline[0].rows)-1]
	if len(choiceRow) != 2 {
		t.Fatalf("choice row = %#v, want exactly 2 buttons", choiceRow)
	}
	if choiceRow[0].Text != "Deny" || choiceRow[1].Text != "Approve" {
		t.Fatalf("choice order = %#v, want [Deny, Approve]", choiceRow)
	}
}

func TestTelegramExecApprovalConfirmationExpandShowsCommandAfterApproval(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, nil)
	approver := newTelegramExecApprover(sender, broker)
	approver.timeout = time.Second

	resultCh := make(chan toolpkg.ExecApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
			Principal:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 42},
			SessionKey: session.SessionKey{ChatID: 7},
			Command:    "rm -rf /tmp/aphelion-runtime-bin",
			Reason:     "recursive delete",
			Proposal: session.OperationProposal{
				Kind:          "destructive_mutation",
				Summary:       "Perform a destructive change",
				WhyNow:        "The requested command deletes existing local state.",
				BoundedEffect: "Remove the targeted files and continue the operation.",
				Status:        session.ProposalStatusPending,
			},
		})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- decisionResult
	}()

	prompt := waitForDecisionInline(t, sender)
	approveData := callbackDataForButton(t, prompt.rows, "Approve")
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-approve",
		Data: approveData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 1,
			Chat:      &telegram.Chat{ID: 7},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(approve) err = %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("ConfirmExec() err = %v", err)
	case decisionResult := <-resultCh:
		if !decisionResult.Approved {
			t.Fatal("Approved = false, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("ConfirmExec() did not resolve after approve callback")
	}

	approvalEdit := waitForDecisionEdit(t, sender, 1)
	expandData := callbackDataForButton(t, approvalEdit.rows, "Expand details")
	if expandData == "" {
		t.Fatalf("approval rows = %#v, want expand details callback", approvalEdit.rows)
	}
	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-expand-approved",
		Data: expandData,
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: approvalEdit.messageID,
			Chat:      &telegram.Chat{ID: approvalEdit.chatID},
		},
	}); err != nil {
		t.Fatalf("HandleCallbackQuery(expand after approval) err = %v", err)
	}

	expanded := waitForDecisionEdit(t, sender, 2)
	if !strings.Contains(expanded.text, "Command:") || !strings.Contains(expanded.text, "rm -rf /tmp/aphelion-runtime-bin") {
		t.Fatalf("expanded text = %q, want full approved command", expanded.text)
	}
}

func TestTelegramExecApproverTimesOutToDeny(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	approver := newTelegramExecApprover(sender, broker)
	approver.timeout = 10 * time.Millisecond

	decisionResult, err := approver.ConfirmExec(context.Background(), toolpkg.ExecApprovalRequest{
		Principal:  principal.Principal{Role: principal.RoleAdmin},
		SessionKey: session.SessionKey{ChatID: 7},
		Command:    "pip install playwright",
		Reason:     "dependency installation",
		Proposal: session.OperationProposal{
			Kind:          "capability_acquisition",
			Summary:       "Acquire browser automation",
			WhyNow:        "A screenshot requires browser automation in this operation.",
			BoundedEffect: "Install Playwright locally and capture one screenshot.",
			Status:        session.ProposalStatusPending,
		},
	})
	if err != nil {
		t.Fatalf("ConfirmExec() err = %v", err)
	}
	if decisionResult.Approved {
		t.Fatal("Approved = true, want false")
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits = %#v, want one blocked edit", sender.edits)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one proposal prompt", sender.inline)
	}
	if !strings.Contains(sender.inline[0].text, "Acquire browser automation") {
		t.Fatalf("inline text = %q, want capability proposal summary", sender.inline[0].text)
	}
}

func waitForDecisionInline(t *testing.T, sender *decisionTestSender) decisionInlineCall {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(sender.inline) > 0 {
			return sender.inline[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("inline = %#v, want at least one prompt", sender.inline)
	return decisionInlineCall{}
}

func waitForDecisionEdit(t *testing.T, sender *decisionTestSender, count int) decisionEditCall {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(sender.edits) >= count {
			return sender.edits[count-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("edits = %#v, want at least %d edits", sender.edits, count)
	return decisionEditCall{}
}

func callbackDataForButton(t *testing.T, rows [][]telegram.InlineButton, label string) string {
	t.Helper()
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label {
				return button.CallbackData
			}
		}
	}
	t.Fatalf("rows = %#v, want button %q", rows, label)
	return ""
}

func hasInlineButton(rows [][]telegram.InlineButton, label string) bool {
	for _, row := range rows {
		for _, button := range row {
			if button.Text == label {
				return true
			}
		}
	}
	return false
}

func TestTelegramDurableMemoryDelegationApproverPromptsWithButtons(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := renderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		go broker.Resolve(pending.ID, "approve")
		return decision.Delivery{MessageID: msgID}, nil
	})
	approver := newTelegramDurableMemoryDelegationApprover(sender, broker)

	decisionResult, err := approver.ConfirmDurableMemoryDelegation(context.Background(), toolpkg.DurableMemoryDelegationApprovalRequest{
		Principal:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		SessionKey: session.SessionKey{ChatID: 7},
		Agent: core.DurableAgent{
			AgentID:     "child-alpha",
			ChannelKind: "external_channel",
			LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
				Charter: "Review an external child channel and surface important threads.",
			}),
		},
		Reason: "Seed child memory with stable channel preferences.",
		Entries: []toolpkg.DurableMemoryDelegationEntry{
			{
				SourceStore: "knowledge",
				CandidateID: "knowledge:1",
				TargetStore: "knowledge",
				Content:     "Keep channel summaries concise and pragmatic.",
			},
		},
	})
	if err != nil {
		t.Fatalf("ConfirmDurableMemoryDelegation() err = %v", err)
	}
	if !decisionResult.Approved {
		t.Fatal("Approved = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one memory delegation prompt", sender.inline)
	}
	if !strings.Contains(strings.ToLower(sender.inline[0].text), "memory delegation") {
		t.Fatalf("inline text = %q, want memory delegation wording", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "child-alpha") {
		t.Fatalf("inline text = %q, want agent id", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 {
		t.Fatalf("rows = %#v, want button rows", sender.inline[0].rows)
	}
	choiceRow := sender.inline[0].rows[len(sender.inline[0].rows)-1]
	if len(choiceRow) != 2 {
		t.Fatalf("choice row = %#v, want two buttons", choiceRow)
	}
	if choiceRow[0].Text != "Deny" || choiceRow[1].Text != "Approve" {
		t.Fatalf("choice order = %#v, want [Deny, Approve]", choiceRow)
	}
	if len(sender.deletes) != 0 {
		t.Fatalf("deletes = %#v, want no prompt delete on approval", sender.deletes)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits = %#v, want durable approval confirmation", sender.edits)
	}
	if !strings.Contains(sender.edits[0].text, "Memory delegation approved.") || !strings.Contains(sender.edits[0].text, "Decision:") || !strings.Contains(sender.edits[0].text, "child-alpha") {
		t.Fatalf("approval edit = %q, want memory delegation confirmation with decision id and agent", sender.edits[0].text)
	}
}

func TestTelegramDurableSnapshotRestoreApproverPromptsWithButtons(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(ctx context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		text := renderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		go broker.Resolve(pending.ID, "approve")
		return decision.Delivery{MessageID: msgID}, nil
	})
	approver := newTelegramDurableSnapshotRestoreApprover(sender, broker)

	decisionResult, err := approver.ConfirmDurableSnapshotRestore(context.Background(), toolpkg.DurableSnapshotRestoreApprovalRequest{
		Principal:  principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001},
		SessionKey: session.SessionKey{ChatID: 7},
		Agent: core.DurableAgent{
			AgentID:     "idolum-child",
			ChannelKind: "telegram_group",
		},
		SnapshotID:        "20260421T120000.000000000Z-k3f3f",
		SnapshotReason:    "Rollback after a bad child-local drift.",
		SnapshotCreatedAt: time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ConfirmDurableSnapshotRestore() err = %v", err)
	}
	if !decisionResult.Approved {
		t.Fatal("Approved = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one snapshot restore prompt", sender.inline)
	}
	if !strings.Contains(strings.ToLower(sender.inline[0].text), "snapshot") {
		t.Fatalf("inline text = %q, want snapshot wording", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "idolum-child") {
		t.Fatalf("inline text = %q, want agent id", sender.inline[0].text)
	}
	choiceRow := sender.inline[0].rows[len(sender.inline[0].rows)-1]
	if len(choiceRow) != 2 {
		t.Fatalf("choice row = %#v, want two buttons", choiceRow)
	}
	if choiceRow[0].Text != "Deny" || choiceRow[1].Text != "Approve" {
		t.Fatalf("choice order = %#v, want [Deny, Approve]", choiceRow)
	}
	if len(sender.deletes) != 0 {
		t.Fatalf("deletes = %#v, want no prompt delete on approval", sender.deletes)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits = %#v, want durable approval confirmation", sender.edits)
	}
	if !strings.Contains(sender.edits[0].text, "Snapshot restore approved.") || !strings.Contains(sender.edits[0].text, "Decision:") || !strings.Contains(sender.edits[0].text, "idolum-child") {
		t.Fatalf("approval edit = %q, want snapshot confirmation with decision id and agent", sender.edits[0].text)
	}
}

func TestHandleCallbackQueryIgnoresExpiredAckAndResolvesDecision(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{
		answerErr: errors.New("telegram answerCallbackQuery failed: Bad Request: query is too old and response timeout expired or query ID is invalid"),
	}
	pendingSeen := make(chan decision.PendingDecision, 1)
	var broker *decision.Broker
	resolved := make(chan string, 1)
	broker = decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		pendingSeen <- pending
		return decision.Delivery{MessageID: 91}, nil
	})
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, nil)

	go func() {
		result, err := broker.Request(context.Background(), decision.Request{
			Kind:          decision.KindInterrupt,
			ChatID:        7,
			SenderID:      42,
			Prompt:        "Still working",
			Choices:       []decision.Choice{{ID: "stop", Label: "Stop"}, {ID: "queue", Label: "Queue"}},
			DefaultChoice: "queue",
			Timeout:       time.Second,
		})
		if err == nil {
			resolved <- result.Choice
		}
	}()

	var pending decision.PendingDecision
	select {
	case pending = <-pendingSeen:
	case <-time.After(time.Second):
		t.Fatal("broker did not publish a pending decision")
	}
	cb := telegram.CallbackQuery{
		ID:   "cb-1",
		Data: decision.EncodeCallbackData(pending.ID, pending.Choices[0].ID),
	}
	if err := handler.HandleCallbackQuery(context.Background(), cb); err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v, want nil for stale callback ack", err)
	}

	select {
	case choice := <-resolved:
		if choice != "stop" {
			t.Fatalf("choice = %q, want stop", choice)
		}
	case <-time.After(time.Second):
		t.Fatal("decision was not resolved after stale callback ack")
	}
}

func TestHandleCallbackQueryReturnsNonStaleAckError(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{
		answerErr: errors.New("telegram answerCallbackQuery failed: Bad Request: chat not found"),
	}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		return decision.Delivery{MessageID: 1}, nil
	}), nil)

	err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-1",
		Data: decision.EncodeCallbackData("1", "approve"),
	})
	if err == nil {
		t.Fatal("HandleCallbackQuery() err = nil, want non-stale ack error")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("HandleCallbackQuery() err = %v, want original ack error", err)
	}
}

func TestHandleCallbackQueryReturnsStaleMessageForMissingDecision(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), nil)

	if err := handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:   "cb-stale",
		Data: decision.EncodeCallbackData("missing-decision", "approve"),
	}); err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v, want nil", err)
	}
	if len(sender.answers) != 1 {
		t.Fatalf("answers = %#v, want one callback answer", sender.answers)
	}
	if !strings.Contains(sender.answers[0].text, "no longer active") {
		t.Fatalf("answer text = %q, want stale-decision hint", sender.answers[0].text)
	}
}

func TestHandleArtifactRetentionMessagePromptsAndRoutesChosenPolicy(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	go func() {
		for i := 0; i < 100; i++ {
			if len(sender.inline) > 0 && len(sender.inline[0].rows) > 0 {
				for _, row := range sender.inline[0].rows {
					for _, button := range row {
						id, choice, ok := decision.DecodeCallbackData(button.CallbackData)
						if ok && choice == "local" {
							broker.Resolve(id, choice)
							return
						}
					}
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	router := &decisionTestRouter{}
	handler := newTelegramDecisionHandler(sender, router, broker, nil)

	msg := core.InboundMessage{
		ChatID:    7,
		SenderID:  42,
		MessageID: 99,
		Artifacts: []core.Artifact{{
			ID:         "doc-1",
			Channel:    "telegram",
			RemoteID:   "file-1",
			Kind:       "document",
			SourceType: "document",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one selector prompt", sender.inline)
	}
	if !strings.Contains(sender.inline[0].text, "How should I retain this inbound file?") {
		t.Fatalf("inline text = %q, want retention prompt", sender.inline[0].text)
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed message", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "local" {
		t.Fatalf("retention choice = %q, want local", got)
	}
	if got := artifact.Metadata["aphelion_materialize"]; got != "local" {
		t.Fatalf("materialize = %q, want local", got)
	}
	if got := artifact.DefaultRetention; got != "child_local" {
		t.Fatalf("DefaultRetention = %q, want child_local", got)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "save the file locally") {
		t.Fatalf("edits = %#v, want local-save confirmation", sender.edits)
	}
}

func TestHandleArtifactRetentionMessageTimeoutDefaultsToSession(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		return decision.Delivery{MessageID: 52}, nil
	})
	router := &decisionTestRouter{}
	handler := newTelegramDecisionHandler(sender, router, broker, nil)
	handler.artifactRetentionTimeout = 10 * time.Millisecond

	msg := core.InboundMessage{
		ChatID:    8,
		SenderID:  42,
		MessageID: 100,
		Artifacts: []core.Artifact{{
			ID:         "doc-2",
			Channel:    "telegram",
			RemoteID:   "file-2",
			Kind:       "document",
			SourceType: "document",
			Filename:   "notes.txt",
			MimeType:   "text/plain",
		}},
	}

	handled, err := handler.HandleArtifactRetentionMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleArtifactRetentionMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.routed) != 1 {
		t.Fatalf("routed = %#v, want one routed message", router.routed)
	}
	artifact := router.routed[0].Artifacts[0]
	if got := artifact.Metadata["aphelion_retention_choice"]; got != "session" {
		t.Fatalf("retention choice = %q, want session", got)
	}
	if got := artifact.DefaultRetention; got != "session_reference" {
		t.Fatalf("DefaultRetention = %q, want session_reference", got)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "session by default") {
		t.Fatalf("edits = %#v, want session-timeout confirmation", sender.edits)
	}
}

func TestTelegramPollerArtifactRetentionCallbackStarvesBehindBlockingMessageHandler(t *testing.T) {
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
		text := renderPendingDecisionSummary(pending)
		msgID, err := sender.SendInlineKeyboard(ctx, pending.ChatID, text, inlineButtonRows(pending), replyToMessageID(pending.MessageID))
		if err != nil {
			return decision.Delivery{}, err
		}
		select {
		case callbackDataReady <- decision.EncodeCallbackData(pending.ID, "local"):
		default:
		}
		return decision.Delivery{MessageID: msgID}, nil
	})
	handler := newTelegramDecisionHandler(sender, router, broker, store)
	handler.artifactRetentionTimeout = 25 * time.Millisecond

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
							"file_name":      "notes.txt",
							"mime_type":      "text/plain",
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

	rows := inlineButtonRows(decision.PendingDecision{
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

	rows := inlineButtonRows(decision.PendingDecision{
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
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker, nil)
	execApprover := newTelegramExecApprover(sender, broker)
	memoryApprover := newTelegramDurableMemoryDelegationApprover(sender, broker)
	snapshotApprover := newTelegramDurableSnapshotRestoreApprover(sender, broker)

	want := 30 * time.Minute
	if defaultUserApprovalTimeout != want {
		t.Fatalf("defaultUserApprovalTimeout = %s, want %s", defaultUserApprovalTimeout, want)
	}
	if execApprover.timeout != want {
		t.Fatalf("exec approval timeout = %s, want %s", execApprover.timeout, want)
	}
	if handler.artifactRetentionTimeout != want {
		t.Fatalf("artifact retention timeout = %s, want %s", handler.artifactRetentionTimeout, want)
	}
	if memoryApprover.timeout != want {
		t.Fatalf("memory delegation timeout = %s, want %s", memoryApprover.timeout, want)
	}
	if snapshotApprover.timeout != want {
		t.Fatalf("snapshot restore timeout = %s, want %s", snapshotApprover.timeout, want)
	}
}

func TestHandleReviewEventCallbackApprovesCapabilityRequest(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	if _, err := store.UpsertCapabilityRequest(session.CapabilityRequest{
		RequestID:      "cap-button-approve",
		RequestedBy:    "telegram:1002",
		RequestedFor:   "telegram:1002",
		AdminPrincipal: "telegram:1001",
		Kind:           session.CapabilityKindGenericDelegation,
		TargetResource: "local-branch",
		Purpose:        "approve from callback",
		ReviewStatus:   session.CapabilityReviewStatusProposed,
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      7001,
		SourceUserID:      1002,
		SourceRole:        "capability_request",
		TargetAdminChatID: 1001,
		Summary:           "Capability request cap-button-approve",
		MetadataJSON:      `{"request_id":"cap-button-approve","review_status":"proposed"}`,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	sender := &decisionTestSender{}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-review-1",
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 1001}},
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionApprove),
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	updated, ok, err := store.CapabilityRequest("cap-button-approve")
	if err != nil || !ok {
		t.Fatalf("CapabilityRequest() ok=%t err=%v", ok, err)
	}
	if updated.ReviewStatus != session.CapabilityReviewStatusApproved {
		t.Fatalf("ReviewStatus = %q, want approved", updated.ReviewStatus)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "Capability request approved.") || !strings.Contains(sender.edits[0].text, "Request: cap-button-approve") || !strings.Contains(sender.edits[0].text, "Review event:") || !strings.Contains(sender.edits[0].text, "Capability request cap-button-approve") {
		t.Fatalf("edits = %#v, want durable approved review-event copy", sender.edits)
	}
	if len(sender.answers) != 1 || sender.answers[0].text != "" {
		t.Fatalf("answers = %#v, want empty ack", sender.answers)
	}
}

func TestReviewEventCallbackTimeoutIsThirtyMinutes(t *testing.T) {
	t.Parallel()

	event := session.ReviewEvent{CreatedAt: time.Now().Add(-29 * time.Minute)}
	if reviewEventCallbackExpired(event, time.Now()) {
		t.Fatal("reviewEventCallbackExpired() = true before 30 minutes")
	}
	event.CreatedAt = time.Now().Add(-31 * time.Minute)
	if !reviewEventCallbackExpired(event, time.Now()) {
		t.Fatal("reviewEventCallbackExpired() = false after 30 minutes")
	}
}
