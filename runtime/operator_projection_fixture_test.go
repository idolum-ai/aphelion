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
		"Approve plan:\nInspect status and recent evidence",
		"Budget:\nup to 2 turns",
		"First step:\nInspect status and recent evidence",
		"Covers:\n- Step 1: Inspect status and recent evidence",
		"Stops before:\n- anything outside scope",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval card = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{
		"Approval needed",
		"I'll do:",
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

func TestApprovalPromptRendersAsTelegramReadableDecisionCard(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		Objective:      "Implement compact operator surface rendering.",
		StageSummary:   "Commit validated rendering updates",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-compact-rendering",
			Summary:          "Commit the validated rendering updates, push branch, and open a pull request",
			BoundedEffect:    "Create one commit, push the branch, and open the PR.",
			ForbiddenActions: []string{"merge", "deploy", "restart", "credential_token_output"},
		},
	}

	text := renderOperationProposalMaterializedPromptFallback(state)
	for _, want := range []string{
		"Approve:\nCommit the validated rendering updates, push branch, and open a pull request",
		"Budget:\nup to 1 turn",
		"Scope:\nCreate one commit, push the branch, and open the PR",
		"Stops before:\n- deploy/restart\n- credentials/tokens\n- merge",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("approval card = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{"Approval needed", "Status:", "Why now:", "Details:", "aprop-compact-rendering"} {
		if strings.Contains(text, notWant) {
			t.Fatalf("approval card = %q, did not want scaffold/internal fragment %q", text, notWant)
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
	for _, want := range []string{"I can't continue “Collect approved profile preferences” yet", "has not opted in", "Use /status"} {
		if !strings.Contains(text, want) {
			t.Fatalf("blocked status = %q, want %q", text, want)
		}
	}
	for _, notWant := range []string{"Approval needed", "Approve", "Use the buttons", "Lease:", "Details: /health trace", "phase-private-profile"} {
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
		StageSummary:   "Bundled Phase 4B: one bounded mail-child read-only adapter proof.",
		RemainingTurns: 2,
		ActionProposal: session.ActionProposal{
			ID:               "aprop-phase-4b-rebundled-email-proof",
			OperationID:      "phase-4b-rebundled-email-proof",
			Summary:          "Bundled Phase 4B: one bounded mail-child read-only adapter proof.",
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
	for _, want := range []string{"Approved work:", "Next:\nBundled Phase 4B", "Scope:\nInspect adapter state", "Budget:\nup to 2 turns", "Stops before:"} {
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
	for _, want := range []string{"Stops before", "credentials/tokens", "external send/contact", "archive/delete", "deploy/restart"} {
		if !strings.Contains(card, want) {
			t.Fatalf("plan budget card = %q, want %q", card, want)
		}
	}
}

func TestDeployApprovalProjectionDoesNotRenderBroadDeployRestartStop(t *testing.T) {
	t.Parallel()

	autoApprove := false
	state := session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		Objective:      "Deploy the pushed runtime repair.",
		StageSummary:   "Build/install, restart Aphelion, and verify the pushed repair live.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			Summary:       "Build/install, restart Aphelion, and verify the pushed repair live.",
			BoundedEffect: "Build/install via existing deploy path, restart Aphelion, inspect service status/recent logs, verify deployed repair evidence.",
			RiskClass:     "deploy",
			AllowedActions: []string{
				"make_build",
				"install_user_service",
				"restart_aphelion_service",
				"run_verify_deploy",
			},
			ForbiddenActions: []string{
				"deploy_without_handoff",
				"restart_without_recovery_artifact",
				"skip_build_or_tests_before_restart",
				"skip_post_deploy_verification",
				"unbounded_restart_loop",
				"credentials_or_tokens",
				"archive_delete_or_mutate_source_data",
			},
			AutoApproveEligible: &autoApprove,
		},
		ContinuationLease: session.ContinuationLease{
			Status:     session.ContinuationLeaseStatusActive,
			LeaseClass: session.ContinuationLeaseClassDeployRestart,
			MaxTurns:   1,
			AllowedActions: []string{
				"make_build",
				"install_user_service",
				"restart_aphelion_service",
				"run_verify_deploy",
			},
			ForbiddenActions: []string{
				"deploy_without_handoff",
				"restart_without_recovery_artifact",
				"skip_post_deploy_verification",
			},
		},
	}

	for name, text := range map[string]string{
		"approval prompt": renderOperationProposalMaterializedPromptFallback(state),
		"approved event":  approvedContinuationEventTextForState(state),
	} {
		stopLine := stopTextForProjection(text)
		if stopLine == "" {
			t.Fatalf("%s text = %q, want Stops before line", name, text)
		}
		if strings.Contains(stopLine, "deploy/restart") {
			t.Fatalf("%s stop line = %q, did not want broad deploy/restart stop for deploy lease", name, stopLine)
		}
		for _, want := range []string{"release without handoff", "restart without recovery artifact", "credentials/tokens", "archive/delete"} {
			if !strings.Contains(stopLine, want) {
				t.Fatalf("%s stop line = %q, want %q", name, stopLine, want)
			}
		}
	}
}

func TestReadOnlyApprovalProjectionStillRendersDeployRestartStop(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		StageSummary:   "Inspect local status.",
		RemainingTurns: 1,
		ActionProposal: session.ActionProposal{
			Summary:          "Inspect local status.",
			BoundedEffect:    "Read local status only.",
			RiskClass:        "read_only_review",
			AllowedActions:   []string{"read_only"},
			ForbiddenActions: []string{"deploy_restart_without_explicit_approval", "credentials_or_tokens"},
		},
		ContinuationLease: session.ContinuationLease{Status: session.ContinuationLeaseStatusActive, MaxTurns: 1},
	}
	text := approvedContinuationEventTextForState(state)
	stopLine := stopTextForProjection(text)
	if !strings.Contains(stopLine, "deploy/restart") {
		t.Fatalf("stop line = %q, want broad deploy/restart stop for non-deploy lease; text=%q", stopLine, text)
	}
}

func stopTextForProjection(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "Stops before") {
			continue
		}
		if strings.TrimSpace(line) != "Stops before:" {
			return line
		}
		var bullets []string
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(next)
			if next == "" {
				break
			}
			bullets = append(bullets, next)
		}
		return strings.Join(bullets, " ")
	}
	return ""
}
