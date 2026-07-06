//go:build linux

package session

import (
	"strings"
	"testing"
	"time"
)

func TestJudgmentUseCommitmentIsImmutableAndQueryable(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7101, UserID: 1001}
	now := time.Now().UTC()
	input := JudgmentUseInput{
		Key:         key,
		ConsumerID:  "test.consumer",
		Consequence: JudgmentUseConsequenceExecution,
		JudgmentRefs: []string{
			JudgmentUseHashRef("effect_plan", "git commit -m test"),
		},
		DependencyRefs: []JudgmentDependencyRef{
			{Kind: "command_hash", Ref: EffectAttemptCommandHash("git commit -m test"), Role: "subject"},
		},
		PolicyRef:            "test_policy_v1",
		ResultRef:            JudgmentUseRef("effect_attempt", "eff-test"),
		Irreversible:         true,
		QualificationStatus:  JudgmentUseQualificationQualified,
		ReconciliationStatus: JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	first, err := store.RecordJudgmentUseCommitment(input)
	if err != nil {
		t.Fatalf("RecordJudgmentUseCommitment(first) err = %v", err)
	}
	input.Reason = "same commitment replay"
	second, err := store.RecordJudgmentUseCommitment(input)
	if err != nil {
		t.Fatalf("RecordJudgmentUseCommitment(replay) err = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("judgment use id changed from %q to %q", first.ID, second.ID)
	}
	input.ID = first.ID
	input.PolicyRef = "different_policy_v2"
	if _, err := store.RecordJudgmentUseCommitment(input); err == nil || !strings.Contains(err.Error(), "immutable commitment mismatch") {
		t.Fatalf("RecordJudgmentUseCommitment(mutated) err = %v, want immutable mismatch", err)
	}
	if err := store.MarkJudgmentUsesForResultRefReconciliation(JudgmentUseRef("effect_attempt", "eff-test"), JudgmentUseReconciliationPending, "verification required", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkJudgmentUsesForResultRefReconciliation() err = %v", err)
	}
	uses, err := store.JudgmentUsesByResultRef(JudgmentUseRef("effect_attempt", "eff-test"), 10)
	if err != nil {
		t.Fatalf("JudgmentUsesByResultRef() err = %v", err)
	}
	if len(uses) != 1 || uses[0].ID != first.ID {
		t.Fatalf("uses = %#v, want single upserted record", uses)
	}
	if uses[0].ReconciliationStatus != JudgmentUseReconciliationPending {
		t.Fatalf("reconciliation status = %q, want pending", uses[0].ReconciliationStatus)
	}
}

func TestEffectAttemptWithJudgmentUseIsAtomicAndReconcilesStatus(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7102, UserID: 1001}
	now := time.Now().UTC()
	attemptInput := EffectAttemptInput{
		AttemptID:  "eff-judgment-use",
		Key:        key,
		Executor:   "tool",
		Tool:       "exec",
		Command:    "git push origin main",
		EffectKind: "git_push",
		Status:     EffectAttemptStatusAttempted,
		StartedAt:  now,
		UpdatedAt:  now,
	}
	useInput := JudgmentUseInput{
		Key:         key,
		ConsumerID:  "test.exec",
		Consequence: JudgmentUseConsequenceExecution,
		JudgmentRefs: []string{
			JudgmentUseHashRef("effect_plan", "git push origin main"),
		},
		PolicyRef:            "test_policy_v1",
		Irreversible:         true,
		QualificationStatus:  JudgmentUseQualificationQualified,
		ReconciliationStatus: JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	attempt, use, err := store.UpsertEffectAttemptWithJudgmentUse(attemptInput, useInput)
	if err != nil {
		t.Fatalf("UpsertEffectAttemptWithJudgmentUse() err = %v", err)
	}
	if attempt.AttemptID != "eff-judgment-use" || use.ResultRef != JudgmentUseRef("effect_attempt", "eff-judgment-use") {
		t.Fatalf("attempt/use = %#v %#v, want linked result ref", attempt, use)
	}
	attemptInput.Status = EffectAttemptStatusUncertain
	attemptInput.ErrorText = "remote timed out after dispatch"
	attemptInput.UpdatedAt = now.Add(time.Second)
	if _, err := store.UpsertEffectAttempt(attemptInput); err != nil {
		t.Fatalf("UpsertEffectAttempt(uncertain) err = %v", err)
	}
	uses, err := store.JudgmentUsesByResultRef(JudgmentUseRef("effect_attempt", "eff-judgment-use"), 10)
	if err != nil {
		t.Fatalf("JudgmentUsesByResultRef() err = %v", err)
	}
	if len(uses) != 1 || uses[0].ReconciliationStatus != JudgmentUseReconciliationPending {
		t.Fatalf("uses = %#v, want pending reconciliation after uncertain attempt", uses)
	}
	if !strings.Contains(uses[0].Reason, "uncertain") {
		t.Fatalf("reason = %q, want uncertain marker", uses[0].Reason)
	}
}

func TestRecordJudgmentWithUseIsAtomicAndBindsJudgmentRef(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7108, UserID: 1001}
	now := time.Now().UTC()
	judgmentInput := JudgmentInput{
		Key:                key,
		Kind:               "test_judgment",
		SchemaVersion:      "v1",
		SubjectKey:         "test:atomic",
		ClaimKey:           "test_claim",
		InterpreterID:      "session.test",
		InterpreterVersion: "v1",
		InputRefs:          []string{JudgmentUseRef("input", "one")},
		ResultJSON:         `{"ok":true}`,
		Completeness:       JudgmentCompletenessComplete,
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "input", Ref: "one", Role: "subject"}},
		SourceFaultDomains: []string{"test"},
		CreatedAt:          now,
		AsOf:               now,
	}
	badUse := JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "",
		Consequence:          JudgmentUseConsequenceExecution,
		DependencyRefs:       []JudgmentDependencyRef{{Kind: "input", Ref: "one", Role: "qualifies"}},
		PolicyRef:            "test_policy_v1",
		ResultRef:            JudgmentUseRef("result", "bad"),
		QualificationStatus:  JudgmentUseQualificationQualified,
		ReconciliationStatus: JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if _, _, err := store.RecordJudgmentWithUse(judgmentInput, badUse); err == nil || !strings.Contains(err.Error(), "requires consumer_id") {
		t.Fatalf("RecordJudgmentWithUse(invalid use) err = %v, want consumer_id rejection", err)
	}
	if judgments, err := store.JudgmentsBySession(key, 10); err != nil || len(judgments) != 0 {
		t.Fatalf("judgments after failed RecordJudgmentWithUse = %#v, %v; want none", judgments, err)
	}

	goodUse := badUse
	goodUse.ConsumerID = "test.consumer"
	goodUse.ResultRef = JudgmentUseRef("result", "one")
	judgment, use, err := store.RecordJudgmentWithUse(judgmentInput, goodUse)
	if err != nil {
		t.Fatalf("RecordJudgmentWithUse(valid) err = %v", err)
	}
	if !stringListContains(use.JudgmentRefs, JudgmentRef(judgment.ID)) {
		t.Fatalf("use refs = %#v, want judgment ref %q", use.JudgmentRefs, JudgmentRef(judgment.ID))
	}
}

