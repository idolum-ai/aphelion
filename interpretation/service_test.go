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

	if _, err := service.RecordUse(session.JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test.consumer",
		Consequence:          session.JudgmentUseConsequenceDiagnostic,
		JudgmentRefs:         []string{session.JudgmentUseRef("judgment", "j_test")},
		DependencyRefs:       []session.JudgmentDependencyRef{{Kind: "test_input", Ref: "one", Role: "qualifies"}},
		ResultRef:            session.JudgmentUseRef("test_result", "missing-policy"),
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	}); err == nil || !strings.Contains(err.Error(), "policy_ref") {
		t.Fatalf("RecordUse(missing policy) err = %v, want policy_ref rejection", err)
	}

	if _, err := service.RecordUse(session.JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test.consumer",
		Consequence:          session.JudgmentUseConsequenceDiagnostic,
		JudgmentRefs:         []string{session.JudgmentUseRef("judgment", "j_test")},
		DependencyRefs:       []session.JudgmentDependencyRef{{Kind: "test_input", Ref: "one", Role: "qualifies"}},
		PolicyRef:            "test_policy_v1",
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	}); err == nil || !strings.Contains(err.Error(), "result_ref") {
		t.Fatalf("RecordUse(missing result) err = %v, want result_ref rejection", err)
	}
}

func TestServiceRejectsUnknownStateValues(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	service := NewService(store)
	key := session.SessionKey{ChatID: 99105, UserID: 1001}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	invalidJudgment := testJudgmentInput(key, now)
	invalidJudgment.Completeness = "confident"
	if _, err := service.RecordJudgment(invalidJudgment); err == nil || !strings.Contains(err.Error(), "invalid completeness") {
		t.Fatalf("RecordJudgment(invalid completeness) err = %v, want invalid completeness", err)
	}
	invalidJudgment = testJudgmentInput(key, now)
	invalidJudgment.Completeness = "com!plete"
	if _, err := service.RecordJudgment(invalidJudgment); err == nil || !strings.Contains(err.Error(), "invalid completeness") {
		t.Fatalf("RecordJudgment(punctuated completeness) err = %v, want invalid completeness", err)
	}
	invalidJudgment = testJudgmentInput(key, now)
	invalidJudgment.Completeness = "Complete"
	if _, err := service.RecordJudgment(invalidJudgment); err == nil || !strings.Contains(err.Error(), "invalid completeness") {
		t.Fatalf("RecordJudgment(mixed-case completeness) err = %v, want invalid completeness", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*session.JudgmentUseInput)
		want   string
	}{
		{
			name: "consequence",
			mutate: func(input *session.JudgmentUseInput) {
				input.Consequence = "mostly_harmless"
			},
			want: "invalid consequence",
		},
		{
			name: "qualification",
			mutate: func(input *session.JudgmentUseInput) {
				input.QualificationStatus = "probably_ok"
			},
			want: "invalid qualification_status",
		},
		{
			name: "reconciliation",
			mutate: func(input *session.JudgmentUseInput) {
				input.ReconciliationStatus = "laterish"
			},
			want: "invalid reconciliation_status",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			input := testUseInput(key, now)
			tc.mutate(&input)
			if _, err := service.RecordUse(input); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RecordUse(%s) err = %v, want %q", tc.name, err, tc.want)
			}
		})
	}

	if _, err := service.AppendChallengeEvent(session.JudgmentChallengeEventInput{
		ChallengeID:         "chal_invalid_state",
		JudgmentID:          "judg_invalid_state",
		Key:                 key,
		EventKind:           "kind_of_challenge",
		Disposition:         session.JudgmentChallengeUnresolved,
		EligibilityStatus:   session.JudgmentEligibilitySuspended,
		OperationalResponse: session.JudgmentOperationalResponseNone,
		CreatedAt:           now,
	}); err == nil || !strings.Contains(err.Error(), "invalid event_kind") {
		t.Fatalf("AppendChallengeEvent(invalid kind) err = %v, want invalid event_kind", err)
	}

	if err := service.MarkUsesForJudgmentReconciliation("judg_invalid_state", "maybe_pending", "test", now); err == nil || !strings.Contains(err.Error(), "invalid reconciliation_status") {
		t.Fatalf("MarkUsesForJudgmentReconciliation(invalid status) err = %v, want invalid reconciliation_status", err)
	}
}

