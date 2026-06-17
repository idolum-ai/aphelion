//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func validateOperationCompletionEvidence(current session.OperationState, state session.OperationState) error {
	current = session.NormalizeOperationState(current)
	state = session.NormalizeOperationState(state)
	state = updateOperationStateWithCurrentIDsForValidation(current, state)
	if err := validateCurrentLeasedExecutablePhases(current, state); err != nil {
		return err
	}
	if err := validateCurrentExecutablePhaseCompletions(current, state); err != nil {
		return err
	}
	for _, phase := range state.PhasePlan.Phases {
		phase = normalizeToolOperationPhase(phase)
		if phase.Status != session.PlanStatusCompleted ||
			!updateOperationPhaseCompletionNeedsWorkEvidenceInState(state, phase) ||
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
		if !updateOperationPhaseCompletionNeedsWorkEvidenceInState(state, phase) ||
			operationPhaseAlreadyCompleted(current, phase) {
			continue
		}
		if phase.Status != session.PlanStatusCompleted || !updateOperationPhaseHasCompletionEvidence(state, phase) {
			return fmt.Errorf("update_operation status completed requires executable phase %q to have matching successful work evidence", phase.ID)
		}
	}
	return nil
}

func validateCurrentExecutablePhaseCompletions(current session.OperationState, state session.OperationState) error {
	for _, currentPhase := range current.PhasePlan.Phases {
		currentPhase = normalizeToolOperationPhase(currentPhase)
		if currentPhase.Status == session.PlanStatusCompleted ||
			!updateOperationPhaseCompletionNeedsWorkEvidenceInState(state, currentPhase) {
			continue
		}
		updatedPhase, ok := matchingUpdatedOperationPhase(current, state, currentPhase)
		if !ok {
			if state.Status == session.OperationStatusCompleted {
				return fmt.Errorf("update_operation cannot remove executable phase %q while completing the operation", currentPhase.ID)
			}
			continue
		}
		switch updatedPhase.Status {
		case session.PlanStatusCompleted:
			if !operationPhaseCompletionMaterialMatches(currentPhase, updatedPhase) ||
				session.OperationPhaseProposalID(current, currentPhase) != session.OperationPhaseProposalID(state, updatedPhase) {
				return fmt.Errorf("update_operation cannot rewrite executable phase %q while completing it", currentPhase.ID)
			}
			if !updateOperationPhaseHasCompletionEvidence(state, currentPhase) {
				return fmt.Errorf("update_operation phase %q cannot be completed without matching successful work evidence", currentPhase.ID)
			}
		case session.PlanStatusPending, session.PlanStatusInProgress:
			if state.Status == session.OperationStatusCompleted {
				return fmt.Errorf("update_operation status completed requires executable phase %q to have matching successful work evidence", currentPhase.ID)
			}
		default:
			if state.Status == session.OperationStatusCompleted {
				return fmt.Errorf("update_operation status completed requires executable phase %q to have matching successful work evidence", currentPhase.ID)
			}
		}
	}
	return nil
}

