//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestHandleInboundRendersIdolumForSimpleFactualTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "The current time is 12:00 UTC."
	provider.faceReplyText = "It's 12:00 UTC."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     73,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "what time is it?",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) != 1 {
		t.Fatalf("seenProposalSystem len = %d, want 1", len(provider.seenProposalSystem))
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face render for simple factual turn")
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
	if finalText != "It's 12:00 UTC." {
		t.Fatalf("final text = %q, want face-rendered reply", finalText)
	}
}

func TestHandleInboundCompactsLongSessionBeforeGovernorTurn(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	cfg.Sessions.MaxContextRatio = 0.40
	cfg.Sessions.CompactionRatio = 0.20
	cfg.Governor.Codex.ContextWindow = 120
	provider.replyText = "fresh reply"
	provider.compactionReplyText = "Compacted summary of the earlier conversation."

	key := session.SessionKey{ChatID: 74, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 4
	long := strings.Repeat("memory-rich content ", 20)
	if err := store.Save(sess, []session.Message{
		{Role: "user", Content: "turn one " + long, TurnIndex: 1},
		{Role: "assistant", Content: "reply one " + long, FloorContent: "reply one " + long, TurnIndex: 1},
		{Role: "user", Content: "turn two " + long, TurnIndex: 2},
		{Role: "assistant", Content: "reply two " + long, FloorContent: "reply two " + long, TurnIndex: 2},
		{Role: "user", Content: "turn three " + long, TurnIndex: 3},
		{Role: "assistant", Content: "reply three " + long, FloorContent: "reply three " + long, TurnIndex: 3},
		{Role: "user", Content: "turn four " + long, TurnIndex: 4},
		{Role: "assistant", Content: "reply four " + long, FloorContent: "reply four " + long, TurnIndex: 4},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(seed) err = %v", err)
	}

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.governorBackend = "codex"

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     74,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "continue",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	reloaded, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load(reloaded) err = %v", err)
	}
	if len(reloaded.CompactionLog) == 0 {
		t.Fatal("compaction log empty, want at least one entry")
	}
	if reloaded.CompactionLog[len(reloaded.CompactionLog)-1].Strategy != "summarize" {
		t.Fatalf("compaction strategy = %q, want summarize", reloaded.CompactionLog[len(reloaded.CompactionLog)-1].Strategy)
	}
	foundSummary := false
	compactedCount := 0
	for _, msg := range reloaded.Messages {
		if msg.Compacted {
			compactedCount++
		}
		if msg.Role == "assistant" && strings.Contains(msg.Content, "Compacted summary of the earlier conversation.") {
			foundSummary = true
		}
	}
	if compactedCount == 0 {
		t.Fatal("compactedCount = 0, want some old messages compacted")
	}
	if !foundSummary {
		t.Fatal("compaction summary message not found in reloaded session")
	}
}

func TestHandleInboundRendersIdolumForCodeHeavyReply(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "```go\nfmt.Println(\"hi\")\n```"
	provider.proposalReplyText = "Push harder"
	provider.faceReplyText = "Idolum should not render code-heavy output."

	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if _, err := rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     75,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "please look into why this code is written this way",
		MessageID:  1,
	}); err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.seenProposalSystem) == 0 {
		t.Fatal("seenProposalSystem empty, want proposal call for open-ended request")
	}
	if len(provider.seenFaceSystem) == 0 {
		t.Fatal("seenFaceSystem empty, want face render for code-heavy reply")
	}
}
