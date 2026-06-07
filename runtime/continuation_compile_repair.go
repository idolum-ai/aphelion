//go:build linux

package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type continuationCompileRepairKind string

const (
	continuationCompileRepairApprovalBoundary  continuationCompileRepairKind = "clarify_approval_boundary"
	continuationCompileRepairAuthorityContract continuationCompileRepairKind = "clarify_authority_contract"
	continuationCompileRepairPersonaIntent     continuationCompileRepairKind = "rebuild_persona_intent"
	continuationCompileRepairPersonaRationale  continuationCompileRepairKind = "rebuild_persona_rationale"
	continuationCompileRepairGovernorIntent    continuationCompileRepairKind = "rebuild_governor_intent"
	continuationCompileRepairGovernorRationale continuationCompileRepairKind = "rebuild_governor_rationale"
	continuationCompileRepairUnknown           continuationCompileRepairKind = "unknown"
)

const (
	operationAuthorityContractRepairPhasePrefix = "phase-clarify-authority-contract"
	operationPersonaIntentRepairPhasePrefix     = "phase-rebuild-persona-intent"
	operationPersonaRationaleRepairPhasePrefix  = "phase-rebuild-persona-rationale"
	operationGovernorIntentRepairPhasePrefix    = "phase-rebuild-governor-intent"
	operationGovernorRationaleRepairPhasePrefix = "phase-rebuild-governor-rationale"
	continuationCompileRepairEventLimit         = 120
)

func continuationCompileRepairForReason(reason string) (continuationCompileRepairKind, bool) {
	switch normalizeOperationPhaseReasonCode(reason) {
	case "waiting_for_a_clearer_approval_boundary":
		return continuationCompileRepairApprovalBoundary, true
	case "invalid_authority_contract", "invalid_authority_no_safe_repair":
		return continuationCompileRepairAuthorityContract, true
	case "persona_intent_missing":
		return continuationCompileRepairPersonaIntent, true
	case "persona_rationale_missing":
		return continuationCompileRepairPersonaRationale, true
	case "governor_intent_missing":
		return continuationCompileRepairGovernorIntent, true
	case "governor_rationale_missing":
		return continuationCompileRepairGovernorRationale, true
	default:
		return continuationCompileRepairUnknown, false
	}
}

func operationStateWithCompileFailureRepairPlan(opState session.OperationState, blockedPhase session.OperationPhase, reason string, now time.Time) (session.OperationState, session.OperationPhase, bool) {
	kind, ok := continuationCompileRepairForReason(reason)
	if !ok || kind == continuationCompileRepairUnknown {
		return session.NormalizeOperationState(opState), session.OperationPhase{}, false
	}
	if kind == continuationCompileRepairApprovalBoundary {
		repaired, ok := operationStateWithApprovalBoundaryDeliberationPlan(opState, blockedPhase, reason, now)
		if !ok {
			return session.NormalizeOperationState(opState), session.OperationPhase{}, false
		}
		repairID := operationApprovalBoundaryFirstPhaseID(blockedPhase)
		for _, phase := range repaired.PhasePlan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if strings.TrimSpace(phase.ID) == repairID {
				return repaired, phase, true
			}
		}
		return repaired, session.OperationPhase{}, true
	}
	opState = session.NormalizeOperationState(opState)
	blockedPhase = normalizeSingleOperationPhase(blockedPhase)
	blockedPhase = operationPhaseResolvedFromProposalID(opState, blockedPhase)
	if !operationCompileRepairAnchorCanEnter(opState, blockedPhase, kind) {
		return opState, session.OperationPhase{}, false
	}
	repairPhase := operationCompileRepairFirstPhase(opState, blockedPhase, kind, reason)
	repaired := operationStateWithInsertedCompileRepairPhase(opState, blockedPhase, repairPhase, kind, reason, now)
	for _, phase := range repaired.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if strings.TrimSpace(phase.ID) == strings.TrimSpace(repairPhase.ID) {
			return repaired, phase, true
		}
	}
	return repaired, repairPhase, true
}

