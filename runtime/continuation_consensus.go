//go:build linux

package runtime

import (
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

type continuationConsensus struct {
	PersonaIntent  session.ContinuationIntent
	GovernorIntent session.ContinuationIntent
	BlockedReason  string
	PlanState      session.PlanState
	OperationState session.OperationState
}

func (c continuationConsensus) eligible() bool {
	return strings.TrimSpace(c.BlockedReason) == "" &&
		c.PersonaIntent.Decision == session.ContinuationIntentDecisionContinue &&
		c.GovernorIntent.Decision == session.ContinuationIntentDecisionContinue &&
		strings.TrimSpace(c.PersonaIntent.Rationale) != "" &&
		strings.TrimSpace(c.GovernorIntent.Rationale) != "" &&
		c.GovernorIntent.Ratified
}

func shouldNotifyContinuationBlocked(priorState session.ContinuationState, priorExists bool, consensus continuationConsensus) bool {
	if consensus.eligible() || !priorExists {
		return false
	}
	if continuationConsensusShouldCloseQuietly(consensus) {
		return false
	}
	priorState = session.NormalizeContinuationState(priorState)
	return priorState.Status == session.ContinuationStatusPending || priorState.Status == session.ContinuationStatusApproved
}

func continuationConsensusShouldCloseQuietly(consensus continuationConsensus) bool {
	if continuationPlanHasOpenStep(consensus.PlanState) {
		return false
	}
	opState := session.NormalizeOperationState(consensus.OperationState)
	return opState.Status == session.OperationStatusCompleted
}

func continuationConsensusHasTypedRemainingWork(consensus continuationConsensus) bool {
	planState := session.NormalizePlanState(consensus.PlanState)
	opState := session.NormalizeOperationState(consensus.OperationState)
	for _, step := range planState.Steps {
		if (step.Status == session.PlanStatusInProgress || step.Status == session.PlanStatusPending) && organicProposalConcreteStateStep(step.Step) {
			return true
		}
	}
	if operationStateTerminalForContinuation(opState) {
		return false
	}
	if pendingOperationProposalNeedsButton(opState.Proposal) || pendingOperationPlanLeaseNeedsButton(opState.PlanLease) {
		return true
	}
	if _, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC()); ok {
		return true
	}
	if bundle, ok := nextOperationPhaseBundleForApproval(opState); ok && len(bundle) > 0 {
		return true
	}
	if _, ok := nextOperationPhaseForApproval(opState); ok {
		return true
	}
	if opState.Status != session.OperationStatusActive && opState.Status != session.OperationStatusBlocked {
		return false
	}
	proposal := opState.Proposal
	if proposal.Active() && proposal.Status != session.ProposalStatusPending {
		proposal = session.OperationProposal{}
	}
	for _, text := range []string{
		proposal.Summary,
		proposal.BoundedEffect,
		opState.Summary,
		opState.Stage,
	} {
		if organicProposalConcreteStateStep(text) {
			return true
		}
	}
	return false
}

func (r *Runtime) buildContinuationConsensus(key session.SessionKey, result *turn.Result) continuationConsensus {
	planState, _ := r.store.PlanState(key)
	operationState, _ := r.store.OperationState(key)
	planState = session.NormalizePlanState(planState)
	operationState = session.NormalizeOperationState(operationState)

	personaIntent := continuationPersonaIntent(result, planState, operationState)
	governorIntent := continuationGovernorIntent(result, planState, operationState)

	return continuationConsensus{
		PersonaIntent:  personaIntent,
		GovernorIntent: governorIntent,
		BlockedReason:  continuationHandshakeBlockedReason(personaIntent, governorIntent),
		PlanState:      planState,
		OperationState: operationState,
	}
}

func continuationPersonaIntent(result *turn.Result, planState session.PlanState, operationState session.OperationState) session.ContinuationIntent {
	intent := session.ContinuationIntent{}
	if result != nil {
		intent = result.PersonaIntent
	}
	intent = normalizeParsedContinuationIntent(intent)
	if intent.NextStep == "" {
		intent.NextStep = clampContinuationText(continuationNextStep(planState, operationState), 220)
	}
	return intent
}

