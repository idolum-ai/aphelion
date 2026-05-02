//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const (
	organicRalphSandboxAction        = "execute_in_approved_user_sandbox"
	organicRalphSandboxProfile       = "approved_user_isolated"
	organicRalphSandboxWriteBoundary = "write_user_workspace_memory_tmp"
)

func applyOrganicRalphSandbox(action session.ActionProposal, opState session.OperationState, proposal session.OperationProposal) session.ActionProposal {
	if !organicRalphOperationProposal(opState, proposal) {
		return action
	}

	action.AllowedActions = append(action.AllowedActions,
		organicRalphSandboxAction,
		"report_evidence",
	)
	action.ForbiddenActions = append(action.ForbiddenActions,
		"write_outside_approved_user_sandbox",
		"network_access_without_separate_grant",
		"read_secrets_or_credentials",
		"purchase_or_public_effect",
		"expand_authority_without_new_approval",
	)
	action.ValidationPlan = append(action.ValidationPlan,
		"execute with the approved_user isolated sandbox profile",
		"keep network denied unless a separate capability grant explicitly allows it",
	)

	if organicRalphProposalIsSystemChange(action, proposal) {
		action.AllowedActions = append(action.AllowedActions,
			organicRalphSandboxWriteBoundary,
			"run_tests_in_sandbox",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"commit_without_separate_approval",
			"deploy",
			"restart_service",
			"push_remote",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"treat prompt root and shared memory as read-only; write only user workspace, user memory, or tmp",
			"report diff, tests, and residual risk before requesting commit, deploy, restart, or push",
		)
		action.BoundedEffect = appendOrganicRalphSandboxBoundedEffect(action.BoundedEffect, "Sandbox boundary: execute as approved_user isolated; writes limited to user workspace, user memory, or tmp; no network, secrets, commit, deploy, restart, or push without separate approval.")
		return session.NormalizeActionProposal(action)
	}

	action.AllowedActions = append(action.AllowedActions,
		"inspect_readonly_state",
	)
	action.ForbiddenActions = append(action.ForbiddenActions,
		"edit_files",
		"write_files",
		"commit",
		"deploy",
		"restart_service",
		"push_remote",
	)
	action.ValidationPlan = append(action.ValidationPlan,
		"keep the action read-only and report evidence before requesting any write lease",
	)
	action.BoundedEffect = appendOrganicRalphSandboxBoundedEffect(action.BoundedEffect, "Sandbox boundary: execute as approved_user isolated read-only review; no edits, network, commit, deploy, restart, or push without separate approval.")
	return session.NormalizeActionProposal(action)
}

func organicRalphOperationProposal(opState session.OperationState, proposal session.OperationProposal) bool {
	opState = session.NormalizeOperationState(opState)
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	if strings.HasPrefix(strings.TrimSpace(opState.ID), "organic-ralph-") {
		return true
	}
	if strings.TrimSpace(opState.Stage) == "organic_proposal" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(proposal.ID), "organic-ralph-")
}

func organicRalphProposalIsSystemChange(action session.ActionProposal, proposal session.OperationProposal) bool {
	kind := normalizeOrganicRalphSandboxKind(firstNonEmptyContinuation(action.RiskClass, proposal.Kind))
	switch kind {
	case "system_change":
		return true
	case "read_only_review", "status_check":
		return false
	}
	inferred := organicRalphKindFromStateText(strings.Join([]string{
		action.Summary,
		action.WhyNow,
		action.BoundedEffect,
		proposal.Summary,
		proposal.WhyNow,
		proposal.BoundedEffect,
	}, "\n"))
	return inferred == "system_change"
}

func normalizeOrganicRalphSandboxKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.ReplaceAll(kind, "-", "_")
	kind = strings.ReplaceAll(kind, " ", "_")
	return kind
}

func appendOrganicRalphSandboxBoundedEffect(effect string, note string) string {
	effect = strings.TrimSpace(effect)
	note = strings.TrimSpace(note)
	if note == "" {
		return effect
	}
	lower := strings.ToLower(effect)
	if strings.Contains(lower, "approved_user") && strings.Contains(lower, "sandbox") {
		return effect
	}
	if effect == "" {
		return note
	}
	return strings.TrimRight(effect, " \t\r\n.") + ". " + note
}

func continuationExecutionActor(actor principal.Principal, state session.ContinuationState) principal.Principal {
	if !continuationRequiresApprovedUserSandbox(state) || actor.TelegramUserID <= 0 {
		return actor
	}
	return principal.Principal{
		TelegramUserID: actor.TelegramUserID,
		Role:           principal.RoleApprovedUser,
	}
}

func continuationRequiresApprovedUserSandbox(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return actionListContains(state.ActionProposal.AllowedActions, organicRalphSandboxAction) ||
		actionListContains(state.ContinuationLease.AllowedActions, organicRalphSandboxAction)
}

func actionListContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
