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

func TestHandleInboundTelegramThreadUsesThreadScopeAndVisiblePrefix(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.streamFaceText = "thread reply"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:           9101,
		SenderID:         1001,
		SenderName:       "operator",
		Text:             "keep this work separate",
		MessageID:        41,
		TelegramThreadID: 2,
		Timestamp:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	finalVisible := ""
	if len(sender.editInline) > 0 {
		finalVisible = sender.editInline[len(sender.editInline)-1].Text
	}
	if len(sender.editClear) > 0 {
		finalVisible = sender.editClear[len(sender.editClear)-1].Text
	}
	if finalVisible == "" && len(sender.sent) > 0 {
		finalVisible = sender.sent[len(sender.sent)-1].Text
	}
	sender.mu.Unlock()
	if !strings.HasPrefix(strings.TrimSpace(finalVisible), "(thread 2)\n\n") {
		t.Fatalf("visible reply = %q, want thread prefix", finalVisible)
	}

	threadSession, err := store.Load(session.SessionKey{ChatID: 9101, UserID: 0, Scope: telegramThreadScopeRef(9101, 2)})
	if err != nil {
		t.Fatalf("Load(thread) err = %v", err)
	}
	if got := threadSession.SessionID; got != "telegram_thread:9101:2" {
		t.Fatalf("thread session id = %q, want typed thread scope", got)
	}
	if len(threadSession.Messages) == 0 {
		t.Fatal("thread session has no messages")
	}
	if got := threadSession.Messages[len(threadSession.Messages)-1].Content; got != "thread reply" {
		t.Fatalf("stored assistant content = %q, want unprefixed scene text", got)
	}
}

