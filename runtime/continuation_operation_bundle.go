//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func continuationStateFromOperationPhaseBundle(opState session.OperationState, phases []session.OperationPhase, promptInput string, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	bundleID := operationPhaseBundleID(opState, phases)
	if bundleID == "" {
		bundleID = newContinuationDecisionID()
	}
	bundlePhases := continuationApprovalBundlePhasesFromOperation(opState, phases)
	objective := firstNonEmptyContinuation(opState.Objective, opState.PhasePlan.Goal, opState.Summary, summarizeContinuationFallback(promptInput))
	nextStep := operationPhaseBundleSummary(bundlePhases)
	if nextStep == "" {
		nextStep = "Approve multiple named phases, then execute them sequentially with stop gates."
	}
	boundedEffect := operationPhaseBundleBoundedEffect(bundlePhases)
	whyNow := "This durable phase plan has multiple bounded phases that can be approved together without approving hard-stop escalation gates."
	if len(phases) > 0 && strings.TrimSpace(phases[0].WhyNow) != "" {
		whyNow = strings.TrimSpace(phases[0].WhyNow)
	}
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     bundleID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: len(bundlePhases),
		PersonaIntent: session.ContinuationIntent{
			Decision:   session.ContinuationIntentDecisionContinue,
			Rationale:  "A multi-phase approval bundle is ready for button-backed approval.",
			NextStep:   nextStep,
			Confidence: "high",
			UpdatedAt:  now,
		},
		GovernorIntent: session.ContinuationIntent{
			Decision:    session.ContinuationIntentDecisionContinue,
			Rationale:   whyNow,
			NextStep:    nextStep,
			Constraints: boundedEffect,
			Confidence:  "high",
			Ratified:    true,
			UpdatedAt:   now,
		},
		ApprovalBundle: session.ContinuationApprovalBundle{
			ID:              bundleID,
			OperationID:     strings.TrimSpace(opState.ID),
			PhasePlanID:     strings.TrimSpace(opState.PhasePlan.ID),
			PlanFingerprint: operationPhasePlanFingerprint(opState, phases),
			Status:          session.ContinuationLeaseStatusPending,
			CurrentPhaseID:  firstContinuationBundlePhaseID(bundlePhases),
			Phases:          bundlePhases,
			ExpiresAt:       now.Add(continuationLeaseDefaultTTL),
			CreatedAt:       now,
			UpdatedAt:       now,
		},
		UpdatedAt: now,
	}
	action := session.ActionProposal{
		ID:               "aprop-" + bundleID,
		OperationID:      bundleID,
		OperatorTitle:    continuationPlanTitleFromText(nextStep),
		PlanTitle:        continuationPlanTitleFromText(firstNonEmptyContinuation(opState.PhasePlan.Goal, objective, nextStep)),
		Summary:          nextStep,
		WhyNow:           whyNow,
		BoundedEffect:    boundedEffect,
		RiskClass:        strongestPhaseAuthorityClass(bundlePhases),
		AllowedActions:   []string{"execute_approved_bundle_phases_sequentially", "use_existing_authority_only", "update_operation_phase_plan", "report_milestone_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval", "execute_phase_outside_bundle", "skip_stop_gate", "credentials_or_tokens", "external_send_or_contact", "archive_delete_or_mutate_source_data", "deploy_restart_without_explicit_approval"},
		ValidationPlan:   []string{"execute only named bundle phases", "preserve per-phase provenance", "report evidence at meaningful milestones and completion", "stop when a hard gate or out-of-bundle phase is reached"},
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	action = applyContinuationLeaseClassBoundaries(action)
	action.PlanHash = actionProposalHash(action)
	state.ActionProposal = session.NormalizeActionProposal(action)
	state.ContinuationLease = buildContinuationLease(state.ActionProposal, len(bundlePhases), now)
	return session.NormalizeContinuationState(state)
}

