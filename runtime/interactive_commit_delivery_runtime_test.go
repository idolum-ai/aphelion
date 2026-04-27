//go:build linux

package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundPersistsAndSends(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.thinkingText = "Reasoning summary"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     42,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	finalText := sender.sent[0].Text
	if len(sender.edits) > 0 {
		finalText = sender.edits[len(sender.edits)-1].Text
	}
	if finalText != "ok" {
		t.Fatalf("final text = %q, want ok", finalText)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 42, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 1 {
		t.Fatalf("turn count = %d, want 1", sess.TurnCount)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	if sess.Messages[0].Role != "user" || sess.Messages[1].Role != "assistant" {
		t.Fatalf("roles = %#v %#v", sess.Messages[0], sess.Messages[1])
	}
	if sess.Messages[1].FloorContent != "ok" {
		t.Fatalf("assistant floor = %q, want ok", sess.Messages[1].FloorContent)
	}
	if sess.Messages[1].Thinking != "Reasoning summary" {
		t.Fatalf("assistant thinking = %q, want reasoning summary", sess.Messages[1].Thinking)
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 42, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 1 || outboundIDs[0] != 1 {
		t.Fatalf("outbound ids = %#v, want [1]", outboundIDs)
	}
}

func TestHandleInboundPersistsWhenSendFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	sender.sendErr = errors.New("send failed")
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     44,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  77,
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want send failure")
	}
	if !strings.Contains(err.Error(), "send outbound reply") {
		t.Fatalf("HandleInbound() err = %v, want send outbound reply error", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 44, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 1 {
		t.Fatalf("turn count = %d, want 1", sess.TurnCount)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	if sess.Messages[1].Role != "assistant" || strings.TrimSpace(sess.Messages[1].Content) == "" {
		t.Fatalf("assistant message = %#v, want non-empty assistant message", sess.Messages[1])
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 44, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 0 {
		t.Fatalf("outbound ids = %#v, want empty on send failure", outboundIDs)
	}
}

func TestHandleInboundStreamingFinalizeFailureDoesNotPersistTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	sender.editErr = errors.New("stream finalize failed")
	sender.sendErr = errors.New("stream finalize fallback failed")
	sender.sendErrAfter = 1
	provider.streamFaceText = "streamed idolum reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     45,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  88,
	})
	if err == nil {
		sender.mu.Lock()
		nSent := len(sender.sent)
		nEditAttempts := sender.editCount
		sentTexts := make([]string, len(sender.sent))
		for i, msg := range sender.sent {
			sentTexts[i] = msg.Text
		}
		sender.mu.Unlock()
		t.Fatalf("HandleInbound() err = nil, want stream finalize failure (sent=%d editAttempts=%d texts=%v)", nSent, nEditAttempts, sentTexts)
	}
	if !strings.Contains(err.Error(), "finish streamed reply") {
		t.Fatalf("HandleInbound() err = %v, want finish streamed reply error", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 45, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 0 {
		t.Fatalf("turn count = %d, want 0", sess.TurnCount)
	}
	if len(sess.Messages) != 0 {
		t.Fatalf("session messages = %d, want 0", len(sess.Messages))
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 45, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 0 {
		t.Fatalf("outbound ids = %#v, want empty", outboundIDs)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.editCount == 0 {
		t.Fatal("expected attempted streamed edit before finalize failure")
	}
	if sender.sendErr == nil {
		t.Fatal("expected sender send error to be configured for finalize fallback path")
	}
	if sender.sendCount < 2 {
		t.Fatalf("sendCount = %d, want at least 2", sender.sendCount)
	}
	if len(sender.sent) == 0 {
		t.Fatal("expected streamed send before finalize failure")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent count = %d, want 1", len(sender.sent))
	}
	if len(sender.edits) > 0 && sender.edits[0].ChatID != 45 {
		t.Fatalf("edit chat id = %d, want 45", sender.edits[0].ChatID)
	}
}

func TestHandleInboundStreamsFaceReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.streamFaceText = "streamed idolum reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     52,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) == 0 {
		t.Fatal("expected at least one streamed send")
	}
	if sender.sent[len(sender.sent)-1].Text == "ok" {
		t.Fatalf("final streamed send = %q, want streamed reply path", sender.sent[len(sender.sent)-1].Text)
	}
	finalEdit := ""
	if len(sender.edits) > 0 {
		finalEdit = sender.edits[len(sender.edits)-1].Text
	}
	if len(sender.editInline) > 0 {
		finalEdit = sender.editInline[len(sender.editInline)-1].Text
	}
	if len(sender.editClear) > 0 {
		finalEdit = sender.editClear[len(sender.editClear)-1].Text
	}
	if finalEdit == "" {
		t.Fatal("expected editMessageText calls during streaming")
	}
	if finalEdit != "streamed idolum reply" {
		t.Fatalf("final edited text = %q, want streamed idolum reply", finalEdit)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 52, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if got := sess.Messages[len(sess.Messages)-1].Content; got != "streamed idolum reply" {
		t.Fatalf("stored rendered reply = %q, want streamed idolum reply", got)
	}
}
