//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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

const continuationOperationalStateNote = "operational continuation_state remains authoritative"
const continuationLeaseDefaultTTL = 30 * time.Minute

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
		state.ActionProposal = buildContinuationActionProposal(state.DecisionID, consensus, objective, nextStep, time.Now().UTC())
		state.ContinuationLease = buildContinuationLease(state.ActionProposal, state.RemainingTurns, time.Now().UTC())
	}
	if err := r.store.UpdateContinuationState(key, state); err != nil {
		return fmt.Errorf("persist continuation state: %w", err)
	}
	if !consensus.eligible() {
		payload := continuationExecutionPayload(state)
		payload["reason"] = strings.TrimSpace(consensus.BlockedReason)
		notify := shouldNotifyContinuationBlocked(priorState, priorExists, consensus)
		payload["user_visible"] = notify
		payload["prior_active"] = priorExists && session.NormalizeContinuationState(priorState).Active()
		r.recordExecutionEvent(key, core.ExecutionEventContinuationBlocked, "continuation", "blocked", payload, time.Now().UTC())
		if notify {
			if err := r.sendContinuationBlockedNotice(ctx, key, msg, state); err != nil {
				return err
			}
		}
		return nil
	}
	r.recordExecutionEvent(key, core.ExecutionEventContinuationOffered, "continuation", "pending", continuationExecutionPayload(state), time.Now().UTC())

	_, err := sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		r.renderContinuationPrompt(ctx, key, msg, state),
		continuationApprovalButtonRows(continuationCallbackID(state)),
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

