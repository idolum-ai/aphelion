//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestOperationPhaseApprovalBoundaryInvariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		phase      session.OperationPhase
		reason     string
		wantRepair bool
	}{
		{
			name: "unclear approval boundary can become read only deliberation",
			phase: session.OperationPhase{
				ID:                "deploy-live-service",
				Summary:           "Deploy the live service",
				Status:            session.PlanStatusPending,
				AuthorityClass:    "workspace_write",
				BlockedReasonCode: "unclear_approval_boundary",
			},
			reason:     "waiting_for_a_clearer_approval_boundary",
			wantRepair: true,
		},
		{
			name: "consent gates are hard blockers not repairable planning",
			phase: session.OperationPhase{
				ID:                "read-private-mailbox",
				Summary:           "Read a private mailbox",
				Status:            session.PlanStatusPending,
				AuthorityClass:    "external_account",
				BlockedReasonCode: "waiting_for_consent",
				RequiresConsent:   true,
			},
			reason:     "waiting for explicit consent",
			wantRepair: false,
		},
		{
			name: "deploy restart hard stop cannot be smuggled through deliberation",
			phase: session.OperationPhase{
				ID:                "restart-service",
				Summary:           "Restart the service",
				Status:            session.PlanStatusPending,
				AuthorityClass:    "system_change",
				BlockedReasonCode: "unclear_approval_boundary",
				ForbiddenActions:  []string{"deploy/restart", "credentials/tokens"},
			},
			reason:     "waiting_for_a_clearer_approval_boundary",
			wantRepair: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := session.OperationState{PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{tc.phase}}}
			got := operationPhaseApprovalBlockCanEnterDeliberation(op, tc.phase, tc.reason)
			if got != tc.wantRepair {
				t.Fatalf("operationPhaseApprovalBlockCanEnterDeliberation() = %t, want %t", got, tc.wantRepair)
			}
		})
	}
}

func TestApprovalBoundaryDeliberationPhaseIsReadOnlyAndNonTransitive(t *testing.T) {
	t.Parallel()
	blocked := session.OperationPhase{
		ID:                "commit-and-pr",
		Summary:           "Commit and open the PR",
		Status:            session.PlanStatusPending,
		AuthorityClass:    "commit",
		BlockedReasonCode: "unclear_approval_boundary",
		AllowedActions:    []string{"commit", "push"},
		ForbiddenActions:  []string{"deploy", "restart"},
	}
	phase := operationApprovalBoundaryDeliberationFirstPhase(session.OperationState{Objective: "Ship changes"}, blocked, "waiting_for_a_clearer_approval_boundary")
	if phase.AuthorityClass != "read_only_review" || !phase.RequiresApproval {
		t.Fatalf("phase authority=%q requires=%t, want read_only_review approval", phase.AuthorityClass, phase.RequiresApproval)
	}
	if strings.Contains(strings.Join(phase.AllowedActions, " "), "commit") || strings.Contains(strings.Join(phase.AllowedActions, " "), "push") {
		t.Fatalf("allowed actions = %#v, want no original write actions", phase.AllowedActions)
	}
	joinedForbidden := strings.Join(phase.ForbiddenActions, " ")
	for _, forbidden := range []string{"execute_blocked_work", "deploy", "restart", "credential", "external_account"} {
		if !strings.Contains(joinedForbidden, forbidden) {
			t.Fatalf("forbidden actions = %#v, missing %q", phase.ForbiddenActions, forbidden)
		}
	}
}
