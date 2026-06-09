//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func operationStatusIsTerminal(status session.OperationStatus) bool {
	switch status {
	case session.OperationStatusCompleted, session.OperationStatusFailed:
		return true
	default:
		return false
	}
}

func (r *Runtime) syncOperationProposalStatusFromContinuation(key session.SessionKey, state session.ContinuationState, status session.ProposalStatus) error {
	if r == nil || r.store == nil || status == "" {
		return nil
	}
	state = session.NormalizeContinuationState(state)
	opID := strings.TrimSpace(state.ActionProposal.OperationID)
	if opID == "" && strings.TrimSpace(state.ActionProposal.ID) == "" && strings.TrimSpace(state.DecisionID) == "" && strings.TrimSpace(state.ContinuationLease.ID) == "" {
		return nil
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return err
	}
	opState = session.NormalizeOperationState(opState)
	planLeaseUpdated := syncOperationPlanLeaseStatusFromContinuation(&opState, state, status)
	updated := planLeaseUpdated
	if syncOperationBundlePhaseStatusFromContinuation(&opState, state, status) {
		updated = true
	}
	if syncOperationPhaseStatusFromContinuation(&opState, state, status) {
		updated = true
	}
	if strings.TrimSpace(opState.Proposal.ID) == opID && opState.Proposal.Status == session.ProposalStatusPending {
		opState.Proposal.Status = status
		opState.Proposal.UpdatedAt = time.Now().UTC()
		updated = true
	}
	if !updated {
		return nil
	}
	if status == session.ProposalStatusApproved {
		if planLeaseUpdated && continuationActionIsPlanLeaseApproval(state) {
			if state.ApprovalBundle.Active() {
				opState.Status = session.OperationStatusActive
				opState.Stage = "plan_lease_active"
			} else {
				opState.Status = session.OperationStatusBlocked
				opState.Stage = "plan_lease_approved"
			}
		} else {
			opState.Status = session.OperationStatusActive
		}
	} else if status == session.ProposalStatusDenied || status == session.ProposalStatusExpired || status == session.ProposalStatusSuperseded {
		opState.Status = session.OperationStatusBlocked
	}
	opState.UpdatedAt = time.Now().UTC()
	return r.store.UpdateOperationState(key, opState)
}

