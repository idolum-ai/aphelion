//go:build linux

package session

import (
	"strings"
	"testing"
	"time"
)

func TestCreateTelegramThreadPromotionDraftCreatesTypedHandoff(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 22, 20, 0, 0, 0, time.UTC)
	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "turn this into a durable child", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	handoff, created, err := store.CreateTelegramThreadPromotionDraft(1001, thread.ThreadID, 2002, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CreateTelegramThreadPromotionDraft() err = %v", err)
	}
	if !created {
		t.Fatal("created = false, want first draft create")
	}
	if handoff.ChatID != 1001 || handoff.ThreadID != thread.ThreadID || handoff.DisplaySlot != thread.DisplaySlot {
		t.Fatalf("handoff thread refs = %#v, want source thread ids", handoff)
	}
	if handoff.Status != TelegramThreadPromotionStatusDraft {
		t.Fatalf("status = %q, want draft", handoff.Status)
	}
	if handoff.SourceSessionID != "telegram_thread:1001:1" {
		t.Fatalf("SourceSessionID = %q, want typed thread session", handoff.SourceSessionID)
	}
	if !strings.Contains(handoff.ContextSummary, "explicit review") || !strings.Contains(handoff.ReviewChecklistJSON, "review memory candidates") {
		t.Fatalf("handoff summary/checklist = %#v, want review requirements", handoff)
	}
	if handoff.MemoryDigestJSON != "[]" || handoff.ResourceReviewJSON != "[]" || handoff.PolicyPatchJSON != "{}" {
		t.Fatalf("handoff defaults = %#v, want no memory/resource/policy transfer", handoff)
	}

	again, created, err := store.CreateTelegramThreadPromotionDraft(1001, thread.ThreadID, 2002, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CreateTelegramThreadPromotionDraft(second) err = %v", err)
	}
	if created || again.HandoffID != handoff.HandoffID {
		t.Fatalf("second draft = %#v created=%v, want idempotent existing draft %s", again, created, handoff.HandoffID)
	}
}

func TestCreateTelegramThreadPromotionDraftRejectsClosedThread(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "finished lane", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if _, closed, err := store.CloseTelegramThread(1001, thread.ThreadID, "done", time.Now().UTC()); err != nil || !closed {
		t.Fatalf("CloseTelegramThread() closed=%t err=%v", closed, err)
	}
	if _, _, err := store.CreateTelegramThreadPromotionDraft(1001, thread.ThreadID, 2002, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("CreateTelegramThreadPromotionDraft(closed) err = %v, want not-open refusal", err)
	}
}
