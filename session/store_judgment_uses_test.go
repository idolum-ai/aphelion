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
