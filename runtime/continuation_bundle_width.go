//go:build linux

package runtime

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const continuationBundleNarrowingEventLimit = 200

type continuationBundleNarrowingObservation struct {
	PhaseID          string
	MaterializedFrom string
	NarrowStreak     int
	CreatedAt        time.Time
}

func (r *Runtime) recordContinuationBundleNarrowing(
	key session.SessionKey,
	opState session.OperationState,
	phases []session.OperationPhase,
	state session.ContinuationState,
	materializedFrom string,
	now time.Time,
) {
	if r == nil || r.store == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	opState = session.NormalizeOperationState(opState)
	state = session.NormalizeContinuationState(state)
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	var bundlePhase session.ContinuationApprovalBundlePhase
	if len(bundle.Phases) == 1 {
		bundlePhase = session.NormalizeContinuationApprovalBundlePhase(bundle.Phases[0])
	} else if len(phases) == 1 {
		phase := normalizeSingleOperationPhase(phases[0])
		bundlePhase = session.ContinuationApprovalBundlePhase{
			ID:                       operationPhaseProposalID(opState, phase),
			OperationPhaseID:         phase.ID,
			OperatorTitle:            phase.OperatorTitle,
			PlanTitle:                phase.PlanTitle,
			Summary:                  phase.Summary,
			AuthorityClass:           phase.AuthorityClass,
			WhyNow:                   phase.WhyNow,
			BoundedEffect:            phase.BoundedEffect,
			AllowedActions:           append([]string(nil), phase.AllowedActions...),
			ForbiddenActions:         append([]string(nil), phase.ForbiddenActions...),
			ValidationPlan:           append([]string(nil), phase.ValidationPlan...),
			RequiredCapabilityGrants: append([]session.CapabilityGrantSpec(nil), phase.RequiredCapabilityGrants...),
		}
	} else {
		return
	}
	phase := continuationNarrowingPhaseFromBundle(opState, phases, bundlePhase)
	phaseID := firstNonEmptyContinuation(phase.ID, bundlePhase.OperationPhaseID, bundlePhase.ID)
	operationID := firstNonEmptyContinuation(opState.ID, bundle.OperationID)
	phasePlanID := firstNonEmptyContinuation(opState.PhasePlan.ID, bundle.PhasePlanID)
	family := operationPhaseApprovalFamily(phase)
	category := continuationNarrowingPhaseCategory(phase, family)

	prior, hasPrior := r.latestContinuationBundleNarrowing(key, operationID, phasePlanID, now)
	narrowStreak := 1
	if hasPrior && prior.NarrowStreak > 0 {
		narrowStreak = prior.NarrowStreak + 1
	}

	payload := map[string]any{
		"operation_id":              operationID,
		"phase_plan_id":             phasePlanID,
		"phase_id":                  phaseID,
		"current_phase_id":          phaseID,
		"phase_family":              family,
		"phase_category":            category,
		"current_phase_category":    category,
		"materialized_from":         strings.TrimSpace(materializedFrom),
		"bundle_id":                 strings.TrimSpace(bundle.ID),
		"bundle_width":              1,
		"bundle_phase_count":        1,
		"narrow_streak":             narrowStreak,
		"consecutive_narrow_count":  narrowStreak,
		"decision_id":               strings.TrimSpace(state.DecisionID),
		"lease_id":                  strings.TrimSpace(state.ContinuationLease.ID),
		"risk_class":                strings.TrimSpace(state.ActionProposal.RiskClass),
		"authority_class":           strings.TrimSpace(phase.AuthorityClass),
		"gate_level":                strings.TrimSpace(phase.GateLevel),
		"gate_reason_code":          strings.TrimSpace(phase.GateReasonCode),
		"approval_subject":          strings.TrimSpace(phase.ApprovalSubject),
		"requires_consent":          phase.RequiresConsent,
		"requires_opt_in":           phase.RequiresOptIn,
		"required_capability_count": len(phase.RequiredCapabilityGrants),
	}
	if hasPrior {
		payload["prior_phase_id"] = prior.PhaseID
		payload["prior_materialized_from"] = prior.MaterializedFrom
		payload["prior_bundle_width"] = 1
		if !prior.CreatedAt.IsZero() {
			payload["prior_observed_at"] = prior.CreatedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationBundleNarrowed, "continuation", "observed", payload, now)
}

func continuationNarrowingPhaseFromBundle(opState session.OperationState, phases []session.OperationPhase, bundlePhase session.ContinuationApprovalBundlePhase) session.OperationPhase {
	opState = session.NormalizeOperationState(opState)
	bundlePhase = session.NormalizeContinuationApprovalBundlePhase(bundlePhase)
	phaseID := strings.TrimSpace(bundlePhase.OperationPhaseID)
	for _, phase := range phases {
		phase = normalizeSingleOperationPhase(phase)
		if phaseID == "" || strings.TrimSpace(phase.ID) == phaseID {
			return phase
		}
	}
	for _, phase := range opState.PhasePlan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if strings.TrimSpace(phase.ID) == phaseID {
			return phase
		}
	}
	return normalizeSingleOperationPhase(session.OperationPhase{
		ID:                       firstNonEmptyContinuation(bundlePhase.OperationPhaseID, bundlePhase.ID),
		OperatorTitle:            bundlePhase.OperatorTitle,
		PlanTitle:                bundlePhase.PlanTitle,
		Summary:                  bundlePhase.Summary,
		AuthorityClass:           bundlePhase.AuthorityClass,
		WhyNow:                   bundlePhase.WhyNow,
		BoundedEffect:            bundlePhase.BoundedEffect,
		AllowedActions:           append([]string(nil), bundlePhase.AllowedActions...),
		ForbiddenActions:         append([]string(nil), bundlePhase.ForbiddenActions...),
		ValidationPlan:           append([]string(nil), bundlePhase.ValidationPlan...),
		RequiredCapabilityGrants: append([]session.CapabilityGrantSpec(nil), bundlePhase.RequiredCapabilityGrants...),
	})
}