func TestJudgmentUsesByJudgmentRefUsesExactJSONMembership(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7103, UserID: 1001}
	now := time.Now().UTC()
	exact := recordTestJudgmentUse(t, store, key, "exact", JudgmentRef("abc"), now)
	prefix := recordTestJudgmentUse(t, store, key, "prefix", JudgmentRef("abc2"), now)
	percent := recordTestJudgmentUse(t, store, key, "percent", JudgmentRef("abc%"), now)
	percentLookalike := recordTestJudgmentUse(t, store, key, "percent-lookalike", JudgmentRef("abcZZ"), now)
	underscore := recordTestJudgmentUse(t, store, key, "underscore", JudgmentRef("slot_1"), now)
	underscoreLookalike := recordTestJudgmentUse(t, store, key, "underscore-lookalike", JudgmentRef("slotA1"), now)

	assertJudgmentUseQueryIDs(t, store, "abc", []string{exact.ID})
	assertJudgmentUseQueryIDs(t, store, "abc2", []string{prefix.ID})
	assertJudgmentUseQueryIDs(t, store, "abc%", []string{percent.ID})
	assertJudgmentUseQueryIDs(t, store, "abcZZ", []string{percentLookalike.ID})
	assertJudgmentUseQueryIDs(t, store, "slot_1", []string{underscore.ID})
	assertJudgmentUseQueryIDs(t, store, "slotA1", []string{underscoreLookalike.ID})
}

