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
)

func (r *Runtime) offerContinuationApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string) error {
	if r == nil || r.outbound == nil {
		return nil
	}
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	if !ok {
		return nil
	}
	planState, _ := r.store.PlanState(key)
	operationState, _ := r.store.OperationState(key)
	objective, nextStep := summarizeContinuationPlan(planState, operationState, promptInput)
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		Objective:      objective,
		StageSummary:   nextStep,
		RemainingTurns: 1,
		UpdatedAt:      time.Now().UTC(),
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return fmt.Errorf("persist continuation state: %w", err)
	}
	_, err := sender.SendInlineKeyboard(ctx, msg.ChatID, renderContinuationPrompt(state), continuationApprovalButtonRows(), nil)
	if err != nil {
		return fmt.Errorf("send continuation approval: %w", err)
	}
	return nil
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

func renderContinuationPrompt(state session.ContinuationState) string {
	lines := []string{"I can continue from here."}
	if state.Objective != "" {
		lines = append(lines, "", "Objective:", state.Objective)
	}
	if state.StageSummary != "" {
		lines = append(lines, "", "Next:", state.StageSummary)
	}
	lines = append(lines, "", fmt.Sprintf("Approve %d more turn(s)?", state.RemainingTurns))
	return strings.Join(lines, "\n")
}

func continuationApprovalButtonRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Stop", CallbackData: "continuation:stop"},
		{Text: "Continue", CallbackData: "continuation:approve"},
	}}
}