func continuationApprovalBundlePhasesFromOperation(opState session.OperationState, phases []session.OperationPhase) []session.ContinuationApprovalBundlePhase {
	opState = session.NormalizeOperationState(opState)
	planIndexes := make(map[string]int, len(opState.PhasePlan.Phases))
	for i, phase := range opState.PhasePlan.Phases {
		if id := strings.TrimSpace(phase.ID); id != "" {
			planIndexes[id] = i + 1
		}
	}
	out := make([]session.ContinuationApprovalBundlePhase, 0, len(phases))
	for i, phase := range phases {
		phase = normalizeSingleOperationPhase(phase)
		id := operationPhaseProposalID(opState, phase)
		phaseIndex := planIndexes[strings.TrimSpace(phase.ID)]
		if phaseIndex <= 0 {
			phaseIndex = i + 1
		}
		out = append(out, session.ContinuationApprovalBundlePhase{
			ID:                       id,
			OperationPhaseID:         firstNonEmptyContinuation(phase.ID, fmt.Sprintf("phase-%d", i+1)),
			Index:                    phaseIndex,
			PhaseFingerprint:         operationPhaseFingerprint(opState, phase, i),
			OperatorTitle:            firstNonEmptyContinuation(phase.OperatorTitle, phase.PlanTitle, continuationPlanTitleFromText(phase.Summary)),
			PlanTitle:                firstNonEmptyContinuation(phase.PlanTitle, phase.OperatorTitle, continuationPlanTitleFromText(phase.Summary)),
			Summary:                  strings.TrimSpace(phase.Summary),
			AuthorityClass:           strings.TrimSpace(phase.AuthorityClass),
			WhyNow:                   strings.TrimSpace(phase.WhyNow),
			BoundedEffect:            strings.TrimSpace(phase.BoundedEffect),
			AllowedActions:           append([]string(nil), phase.AllowedActions...),
			ForbiddenActions:         append([]string(nil), phase.ForbiddenActions...),
			ValidationPlan:           append([]string(nil), phase.ValidationPlan...),
			RequiredCapabilityGrants: append([]session.CapabilityGrantSpec(nil), phase.RequiredCapabilityGrants...),
			Status:                   session.ContinuationLeaseStatusPending,
		})
	}
	return out
}

func (r *Runtime) validateContinuationApprovalBundleFingerprints(key session.SessionKey, state session.ContinuationState) error {
	if r == nil || r.store == nil {
		return nil
	}
	state = session.NormalizeContinuationState(state)
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if !bundle.Active() || len(bundle.Phases) == 0 || strings.TrimSpace(bundle.PlanFingerprint) == "" {
		return nil
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return nil
	}
	opState = session.NormalizeOperationState(opState)
	if strings.TrimSpace(bundle.OperationID) != "" && strings.TrimSpace(opState.ID) != "" && strings.TrimSpace(bundle.OperationID) != strings.TrimSpace(opState.ID) {
		return fmt.Errorf("approval bundle operation changed: %w", core.ErrContinuationStale)
	}
	if strings.TrimSpace(bundle.PhasePlanID) != "" && strings.TrimSpace(opState.PhasePlan.ID) != "" && strings.TrimSpace(bundle.PhasePlanID) != strings.TrimSpace(opState.PhasePlan.ID) {
		return fmt.Errorf("approval bundle phase plan changed: %w", core.ErrContinuationStale)
	}
	planPhases := opState.PhasePlan.Phases
	if got := operationPhasePlanFingerprint(opState, planPhases); got != "" && strings.TrimSpace(bundle.PlanFingerprint) != "" && got != strings.TrimSpace(bundle.PlanFingerprint) {
		return fmt.Errorf("approval bundle plan fingerprint changed: %w", core.ErrContinuationStale)
	}
	byOperationPhaseID := make(map[string]session.OperationPhase, len(planPhases))
	byOperationPhaseIndex := make(map[string]int, len(planPhases))
	for i, phase := range planPhases {
		phase = normalizeSingleOperationPhase(phase)
		id := strings.TrimSpace(phase.ID)
		if id == "" {
			id = fmt.Sprintf("phase-%d", i+1)
		}
		byOperationPhaseID[id] = phase
		byOperationPhaseIndex[id] = i
	}
	for _, token := range bundle.Phases {
		token = session.NormalizeContinuationApprovalBundlePhase(token)
		if token.Status == session.ContinuationLeaseStatusDeferred || token.Status == session.ContinuationLeaseStatusConsumed || strings.TrimSpace(token.PhaseFingerprint) == "" {
			continue
		}
		phaseID := strings.TrimSpace(token.OperationPhaseID)
		phase, ok := byOperationPhaseID[phaseID]
		if !ok {
			return fmt.Errorf("approval bundle phase missing: %w", core.ErrContinuationStale)
		}
		if !continuationApprovalBundlePhaseMatchesOperation(opState, token, phase, byOperationPhaseIndex[phaseID]) {
			return fmt.Errorf("approval bundle phase fingerprint changed: %w", core.ErrContinuationStale)
		}
	}
	return nil
}

