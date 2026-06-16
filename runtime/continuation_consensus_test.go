//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestSummarizeContinuationPlanDoesNotMixCompletedOperationObjectiveWithLivePlan(t *testing.T) {
	t.Parallel()

	plan := session.PlanState{
		Explanation: "Generate a compact Spanish PDF report for the Imexx reverse-engineering work.",
		Steps: []session.PlanStep{
			{Step: "Inspect the key Imexx report sources and available PDF tooling", Status: session.PlanStatusCompleted},
			{Step: "Draft the Spanish executive report and generate a PDF artifact", Status: session.PlanStatusInProgress},
			{Step: "Validate the generated PDF and deliver it", Status: session.PlanStatusPending},
		},
	}
	completedOperation := session.OperationState{
		ID:        "aphelion-pr-219-review",
		Objective: "Record PR #219 closure and operator-reported redeploy after merge.",
		Status:    session.OperationStatusCompleted,
		Stage:     "closed",
		Summary:   "PR #219 was already reviewed and closed; it is background evidence for this session.",
	}

	objective, nextStep := summarizeContinuationPlan(plan, completedOperation, "go for it")
	if strings.Contains(objective, "PR #219") {
		t.Fatalf("objective = %q, want live plan/current request over completed operation objective", objective)
	}
	if !strings.Contains(objective, "Imexx") || !strings.Contains(objective, "PDF") {
		t.Fatalf("objective = %q, want live Imexx PDF objective", objective)
	}
	if nextStep != "Draft the Spanish executive report and generate a PDF artifact" {
		t.Fatalf("nextStep = %q, want live in-progress plan step", nextStep)
	}
}

func TestContinuationConsensusKeepsLivePlanWorkWhenOperationIsCompleted(t *testing.T) {
	t.Parallel()

	consensus := continuationConsensus{
		PlanState: session.PlanState{
			Steps: []session.PlanStep{
				{Step: "Draft the Spanish executive report and generate a PDF artifact", Status: session.PlanStatusInProgress},
			},
		},
		OperationState: session.OperationState{
			ID:        "aphelion-pr-219-review",
			Objective: "Record PR #219 closure and operator-reported redeploy after merge.",
			Status:    session.OperationStatusCompleted,
		},
	}

	if !continuationConsensusHasTypedRemainingWork(consensus) {
		t.Fatal("continuationConsensusHasTypedRemainingWork() = false, want active plan work to outrank terminal operation background")
	}
	if continuationConsensusShouldCloseQuietly(consensus) {
		t.Fatal("continuationConsensusShouldCloseQuietly() = true, want visible live plan handling")
	}
}
