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
	state := session.ContinuationState{
		Status:         session.ContinuationStatusPending,
		Objective:      summarizeContinuationObjective(promptInput),
		StageSummary:   "Resume the next bounded step from this thread.",
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

func summarizeContinuationObjective(promptInput string) string {
	trimmed := strings.TrimSpace(promptInput)
	if trimmed == "" {
		return "Continue the current thread."
	}
	if len(trimmed) > 160 {
		trimmed = strings.TrimSpace(trimmed[:160]) + "…"
	}
	return trimmed
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
