//go:build linux

package session

import (
	"strings"
	"testing"
	"time"
)

func TestJudgmentUseUpsertIsIdempotentAndQueryable(t *testing.T) {
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
	first, err := store.UpsertJudgmentUse(input)
	if err != nil {
		t.Fatalf("UpsertJudgmentUse(first) err = %v", err)
	}
	input.ReconciliationStatus = JudgmentUseReconciliationPending
	input.Reason = "verification required"
	second, err := store.UpsertJudgmentUse(input)
	if err != nil {
		t.Fatalf("UpsertJudgmentUse(second) err = %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("judgment use id changed from %q to %q", first.ID, second.ID)
	}
	if second.ReconciliationStatus != JudgmentUseReconciliationPending {
		t.Fatalf("reconciliation status = %q, want pending", second.ReconciliationStatus)
	}
	uses, err := store.JudgmentUsesByResultRef(JudgmentUseRef("effect_attempt", "eff-test"), 10)
	if err != nil {
		t.Fatalf("JudgmentUsesByResultRef() err = %v", err)
	}
	if len(uses) != 1 || uses[0].ID != first.ID {
		t.Fatalf("uses = %#v, want single upserted record", uses)
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
