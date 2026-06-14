//go:build linux

package tool

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) persistExecProposalState(key session.SessionKey, proposal session.OperationProposal, status session.ProposalStatus) error {
	if r == nil || r.store == nil || (key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero()) {
		return nil
	}

	current, err := r.store.OperationState(key)
	if err != nil {
		return err
	}
	current = session.NormalizeOperationState(current)

	now := time.Now().UTC()
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	if proposal.Status == "" {
		proposal.Status = session.ProposalStatusPending
	}
	if status != "" {
		proposal.Status = status
	}
	if execProposalShouldStaySidecar(current, proposal) {
		return nil
	}
	if proposal.ID == "" {
		proposal.ID = generatedOperationID("exec-proposal")
	}
	proposal.UpdatedAt = now

	if !current.Active() {
		current.ID = generatedOperationID("op")
	}
	if current.ID == "" {
		current.ID = generatedOperationID("op")
	}
	if current.Objective == "" {
		current.Objective = "Continue the current operation."
	}

	switch proposal.Status {
	case session.ProposalStatusApproved:
		if current.Status != session.OperationStatusCompleted && current.Status != session.OperationStatusFailed {
			current.Status = session.OperationStatusActive
		}
		current.Stage = "execution"
	case session.ProposalStatusDenied, session.ProposalStatusExpired, session.ProposalStatusPending:
		current.Status = session.OperationStatusBlocked
		current.Stage = "proposal"
	}

	current.Summary = proposalStatusSummary(proposal)
	current.Proposal = proposal
	current.UpdatedAt = now
	return r.store.UpdateOperationState(key, current)
}

func execProposalShouldStaySidecar(current session.OperationState, proposal session.OperationProposal) bool {
	current = session.NormalizeOperationState(current)
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	if !current.Active() || !execProposalIsHeuristicConfirmation(proposal) {
		return false
	}
	for _, phase := range current.PhasePlan.Phases {
		if phase.Status == session.PlanStatusInProgress {
			return true
		}
	}
	return false
}

func execProposalIsHeuristicConfirmation(proposal session.OperationProposal) bool {
	switch strings.TrimSpace(proposal.Kind) {
	case "possible_delete_command",
		"possible_database_delete_command",
		"high_impact_storage_command",
		"service_interruption_command",
		"process_interruption_command",
		"remote_shell_execution",
		"capability_acquisition",
		"external_operation",
		"external_account_command",
		"remote_host_operation",
		"service_process_change",
		"repo_history_mutation",
		"workspace_escape":
		return true
	default:
		return false
	}
}

func proposalStatusSummary(proposal session.OperationProposal) string {
	summary := strings.TrimSpace(proposal.Summary)
	if summary == "" {
		summary = "bounded operational proposal"
	}
	if proposal.Kind == "repo_history_mutation" && repoHistoryProposalIsCommit(proposal) {
		switch proposal.Status {
		case session.ProposalStatusApproved:
			return "Repository commit approval granted: " + summary
		case session.ProposalStatusDenied:
			return "Repository commit blocked: proposal denied. Next action: approve the specific git commit proposal card or request a fresh one."
		case session.ProposalStatusExpired:
			return "Repository commit blocked: approval timed out/default-denied. Next action: request and approve a fresh git commit proposal."
		default:
			return "Waiting on repository commit approval: " + summary
		}
	}
	switch proposal.Status {
	case session.ProposalStatusApproved:
		return "Proposal approved: " + summary
	case session.ProposalStatusDenied:
		return "Proposal denied: " + summary
	case session.ProposalStatusExpired:
		return "Proposal expired: " + summary
	default:
		return "Waiting on proposal approval: " + summary
	}
}

func repoHistoryProposalIsCommit(proposal session.OperationProposal) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(proposal.Summary)), "commit")
}

func execApprovalDeniedError(reason string, decision ExecApprovalDecision) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "command approval"
	}
	if reason != "repository commit" {
		return fmt.Errorf("proposal denied: %s", reason)
	}
	return errors.New(repositoryCommitDeniedDiagnostic(decision))
}

func repositoryCommitDeniedDiagnostic(decision ExecApprovalDecision) string {
	status := "denied"
	denialReason := "denied"
	if decision.TimedOut {
		status = "expired"
		denialReason = "timeout"
	} else if strings.TrimSpace(decision.Choice) == "" {
		denialReason = "unknown"
	}
	requiredKind := strings.TrimSpace(decision.RequiredApprovalKind)
	if requiredKind == "" {
		requiredKind = "proposal_approval"
	}
	defaultChoice := strings.TrimSpace(decision.DefaultChoice)
	if defaultChoice == "" {
		defaultChoice = "deny"
	}

	lines := []string{
		"proposal denied: repository commit",
		"gate: repository_commit",
		"required_approval_kind: " + requiredKind,
		"required_approval_status: " + status,
		"required_approval_default: " + defaultChoice,
		"denial_reason: " + denialReason,
	}
	if decisionID := strings.TrimSpace(decision.DecisionID); decisionID != "" {
		lines = append(lines, "decision_id: "+decisionID)
	}
	if choice := strings.TrimSpace(decision.Choice); choice != "" {
		lines = append(lines, "decision_choice: "+choice)
	}
	lines = append(lines,
		"continuation_approval_covered: false",
		"why_not: continuation approval resumes the plan turn; git commit opens a separate repository-history proposal gate.",
		"next_action: approve the specific git commit proposal card, or request a fresh commit approval if it expired.",
	)
	return strings.Join(lines, "\n")
}