func (r *Runtime) sendContinuationBlockedNotice(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState) error {
	if r == nil || r.outbound == nil {
		return nil
	}
	text := strings.TrimSpace(r.renderContinuationBlockedNotice(ctx, key, msg, state))
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

func (r *Runtime) renderContinuationBlockedNotice(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState) string {
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
	grounded, note := r.groundContinuationBlockedNoticeWithExecutionEvidence(key, state, rendered)
	if note != "" {
		log.Printf("WARN continuation blocked notice grounding fallback chat_id=%d note=%s", key.ChatID, note)
	}
	return grounded
}

func (r *Runtime) groundContinuationBlockedNoticeWithExecutionEvidence(
	key session.SessionKey,
	state session.ContinuationState,
	candidate string,
) (string, string) {
	candidate = strings.TrimSpace(candidate)
	fallback := renderContinuationBlockedFallback(state)
	if candidate == "" {
		return fallback, "rendered continuation blocked notice is empty"
	}
	if r == nil || r.store == nil {
		return candidate, ""
	}
	events, err := r.store.ExecutionEventsBySession(key, 0, 300)
	if err != nil || len(events) == 0 {
		return fallback, "continuation evidence is unavailable; " + continuationOperationalStateNote
	}
	latestType := ""
	for _, event := range events {
		eventType := strings.TrimSpace(event.EventType)
		switch eventType {
		case core.ExecutionEventContinuationOffered,
			core.ExecutionEventContinuationApproved,
			core.ExecutionEventContinuationRevoked,
			core.ExecutionEventContinuationConsumed,
			core.ExecutionEventContinuationBlocked:
			latestType = eventType
		}
	}
	if latestType != core.ExecutionEventContinuationBlocked {
		return fallback, fmt.Sprintf("blocked notice is not grounded by blocked continuation event (latest=%s); %s", latestType, continuationOperationalStateNote)
	}
	if strings.TrimSpace(state.HandshakeBlockedReason) == "" {
		return fallback, "blocked notice state has no blocked reason"
	}
	return candidate, ""
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
		return fmt.Sprintf("I can't continue yet because %s did not publish a continuation intent for this turn.", prompt.DefaultGovernorName)
	case "governor_rationale_missing":
		return fmt.Sprintf("I can't continue yet because %s did not provide a continuation rationale.", prompt.DefaultGovernorName)
	case "governor_not_ratified":
		return fmt.Sprintf("I can't continue yet because %s did not ratify continuation for this turn.", prompt.DefaultGovernorName)
	case "governor_not_willing":
		return fmt.Sprintf("I can't continue yet because %s explicitly held continuation for this turn.", prompt.DefaultGovernorName)
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

func (r *Runtime) renderContinuationPrompt(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState) string {
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
	grounded, note := r.groundContinuationPromptWithExecutionEvidence(key, state, rendered)
	if note != "" {
		log.Printf("WARN continuation prompt grounding fallback chat_id=%d decision_id=%s note=%s", key.ChatID, strings.TrimSpace(state.DecisionID), note)
	}
	return grounded
}

func (r *Runtime) groundContinuationPromptWithExecutionEvidence(
	key session.SessionKey,
	state session.ContinuationState,
	candidate string,
) (string, string) {
	candidate = strings.TrimSpace(candidate)
	fallback := renderContinuationPromptFallback(state)
	if candidate == "" {
		return fallback, "rendered continuation prompt is empty"
	}
	if r == nil || r.store == nil {
		return candidate, ""
	}
	decisionID := strings.TrimSpace(state.DecisionID)
	if decisionID == "" {
		return fallback, "continuation decision id is missing"
	}
	events, err := r.store.ExecutionEventsBySession(key, 0, 300)
	if err != nil || len(events) == 0 {
		return fallback, "continuation evidence is unavailable; " + continuationOperationalStateNote
	}

	latestType := ""
	for _, event := range events {
		eventType := strings.TrimSpace(event.EventType)
		switch eventType {
		case core.ExecutionEventContinuationOffered,
			core.ExecutionEventContinuationApproved,
			core.ExecutionEventContinuationRevoked,
			core.ExecutionEventContinuationConsumed,
			core.ExecutionEventContinuationBlocked:
		default:
			continue
		}
		payload := executionEventPayload(event.PayloadJSON)
		if strings.TrimSpace(payloadString(payload, "decision_id")) != decisionID {
			continue
		}
		latestType = eventType
	}
	if latestType == "" {
		return fallback, "no continuation event matches decision id; " + continuationOperationalStateNote
	}

	expectedStatus := session.NormalizeContinuationState(state).Status
	switch expectedStatus {
	case session.ContinuationStatusPending:
		if latestType != core.ExecutionEventContinuationOffered {
			return fallback, fmt.Sprintf("pending continuation is not grounded by offered event (latest=%s); %s", latestType, continuationOperationalStateNote)
		}
	case session.ContinuationStatusApproved:
		if latestType != core.ExecutionEventContinuationApproved {
			return fallback, fmt.Sprintf("approved continuation is not grounded by approved event (latest=%s); %s", latestType, continuationOperationalStateNote)
		}
	}
	return candidate, ""
}

func continuationExecutionPayload(state session.ContinuationState) map[string]any {
	payload := map[string]any{
		"decision_id":     strings.TrimSpace(state.DecisionID),
		"objective":       strings.TrimSpace(state.Objective),
		"stage_summary":   strings.TrimSpace(state.StageSummary),
		"remaining_turns": state.RemainingTurns,
		"state_source":    "continuation_state",
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	if proposal.Active() {
		payload["proposal_id"] = strings.TrimSpace(proposal.ID)
		payload["proposal_status"] = strings.TrimSpace(string(proposal.Status))
		payload["risk_class"] = strings.TrimSpace(proposal.RiskClass)
		payload["plan_hash"] = strings.TrimSpace(proposal.PlanHash)
		if !proposal.ExpiresAt.IsZero() {
			payload["expires_at"] = proposal.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
	}
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	if strings.TrimSpace(lease.ID) != "" || strings.TrimSpace(lease.ProposalID) != "" {
		payload["lease_id"] = strings.TrimSpace(lease.ID)
		payload["lease_status"] = strings.TrimSpace(string(lease.Status))
		payload["lease_remaining_turns"] = lease.RemainingTurns
		payload["lease_max_turns"] = lease.MaxTurns
	}
	return payload
}

func buildContinuationActionProposal(decisionID string, consensus continuationConsensus, objective string, nextStep string, now time.Time) session.ActionProposal {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	op := session.NormalizeOperationState(consensus.OperationState)
	proposal := op.Proposal
	actionProposal := session.ActionProposal{
		ID:               "aprop-" + strings.TrimSpace(decisionID),
		OperationID:      strings.TrimSpace(op.ID),
		Summary:          firstNonEmptyContinuation(proposal.Summary, nextStep, objective),
		WhyNow:           firstNonEmptyContinuation(proposal.WhyNow, consensus.GovernorIntent.Rationale, consensus.PersonaIntent.Rationale),
		BoundedEffect:    firstNonEmptyContinuation(proposal.BoundedEffect, consensus.GovernorIntent.Constraints, "Resume one bounded continuation turn and report the result."),
		RiskClass:        firstNonEmptyContinuation(proposal.Kind, "continuation"),
		AllowedActions:   []string{"continue_one_turn", "use_existing_authority_only", "report_evidence"},
		ForbiddenActions: []string{"expand_authority_without_new_approval", "external_effect_outside_bounded_effect", "ignore_stop_or_revocation"},
		ValidationPlan:   []string{"consume at most the approved continuation turn", "report what changed and what evidence supports it"},
		ExpiresAt:        now.Add(continuationLeaseDefaultTTL),
		Status:           session.ProposalStatusPending,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	actionProposal.PlanHash = actionProposalHash(actionProposal)
	return session.NormalizeActionProposal(actionProposal)
}

func buildContinuationLease(proposal session.ActionProposal, turns int, now time.Time) session.ContinuationLease {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	proposal = session.NormalizeActionProposal(proposal)
	if turns <= 0 {
		turns = 1
	}
	lease := session.ContinuationLease{
		ID:               "lease-" + strings.TrimPrefix(strings.TrimSpace(proposal.ID), "aprop-"),
		ProposalID:       strings.TrimSpace(proposal.ID),
		MissionID:        strings.TrimSpace(proposal.MissionID),
		Status:           session.ContinuationLeaseStatusPending,
		MaxTurns:         turns,
		RemainingTurns:   turns,
		AllowedActions:   append([]string(nil), proposal.AllowedActions...),
		ForbiddenActions: append([]string(nil), proposal.ForbiddenActions...),
		ValidationPlan:   append([]string(nil), proposal.ValidationPlan...),
		ExpiresAt:        proposal.ExpiresAt,
		PlanHash:         proposal.PlanHash,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	return session.NormalizeContinuationLease(lease)
}

func actionProposalHash(proposal session.ActionProposal) string {
	proposal.PlanHash = ""
	proposal.CreatedAt = time.Time{}
	proposal.UpdatedAt = time.Time{}
	raw, err := json.Marshal(proposal)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func continuationStateWithLeaseApproved(state session.ContinuationState, approverID int64, now time.Time) (session.ContinuationState, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	if state.ActionProposal.Active() && !state.ActionProposal.ExpiresAt.IsZero() && !state.ActionProposal.ExpiresAt.After(now) {
		state.ActionProposal.Status = session.ProposalStatusExpired
		state.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
		state.ContinuationLease.RemainingTurns = 0
		state.Status = session.ContinuationStatusIdle
		state.RemainingTurns = 0
		state.UpdatedAt = now
		return session.NormalizeContinuationState(state), fmt.Errorf("continuation proposal expired")
	}
	if strings.TrimSpace(state.ContinuationLease.ID) == "" {
		if !state.ActionProposal.Active() {
			state.ActionProposal = buildContinuationActionProposal(state.DecisionID, continuationConsensus{PersonaIntent: state.PersonaIntent, GovernorIntent: state.GovernorIntent}, state.Objective, state.StageSummary, now)
		}
		state.ContinuationLease = buildContinuationLease(state.ActionProposal, state.RemainingTurns, now)
	}
	if !state.ContinuationLease.ExpiresAt.IsZero() && !state.ContinuationLease.ExpiresAt.After(now) {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
		state.ContinuationLease.RemainingTurns = 0
		state.ActionProposal.Status = session.ProposalStatusExpired
		state.Status = session.ContinuationStatusIdle
		state.RemainingTurns = 0
		state.UpdatedAt = now
		return session.NormalizeContinuationState(state), fmt.Errorf("continuation lease expired")
	}
	if state.RemainingTurns <= 0 {
		state.RemainingTurns = state.ContinuationLease.RemainingTurns
	}
	if state.RemainingTurns <= 0 {
		state.RemainingTurns = 1
	}
	state.Status = session.ContinuationStatusApproved
	state.ApprovedBy = approverID
	state.UpdatedAt = now
	state.ActionProposal.Status = session.ProposalStatusApproved
	state.ActionProposal.UpdatedAt = now
	state.ContinuationLease.Status = session.ContinuationLeaseStatusActive
	state.ContinuationLease.ApprovedBy = approverID
	state.ContinuationLease.ApprovedAt = now
	state.ContinuationLease.UpdatedAt = now
	if state.ContinuationLease.RemainingTurns <= 0 {
		state.ContinuationLease.RemainingTurns = state.RemainingTurns
	}
	if state.ContinuationLease.MaxTurns <= 0 {
		state.ContinuationLease.MaxTurns = state.ContinuationLease.RemainingTurns
	}
	return session.NormalizeContinuationState(state), nil
}

func continuationStateWithLeaseRevoked(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	if state.ActionProposal.Active() && state.ActionProposal.Status != session.ProposalStatusApproved {
		state.ActionProposal.Status = session.ProposalStatusDenied
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusRevoked
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.RevokedAt = now
		state.ContinuationLease.UpdatedAt = now
	}
	state.Status = session.ContinuationStatusRevoked
	state.RemainingTurns = 0
	state.ApprovedBy = 0
	state.DecisionID = ""
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationLeaseExpired(state session.ContinuationState, now time.Time) bool {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state = session.NormalizeContinuationState(state)
	lease := state.ContinuationLease
	return strings.TrimSpace(lease.ID) != "" && !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now.UTC())
}

func continuationStateWithLeaseExpired(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	state.Status = session.ContinuationStatusIdle
	state.RemainingTurns = 0
	state.ApprovedBy = 0
	state.DecisionID = ""
	if state.ActionProposal.Active() {
		state.ActionProposal.Status = session.ProposalStatusExpired
		state.ActionProposal.UpdatedAt = now
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		state.ContinuationLease.Status = session.ContinuationLeaseStatusExpired
		state.ContinuationLease.RemainingTurns = 0
		state.ContinuationLease.UpdatedAt = now
	}
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
}

func continuationStateAfterLeaseTurnConsumed(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	if state.RemainingTurns > 0 {
		state.RemainingTurns--
	}
	if strings.TrimSpace(state.ContinuationLease.ID) != "" || strings.TrimSpace(state.ContinuationLease.ProposalID) != "" {
		if state.ContinuationLease.RemainingTurns > 0 {
			state.ContinuationLease.RemainingTurns--
		}
		state.ContinuationLease.UpdatedAt = now
		if state.ContinuationLease.RemainingTurns <= 0 {
			state.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
			state.ContinuationLease.ConsumedAt = now
		}
	}
	if state.RemainingTurns <= 0 {
		state.Status = session.ContinuationStatusIdle
		state.DecisionID = ""
		state.ApprovedBy = 0
	}
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
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
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	if proposal.Active() {
		if effect := strings.TrimSpace(proposal.BoundedEffect); effect != "" {
			lines = append(lines, "", "Bounded effect:", effect)
		}
		if len(proposal.AllowedActions) > 0 {
			lines = append(lines, "", "Allowed actions:", strings.Join(proposal.AllowedActions, ", "))
		}
		if len(proposal.ForbiddenActions) > 0 {
			lines = append(lines, "", "Forbidden actions:", strings.Join(proposal.ForbiddenActions, ", "))
		}
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

func continuationCallbackID(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if id := strings.TrimSpace(state.ActionProposal.ID); id != "" {
		return id
	}
	if id := strings.TrimSpace(state.ContinuationLease.ProposalID); id != "" {
		return id
	}
	if id := strings.TrimSpace(state.ContinuationLease.ID); id != "" {
		return id
	}
	return strings.TrimSpace(state.DecisionID)
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
