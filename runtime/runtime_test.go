//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type fakeProvider struct {
	mu            sync.Mutex
	callCount     int
	replyText     string
	seenSystem    []string
	responseUsage core.TokenUsage
}

func (f *fakeProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++

	if len(messages) > 0 && messages[0].Role == "system" {
		f.seenSystem = append(f.seenSystem, messages[0].Content)
	} else {
		f.seenSystem = append(f.seenSystem, "")
	}

	return &agent.Response{
		Content: f.replyText,
		Usage:   f.responseUsage,
	}, nil
}

type fakeSender struct {
	mu   sync.Mutex
	sent []core.OutboundMessage
}

func (f *fakeSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return int64(len(f.sent)), nil
}

func TestHandleInboundPersistsAndSends(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
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
	if sender.sent[0].Text != "ok" {
		t.Fatalf("sent text = %q, want ok", sender.sent[0].Text)
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
}

func TestHandleInboundReloadsPromptContextEachTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	heartbeatPath := filepath.Join(cfg.Agent.Workspace, "HEARTBEAT.md")
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
	if len(provider.seenSystem) < 2 {
		t.Fatalf("seen system len = %d, want >=2", len(provider.seenSystem))
	}
	if !strings.Contains(provider.seenSystem[0], "v1") {
		t.Fatalf("first system prompt missing v1: %q", provider.seenSystem[0])
	}
	if !strings.Contains(provider.seenSystem[1], "v2") {
		t.Fatalf("second system prompt missing v2: %q", provider.seenSystem[1])
	}
	if !strings.Contains(provider.seenSystem[0], `role is "admin"`) {
		t.Fatalf("first system prompt missing principal role: %q", provider.seenSystem[0])
	}
}

func buildRuntimeFixtures(t *testing.T) (*config.Config, *session.SQLiteStore, *fakeProvider, *fakeSender) {
	t.Helper()

	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")

	cfg := &config.Config{
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{
				AdminUserIDs:    []int64{1001},
				ApprovedUserIDs: []int64{1002},
			},
		},
		Agent: config.AgentConfig{
			Workspace:              root,
			MaxIterations:          10,
			ToolTimeout:            10,
			BootstrapFiles:         []string{"AGENTS.md"},
			DynamicFiles:           []string{"MEMORY.md", "HEARTBEAT.md"},
			BootstrapMaxChars:      20000,
			BootstrapTotalMaxChars: 150000,
			DailyNotes:             false,
			DailyNotesDir:          "memory",
		},
	}

	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agent rules"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("memory"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	provider := &fakeProvider{
		replyText: "ok",
		responseUsage: core.TokenUsage{
			InputTokens:  10,
			OutputTokens: 5,
			TotalTokens:  15,
		},
	}
	sender := &fakeSender{}
	return cfg, store, provider, sender
}

func TestNewRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	store := &session.SQLiteStore{}
	provider := &fakeProvider{}
	sender := &fakeSender{}

	if _, err := New(nil, store, provider, nil, sender); err == nil {
		t.Fatal("expected nil config error")
	}
	if _, err := New(cfg, nil, provider, nil, sender); err == nil {
		t.Fatal("expected nil store error")
	}
	if _, err := New(cfg, store, nil, nil, sender); err == nil {
		t.Fatal("expected nil provider error")
	}
	if _, err := New(cfg, store, provider, nil, nil); err == nil {
		t.Fatal("expected nil outbound error")
	}
}

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