func TestAbsorbTelegramThreadClosesThreadAndRecordsMainNote(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "The side thread decided to keep child setup read-only."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	thread, created, err := store.CreateTelegramThreadForUpdate(9102, 1001, 501, 71, "create a read-only child", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if !created {
		t.Fatal("thread created = false, want true")
	}
	threadKey := session.SessionKey{ChatID: 9102, UserID: 0, Scope: telegramThreadScopeRef(9102, thread.ThreadID)}
	threadSession, err := store.Load(threadKey)
	if err != nil {
		t.Fatalf("Load(thread) err = %v", err)
	}
	messages := appendSyntheticTurn(threadSession, "create a read-only child", "Use the read-only charter preset.", "Use the read-only charter preset.", "")
	if err := store.Save(threadSession, messages, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(thread) err = %v", err)
	}

	text, err := rt.AbsorbTelegramThread(context.Background(), 9102, 1001, thread.ThreadID)
	if err != nil {
		t.Fatalf("AbsorbTelegramThread() err = %v", err)
	}
	if !strings.Contains(text, "Absorbed thread 1.") {
		t.Fatalf("absorb text = %q, want absorb acknowledgement", text)
	}

	closed, ok, err := store.TelegramThread(9102, thread.ThreadID)
	if err != nil {
		t.Fatalf("TelegramThread() err = %v", err)
	}
	if !ok || closed.Open() {
		t.Fatalf("thread after absorb = %#v ok=%v, want closed", closed, ok)
	}

	mainSession, err := store.Load(session.SessionKey{ChatID: 9102, UserID: 0, Scope: telegramDMScopeRef(9102)})
	if err != nil {
		t.Fatalf("Load(main) err = %v", err)
	}
	if len(mainSession.Messages) < 2 {
		t.Fatalf("main messages = %d, want synthetic absorb turn", len(mainSession.Messages))
	}
	last := mainSession.Messages[len(mainSession.Messages)-1]
	if !strings.Contains(last.Content, "Thread 1 absorbed into the main chat.") {
		t.Fatalf("main absorb note = %q, want thread absorb note", last.Content)
	}
	if strings.Contains(last.Content, "(thread 1)") {
		t.Fatalf("main absorb note = %q, want no presentation prefix in storage", last.Content)
	}

	beforeSecondAbsorb := len(mainSession.Messages)
	if _, err := rt.AbsorbTelegramThread(context.Background(), 9102, 1001, thread.ThreadID); !IsTelegramThreadUserError(err) {
		t.Fatalf("second AbsorbTelegramThread() err = %v, want user-facing closed-thread error", err)
	}
	mainSession, err = store.Load(session.SessionKey{ChatID: 9102, UserID: 0, Scope: telegramDMScopeRef(9102)})
	if err != nil {
		t.Fatalf("Load(main after second absorb) err = %v", err)
	}
	if len(mainSession.Messages) != beforeSecondAbsorb {
		t.Fatalf("main messages after second absorb = %d, want unchanged %d", len(mainSession.Messages), beforeSecondAbsorb)
	}
}

func TestAbsorbTelegramThreadUsesDisplaySlotForVisibleAbsorbText(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "The side thread reached a stable outcome."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	first, _, err := store.CreateTelegramThreadForUpdate(9105, 1001, 801, 101, "first side thread", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(first) err = %v", err)
	}
	if _, closed, err := store.CloseTelegramThread(9105, first.ThreadID, "done", time.Now().UTC()); err != nil || !closed {
		t.Fatalf("CloseTelegramThread(first) closed=%t err=%v", closed, err)
	}
	thread, _, err := store.CreateTelegramThreadForUpdate(9105, 1001, 802, 102, "second canonical thread using display slot one", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate(second) err = %v", err)
	}
	if thread.ThreadID == thread.DisplaySlot || thread.ThreadID != 2 || thread.DisplaySlot != 1 {
		t.Fatalf("thread ids = canonical %d display %d, want canonical 2 display 1", thread.ThreadID, thread.DisplaySlot)
	}

	threadKey := session.SessionKey{ChatID: 9105, UserID: 0, Scope: telegramThreadScopeRef(9105, thread.ThreadID)}
	threadSession, err := store.Load(threadKey)
	if err != nil {
		t.Fatalf("Load(thread) err = %v", err)
	}
	messages := appendSyntheticTurn(threadSession, "second canonical thread using display slot one", "Stable outcome.", "Stable outcome.", "")
	if err := store.Save(threadSession, messages, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(thread) err = %v", err)
	}

	text, err := rt.AbsorbTelegramThread(context.Background(), 9105, 1001, thread.ThreadID)
	if err != nil {
		t.Fatalf("AbsorbTelegramThread() err = %v", err)
	}
	if !strings.Contains(text, "Absorbed thread 1.") || strings.Contains(text, "Absorbed thread 2.") {
		t.Fatalf("absorb text = %q, want visible display slot 1 not canonical id 2", text)
	}

	mainSession, err := store.Load(session.SessionKey{ChatID: 9105, UserID: 0, Scope: telegramDMScopeRef(9105)})
	if err != nil {
		t.Fatalf("Load(main) err = %v", err)
	}
	if len(mainSession.Messages) < 2 {
		t.Fatalf("main messages = %d, want synthetic absorb turn", len(mainSession.Messages))
	}
	userMsg := mainSession.Messages[len(mainSession.Messages)-2]
	assistantMsg := mainSession.Messages[len(mainSession.Messages)-1]
	if userMsg.Content != "/absorb 1" {
		t.Fatalf("synthetic user content = %q, want visible absorb command", userMsg.Content)
	}
	if !strings.Contains(assistantMsg.Content, "Thread 1 absorbed into the main chat.") || strings.Contains(assistantMsg.Content, "Thread 2 absorbed into the main chat.") {
		t.Fatalf("main absorb note = %q, want display slot 1 not canonical id 2", assistantMsg.Content)
	}
	if !strings.Contains(assistantMsg.FloorMetadata, `"thread_id":2`) || !strings.Contains(assistantMsg.FloorMetadata, `"thread_label":"1"`) {
		t.Fatalf("floor metadata = %q, want canonical id and visible label", assistantMsg.FloorMetadata)
	}
}

func TestAbsorbTelegramThreadWaitsForThreadSessionLane(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "The side thread can now be safely absorbed."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	thread, _, err := store.CreateTelegramThreadForUpdate(9104, 1001, 701, 91, "finish before absorb", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadKey := session.SessionKey{ChatID: 9104, UserID: 0, Scope: telegramThreadScopeRef(9104, thread.ThreadID)}
	threadSession, err := store.Load(threadKey)
	if err != nil {
		t.Fatalf("Load(thread) err = %v", err)
	}
	messages := appendSyntheticTurn(threadSession, "finish before absorb", "Thread work finished.", "Thread work finished.", "")
	if err := store.Save(threadSession, messages, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(thread) err = %v", err)
	}

	unlock := rt.lockSession(threadKey)
	done := make(chan error, 1)
	go func() {
		_, err := rt.AbsorbTelegramThread(context.Background(), 9104, 1001, thread.ThreadID)
		done <- err
	}()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("AbsorbTelegramThread() returned before thread lane unlocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AbsorbTelegramThread() err = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AbsorbTelegramThread() did not finish after thread lane unlocked")
	}
}

func TestMemoryReviewIncludesTelegramThreadCandidates(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	thread, _, err := store.CreateTelegramThreadForUpdate(9103, 1001, 601, 81, "track a separate child setup", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	threadKey := session.SessionKey{ChatID: 9103, UserID: 0, Scope: telegramThreadScopeRef(9103, thread.ThreadID)}
	threadSession, err := store.Load(threadKey)
	if err != nil {
		t.Fatalf("Load(thread) err = %v", err)
	}
	messages := appendSyntheticTurn(threadSession, "remember the side thread candidate", "The child should remain read-only.", "The child should remain read-only.", "")
	if err := store.Save(threadSession, messages, core.TokenUsage{}); err != nil {
		t.Fatalf("Save(thread) err = %v", err)
	}

	snapshot, err := rt.MemoryReviewSnapshot(context.Background(), 9103, 1001, core.MemoryReviewSourceSessionRecent)
	if err != nil {
		t.Fatalf("MemoryReviewSnapshot() err = %v", err)
	}
	found := false
	for _, item := range snapshot.Items {
		if strings.Contains(item.Label, "thread=1") && strings.Contains(item.Excerpt, "read-only") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("memory items = %#v, want thread candidate", snapshot.Items)
	}
}

func TestMemoryFocusIsScopedToTelegramThread(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 91033, UserID: 0, Scope: telegramThreadScopeRef(91033, 2)}
	focus := core.MemoryFocus{
		Source:  core.MemoryReviewSourceSessionRecent,
		ItemID:  "thread:2:user",
		Label:   "thread=2 role=user",
		Excerpt: "Keep this thread focused on child setup.",
		Query:   "child setup",
		SetAt:   time.Now().UTC(),
	}
	rt.SetMemoryFocusForKey(key, focus)
	if _, ok := rt.MemoryFocus(91033); ok {
		t.Fatal("MemoryFocus(main chat) = active, want thread focus isolated")
	}
	got, ok := rt.MemoryFocusForKey(key)
	if !ok || got.ItemID != focus.ItemID {
		t.Fatalf("MemoryFocusForKey(thread) = %#v/%t, want thread focus", got, ok)
	}
	msg := rt.applyMemoryFocusToInbound(core.InboundMessage{ChatID: 91033, TelegramThreadID: 2, Text: "continue"}, key)
	if !strings.Contains(msg.Text, "MEMORY_FOCUS_CONTEXT") || !strings.Contains(msg.Text, "child setup") {
		t.Fatalf("thread-focused text = %q, want injected focus context", msg.Text)
	}
	mainKey := session.SessionKey{ChatID: 91033, UserID: 0, Scope: telegramDMScopeRef(91033)}
	mainMsg := rt.applyMemoryFocusToInbound(core.InboundMessage{ChatID: 91033, Text: "continue"}, mainKey)
	if mainMsg.Text != "continue" {
		t.Fatalf("main text = %q, want no thread focus injection", mainMsg.Text)
	}
}

func TestTelegramThreadProgressPrefixHelpers(t *testing.T) {
	t.Parallel()

	run := session.TurnRun{
		Scope: session.TelegramThreadScopeRef(9104, 6),
	}
	if got := progressTurnRunDisplayPrefix(run); got != "(thread 6)" {
		t.Fatalf("progressTurnRunDisplayPrefix() = %q, want thread prefix", got)
	}
	reporter := &toolProgressReporter{displayPrefix: "(thread 6)"}
	if got := reporter.prefixProgressText("Working"); got != "(thread 6)\n\nWorking" {
		t.Fatalf("prefixProgressText() = %q, want visible thread prefix", got)
	}
	if got := reporter.prefixProgressText("(thread 6)\n\nWorking"); got != "(thread 6)\n\nWorking" {
		t.Fatalf("prefixProgressText(existing) = %q, want no duplicate prefix", got)
	}
}