func TestMarkJudgmentUsesForJudgmentReconciliationUsesExactJSONMembership(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 7104, UserID: 1001}
	now := time.Now().UTC()
	exact := recordTestJudgmentUse(t, store, key, "exact", JudgmentRef("abc"), now)
	prefix := recordTestJudgmentUse(t, store, key, "prefix", JudgmentRef("abc2"), now)
	percent := recordTestJudgmentUse(t, store, key, "percent", JudgmentRef("abc%"), now)
	percentLookalike := recordTestJudgmentUse(t, store, key, "percent-lookalike", JudgmentRef("abcZZ"), now)
	underscore := recordTestJudgmentUse(t, store, key, "underscore", JudgmentRef("slot_1"), now)
	underscoreLookalike := recordTestJudgmentUse(t, store, key, "underscore-lookalike", JudgmentRef("slotA1"), now)

	if err := store.MarkJudgmentUsesForJudgmentReconciliation("abc", JudgmentUseReconciliationPending, "exact abc", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkJudgmentUsesForJudgmentReconciliation(abc) err = %v", err)
	}
	assertJudgmentUseReconciliationStatus(t, store, key, map[string]JudgmentUseReconciliationStatus{
		exact.ID:               JudgmentUseReconciliationPending,
		prefix.ID:              JudgmentUseReconciliationNotRequired,
		percent.ID:             JudgmentUseReconciliationNotRequired,
		percentLookalike.ID:    JudgmentUseReconciliationNotRequired,
		underscore.ID:          JudgmentUseReconciliationNotRequired,
		underscoreLookalike.ID: JudgmentUseReconciliationNotRequired,
	})

	if err := store.MarkJudgmentUsesForJudgmentReconciliation("abc%", JudgmentUseReconciliationPending, "exact percent", now.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkJudgmentUsesForJudgmentReconciliation(abc%%) err = %v", err)
	}
	if err := store.MarkJudgmentUsesForJudgmentReconciliation("slot_1", JudgmentUseReconciliationPending, "exact underscore", now.Add(3*time.Second)); err != nil {
		t.Fatalf("MarkJudgmentUsesForJudgmentReconciliation(slot_1) err = %v", err)
	}
	assertJudgmentUseReconciliationStatus(t, store, key, map[string]JudgmentUseReconciliationStatus{
		exact.ID:               JudgmentUseReconciliationPending,
		prefix.ID:              JudgmentUseReconciliationNotRequired,
		percent.ID:             JudgmentUseReconciliationPending,
		percentLookalike.ID:    JudgmentUseReconciliationNotRequired,
		underscore.ID:          JudgmentUseReconciliationPending,
		underscoreLookalike.ID: JudgmentUseReconciliationNotRequired,
	})
}

func recordTestJudgmentUse(t *testing.T, store *SQLiteStore, key SessionKey, suffix string, judgmentRef string, now time.Time) JudgmentUse {
	t.Helper()
	use, err := store.RecordJudgmentUseCommitment(JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test." + suffix,
		Consequence:          JudgmentUseConsequenceDiagnostic,
		JudgmentRefs:         []string{judgmentRef},
		PolicyRef:            "test_policy_v1",
		ResultRef:            JudgmentUseRef("test_result", suffix),
		QualificationStatus:  JudgmentUseQualificationQualified,
		ReconciliationStatus: JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	if err != nil {
		t.Fatalf("RecordJudgmentUseCommitment(%s) err = %v", suffix, err)
	}
	return use
}

func assertJudgmentUseQueryIDs(t *testing.T, store *SQLiteStore, judgmentID string, want []string) {
	t.Helper()
	uses, err := store.JudgmentUsesByJudgmentRef(judgmentID, 20)
	if err != nil {
		t.Fatalf("JudgmentUsesByJudgmentRef(%q) err = %v", judgmentID, err)
	}
	got := make(map[string]struct{}, len(uses))
	for _, use := range uses {
		got[use.ID] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("JudgmentUsesByJudgmentRef(%q) ids = %#v, want %#v", judgmentID, got, want)
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Fatalf("JudgmentUsesByJudgmentRef(%q) ids = %#v, missing %q", judgmentID, got, id)
		}
	}
}

func assertJudgmentUseReconciliationStatus(t *testing.T, store *SQLiteStore, key SessionKey, want map[string]JudgmentUseReconciliationStatus) {
	t.Helper()
	uses, err := store.JudgmentUsesBySession(key, 20)
	if err != nil {
		t.Fatalf("JudgmentUsesBySession() err = %v", err)
	}
	got := make(map[string]JudgmentUseReconciliationStatus, len(uses))
	for _, use := range uses {
		got[use.ID] = use.ReconciliationStatus
	}
	for id, status := range want {
		if got[id] != status {
			t.Fatalf("use %s reconciliation status = %q, want %q (all statuses %#v)", id, got[id], status, got)
		}
	}
}

func stringListContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
