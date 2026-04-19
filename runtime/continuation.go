//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
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

func (r *Runtime) offerContinuationApproval(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, result *turn.Result) error {
	if r == nil || r.outbound == nil || r.store == nil {
		return nil
	}
	priorState, priorExists, _ := r.store.ContinuationStateIfExists(key)
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	if !ok {
		return nil
	}

	consensus := r.buildContinuationConsensus(key, result)
	objective, nextStep := summarizeContinuationPlan(consensus.PlanState, consensus.OperationState, promptInput)

	state := session.ContinuationState{
		Kind:                   session.TurnAuthorizationKindContinuation,
		Status:                 session.ContinuationStatusIdle,
		Objective:              objective,
		StageSummary:           nextStep,
		RemainingTurns:         0,
		PersonaIntent:          consensus.PersonaIntent,
		GovernorIntent:         consensus.GovernorIntent,
		HandshakeBlockedReason: consensus.BlockedReason,
		UpdatedAt:              time.Now().UTC(),
	}
	if consensus.eligible() {
		state.Status = session.ContinuationStatusPending
		state.DecisionID = newContinuationDecisionID()
		state.RemainingTurns = 1
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return fmt.Errorf("persist continuation state: %w", err)
	}
	if !consensus.eligible() {
		if shouldNotifyContinuationBlocked(priorState, priorExists, consensus) {
			if err := r.sendContinuationBlockedNotice(ctx, msg, state); err != nil {
				return err
			}
		}
		return nil
	}

	_, err := sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		r.renderContinuationPrompt(ctx, msg, state),
		continuationApprovalButtonRows(state.DecisionID),
		nil,
	)
	if err != nil {
		return fmt.Errorf("send continuation approval: %w", err)
	}
	return nil
}

