//go:build linux

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
)

func TestHandleInboundAggressivePrefetchInjectsSemanticMemory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Memory.Semantic.Enabled = true
	cfg.Memory.Semantic.Sources = []string{"memory/knowledge.md"}
	cfg.Memory.Semantic.InteractiveTopK = 5
	cfg.Memory.Semantic.InteractiveMaxChars = 4000
	cfg.Memory.Aggressive.Enabled = true
	cfg.Memory.Aggressive.PrefetchEveryTurn = true

	knowledgePath := filepath.Join(cfg.Agent.PromptRoot, "memory", "knowledge.md")
	if err := os.MkdirAll(filepath.Dir(knowledgePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() err = %v", err)
	}
	if err := os.WriteFile(knowledgePath, []byte("# knowledge\n\n- User prefers concise updates about queue status."), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge) err = %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     881,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "give me a concise queue status update",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.lastGovernorMsgs) == 0 {
		t.Fatal("lastGovernorMsgs empty, want turn input messages")
	}
	joined := make([]string, 0, len(provider.lastGovernorMsgs))
	for _, msg := range provider.lastGovernorMsgs {
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		joined = append(joined, msg.Content)
	}
	all := strings.Join(joined, "\n\n")
	if !strings.Contains(all, "AUTO_RECALL_MEMORY") {
		t.Fatalf("turn input missing auto recall block: %q", all)
	}
	if !strings.Contains(all, "concise updates") {
		t.Fatalf("turn input missing recalled excerpt: %q", all)
	}
}

func TestHandleInboundAggressiveCaptureWritesCuratedMemory(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Memory.Aggressive.Enabled = true
	cfg.Memory.Aggressive.CaptureEveryTurn = true
	provider.memoryCaptureReplyText = "[MEMORY]\n- Prefers concise status updates after each action.\n[/MEMORY]\n[KNOWLEDGE]\n[/KNOWLEDGE]\n[DECISIONS]\n[/DECISIONS]\n[QUESTIONS]\n[/QUESTIONS]\n[RHIZOME]\n[/RHIZOME]"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     882,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "please keep status updates concise",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(cfg.Agent.PromptRoot, "MEMORY.md"))
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) err = %v", err)
	}
	if !strings.Contains(string(raw), "Prefers concise status updates") {
		t.Fatalf("MEMORY.md missing captured line: %q", string(raw))
	}
}

func TestFlushChatMemoryWritesCuratedMemoryFromSessionBoundary(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Memory.Aggressive.Enabled = true
	cfg.Memory.Aggressive.FlushOnSessionBoundary = true
	provider.memoryFlushReplyText = "[MEMORY]\n[/MEMORY]\n[KNOWLEDGE]\n[/KNOWLEDGE]\n[DECISIONS]\n- Keep session resets explicit and user-driven.\n[/DECISIONS]\n[QUESTIONS]\n[/QUESTIONS]\n[RHIZOME]\n[/RHIZOME]"

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	chatID := int64(883)
	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     chatID,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "start a session that we can flush",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if err := rt.FlushChatMemory(context.Background(), chatID, "new_session"); err != nil {
		t.Fatalf("FlushChatMemory() err = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(cfg.Agent.PromptRoot, "memory", "decisions.md"))
	if err != nil {
		t.Fatalf("ReadFile(decisions.md) err = %v", err)
	}
	if !strings.Contains(string(raw), "session resets explicit") {
		t.Fatalf("decisions.md missing flush memory line: %q", string(raw))
	}
}
