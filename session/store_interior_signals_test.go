//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestInteriorSignalStoreAccumulatesDedupesDecaysAndCooldown(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	key := SessionKey{ChatID: 7101, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "7101"}}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	obs := InteriorSignalObservationInput{
		Category:          "semantic_recurrence",
		SubjectKey:        "release-workflow",
		Summary:           "release workflow keeps resurfacing",
		Source:            "heartbeat_review_events",
		SourceFingerprint: "same-evidence",
		Weight:            0.5,
		Confidence:        0.6,
		Evidence:          []RecordReference{{Kind: "review_event", Ref: "review_event:1"}},
	}

	states, err := store.RecordInteriorSignalObservations(key, []InteriorSignalObservationInput{obs}, now)
	if err != nil {
		t.Fatalf("RecordInteriorSignalObservations(first) err = %v", err)
	}
	state := requireInteriorSignalState(t, states, "semantic_recurrence", "release-workflow")
	if state.ObservationCount != 1 || state.Intensity != 0.5 {
		t.Fatalf("first state = %#v, want one observation at 0.5 intensity", state)
	}

	states, err = store.RecordInteriorSignalObservations(key, []InteriorSignalObservationInput{obs}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("RecordInteriorSignalObservations(duplicate) err = %v", err)
	}
	state = requireInteriorSignalState(t, states, "semantic_recurrence", "release-workflow")
	if state.ObservationCount != 1 {
		t.Fatalf("duplicate observation count = %d, want unchanged", state.ObservationCount)
	}
	if state.Intensity >= 0.5 {
		t.Fatalf("duplicate intensity = %.4f, want decayed below first value", state.Intensity)
	}

	obs.SourceFingerprint = "new-evidence"
	obs.Evidence = []RecordReference{{Kind: "review_event", Ref: "review_event:2"}}
	states, err = store.RecordInteriorSignalObservations(key, []InteriorSignalObservationInput{obs}, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("RecordInteriorSignalObservations(new evidence) err = %v", err)
	}
	state = requireInteriorSignalState(t, states, "semantic_recurrence", "release-workflow")
	if state.ObservationCount != 2 {
		t.Fatalf("new evidence observation count = %d, want 2", state.ObservationCount)
	}
	if state.Intensity <= 0.85 {
		t.Fatalf("new evidence intensity = %.4f, want accumulated pressure", state.Intensity)
	}

	observations, err := store.RecentInteriorSignalObservations(key, []InteriorSignalRef{{Category: "semantic_recurrence", SubjectKey: "release-workflow"}}, now.Add(30*time.Minute), 10)
	if err != nil {
		t.Fatalf("RecentInteriorSignalObservations() err = %v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("recent observations = %d, want duplicate plus new evidence after since", len(observations))
	}
	if observations[0].SourceFingerprint != "new-evidence" || observations[1].SourceFingerprint != "same-evidence" {
		t.Fatalf("recent observation order = %#v, want newest first", observations)
	}
	if observations[0].AppliedWeight <= 0 || observations[1].AppliedWeight != 0 {
		t.Fatalf("applied weights = %.2f/%.2f, want new evidence applied and duplicate suppressed", observations[0].AppliedWeight, observations[1].AppliedWeight)
	}
	filtered, err := store.RecentInteriorSignalObservations(key, []InteriorSignalRef{{Category: "semantic_recurrence", SubjectKey: "other"}}, time.Time{}, 10)
	if err != nil {
		t.Fatalf("RecentInteriorSignalObservations(filtered) err = %v", err)
	}
	if len(filtered) != 0 {
		t.Fatalf("filtered observations = %#v, want none for unmatched ref", filtered)
	}

	states, err = store.InteriorSignalStates(key, now.Add(14*time.Hour))
	if err != nil {
		t.Fatalf("InteriorSignalStates(decayed) err = %v", err)
	}
	decayed := requireInteriorSignalState(t, states, "semantic_recurrence", "release-workflow")
	if decayed.Intensity >= state.Intensity {
		t.Fatalf("decayed intensity = %.4f, want below %.4f", decayed.Intensity, state.Intensity)
	}

	if err := store.MarkInteriorSignalsSurfaced(key, []InteriorSignalRef{{Category: "semantic_recurrence", SubjectKey: "release-workflow"}}, now.Add(15*time.Hour)); err != nil {
		t.Fatalf("MarkInteriorSignalsSurfaced() err = %v", err)
	}
	states, err = store.InteriorSignalStates(key, now.Add(15*time.Hour))
	if err != nil {
		t.Fatalf("InteriorSignalStates(cooldown) err = %v", err)
	}
	state = requireInteriorSignalState(t, states, "semantic_recurrence", "release-workflow")
	if !InteriorSignalInCooldown(state, now.Add(15*time.Hour)) {
		t.Fatalf("state cooldown = %#v, want active cooldown", state.CooldownUntil)
	}
}

func TestInteriorSignalTablesMigrateFromV65(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version(version) VALUES (65)`); err != nil {
		t.Fatalf("insert schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v65) err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteColumn(t, store.db, "interior_signal_states", "intensity")
	assertSQLiteColumn(t, store.db, "interior_signal_observations", "applied_weight")
}

func requireInteriorSignalState(t *testing.T, states []InteriorSignalState, category, subjectKey string) InteriorSignalState {
	t.Helper()
	for _, state := range states {
		if state.Category == category && state.SubjectKey == subjectKey {
			return state
		}
	}
	t.Fatalf("state %s/%s not found in %#v", category, subjectKey, states)
	return InteriorSignalState{}
}
