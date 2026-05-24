//go:build linux

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

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

func TestHandleBusyTelegramMessageQueueAckPreservesThreadDisplaySlot(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Now().UTC()
	first, _, err := store.CreateTelegramThreadForUpdate(7, 42, 1001, 2001, "old thread", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(first) err = %v", err)
	}
	if _, closed, err := store.CloseTelegramThread(7, first.ThreadID, "closed", now); err != nil || !closed {
		t.Fatalf("CloseTelegramThread() closed=%t err=%v", closed, err)
	}
	thread, _, err := store.CreateTelegramThreadForUpdate(7, 42, 1002, 2002, "visible slot reused", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(second) err = %v", err)
	}
	if thread.ThreadID == thread.DisplaySlot || thread.DisplaySlot != 1 {
		t.Fatalf("thread = %#v, want canonical id distinct from visible slot 1", thread)
	}

	sender := &decisionTestSender{}
	var broker *decision.Broker
	broker = decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		go broker.Resolve(pending.ID, "queue")
		return decision.Delivery{MessageID: 41}, nil
	})
	router := &decisionAcceptedTestRouter{decisionTestRouter: &decisionTestRouter{status: core.SessionStatus{Active: true}}}
	handler := newTelegramDecisionHandler(sender, router, broker, store)
	handler.interruptTimeout = time.Minute

	msg := core.InboundMessage{ChatID: 7, SenderID: 42, MessageID: 99, TelegramThreadID: thread.ThreadID, Text: "next task"}
	handled, err := handler.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	edit := waitForDecisionEdit(t, sender, 1)
	if !strings.HasPrefix(edit.text, "(thread 1)\n\n") {
		t.Fatalf("edit text = %q, want visible thread display-slot prefix", edit.text)
	}
	if strings.Contains(edit.text, "(thread 2)") {
		t.Fatalf("edit text = %q, leaked canonical thread id instead of visible slot", edit.text)
	}
	if !strings.Contains(edit.text, "Got it — I'll process your message next. ⏳") {
		t.Fatalf("edit text = %q, want queue acknowledgement", edit.text)
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

func TestHandleBusyTelegramMessageScopesPendingDecisionToThread(t *testing.T) {
	t.Parallel()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	seen := make(chan decision.PendingDecision, 1)
	broker := decision.NewBroker(func(_ context.Context, pending decision.PendingDecision) (decision.Delivery, error) {
		seen <- pending
		return decision.Delivery{MessageID: 41}, nil
	})
	router := &decisionTestRouter{status: core.SessionStatus{Active: true}}
	handler := newTelegramDecisionHandler(&decisionTestSender{}, router, broker, store)
	handler.interruptTimeout = time.Minute

	msg := core.InboundMessage{ChatID: 7, SenderID: 42, MessageID: 99, TelegramThreadID: 3, Text: "next task"}
	handled, err := handler.HandleBusyMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("HandleBusyMessage() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}

	ownerKey := "session:telegram_thread:7:3:sender:42"
	record, err := store.PendingBusyDecision(ownerKey)
	if err != nil {
		t.Fatalf("PendingBusyDecision(%q) err = %v", ownerKey, err)
	}
	if record.SessionID != "telegram_thread:7:3" || record.ScopeKind != string(session.ScopeKindTelegramThread) || record.ScopeID != "7:3" || record.MessageID != 99 {
		t.Fatalf("record = %#v, want scoped thread pending row", record)
	}
	select {
	case pending := <-seen:
		if pending.OwnerKey != ownerKey || pending.SessionID != "telegram_thread:7:3" || pending.ScopeKind != string(session.ScopeKindTelegramThread) {
			t.Fatalf("pending request = %#v, want scoped thread owner", pending.Request)
		}
		broker.Resolve(pending.ID, "queue")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broker pending decision")
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
