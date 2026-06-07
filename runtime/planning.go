//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func mergeSessionPlanState(inMemory session.PlanState, persisted session.PlanState) session.PlanState {
	inMemory = session.NormalizePlanState(inMemory)
	persisted = session.NormalizePlanState(persisted)

	switch {
	case persisted.UpdatedAt.After(inMemory.UpdatedAt):
		return persisted
	case inMemory.UpdatedAt.After(persisted.UpdatedAt):
		return inMemory
	case len(persisted.Steps) > 0 || persisted.Explanation != "":
		return persisted
	default:
		return inMemory
	}
}

func mergeSessionOperationState(inMemory session.OperationState, persisted session.OperationState) session.OperationState {
	inMemory = session.NormalizeOperationState(inMemory)
	persisted = session.NormalizeOperationState(persisted)

	switch {
	case persisted.UpdatedAt.After(inMemory.UpdatedAt):
		return persisted
	case inMemory.UpdatedAt.After(persisted.UpdatedAt):
		return inMemory
	case persisted.Active():
		return persisted
	default:
		return inMemory
	}
}

func mergeSessionContinuationState(inMemory session.ContinuationState, persisted session.ContinuationState) session.ContinuationState {
	inMemoryUpdatedAt := inMemory.UpdatedAt
	persistedUpdatedAt := persisted.UpdatedAt
	inMemory = session.NormalizeTurnAuthorizationState(inMemory)
	persisted = session.NormalizeTurnAuthorizationState(persisted)

	if continuationTerminalLeaseSupersedes(persisted, inMemory) {
		return persisted
	}
	switch {
	case persistedUpdatedAt.After(inMemoryUpdatedAt):
		return persisted
	case inMemoryUpdatedAt.After(persistedUpdatedAt):
		return inMemory
	case continuationStateHasDurableRecord(persisted):
		return persisted
	default:
		return inMemory
	}
}

func continuationTerminalLeaseSupersedes(persisted session.ContinuationState, inMemory session.ContinuationState) bool {
	leaseID := strings.TrimSpace(persisted.ContinuationLease.ID)
	if leaseID == "" || leaseID != strings.TrimSpace(inMemory.ContinuationLease.ID) {
		return false
	}
	switch persisted.ContinuationLease.Status {
	case session.ContinuationLeaseStatusConsumed, session.ContinuationLeaseStatusRevoked, session.ContinuationLeaseStatusExpired:
		return true
	default:
		return false
	}
}

func continuationStateHasDurableRecord(state session.ContinuationState) bool {
	return strings.TrimSpace(state.DecisionID) != "" ||
		state.DecisionMessageID > 0 ||
		strings.TrimSpace(state.Objective) != "" ||
		strings.TrimSpace(state.StageSummary) != "" ||
		state.RemainingTurns > 0 ||
		state.ApprovedBy > 0 ||
		state.ActionProposal.Active() ||
		strings.TrimSpace(state.ContinuationLease.ID) != "" ||
		strings.TrimSpace(state.ContinuationLease.ProposalID) != "" ||
		state.ApprovalBundle.Active() ||
		!state.ParkedAt.IsZero() ||
		strings.TrimSpace(state.ParkedReason) != "" ||
		strings.TrimSpace(state.ParkedSource) != ""
}
