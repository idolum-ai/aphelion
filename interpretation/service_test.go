//go:build linux

package interpretation

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestServiceRecordsJudgmentAndUse(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	service := NewService(store)
	key := session.SessionKey{ChatID: 99101, UserID: 1001}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	judgment, use, err := service.RecordJudgmentAndUse(testJudgmentInput(key, now), session.JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test.consumer",
		Consequence:          session.JudgmentUseConsequenceDiagnostic,
		DependencyRefs:       []session.JudgmentDependencyRef{{Kind: "test_input", Ref: "one", Role: "qualifies"}},
		PolicyRef:            "test_policy_v1",
		ResultRef:            session.JudgmentUseRef("test_result", "one"),
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		Reason:               "test use",
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	if err != nil {
		t.Fatalf("RecordJudgmentAndUse() err = %v", err)
	}
	if judgment.ID == "" || use.ID == "" {
		t.Fatalf("judgment/use ids = %q/%q, want populated", judgment.ID, use.ID)
	}
	if len(use.JudgmentRefs) != 1 || use.JudgmentRefs[0] != session.JudgmentRef(judgment.ID) {
		t.Fatalf("use judgment refs = %#v, want recorded judgment ref", use.JudgmentRefs)
	}
}

func TestServiceRejectsInvalidCompletenessAndMissingGround(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	service := NewService(store)
	key := session.SessionKey{ChatID: 99102, UserID: 1001}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	completeWithUnknowns := testJudgmentInput(key, now)
	completeWithUnknowns.Unknowns = []session.UnknownPredicate{{Kind: "missing_target"}}
	if _, err := service.RecordJudgment(completeWithUnknowns); err == nil || !strings.Contains(err.Error(), "complete judgment") {
		t.Fatalf("RecordJudgment(complete unknowns) err = %v, want complete judgment rejection", err)
	}

	partialWithoutUnknowns := testJudgmentInput(key, now)
	partialWithoutUnknowns.Completeness = session.JudgmentCompletenessPartial
	if _, err := service.RecordJudgment(partialWithoutUnknowns); err == nil || !strings.Contains(err.Error(), "partial judgment") {
		t.Fatalf("RecordJudgment(partial no unknowns) err = %v, want partial judgment rejection", err)
	}

	missingDeps := testJudgmentInput(key, now)
	missingDeps.DependencyRefs = nil
	if _, err := service.RecordJudgment(missingDeps); err == nil || !strings.Contains(err.Error(), "dependency refs") {
		t.Fatalf("RecordJudgment(missing deps) err = %v, want dependency rejection", err)
	}

	if _, err := service.RecordUse(session.JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test.consumer",
		Consequence:          session.JudgmentUseConsequenceDiagnostic,
		JudgmentRefs:         []string{session.JudgmentUseRef("judgment", "j_test")},
		PolicyRef:            "test_policy_v1",
		ResultRef:            session.JudgmentUseRef("test_result", "missing-deps"),
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	}); err == nil || !strings.Contains(err.Error(), "dependency refs") {
		t.Fatalf("RecordUse(missing deps) err = %v, want dependency rejection", err)
	}
}

