//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const (
	organicProposalSandboxAction        = "execute_in_approved_user_sandbox"
	organicProposalSandboxProfile       = "approved_user_isolated"
	organicProposalSandboxWriteBoundary = "write_user_workspace_memory_tmp"
)

func applyOrganicProposalSandbox(action session.ActionProposal, opState session.OperationState, proposal session.OperationProposal) session.ActionProposal {
	if !organicProposalOperationProposal(opState, proposal) {
		return action
	}

	action.AllowedActions = append(action.AllowedActions,
		organicProposalSandboxAction,
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

	if organicProposalIsSystemChange(action, proposal) {
		action.AllowedActions = append(action.AllowedActions,
			organicProposalSandboxWriteBoundary,
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
		action.BoundedEffect = appendOrganicProposalSandboxBoundedEffect(action.BoundedEffect, "Sandbox boundary: execute as approved_user isolated; writes limited to user workspace, user memory, or tmp; no network, secrets, commit, deploy, restart, or push without separate approval.")
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
	action.BoundedEffect = appendOrganicProposalSandboxBoundedEffect(action.BoundedEffect, "Sandbox boundary: execute as approved_user isolated read-only review; no edits, network, commit, deploy, restart, or push without separate approval.")
	return session.NormalizeActionProposal(action)
}

func applyGoalContinuationSandbox(action session.ActionProposal, opState session.OperationState, proposal session.OperationProposal) session.ActionProposal {
	if !goalContinuationOperationProposal(opState, proposal) {
		return action
	}
	switch normalizeOrganicProposalSandboxKind(firstNonEmptyContinuation(action.RiskClass, proposal.Kind)) {
	case "", "read_only_review", "status_check":
	default:
		return session.NormalizeActionProposal(action)
	}
	action.AllowedActions = append(action.AllowedActions,
		"inspect_readonly_state",
		"draft_next_phase_plan",
		"propose_one_safe_live_test",
	)
	action.ForbiddenActions = append(action.ForbiddenActions,
		"edit_files",
		"write_files",
		"read_secrets_or_credentials",
		"use_credentials",
		"external_account_action",
		"commit",
		"deploy",
		"restart_service",
		"push_remote",
		"purchase_or_public_effect",
	)
	action.ValidationPlan = append(action.ValidationPlan,
		"keep the next-phase lease read-only",
		"report the broader phased plan and exactly one safe next live smoke test before requesting any execution lease",
	)
	action.BoundedEffect = appendOrganicProposalSandboxBoundedEffect(action.BoundedEffect, "Sandbox boundary: read-only next-phase planning only; no edits, secrets, credentials, external account actions, commit, deploy, restart, or push without a separate lease.")
	return session.NormalizeActionProposal(action)
}

func organicProposalOperationProposal(opState session.OperationState, proposal session.OperationProposal) bool {
	opState = session.NormalizeOperationState(opState)
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	if strings.HasPrefix(strings.TrimSpace(opState.ID), "organic-proposal-") {
		return true
	}
	if strings.TrimSpace(opState.Stage) == "organic_proposal" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(proposal.ID), "organic-proposal-")
}

func goalContinuationOperationProposal(opState session.OperationState, proposal session.OperationProposal) bool {
	opState = session.NormalizeOperationState(opState)
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	if strings.HasPrefix(strings.TrimSpace(opState.ID), goalContinuationIDPrefix) {
		return true
	}
	if strings.TrimSpace(opState.Stage) == "next_phase_proposal" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(proposal.ID), goalContinuationIDPrefix)
}

func organicProposalIsSystemChange(action session.ActionProposal, proposal session.OperationProposal) bool {
	kind := normalizeOrganicProposalSandboxKind(firstNonEmptyContinuation(action.RiskClass, proposal.Kind))
	switch kind {
	case "system_change":
		return true
	case "read_only_review", "status_check":
		return false
	}
	inferred := organicProposalKindFromStateText(strings.Join([]string{
		action.Summary,
		action.WhyNow,
		action.BoundedEffect,
		proposal.Summary,
		proposal.WhyNow,
		proposal.BoundedEffect,
	}, "\n"))
	return inferred == "system_change"
}

func normalizeOrganicProposalSandboxKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.ReplaceAll(kind, "-", "_")
	kind = strings.ReplaceAll(kind, " ", "_")
	return kind
}

func appendOrganicProposalSandboxBoundedEffect(effect string, note string) string {
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
	return actionListContains(state.ActionProposal.AllowedActions, organicProposalSandboxAction) ||
		actionListContains(state.ContinuationLease.AllowedActions, organicProposalSandboxAction)
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
