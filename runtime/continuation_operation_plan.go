//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func pendingOperationProposalNeedsButton(proposal session.OperationProposal) bool {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	return proposal.Active() && proposal.Status == session.ProposalStatusPending && strings.TrimSpace(proposal.ID) != "" && strings.TrimSpace(proposal.Summary) != ""
}

func pendingOperationPlanLeaseNeedsButton(lease session.OperationPlanLease) bool {
	lease = session.NormalizeOperationState(session.OperationState{PlanLease: lease}).PlanLease
	if !lease.Active() || lease.Status != session.PlanLeaseStatusProposed || strings.TrimSpace(lease.ID) == "" {
		return false
	}
	return strings.TrimSpace(lease.Summary) != "" ||
		strings.TrimSpace(lease.Objective) != "" ||
		len(lease.Lanes) > 0 ||
		len(lease.AllowedActions) > 0 ||
		len(lease.ValidationGates) > 0
}

func operationPlanLeaseFromPhasePlan(opState session.OperationState, now time.Time) (session.OperationPlanLease, bool) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	if opState.PlanLease.Active() || len(opState.PhasePlan.Phases) == 0 {
		return session.OperationPlanLease{}, false
	}
	if operationPhasePlanHasBlockingInProgress(opState.PhasePlan) {
		return session.OperationPlanLease{}, false
	}
	start := operationPhasePlanStartIndex(opState.PhasePlan)
	pendingCount := operationPhasePlanBudgetLaneCountFrom(opState.PhasePlan, start)
	phases := make([]session.OperationPhase, 0, operationPlanBudgetMaxLanes)
	stoppedAtGate := ""
phaseLoop:
	for i := start; i < len(opState.PhasePlan.Phases) && len(phases) < operationPlanBudgetMaxLanes; i++ {
		phase := normalizeSingleOperationPhase(opState.PhasePlan.Phases[i])
		if operationPhasePlanPhaseIsStaleInProgress(opState.PhasePlan, phase) {
			continue
		}
		if operationPhaseApprovalExcludedReason(opState.PhasePlan, phase) != "" {
			continue
		}
		if phase.Status == session.PlanStatusCompleted {
			continue
		}
		switch operationPhaseApprovalKindFor(phase) {
		case operationPhaseApprovalNone:
			continue
		case operationPhaseApprovalBlocked:
			reason := operationPhaseApprovalBlockedReason(phase)
			if reason == "" {
				reason = "blocked before plan budget"
			}
			stoppedAtGate = reason
			break phaseLoop
		case operationPhaseApprovalFresh:
			if operationPhaseFreshGateCanJoinPlanBudget(phase) {
				phases = append(phases, phase)
				continue
			}
			stoppedAtGate = operationPhaseStopGateLabel(phase)
			break phaseLoop
		case operationPhaseApprovalPlanBudget:
			phases = append(phases, phase)
		}
	}
	if len(phases) == 0 {
		return session.OperationPlanLease{}, false
	}
	if pendingCount < 2 && stoppedAtGate == "" && !phases[0].RequiresApproval {
		return session.OperationPlanLease{}, false
	}
	lease := session.OperationPlanLease{
		ID:               operationPhasePlanLeaseID(opState, phases),
		OperatorTitle:    continuationPlanTitleFromText(operationPhasePlanLeaseSummary(opState, phases)),
		PlanTitle:        continuationPlanTitleFromText(firstNonEmptyContinuation(opState.PhasePlan.Goal, opState.Objective, opState.Summary)),
		Summary:          operationPhasePlanLeaseSummary(opState, phases),
		Objective:        firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, opState.Summary, "Continue the approved operation plan."),
		OperationID:      strings.TrimSpace(opState.ID),
		Status:           session.PlanLeaseStatusProposed,
		TurnBudget:       operationPhasePlanLeaseTurnBudget(phases),
		CoveredPhaseIDs:  operationPhaseIDs(phases),
		Lanes:            operationPlanLeaseLanesFromPhases(phases),
		AllowedActions:   []string{"execute_plan_budget_lanes", "use_existing_authority_only", "update_operation_phase_plan", "report_milestone_evidence"},
		ForbiddenActions: []string{"work_outside_plan_budget", "silent_escalation", "skip_stop_gate", "credentials_or_tokens", "external_send_or_contact", "archive_delete_or_mutate_source_data", "deploy_restart_without_explicit_approval"},
		ValidationGates:  []string{"report evidence at meaningful milestones and completion", "stop if the next action is outside the disclosed plan budget"},
		ExitConditions:   []string{"turn budget is spent", "covered phases are complete", "a stop condition appears", "operator pauses or revokes"},
		ExpiresAt:        now.Add(12 * time.Hour),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if stoppedAtGate != "" {
		lease.HardInterrupts = []string{stoppedAtGate}
	}
	lease = session.NormalizeOperationPlanLease(lease)
	return lease, true
}

