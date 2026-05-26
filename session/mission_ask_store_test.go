//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMissionAskPromptLedgerCooldownsAndStatus(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "mission-ask.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	key := SessionKey{ChatID: 1001, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1001"}}
	owner := "telegram:1001"

	low, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(owner, key, "low-1", MissionAskConfidenceLow), now)
	if err != nil || !allowed || reason != "" {
		t.Fatalf("CreateMissionAskPromptIfAllowed(low) = prompt:%#v allowed:%t reason:%q err:%v, want allowed", low, allowed, reason, err)
	}
	if low.Status != MissionAskStatusPending || low.Confidence != MissionAskConfidenceLow {
		t.Fatalf("low prompt = %#v, want normalized pending/low", low)
	}

	if _, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(owner, key, "low-2", MissionAskConfidenceLow), now.Add(time.Hour)); err != nil || allowed || reason != "low_confidence_cooldown" {
		t.Fatalf("second low prompt allowed=%t reason=%q err=%v, want low confidence cooldown", allowed, reason, err)
	}
	if _, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(owner, key, "low-1", MissionAskConfidenceLow), now.Add(2*time.Hour)); err != nil || allowed || reason != "same_association_cooldown" {
		t.Fatalf("same association prompt allowed=%t reason=%q err=%v, want same association cooldown", allowed, reason, err)
	}

	asked, err := store.UpdateMissionAskPromptStatus(low.ID, owner, MissionAskStatusAsked, "queued", now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("UpdateMissionAskPromptStatus(asked) err = %v", err)
	}
	if asked.Status != MissionAskStatusAsked || asked.AskedAt.IsZero() || asked.ResolvedAt.IsZero() == false {
		t.Fatalf("asked prompt = %#v, want asked_at only", asked)
	}
	pending, ok, err := store.PendingMissionAskPromptForSession(owner, key)
	if err != nil || !ok || pending.ID != low.ID {
		t.Fatalf("PendingMissionAskPromptForSession() = %#v ok=%t err=%v, want asked prompt", pending, ok, err)
	}
	resolved, err := store.UpdateMissionAskPromptStatus(low.ID, owner, MissionAskStatusResolved, "operator answered", now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("UpdateMissionAskPromptStatus(resolved) err = %v", err)
	}
	if resolved.Status != MissionAskStatusResolved || resolved.ResolvedAt.IsZero() || resolved.ResultSummary != "operator answered" {
		t.Fatalf("resolved prompt = %#v, want resolved terminal summary", resolved)
	}
	if _, ok, err := store.PendingMissionAskPromptForSession(owner, key); err != nil || ok {
		t.Fatalf("PendingMissionAskPromptForSession(after resolved) ok=%t err=%v, want none", ok, err)
	}
}

func TestMissionAskPromptLedgerHighConfidenceAndIgnoredCooldowns(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "mission-ask-cooldowns.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	key := SessionKey{ChatID: 1001, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1001"}}
	owner := "telegram:1001"

	firstHigh, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(owner, key, "high-1", MissionAskConfidenceHigh), now)
	if err != nil || !allowed || reason != "" {
		t.Fatalf("CreateMissionAskPromptIfAllowed(high) = prompt:%#v allowed:%t reason:%q err:%v, want allowed", firstHigh, allowed, reason, err)
	}
	if _, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(owner, key, "high-2", MissionAskConfidenceHigh), now.Add(2*time.Hour)); err != nil || allowed || reason != "high_confidence_cooldown" {
		t.Fatalf("second high prompt allowed=%t reason=%q err=%v, want high confidence cooldown", allowed, reason, err)
	}
	if _, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(owner, key, "high-2", MissionAskConfidenceHigh), now.Add(5*time.Hour)); err != nil || !allowed || reason != "" {
		t.Fatalf("high prompt after cooldown allowed=%t reason=%q err=%v, want allowed", allowed, reason, err)
	}

	ignoredOwner := "telegram:2002"
	ignored, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(ignoredOwner, key, "ignored-association", MissionAskConfidenceHigh), now)
	if err != nil || !allowed || reason != "" {
		t.Fatalf("CreateMissionAskPromptIfAllowed(ignored seed) = prompt:%#v allowed:%t reason:%q err:%v, want allowed", ignored, allowed, reason, err)
	}
	if _, err := store.UpdateMissionAskPromptStatus(ignored.ID, ignoredOwner, MissionAskStatusIgnored, "operator ignored", now.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateMissionAskPromptStatus(ignored) err = %v", err)
	}
	if _, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(testMissionAskPrompt(ignoredOwner, key, "ignored-association", MissionAskConfidenceHigh), now.Add(6*time.Hour)); err != nil || allowed || reason != "ignored_association_cooldown" {
		t.Fatalf("ignored association allowed=%t reason=%q err=%v, want ignored association cooldown", allowed, reason, err)
	}
}

func testMissionAskPrompt(owner string, key SessionKey, fingerprint string, confidence MissionAskConfidence) MissionAskPrompt {
	return MissionAskPrompt{
		Owner:             owner,
		ChatID:            key.ChatID,
		SenderID:          1001,
		SessionID:         SessionIDForKey(key),
		Scope:             key.Scope,
		SourceMessageID:   42,
		MissionID:         "mission-docs",
		Confidence:        confidence,
		Status:            MissionAskStatusPending,
		QuestionText:      "Should this be associated with the docs mission?",
		SourceFingerprint: fingerprint,
		EvidenceJSON:      `{"source":"test"}`,
	}
}