func continuationGovernorIntent(result *turn.Result, planState session.PlanState, operationState session.OperationState) session.ContinuationIntent {
	intent := session.ContinuationIntent{}
	if result != nil {
		intent = result.GovernorIntent
	}
	intent = normalizeParsedContinuationIntent(intent)
	if intent.NextStep == "" {
		intent.NextStep = clampContinuationText(continuationNextStep(planState, operationState), 220)
	}
	if intent.Constraints == "" {
		intent.Constraints = clampContinuationText(firstNonEmptyContinuation(operationState.Proposal.BoundedEffect, operationState.Stage), 220)
	}
	return intent
}

func continuationHandshakeBlockedReason(persona session.ContinuationIntent, governor session.ContinuationIntent) string {
	if persona.Decision == "" {
		return "persona_intent_missing"
	}
	if strings.TrimSpace(persona.Rationale) == "" {
		return "persona_rationale_missing"
	}
	if persona.Decision != session.ContinuationIntentDecisionContinue {
		return "persona_not_willing"
	}
	if governor.Decision == "" {
		return "governor_intent_missing"
	}
	if strings.TrimSpace(governor.Rationale) == "" {
		return "governor_rationale_missing"
	}
	if !governor.Ratified {
		return "governor_not_ratified"
	}
	if governor.Decision != session.ContinuationIntentDecisionContinue {
		return "governor_not_willing"
	}
	return ""
}

func summarizeContinuationPlan(planState session.PlanState, operationState session.OperationState, promptInput string) (objective string, nextStep string) {
	planState = session.NormalizePlanState(planState)
	operationState = session.NormalizeOperationState(operationState)

	if operationStateTerminalForContinuation(operationState) {
		objective = firstNonEmptyContinuation(
			planState.Explanation,
			summarizeContinuationFallback(promptInput),
		)
	} else {
		objective = firstNonEmptyContinuation(
			operationState.Objective,
			operationState.Summary,
			planState.Explanation,
			summarizeContinuationFallback(promptInput),
		)
	}
	nextStep = continuationNextStep(planState, operationState)
	if nextStep == "" {
		nextStep = "Resume the next bounded step from this thread."
	}
	return objective, nextStep
}

func continuationPlanHasOpenStep(planState session.PlanState) bool {
	planState = session.NormalizePlanState(planState)
	for _, step := range planState.Steps {
		if strings.TrimSpace(step.Step) == "" {
			continue
		}
		if step.Status == session.PlanStatusInProgress || step.Status == session.PlanStatusPending {
			return true
		}
	}
	return false
}

func operationStateTerminalForContinuation(operationState session.OperationState) bool {
	operationState = session.NormalizeOperationState(operationState)
	return operationState.Status == session.OperationStatusCompleted || operationState.Status == session.OperationStatusFailed
}

func continuationNextStep(planState session.PlanState, operationState session.OperationState) string {
	for _, step := range planState.Steps {
		if step.Status == session.PlanStatusInProgress || step.Status == session.PlanStatusPending {
			return step.Step
		}
	}
	if operationStateTerminalForContinuation(operationState) {
		return ""
	}
	if strings.TrimSpace(operationState.Proposal.Summary) != "" {
		return operationState.Proposal.Summary
	}
	if strings.TrimSpace(operationState.Proposal.BoundedEffect) != "" {
		return operationState.Proposal.BoundedEffect
	}
	if strings.TrimSpace(operationState.Stage) != "" {
		return operationState.Stage
	}
	return ""
}

func summarizeContinuationFallback(promptInput string) string {
	trimmed := strings.TrimSpace(promptInput)
	if trimmed == "" {
		return "Continue the current thread."
	}
	if len(trimmed) > 160 {
		trimmed = strings.TrimSpace(trimmed[:160]) + "…"
	}
	return trimmed
}

func firstNonEmptyContinuation(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func clampContinuationText(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "…"
}