func operationPhasePlanStartIndex(plan session.OperationPhasePlan) int {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for i, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				return i
			}
		}
	}
	for i, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusPending || phase.Status == "" {
			return i
		}
	}
	return 0
}

func operationPhasePlanBudgetLaneCountFrom(plan session.OperationPhasePlan, start int) int {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if start < 0 {
		start = 0
	}
	count := 0
	for i := start; i < len(plan.Phases); i++ {
		if operationPhaseEligibleForPlanBudget(plan.Phases[i]) {
			count++
		}
	}
	return count
}

func operationPhasePlanHasBlockingInProgress(plan session.OperationPhasePlan) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	if len(plan.Phases) == 0 {
		return false
	}
	currentID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentID != "" {
		for _, phase := range plan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if strings.TrimSpace(phase.ID) != currentID {
				continue
			}
			return phase.Status == session.PlanStatusInProgress
		}
	}
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusInProgress {
			return true
		}
	}
	return false
}

func operationPhasePlanPhaseIsStaleInProgress(plan session.OperationPhasePlan, phase session.OperationPhase) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	phase = normalizeSingleOperationPhase(phase)
	currentID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentID == "" {
		return false
	}
	return phase.Status == session.PlanStatusInProgress && strings.TrimSpace(phase.ID) != currentID
}

func operationStateWithNonCurrentInProgressPhasesCleared(opState session.OperationState, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID)
	if currentID == "" {
		return opState
	}
	currentStatus := session.PlanStatus("")
	for _, phase := range opState.PhasePlan.Phases {
		if strings.TrimSpace(phase.ID) == currentID {
			currentStatus = phase.Status
			break
		}
	}
	if currentStatus == session.PlanStatusInProgress {
		return opState
	}
	changed := false
	for i := range opState.PhasePlan.Phases {
		if strings.TrimSpace(opState.PhasePlan.Phases[i].ID) == currentID {
			continue
		}
		if opState.PhasePlan.Phases[i].Status != session.PlanStatusInProgress {
			continue
		}
		opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		opState.PhasePlan.Phases[i].LeaseID = ""
		changed = true
	}
	if changed {
		opState.PhasePlan.UpdatedAt = now
		opState.UpdatedAt = now
	}
	return session.NormalizeOperationState(opState)
}

func operationStateWithInactiveCurrentPhaseLeaseCleared(opState session.OperationState, cont session.ContinuationState, contExists bool, now time.Time) session.OperationState {
	if !contExists {
		return session.NormalizeOperationState(opState)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	cont = session.NormalizeContinuationState(cont)
	leaseID := strings.TrimSpace(cont.ContinuationLease.ID)
	if leaseID == "" || !continuationStateLeaseInactiveForPhaseRecovery(cont) {
		return opState
	}
	currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID)
	if currentID == "" {
		return opState
	}
	changed := false
	for i := range opState.PhasePlan.Phases {
		phase := opState.PhasePlan.Phases[i]
		if strings.TrimSpace(phase.ID) != currentID || strings.TrimSpace(phase.LeaseID) != leaseID || phase.Status != session.PlanStatusInProgress {
			continue
		}
		opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		opState.PhasePlan.Phases[i].LeaseID = ""
		changed = true
		break
	}
	if !changed {
		return opState
	}
	if opState.Proposal.Status == session.ProposalStatusApproved && operationProposalMatchesContinuation(opState.Proposal, cont) {
		opState.Proposal.Status = session.ProposalStatusSuperseded
		opState.Proposal.UpdatedAt = now
	}
	opState.Status = session.OperationStatusBlocked
	opState.Stage = firstNonEmptyContinuation(strings.TrimSpace(opState.Stage), "phase_approval_recovered_from_inactive_lease")
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}

