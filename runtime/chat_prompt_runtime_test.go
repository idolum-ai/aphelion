//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestStartChatActionLoopSendsTyping(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{actionCh: make(chan chatAction, 1)}
	rt := &Runtime{outbound: sender}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := rt.startChatActionLoop(ctx, 42, "typing")
	defer stop()

	select {
	case got := <-sender.actionCh:
		if got.ChatID != 42 {
			t.Fatalf("chat id = %d, want 42", got.ChatID)
		}
		if got.Action != "typing" {
			t.Fatalf("action = %q, want typing", got.Action)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected typing action to be sent")
	}
}

func TestHandleInboundReloadsPromptContextEachTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	heartbeatPath := filepath.Join(cfg.Agent.ExecRoot, "HEARTBEAT.md")
	if err := os.WriteFile(heartbeatPath, []byte("v1"), 0o600); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "first",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("first HandleInbound() err = %v", err)
	}

	if err := os.WriteFile(heartbeatPath, []byte("v2"), 0o600); err != nil {
		t.Fatalf("rewrite heartbeat: %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     7,
		SenderID:   1001,
		SenderName: "daniel",
		Text:       "second",
		MessageID:  2,
	})
	if err != nil {
		t.Fatalf("second HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenGovernorSystem) < 2 {
		t.Fatalf("seen governor system len = %d, want >=2", len(provider.seenGovernorSystem))
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "v1") {
		t.Fatalf("first governor prompt missing v1: %q", provider.seenGovernorSystem[0])
	}
	if !strings.Contains(provider.seenGovernorSystem[1], "v2") {
		t.Fatalf("second governor prompt missing v2: %q", provider.seenGovernorSystem[1])
	}
	if !strings.Contains(provider.seenGovernorSystem[0], "principal_role: admin") {
		t.Fatalf("first governor prompt missing principal role: %q", provider.seenGovernorSystem[0])
	}
}
