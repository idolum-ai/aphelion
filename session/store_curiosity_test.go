//go:build linux

package session

import (
	"path/filepath"
	"strings"
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

func TestCuriosityLeaseIDIgnoresAuthorityEnvelopeForDailySpend(t *testing.T) {
	period := "2026-06-10"
	first := CuriosityLeaseID(period, []string{CuriositySourceWorkspace}, []string{"README.md"})
	second := CuriosityLeaseID(period, []string{CuriositySourceWorkspace, CuriositySourceURL}, []string{"README.md", "https://example.com/feed"})
	if first != second {
		t.Fatalf("CuriosityLeaseID changed with authority envelope: %q vs %q", first, second)
	}
	if first != "curiosity-"+period {
		t.Fatalf("CuriosityLeaseID = %q, want lane/day stable ID", first)
	}
}

func TestCuriosityLeaseEnvelopeNarrowingPreservesSpend(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	leaseID := CuriosityLeaseID("2026-06-10", nil, nil)
	initial, err := store.EnsureCuriosityLease(CuriosityLease{
		ID:                 leaseID,
		Status:             CuriosityLeaseStatusActive,
		Scope:              ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"},
		AllowedSourceKinds: []string{CuriositySourceWorkspace, CuriositySourceMemory},
		AllowedSourceRefs:  []string{"README.md", "memory/questions.md"},
		DailyTurnBudget:    3,
		MaxLooksPerTurn:    1,
		PeriodStart:        "2026-06-10",
		ApprovedBy:         "config:curiosity",
		ExpiresAt:          now.Add(12 * time.Hour),
	}, now)
	if err != nil {
		t.Fatalf("EnsureCuriosityLease(initial) err = %v", err)
	}
	if _, ok, err := store.ConsumeCuriosityLeaseTurn(initial.ID, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("ConsumeCuriosityLeaseTurn() ok=%v err=%v", ok, err)
	}

	narrowed, err := store.EnsureCuriosityLease(CuriosityLease{
		ID:                 leaseID,
		Status:             CuriosityLeaseStatusActive,
		Scope:              ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"},
		AllowedSourceKinds: []string{CuriositySourceWorkspace},
		AllowedSourceRefs:  []string{"README.md"},
		DailyTurnBudget:    3,
		MaxLooksPerTurn:    1,
		PeriodStart:        "2026-06-10",
		ApprovedBy:         "config:curiosity",
		ExpiresAt:          now.Add(12 * time.Hour),
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("EnsureCuriosityLease(narrowed) err = %v", err)
	}
	if narrowed.TurnsUsed != 1 {
		t.Fatalf("turns_used = %d, want preserved spend after envelope narrowing", narrowed.TurnsUsed)
	}
	if containsCuriosityTestString(narrowed.AllowedSourceKinds, CuriositySourceMemory) || containsCuriosityTestString(narrowed.AllowedSourceRefs, "memory/questions.md") {
		t.Fatalf("narrowed lease kept old authority envelope: %#v %#v", narrowed.AllowedSourceKinds, narrowed.AllowedSourceRefs)
	}
	if !containsCuriosityTestString(narrowed.AllowedSourceKinds, CuriositySourceWorkspace) || !containsCuriosityTestString(narrowed.AllowedSourceRefs, "README.md") {
		t.Fatalf("narrowed lease lost intended authority envelope: %#v %#v", narrowed.AllowedSourceKinds, narrowed.AllowedSourceRefs)
	}
}

func TestCuriosityLeaseConfigEditDoesNotReactivateExhaustedSpend(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	leaseID := CuriosityLeaseID("2026-06-10", nil, nil)
	lease, err := store.EnsureCuriosityLease(CuriosityLease{
		ID:                 leaseID,
		Status:             CuriosityLeaseStatusActive,
		Scope:              ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"},
		AllowedSourceKinds: []string{CuriositySourceWorkspace},
		AllowedSourceRefs:  []string{"README.md"},
		DailyTurnBudget:    1,
		MaxLooksPerTurn:    1,
		PeriodStart:        "2026-06-10",
		ApprovedBy:         "config:curiosity",
		ExpiresAt:          now.Add(12 * time.Hour),
	}, now)
	if err != nil {
		t.Fatalf("EnsureCuriosityLease(initial) err = %v", err)
	}
	if _, ok, err := store.ConsumeCuriosityLeaseTurn(lease.ID, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("ConsumeCuriosityLeaseTurn() ok=%v err=%v", ok, err)
	}

	edited, err := store.EnsureCuriosityLease(CuriosityLease{
		ID:                 leaseID,
		Status:             CuriosityLeaseStatusActive,
		Scope:              ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"},
		AllowedSourceKinds: []string{CuriositySourceWorkspace, CuriositySourceURL},
		AllowedSourceRefs:  []string{"README.md", "https://example.com/feed"},
		DailyTurnBudget:    1,
		MaxLooksPerTurn:    1,
		PeriodStart:        "2026-06-10",
		ApprovedBy:         "config:curiosity",
		ExpiresAt:          now.Add(12 * time.Hour),
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("EnsureCuriosityLease(edited) err = %v", err)
	}
	if edited.Status != CuriosityLeaseStatusExhausted || edited.TurnsUsed != 1 {
		t.Fatalf("edited lease = %#v, want exhausted spend preserved after config edit", edited)
	}
	if _, ok, err := store.ConsumeCuriosityLeaseTurn(leaseID, now.Add(3*time.Minute)); err != nil || ok {
		t.Fatalf("ConsumeCuriosityLeaseTurn(after edit) ok=%v err=%v, want exhausted", ok, err)
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

func TestStrandedCuriosityObservationsClearAfterPressureHandoff(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	key := SessionKey{ChatID: -2004, Scope: ScopeRef{Kind: ScopeKindCuriosity, ID: "admin-curiosity"}}
	obs, err := store.RecordCuriosityObservation(key, CuriosityObservationInput{
		LeaseID:     "lease-1",
		CandidateID: "candidate-1",
		SourceKind:  CuriositySourceWorkspace,
		SourceRef:   "README.md",
		SubjectKey:  "release-work",
		Summary:     "README still mentions the release checklist.",
		ContentHash: "sha256:abc",
		Confidence:  0.8,
		ObservedAt:  now,
	}, now)
	if err != nil {
		t.Fatalf("RecordCuriosityObservation() err = %v", err)
	}
	stranded, err := store.StrandedCuriosityObservations(10)
	if err != nil {
		t.Fatalf("StrandedCuriosityObservations() err = %v", err)
	}
	if len(stranded) != 1 || stranded[0].ID != obs.ID {
		t.Fatalf("stranded = %#v, want observation %d", stranded, obs.ID)
	}

	fingerprint := CuriosityPressureFingerprint(obs.LeaseID, obs.CandidateID, obs.ContentHash)
	_, err = store.RecordInteriorSignalObservations(SessionKey{ChatID: -2005, Scope: ScopeRef{Kind: ScopeKindHeartbeat, ID: "heartbeat"}}, []InteriorSignalObservationInput{{
		Category:          "semantic_recurrence",
		SubjectKey:        obs.SubjectKey,
		Summary:           obs.Summary,
		Source:            "curiosity",
		SourceFingerprint: fingerprint,
		Weight:            0.2,
		Confidence:        0.8,
		ObservedAt:        now,
	}}, now)
	if err != nil {
		t.Fatalf("RecordInteriorSignalObservations() err = %v", err)
	}
	stranded, err = store.StrandedCuriosityObservations(10)
	if err != nil {
		t.Fatalf("StrandedCuriosityObservations(after) err = %v", err)
	}
	if len(stranded) != 0 {
		t.Fatalf("stranded after pressure handoff = %#v, want none", stranded)
	}
}

func TestSafeCuriosityURLSourceRefRedactsQueryValues(t *testing.T) {
	raw := "https://Example.COM/feed/releases?token=secret-value&Topic=Release&token=other"
	ref := SafeCuriosityURLSourceRef(raw)
	for _, forbidden := range []string{"secret-value", "Release", "other", "Example.COM"} {
		if strings.Contains(ref, forbidden) {
			t.Fatalf("safe URL ref = %q, leaked %q", ref, forbidden)
		}
	}
	for _, want := range []string{"url:https://example.com/feed/releases", "query_keys=token,topic", "sha256:"} {
		if !strings.Contains(ref, want) {
			t.Fatalf("safe URL ref = %q, want %q", ref, want)
		}
	}
}

func TestSafeCuriosityURLSourceRefIgnoresQueryValueRotation(t *testing.T) {
	first := SafeCuriosityURLSourceRef("https://example.com/feed/releases?token=first-secret&topic=Release")
	second := SafeCuriosityURLSourceRef("https://example.com/feed/releases?topic=Other&token=second-secret")
	if first != second {
		t.Fatalf("safe URL refs differ after query value rotation:\nfirst:  %s\nsecond: %s", first, second)
	}
	for _, forbidden := range []string{"first-secret", "second-secret", "Release", "Other"} {
		if strings.Contains(first, forbidden) || strings.Contains(second, forbidden) {
			t.Fatalf("safe URL refs leaked rotated query value %q: %q / %q", forbidden, first, second)
		}
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

func containsCuriosityTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