func continuationStateLeaseInactiveForPhaseRecovery(cont session.ContinuationState) bool {
	cont = session.NormalizeContinuationState(cont)
	switch cont.Status {
	case session.ContinuationStatusRevoked:
		return true
	}
	switch cont.ContinuationLease.Status {
	case session.ContinuationLeaseStatusRevoked, session.ContinuationLeaseStatusExpired:
		return true
	default:
		return false
	}
}

func operationPhasePlanLeaseID(opState session.OperationState, phases []session.OperationPhase) string {
	base := firstNonEmptyContinuation(opState.ID, opState.PhasePlan.ID, "operation")
	firstID := "plan"
	lastID := "budget"
	if len(phases) > 0 {
		firstID = firstNonEmptyContinuation(phases[0].ID, phases[0].Summary, "first")
		lastID = firstNonEmptyContinuation(phases[len(phases)-1].ID, phases[len(phases)-1].Summary, "last")
	}
	id := sanitizeOperationPhaseProposalID("plan-budget-" + base + "-" + firstID + "-to-" + lastID)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func operationPhasePlanLeaseSummary(opState session.OperationState, phases []session.OperationPhase) string {
	if len(phases) == 0 {
		return "Approve plan budget"
	}
	goal := firstNonEmptyContinuation(opState.PhasePlan.Goal, opState.Objective, opState.Summary)
	firstIndex := operationPhaseIndex(opState.PhasePlan, phases[0].ID)
	lastIndex := operationPhaseIndex(opState.PhasePlan, phases[len(phases)-1].ID)
	if firstIndex <= 0 {
		firstIndex = 1
	}
	if lastIndex <= 0 {
		lastIndex = firstIndex + len(phases) - 1
	}
	label := fmt.Sprintf("Approve plan budget: phases %d-%d", firstIndex, lastIndex)
	if firstIndex == lastIndex {
		label = fmt.Sprintf("Approve plan budget: phase %d", firstIndex)
	}
	if goal != "" {
		label += " for " + goal
	}
	return label
}

func operationPhaseIndex(plan session.OperationPhasePlan, phaseID string) int {
	phaseID = strings.TrimSpace(phaseID)
	for i, phase := range plan.Phases {
		if strings.TrimSpace(phase.ID) == phaseID {
			return i + 1
		}
	}
	return 0
}

func operationPhasePlanLeaseTurnBudget(phases []session.OperationPhase) int {
	turns := 0
	for range phases {
		turns++
	}
	if turns <= 0 {
		return 1
	}
	return turns
}

func operationPhaseIDs(phases []session.OperationPhase) []string {
	out := make([]string, 0, len(phases))
	for _, phase := range phases {
		if id := strings.TrimSpace(phase.ID); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func operationPlanLeaseLanesFromPhases(phases []session.OperationPhase) []session.OperationPlanLeaseLane {
	lanes := make([]session.OperationPlanLeaseLane, 0, len(phases))
	for _, phase := range phases {
		phase = normalizeSingleOperationPhase(phase)
		lanes = append(lanes, session.OperationPlanLeaseLane{
			ID:               strings.TrimSpace(phase.ID),
			OperatorTitle:    firstNonEmptyContinuation(phase.OperatorTitle, phase.PlanTitle, continuationPlanTitleFromText(phase.Summary)),
			PlanTitle:        firstNonEmptyContinuation(phase.PlanTitle, phase.OperatorTitle, continuationPlanTitleFromText(phase.Summary)),
			Summary:          strings.TrimSpace(phase.Summary),
			AuthorityClass:   strings.TrimSpace(phase.AuthorityClass),
			ExpectedTurns:    1,
			AllowedActions:   append([]string(nil), phase.AllowedActions...),
			ForbiddenActions: append([]string(nil), phase.ForbiddenActions...),
		})
	}
	return lanes
}

func operationPhaseStopGateLabel(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	return firstNonEmptyContinuation(phase.AuthorityClass, phase.Summary, "fresh approval gate")
}

func operationProposalMatchesContinuation(proposal session.OperationProposal, state session.ContinuationState) bool {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	state = session.NormalizeContinuationState(state)
	proposalID := strings.TrimSpace(proposal.ID)
	if proposalID == "" {
		return false
	}
	return strings.TrimSpace(state.ActionProposal.OperationID) == proposalID || strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-") == proposalID || strings.TrimSpace(state.DecisionID) == proposalID
}

func operationPlanLeaseMatchesContinuation(lease session.OperationPlanLease, state session.ContinuationState) bool {
	lease = session.NormalizeOperationState(session.OperationState{PlanLease: lease}).PlanLease
	state = session.NormalizeContinuationState(state)
	leaseID := strings.TrimSpace(lease.ID)
	if leaseID == "" {
		return false
	}
	return strings.TrimSpace(state.ActionProposal.OperationID) == leaseID ||
		strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-") == operationPlanLeaseProposalID(lease) ||
		strings.TrimSpace(state.DecisionID) == operationPlanLeaseProposalID(lease) ||
		strings.TrimSpace(state.ContinuationLease.ID) == "lease-"+operationPlanLeaseProposalID(lease)
}

func nextOperationPhaseForApproval(opState session.OperationState) (session.OperationPhase, bool) {
	opState = session.NormalizeOperationState(opState)
	plan := opState.PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}, false
	}
	if operationPhasePlanHasBlockingInProgress(plan) {
		return session.OperationPhase{}, false
	}
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for _, phase := range plan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if strings.TrimSpace(phase.ID) == currentID && operationPhaseNeedsStandaloneApproval(opState, phase) {
				return phase, true
			}
		}
	}
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if operationPhasePlanPhaseIsStaleInProgress(plan, phase) {
			continue
		}
		if operationPhaseNeedsStandaloneApproval(opState, phase) {
			return phase, true
		}
	}
	return session.OperationPhase{}, false
}

