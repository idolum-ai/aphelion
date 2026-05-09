//go:build linux

package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestLiveApprovalCardFixtureRendersAsHumanPlanProjection(t *testing.T) {
	t.Parallel()

	opState := session.OperationState{
		ID:        "repo-repair-plan",
		Objective: "Deliver a bounded repository repair.",
		PhasePlan: session.OperationPhasePlan{
			ID: "repo-repair-phases",
			Phases: []session.OperationPhase{
				{
					ID:             "phase-read",
					Summary:        "Inspect status and recent evidence",
					Status:         session.PlanStatusPending,
					AuthorityClass: "read_only_review",
					BoundedEffect:  "Read local non-secret status and report the evidence.",
				},
				{
					ID:             "phase-patch",
					Summary:        "Patch local rendering",
					Status:         session.PlanStatusPending,
					AuthorityClass: "workspace_write",
					BoundedEffect:  "Edit local rendering code and focused tests only.",
				},
			},
		},
	}
	lease, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC())
	if !ok {
		t.Fatal("operationPlanLeaseFromPhasePlan() ok = false, want synthesized plan budget")
	}
	opState.PlanLease = lease
	state := continuationStateFromOperationPlanLease(opState, lease, "continue", time.Now().UTC())
	text := renderOperationProposalMaterializedPromptFallback(state)

	for _, want := range []string{
		"Plan:",
		"Budget: up to 2 turns",
		"I'll do:",
		"Step 1: Inspect status and recent evidence",
		"Step 2: Patch local rendering",
		"Stops before:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval card = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{
		"Approval needed",
		"Lease:",
		"Operator card:",
		"Bundle phases:",
		"Use the buttons",
		"phase-read",
		"lease-",
		"aprop-",
	} {
		if strings.Contains(text, notWant) {
			t.Fatalf("approval card = %q, did not want protocol/internal fragment %q", text, notWant)
		}
	}
}

func TestBlockedApprovalFixturePointsToDebugWithoutApprovalRitual(t *testing.T) {
	t.Parallel()

	opState := session.OperationState{
		ID:        "profile-intake",
		Objective: "Prepare profile intake.",
		PhasePlan: session.OperationPhasePlan{Goal: "Consent-first profile intake."},
	}
	phase := session.OperationPhase{
		ID:      "phase-private-profile",
		Summary: "Collect approved profile preferences",
		WhyNow:  "Blocked until the resource owner opts in.",
	}
	text := renderOperationPhaseApprovalBlockedStatus(opState, phase, "waiting for explicit opt-in")
	for _, want := range []string{"Blocked: Collect approved profile preferences", "Why now:", "explicit opt-in", "Next:", "Details: /debug"} {
		if !strings.Contains(text, want) {
			t.Fatalf("blocked status = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{"Approval needed", "Approve", "Use the buttons", "Lease:"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("blocked status = %q, did not want %q", text, notWant)
		}
	}
}
