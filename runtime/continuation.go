//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/turn"
)

type continuationConsensus struct {
	PersonaRationale  string
	GovernorRationale string
	PlanState         session.PlanState
	OperationState    session.OperationState
}

func (c continuationConsensus) eligible() bool {
	return strings.TrimSpace(c.PersonaRationale) != "" && strings.TrimSpace(c.GovernorRationale) != ""
}

func (r *Runtime) offerContinuationApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, result *turn.Result) error {
	if r == nil || r.outbound == nil || r.store == nil {
		return nil
	}
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	if !ok {
		return nil
	}
	consensus := r.buildContinuationConsensus(key, result)
	if !consensus.eligible() {
		if err := r.clearPendingContinuationOffer(key); err != nil {
			return fmt.Errorf("clear stale continuation approval: %w", err)
		}
		return nil
	}
	objective, nextStep := summarizeContinuationPlan(consensus.PlanState, consensus.OperationState, promptInput)
	decisionID := newContinuationDecisionID()
	state := session.ContinuationState{
		Kind:           session.TurnAuthorizationKindContinuation,
		Status:         session.ContinuationStatusPending,
		DecisionID:     decisionID,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: 1,
		UpdatedAt:      time.Now().UTC(),
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return fmt.Errorf("persist continuation state: %w", err)
	}
	_, err := sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		renderContinuationPrompt(state, consensus.PersonaRationale, consensus.GovernorRationale),
		continuationApprovalButtonRows(decisionID),
		nil,
	)
	if err != nil {
		return fmt.Errorf("send continuation approval: %w", err)
	}
	return nil
}

func (r *Runtime) buildContinuationConsensus(key session.SessionKey, result *turn.Result) continuationConsensus {
	planState, _ := r.store.PlanState(key)
	operationState, _ := r.store.OperationState(key)
	planState = session.NormalizePlanState(planState)
	operationState = session.NormalizeOperationState(operationState)

	governorRationale := continuationGovernorConsensus(planState, operationState)
	if !governorContinuationRatified(planState, operationState) {
		governorRationale = ""
	}

	return continuationConsensus{
		PersonaRationale:  continuationPersonaRationale(result),
		GovernorRationale: governorRationale,
		PlanState:         planState,
		OperationState:    operationState,
	}
}

func continuationPersonaRationale(result *turn.Result) string {
	if result == nil {
		return ""
	}
	return clampContinuationText(result.ProposalNote, 220)
}

func continuationGovernorConsensus(planState session.PlanState, operationState session.OperationState) string {
	planState = session.NormalizePlanState(planState)
	operationState = session.NormalizeOperationState(operationState)
	return firstNonEmptyContinuation(
		clampContinuationText(operationState.Proposal.WhyNow, 220),
		clampContinuationText(operationState.Proposal.Summary, 220),
		clampContinuationText(operationState.Summary, 220),
		clampContinuationText(continuationNextStep(planState, operationState), 220),
		clampContinuationText(operationState.Stage, 220),
		clampContinuationText(planState.Explanation, 220),
	)
}

func governorContinuationRatified(planState session.PlanState, operationState session.OperationState) bool {
	planState = session.NormalizePlanState(planState)
	operationState = session.NormalizeOperationState(operationState)
	if operationState.Proposal.Status == session.ProposalStatusApproved {
		return true
	}
	if operationState.Status == session.OperationStatusActive || operationState.Status == session.OperationStatusBlocked {
		return true
	}
	return hasActivePlanStep(planState)
}

func hasActivePlanStep(planState session.PlanState) bool {
	planState = session.NormalizePlanState(planState)
	for _, step := range planState.Steps {
		if step.Status == session.PlanStatusInProgress || step.Status == session.PlanStatusPending {
			return true
		}
	}
	return false
}

func (r *Runtime) clearPendingContinuationOffer(key session.SessionKey) error {
	if r == nil || r.store == nil {
		return nil
	}
	state, exists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending && state.Status != session.ContinuationStatusApproved {
		return nil
	}
	return r.store.UpdateContinuationState(key, session.ContinuationState{
		Kind:      session.TurnAuthorizationKindContinuation,
		Status:    session.ContinuationStatusIdle,
		UpdatedAt: time.Now().UTC(),
	})
}

func summarizeContinuationPlan(planState session.PlanState, operationState session.OperationState, promptInput string) (objective string, nextStep string) {
	planState = session.NormalizePlanState(planState)
	operationState = session.NormalizeOperationState(operationState)

	objective = firstNonEmptyContinuation(
		operationState.Objective,
		operationState.Summary,
		planState.Explanation,
		summarizeContinuationFallback(promptInput),
	)
	nextStep = continuationNextStep(planState, operationState)
	if nextStep == "" {
		nextStep = "Resume the next bounded step from this thread."
	}
	return objective, nextStep
}

func continuationNextStep(planState session.PlanState, operationState session.OperationState) string {
	for _, step := range planState.Steps {
		if step.Status == session.PlanStatusInProgress || step.Status == session.PlanStatusPending {
			return step.Step
		}
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

func renderContinuationPrompt(state session.ContinuationState, personaRationale string, governorRationale string) string {
	lines := []string{"I can continue from here."}
	if personaRationale != "" {
		lines = append(lines, "", "Persona rationale:", personaRationale)
	}
	if governorRationale != "" {
		lines = append(lines, "", "Governor rationale:", governorRationale)
	}
	if state.Objective != "" {
		lines = append(lines, "", "Objective:", state.Objective)
	}
	if state.StageSummary != "" {
		lines = append(lines, "", "Next:", state.StageSummary)
	}
	lines = append(lines, "", fmt.Sprintf("Approve %d more turn(s)?", state.RemainingTurns))
	return strings.Join(lines, "\n")
}

func continuationApprovalButtonRows(decisionID string) [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, "stop")},
		{Text: "Continue", CallbackData: encodeContinuationCallbackData(decisionID, "approve")},
	}}
}

func newContinuationDecisionID() string {
	return fmt.Sprintf("%x", time.Now().UTC().UnixNano())
}

func encodeContinuationCallbackData(decisionID string, action string) string {
	decisionID = strings.TrimSpace(decisionID)
	action = strings.TrimSpace(action)
	if decisionID == "" {
		return "continuation:" + action
	}
	return "continuation:" + decisionID + ":" + action
}