func TestServiceRecordsEffectAttemptWithUseAtomically(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	service := NewService(store)
	key := session.SessionKey{ChatID: 99103, UserID: 1001}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	judgment, err := service.RecordJudgment(testJudgmentInput(key, now))
	if err != nil {
		t.Fatalf("RecordJudgment() err = %v", err)
	}

	attempt, use, err := service.RecordEffectAttemptWithUse(session.EffectAttemptInput{
		AttemptID:    "eff_interpretation_service",
		Key:          key,
		Executor:     "test",
		Tool:         "exec",
		Command:      "git status",
		EffectKind:   "read_only_inspection",
		EffectReason: "test",
		SubjectJSON:  `{"kind":"test"}`,
		Status:       session.EffectAttemptStatusAttempted,
		StartedAt:    now,
		UpdatedAt:    now,
	}, session.JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test.exec.dispatch",
		Consequence:          session.JudgmentUseConsequenceExecution,
		JudgmentRefs:         []string{session.JudgmentRef(judgment.ID)},
		DependencyRefs:       []session.JudgmentDependencyRef{{Kind: "judgment", Ref: judgment.ID, Role: "qualifies"}},
		PolicyRef:            "test_exec_v1",
		ResultRef:            session.JudgmentUseRef("effect_attempt", "eff_interpretation_service"),
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		Reason:               "test effect use",
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	if err != nil {
		t.Fatalf("RecordEffectAttemptWithUse() err = %v", err)
	}
	if attempt.AttemptID != "eff_interpretation_service" || use.ResultRef != session.JudgmentUseRef("effect_attempt", attempt.AttemptID) {
		t.Fatalf("attempt/use = %#v/%#v, want linked effect attempt use", attempt, use)
	}
}

func TestServiceQualifiesIrreversibleUseWithDecorrelatedGround(t *testing.T) {
	t.Parallel()

	service := NewService(nil)
	challenged := session.JudgmentGroundProfile{
		DependencyRefs:     []session.JudgmentDependencyRef{{Kind: "command_hash", Ref: "one", Role: "subject"}},
		SourceFaultDomains: []string{"shell_text", "commandeffect_plan_v1"},
	}
	correlated := session.JudgmentGroundProfile{
		DependencyRefs:     []session.JudgmentDependencyRef{{Kind: "command_hash", Ref: "one", Role: "support"}},
		SourceFaultDomains: []string{"shell_text"},
	}
	if decision, err := service.QualifyDecorrelatedUse(DecorrelatedQualificationInput{
		Irreversible: true,
		Challenged:   challenged,
		Support:      correlated,
		Blocked:      "blocked",
	}); err == nil || decision.Status != session.JudgmentUseQualificationBlocked {
		t.Fatalf("QualifyDecorrelatedUse(correlated) = %#v, %v; want blocked", decision, err)
	}

	decorrelated := session.JudgmentGroundProfile{
		DependencyRefs:      []session.JudgmentDependencyRef{{Kind: "operator_decision", Ref: "approve-1", Role: "qualifies"}},
		SourceFaultDomains:  []string{"operator_approval_event"},
		ExternalEvidenceRef: session.JudgmentUseRef("operator_decision", "approve-1"),
	}
	decision, err := service.QualifyDecorrelatedUse(DecorrelatedQualificationInput{
		Irreversible: true,
		Challenged:   challenged,
		Support:      decorrelated,
		Qualified:    "qualified",
	})
	if err != nil {
		t.Fatalf("QualifyDecorrelatedUse(decorrelated) err = %v", err)
	}
	if decision.Status != session.JudgmentUseQualificationQualified || !decision.Decorrelated.Decorrelated {
		t.Fatalf("decision = %#v, want qualified decorrelated", decision)
	}
}

func testStore(t *testing.T) *session.SQLiteStore {
	t.Helper()
	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func testJudgmentInput(key session.SessionKey, now time.Time) session.JudgmentInput {
	return session.JudgmentInput{
		Key:                key,
		Kind:               "test_judgment",
		SchemaVersion:      "v1",
		SubjectKey:         "test:one",
		ClaimKey:           "test_claim",
		InterpreterID:      "interpretation.test",
		InterpreterVersion: "v1",
		InputRefs:          []string{session.JudgmentUseRef("test_input", "one")},
		InputHash:          "sha256:test",
		ResultJSON:         `{"ok":true}`,
		Completeness:       session.JudgmentCompletenessComplete,
		DependencyRefs:     []session.JudgmentDependencyRef{{Kind: "test_input", Ref: "one", Role: "subject"}},
		SourceFaultDomains: []string{"test_source"},
		Sensitivity:        "test_metadata",
		AsOf:               now,
		CreatedAt:          now,
	}
}
