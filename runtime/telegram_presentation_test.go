//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestTelegramPresentationUsesDisplaySlotForRuntimeVisiblePrefix(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread := createRuntimePresentationDisplaySlotThread(t, store, 9411)

	presentation := rt.telegramPresentationForMessage(core.InboundMessage{ChatID: 9411, TelegramThreadID: thread.ThreadID})
	if presentation.ThreadID != thread.ThreadID || presentation.ThreadLabel != "1" || presentation.Prefix != "(thread 1)" {
		t.Fatalf("presentation = %#v, want durable id %d with display slot prefix", presentation, thread.ThreadID)
	}
	got := rt.prefixTelegramPresentedText(presentation, "Working")
	if got != "(thread 1)\n\nWorking" || strings.Contains(got, "(thread 2)") {
		t.Fatalf("presented text = %q, want display slot 1 not durable id %d", got, thread.ThreadID)
	}
}

func TestTelegramPresentationUsesUnresolvedFallbackWhenLookupMissing(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	presentation := rt.telegramPresentationForMessage(core.InboundMessage{ChatID: 9412, TelegramThreadID: 12})
	if presentation.ThreadID != 12 || presentation.ThreadLabel != "unresolved" || presentation.Prefix != "(thread unresolved)" {
		t.Fatalf("presentation = %#v, want unresolved display-only fallback without minting or exposing durable id", presentation)
	}
}

func TestSendReplyUsesTelegramPresentationPrefix(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread := createRuntimePresentationDisplaySlotThread(t, store, 9413)

	if _, _, err := rt.sendReply(context.Background(), core.InboundMessage{ChatID: 9413, MessageID: 77, TelegramThreadID: thread.ThreadID}, "Final answer", nil, false); err != nil {
		t.Fatalf("sendReply() err = %v", err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || !strings.HasPrefix(sender.sent[0].Text, "(thread 1)\n\n") || strings.Contains(sender.sent[0].Text, "(thread 2)") {
		t.Fatalf("sent = %#v, want final reply prefixed with display slot", sender.sent)
	}
}

func TestStreamEditorUsesTelegramPresentationPrefix(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread := createRuntimePresentationDisplaySlotThread(t, store, 9414)

	stream := rt.newStreamEditor(core.InboundMessage{ChatID: 9414, TelegramThreadID: thread.ThreadID})
	if stream == nil {
		t.Fatal("newStreamEditor() = nil")
	}
	if stream.displayPrefix != "(thread 1)" {
		t.Fatalf("stream.displayPrefix = %q, want display slot prefix", stream.displayPrefix)
	}
}

func TestDeliverWorkResultUsesTelegramPresentationAndKeepsDurableThreadScope(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread := createRuntimePresentationDisplaySlotThread(t, store, 9415)
	key := session.SessionKey{ChatID: 9415, UserID: 0, Scope: telegramThreadScopeRef(9415, thread.ThreadID)}

	if err := rt.deliverWorkResult(context.Background(), key, WorkResult{ExecutorName: "codex", Summary: "patched runtime presentation"}, session.OperationArtifact{}); err != nil {
		t.Fatalf("deliverWorkResult() err = %v", err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 || !strings.HasPrefix(sender.sent[0].Text, "(thread 1)\n\n") || strings.Contains(sender.sent[0].Text, "(thread 2)") {
		t.Fatalf("sent = %#v, want work result prefixed with display slot", sender.sent)
	}
	if got := session.SessionIDForKey(key); !strings.Contains(got, ":2") || strings.Contains(got, ":1") {
		t.Fatalf("SessionIDForKey(%#v) = %q, want durable thread id retained internally", key, got)
	}
}

func createRuntimePresentationDisplaySlotThread(t *testing.T, store *session.SQLiteStore, chatID int64) session.TelegramThread {
	t.Helper()
	now := time.Now().UTC()
	first, _, err := store.CreateTelegramThreadForUpdate(chatID, 1001, 10001, 20001, "first thread", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(first) err = %v", err)
	}
	if _, closed, err := store.CloseTelegramThread(chatID, first.ThreadID, "done", now.Add(time.Second)); err != nil || !closed {
		t.Fatalf("CloseTelegramThread(first) closed=%t err=%v", closed, err)
	}
	thread, _, err := store.CreateTelegramThreadForUpdate(chatID, 1001, 10002, 20002, "second thread reusing display slot", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(second) err = %v", err)
	}
	if thread.ThreadID != 2 || thread.DisplaySlot != 1 {
		t.Fatalf("thread ids = durable %d display %d, want durable 2/display 1", thread.ThreadID, thread.DisplaySlot)
	}
	return thread
}

func TestContinuationPromptUsesPresentationButRecordsDurableCallbackThread(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	thread := createRuntimePresentationDisplaySlotThread(t, store, 9416)
	key := session.SessionKey{ChatID: 9416, UserID: 0, Scope: telegramThreadScopeRef(9416, thread.ThreadID)}
	msg := core.InboundMessage{ChatID: 9416, SenderID: 1001, MessageID: 77, TelegramThreadID: thread.ThreadID}
	state := session.ContinuationState{Status: session.ContinuationStatusPending, DecisionID: "decision-presented", RemainingTurns: 1}

	if err := rt.sendContinuationApprovalPrompt(context.Background(), key, msg, state, "Continue scoped work?"); err != nil {
		t.Fatalf("sendContinuationApprovalPrompt() err = %v", err)
	}
	sender.mu.Lock()
	if len(sender.inline) != 1 || !strings.HasPrefix(sender.inline[0].text, "(thread 1)\n\n") || strings.Contains(sender.inline[0].text, "(thread 2)") {
		t.Fatalf("inline = %#v, want approval card prefixed with display slot", sender.inline)
	}
	sender.mu.Unlock()
	if got, ok, err := store.TelegramThreadIDForReplyMessage(9416, 1); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(continuation prompt) = %d ok=%v err=%v, want durable thread %d", got, ok, err, thread.ThreadID)
	}
}
