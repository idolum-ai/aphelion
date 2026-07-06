//go:build linux

package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) reconciledContinuationStateFromInvalidAuthority(state session.ContinuationState, compilation session.AuthorityContractCompilation, now time.Time) (session.ContinuationState, bool) {
	if compilation.Valid() || len(compilation.Contradictions) == 0 {
		return session.ContinuationState{}, false
	}
	state = session.NormalizeContinuationState(state)
	if state.ApprovalBundle.Active() {
		return session.ContinuationState{}, false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	allowedToRemove := map[string]struct{}{}
	for _, contradiction := range compilation.Contradictions {
		switch strings.TrimSpace(contradiction.Reason) {
		case session.AuthorityContradictionReasonProposalRequiresForbiddenGitPush:
			return session.ContinuationState{}, false
		case "allowed_action_implies_forbidden_authority", "allowed_action_exactly_forbidden":
		default:
			continue
		}
		if normalized := normalizeContinuationAuthorityAction(contradiction.AllowedAction); normalized != "" {
			allowedToRemove[normalized] = struct{}{}
		}
	}
	if len(allowedToRemove) == 0 {
		return session.ContinuationState{}, false
	}

	action := state.ActionProposal
	reconciledAllowed := make([]string, 0, len(action.AllowedActions))
	removed := false
	for _, allowed := range action.AllowedActions {
		if _, ok := allowedToRemove[normalizeContinuationAuthorityAction(allowed)]; ok {
			removed = true
			continue
		}
		reconciledAllowed = append(reconciledAllowed, allowed)
	}
	if !removed || len(reconciledAllowed) == 0 {
		return session.ContinuationState{}, false
	}

	turns := state.RemainingTurns
	if turns <= 0 {
		turns = state.ContinuationLease.MaxTurns
	}
	if turns <= 0 {
		turns = 1
	}
	decisionID := newContinuationDecisionID()
	action.ID = "aprop-" + decisionID
	action.AllowedActions = reconciledAllowed
	if continuationStateHasExternalAccountGrant(state) && !allowedActionsIncludeLocalRepoMutation(reconciledAllowed) {
		action.RiskClass = "external_account_action"
	} else {
		action.RiskClass = safestContinuationRiskClassForAllowedActions(action.RiskClass, reconciledAllowed)
	}
	action.Status = session.ProposalStatusPending
	action.CreatedAt = now
	action.UpdatedAt = now
	action.ExpiresAt = now.Add(continuationLeaseDefaultTTL)
	action = applyContinuationLeaseClassBoundaries(action)
	action.PlanHash = actionProposalHash(action)
	action = session.NormalizeActionProposal(action)

	reconciled := state
	reconciled.Status = session.ContinuationStatusPending
	reconciled.DecisionID = decisionID
	reconciled.RemainingTurns = turns
	reconciled.HandshakeBlockedReason = ""
	reconciled.ParkedAt = time.Time{}
	reconciled.ParkedReason = ""
	reconciled.ParkedSource = ""
	reconciled.ActionProposal = action
	reconciled.ContinuationLease = buildContinuationLease(action, turns, now)
	reconciled.ContinuationLease.RequiredCapabilityGrants = continuationRequiredCapabilityGrantSpecs(state)
	reconciled.UpdatedAt = now
	reconciled = session.NormalizeContinuationState(reconciled)
	if continuationAuthorityCompilation(reconciled).Invalid() {
		return session.ContinuationState{}, false
	}
	return reconciled, true
}

func allowedActionsIncludeLocalRepoMutation(actions []string) bool {
	switch workModeFromStructuredAuthorityList(actions) {
	case WorkModeWorkspaceWrite, WorkModeCommit, WorkModeDeploy:
		return true
	default:
		return false
	}
}

func continuationStateHasExternalAccountGrant(state session.ContinuationState) bool {
	for _, grant := range continuationRequiredCapabilityGrantSpecs(state) {
		if grant.Kind == session.CapabilityKindExternalAccount {
			return true
		}
	}
	return false
}

func safestContinuationRiskClassForAllowedActions(current string, allowed []string) string {
	mode := workModeFromStructuredAuthorityList(allowed)
	switch mode {
	case WorkModeDeploy:
		return "deploy"
	case WorkModeCommit:
		return "commit"
	case WorkModeWorkspaceWrite:
		return "workspace_write"
	case WorkModeReadOnly:
		return "read_only_review"
	default:
		if workModeFromStructuredAuthority(current) == WorkModeDeploy || workModeFromStructuredAuthority(current) == WorkModeCommit {
			return "read_only_review"
		}
		return strings.TrimSpace(current)
	}
}

func (r *Runtime) materializeReconciledAuthorityApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, state session.ContinuationState, compilation session.AuthorityContractCompilation, source string, now time.Time) (session.OperationState, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	source = strings.TrimSpace(source)

	switch source {
	case "operation_plan_lease":
		opState = operationStateWithMaterializedPlanLease(opState, state, now)
	case "operation_phase_bundle":
		if phases := operationPhasesForApprovalBundle(opState, state.ApprovalBundle); len(phases) > 0 {
			opState = operationStateWithMaterializedPhaseBundleLease(opState, phases, state, now)
		}
	case "operation_phase_plan", "operation_phase_required_capability":
		if phaseID := operationPhaseIDForContinuationState(opState, state); phaseID != "" {
			opState = operationStateWithMaterializedPhaseLease(opState, phaseID, state, now)
		}
	default:
		if opState.Proposal.Active() {
			opState.Proposal.Status = session.ProposalStatusPending
			opState.Proposal.UpdatedAt = now
		}
	}
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return opState, err
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return opState, err
	}
	phase, _ := operationPhaseFromInvalidMaterializedAuthority(opState, state, source)
	r.recordContinuationCompileRepaired(key, opState, phase, state, compilation, "invalid_authority_contract", continuationCompileRepairAuthorityContract, source, map[string]any{
		"repair_strategy": "remove_contradictory_allowed_actions",
		"user_visible":    false,
	}, now)
	payload := continuationExecutionPayload(state)
	payload["materialized_from"] = firstNonEmptyContinuation(source, "authority_reconciliation")
	payload["reconciled_from_invalid_authority_contract"] = true
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
	if err := r.sendMaterializedContinuationApprovalLocked(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), firstNonEmptyContinuation(source, "authority_reconciliation")); err != nil {
		return opState, err
	}
	return opState, nil
}

