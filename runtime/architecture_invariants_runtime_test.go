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

func TestInvariantFloorSceneSplitPersistsSceneAndFloorSeparately(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.TrimSpace(`FACTS:
- governor fact

ALLOWED_ACTIONS:
- summarize the findings`)
	provider.faceReplyText = "Here is the summary you asked for."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     4201,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "summarize what matters",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 4201, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	assistant := sess.Messages[1]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q, want assistant", assistant.Role)
	}
	if assistant.Content != "Here is the summary you asked for." {
		t.Fatalf("assistant content = %q, want rendered scene text", assistant.Content)
	}
	if !strings.Contains(assistant.FloorContent, "FACTS:") || !strings.Contains(assistant.FloorContent, "governor fact") {
		t.Fatalf("assistant floor content = %q, want structured floor material", assistant.FloorContent)
	}
	if assistant.Content == assistant.FloorContent {
		t.Fatalf("assistant content and floor should differ, both are %q", assistant.Content)
	}
}

func TestInvariantPersistBeforeDeliverWhenSendFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = strings.TrimSpace(`FACTS:
- governor fact on failed send`)
	provider.faceReplyText = "Visible scene should still persist."
	sender.sendErr = errors.New("send failed")

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     4202,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "trigger send failure",
		MessageID:  2,
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want send failure")
	}
	if !strings.Contains(err.Error(), "send outbound reply") {
		t.Fatalf("HandleInbound() err = %v, want send outbound reply error", err)
	}

	sess, err := store.Load(session.SessionKey{ChatID: 4202, UserID: 0})
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if sess.TurnCount != 1 {
		t.Fatalf("turn count = %d, want 1 persisted turn", sess.TurnCount)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("session messages = %d, want 2", len(sess.Messages))
	}
	assistant := sess.Messages[1]
	if assistant.Content != "Visible scene should still persist." {
		t.Fatalf("assistant content = %q, want persisted rendered scene", assistant.Content)
	}
	if !strings.Contains(assistant.FloorContent, "governor fact on failed send") {
		t.Fatalf("assistant floor content = %q, want persisted floor sidecar", assistant.FloorContent)
	}
	outboundIDs, err := store.OutboundAfterTurn(session.SessionKey{ChatID: 4202, UserID: 0}, 0)
	if err != nil {
		t.Fatalf("OutboundAfterTurn() err = %v", err)
	}
	if len(outboundIDs) != 0 {
		t.Fatalf("outbound ids = %#v, want empty on failed delivery", outboundIDs)
	}
}
