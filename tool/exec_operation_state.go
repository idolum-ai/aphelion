//go:build linux

package tool

import (
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
