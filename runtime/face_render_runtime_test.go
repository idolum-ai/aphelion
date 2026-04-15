//go:build linux

package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundRendersViaFaceByDefault(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     901,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
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
	if finalText != "idolum rendered" {
		t.Fatalf("outbound text = %q, want idolum rendered", finalText)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 901, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) < 2 {
		t.Fatalf("session messages len = %d, want >= 2", len(sess.Messages))
	}
	if sess.Messages[1].Content != "idolum rendered" {
		t.Fatalf("session assistant text = %q, want rendered reply", sess.Messages[1].Content)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
}

func TestHandleInboundFaceFailureFallsBackToFloorFallback(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceErr = errors.New("face unavailable")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     902,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "governor canonical" {
		t.Fatalf("outbound text = %q, want canonical fallback", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 902, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != "governor canonical" {
		t.Fatalf("visible transcript assistant content = %q, want canonical fallback", sess.Messages[1].Content)
	}
}

func TestHandleInboundFloorFallbackBackendSkipsFaceRender(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "governor canonical"
	provider.faceReplyText = "idolum rendered"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     903,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "hello",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "governor canonical" {
		t.Fatalf("outbound text = %q, want canonical passthrough", sender.sent[0].Text)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 903, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.LastFloorText != "governor canonical" {
		t.Fatalf("session floor sidecar = %q, want canonical", sess.LastFloorText)
	}
	if len(sess.Messages) < 2 || sess.Messages[1].Content != "governor canonical" {
		t.Fatalf("visible transcript assistant content = %q, want canonical passthrough", sess.Messages[1].Content)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenFaceSystem) != 0 {
		t.Fatalf("face should not be called in passthrough mode; calls=%d", len(provider.seenFaceSystem))
	}
}