func operationPhaseResolvedFromProposalID(opState session.OperationState, phase session.OperationPhase) session.OperationPhase {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	phaseID := strings.TrimSpace(phase.ID)
	if phaseID == "" {
		return phase
	}
	for _, candidate := range opState.PhasePlan.Phases {
		candidate = normalizeSingleOperationPhase(candidate)
		if operationPhaseProposalID(opState, candidate) != phaseID {
			continue
		}
		if strings.TrimSpace(candidate.Summary) == "" {
			candidate.Summary = phase.Summary
		}
		if strings.TrimSpace(candidate.AuthorityClass) == "" {
			candidate.AuthorityClass = phase.AuthorityClass
		}
		if strings.TrimSpace(candidate.BoundedEffect) == "" {
			candidate.BoundedEffect = phase.BoundedEffect
		}
		return normalizeSingleOperationPhase(candidate)
	}
	return phase
}

func operationCompileRepairAnchorCanEnter(opState session.OperationState, phase session.OperationPhase, kind continuationCompileRepairKind) bool {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	if !phase.Active() || phase.Status != session.PlanStatusPending {
		return false
	}
	if operationPhaseIsCompileRepair(phase) {
		return false
	}
	if operationPhaseApprovalExcludedReason(opState.PhasePlan, phase) != "" {
		return false
	}
	if phase.RequiresConsent || phase.RequiresOptIn || len(phase.RequiredCapabilityGrants) > 0 {
		return false
	}
	if operationPhaseHasThirdPartyPrivateDataGate(phase) {
		return false
	}
	if kind == continuationCompileRepairApprovalBoundary && operationPhasePlanBudgetHardStopReason(phase) != "" {
		return false
	}
	return true
}

func operationPhaseIsCompileRepair(phase session.OperationPhase) bool {
	id := strings.TrimSpace(phase.ID)
	if id == "" {
		return false
	}
	for _, prefix := range []string{
		operationApprovalBoundaryPlanPhasePrefix,
		operationAuthorityContractRepairPhasePrefix,
		operationPersonaIntentRepairPhasePrefix,
		operationPersonaRationaleRepairPhasePrefix,
		operationGovernorIntentRepairPhasePrefix,
		operationGovernorRationaleRepairPhasePrefix,
	} {
		if strings.HasPrefix(id, prefix) {
			return true
		}
	}
	return false
}

func operationCompileRepairFirstPhase(opState session.OperationState, blockedPhase session.OperationPhase, kind continuationCompileRepairKind, reason string) session.OperationPhase {
	opState = session.NormalizeOperationState(opState)
	blockedPhase = normalizeSingleOperationPhase(blockedPhase)
	blockedTitle := firstNonEmptyContinuation(blockedPhase.Summary, opState.Objective, "the requested work")
	phase := session.OperationPhase{
		ID:             operationCompileRepairFirstPhaseID(kind, blockedPhase),
		Summary:        operationCompileRepairSummary(kind, blockedTitle),
		Status:         session.PlanStatusPending,
		AuthorityClass: "read_only_review",
		WhyNow:         operationCompileRepairWhyNow(kind, reason),
		BoundedEffect:  operationCompileRepairBoundedEffect(kind),
		AllowedActions: operationCompileRepairAllowedActions(kind),
		ForbiddenActions: []string{
			"execute_blocked_work",
			"execute_original_work",
			"external_account_use",
			"private_content_access",
			"credential_or_token_output",
			"deploy",
			"restart_service",
			"destructive_or_irreversible_action",
			"modify_policy_or_grants",
			"expand_authority_without_new_approval",
		},
		ValidationPlan:   operationCompileRepairValidationPlan(kind),
		RequiresApproval: true,
	}
	return normalizeSingleOperationPhase(phase)
}

