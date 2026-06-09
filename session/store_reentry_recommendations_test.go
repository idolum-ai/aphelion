//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReentryRecommendationStoreDedupeAndTransitions(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	record := ReentryRecommendation{
		ID:                  "reentry-test",
		Owner:               "telegram:7",
		ChatID:              7,
		SenderID:            1001,
		SessionID:           "telegram:7:0",
		Scope:               ScopeRef{Kind: ScopeKindTelegramDM, ID: "7"},
		SourceTurnRunID:     42,
		TerminalFingerprint: "terminal-state-a",
		Candidates: []ReentryRecommendationCandidate{
			{
				ID:               "c1",
				Kind:             ReentryCandidateRequestNextLease,
				Label:            "Next lease",
				Summary:          "Open a bounded follow-up.",
				PromptText:       "Ask for fresh approval before continuing.",
				AuthorityClass:   "commit",
				RequiresApproval: true,
			},
		},
	}
	created, allowed, reason, err := store.CreateReentryRecommendationIfAllowed(record, now)
	if err != nil {
		t.Fatalf("CreateReentryRecommendationIfAllowed() err = %v", err)
	}
	if !allowed || reason != "" {
		t.Fatalf("allowed=%v reason=%q, want first create allowed", allowed, reason)
	}
	if created.Status != ReentryRecommendationStatusPending || created.CreatedAt.IsZero() {
		t.Fatalf("created = %#v, want pending timestamped recommendation", created)
	}

	duplicate := record
	duplicate.ID = "reentry-duplicate"
	_, allowed, reason, err = store.CreateReentryRecommendationIfAllowed(duplicate, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CreateReentryRecommendationIfAllowed(duplicate) err = %v", err)
	}
	if allowed || reason != "same_terminal_fingerprint" {
		t.Fatalf("duplicate allowed=%v reason=%q, want same_terminal_fingerprint block", allowed, reason)
	}

	shown, ok, err := store.MarkReentryRecommendationShown(created.ID, 93, now.Add(time.Second))
	if err != nil || !ok {
		t.Fatalf("MarkReentryRecommendationShown() ok=%v err=%v", ok, err)
	}
	if shown.Status != ReentryRecommendationStatusShown || shown.DeliveryMessageID != 93 || shown.ShownAt.IsZero() {
		t.Fatalf("shown = %#v, want shown status with delivery message", shown)
	}

	selected, ok, err := store.MarkReentryRecommendationSelected(created.ID, "c1", "operator selected candidate", now.Add(2*time.Second))
	if err != nil || !ok {
		t.Fatalf("MarkReentryRecommendationSelected() ok=%v err=%v", ok, err)
	}
	if selected.Status != ReentryRecommendationStatusSelected || selected.SelectedCandidateID != "c1" || selected.SelectedAt.IsZero() {
		t.Fatalf("selected = %#v, want selected terminal status", selected)
	}

	ignored, ok, err := store.MarkReentryRecommendationIgnored(created.ID, "late ignore", now.Add(3*time.Second))
	if err != nil || !ok {
		t.Fatalf("MarkReentryRecommendationIgnored(after selected) ok=%v err=%v", ok, err)
	}
	if ignored.Status != ReentryRecommendationStatusSelected {
		t.Fatalf("late ignored status = %s, want terminal selected unchanged", ignored.Status)
	}
}
