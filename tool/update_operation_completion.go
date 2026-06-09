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
	state = updateOperationStateWithCurrentIDsForValidation(current, state)
	if err := validateCurrentLeasedExecutablePhases(current, state); err != nil {
		return err
	}
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

func validateCurrentLeasedExecutablePhases(current session.OperationState, state session.OperationState) error {
	for _, currentPhase := range current.PhasePlan.Phases {
		currentPhase = normalizeToolOperationPhase(currentPhase)
		if currentPhase.Status != session.PlanStatusInProgress ||
			strings.TrimSpace(currentPhase.LeaseID) == "" ||
			!updateOperationPhaseCompletionNeedsWorkEvidence(currentPhase) {
			continue
		}
		updatedPhase, ok := matchingUpdatedOperationPhase(current, state, currentPhase)
		if !ok {
			return fmt.Errorf("update_operation cannot remove in-progress executable phase %q while its lease is active", currentPhase.ID)
		}
		if !operationPhaseCompletionMaterialMatches(currentPhase, updatedPhase) ||
			session.OperationPhaseProposalID(current, currentPhase) != session.OperationPhaseProposalID(state, updatedPhase) {
			return fmt.Errorf("update_operation cannot rewrite in-progress executable phase %q while its lease is active", currentPhase.ID)
		}
		switch updatedPhase.Status {
		case session.PlanStatusCompleted:
			if !updateOperationPhaseHasCompletionEvidence(state, currentPhase) {
				return fmt.Errorf("update_operation phase %q cannot be completed without matching successful work evidence", currentPhase.ID)
			}
		case session.PlanStatusInProgress:
			if state.Status == session.OperationStatusCompleted {
				return fmt.Errorf("update_operation status completed requires executable phase %q to have matching successful work evidence", currentPhase.ID)
			}
		default:
			return fmt.Errorf("update_operation cannot downgrade in-progress executable phase %q while its lease is active", currentPhase.ID)
		}
	}
	return nil
}

func matchingUpdatedOperationPhase(current session.OperationState, state session.OperationState, currentPhase session.OperationPhase) (session.OperationPhase, bool) {
	currentPhase = normalizeToolOperationPhase(currentPhase)
	currentPhaseID := strings.TrimSpace(currentPhase.ID)
	currentProposalID := session.OperationPhaseProposalID(current, currentPhase)
	for _, candidate := range state.PhasePlan.Phases {
		candidate = normalizeToolOperationPhase(candidate)
		if currentPhaseID != "" && strings.TrimSpace(candidate.ID) == currentPhaseID {
			return candidate, true
		}
		if currentProposalID != "" && session.OperationPhaseProposalID(state, candidate) == currentProposalID {
			return candidate, true
		}
	}
	return session.OperationPhase{}, false
}

func operationPhaseCompletionMaterialMatches(left session.OperationPhase, right session.OperationPhase) bool {
	left = normalizeToolOperationPhase(left)
	right = normalizeToolOperationPhase(right)
	return strings.TrimSpace(left.AuthorityClass) == strings.TrimSpace(right.AuthorityClass) &&
		strings.TrimSpace(left.BoundedEffect) == strings.TrimSpace(right.BoundedEffect) &&
		operationStringSlicesEqual(left.AllowedActions, right.AllowedActions) &&
		operationStringSlicesEqual(left.ForbiddenActions, right.ForbiddenActions)
}

func operationStringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if strings.TrimSpace(left[i]) != strings.TrimSpace(right[i]) {
			return false
		}
	}
	return true
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
	return session.OperationPhaseRequiresWorkEvidence(phase)
}

func updateOperationPhaseHasCompletionEvidence(state session.OperationState, phase session.OperationPhase) bool {
	state = session.NormalizeOperationState(state)
	phase = normalizeToolOperationPhase(phase)
	work := session.NormalizeWorkOperationMetadata(state.Work)
	if work.LastCompletedAt.IsZero() || strings.TrimSpace(work.LastError) != "" {
		return false
	}
	opID := strings.TrimSpace(state.ID)
	if opID == "" || strings.TrimSpace(work.LastOperationID) != opID {
		return false
	}
	leaseID := strings.TrimSpace(phase.LeaseID)
	if leaseID == "" || strings.TrimSpace(work.LastLeaseID) != leaseID {
		return false
	}
	workMode := strings.TrimSpace(session.OperationPhaseWorkAction(phase))
	if workMode == "" || strings.TrimSpace(work.LastWorkMode) != workMode {
		return false
	}
	proposalID := session.OperationPhaseProposalID(state, phase)
	if proposalID == "" || strings.TrimSpace(work.LastActionOperationID) != proposalID {
		return false
	}
	actionProposalID := strings.TrimSpace(work.LastActionProposalID)
	if actionProposalID == "" || (actionProposalID != proposalID && actionProposalID != "aprop-"+proposalID) {
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

func updateOperationStateWithCurrentIDsForValidation(current session.OperationState, state session.OperationState) session.OperationState {
	if strings.TrimSpace(state.ID) == "" {
		state.ID = strings.TrimSpace(current.ID)
	}
	if strings.TrimSpace(state.PhasePlan.ID) == "" {
		state.PhasePlan.ID = strings.TrimSpace(current.PhasePlan.ID)
	}
	return session.NormalizeOperationState(state)
}