func syncOperationPlanLeaseStatusFromContinuation(opState *session.OperationState, state session.ContinuationState, status session.ProposalStatus) bool {
	if opState == nil {
		return false
	}
	*opState = session.NormalizeOperationState(*opState)
	state = session.NormalizeContinuationState(state)
	if !continuationActionIsPlanLeaseApproval(state) {
		return false
	}
	leaseID := strings.TrimSpace(state.ActionProposal.OperationID)
	if leaseID == "" {
		leaseID = strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-plan-lease-")
	}
	if leaseID == "" || strings.TrimSpace(opState.PlanLease.ID) != leaseID {
		return false
	}
	now := time.Now().UTC()
	switch status {
	case session.ProposalStatusApproved:
		if state.ApprovalBundle.Active() {
			opState.PlanLease.Status = session.PlanLeaseStatusActive
		} else {
			opState.PlanLease.Status = session.PlanLeaseStatusApproved
		}
		opState.PlanLease.ApprovedBy = firstNonZeroInt64(state.ContinuationLease.ApprovedBy, state.ApprovedBy)
		if !state.ContinuationLease.ApprovedAt.IsZero() {
			opState.PlanLease.ApprovedAt = state.ContinuationLease.ApprovedAt.UTC()
		} else {
			opState.PlanLease.ApprovedAt = now
		}
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = session.ProposalStatusApproved
			opState.Proposal.UpdatedAt = now
		}
	case session.ProposalStatusDenied:
		opState.PlanLease.Status = session.PlanLeaseStatusRevoked
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = session.ProposalStatusDenied
			opState.Proposal.UpdatedAt = now
		}
	case session.ProposalStatusExpired, session.ProposalStatusSuperseded:
		opState.PlanLease.Status = session.PlanLeaseStatusExpired
		if opState.Proposal.Status == session.ProposalStatusPending {
			opState.Proposal.Status = status
			opState.Proposal.UpdatedAt = now
		}
	}
	opState.PlanLease.UpdatedAt = now
	opState.UpdatedAt = now
	*opState = session.NormalizeOperationState(*opState)
	return true
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func syncOperationBundlePhaseStatusFromContinuation(opState *session.OperationState, state session.ContinuationState, status session.ProposalStatus) bool {
	if opState == nil {
		return false
	}
	*opState = session.NormalizeOperationState(*opState)
	state = session.NormalizeContinuationState(state)
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if strings.TrimSpace(bundle.ID) == "" || len(bundle.Phases) == 0 {
		return false
	}
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	currentPhaseID := strings.TrimSpace(bundle.CurrentPhaseID)
	if currentPhaseID == "" {
		currentPhaseID = firstContinuationBundlePhaseID(bundle.Phases)
	}
	bundleIDs := make(map[string]session.ContinuationApprovalBundlePhase, len(bundle.Phases))
	for _, phase := range bundle.Phases {
		if id := strings.TrimSpace(phase.OperationPhaseID); id != "" {
			bundleIDs[id] = phase
		}
	}
	if len(bundleIDs) == 0 {
		return false
	}
	updated := false
	for i := range opState.PhasePlan.Phases {
		phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
		bundlePhase, ok := bundleIDs[phaseID]
		if !ok {
			continue
		}
		switch status {
		case session.ProposalStatusApproved:
			if bundlePhase.Status == session.ContinuationLeaseStatusDeferred {
				opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
				opState.PhasePlan.Phases[i].LeaseID = ""
				break
			}
			opState.PhasePlan.Phases[i].LeaseID = leaseID
			if strings.TrimSpace(bundlePhase.ID) == currentPhaseID || currentPhaseID == "" {
				opState.PhasePlan.Phases[i].Status = session.PlanStatusInProgress
				opState.PhasePlan.CurrentPhaseID = phaseID
			} else if opState.PhasePlan.Phases[i].Status == "" {
				opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
			}
		case session.ProposalStatusDenied, session.ProposalStatusExpired, session.ProposalStatusSuperseded:
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
			opState.PhasePlan.Phases[i].LeaseID = ""
			if opState.PhasePlan.CurrentPhaseID == "" {
				opState.PhasePlan.CurrentPhaseID = phaseID
			}
		}
		updated = true
	}
	if updated {
		opState.PhasePlan.UpdatedAt = time.Now().UTC()
		*opState = session.NormalizeOperationState(*opState)
	}
	return updated
}

func syncOperationPhaseStatusFromContinuation(opState *session.OperationState, state session.ContinuationState, status session.ProposalStatus) bool {
	if opState == nil {
		return false
	}
	*opState = session.NormalizeOperationState(*opState)
	state = session.NormalizeContinuationState(state)
	opID := strings.TrimSpace(state.ActionProposal.OperationID)
	actionID := strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-")
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	updated := false
	for i := range opState.PhasePlan.Phases {
		phase := opState.PhasePlan.Phases[i]
		proposalID := operationPhaseProposalID(*opState, phase)
		if proposalID == "" {
			continue
		}
		matches := opID == proposalID || actionID == proposalID || strings.TrimSpace(state.DecisionID) == proposalID
		if !matches && leaseID != "" {
			matches = strings.TrimSpace(phase.LeaseID) == leaseID
		}
		if !matches {
			continue
		}
		switch status {
		case session.ProposalStatusApproved:
			opState.PhasePlan.Phases[i].Status = session.PlanStatusInProgress
			opState.PhasePlan.Phases[i].LeaseID = leaseID
			opState.PhasePlan.CurrentPhaseID = opState.PhasePlan.Phases[i].ID
		case session.ProposalStatusDenied, session.ProposalStatusExpired, session.ProposalStatusSuperseded:
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
			opState.PhasePlan.Phases[i].LeaseID = ""
			opState.PhasePlan.CurrentPhaseID = opState.PhasePlan.Phases[i].ID
		}
		opState.PhasePlan.UpdatedAt = time.Now().UTC()
		updated = true
		break
	}
	if updated {
		*opState = session.NormalizeOperationState(*opState)
	}
	return updated
}