func operationStateWithInsertedCompileRepairPhase(opState session.OperationState, blockedPhase session.OperationPhase, repairPhase session.OperationPhase, kind continuationCompileRepairKind, reason string, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	blockedPhase = normalizeSingleOperationPhase(blockedPhase)
	repairPhase = normalizeSingleOperationPhase(repairPhase)

	phases := make([]session.OperationPhase, 0, len(opState.PhasePlan.Phases)+2)
	inserted := false
	foundBlocked := false
	blockedID := strings.TrimSpace(blockedPhase.ID)
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if blockedID != "" && strings.TrimSpace(phase.ID) == blockedID {
			foundBlocked = true
			if !inserted {
				phases = append(phases, repairPhase)
				inserted = true
			}
			phase.BlockedReasonCode = ""
			phase.RequiresApproval = true
			phase.Status = session.PlanStatusPending
		}
		phases = append(phases, phase)
	}
	if !foundBlocked && blockedPhase.Active() {
		if !inserted {
			phases = append(phases, repairPhase)
			inserted = true
		}
		blockedPhase.BlockedReasonCode = ""
		blockedPhase.RequiresApproval = true
		blockedPhase.Status = session.PlanStatusPending
		phases = append(phases, blockedPhase)
	}
	if !inserted {
		phases = append([]session.OperationPhase{repairPhase}, phases...)
	}

	if strings.TrimSpace(opState.PhasePlan.ID) == "" {
		opState.PhasePlan.ID = operationCompileRepairPlanID(opState)
	}
	if strings.TrimSpace(opState.PhasePlan.Goal) == "" {
		opState.PhasePlan.Goal = firstNonEmptyContinuation(opState.Objective, blockedPhase.Summary, "Repair continuation approval shape and continue safely")
	}
	opState.PhasePlan.CurrentPhaseID = repairPhase.ID
	opState.PhasePlan.Phases = phases
	opState.PhasePlan.UpdatedAt = now
	if opState.Status == "" || opState.Status == session.OperationStatusBlocked {
		opState.Status = session.OperationStatusActive
	}
	opState.Stage = "approval_request"
	opState.Summary = operationCompileRepairOperationSummary(kind, blockedPhase, reason)
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}

func operationCompileRepairPlanID(opState session.OperationState) string {
	base := normalizeOperationPhaseReasonCode(firstNonEmptyContinuation(opState.ID, opState.Objective, "operation"))
	if base == "" {
		base = "operation"
	}
	return base + "-compile-repair-plan"
}

func operationCompileRepairFirstPhaseID(kind continuationCompileRepairKind, blockedPhase session.OperationPhase) string {
	base := normalizeOperationPhaseReasonCode(firstNonEmptyContinuation(blockedPhase.ID, blockedPhase.Summary, "phase"))
	if base == "" {
		base = "phase"
	}
	id := operationCompileRepairPhasePrefix(kind) + "-for-" + base
	if len(id) <= 96 {
		return id
	}
	return strings.TrimRight(id[:96], "-_")
}

func operationCompileRepairPhasePrefix(kind continuationCompileRepairKind) string {
	switch kind {
	case continuationCompileRepairAuthorityContract:
		return operationAuthorityContractRepairPhasePrefix
	case continuationCompileRepairPersonaIntent:
		return operationPersonaIntentRepairPhasePrefix
	case continuationCompileRepairPersonaRationale:
		return operationPersonaRationaleRepairPhasePrefix
	case continuationCompileRepairGovernorIntent:
		return operationGovernorIntentRepairPhasePrefix
	case continuationCompileRepairGovernorRationale:
		return operationGovernorRationaleRepairPhasePrefix
	default:
		return "phase-repair-continuation-compile"
	}
}