func nextOperationPhaseBundleForApproval(opState session.OperationState) ([]session.OperationPhase, bool) {
	opState = session.NormalizeOperationState(opState)
	plan := opState.PhasePlan
	if len(plan.Phases) < 2 {
		return nil, false
	}
	if operationPhasePlanHasBlockingInProgress(plan) {
		return nil, false
	}
	start := 0
	currentFound := false
	if currentID := strings.TrimSpace(plan.CurrentPhaseID); currentID != "" {
		for i, phase := range plan.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				start = i
				currentFound = true
				break
			}
		}
	}
	bundle := operationPhaseBundleForApprovalFrom(opState, start)
	if len(bundle) < 2 && start > 0 && !currentFound {
		bundle = operationPhaseBundleForApprovalFrom(opState, 0)
	}
	if len(bundle) < 2 {
		return nil, false
	}
	return bundle, true
}

func operationPhaseBundleForApprovalFrom(opState session.OperationState, start int) []session.OperationPhase {
	opState = session.NormalizeOperationState(opState)
	plan := opState.PhasePlan
	if start < 0 {
		start = 0
	}
	bundle := make([]session.OperationPhase, 0, operationApprovalBundleMaxPhases)
	for i := start; i < len(plan.Phases) && len(bundle) < operationApprovalBundleMaxPhases; i++ {
		phase := normalizeSingleOperationPhase(plan.Phases[i])
		if operationPhasePlanPhaseIsStaleInProgress(plan, phase) {
			continue
		}
		if operationPhaseApprovalExcludedReason(plan, phase) != "" {
			continue
		}
		if phase.Status == session.PlanStatusCompleted {
			continue
		}
		if !operationPhaseEligibleForPlanBudget(phase) {
			break
		}
		if operationPlanLeaseCoversPhaseAsBudget(opState.PlanLease, phase) {
			break
		}
		if !operationPhaseBundleCanAdd(bundle, phase) {
			break
		}
		candidate := append(append([]session.OperationPhase(nil), bundle...), phase)
		if operationPhaseBundleInvalidReason(opState, candidate) != "" {
			break
		}
		bundle = candidate
	}
	return bundle
}

