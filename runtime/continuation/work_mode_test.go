//go:build linux

package continuation

import (
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func TestWorkModeForStateDoesNotGrantWorkspaceWriteFromProseOnly(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Patch prompt handling and validate it.",
		ActionProposal: session.ActionProposal{
			Summary:       "Patch prompt handling",
			BoundedEffect: "Edit prompt code and run tests; stop before deploy.",
		},
	}

	if got := WorkModeForState(state); got == WorkModeWorkspaceWrite {
		t.Fatalf("WorkModeForState() = %q, want prose not to grant workspace_write", got)
	}
}

func TestWorkModeForStateTrustsExplicitReadOnlyOverNegatedDeployText(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		StageSummary: "Inspect mail-child adapter environment/path metadata.",
		ActionProposal: session.ActionProposal{
			RiskClass: "read_only_child_adapter_environment_inspection",
			Summary:   "Inspect child adapter metadata",
			BoundedEffect: "Read local non-secret child state, adapter config, execution events, binary path metadata, and sanitized command metadata. " +
				"No mailbox content/query, OAuth, file mutation, credential exposure, config edits, deploy, or restart.",
			AllowedActions: []string{
				"inspect_durable_agent_state",
				"inspect_external_channel_adapter_state",
				"inspect_execution_events_for_mailbox_adapter_command",
				"inspect_binary_path_metadata",
				"inspect_nonsecret_environment_metadata",
				"report_mismatch_and_repair_options",
			},
			ForbiddenActions: []string{
				"read_mailbox_contents",
				"deploy",
				"restart",
			},
		},
	}

	if got := WorkModeForState(state); got != WorkModeReadOnly {
		t.Fatalf("WorkModeForState() = %q, want read_only", got)
	}
}

func TestLeaseAccessCheckBlocksBroadCommitForbiddenAction(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := session.ContinuationState{
		Status: session.ContinuationStatusApproved,
		ActionProposal: session.ActionProposal{
			RiskClass:        "workspace_commit_then_repo_write_bounded",
			AllowedActions:   []string{"git_commit_validated_slices", "edit_repo_code"},
			ForbiddenActions: []string{"commit"},
		},
		ContinuationLease: session.ContinuationLease{
			ID:               "lease-commit-forbidden",
			Status:           session.ContinuationLeaseStatusActive,
			RemainingTurns:   1,
			AllowedActions:   []string{"git_commit_validated_slices", "edit_repo_code"},
			ForbiddenActions: []string{"commit"},
			ExpiresAt:        now.Add(time.Hour),
		},
	}

	mode := WorkModeForState(state)
	if mode != WorkModeCommit {
		t.Fatalf("WorkModeForState() = %q, want commit", mode)
	}
	decision := LeaseAccessCheck(state, mode, now)
	if decision.Allowed || decision.Reason != "action_forbidden" {
		t.Fatalf("LeaseAccessCheck() = %#v, want broad commit forbidden", decision)
	}
}

func TestWorkModeForStateUsesCurrentBundlePhaseAuthority(t *testing.T) {
	t.Parallel()

	state := session.ContinuationState{
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:             "mixed-authority-bundle",
			Status:         session.ContinuationLeaseStatusActive,
			CurrentPhaseID: "phase-commit",
			Phases: []session.ContinuationApprovalBundlePhase{
				{
					ID:             "phase-read",
					Status:         session.ContinuationLeaseStatusActive,
					AuthorityClass: "read_only",
					AllowedActions: []string{"inspect_code"},
				},
				{
					ID:             "phase-commit",
					Status:         session.ContinuationLeaseStatusActive,
					AuthorityClass: "commit",
					AllowedActions: []string{"git_commit", "report_commit_evidence"},
				},
			},
		},
	}

	if got := WorkModeForState(state); got != WorkModeCommit {
		t.Fatalf("WorkModeForState() = %q, want current bundle phase commit authority", got)
	}
	phase, ok := CurrentBundlePhase(state.ApprovalBundle)
	if !ok || phase.ID != "phase-commit" {
		t.Fatalf("CurrentBundlePhase() = %#v/%v, want current phase-commit", phase, ok)
	}
}

func TestLeaseAccessCheckAllowsStructuredAuthorityWhenNotForbidden(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	state := session.ContinuationState{
		Status: session.ContinuationStatusApproved,
		ActionProposal: session.ActionProposal{
			RiskClass: "workspace_write",
		},
		ContinuationLease: session.ContinuationLease{
			ID:             "lease-structured-authority",
			Status:         session.ContinuationLeaseStatusActive,
			RemainingTurns: 1,
			AllowedActions: []string{"edit_repo_code"},
			ExpiresAt:      now.Add(time.Hour),
		},
	}

	decision := LeaseAccessCheck(state, WorkModeWorkspaceWrite, now)
	if !decision.Allowed || decision.Reason != "allowed_by_structured_authority" {
		t.Fatalf("LeaseAccessCheck() = %#v, want structured workspace_write allowed", decision)
	}

	state.ContinuationLease.ForbiddenActions = []string{"workspace_write"}
	decision = LeaseAccessCheck(state, WorkModeWorkspaceWrite, now)
	if decision.Allowed || decision.Reason != "action_forbidden" {
		t.Fatalf("LeaseAccessCheck(forbidden) = %#v, want explicit forbidden action to win", decision)
	}
}

func TestLeaseAccessCheckFailsClosedForInactiveLease(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	for _, tc := range []struct {
		name  string
		lease session.ContinuationLease
	}{
		{
			name: "consumed",
			lease: session.ContinuationLease{
				ID:             "lease-consumed",
				Status:         session.ContinuationLeaseStatusConsumed,
				RemainingTurns: 0,
				AllowedActions: []string{"read_only"},
				ExpiresAt:      now.Add(time.Hour),
			},
		},
		{
			name: "expired",
			lease: session.ContinuationLease{
				ID:             "lease-expired",
				Status:         session.ContinuationLeaseStatusActive,
				RemainingTurns: 1,
				AllowedActions: []string{"read_only"},
				ExpiresAt:      now.Add(-time.Second),
			},
		},
		{
			name: "pending",
			lease: session.ContinuationLease{
				ID:             "lease-pending",
				Status:         session.ContinuationLeaseStatusPending,
				RemainingTurns: 1,
				AllowedActions: []string{"read_only"},
				ExpiresAt:      now.Add(time.Hour),
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := session.ContinuationState{ContinuationLease: tc.lease}
			decision := LeaseAccessCheck(state, WorkModeReadOnly, now)
			if decision.Allowed || decision.Reason != "lease_inactive_or_expired" {
				t.Fatalf("LeaseAccessCheck() = %#v, want lease_inactive_or_expired", decision)
			}
		})
	}
}