func operationCompileRepairSummary(kind continuationCompileRepairKind, blockedTitle string) string {
	title := truncatePreview(blockedTitle, 96)
	switch kind {
	case continuationCompileRepairAuthorityContract:
		return "Clarify authority contract for \"" + title + "\""
	case continuationCompileRepairPersonaIntent:
		return "Rebuild continuation intent for \"" + title + "\""
	case continuationCompileRepairPersonaRationale:
		return "Rebuild continuation rationale for \"" + title + "\""
	case continuationCompileRepairGovernorIntent:
		return "Rebuild governor continuation intent for \"" + title + "\""
	case continuationCompileRepairGovernorRationale:
		return "Rebuild governor continuation rationale for \"" + title + "\""
	default:
		return "Repair continuation compile blocker for \"" + title + "\""
	}
}

func operationCompileRepairWhyNow(kind continuationCompileRepairKind, reason string) string {
	reason = normalizeOperationPhaseReasonCode(reason)
	switch kind {
	case continuationCompileRepairAuthorityContract:
		return "The continuation authority compiler rejected the approval shape; repair must inspect and narrow the typed contract before any blocked work can be approved."
	case continuationCompileRepairPersonaIntent, continuationCompileRepairPersonaRationale, continuationCompileRepairGovernorIntent, continuationCompileRepairGovernorRationale:
		return "The continuation handshake is missing a required typed intent field; repair must rebuild that contract before a follow-up approval can be offered."
	default:
		if reason != "" {
			return "Continuation compile repair is needed for reason " + reason + "."
		}
		return "Continuation compile repair is needed before the blocked work can be approved."
	}
}

func operationCompileRepairBoundedEffect(kind continuationCompileRepairKind) string {
	switch kind {
	case continuationCompileRepairAuthorityContract:
		return "Inspect only the operation state, phase contract, and prior approval evidence; draft a narrower authority shape whose allowed actions are disjoint from forbidden actions. Does not execute the blocked work."
	case continuationCompileRepairPersonaIntent, continuationCompileRepairPersonaRationale, continuationCompileRepairGovernorIntent, continuationCompileRepairGovernorRationale:
		return "Inspect only the latest turn result, plan state, operation state, and continuation evidence; rebuild the missing typed continuation field. Does not execute the pending work."
	default:
		return "Inspect only local continuation evidence and draft a safer follow-up contract. Does not execute the blocked work."
	}
}

func operationCompileRepairAllowedActions(kind continuationCompileRepairKind) []string {
	actions := []string{
		"inspect_request_context",
		"inspect_operation_state",
		"inspect_continuation_evidence",
		"read_only_review",
		"report_repair_plan",
	}
	switch kind {
	case continuationCompileRepairAuthorityContract:
		actions = append(actions,
			"inspect_authority_contract",
			"draft_narrow_authority_contract",
			"propose_normal_phase_plan",
		)
	case continuationCompileRepairPersonaIntent, continuationCompileRepairPersonaRationale, continuationCompileRepairGovernorIntent, continuationCompileRepairGovernorRationale:
		actions = append(actions,
			"inspect_turn_result",
			"draft_continuation_intent",
			"propose_continuation_contract",
		)
	default:
		actions = append(actions, "draft_compile_repair")
	}
	return actions
}

func operationCompileRepairValidationPlan(kind continuationCompileRepairKind) []string {
	switch kind {
	case continuationCompileRepairAuthorityContract:
		return []string{
			"Identify which approval action conflicts with a forbidden boundary.",
			"Produce a narrower phase contract with non-overlapping allowed and forbidden actions.",
			"Preserve the original requested work as pending later approval.",
			"Confirm no blocked work or external effect occurred during authority repair.",
		}
	case continuationCompileRepairPersonaIntent, continuationCompileRepairPersonaRationale, continuationCompileRepairGovernorIntent, continuationCompileRepairGovernorRationale:
		return []string{
			"Name the missing continuation field and the evidence used to rebuild it.",
			"Preserve the pending work as later approval rather than executing it.",
			"Confirm no blocked work or external effect occurred during continuation repair.",
		}
	default:
		return []string{
			"Identify the compile blocker.",
			"Draft the smallest safe follow-up contract.",
			"Confirm no blocked work or external effect occurred during repair.",
		}
	}
}