func operationPhasePlanFingerprint(opState session.OperationState, phases []session.OperationPhase) string {
	opState = session.NormalizeOperationState(opState)
	entries := make([]string, 0, len(phases))
	for i, phase := range phases {
		entries = append(entries, operationPhaseFingerprint(opState, phase, i))
	}
	return stableOperationBundleFingerprint(map[string]any{
		"operation_id":  strings.TrimSpace(opState.ID),
		"phase_plan_id": strings.TrimSpace(opState.PhasePlan.ID),
		"goal":          strings.TrimSpace(opState.PhasePlan.Goal),
		"phase_tokens":  entries,
	})
}

func operationPhaseFingerprint(opState session.OperationState, phase session.OperationPhase, index int) string {
	opState = session.NormalizeOperationState(opState)
	phase = normalizeSingleOperationPhase(phase)
	return stableOperationBundleFingerprint(map[string]any{
		"operation_id":               strings.TrimSpace(opState.ID),
		"phase_plan_id":              strings.TrimSpace(opState.PhasePlan.ID),
		"operation_phase_id":         firstNonEmptyContinuation(phase.ID, fmt.Sprintf("phase-%d", index+1)),
		"index":                      index + 1,
		"authority_class":            strings.TrimSpace(phase.AuthorityClass),
		"bounded_effect":             strings.TrimSpace(phase.BoundedEffect),
		"allowed_actions":            append([]string(nil), phase.AllowedActions...),
		"forbidden_actions":          append([]string(nil), phase.ForbiddenActions...),
		"validation_plan":            append([]string(nil), phase.ValidationPlan...),
		"required_capability_grants": append([]session.CapabilityGrantSpec(nil), phase.RequiredCapabilityGrants...),
		"gate_level":                 strings.TrimSpace(phase.GateLevel),
		"gate_reason_code":           strings.TrimSpace(phase.GateReasonCode),
		"approval_subject":           strings.TrimSpace(phase.ApprovalSubject),
		"requires_consent":           phase.RequiresConsent,
		"requires_opt_in":            phase.RequiresOptIn,
		"stale_authority":            phase.StaleAuthority,
		"supersedes_phase_ids":       append([]string(nil), phase.SupersedesPhaseIDs...),
	})
}

