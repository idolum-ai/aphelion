//go:build linux

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleTelegramIngressMessagePromptsMediaBeforeRetention(t *testing.T) {
	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: []session.TelegramThread{
		{ChatID: 44, ThreadID: 101, DisplaySlot: 1, Status: session.TelegramThreadStatusOpen},
	}}
	decisions := &stubIngressDecisions{retentionHandled: true}
	msg := core.InboundMessage{
		ChatID:    44,
		SenderID:  12,
		MessageID: 700,
		Artifacts: []core.Artifact{{
			ID:         "photo-1",
			Channel:    "telegram",
			RemoteID:   "photo-file",
			Kind:       "image",
			SourceType: "photo",
			Filename:   "photo.jpg",
		}},
	}

	if err := handleTelegramIngressMessage(context.Background(), sender, router, decisions, msg); err != nil {
		t.Fatalf("handleTelegramIngressMessage() err = %v", err)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline calls = %d, want 1 media picker prompt", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Which chat or thread") {
		t.Fatalf("picker text = %q, want media thread prompt", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) == 0 || len(sender.inline[0].rows[0]) == 0 || sender.inline[0].rows[0][0].Text != "Main chat" {
		t.Fatalf("picker rows = %#v, want explicit Main chat target first", sender.inline[0].rows)
	}
	if router.mediaPickerRecordChatID != msg.ChatID || router.mediaPickerRecordMessageID != 1 {
		t.Fatalf("recorded picker = %d/%d, want %d/1", router.mediaPickerRecordChatID, router.mediaPickerRecordMessageID, msg.ChatID)
	}
	if router.mediaPickerRecordInbound.MessageID != msg.MessageID || len(router.mediaPickerRecordInbound.Artifacts) != 1 {
		t.Fatalf("recorded inbound = %#v, want original media message", router.mediaPickerRecordInbound)
	}
	if decisions.busyCalls != 0 || decisions.retentionCalls != 0 {
		t.Fatalf("decision calls busy/retention = %d/%d, want media picker to short-circuit first", decisions.busyCalls, decisions.retentionCalls)
	}
	if router.routeAcceptedMsg != nil {
		t.Fatalf("routeAcceptedMsg = %#v, want no direct routing before picker choice", router.routeAcceptedMsg)
	}
}

func TestHandleTelegramIngressMessageKeepsExplicitThreadMediaInDecisionLane(t *testing.T) {
	sender := &stubCommandSender{}
	router := &stubCommandRouter{threadsReturn: []session.TelegramThread{
		{ChatID: 44, ThreadID: 2, DisplaySlot: 2, Status: session.TelegramThreadStatusOpen},
	}}
	decisions := &stubIngressDecisions{retentionHandled: true}
	msg := core.InboundMessage{
		ChatID:    44,
		SenderID:  12,
		MessageID: 701,
		Text:      "(thread 2) inspect this",
		Artifacts: []core.Artifact{{
			ID:         "doc-1",
			Channel:    "telegram",
			RemoteID:   "doc-file",
			Kind:       "document",
			SourceType: "document",
			Filename:   "notes.txt",
		}},
	}

	if err := handleTelegramIngressMessage(context.Background(), sender, router, decisions, msg); err != nil {
		t.Fatalf("handleTelegramIngressMessage() err = %v", err)
	}
	if len(sender.inline) != 0 {
		t.Fatalf("inline calls = %d, want no picker for explicitly targeted media", len(sender.inline))
	}
	if decisions.retentionCalls != 1 || decisions.retentionMsg == nil {
		t.Fatalf("retention calls/msg = %d/%#v, want retention decision for targeted media", decisions.retentionCalls, decisions.retentionMsg)
	}
	if decisions.retentionMsg.TelegramThreadID != 2 {
		t.Fatalf("retention TelegramThreadID = %d, want 2", decisions.retentionMsg.TelegramThreadID)
	}
	if decisions.retentionMsg.Text != "inspect this" {
		t.Fatalf("retention text = %q, want stripped thread prefix payload", decisions.retentionMsg.Text)
	}
	if router.routeAcceptedMsg != nil {
		t.Fatalf("routeAcceptedMsg = %#v, want retention to consume targeted media", router.routeAcceptedMsg)
	}
}

func TestHandleTelegramIngressMessageRoutesReactionToDecisionHandler(t *testing.T) {
	sender := &stubCommandSender{}
	router := &stubCommandRouter{}
	decisions := &stubIngressDecisions{reactionHandled: true}
	msg := core.InboundMessage{
		ChatID:    44,
		SenderID:  1001,
		MessageID: 901,
		Text:      "reaction_update message_id=901 new=👍",
		Reaction:  &core.InboundReaction{MessageID: 901, New: []string{"👍"}},
	}

	if err := handleTelegramIngressMessage(context.Background(), sender, router, decisions, msg); err != nil {
		t.Fatalf("handleTelegramIngressMessage() err = %v", err)
	}
	if decisions.reactionCalls != 1 || decisions.reactionMsg == nil {
		t.Fatalf("reaction calls/msg = %d/%#v, want decision reaction handler", decisions.reactionCalls, decisions.reactionMsg)
	}
	if decisions.busyCalls != 0 || decisions.retentionCalls != 0 {
		t.Fatalf("decision calls busy/retention = %d/%d, want reaction to short-circuit", decisions.busyCalls, decisions.retentionCalls)
	}
	if router.routeAcceptedMsg != nil {
		t.Fatalf("routeAcceptedMsg = %#v, want reaction not routed as prose", router.routeAcceptedMsg)
	}
}

type stubIngressDecisions struct {
	busyCalls        int
	busyHandled      bool
	busyErr          error
	reactionCalls    int
	reactionMsg      *core.InboundMessage
	reactionHandled  bool
	reactionErr      error
	retentionCalls   int
	retentionMsg     *core.InboundMessage
	retentionHandled bool
	retentionErr     error
}

func (d *stubIngressDecisions) HandleReactionMessage(_ context.Context, msg core.InboundMessage) (bool, error) {
	d.reactionCalls++
	copied := msg
	d.reactionMsg = &copied
	if d.reactionErr != nil {
		return false, d.reactionErr
	}
	return d.reactionHandled, nil
}

func (d *stubIngressDecisions) HandleBusyMessage(_ context.Context, _ core.InboundMessage) (bool, error) {
	d.busyCalls++
	if d.busyErr != nil {
		return false, d.busyErr
	}
	return d.busyHandled, nil
}

func (d *stubIngressDecisions) HandleArtifactRetentionMessage(_ context.Context, msg core.InboundMessage) (bool, error) {
	d.retentionCalls++
	copied := msg
	d.retentionMsg = &copied
	if d.retentionErr != nil {
		return false, d.retentionErr
	}
	return d.retentionHandled, nil
}