func operationStateWithConsumedWorkContinuationPhaseCompleted(opState session.OperationState, state session.ContinuationState, now time.Time) (session.OperationState, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	priorStatus := opState.Status
	priorStage := strings.TrimSpace(opState.Stage)
	wasTerminal := operationStatusIsTerminal(priorStatus)
	if state.ContinuationLease.Status != session.ContinuationLeaseStatusConsumed ||
		state.ContinuationLease.RemainingTurns > 0 ||
		strings.TrimSpace(state.ContinuationLease.ID) == "" ||
		continuationWorkMode(state) == "" {
		return opState, false
	}
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	updated := false
	for i := range opState.PhasePlan.Phases {
		phase := normalizeSingleOperationPhase(opState.PhasePlan.Phases[i])
		if phase.Status != session.PlanStatusInProgress {
			continue
		}
		if strings.TrimSpace(phase.LeaseID) != leaseID && !operationPhaseMatchesConsumedContinuation(opState, phase, state) {
			continue
		}
		if !operationPhaseHasConsumedWorkCompletionEvidence(opState, phase, state) {
			continue
		}
		opState.PhasePlan.Phases[i].Status = session.PlanStatusCompleted
		if opState.PhasePlan.Phases[i].CompletedAt.IsZero() {
			opState.PhasePlan.Phases[i].CompletedAt = now
		}
		updated = true
		break
	}
	if !updated {
		return opState, false
	}
	if reconciled, reconciledDuplicates := operationStateWithCompletedPhaseDuplicatesReconciled(opState, now); reconciledDuplicates {
		opState = reconciled
	}
	if reconciled, clearedStaleLease := operationStateWithStalePlanLeaseCleared(opState, now); clearedStaleLease {
		opState = reconciled
	}
	if closed, completed := operationStateWithCompletedPhasePlanClosed(opState, now); completed {
		opState = closed
		return session.NormalizeOperationState(opState), true
	}
	if wasTerminal {
		opState.Status = priorStatus
		opState.Stage = firstNonEmptyContinuation(priorStage, string(priorStatus))
		if priorStatus == session.OperationStatusCompleted &&
			(opState.Proposal.Status == session.ProposalStatusPending || opState.Proposal.Status == session.ProposalStatusApproved) {
			opState.Proposal.Status = session.ProposalStatusSuperseded
			opState.Proposal.UpdatedAt = now
		}
		opState.PhasePlan.UpdatedAt = now
		opState.UpdatedAt = now
		return session.NormalizeOperationState(opState), true
	}
	opState.Status = session.OperationStatusActive
	opState.Stage = firstNonEmptyContinuation(strings.TrimSpace(opState.Stage), "phase_completed")
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState), true
}

func operationPhaseHasConsumedWorkCompletionEvidence(opState session.OperationState, phase session.OperationPhase, state session.ContinuationState) bool {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	state = session.NormalizeContinuationState(state)
	work := session.NormalizeWorkOperationMetadata(opState.Work)
	if work.LastCompletedAt.IsZero() || strings.TrimSpace(work.LastError) != "" {
		return false
	}
	leaseID := strings.TrimSpace(state.ContinuationLease.ID)
	if leaseID == "" || strings.TrimSpace(work.LastLeaseID) != leaseID {
		return false
	}
	if opID := strings.TrimSpace(opState.ID); opID != "" && strings.TrimSpace(work.LastOperationID) != "" && strings.TrimSpace(work.LastOperationID) != opID {
		return false
	}
	if mode := operationPhaseConsumedWorkMode(opState, phase, state); mode != "" && strings.TrimSpace(work.LastWorkMode) != "" && strings.TrimSpace(work.LastWorkMode) != string(mode) {
		return false
	}
	proposalID := operationPhaseProposalID(opState, phase)
	if actionOpID := strings.TrimSpace(work.LastActionOperationID); actionOpID != "" && actionOpID != proposalID {
		return false
	}
	actionProposalID := strings.TrimSpace(state.ActionProposal.ID)
	leaseProposalID := strings.TrimSpace(state.ContinuationLease.ProposalID)
	if recorded := strings.TrimSpace(work.LastActionProposalID); recorded != "" &&
		recorded != actionProposalID &&
		recorded != leaseProposalID {
		return false
	}
	return true
}

func operationPhaseConsumedWorkMode(opState session.OperationState, phase session.OperationPhase, state session.ContinuationState) WorkMode {
	if bundlePhase, ok := operationPhaseConsumedApprovalBundlePhase(opState, phase, state); ok {
		state.ApprovalBundle.CurrentPhaseID = strings.TrimSpace(bundlePhase.ID)
		return continuationWorkMode(state)
	}
	return continuationWorkMode(state)
}