func operationPhaseBundleInvalidReason(opState session.OperationState, phases []session.OperationPhase) string {
	if len(phases) == 0 {
		return ""
	}
	opState = session.NormalizeOperationState(opState)
	bundle := session.ContinuationApprovalBundle{
		Phases: continuationApprovalBundlePhasesFromOperation(opState, phases),
	}
	return continuationApprovalBundleInvalidReason(opState.PhasePlan, bundle)
}

func operationPhaseBundleCanAdd(bundle []session.OperationPhase, phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if len(bundle) == 0 {
		return true
	}
	want := operationPhaseApprovalFamily(bundle[0])
	got := operationPhaseApprovalFamily(phase)
	if want == "" || got == "" {
		return want == got
	}
	return want == got
}

func operationPhaseApprovalFamily(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	class := session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect)
	switch class {
	case session.ContinuationLeaseClassLocalWorkspace:
		return "local_workspace"
	case session.ContinuationLeaseClassDataAccess:
		return "data_access"
	case session.ContinuationLeaseClassChildWake:
		return "child_wake"
	case session.ContinuationLeaseClassCapabilityGrant:
		return "capability_grant"
	case session.ContinuationLeaseClassDeployRestart:
		return "deploy_restart"
	default:
		return ""
	}
}

func operationPhaseApprovalExcludedReason(plan session.OperationPhasePlan, phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	if operationPhasePlanPhaseIsStaleInProgress(plan, phase) {
		return "stale non-current in-progress phase"
	}
	if operationPhaseSupersededByPlan(plan, phase) {
		return "superseded by newer phase"
	}
	if phase.Status == session.PlanStatusCompleted {
		return "completed phase"
	}
	if phase.StaleAuthority || operationPhaseReasonCodeIsStaleAuthority(phase.BlockedReasonCode) {
		return "superseded or stale phase"
	}
	return ""
}

func operationPhaseSupersededByPlan(plan session.OperationPhasePlan, phase session.OperationPhase) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	phaseID := strings.TrimSpace(phase.ID)
	if phaseID == "" {
		return false
	}
	for _, candidate := range plan.Phases {
		candidate = normalizeSingleOperationPhase(candidate)
		if strings.TrimSpace(candidate.ID) == phaseID {
			continue
		}
		for _, supersededID := range candidate.SupersedesPhaseIDs {
			if strings.TrimSpace(supersededID) == phaseID {
				return true
			}
		}
	}
	return false
}

func operationPhaseApprovalBlockedReason(phase session.OperationPhase) string {
	gate := operationPhaseApprovalGate(phase)
	if gate.Level == operationGateLevelHardConsentBlock {
		return strings.TrimSpace(gate.BlockedReason)
	}
	return ""
}

func operationPhaseReasonCodeIsStaleAuthority(code string) bool {
	switch normalizeOperationPhaseReasonCode(code) {
	case "stale_authority", "superseded", "superseded_phase", "stale_phase", "old_authority", "old_lease":
		return true
	default:
		return false
	}
}

