//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStreamEditorUsesEphemeralStopControls(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	editor := &streamEditor{
		sender:          sender,
		editor:          sender,
		keyboardEditor:  sender,
		keyboardClearer: sender,
		chatID:          42,
		interval:        0,
		cursor:          "...",
		controlRows:     streamStopControlRows("stream-1"),
	}

	if err := editor.OnChunk(context.Background(), "hello"); err != nil {
		t.Fatalf("OnChunk() err = %v", err)
	}
	if err := editor.OnChunk(context.Background(), " world"); err != nil {
		t.Fatalf("OnChunk(second) err = %v", err)
	}
	if _, err := editor.Finish(context.Background()); err != nil {
		t.Fatalf("Finish() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "hello world" {
		t.Fatalf("stored text = %q, want final text", sender.sent[0].Text)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline len = %d, want 1", len(sender.editInline))
	}
	if len(sender.editInline[0].Rows) != 1 || len(sender.editInline[0].Rows[0]) != 1 {
		t.Fatalf("inline rows = %#v, want one stop button", sender.editInline[0].Rows)
	}
	if got := sender.editInline[0].Rows[0][0].CallbackData; !strings.HasPrefix(got, "stream:stream-1:stop") {
		t.Fatalf("callback data = %q, want stream stop callback", got)
	}
	if len(sender.editClear) != 1 || sender.editClear[0].Text != "hello world" {
		t.Fatalf("clear edits = %#v, want final clear edit with hello world", sender.editClear)
	}
}

func TestStreamEditorFinishStoppedClearsControls(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	editor := &streamEditor{
		sender:          sender,
		editor:          sender,
		keyboardEditor:  sender,
		keyboardClearer: sender,
		chatID:          42,
		interval:        time.Hour,
		cursor:          "...",
		controlRows:     streamStopControlRows("stream-2"),
	}

	if err := editor.OnChunk(context.Background(), "partial"); err != nil {
		t.Fatalf("OnChunk() err = %v", err)
	}
	if _, err := editor.FinishStopped(context.Background()); err != nil {
		t.Fatalf("FinishStopped() err = %v", err)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear len = %d, want 1", len(sender.editClear))
	}
	if got := sender.editClear[0].Text; !strings.Contains(got, "partial") || !strings.Contains(got, "Stopped.") {
		t.Fatalf("stopped text = %q, want partial and stopped marker", got)
	}
}

func TestRuntimeStreamControlLifecycle(t *testing.T) {
	t.Parallel()

	rt := &Runtime{}
	streamID := rt.beginStreamControl(42)
	if streamID == "" {
		t.Fatal("beginStreamControl() returned empty id")
	}
	rt.attachStreamControlMessage(streamID, 99)
	if !rt.MarkStreamControlStopping(streamID, 42) {
		t.Fatal("MarkStreamControlStopping() = false, want true for active stream")
	}
	if rt.MarkStreamControlStopping(streamID, 42) {
		t.Fatal("MarkStreamControlStopping() = true after first stop, want stale false")
	}

	otherID := rt.beginStreamControl(42)
	if rt.MarkStreamControlStopping(otherID, 7) {
		t.Fatal("MarkStreamControlStopping() = true for wrong chat, want false")
	}
	rt.finishStreamControl(otherID)
	if rt.MarkStreamControlStopping(otherID, 42) {
		t.Fatal("MarkStreamControlStopping() = true after finish, want false")
	}
}
