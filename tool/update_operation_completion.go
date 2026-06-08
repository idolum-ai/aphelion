//go:build linux

package tool

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func validateOperationCompletionEvidence(current session.OperationState, state session.OperationState) error {
	current = session.NormalizeOperationState(current)
	state = session.NormalizeOperationState(state)
	for _, phase := range state.PhasePlan.Phases {
		phase = normalizeToolOperationPhase(phase)
		if phase.Status != session.PlanStatusCompleted ||
			!updateOperationPhaseCompletionNeedsWorkEvidence(phase) ||
			operationPhaseAlreadyCompleted(current, phase) {
			continue
		}
		if !updateOperationPhaseHasCompletionEvidence(state, phase) {
			return fmt.Errorf("update_operation phase %q cannot be completed without matching successful work evidence", phase.ID)
		}
	}
	if state.Status != session.OperationStatusCompleted {
		return nil
	}
	phases := state.PhasePlan.Phases
	if len(phases) == 0 {
		phases = current.PhasePlan.Phases
	}
	for _, phase := range phases {
		phase = normalizeToolOperationPhase(phase)
		if !updateOperationPhaseCompletionNeedsWorkEvidence(phase) ||
			operationPhaseAlreadyCompleted(current, phase) {
			continue
		}
		if phase.Status != session.PlanStatusCompleted || !updateOperationPhaseHasCompletionEvidence(state, phase) {
			return fmt.Errorf("update_operation status completed requires executable phase %q to have matching successful work evidence", phase.ID)
		}
	}
	return nil
}

func operationPhaseAlreadyCompleted(current session.OperationState, phase session.OperationPhase) bool {
	phaseID := strings.TrimSpace(phase.ID)
	if phaseID == "" {
		return false
	}
	for _, currentPhase := range current.PhasePlan.Phases {
		currentPhase = normalizeToolOperationPhase(currentPhase)
		if strings.TrimSpace(currentPhase.ID) == phaseID && currentPhase.Status == session.PlanStatusCompleted {
			return true
		}
	}
	return false
}

func updateOperationPhaseCompletionNeedsWorkEvidence(phase session.OperationPhase) bool {
	phase = normalizeToolOperationPhase(phase)
	if contract, ok := session.AuthorityContractFor(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect); ok {
		if contract.ExternalEffectsAllowed {
			return true
		}
		switch contract.WorkAction {
		case session.AuthorityWorkActionWorkspaceWrite, session.AuthorityWorkActionCommit, session.AuthorityWorkActionDeploy:
			return true
		default:
			return false
		}
	}
	lower := strings.ToLower(strings.Join(append([]string{
		phase.AuthorityClass,
		phase.Summary,
		phase.BoundedEffect,
	}, phase.AllowedActions...), " "))
	for _, needle := range []string{
		"workspace_write",
		"edit",
		"write",
		"patch",
		"commit",
		"push",
		"pull request",
		"open_pr",
		"github",
		"external_account",
		"deploy",
		"restart",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func updateOperationPhaseHasCompletionEvidence(state session.OperationState, phase session.OperationPhase) bool {
	state = session.NormalizeOperationState(state)
	phase = normalizeToolOperationPhase(phase)
	work := session.NormalizeWorkOperationMetadata(state.Work)
	if work.LastCompletedAt.IsZero() || strings.TrimSpace(work.LastError) != "" {
		return false
	}
	leaseID := strings.TrimSpace(phase.LeaseID)
	if leaseID == "" || strings.TrimSpace(work.LastLeaseID) != leaseID {
		return false
	}
	if opID := strings.TrimSpace(state.ID); opID != "" && strings.TrimSpace(work.LastOperationID) != "" && strings.TrimSpace(work.LastOperationID) != opID {
		return false
	}
	return true
}

func normalizeToolOperationPhase(phase session.OperationPhase) session.OperationPhase {
	plan := session.NormalizeOperationState(session.OperationState{PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{phase}}}).PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}
	}
	return plan.Phases[0]
}