func validateCurrentLeasedExecutablePhases(current session.OperationState, state session.OperationState) error {
	for _, currentPhase := range current.PhasePlan.Phases {
		currentPhase = normalizeToolOperationPhase(currentPhase)
		if currentPhase.Status != session.PlanStatusInProgress ||
			strings.TrimSpace(currentPhase.LeaseID) == "" ||
			!updateOperationPhaseCompletionNeedsWorkEvidenceInState(state, currentPhase) {
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
	phase = normalizeToolOperationPhase(phase)
	return session.OperationPhaseRequiresWorkEvidence(phase)
}

func updateOperationPhaseCompletionNeedsWorkEvidenceInState(state session.OperationState, phase session.OperationPhase) bool {
	phase = normalizeToolOperationPhase(phase)
	if updateOperationPhaseIsStaleNonBlocking(state, phase) {
		return false
	}
	return updateOperationPhaseCompletionNeedsWorkEvidence(phase)
}

func updateOperationPhaseIsStaleNonBlocking(state session.OperationState, phase session.OperationPhase) bool {
	state = session.NormalizeOperationState(state)
	phase = normalizeToolOperationPhase(phase)
	stale := phase.StaleAuthority
	switch strings.TrimSpace(phase.BlockedReasonCode) {
	case "superseded_phase", "stale_phase", "stale_authority":
		stale = true
	}
	if !stale {
		return false
	}
	return updateOperationPhaseCoveredByCompletedSuperseder(state, phase)
}

func updateOperationPhaseCoveredByCompletedSuperseder(state session.OperationState, phase session.OperationPhase) bool {
	phase = normalizeToolOperationPhase(phase)
	phaseID := strings.TrimSpace(phase.ID)
	if phaseID == "" {
		return false
	}
	phaseNeedsEvidence := updateOperationPhaseCompletionNeedsWorkEvidence(phase)
	for _, candidate := range state.PhasePlan.Phases {
		candidate = normalizeToolOperationPhase(candidate)
		if strings.TrimSpace(candidate.ID) == phaseID || candidate.Status != session.PlanStatusCompleted {
			continue
		}
		if !operationStringSliceContains(candidate.SupersedesPhaseIDs, phaseID) {
			continue
		}
		candidateNeedsEvidence := updateOperationPhaseCompletionNeedsWorkEvidence(candidate)
		if candidateNeedsEvidence && updateOperationPhaseHasCompletionEvidence(state, candidate) {
			return true
		}
		if !phaseNeedsEvidence && !candidateNeedsEvidence {
			return true
		}
	}
	return false
}

func operationStringSliceContains(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func updateOperationPhaseHasCompletionEvidence(state session.OperationState, phase session.OperationPhase) bool {
	return updateOperationPhaseCompletionEvidenceReasonCode(state, phase) == ""
}

func updateOperationPhaseCompletionEvidenceReason(state session.OperationState, phase session.OperationPhase) string {
	return operationCompletionEvidenceReasonText(updateOperationPhaseCompletionEvidenceReasonCode(state, phase))
}

func updateOperationPhaseCompletionEvidenceReasonCode(state session.OperationState, phase session.OperationPhase) string {
	state = session.NormalizeOperationState(state)
	phase = normalizeToolOperationPhase(phase)
	work := session.NormalizeWorkOperationMetadata(state.Work)
	if strings.TrimSpace(work.LastError) != "" {
		return "last_work_error"
	}
	if work.LastCompletedAt.IsZero() {
		return "missing_completion_timestamp"
	}
	opID := strings.TrimSpace(state.ID)
	if opID == "" || strings.TrimSpace(work.LastOperationID) != opID {
		return "operation_mismatch"
	}
	leaseID := strings.TrimSpace(phase.LeaseID)
	if leaseID == "" || strings.TrimSpace(work.LastLeaseID) != leaseID {
		return "lease_mismatch"
	}
	workMode := strings.TrimSpace(session.OperationPhaseWorkAction(phase))
	if workMode == "" || strings.TrimSpace(work.LastWorkMode) != workMode {
		return "work_mode_mismatch"
	}
	proposalID := session.OperationPhaseProposalID(state, phase)
	if proposalID == "" || strings.TrimSpace(work.LastActionOperationID) != proposalID {
		return "proposal_mismatch"
	}
	actionProposalID := strings.TrimSpace(work.LastActionProposalID)
	if actionProposalID == "" || (actionProposalID != proposalID && actionProposalID != "aprop-"+proposalID) {
		return "action_proposal_mismatch"
	}
	return ""
}

func operationCompletionEvidenceReasonText(code string) string {
	switch strings.TrimSpace(code) {
	case "last_work_error":
		return "last work ended with an error"
	case "missing_completion_timestamp":
		return "last work has no completion timestamp"
	case "operation_mismatch":
		return "last work does not match the operation"
	case "lease_mismatch":
		return "last work lease does not match the current phase lease"
	case "work_mode_mismatch":
		return "last work mode does not satisfy the phase authority"
	case "proposal_mismatch":
		return "last work does not match the current phase proposal"
	case "action_proposal_mismatch":
		return "last work has no matching action proposal id"
	default:
		return ""
	}
}

// OperationCompletionEvidenceStatus projects executable phase completion evidence
// without mutating operation state. Status and doctor surfaces can use this to
// explain why real work evidence does or does not justify operation closure.
func OperationCompletionEvidenceStatus(state session.OperationState) []session.OperationEvidenceStatus {
	state = session.NormalizeOperationState(state)
	statuses := make([]session.OperationEvidenceStatus, 0, len(state.PhasePlan.Phases))
	for _, phase := range state.PhasePlan.Phases {
		phase = normalizeToolOperationPhase(phase)
		if !updateOperationPhaseCompletionNeedsWorkEvidenceInState(state, phase) {
			continue
		}
		reasonCode := updateOperationPhaseCompletionEvidenceReasonCode(state, phase)
		statuses = append(statuses, session.OperationEvidenceStatus{
			PhaseID:        strings.TrimSpace(phase.ID),
			AuthorityClass: strings.TrimSpace(phase.AuthorityClass),
			Status:         phase.Status,
			EvidenceKind:   "work_metadata",
			Satisfied:      reasonCode == "",
			ReasonCode:     reasonCode,
			Reason:         operationCompletionEvidenceReasonText(reasonCode),
			CompletedAt:    completedAtPtr(state.Work.LastCompletedAt),
			WorkMode:       strings.TrimSpace(state.Work.LastWorkMode),
			LeaseID:        strings.TrimSpace(state.Work.LastLeaseID),
		})
	}
	return statuses
}

func completedAtPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
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
	for i, phase := range state.PhasePlan.Phases {
		currentPhase, ok := matchingUpdatedOperationPhase(state, current, phase)
		if !ok {
			continue
		}
		state.PhasePlan.Phases[i] = updateOperationPhaseWithRuntimeOwnedFields(currentPhase, phase)
	}
	return session.NormalizeOperationState(state)
}

func updateOperationPhaseWithRuntimeOwnedFields(current session.OperationPhase, updated session.OperationPhase) session.OperationPhase {
	current = normalizeToolOperationPhase(current)
	updated = normalizeToolOperationPhase(updated)
	if strings.TrimSpace(updated.LeaseID) == "" {
		updated.LeaseID = strings.TrimSpace(current.LeaseID)
	}
	return updated
}
