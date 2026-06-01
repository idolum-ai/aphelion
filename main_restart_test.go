//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/router"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

type stubRestartDetacher struct {
	detachAllCalls int
	detachAllErr   error
}

func (s *stubRestartDetacher) DetachByOwner(ctx context.Context, ownerKey string) (int, error) {
	_ = ctx
	_ = ownerKey
	return 0, nil
}

func (s *stubRestartDetacher) DetachAll(ctx context.Context) (int, error) {
	_ = ctx
	s.detachAllCalls++
	if s.detachAllErr != nil {
		return 0, s.detachAllErr
	}
	return 3, nil
}

func TestTelegramCommandControlRestartSchedulesProcessExit(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	control := telegramCommandControl{}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v", err)
	}

	select {
	case code := <-exited:
		if code != exitCodeFailure {
			t.Fatalf("exit code = %d, want %d", code, exitCodeFailure)
		}
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}

func TestTelegramCommandControlRestartDetachesPendingWhenConfigured(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	detacher := &stubRestartDetacher{}
	control := telegramCommandControl{
		decisionDetacher:       detacher,
		detachPendingOnRestart: true,
	}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v", err)
	}

	if detacher.detachAllCalls != 1 {
		t.Fatalf("detach all calls = %d, want 1", detacher.detachAllCalls)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}

func TestTelegramCommandControlRestartSkipsDetachWhenDisabled(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	detacher := &stubRestartDetacher{}
	control := telegramCommandControl{
		decisionDetacher:       detacher,
		detachPendingOnRestart: false,
	}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v", err)
	}
	if detacher.detachAllCalls != 0 {
		t.Fatalf("detach all calls = %d, want 0", detacher.detachAllCalls)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}

func TestTelegramCommandControlRestartContinuesWhenDetachFails(t *testing.T) {
	originalExit := processExit
	defer func() {
		processExit = originalExit
	}()

	exited := make(chan int, 1)
	processExit = func(code int) {
		exited <- code
	}

	detacher := &stubRestartDetacher{detachAllErr: errors.New("db unavailable")}
	control := telegramCommandControl{
		decisionDetacher:       detacher,
		detachPendingOnRestart: true,
	}
	if err := control.Restart(7); err != nil {
		t.Fatalf("Restart() err = %v, want nil even when detach fails", err)
	}
	if detacher.detachAllCalls != 1 {
		t.Fatalf("detach all calls = %d, want 1", detacher.detachAllCalls)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("Restart() did not schedule process exit")
	}
}

type stopFlushProvider struct{}

func (p *stopFlushProvider) Complete(_ context.Context, messages []agent.Message, _ []agent.ToolDef) (*agent.Response, error) {
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		if strings.Contains(msg.Content, "BEGIN_SESSION_MEMORY_FLUSH") {
			return &agent.Response{Content: "[MEMORY]\n[/MEMORY]\n[KNOWLEDGE]\n[/KNOWLEDGE]\n[DECISIONS]\n- stop boundary flush is durable.\n[/DECISIONS]\n[QUESTIONS]\n[/QUESTIONS]\n[RHIZOME]\n[/RHIZOME]"}, nil
		}
	}
	return &agent.Response{Content: "ok"}, nil
}

type stopFlushSender struct {
	sent []core.OutboundMessage
}

func (s *stopFlushSender) SendMessage(_ context.Context, msg core.OutboundMessage) (int64, error) {
	s.sent = append(s.sent, msg)
	return int64(len(s.sent)), nil
}

func TestTelegramCommandControlStopFlushesMemoryOnBoundaryWhenConfigured(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("rules"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("memory"), 0o600); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}

	cfg := &config.Config{
		Principals: config.PrincipalsConfig{
			Telegram: config.TelegramPrincipalsConfig{AdminUserIDs: []int64{1001}},
		},
		Governor: config.GovernorConfig{
			Backend:        "native",
			NativeProvider: "anthropic",
			Codex: config.GovernorCodexConfig{
				AuthSource:    "auto",
				BaseURL:       "https://chatgpt.com/backend-api",
				ContextWindow: 200000,
			},
		},
		Sessions: config.SessionsConfig{
			DBPath:             dbPath,
			IdleExpiry:         "24h",
			MaxContextRatio:    0.75,
			CompactionRatio:    0.55,
			CompactionStrategy: "summarize",
		},
		Agent: config.AgentConfig{
			PromptRoot:             root,
			ExecRoot:               root,
			SharedMemoryRoot:       root,
			UserWorkspaceRoot:      filepath.Join(root, "isolated", "workspaces"),
			UserMemoryRoot:         filepath.Join(root, "isolated", "memory"),
			MaxIterations:          10,
			ToolTimeout:            10,
			BootstrapFiles:         []string{"AGENTS.md"},
			DynamicFiles:           []string{"MEMORY.md"},
			BootstrapMaxChars:      20000,
			BootstrapTotalMaxChars: 150000,
		},
		Memory: config.MemoryConfig{
			Aggressive: config.MemoryAggressiveConfig{
				Enabled:                true,
				FlushOnSessionBoundary: true,
			},
			Reflection: config.MemoryReflectionConfig{Enabled: true, Every: "6h"},
			Decay:      config.MemoryDecayConfig{Enabled: true, HotDays: 3, WarmDays: 14, ColdDays: 30},
			Identity:   config.MemoryIdentityConfig{Preserve: []string{"SOUL.md", "IDENTITY.md", "MEMORY.md"}},
			WritePolicy: config.MemoryWritePolicyConfig{
				DirectUserWrites: "apply",
				ReflectionWrites: "propose",
				AggressiveWrites: "apply",
			},
		},
	}

	sender := &stopFlushSender{}
	rt, err := runtime.New(cfg, store, &stopFlushProvider{}, nil, sender)
	if err != nil {
		t.Fatalf("runtime.New() err = %v", err)
	}

	chatID := int64(444)
	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     chatID,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "create session before stop",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	control := telegramCommandControl{
		router: router.NewRouter(rt.AgentFunc()),
		rt:     rt,
	}
	control.Stop(chatID)

	raw, err := os.ReadFile(filepath.Join(root, "memory", "decisions.md"))
	if err != nil {
		t.Fatalf("read decisions.md: %v", err)
	}
	if !strings.Contains(string(raw), "stop boundary flush is durable") {
		t.Fatalf("decisions.md = %q, want flushed boundary memory", string(raw))
	}
}
