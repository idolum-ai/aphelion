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

func TestBlockedApprovalFixtureRendersHumanStatusWithoutApprovalRitual(t *testing.T) {
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
	for _, want := range []string{"I can't continue that step yet.", "Plan: Collect approved profile preferences", "Reason:", "has not opted in", "Next:", "Use /status"} {
		if !strings.Contains(text, want) {
			t.Fatalf("blocked status = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{"Approval needed", "Approve", "Use the buttons", "Lease:", "Details: /debug", "phase-private-profile"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("blocked status = %q, did not want %q", text, notWant)
		}
	}
}

func TestApprovedContinuationEventProjectionHidesLedgerInternals(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "phase-4b-rebundled-email-proof",
		Objective:      "Run a bounded email adapter proof.",
		StageSummary:   "Bundled Phase 4B: one bounded idolum-email read-only adapter proof.",
		RemainingTurns: 2,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-phase-4b-rebundled-email-proof",
			OperationID:      "phase-4b-rebundled-email-proof",
			Summary:          "Bundled Phase 4B: one bounded idolum-email read-only adapter proof.",
			BoundedEffect:    "Inspect adapter state and run one read-only proof.",
			RiskClass:        "external_account_email_read_public_web_read",
			ForbiddenActions: []string{"credentials_or_tokens", "external_send_or_contact", "deploy_restart_without_explicit_approval"},
		},
		ContinuationLease: session.ContinuationLease{
			ID:         "lease-phase-4b-rebundled-email-proof",
			ProposalID: "aprop-phase-4b-rebundled-email-proof",
			Status:     session.ContinuationLeaseStatusActive,
		},
	}
	text := approvedContinuationEventTextForState(state)
	for _, want := range []string{"Approved work:", "Next: Bundled Phase 4B", "Scope: Inspect adapter state", "Budget: up to 2 turns", "Stops before:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("approved continuation text = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{"proposal_id:", "operation_id:", "lease_id:", "risk_class:", "aprop-", "lease-phase-4b", "external_account_email_read_public_web_read"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("approved continuation text = %q, did not want internal fragment %q", text, notWant)
		}
	}
}

func TestSynthesizedPlanBudgetUsesMilestoneEvidenceAndHardStops(t *testing.T) {
	t.Parallel()

	opState := session.OperationState{
		ID:        "job-search-repair",
		Objective: "Repair the job-search flow.",
		PhasePlan: session.OperationPhasePlan{
			ID: "job-search-repair-plan",
			Phases: []session.OperationPhase{
				{ID: "phase-read", Summary: "Inspect mailbox readiness", Status: session.PlanStatusPending, AuthorityClass: "read_only_review", BoundedEffect: "Inspect non-secret readiness state."},
				{ID: "phase-patch", Summary: "Patch projection code", Status: session.PlanStatusPending, AuthorityClass: "workspace_write", BoundedEffect: "Edit local runtime projection code."},
				{ID: "phase-test", Summary: "Run focused validation", Status: session.PlanStatusPending, AuthorityClass: "workspace_write", BoundedEffect: "Run focused tests and report evidence."},
			},
		},
	}
	lease, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC())
	if !ok {
		t.Fatal("operationPlanLeaseFromPhasePlan() ok = false, want bounded plan budget")
	}
	for _, want := range []string{"report_milestone_evidence", "credentials_or_tokens", "external_send_or_contact", "archive_delete_or_mutate_source_data", "deploy_restart_without_explicit_approval"} {
		combined := strings.Join(append(append([]string{}, lease.AllowedActions...), lease.ForbiddenActions...), "\n")
		if !strings.Contains(combined, want) {
			t.Fatalf("lease actions = %#v/%#v, want %q", lease.AllowedActions, lease.ForbiddenActions, want)
		}
	}
	if strings.Contains(strings.Join(lease.ValidationGates, "\n"), "after each turn") {
		t.Fatalf("validation gates = %#v, want milestone evidence rather than per-turn ceremony", lease.ValidationGates)
	}
	state := continuationStateFromOperationPlanLease(opState, lease, "continue", time.Now().UTC())
	card := renderOperationProposalMaterializedPromptFallback(state)
	for _, want := range []string{"Stops before:", "credentials/tokens", "external send/contact", "archive/delete", "deploy/restart"} {
		if !strings.Contains(card, want) {
			t.Fatalf("plan budget card = %q, want %q", card, want)
		}
	}
}