func operationCompileRepairOperationSummary(kind continuationCompileRepairKind, blockedPhase session.OperationPhase, reason string) string {
	title := firstNonEmptyContinuation(blockedPhase.Summary, blockedPhase.ID, "the requested work")
	return "Continuation compile blocker routed through " + string(kind) + " for \"" + truncatePreview(title, 96) + "\": " + strings.TrimSpace(reason) + ". The next approval is a read-only repair slice; the original work remains pending later approval."
}

func operationPhaseFromInvalidMaterializedAuthority(opState session.OperationState, state session.ContinuationState, source string) (session.OperationPhase, bool) {
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	source = strings.TrimSpace(source)

	switch source {
	case "operation_phase_bundle":
		if phase, ok := currentOperationPhaseFromApprovalBundle(opState, state.ApprovalBundle); ok {
			return phase, true
		}
	case "operation_plan_lease":
		if phases := operationPlanLeasePhasesFromOperation(opState, opState.PlanLease); len(phases) > 0 {
			return normalizeSingleOperationPhase(phases[0]), true
		}
	}
	if phaseID := operationPhaseIDForContinuationState(opState, state); phaseID != "" {
		for _, phase := range opState.PhasePlan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if strings.TrimSpace(phase.ID) == phaseID {
				return phase, true
			}
		}
	}
	if currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID); currentID != "" {
		for _, phase := range opState.PhasePlan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if strings.TrimSpace(phase.ID) == currentID && phase.Status == session.PlanStatusPending {
				return phase, true
			}
		}
	}
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusPending {
			return phase, true
		}
	}
	if phase := operationPhaseFromContinuationState(state); phase.Active() {
		return phase, true
	}
	if proposal := opState.Proposal; proposal.Active() {
		return normalizeSingleOperationPhase(session.OperationPhase{
			ID:               firstNonEmptyContinuation(proposal.ID, opState.ID, "operation-proposal"),
			OperatorTitle:    proposal.OperatorTitle,
			PlanTitle:        proposal.PlanTitle,
			Summary:          proposal.Summary,
			Status:           session.PlanStatusPending,
			AuthorityClass:   proposal.Kind,
			WhyNow:           proposal.WhyNow,
			BoundedEffect:    proposal.BoundedEffect,
			RequiresApproval: true,
		}), true
	}
	return session.OperationPhase{}, false
}

func currentOperationPhaseFromApprovalBundle(opState session.OperationState, bundle session.ContinuationApprovalBundle) (session.OperationPhase, bool) {
	opState = session.NormalizeOperationState(opState)
	if bundlePhase, ok := session.CurrentContinuationApprovalBundlePhase(bundle); ok {
		phaseID := strings.TrimSpace(bundlePhase.OperationPhaseID)
		for _, phase := range opState.PhasePlan.Phases {
			phase = normalizeSingleOperationPhase(phase)
			if phaseID == "" || strings.TrimSpace(phase.ID) == phaseID {
				return phase, true
			}
		}
		return normalizeSingleOperationPhase(session.OperationPhase{
			ID:                       firstNonEmptyContinuation(bundlePhase.OperationPhaseID, bundlePhase.ID),
			OperatorTitle:            bundlePhase.OperatorTitle,
			PlanTitle:                bundlePhase.PlanTitle,
			Summary:                  bundlePhase.Summary,
			Status:                   session.PlanStatusPending,
			AuthorityClass:           bundlePhase.AuthorityClass,
			WhyNow:                   bundlePhase.WhyNow,
			BoundedEffect:            bundlePhase.BoundedEffect,
			AllowedActions:           append([]string(nil), bundlePhase.AllowedActions...),
			ForbiddenActions:         append([]string(nil), bundlePhase.ForbiddenActions...),
			ValidationPlan:           append([]string(nil), bundlePhase.ValidationPlan...),
			RequiresApproval:         true,
			RequiredCapabilityGrants: append([]session.CapabilityGrantSpec(nil), bundlePhase.RequiredCapabilityGrants...),
		}), true
	}
	return session.OperationPhase{}, false
}

