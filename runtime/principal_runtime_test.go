//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestAgentFuncDelegates(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	fn := rt.AgentFunc()
	_, err = fn(context.Background(), nil, core.InboundMessage{
		ChatID:     8,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "hello",
		MessageID:  1,
		Raw:        json.RawMessage(`{"source":"test"}`),
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("AgentFunc() err = %v", err)
	}
}

func TestHandleInboundRejectsUnknownPrincipal(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     123,
		SenderID:   999999,
		SenderName: "intruder",
		Text:       "hello",
		MessageID:  1,
	})
	if err == nil {
		t.Fatal("HandleInbound() err = nil, want principal denied error")
	}
	if !strings.Contains(err.Error(), ErrPrincipalDenied.Error()) {
		t.Fatalf("error = %v, want %q", err, ErrPrincipalDenied)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.callCount != 0 {
		t.Fatalf("provider call count = %d, want 0", provider.callCount)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent len = %d, want 0", len(sender.sent))
	}
}