func normalizeOperationPhaseReasonCode(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	replacer := strings.NewReplacer("-", "_", " ", "_", "/", "_", ".", "_")
	code = replacer.Replace(code)
	for strings.Contains(code, "__") {
		code = strings.ReplaceAll(code, "__", "_")
	}
	return strings.Trim(code, "_")
}

func operationPhasePlanOwnsContinuation(plan session.OperationPhasePlan) bool {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	return len(plan.Phases) > 0
}

func operationProposalBelongsToPhasePlan(opState session.OperationState, proposal session.OperationProposal) bool {
	opState = session.NormalizeOperationState(opState)
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	proposalID := strings.TrimSpace(proposal.ID)
	if proposalID == "" || len(opState.PhasePlan.Phases) == 0 {
		return false
	}
	for _, phase := range opState.PhasePlan.Phases {
		if proposalID == operationPhaseProposalID(opState, phase) {
			return true
		}
	}
	return false
}

func operationPhaseApprovalKindFor(phase session.OperationPhase) operationPhaseApprovalKind {
	phase = normalizeSingleOperationPhase(phase)
	if !phase.Active() || phase.Status != session.PlanStatusPending {
		return operationPhaseApprovalNone
	}
	if operationPhaseApprovalBlockedReason(phase) != "" {
		return operationPhaseApprovalBlocked
	}
	if operationPhaseRequiresFreshApprovalGate(phase) {
		return operationPhaseApprovalFresh
	}
	if phase.RequiresApproval {
		return operationPhaseApprovalPlanBudget
	}
	if operationPhaseHasPlanMaterial(phase) {
		return operationPhaseApprovalPlanBudget
	}
	return operationPhaseApprovalNone
}

func operationPhaseEligibleForPlanBudget(phase session.OperationPhase) bool {
	switch operationPhaseApprovalKindFor(phase) {
	case operationPhaseApprovalPlanBudget:
		return true
	case operationPhaseApprovalFresh:
		return operationPhaseFreshGateCanJoinPlanBudget(phase)
	default:
		return false
	}
}

func operationPhaseFreshGateCanJoinPlanBudget(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	if operationPhaseApprovalBlockedReason(phase) != "" {
		return false
	}
	if operationPhasePlanBudgetHardStopReason(phase) != "" {
		return false
	}
	gate := operationPhaseApprovalGate(phase)
	if gate.Level == operationGateLevelEscalatedOperatorApproval {
		return true
	}
	class := session.InferContinuationLeaseClass(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect)
	switch class {
	case session.ContinuationLeaseClassLocalWorkspace, session.ContinuationLeaseClassDataAccess, session.ContinuationLeaseClassChildWake:
		return true
	default:
		return false
	}
}

func operationPhasePlanBudgetHardStopReason(phase session.OperationPhase) string {
	phase = normalizeSingleOperationPhase(phase)
	hardCodes := map[string]string{
		"deploy":                      "deploy/restart",
		"live_deploy":                 "deploy/restart",
		"run_deploy":                  "deploy/restart",
		"restart":                     "deploy/restart",
		"restart_service":             "deploy/restart",
		"service_restart":             "deploy/restart",
		"systemctl_restart":           "deploy/restart",
		"park_restart":                "deploy/restart",
		"install_user_service":        "deploy/restart",
		"make_install_user_service":   "deploy/restart",
		"reinstall":                   "deploy/restart",
		"system_change":               "system change",
		"policy_apply":                "policy or permission change",
		"grant_or_revoke_capability":  "policy or permission change",
		"capability_grant":            "policy or permission change",
		"capability_revoke":           "policy or permission change",
		"mailbox_access":              "mailbox access",
		"mailbox_mutation":            "mailbox mutation",
		"mailbox_read":                "mailbox read",
		"email_read":                  "mailbox read",
		"external_account_email_read": "mailbox read",
		"external_account_email_read_public_web_read": "mailbox read",
		"credential_access":                           "credential access",
		"read_credentials_or_tokens":                  "credential access",
		"external_account_action":                     "external account action",
		"private_data_intake":                         "private data intake",
		"resource_owner_data_intake":                  "private data intake",
		"resource_owner_profile_intake":               "private data intake",
		"private_profile_intake":                      "private data intake",
		"profile_evaluation_rubric":                   "private data intake",
		"cv_ingestion":                                "private data intake",
		"private_material_processing":                 "private data intake",
		"rank_private_material":                       "private data intake",
		"scout_public_opportunities":                  "private data intake",
		"purchase":                                    "purchase/spend",
		"spend":                                       "purchase/spend",
		"public_contact":                              "public contact",
		"public_posting":                              "public posting",
		"communication":                               "communication",
		"push":                                        "remote push",
		"git_push":                                    "remote push",
		"push_remote":                                 "remote push",
	}
	for _, code := range operationPhaseStructuredCodes(phase) {
		if label, ok := hardCodes[code]; ok {
			return label
		}
	}
	return ""
}

