//go:build linux

package main

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const continuationCallbackPrefix = "continuation:"
const staleContinuationCallbackText = "This continuation prompt is no longer active. Use the newest prompt."

func continuationApprovalRows() [][]telegram.InlineButton {
	return [][]telegram.InlineButton{{
		{Text: "Stop", CallbackData: encodeContinuationCallbackData("decision", "stop")},
		{Text: "Continue", CallbackData: encodeContinuationCallbackData("decision", "approve")},
	}}
}

func encodeContinuationCallbackData(decisionID string, action string) string {
	decisionID = strings.TrimSpace(decisionID)
	action = strings.TrimSpace(action)
	if decisionID == "" {
		return continuationCallbackPrefix + action
	}
	return continuationCallbackPrefix + decisionID + ":" + action
}

func decodeContinuationCallbackData(data string) (decisionID string, action string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, continuationCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, continuationCallbackPrefix))
	if payload == "" {
		return "", "", false
	}
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) == 1 {
		action = strings.TrimSpace(parts[0])
		if action == "" {
			return "", "", false
		}
		return "", action, true
	}
	decisionID = strings.TrimSpace(parts[0])
	action = strings.TrimSpace(parts[1])
	if decisionID == "" || action == "" {
		return "", "", false
	}
	return decisionID, action, true
}

func continuationCallbackMatchesState(state session.ContinuationState, decisionID string, action string) bool {
	state = session.NormalizeContinuationState(state)
	decisionID = strings.TrimSpace(decisionID)
	action = strings.TrimSpace(action)
	if decisionID == "" || state.DecisionID == "" {
		return false
	}
	if decisionID != state.DecisionID {
		return false
	}
	switch action {
	case "approve":
		return state.Status == session.ContinuationStatusPending && state.RemainingTurns > 0
	case "stop":
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	default:
		return false
	}
}

func renderContinuationDecision(state session.ContinuationState, approved bool) string {
	if !approved {
		return ""
	}
	text := "Continuation approved."
	if state.RemainingTurns > 0 {
		text += fmt.Sprintf(" Remaining turns: %d.", state.RemainingTurns)
	}
	if state.StageSummary != "" {
		text += " Next: " + state.StageSummary
	}
	return text
}
