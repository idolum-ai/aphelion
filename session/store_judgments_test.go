//go:build linux

package session

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordJudgmentIsImmutableAndChallengeEventsAppend(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	key := SessionKey{ChatID: 99174, UserID: 1001}
	now := time.Now().UTC()
	input := JudgmentInput{
		Key:                key,
		Kind:               "shell_effect_plan",
		SchemaVersion:      "v1",
		SubjectKey:         "exec:sha256:test",
		ClaimKey:           "command_effect_plan",
		InterpreterID:      "commandeffect.plan_command",
		InterpreterVersion: "v1",
		InputRefs:          []string{JudgmentUseRef("command_hash", "sha256:test")},
		InputHash:          "sha256:test",
		ResultJSON:         `{"effects":[{"kind":"workspace_mutation"}]}`,
		Completeness:       JudgmentCompletenessComplete,
		DependencyRefs:     []JudgmentDependencyRef{{Kind: "command_hash", Ref: "sha256:test", Role: "subject"}},
		SourceFaultDomains: []string{"shell_text", "commandeffect_plan_v1"},
		Sensitivity:        "redacted_command_metadata",
		AsOf:               now,
		CreatedAt:          now,
	}
	first, err := store.RecordJudgment(input)
	if err != nil {
		t.Fatalf("RecordJudgment() err = %v", err)
	}
	replayed, err := store.RecordJudgment(input)
	if err != nil {
		t.Fatalf("RecordJudgment(replay) err = %v", err)
	}
	if replayed.ID != first.ID || replayed.ContentHash != first.ContentHash {
		t.Fatalf("replayed judgment = %#v, want same immutable judgment %#v", replayed, first)
	}

	conflicting := input
	conflicting.ID = first.ID
	conflicting.ResultJSON = `{"effects":[{"kind":"external_account_command"}]}`
	if _, err := store.RecordJudgment(conflicting); err == nil || !strings.Contains(err.Error(), "immutable commitment mismatch") {
		t.Fatalf("RecordJudgment(conflict) err = %v, want immutable mismatch", err)
	}

	opened, err := store.AppendJudgmentChallengeEvent(JudgmentChallengeEventInput{
		Key:                 key,
		JudgmentID:          first.ID,
		EventKind:           JudgmentChallengeOpened,
		GroundRefs:          []JudgmentDependencyRef{{Kind: "effect_attempt", Ref: "eff_test", Role: "contradicts"}},
		Disposition:         JudgmentChallengeUnresolved,
		EligibilityStatus:   JudgmentEligibilitySuspended,
		OperationalResponse: JudgmentOperationalResponseVerify,
		Reason:              "effect attempt contradicts shell plan",
		CreatedAt:           now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("AppendJudgmentChallengeEvent(opened) err = %v", err)
	}
	if opened.ChallengeID == "" {
		t.Fatalf("opened challenge event = %#v, want challenge id", opened)
	}
	if _, err := store.AppendJudgmentChallengeEvent(JudgmentChallengeEventInput{
		Key:                 key,
		ChallengeID:         opened.ChallengeID,
		JudgmentID:          first.ID,
		EventKind:           JudgmentChallengeAdjudicationRecorded,
		GroundRefs:          []JudgmentDependencyRef{{Kind: "effect_attempt", Ref: "eff_test", Role: "contradicts"}},
		Disposition:         JudgmentChallengeContradicted,
		EligibilityStatus:   JudgmentEligibilitySuspended,
		OperationalResponse: JudgmentOperationalResponseVerify,
		Reason:              "decorrelated effect evidence wins",
		CreatedAt:           now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("AppendJudgmentChallengeEvent(adjudication) err = %v", err)
	}
	events, err := store.JudgmentChallengeEvents(first.ID, 10)
	if err != nil {
		t.Fatalf("JudgmentChallengeEvents() err = %v", err)
	}
	if len(events) != 2 || events[0].EventKind != JudgmentChallengeOpened || events[1].Disposition != JudgmentChallengeContradicted {
		t.Fatalf("events = %#v, want opened then contradicted adjudication", events)
	}

	use, err := store.RecordJudgmentUseCommitment(JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test.consumer",
		Consequence:          JudgmentUseConsequenceDiagnostic,
		JudgmentRefs:         []string{JudgmentRef(first.ID)},
		DependencyRefs:       []JudgmentDependencyRef{{Kind: "judgment", Ref: first.ID, Role: "qualifies"}},
		PolicyRef:            "test_policy_v1",
		ResultRef:            JudgmentUseRef("test_result", "one"),
		QualificationStatus:  JudgmentUseQualificationQualified,
		ReconciliationStatus: JudgmentUseReconciliationNotRequired,
		Reason:               "test use",
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	if err != nil {
		t.Fatalf("RecordJudgmentUseCommitment() err = %v", err)
	}
	if err := store.MarkJudgmentUsesForJudgmentReconciliation(first.ID, JudgmentUseReconciliationPending, "challenge contradicted judgment", now.Add(3*time.Second)); err != nil {
		t.Fatalf("MarkJudgmentUsesForJudgmentReconciliation() err = %v", err)
	}
	uses, err := store.JudgmentUsesByJudgmentRef(first.ID, 10)
	if err != nil {
		t.Fatalf("JudgmentUsesByJudgmentRef() err = %v", err)
	}
	if len(uses) != 1 || uses[0].ID != use.ID || uses[0].ReconciliationStatus != JudgmentUseReconciliationPending {
		t.Fatalf("uses = %#v, want challenged use marked pending", uses)
	}
}