func shouldNotifyContinuationBlocked(priorState session.ContinuationState, priorExists bool, consensus continuationConsensus) bool {
	if consensus.eligible() || !priorExists {
		return false
	}
	priorState = session.NormalizeContinuationState(priorState)
	return priorState.Status == session.ContinuationStatusPending || priorState.Status == session.ContinuationStatusApproved
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

func (r *Runtime) sendContinuationBlockedNotice(ctx context.Context, msg core.InboundMessage, state session.ContinuationState) error {
	if r == nil || r.outbound == nil {
		return nil
	}
	text := strings.TrimSpace(r.renderContinuationBlockedNotice(ctx, msg, state))
	if text == "" {
		return nil
	}
	_, err := r.outbound.SendMessage(ctx, core.OutboundMessage{
		ChatID: msg.ChatID,
		Text:   text,
	})
	if err != nil {
		return fmt.Errorf("send continuation blocked notice: %w", err)
	}
	return nil
}

func (r *Runtime) renderContinuationBlockedNotice(ctx context.Context, msg core.InboundMessage, state session.ContinuationState) string {
	fallback := renderContinuationBlockedFallback(state)
	if r == nil {
		return fallback
	}
	if r.faceBackend == face.BackendFloorFallback {
		return fallback
	}
	renderer := r.currentFaceRenderer()
	if renderer == nil {
		return fallback
	}
	workspaceRoot := ""
	if r.cfg != nil {
		workspaceRoot = strings.TrimSpace(r.cfg.Agent.PromptRoot)
	}

	rendered, err := renderer.Render(ctx, face.RenderRequest{
		GovernorName:    prompt.DefaultGovernorName,
		FaceName:        face.DefaultFaceName,
		Channel:         "telegram",
		Mode:            "repair",
		PrincipalRole:   "approved_user",
		WorkspaceRoot:   workspaceRoot,
		FloorText:       fallback,
		LatestUserInput: strings.TrimSpace(msg.Text),
		CandidateReply:  fallback,
		RepairNotes: []string{
			"Keep this in first person as Idolum.",
			"Explain why continuation is unavailable right now.",
		},
		Runtime: prompt.RuntimeAwareness{
			ContinuationStatus:         string(state.Status),
			ContinuationActive:         state.Active(),
			ContinuationPersonaIntent:  string(state.PersonaIntent.Decision),
			ContinuationPersonaWhy:     state.PersonaIntent.Rationale,
			ContinuationGovernorIntent: string(state.GovernorIntent.Decision),
			ContinuationGovernorWhy:    state.GovernorIntent.Rationale,
			ContinuationRatified:       state.GovernorIntent.Ratified,
			ContinuationBlockedReason:  state.HandshakeBlockedReason,
		},
	})
	if err != nil {
		return fallback
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return fallback
	}
	return rendered
}

func renderContinuationBlockedFallback(state session.ContinuationState) string {
	reason := strings.TrimSpace(state.HandshakeBlockedReason)
	switch reason {
	case "persona_intent_missing":
		return "I can't continue yet because I did not publish a continuation intent for this turn."
	case "persona_rationale_missing":
		return "I can't continue yet because I did not provide a clear continuation rationale."
	case "persona_not_willing":
		return "I can't continue yet because I chose to hold this thread instead of auto-continuing."
	case "governor_intent_missing":
		return "I can't continue yet because Aphelion did not publish a continuation intent for this turn."
	case "governor_rationale_missing":
		return "I can't continue yet because Aphelion did not provide a continuation rationale."
	case "governor_not_ratified":
		return "I can't continue yet because Aphelion did not ratify continuation for this turn."
	case "governor_not_willing":
		return "I can't continue yet because Aphelion explicitly held continuation for this turn."
	default:
		return "I can't continue this thread yet because the continuation handshake is still blocked."
	}
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

func (r *Runtime) renderContinuationPrompt(ctx context.Context, msg core.InboundMessage, state session.ContinuationState) string {
	fallback := renderContinuationPromptFallback(state)
	if r == nil {
		return fallback
	}
	if r.faceBackend == face.BackendFloorFallback {
		return fallback
	}
	renderer := r.currentFaceRenderer()
	if renderer == nil {
		return fallback
	}
	workspaceRoot := ""
	if r.cfg != nil {
		workspaceRoot = strings.TrimSpace(r.cfg.Agent.PromptRoot)
	}

	rendered, err := renderer.Render(ctx, face.RenderRequest{
		GovernorName:    prompt.DefaultGovernorName,
		FaceName:        face.DefaultFaceName,
		Channel:         "telegram",
		Mode:            "repair",
		PrincipalRole:   "approved_user",
		WorkspaceRoot:   workspaceRoot,
		FloorText:       fallback,
		LatestUserInput: strings.TrimSpace(msg.Text),
		CandidateReply:  fallback,
		RepairNotes: []string{
			"Keep this in first person as Idolum.",
			"Frame continuation as one coherent system thought, not a dialogue between internal roles.",
			"Do not use labels like Persona intent, Persona rationale, Governor intent, or Governor rationale.",
			"Keep the boundaries, objective, and next step explicit.",
		},
		Runtime: prompt.RuntimeAwareness{
			ContinuationStatus:         string(state.Status),
			ContinuationActive:         state.Active(),
			ContinuationPersonaIntent:  string(state.PersonaIntent.Decision),
			ContinuationPersonaWhy:     state.PersonaIntent.Rationale,
			ContinuationGovernorIntent: string(state.GovernorIntent.Decision),
			ContinuationGovernorWhy:    state.GovernorIntent.Rationale,
			ContinuationRatified:       state.GovernorIntent.Ratified,
			ContinuationBlockedReason:  state.HandshakeBlockedReason,
			OperationObjective:         state.Objective,
			OperationSummary:           state.StageSummary,
			ProposalBoundedEffect:      state.GovernorIntent.Constraints,
		},
	})
	if err != nil {
		return fallback
	}
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return fallback
	}
	if continuationPromptHasSplitRoleLabels(rendered) {
		return fallback
	}
	return rendered
}

func continuationPromptHasSplitRoleLabels(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, marker := range []string{
		"persona intent:",
		"persona rationale:",
		"governor intent:",
		"governor rationale:",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func renderContinuationPromptFallback(state session.ContinuationState) string {
	lines := []string{"I can continue from here."}
	reasons := make([]string, 0, 2)
	if reason := strings.TrimSpace(state.PersonaIntent.Rationale); reason != "" {
		reasons = append(reasons, reason)
	}
	if reason := strings.TrimSpace(state.GovernorIntent.Rationale); reason != "" {
		reasons = append(reasons, reason)
	}
	if len(reasons) > 0 {
		lines = append(lines, "", "Why continuing makes sense:", strings.Join(reasons, " "))
	}
	if constraints := strings.TrimSpace(state.GovernorIntent.Constraints); constraints != "" {
		lines = append(lines, "", "Boundaries:", constraints)
	}
	if objective := strings.TrimSpace(state.Objective); objective != "" {
		lines = append(lines, "", "Objective:", objective)
	}
	if nextStep := strings.TrimSpace(state.StageSummary); nextStep != "" {
		lines = append(lines, "", "Next step:", nextStep)
	}
	lines = append(lines, "", fmt.Sprintf("Should I continue for %d more turn(s)?", state.RemainingTurns))
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