func TestServiceNilStoreFailsClosedForDurableWrites(t *testing.T) {
	t.Parallel()

	service := NewService(nil)
	key := session.SessionKey{ChatID: 99104, UserID: 1001}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	want := "interpretation store unavailable"

	if _, err := service.RecordJudgment(testJudgmentInput(key, now)); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RecordJudgment(nil store) err = %v, want %q", err, want)
	}
	useInput := testUseInput(key, now)
	if _, err := service.RecordUse(useInput); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RecordUse(nil store) err = %v, want %q", err, want)
	}
	if _, _, err := service.RecordJudgmentAndUse(testJudgmentInput(key, now), useInput); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RecordJudgmentAndUse(nil store) err = %v, want %q", err, want)
	}
	if _, _, err := service.RecordEffectAttemptWithUse(session.EffectAttemptInput{}, useInput); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RecordEffectAttemptWithUse(nil store) err = %v, want %q", err, want)
	}
	if _, err := service.AppendChallengeEvent(session.JudgmentChallengeEventInput{}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("AppendChallengeEvent(nil store) err = %v, want %q", err, want)
	}
	if err := service.MarkUsesForJudgmentReconciliation("j", session.JudgmentUseReconciliationPending, "test", now); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("MarkUsesForJudgmentReconciliation(nil store) err = %v, want %q", err, want)
	}
	if _, err := service.JudgmentGroundProfile("j", 1); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("JudgmentGroundProfile(nil store) err = %v, want %q", err, want)
	}
}

