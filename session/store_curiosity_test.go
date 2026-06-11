//go:build linux

package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCuriosityLeaseConsumptionIsBounded(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	lease, err := store.EnsureCuriosityLease(CuriosityLease{
		ID:                 "curiosity-test",
		Status:             CuriosityLeaseStatusActive,
		Scope:              ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"},
		AllowedSourceKinds: []string{CuriositySourceWorkspace},
		AllowedSourceRefs:  []string{"README.md"},
		DailyTurnBudget:    1,
		MaxLooksPerTurn:    1,
		PeriodStart:        "2026-06-10",
		ApprovedBy:         "config:curiosity",
		ExpiresAt:          now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatalf("EnsureCuriosityLease() err = %v", err)
	}
	if lease.TurnsUsed != 0 || lease.Status != CuriosityLeaseStatusActive {
		t.Fatalf("initial lease = %#v", lease)
	}

	consumed, ok, err := store.ConsumeCuriosityLeaseTurn("curiosity-test", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("first ConsumeCuriosityLeaseTurn() err = %v", err)
	}
	if !ok || consumed.TurnsUsed != 1 || consumed.Status != CuriosityLeaseStatusExhausted {
		t.Fatalf("first consume = %#v ok=%v, want one permitted final turn", consumed, ok)
	}
	blocked, ok, err := store.ConsumeCuriosityLeaseTurn("curiosity-test", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("second ConsumeCuriosityLeaseTurn() err = %v", err)
	}
	if ok || blocked.TurnsUsed != 1 || blocked.Status != CuriosityLeaseStatusExhausted {
		t.Fatalf("second consume = %#v ok=%v, want blocked exhausted lease", blocked, ok)
	}
}

func TestCuriosityObservationDedupesByLeaseCandidateAndContentHash(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: -2001, Scope: ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"}}
	input := CuriosityObservationInput{
		LeaseID:     "lease-1",
		CandidateID: "candidate-1",
		SourceKind:  CuriositySourceWorkspace,
		SourceRef:   "README.md",
		SubjectKey:  "release-work",
		Summary:     "README still mentions the release checklist.",
		ContentHash: "sha256:abc",
		Confidence:  0.8,
	}
	first, err := store.RecordCuriosityObservation(key, input, time.Now())
	if err != nil {
		t.Fatalf("RecordCuriosityObservation(first) err = %v", err)
	}
	second, err := store.RecordCuriosityObservation(key, input, time.Now())
	if err != nil {
		t.Fatalf("RecordCuriosityObservation(second) err = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("dedupe IDs = %d/%d, want same row", first.ID, second.ID)
	}
	observations, err := store.CuriosityObservations(10)
	if err != nil {
		t.Fatalf("CuriosityObservations() err = %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(observations))
	}
}

func TestCuriosityRetentionPrunesOldLeasesAndObservations(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	start := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	oldLease, err := store.EnsureCuriosityLease(CuriosityLease{
		ID:                 "curiosity-old",
		Status:             CuriosityLeaseStatusExhausted,
		Scope:              ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"},
		AllowedSourceKinds: []string{CuriositySourceWorkspace},
		AllowedSourceRefs:  []string{"README.md"},
		DailyTurnBudget:    1,
		MaxLooksPerTurn:    1,
		PeriodStart:        "2026-06-01",
		ApprovedBy:         "config:curiosity",
		ExpiresAt:          start.Add(time.Hour),
	}, start)
	if err != nil {
		t.Fatalf("EnsureCuriosityLease(old) err = %v", err)
	}
	key := SessionKey{ChatID: -2002, Scope: ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"}}
	if _, err := store.RecordCuriosityObservation(key, CuriosityObservationInput{
		LeaseID:     oldLease.ID,
		CandidateID: "candidate-old",
		SourceKind:  CuriositySourceWorkspace,
		SourceRef:   "README.md",
		SubjectKey:  "release-work",
		Summary:     "old observation",
		ContentHash: "sha256:old",
		Confidence:  0.8,
		ObservedAt:  start,
	}, start); err != nil {
		t.Fatalf("RecordCuriosityObservation(old) err = %v", err)
	}

	later := start.Add(31 * 24 * time.Hour)
	if _, err := store.EnsureCuriosityLease(CuriosityLease{
		ID:                 "curiosity-new",
		Status:             CuriosityLeaseStatusActive,
		Scope:              ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"},
		AllowedSourceKinds: []string{CuriositySourceWorkspace},
		AllowedSourceRefs:  []string{"README.md"},
		DailyTurnBudget:    1,
		MaxLooksPerTurn:    1,
		PeriodStart:        "2026-07-02",
		ApprovedBy:         "config:curiosity",
		ExpiresAt:          later.Add(time.Hour),
	}, later); err != nil {
		t.Fatalf("EnsureCuriosityLease(new) err = %v", err)
	}
	observations, err := store.CuriosityObservations(10)
	if err != nil {
		t.Fatalf("CuriosityObservations() err = %v", err)
	}
	if len(observations) != 0 {
		t.Fatalf("old observations survived prune: %#v", observations)
	}
	var leaseCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM curiosity_leases WHERE id = ?`, oldLease.ID).Scan(&leaseCount); err != nil {
		t.Fatalf("query old lease count: %v", err)
	}
	if leaseCount != 0 {
		t.Fatalf("old lease count = %d, want pruned", leaseCount)
	}
}