func operationPhaseFromContinuationState(state session.ContinuationState) session.OperationPhase {
	state = session.NormalizeContinuationState(state)
	action := state.ActionProposal
	if !action.Active() {
		return session.OperationPhase{}
	}
	return normalizeSingleOperationPhase(session.OperationPhase{
		ID:               firstNonEmptyContinuation(action.OperationID, strings.TrimPrefix(action.ID, "aprop-"), state.DecisionID, "continuation-approval"),
		OperatorTitle:    action.OperatorTitle,
		PlanTitle:        action.PlanTitle,
		Summary:          firstNonEmptyContinuation(action.Summary, state.StageSummary, state.Objective),
		Status:           session.PlanStatusPending,
		AuthorityClass:   action.RiskClass,
		WhyNow:           action.WhyNow,
		BoundedEffect:    action.BoundedEffect,
		AllowedActions:   append([]string(nil), action.AllowedActions...),
		ForbiddenActions: append([]string(nil), action.ForbiddenActions...),
		ValidationPlan:   append([]string(nil), action.ValidationPlan...),
		RequiresApproval: true,
	})
}

func (r *Runtime) materializeCompileRepairPhaseApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, opState session.OperationState, repairPhase session.OperationPhase, blockedPhase session.OperationPhase, originalState session.ContinuationState, compilation session.AuthorityContractCompilation, reason string, kind continuationCompileRepairKind, source string, now time.Time) (session.OperationState, error) {
	if r == nil || r.store == nil {
		return session.NormalizeOperationState(opState), nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	repairPhase = normalizeSingleOperationPhase(repairPhase)
	blockedPhase = normalizeSingleOperationPhase(blockedPhase)
	state := continuationStateFromOperationPhase(opState, repairPhase, "", now)
	if repairCompilation := continuationAuthorityCompilation(state); repairCompilation.Invalid() {
		r.recordContinuationCompileRepairExhausted(key, opState, repairPhase, originalState, repairCompilation, "repair_phase_invalid_authority_contract", kind, source, false, now)
		return opState, fmt.Errorf("compile repair phase authority invalid: %s", continuationAuthorityContractInvalidReason(repairCompilation))
	}

	opState = operationStateWithMaterializedPhaseLease(opState, repairPhase.ID, state, now)
	if err := r.store.UpdateOperationState(key, opState); err != nil {
		return opState, fmt.Errorf("persist continuation compile repair operation state: %w", err)
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return opState, fmt.Errorf("persist continuation compile repair state: %w", err)
	}

	r.recordContinuationCompileRepaired(key, opState, repairPhase, originalState, compilation, reason, kind, source, map[string]any{
		"blocked_phase_id":      strings.TrimSpace(blockedPhase.ID),
		"blocked_phase_summary": strings.TrimSpace(blockedPhase.Summary),
		"repair_phase_id":       strings.TrimSpace(repairPhase.ID),
		"repair_strategy":       "read_only_phase_before_blocked_work",
		"user_visible":          false,
	}, now)
	payload := continuationExecutionPayload(state)
	payload["materialized_from"] = "continuation_compile_repair"
	payload["repair_kind"] = string(kind)
	payload["repair_reason"] = normalizeOperationPhaseReasonCode(reason)
	payload["phase_plan_id"] = strings.TrimSpace(opState.PhasePlan.ID)
	payload["phase_id"] = strings.TrimSpace(repairPhase.ID)
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", payload, now)
	r.recordContinuationBundleNarrowing(key, opState, []session.OperationPhase{repairPhase}, state, "continuation_compile_repair", now)
	if err := r.sendMaterializedContinuationApproval(ctx, key, msg, state, renderOperationProposalMaterializedPromptFallback(state), "continuation_compile_repair"); err != nil {
		return opState, err
	}
	return opState, nil
}

func (r *Runtime) repairOrganicContinuationHandshakeCompileBlock(ctx context.Context, key session.SessionKey, msg core.InboundMessage, consensus continuationConsensus, state session.ContinuationState, reason string, now time.Time) (bool, error) {
	if r == nil || r.store == nil {
		return false, nil
	}
	kind, ok := continuationCompileRepairForReason(reason)
	if !ok {
		return false, nil
	}
	switch kind {
	case continuationCompileRepairPersonaIntent, continuationCompileRepairPersonaRationale, continuationCompileRepairGovernorIntent, continuationCompileRepairGovernorRationale:
	default:
		return false, nil
	}
	opState := session.NormalizeOperationState(consensus.OperationState)
	if !opState.Active() {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	blockedPhase, ok := operationPhaseFromInvalidMaterializedAuthority(opState, state, "organic_continuation")
	if !ok {
		r.recordContinuationCompileRepairExhausted(key, opState, session.OperationPhase{}, state, session.AuthorityContractCompilation{}, reason, kind, "organic_continuation", false, now)
		return false, nil
	}
	operationID := strings.TrimSpace(opState.ID)
	phaseID := strings.TrimSpace(blockedPhase.ID)
	if r.continuationCompileRepairAlreadyAttempted(key, operationID, phaseID, reason, kind) {
		r.recordContinuationCompileRepairExhausted(key, opState, blockedPhase, state, session.AuthorityContractCompilation{}, reason, kind, "organic_continuation", false, now)
		return false, nil
	}
	repairedState, repairPhase, repaired := operationStateWithCompileFailureRepairPlan(opState, blockedPhase, reason, now)
	if !repaired {
		r.recordContinuationCompileRepairExhausted(key, opState, blockedPhase, state, session.AuthorityContractCompilation{}, reason, kind, "organic_continuation", false, now)
		return false, nil
	}
	if _, err := r.materializeCompileRepairPhaseApproval(ctx, key, msg, repairedState, repairPhase, blockedPhase, state, session.AuthorityContractCompilation{}, reason, kind, "organic_continuation", now); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Runtime) recordContinuationCompileRepaired(key session.SessionKey, opState session.OperationState, repairPhase session.OperationPhase, state session.ContinuationState, compilation session.AuthorityContractCompilation, reason string, kind continuationCompileRepairKind, source string, extra map[string]any, now time.Time) {
	if r == nil {
		return
	}
	payload := continuationCompileRepairPayload(opState, repairPhase, state, compilation, reason, kind, source)
	for k, v := range extra {
		payload[k] = v
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationCompileRepaired, "continuation", "repaired", payload, now)
}

func (r *Runtime) recordContinuationCompileRepairExhausted(key session.SessionKey, opState session.OperationState, blockedPhase session.OperationPhase, state session.ContinuationState, compilation session.AuthorityContractCompilation, reason string, kind continuationCompileRepairKind, source string, userVisible bool, now time.Time) {
	if r == nil {
		return
	}
	payload := continuationCompileRepairPayload(opState, blockedPhase, state, compilation, reason, kind, source)
	payload["user_visible"] = userVisible
	r.recordExecutionEvent(key, core.ExecutionEventContinuationCompileRepairExhausted, "continuation", "exhausted", payload, now)
}

func (r *Runtime) recordContinuationCompileUnknownReason(key session.SessionKey, opState session.OperationState, state session.ContinuationState, reason string, source string, userVisible bool, now time.Time) {
	if r == nil {
		return
	}
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	payload := map[string]any{
		"operation_id":           strings.TrimSpace(opState.ID),
		"phase_plan_id":          strings.TrimSpace(opState.PhasePlan.ID),
		"decision_id":            strings.TrimSpace(state.DecisionID),
		"proposal_id":            strings.TrimSpace(state.ActionProposal.ID),
		"reason":                 strings.TrimSpace(reason),
		"normalized_reason":      normalizeOperationPhaseReasonCode(reason),
		"materialization_source": strings.TrimSpace(source),
		"user_visible":           userVisible,
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationCompileUnknownReason, "continuation", "unknown_reason", payload, now)
}

func continuationCompileRepairPayload(opState session.OperationState, phase session.OperationPhase, state session.ContinuationState, compilation session.AuthorityContractCompilation, reason string, kind continuationCompileRepairKind, source string) map[string]any {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	state = session.NormalizeContinuationState(state)
	payload := map[string]any{
		"operation_id":                           strings.TrimSpace(opState.ID),
		"phase_plan_id":                          strings.TrimSpace(opState.PhasePlan.ID),
		"phase_id":                               strings.TrimSpace(phase.ID),
		"phase_summary":                          strings.TrimSpace(phase.Summary),
		"decision_id":                            strings.TrimSpace(state.DecisionID),
		"proposal_id":                            strings.TrimSpace(state.ActionProposal.ID),
		"reason":                                 strings.TrimSpace(reason),
		"normalized_reason":                      normalizeOperationPhaseReasonCode(reason),
		"repair_kind":                            string(kind),
		"materialization_source":                 strings.TrimSpace(source),
		"authority_contract_status":              string(compilation.Status),
		"authority_contract_work_action":         strings.TrimSpace(compilation.WorkAction),
		"authority_contract_suggested_repair":    strings.TrimSpace(compilation.SuggestedRepair),
		"authority_contract_contradictions":      compilation.Contradictions,
		"authority_contract_contradiction_count": len(compilation.Contradictions),
	}
	if summary := session.AuthorityContractCompilationSummary(compilation); summary != "" && summary != "authority contract valid" {
		payload["authority_contract_summary"] = summary
		payload["summary"] = summary
	}
	return payload
}

func (r *Runtime) continuationCompileRepairAlreadyAttempted(key session.SessionKey, operationID string, phaseID string, reason string, kind continuationCompileRepairKind) bool {
	if r == nil || r.store == nil {
		return false
	}
	events, err := r.store.LatestExecutionEventsBySession(key, continuationCompileRepairEventLimit)
	if err != nil {
		return false
	}
	sort.Slice(events, func(i, j int) bool { return executionEventBefore(events[i], events[j]) })
	operationID = strings.TrimSpace(operationID)
	phaseID = strings.TrimSpace(phaseID)
	normalizedReason := normalizeOperationPhaseReasonCode(reason)
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventContinuationCompileRepaired, core.ExecutionEventContinuationCompileRepairExhausted:
		default:
			continue
		}
		payload := executionEventPayload(event.PayloadJSON)
		if operationID != "" && payloadString(payload, "operation_id") != operationID {
			continue
		}
		if phaseID != "" {
			eventPhaseID := payloadString(payload, "phase_id")
			eventRepairPhaseID := payloadString(payload, "repair_phase_id")
			eventBlockedPhaseID := payloadString(payload, "blocked_phase_id")
			if eventPhaseID != "" || eventRepairPhaseID != "" {
				expectedRepairID := operationCompileRepairFirstPhaseID(kind, session.OperationPhase{ID: phaseID})
				if eventPhaseID != phaseID && eventRepairPhaseID != phaseID && eventBlockedPhaseID != phaseID && eventPhaseID != expectedRepairID && eventRepairPhaseID != expectedRepairID {
					continue
				}
			}
		}
		if normalizedReason != "" && payloadString(payload, "normalized_reason") != normalizedReason {
			continue
		}
		if kind != "" && payloadString(payload, "repair_kind") != string(kind) {
			continue
		}
		return true
	}
	return false
}