func operationPhasesForApprovalBundle(opState session.OperationState, bundle session.ContinuationApprovalBundle) []session.OperationPhase {
	opState = session.NormalizeOperationState(opState)
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if !bundle.Active() || len(bundle.Phases) == 0 {
		return nil
	}
	wanted := map[string]struct{}{}
	for _, phase := range bundle.Phases {
		if id := strings.TrimSpace(phase.OperationPhaseID); id != "" {
			wanted[id] = struct{}{}
		}
	}
	out := make([]session.OperationPhase, 0, len(wanted))
	for _, phase := range opState.PhasePlan.Phases {
		if _, ok := wanted[strings.TrimSpace(phase.ID)]; ok {
			out = append(out, phase)
		}
	}
	return out
}

func operationPhaseIDForContinuationState(opState session.OperationState, state session.ContinuationState) string {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	candidates := map[string]struct{}{}
	for _, value := range []string{
		state.ActionProposal.OperationID,
		strings.TrimPrefix(strings.TrimSpace(state.ActionProposal.ID), "aprop-"),
		state.DecisionID,
	} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			candidates[trimmed] = struct{}{}
		}
	}
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if _, ok := candidates[operationPhaseProposalID(opState, phase)]; ok {
			return strings.TrimSpace(phase.ID)
		}
	}
	currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID)
	if currentID != "" {
		for _, phase := range opState.PhasePlan.Phases {
			if strings.TrimSpace(phase.ID) == currentID && phase.Status == session.PlanStatusPending {
				return currentID
			}
		}
	}
	return ""
}

func normalizeContinuationAuthorityAction(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		",", "_",
		":", "_",
		"(", "_",
		")", "_",
		"[", "_",
		"]", "_",
		"{", "_",
		"}", "_",
		"|", "_",
	)
	value = replacer.Replace(value)
	for strings.Contains(value, "__") {
		value = strings.ReplaceAll(value, "__", "_")
	}
	return strings.Trim(value, "_")
}
