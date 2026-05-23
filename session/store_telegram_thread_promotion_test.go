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
	if handoff.MemoryDigestJSON != "[]" || handoff.ResourceReviewJSON != "[]" || handoff.PolicyPatchJSON != "{}" || handoff.ProposedChildJSON != "{}" {
		t.Fatalf("handoff defaults = %#v, want no memory/resource/policy/child transfer", handoff)
	}
	if handoff.FirstTask == "" || handoff.ValidationJSON == "[]" {
		t.Fatalf("first task/validation = %#v/%s, want review package defaults", handoff.FirstTask, handoff.ValidationJSON)
	}

	again, created, err := store.CreateTelegramThreadPromotionDraft(1001, thread.ThreadID, 2002, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("CreateTelegramThreadPromotionDraft(second) err = %v", err)
	}
	if created || again.HandoffID != handoff.HandoffID {
		t.Fatalf("second draft = %#v created=%v, want idempotent existing draft %s", again, created, handoff.HandoffID)
	}
}

func TestTelegramThreadPromotionReviewPackageAndTransitions(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	now := time.Date(2026, 5, 23, 8, 0, 0, 0, time.UTC)
	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "turn this into a durable child", now)
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	handoff, _, err := store.CreateTelegramThreadPromotionDraft(1001, thread.ThreadID, 2002, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CreateTelegramThreadPromotionDraft() err = %v", err)
	}
	handoff.ContextSummary = "review package"
	handoff.MemoryDigestJSON = EncodeTelegramThreadPromotionJSON([]TelegramThreadPromotionMemoryCandidate{{CandidateID: "m1", Decision: "review_required"}}, `[]`)
	handoff.ResourceReviewJSON = EncodeTelegramThreadPromotionJSON([]TelegramThreadPromotionResourceCandidate{{CandidateID: "r1", Decision: "review_required"}}, `[]`)
	handoff.PolicyPatchJSON = EncodeTelegramThreadPromotionJSON(TelegramThreadPromotionPolicyReview{Autonomy: "review_before_reply", Visibility: "parent_relay_only"}, `{}`)
	handoff.ProposedChildJSON = EncodeTelegramThreadPromotionJSON(TelegramThreadPromotionProposedChild{AgentID: "thread-1001-1", Label: "durable lane"}, `{}`)
	updated, err := store.UpdateTelegramThreadPromotionReviewPackage(handoff, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("UpdateTelegramThreadPromotionReviewPackage() err = %v", err)
	}
	if updated.ProposedChildJSON == "{}" || !strings.Contains(updated.MemoryDigestJSON, "review_required") {
		t.Fatalf("updated handoff = %#v, want review package", updated)
	}
	ready, err := store.MarkTelegramThreadPromotionReady(handoff.HandoffID, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("MarkTelegramThreadPromotionReady() err = %v", err)
	}
	if ready.Status != TelegramThreadPromotionStatusReady {
		t.Fatalf("ready status = %q", ready.Status)
	}
	cancelled, err := store.CancelTelegramThreadPromotion(handoff.HandoffID, now.Add(4*time.Minute))
	if err != nil {
		t.Fatalf("CancelTelegramThreadPromotion() err = %v", err)
	}
	if cancelled.Status != TelegramThreadPromotionStatusCancelled {
		t.Fatalf("cancelled status = %q", cancelled.Status)
	}
	fresh, created, err := store.CreateTelegramThreadPromotionDraft(1001, thread.ThreadID, 2002, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("CreateTelegramThreadPromotionDraft(after cancel) err = %v", err)
	}
	if !created || fresh.HandoffID == handoff.HandoffID {
		t.Fatalf("fresh draft=%#v created=%t, want new draft after cancel", fresh, created)
	}
	superseded, err := store.SupersedeTelegramThreadPromotion(fresh.HandoffID, now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("SupersedeTelegramThreadPromotion() err = %v", err)
	}
	if superseded.Status != TelegramThreadPromotionStatusSuperseded {
		t.Fatalf("superseded status = %q", superseded.Status)
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