func operationPhaseConsumedApprovalBundlePhase(opState session.OperationState, phase session.OperationPhase, state session.ContinuationState) (session.ContinuationApprovalBundlePhase, bool) {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	state = session.NormalizeContinuationState(state)
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) == 0 {
		return session.ContinuationApprovalBundlePhase{}, false
	}
	proposalID := operationPhaseProposalID(opState, phase)
	phaseID := strings.TrimSpace(phase.ID)
	for _, bundlePhase := range bundle.Phases {
		bundlePhase = session.NormalizeContinuationApprovalBundlePhase(bundlePhase)
		if proposalID != "" && strings.TrimSpace(bundlePhase.ID) == proposalID {
			return bundlePhase, true
		}
		if phaseID != "" && strings.TrimSpace(bundlePhase.OperationPhaseID) == phaseID {
			return bundlePhase, true
		}
	}
	return session.ContinuationApprovalBundlePhase{}, false
}

func operationPhaseMatchesConsumedContinuation(opState session.OperationState, phase session.OperationPhase, state session.ContinuationState) bool {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	state = session.NormalizeContinuationState(state)
	proposalID := operationPhaseProposalID(opState, phase)
	if proposalID == "" {
		return false
	}
	return strings.TrimSpace(state.ActionProposal.OperationID) == proposalID ||
		strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-") == proposalID ||
		strings.TrimSpace(state.ContinuationLease.ProposalID) == "aprop-"+proposalID
}

func operationStateWithCompletedPhaseDuplicatesReconciled(opState session.OperationState, now time.Time) (session.OperationState, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	if len(opState.PhasePlan.Phases) < 2 {
		return opState, false
	}

	type completedPhase struct {
		index int
		phase session.OperationPhase
	}
	completed := make([]completedPhase, 0, len(opState.PhasePlan.Phases))
	for i, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusCompleted {
			completed = append(completed, completedPhase{index: i, phase: phase})
		}
	}
	if len(completed) == 0 {
		return opState, false
	}

	updated := false
	for i := range opState.PhasePlan.Phases {
		candidate := normalizeSingleOperationPhase(opState.PhasePlan.Phases[i])
		if candidate.Status == session.PlanStatusCompleted {
			continue
		}
		for _, done := range completed {
			if !operationPhaseDuplicatesCompletedPhase(opState, candidate, done.phase) {
				continue
			}
			opState.PhasePlan.Phases[i].Status = session.PlanStatusCompleted
			opState.PhasePlan.Phases[i].StaleAuthority = true
			opState.PhasePlan.Phases[i].BlockedReasonCode = "superseded_phase"
			opState.PhasePlan.Phases[i].LeaseID = ""
			if opState.PhasePlan.Phases[i].CompletedAt.IsZero() {
				opState.PhasePlan.Phases[i].CompletedAt = firstNonZeroTime(done.phase.CompletedAt, now)
			}
			doneID := strings.TrimSpace(done.phase.ID)
			candidateID := strings.TrimSpace(candidate.ID)
			if doneID != "" && candidateID != "" && !stringSliceContains(opState.PhasePlan.Phases[done.index].SupersedesPhaseIDs, candidateID) {
				opState.PhasePlan.Phases[done.index].SupersedesPhaseIDs = append(opState.PhasePlan.Phases[done.index].SupersedesPhaseIDs, candidateID)
			}
			updated = true
			break
		}
	}
	if !updated {
		return opState, false
	}
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState), true
}

func operationStateWithCompletedPhasePlanClosed(opState session.OperationState, now time.Time) (session.OperationState, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	if !operationPhasePlanAllPhasesCompleted(opState.PhasePlan) || opState.Status == session.OperationStatusCompleted {
		return opState, false
	}
	if operationStateHasNonPhasePendingProposal(opState) {
		return opState, false
	}
	opState.Status = session.OperationStatusCompleted
	opState.Stage = "completed"
	if opState.Proposal.Status == session.ProposalStatusPending || opState.Proposal.Status == session.ProposalStatusApproved {
		opState.Proposal.Status = session.ProposalStatusSuperseded
		opState.Proposal.UpdatedAt = now
	}
	switch opState.PlanLease.Status {
	case session.PlanLeaseStatusProposed, session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive:
		opState.PlanLease.Status = session.PlanLeaseStatusCompleted
		opState.PlanLease.RemainingTurns = 0
		opState.PlanLease.UpdatedAt = now
	}
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState), true
}

