//go:build linux

package main

import (
	"context"
	"errors"
	"strings"
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
	inline  []decisionInlineCall
	edits   []decisionEditCall
	deletes []decisionDeleteCall
	answers []decisionAnswerCall
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
}

type decisionDeleteCall struct {
	chatID    int64
	messageID int64
}

type decisionAnswerCall struct {
	id   string
	text string
}

func (s *decisionTestSender) SendInlineKeyboard(_ context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error) {
	s.inline = append(s.inline, decisionInlineCall{chatID: chatID, text: text, rows: rows, replyTo: replyTo})
	return int64(len(s.inline)), nil
}

func (s *decisionTestSender) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, _ string) error {
	s.edits = append(s.edits, decisionEditCall{chatID: chatID, messageID: messageID, text: text})
	return nil
}

func (s *decisionTestSender) DeleteMessage(_ context.Context, chatID int64, messageID int64) error {
	s.deletes = append(s.deletes, decisionDeleteCall{chatID: chatID, messageID: messageID})
	return nil
}

func (s *decisionTestSender) AnswerCallbackQuery(_ context.Context, id string, text string) error {
	s.answers = append(s.answers, decisionAnswerCall{id: id, text: text})
	return s.answerErr
}

type decisionTestRouter struct {
	status    core.SessionStatus
	stopCalls []int64
	routed    []core.InboundMessage
}

func (r *decisionTestRouter) Status(chatID int64) core.SessionStatus {
	return r.status
}

func (r *decisionTestRouter) Stop(chatID int64) core.StopResult {
	r.stopCalls = append(r.stopCalls, chatID)
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
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{status: core.SessionStatus{Active: true}}, broker)
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
	handler := newTelegramDecisionHandler(sender, router, broker)

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
	if len(router.stopCalls) != 1 {
		t.Fatalf("stopCalls = %#v, want one stop", router.stopCalls)
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
	handler := newTelegramDecisionHandler(sender, router, broker)

	msg := core.InboundMessage{ChatID: 7, MessageID: 15, Text: "wait, do X instead"}
	handled, err := handler.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if len(router.stopCalls) != 1 {
		t.Fatalf("stopCalls = %#v, want one stop", router.stopCalls)
	}
	if len(router.routed) != 1 || router.routed[0].Text != msg.Text {
		t.Fatalf("routed = %#v, want original follow-up message", router.routed)
	}
}

func TestTelegramExecApproverDeletesPromptOnApprove(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	broker := newTelegramDecisionBroker(sender)
	go func() {
		time.Sleep(5 * time.Millisecond)
		if len(sender.inline) == 0 || len(sender.inline[0].rows) == 0 || len(sender.inline[0].rows[0]) == 0 {
			return
		}
		data := sender.inline[0].rows[0][0].CallbackData
		id, choice, ok := decision.DecodeCallbackData(data)
		if ok {
			broker.Resolve(id, choice)
		}
	}()
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
	if len(sender.deletes) != 1 {
		t.Fatalf("deletes = %#v, want one prompt delete", sender.deletes)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline = %#v, want one proposal prompt", sender.inline)
	}
	if !strings.Contains(sender.inline[0].text, "Perform a destructive change") {
		t.Fatalf("inline text = %q, want proposal summary", sender.inline[0].text)
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
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, broker)

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
	}))

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