func stableOperationBundleFingerprint(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func continuationApprovalBundlePhaseMatchesOperation(opState session.OperationState, token session.ContinuationApprovalBundlePhase, phase session.OperationPhase, index int) bool {
	token = session.NormalizeContinuationApprovalBundlePhase(token)
	phase = normalizeSingleOperationPhase(phase)
	if strings.TrimSpace(token.OperationPhaseID) != firstNonEmptyContinuation(phase.ID, fmt.Sprintf("phase-%d", index+1)) {
		return false
	}
	return strings.TrimSpace(token.PhaseFingerprint) != "" && strings.TrimSpace(token.PhaseFingerprint) == operationPhaseFingerprint(opState, phase, index)
}

func operationPhaseBundleID(opState session.OperationState, phases []session.OperationPhase) string {
	opState = session.NormalizeOperationState(opState)
	if len(phases) == 0 {
		return ""
	}
	base := firstNonEmptyContinuation(opState.ID, opState.PhasePlan.ID, "operation")
	firstID := firstNonEmptyContinuation(phases[0].ID, phases[0].Summary, "first")
	lastID := firstNonEmptyContinuation(phases[len(phases)-1].ID, phases[len(phases)-1].Summary, "last")
	id := sanitizeOperationPhaseProposalID("bundle-" + base + "-" + firstID + "-to-" + lastID)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func operationPhaseBundleSummary(phases []session.ContinuationApprovalBundlePhase) string {
	if len(phases) == 0 {
		return ""
	}
	first := phases[0].Index
	last := phases[len(phases)-1].Index
	parts := make([]string, 0, len(phases))
	for _, phase := range phases {
		if summary := strings.TrimSpace(phase.Summary); summary != "" {
			parts = append(parts, summary)
		}
	}
	prefix := fmt.Sprintf("Approve stages %d–%d", first, last)
	if len(parts) == 0 {
		return prefix
	}
	return prefix + ": " + strings.Join(parts, " → ")
}

func operationPhaseBundleBoundedEffect(phases []session.ContinuationApprovalBundlePhase) string {
	parts := make([]string, 0, len(phases)+1)
	for _, phase := range phases {
		label := fmt.Sprintf("phase %d", phase.Index)
		if summary := strings.TrimSpace(phase.Summary); summary != "" {
			label += " " + summary
		}
		if effect := strings.TrimSpace(phase.BoundedEffect); effect != "" {
			parts = append(parts, label+": "+effect)
		} else {
			parts = append(parts, label)
		}
	}
	parts = append(parts, "Stop before any phase not named in this bundle or any hard escalation gate.")
	return strings.Join(parts, " | ")
}

func firstContinuationBundlePhaseID(phases []session.ContinuationApprovalBundlePhase) string {
	for _, phase := range phases {
		if id := strings.TrimSpace(phase.ID); id != "" {
			return id
		}
	}
	return ""
}

func strongestPhaseAuthorityClass(phases []session.ContinuationApprovalBundlePhase) string {
	best := "continuation_bundle"
	for _, phase := range phases {
		mode := workModeFromStructuredAuthority(phase.AuthorityClass)
		switch mode {
		case WorkModeDeploy:
			return "deploy"
		case WorkModeWorkspaceWrite:
			best = "workspace_write"
		case WorkModeReadOnly:
			if best == "continuation_bundle" {
				best = "read_only_review"
			}
		}
	}
	return best
}

func operationStateWithMaterializedPhaseBundleLease(opState session.OperationState, phases []session.OperationPhase, state session.ContinuationState, now time.Time) session.OperationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	phaseIDs := make(map[string]struct{}, len(phases))
	for _, phase := range phases {
		phaseIDs[strings.TrimSpace(phase.ID)] = struct{}{}
	}
	opState.Status = session.OperationStatusBlocked
	opState.Stage = "bundle_approval"
	opState.Proposal = session.OperationProposal{
		ID:            strings.TrimSpace(state.ActionProposal.OperationID),
		Kind:          strings.TrimSpace(state.ActionProposal.RiskClass),
		OperatorTitle: strings.TrimSpace(state.ActionProposal.OperatorTitle),
		PlanTitle:     strings.TrimSpace(state.ActionProposal.PlanTitle),
		Summary:       strings.TrimSpace(state.ActionProposal.Summary),
		WhyNow:        strings.TrimSpace(state.ActionProposal.WhyNow),
		BoundedEffect: strings.TrimSpace(state.ActionProposal.BoundedEffect),
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	firstPhaseID := ""
	for i := range opState.PhasePlan.Phases {
		phaseID := strings.TrimSpace(opState.PhasePlan.Phases[i].ID)
		if _, ok := phaseIDs[phaseID]; !ok {
			continue
		}
		if firstPhaseID == "" {
			firstPhaseID = phaseID
		}
		opState.PhasePlan.Phases[i].LeaseID = strings.TrimSpace(state.ContinuationLease.ID)
		if opState.PhasePlan.Phases[i].Status == "" {
			opState.PhasePlan.Phases[i].Status = session.PlanStatusPending
		}
	}
	if firstPhaseID != "" {
		opState.PhasePlan.CurrentPhaseID = firstPhaseID
	}
	opState.PhasePlan.UpdatedAt = now
	opState.UpdatedAt = now
	return session.NormalizeOperationState(opState)
}