func TestServiceRecordJudgmentAndUseIsAtomicAndBindsRefs(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	service := NewService(store)
	key := session.SessionKey{ChatID: 99106, UserID: 1001}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	judgmentInput := testJudgmentInput(key, now)
	badUse := testUseInput(key, now)
	badUse.PolicyRef = ""
	if _, _, err := service.RecordJudgmentAndUse(judgmentInput, badUse); err == nil || !strings.Contains(err.Error(), "policy_ref") {
		t.Fatalf("RecordJudgmentAndUse(invalid use) err = %v, want policy_ref rejection", err)
	}
	if judgments, err := store.JudgmentsBySession(key, 10); err != nil || len(judgments) != 0 {
		t.Fatalf("judgments after failed atomic write = %#v, %v; want none", judgments, err)
	}

	goodUse := testUseInput(key, now)
	goodUse.JudgmentRefs = []string{session.JudgmentUseRef("operation_state", "op-1")}
	judgment, use, err := service.RecordJudgmentAndUse(judgmentInput, goodUse)
	if err != nil {
		t.Fatalf("RecordJudgmentAndUse(valid) err = %v", err)
	}
	if !stringSliceContains(use.JudgmentRefs, session.JudgmentRef(judgment.ID)) {
		t.Fatalf("use judgment refs = %#v, want judgment ref %q", use.JudgmentRefs, session.JudgmentRef(judgment.ID))
	}
	if !stringSliceContains(use.JudgmentRefs, session.JudgmentUseRef("operation_state", "op-1")) {
		t.Fatalf("use judgment refs = %#v, want preserved operation state ref", use.JudgmentRefs)
	}

	mismatchedKeyUse := testUseInput(session.SessionKey{ChatID: 99160, UserID: 1001}, now)
	if _, _, err := service.RecordJudgmentAndUse(testJudgmentInput(key, now), mismatchedKeyUse); err == nil || !strings.Contains(err.Error(), "key does not match") {
		t.Fatalf("RecordJudgmentAndUse(mismatched key) err = %v, want key mismatch", err)
	}

	explicitIDUse := testUseInput(key, now)
	explicitIDUse.ID = "juse_precomputed"
	if _, _, err := service.RecordJudgmentAndUse(testJudgmentInput(key, now), explicitIDUse); err == nil || !strings.Contains(err.Error(), "service-managed") {
		t.Fatalf("RecordJudgmentAndUse(explicit use id) err = %v, want service-managed id rejection", err)
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

func TestServiceRejectsInconsistentEffectAttemptUse(t *testing.T) {
	t.Parallel()

	store := testStore(t)
	service := NewService(store)
	key := session.SessionKey{ChatID: 99107, UserID: 1001}
	now := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	judgment, err := service.RecordJudgment(testJudgmentInput(key, now))
	if err != nil {
		t.Fatalf("RecordJudgment() err = %v", err)
	}
	attemptInput := session.EffectAttemptInput{
		AttemptID:    "eff_interpretation_mismatch",
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
	}
	useInput := testUseInput(key, now)
	useInput.ConsumerID = "test.exec.dispatch"
	useInput.Consequence = session.JudgmentUseConsequenceExecution
	useInput.JudgmentRefs = []string{session.JudgmentRef(judgment.ID)}
	useInput.ResultRef = session.JudgmentUseRef("effect_attempt", "other")
	if _, _, err := service.RecordEffectAttemptWithUse(attemptInput, useInput); err == nil || !strings.Contains(err.Error(), "does not match effect attempt") {
		t.Fatalf("RecordEffectAttemptWithUse(wrong result ref) err = %v, want result ref mismatch", err)
	}

	useInput = testUseInput(key, now)
	useInput.ID = "juse_precomputed"
	useInput.ConsumerID = "test.exec.dispatch"
	useInput.Consequence = session.JudgmentUseConsequenceExecution
	useInput.JudgmentRefs = []string{session.JudgmentRef(judgment.ID)}
	if _, _, err := service.RecordEffectAttemptWithUse(attemptInput, useInput); err == nil || !strings.Contains(err.Error(), "service-managed") {
		t.Fatalf("RecordEffectAttemptWithUse(explicit use id) err = %v, want service-managed id rejection", err)
	}

	useInput = testUseInput(key, now)
	useInput.ConsumerID = "test.exec.dispatch"
	useInput.Consequence = session.JudgmentUseConsequenceExecution
	useInput.JudgmentRefs = []string{session.JudgmentRef(judgment.ID)}
	useInput.ResultRef = ""
	useInput.SessionID = "telegram_dm:999/user:1001"
	if _, _, err := service.RecordEffectAttemptWithUse(attemptInput, useInput); err == nil || !strings.Contains(err.Error(), "does not match effect attempt session_id") {
		t.Fatalf("RecordEffectAttemptWithUse(wrong session) err = %v, want session mismatch", err)
	}

	attemptInput.Status = "mystery"
	useInput = testUseInput(key, now)
	useInput.ConsumerID = "test.exec.dispatch"
	useInput.Consequence = session.JudgmentUseConsequenceExecution
	useInput.JudgmentRefs = []string{session.JudgmentRef(judgment.ID)}
	if _, _, err := service.RecordEffectAttemptWithUse(attemptInput, useInput); err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("RecordEffectAttemptWithUse(invalid status) err = %v, want invalid status", err)
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

func testUseInput(key session.SessionKey, now time.Time) session.JudgmentUseInput {
	return session.JudgmentUseInput{
		Key:                  key,
		ConsumerID:           "test.consumer",
		Consequence:          session.JudgmentUseConsequenceDiagnostic,
		JudgmentRefs:         []string{session.JudgmentUseRef("judgment", "j_test")},
		DependencyRefs:       []session.JudgmentDependencyRef{{Kind: "test_input", Ref: "one", Role: "qualifies"}},
		PolicyRef:            "test_policy_v1",
		ResultRef:            session.JudgmentUseRef("test_result", "one"),
		QualificationStatus:  session.JudgmentUseQualificationQualified,
		ReconciliationStatus: session.JudgmentUseReconciliationNotRequired,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
