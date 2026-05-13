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
		From: &telegram.User{ID: 42},
		Message: &telegram.Message{
			MessageID: 91,
			Chat:      &telegram.Chat{ID: 7},
		},
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