func (r *Runtime) latestContinuationBundleNarrowing(key session.SessionKey, operationID string, phasePlanID string, before time.Time) (continuationBundleNarrowingObservation, bool) {
	if r == nil || r.store == nil {
		return continuationBundleNarrowingObservation{}, false
	}
	events, err := r.store.LatestExecutionEventsBySession(key, continuationBundleNarrowingEventLimit)
	if err != nil {
		return continuationBundleNarrowingObservation{}, false
	}
	sort.Slice(events, func(i, j int) bool { return executionEventBefore(events[i], events[j]) })

	var latest continuationBundleNarrowingObservation
	found := false
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventContinuationBundleNarrowed {
			continue
		}
		if !before.IsZero() && event.CreatedAt.After(before) {
			continue
		}
		payload := executionEventPayload(event.PayloadJSON)
		if operationID != "" && payloadString(payload, "operation_id") != operationID {
			continue
		}
		if phasePlanID != "" && payloadString(payload, "phase_plan_id") != phasePlanID {
			continue
		}
		streak, _ := payloadInt64(payload, "narrow_streak")
		if streak <= 0 {
			streak, _ = payloadInt64(payload, "consecutive_narrow_count")
		}
		latest = continuationBundleNarrowingObservation{
			PhaseID:          payloadString(payload, "phase_id"),
			MaterializedFrom: payloadString(payload, "materialized_from"),
			NarrowStreak:     int(streak),
			CreatedAt:        event.CreatedAt,
		}
		found = true
	}
	return latest, found
}

func continuationNarrowingPhaseCategory(phase session.OperationPhase, family string) string {
	phase = normalizeSingleOperationPhase(phase)
	family = strings.TrimSpace(family)
	switch family {
	case "data_access", "child_wake", "capability_grant", "deploy_restart":
		return "authority"
	case "local_workspace":
		if continuationNarrowingPhaseTextHasAny(phase, "scope", "decide", "decision", "choose", "architecture", "design", "proposal", "policy") {
			return "decision"
		}
		return "mechanical"
	}
	if phase.RequiresConsent || phase.RequiresOptIn || len(phase.RequiredCapabilityGrants) > 0 {
		return "authority"
	}
	if continuationNarrowingPhaseTextHasAny(phase, "consent", "opt_in", "optin", "private", "privacy", "credential", "token", "secret", "capability", "grant", "deploy", "restart", "external_account", "third_party") {
		return "authority"
	}
	if continuationNarrowingPhaseTextHasAny(phase, "implement", "patch", "edit", "test", "validate", "verify", "lint", "build", "commit", "push", "open_pr", "open_pull_request", "merge", "review_own_pr") {
		return "mechanical"
	}
	if continuationNarrowingPhaseTextHasAny(phase, "review", "decide", "decision", "choose", "plan", "design", "architecture", "proposal", "scope") {
		return "decision"
	}
	return "unknown"
}

func continuationNarrowingPhaseTextHasAny(phase session.OperationPhase, needles ...string) bool {
	text := strings.ToLower(strings.Join([]string{
		phase.ID,
		phase.OperatorTitle,
		phase.PlanTitle,
		phase.Summary,
		phase.AuthorityClass,
		phase.WhyNow,
		phase.BoundedEffect,
		phase.GateLevel,
		phase.GateReasonCode,
		phase.ApprovalSubject,
		strings.Join(phase.AllowedActions, " "),
		strings.Join(phase.ForbiddenActions, " "),
	}, " "))
	normalized := strings.NewReplacer("-", "_", "/", "_", ".", "_", " ", "_").Replace(text)
	for _, needle := range needles {
		needle = strings.TrimSpace(strings.ToLower(needle))
		if needle == "" {
			continue
		}
		if strings.Contains(normalized, strings.NewReplacer("-", "_", "/", "_", ".", "_", " ", "_").Replace(needle)) {
			return true
		}
	}
	return false
}

func continuationBundleNarrowingSummary(payload map[string]any) string {
	parts := make([]string, 0, 7)
	if phaseID := payloadString(payload, "phase_id"); phaseID != "" {
		parts = append(parts, "phase_id="+phaseID)
	}
	if family := payloadString(payload, "phase_family"); family != "" {
		parts = append(parts, "phase_family="+family)
	}
	if category := payloadString(payload, "phase_category"); category != "" {
		parts = append(parts, "phase_category="+category)
	}
	if materializedFrom := payloadString(payload, "materialized_from"); materializedFrom != "" {
		parts = append(parts, "materialized_from="+materializedFrom)
	}
	if streak, ok := payloadInt64(payload, "narrow_streak"); ok {
		parts = append(parts, fmt.Sprintf("narrow_streak=%d", streak))
	}
	if priorPhaseID := payloadString(payload, "prior_phase_id"); priorPhaseID != "" {
		parts = append(parts, "prior_phase_id="+priorPhaseID)
	}
	return strings.Join(parts, " ")
}