func operationStateHasNonPhasePendingProposal(opState session.OperationState) bool {
	opState = session.NormalizeOperationState(opState)
	if !pendingOperationProposalNeedsButton(opState.Proposal) {
		return false
	}
	if operationProposalBelongsToPhasePlan(opState, opState.Proposal) {
		return false
	}
	if operationProposalMatchesPlanLease(opState.Proposal, opState.PlanLease) {
		return false
	}
	return true
}

func operationPhasePlanAllPhasesCompleted(plan session.OperationPhasePlan) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if len(plan.Phases) == 0 {
		return false
	}
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status != session.PlanStatusCompleted {
			return false
		}
	}
	return true
}

func operationPhaseDuplicatesCompletedPhase(opState session.OperationState, candidate session.OperationPhase, completed session.OperationPhase) bool {
	opState = session.NormalizeOperationState(opState)
	candidate = normalizeSingleOperationPhase(candidate)
	completed = normalizeSingleOperationPhase(completed)
	candidateID := strings.TrimSpace(candidate.ID)
	completedID := strings.TrimSpace(completed.ID)
	if candidateID == "" || completedID == "" || candidateID == completedID {
		return false
	}
	for _, supersededID := range completed.SupersedesPhaseIDs {
		if strings.TrimSpace(supersededID) == candidateID {
			return true
		}
	}
	completedProposalID := operationPhaseProposalID(opState, completed)
	if completedProposalID != "" && candidateID == completedProposalID {
		return true
	}
	if completedID != "" && strings.Contains(candidateID, completedID) && operationPhaseCoreEquivalent(candidate, completed) {
		return true
	}
	if completedProposalID != "" && strings.Contains(candidateID, completedProposalID) && operationPhaseCoreEquivalent(candidate, completed) {
		return true
	}
	return false
}

func operationPhaseCoreEquivalent(a session.OperationPhase, b session.OperationPhase) bool {
	a = normalizeSingleOperationPhase(a)
	b = normalizeSingleOperationPhase(b)
	if normalizeOperationPhaseReasonCode(a.Summary) != normalizeOperationPhaseReasonCode(b.Summary) {
		return false
	}
	return strings.TrimSpace(a.AuthorityClass) == strings.TrimSpace(b.AuthorityClass)
}

func operationStateWithStalePlanLeaseCleared(opState session.OperationState, now time.Time) (session.OperationState, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	if !opState.PlanLease.Active() || len(opState.PlanLease.CoveredPhaseIDs) == 0 {
		return opState, false
	}
	switch opState.PlanLease.Status {
	case session.PlanLeaseStatusProposed, session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive:
	default:
		return opState, false
	}
	covered := make(map[string]struct{}, len(opState.PlanLease.CoveredPhaseIDs))
	for _, id := range opState.PlanLease.CoveredPhaseIDs {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			covered[trimmed] = struct{}{}
		}
	}
	if len(covered) == 0 {
		return opState, false
	}
	foundCovered := false
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if _, ok := covered[strings.TrimSpace(phase.ID)]; !ok {
			continue
		}
		foundCovered = true
		if phase.Status == session.PlanStatusCompleted {
			continue
		}
		if operationPhaseApprovalExcludedReason(opState.PhasePlan, phase) != "" {
			continue
		}
		if operationPhaseEligibleForPlanBudget(phase) || operationPhaseNeedsStandaloneApproval(opState, phase) {
			return opState, false
		}
	}
	if !foundCovered {
		return opState, false
	}
	opState.PlanLease.Status = session.PlanLeaseStatusCompleted
	opState.PlanLease.RemainingTurns = 0
	opState.PlanLease.UpdatedAt = now
	if opState.Proposal.Status == session.ProposalStatusPending && operationProposalMatchesPlanLease(opState.Proposal, opState.PlanLease) {
		opState.Proposal.Status = session.ProposalStatusSuperseded
		opState.Proposal.UpdatedAt = now
	}
	if opState.Stage == "plan_lease_approval" {
		opState.Stage = "stale_plan_lease_repaired"
	}
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState), true
}

func operationProposalMatchesPlanLease(proposal session.OperationProposal, lease session.OperationPlanLease) bool {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	lease = session.NormalizeOperationPlanLease(lease)
	proposalID := strings.TrimSpace(proposal.ID)
	if proposalID == "" {
		return false
	}
	return proposalID == strings.TrimSpace(lease.ID) || proposalID == operationPlanLeaseProposalID(lease)
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

func stringSliceContains(values []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}