func operationPhaseNeedsStandaloneApproval(opState session.OperationState, phase session.OperationPhase) bool {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	if operationPhaseApprovalExcludedReason(opState.PhasePlan, phase) != "" {
		return false
	}
	switch operationPhaseApprovalKindFor(phase) {
	case operationPhaseApprovalBlocked:
		return true
	case operationPhaseApprovalFresh:
		if operationPlanLeaseCoversPhaseAsBudget(opState.PlanLease, phase) && operationPhaseFreshGateCanJoinPlanBudget(phase) {
			return false
		}
		return true
	case operationPhaseApprovalPlanBudget:
		return false
	default:
		return false
	}
}

func operationPlanLeaseCoversPhaseAsBudget(lease session.OperationPlanLease, phase session.OperationPhase) bool {
	lease = session.NormalizeOperationPlanLease(lease)
	phase = normalizeSingleOperationPhase(phase)
	switch lease.Status {
	case session.PlanLeaseStatusActive, session.PlanLeaseStatusApproved:
	default:
		return false
	}
	phaseID := strings.TrimSpace(phase.ID)
	if phaseID == "" {
		return false
	}
	for _, coveredID := range lease.CoveredPhaseIDs {
		if strings.TrimSpace(coveredID) == phaseID {
			return true
		}
	}
	for _, lane := range lease.Lanes {
		if strings.TrimSpace(lane.ID) == phaseID {
			return true
		}
	}
	return false
}

func operationPhaseHasPlanMaterial(phase session.OperationPhase) bool {
	phase = normalizeSingleOperationPhase(phase)
	return strings.TrimSpace(phase.Summary) != "" ||
		strings.TrimSpace(phase.AuthorityClass) != "" ||
		strings.TrimSpace(phase.BoundedEffect) != "" ||
		len(phase.AllowedActions) > 0 ||
		len(phase.ForbiddenActions) > 0 ||
		len(phase.ValidationPlan) > 0
}

func operationPhaseMatchesContinuation(opState session.OperationState, phase session.OperationPhase, state session.ContinuationState) bool {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	proposalID := operationPhaseProposalID(opState, phase)
	if proposalID == "" {
		return false
	}
	if strings.TrimSpace(state.ActionProposal.OperationID) == proposalID ||
		strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-") == proposalID ||
		strings.TrimSpace(state.DecisionID) == proposalID {
		return true
	}
	leaseID := strings.TrimSpace(phase.LeaseID)
	return leaseID != "" && leaseID == strings.TrimSpace(state.ContinuationLease.ID)
}

func operationPhaseBundleMatchesContinuation(opState session.OperationState, phases []session.OperationPhase, state session.ContinuationState) bool {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	bundleID := operationPhaseBundleID(opState, phases)
	if bundleID == "" {
		return false
	}
	if strings.TrimSpace(state.ApprovalBundle.ID) == bundleID || strings.TrimSpace(state.ActionProposal.OperationID) == bundleID || strings.TrimSpace(state.DecisionID) == bundleID {
		return true
	}
	return false
}
