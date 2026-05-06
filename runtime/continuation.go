//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"unicode"

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

const (
	continuationActionApproveLease = "approve_lease"
	continuationActionContinueOnce = "continue_once"
	continuationActionAskEdit      = "ask_edit"
	continuationActionStopPark     = "stop_park"
	continuationActionResumeEdge   = "resume_edge"
	continuationActionAskNextLease = "ask_next_lease"
	continuationActionStatusOnly   = "status_only"
	continuationActionStop         = "stop"
)

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
	if approved, err := r.maybeAutoApproveContinuationOffer(ctx, key, msg, state, "organic_continuation"); approved || err != nil {
		return err
	}

	return r.sendContinuationApprovalPrompt(ctx, key, msg, state, r.renderContinuationPrompt(ctx, key, msg, state))
}

func (r *Runtime) sendContinuationApprovalPrompt(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, text string) error {
	sender, ok := r.continuationApprovalPromptSender()
	if !ok {
		return nil
	}
	_, err := sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		text,
		continuationApprovalButtonRows(state),
		nil,
	)
	if err != nil {
		return fmt.Errorf("send continuation approval: %w", err)
	}
	return nil
}

func (r *Runtime) continuationApprovalPromptSender() (interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}, bool) {
	if r == nil || r.outbound == nil {
		return nil, false
	}
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	return sender, ok
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
		GovernorName:    r.governorName(),
		FaceName:        r.faceName(),
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
		GovernorName:    r.governorName(),
		FaceName:        r.faceName(),
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
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if bundle.Active() {
		payload["bundle_id"] = strings.TrimSpace(bundle.ID)
		payload["bundle_status"] = strings.TrimSpace(string(bundle.Status))
		payload["bundle_current_phase_id"] = strings.TrimSpace(bundle.CurrentPhaseID)
		payload["bundle_phase_count"] = len(bundle.Phases)
		if phase, ok := currentContinuationBundlePhase(bundle); ok {
			payload["bundle_phase_id"] = strings.TrimSpace(phase.ID)
			payload["bundle_operation_phase_id"] = strings.TrimSpace(phase.OperationPhaseID)
			payload["bundle_phase_index"] = phase.Index
			payload["bundle_phase_authority_class"] = strings.TrimSpace(phase.AuthorityClass)
		}
	}
	if !state.ParkedAt.IsZero() {
		payload["parked_at"] = state.ParkedAt.UTC().Format(time.RFC3339Nano)
	}
	if reason := strings.TrimSpace(state.ParkedReason); reason != "" {
		payload["parked_reason"] = reason
	}
	if source := strings.TrimSpace(state.ParkedSource); source != "" {
		payload["parked_source"] = source
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
	actionProposal = applyContinuationLeaseClassBoundaries(actionProposal)
	actionProposal.PlanHash = actionProposalHash(actionProposal)
	return session.NormalizeActionProposal(actionProposal)
}

func applyContinuationLeaseClassBoundaries(action session.ActionProposal) session.ActionProposal {
	action = session.NormalizeActionProposal(action)
	action = session.ApplyAuthorityContractToActionProposal(action)
	class := session.InferContinuationLeaseClass(action.RiskClass, action.AllowedActions, action.BoundedEffect)
	switch class {
	case session.ContinuationLeaseClassDataAccess:
		action.AllowedActions = append(action.AllowedActions,
			"request_data_access",
			"read_approved_resource",
			"report_data_access_result",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"silent_data_ingestion",
			"read_unapproved_resource",
			"broad_filesystem_scan",
			"persist_data_without_approval",
			"external_account_access_without_grant",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"record resource descriptor, transform, retention, and access result",
			"verify no data was consumed before approval",
		)
	case session.ContinuationLeaseClassChildWake:
		action.AllowedActions = append(action.AllowedActions,
			"request_child_wake",
			"wake_named_child",
			"report_child_wake_result",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"wake_unnamed_child",
			"change_child_policy_without_approval",
			"grant_child_capability_without_capability_authority",
			"unbounded_child_wake_loop",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"record child agent id, wake count, parent message, and final child state",
		)
	case session.ContinuationLeaseClassCapabilityGrant:
		action.AllowedActions = append(action.AllowedActions,
			"prepare_capability_request",
			"review_capability_scope",
			"capability_access_check",
			"report_capability_decision",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"treat_lease_as_capability_grant",
			"grant_without_capability_authority",
			"invoke_without_active_capability_grant",
			"broaden_capability_target_silently",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"show request id, target resource, allowed actions, and active grant/access-check evidence before invocation",
		)
	case session.ContinuationLeaseClassDeployRestart:
		action.AllowedActions = append(action.AllowedActions,
			"prepare_release_handoff",
			"run_explicit_release_step",
			"post_restart_verification",
			"report_release_result",
		)
		action.ForbiddenActions = append(action.ForbiddenActions,
			"deploy_without_handoff",
			"restart_without_recovery_artifact",
			"unbounded_restart_loop",
			"skip_post_deploy_verification",
			"push_or_commit_outside_release_lease",
		)
		action.ValidationPlan = append(action.ValidationPlan,
			"record pre-action git/service state, handoff, post-action status, journal/smoke evidence, and rollback/residual risk",
		)
	}
	return session.NormalizeActionProposal(action)
}

func buildContinuationLease(proposal session.ActionProposal, turns int, now time.Time) session.ContinuationLease {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	proposal = session.ApplyAuthorityContractToActionProposal(proposal)
	if turns <= 0 {
		turns = 1
	}
	leaseClass := session.InferContinuationLeaseClass(proposal.RiskClass, proposal.AllowedActions, proposal.BoundedEffect)
	lease := session.ContinuationLease{
		ID:               "lease-" + strings.TrimPrefix(strings.TrimSpace(proposal.ID), "aprop-"),
		ProposalID:       strings.TrimSpace(proposal.ID),
		MissionID:        strings.TrimSpace(proposal.MissionID),
		Status:           session.ContinuationLeaseStatusPending,
		MaxTurns:         turns,
		RemainingTurns:   turns,
		LeaseClass:       leaseClass,
		Constraints:      session.DefaultContinuationLeaseConstraints(leaseClass),
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
		return session.NormalizeContinuationState(state), fmt.Errorf("continuation proposal expired: %w", core.ErrContinuationExpired)
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
		return session.NormalizeContinuationState(state), fmt.Errorf("continuation lease expired: %w", core.ErrContinuationExpired)
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
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusActive
		state.ApprovalBundle.ApprovedBy = approverID
		state.ApprovalBundle.ApprovedAt = now
		state.ApprovalBundle.UpdatedAt = now
		if state.ApprovalBundle.CurrentPhaseID == "" {
			state.ApprovalBundle.CurrentPhaseID = firstContinuationBundlePhaseID(state.ApprovalBundle.Phases)
		}
		for i := range state.ApprovalBundle.Phases {
			if strings.TrimSpace(state.ApprovalBundle.Phases[i].ID) == strings.TrimSpace(state.ApprovalBundle.CurrentPhaseID) {
				state.ApprovalBundle.Phases[i].Status = session.ContinuationLeaseStatusActive
				break
			}
		}
	}
	if state.ContinuationLease.RemainingTurns <= 0 {
		state.ContinuationLease.RemainingTurns = state.RemainingTurns
	}
	if state.ContinuationLease.MaxTurns <= 0 {
		state.ContinuationLease.MaxTurns = state.ContinuationLease.RemainingTurns
	}
	return session.NormalizeContinuationState(state), nil
}

func continuationStateWithPlanLeaseApprovalConsumed(state session.ContinuationState, now time.Time) session.ContinuationState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	state = session.NormalizeContinuationState(state)
	state.Status = session.ContinuationStatusIdle
	state.RemainingTurns = 0
	state.DecisionID = ""
	state.ActionProposal.Status = session.ProposalStatusApproved
	state.ActionProposal.UpdatedAt = now
	state.ContinuationLease.Status = session.ContinuationLeaseStatusConsumed
	state.ContinuationLease.RemainingTurns = 0
	state.ContinuationLease.ConsumedAt = now
	state.ContinuationLease.UpdatedAt = now
	state.UpdatedAt = now
	return session.NormalizeContinuationState(state)
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
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusRevoked
		state.ApprovalBundle.RevokedAt = now
		state.ApprovalBundle.UpdatedAt = now
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
	if state.ApprovalBundle.Active() {
		state.ApprovalBundle.Status = session.ContinuationLeaseStatusExpired
		state.ApprovalBundle.UpdatedAt = now
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
	state.ApprovalBundle = continuationApprovalBundleAfterTurnConsumed(state.ApprovalBundle, now)
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

func continuationOperatorCardLines(state session.ContinuationState) []string {
	state = session.NormalizeContinuationState(state)
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	class := lease.LeaseClass
	if class == "" {
		class = session.InferContinuationLeaseClass(state.ActionProposal.RiskClass, state.ActionProposal.AllowedActions, state.ActionProposal.BoundedEffect)
	}
	lines := []string{
		"Lease class: " + session.ContinuationLeaseClassLabel(class),
		"Boundary: " + session.ContinuationLeaseClassBoundary(class),
	}
	if adjudication := continuationProposalRiskAdjudication(state); len(adjudication.Findings) > 0 {
		for _, finding := range adjudication.Findings {
			finding = core.NormalizeRuntimeFinding(finding)
			if finding.Kind == "" {
				continue
			}
			lines = append(lines, "Risk note: "+continuationProposalRiskFindingLabel(finding.Kind))
		}
	}
	constraints := lease.Constraints
	if len(constraints) == 0 {
		constraints = session.DefaultContinuationLeaseConstraints(class)
	}
	if len(constraints) > 0 {
		keys := make([]string, 0, len(constraints))
		for key := range constraints {
			key = strings.TrimSpace(key)
			if key != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := strings.TrimSpace(constraints[key])
			if value == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("Constraint: %s=%s", key, value))
		}
	}
	return lines
}

func continuationProposalRiskFindingLabel(kind string) string {
	switch strings.TrimSpace(kind) {
	case "may_delete":
		return "may delete"
	case "may_restart_or_deploy":
		return "may restart/deploy"
	case "may_external_effect":
		return "may affect external systems"
	default:
		return strings.ReplaceAll(strings.TrimSpace(kind), "_", " ")
	}
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
	if card := continuationOperatorCardLines(state); len(card) > 0 {
		lines = append(lines, "", "Operator card:")
		lines = append(lines, card...)
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

func continuationApprovalButtonRows(state session.ContinuationState) [][]telegram.InlineButton {
	state = session.NormalizeContinuationState(state)
	decisionID := continuationCallbackID(state)
	if decisionID == "" {
		return nil
	}
	if continuationButtonStateExpired(state) {
		return [][]telegram.InlineButton{
			{
				{Text: "Refresh lease", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskNextLease)},
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if continuationButtonStateIsPlanLease(state) && state.Status == session.ContinuationStatusApproved {
		return [][]telegram.InlineButton{
			{
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
		return [][]telegram.InlineButton{
			{
				{Text: "Run now", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionResumeEdge)},
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Park", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if state.Status == session.ContinuationStatusPending {
		approveLabel := "Approve & run"
		reviseLabel := "Revise proposal"
		if continuationButtonStateIsPlanLease(state) {
			approveLabel = "Approve plan budget"
			reviseLabel = "Narrow scope"
		} else if label := continuationBundleButtonLabel(state); label != "" {
			approveLabel = "Approve " + label
			reviseLabel = "Revise " + label
		} else if continuationButtonStateIsPhasePlan(state) {
			approveLabel = "Approve phase"
			reviseLabel = "Revise phase"
			if subject := continuationApprovalButtonSubject(state); subject != "" {
				approveLabel = "Approve " + subject
				reviseLabel = "Revise " + subject
			}
		}
		return [][]telegram.InlineButton{
			{
				{Text: approveLabel, CallbackData: encodeContinuationCallbackData(decisionID, continuationActionApproveLease)},
				{Text: "Scope details", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: reviseLabel, CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskEdit)},
				{Text: "Park", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
			},
			{
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	return [][]telegram.InlineButton{
		{
			{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
		},
	}
}

func continuationBundleButtonLabel(state session.ContinuationState) string {
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) < 2 {
		return ""
	}
	first := bundle.Phases[0].Index
	last := bundle.Phases[len(bundle.Phases)-1].Index
	if first <= 0 {
		first = 1
	}
	if last <= 0 {
		last = len(bundle.Phases)
	}
	if first == last {
		return fmt.Sprintf("stage %d", first)
	}
	return fmt.Sprintf("stages %d–%d", first, last)
}

func continuationUserFacingPlanLabel(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	title := continuationUserFacingPlanTitle(state)
	phase := continuationUserFacingPhaseLabel(state)
	if title == "" && phase == "" {
		return ""
	}
	if title == "" {
		title = phase
		phase = ""
	}
	if phase != "" && !continuationTitleContainsPhase(title, phase) {
		title += " (" + phase + ")"
	}
	return "Plan: " + title
}

func continuationUserFacingPlanTitle(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	texts := []string{
		state.StageSummary,
		state.ActionProposal.Summary,
		state.Objective,
		state.ActionProposal.OperationID,
		state.DecisionID,
		state.ContinuationLease.ProposalID,
		state.ContinuationLease.ID,
	}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		texts = append(texts, phase.Summary, phase.OperationPhaseID, phase.ID)
	}
	if title := continuationNamedAgentPlanTitle(strings.Join(texts, "\n")); title != "" {
		return title
	}
	for _, candidate := range []string{state.ActionProposal.Summary, state.Objective, state.StageSummary} {
		if title := cleanContinuationPlanTitleCandidate(candidate); title != "" {
			return title
		}
	}
	if subject := continuationApprovalButtonSubject(state); subject != "" {
		return subject
	}
	return ""
}

func continuationNamedAgentPlanTitle(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" || !strings.Contains(lower, "agent") {
		return ""
	}
	subject := ""
	switch {
	case strings.Contains(lower, "job") || strings.Contains(lower, "career"):
		subject = "Job Agent"
	case strings.Contains(lower, "telegram"):
		subject = "Telegram Agent"
	default:
		return ""
	}
	if name := continuationHumanNameCandidate(text); name != "" {
		return name + "'s " + subject
	}
	return subject
}

func continuationHumanNameCandidate(text string) string {
	replacer := strings.NewReplacer(
		"-", " ",
		"_", " ",
		":", " ",
		"/", " ",
		"\\", " ",
		".", " ",
		",", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
	)
	for _, field := range strings.Fields(replacer.Replace(strings.TrimSpace(text))) {
		name := strings.Trim(field, "'\"`")
		name = strings.TrimSuffix(strings.TrimSuffix(name, "'s"), "’s")
		if continuationLooksLikeHumanName(name) {
			return name
		}
	}
	return ""
}

func continuationLooksLikeHumanName(token string) bool {
	token = strings.TrimSpace(token)
	runes := []rune(token)
	if len(runes) < 2 {
		return false
	}
	for _, r := range runes {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	if !unicode.IsUpper(runes[0]) {
		return false
	}
	allUpper := true
	for _, r := range runes[1:] {
		if unicode.IsLower(r) {
			allUpper = false
			break
		}
	}
	if allUpper {
		return false
	}
	return !continuationHumanNameStopWord(strings.ToLower(token))
}

func continuationHumanNameStopWord(word string) bool {
	switch strings.TrimSpace(word) {
	case "", "approve", "approval", "bounded", "bundle", "child", "consent", "create", "current", "execute", "fresh", "intake", "job", "later", "phase", "plan", "profile", "public", "resume", "run", "stage", "stages", "superseded", "telegram", "the", "this", "use":
		return true
	default:
		return false
	}
}

func cleanContinuationPlanTitleCandidate(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || continuationLooksLikeSystemIdentifier(value) {
		return ""
	}
	if idx := strings.IndexAny(value, "\n\r"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "approve plan budget:") {
		if idx := strings.LastIndex(lower, " for "); idx >= 0 {
			return cleanContinuationPlanTitleCandidate(value[idx+5:])
		}
		return ""
	}
	for _, prefix := range []string{
		"approve stage",
		"approve stages",
		"approve phase",
		"approval needed",
		"continuation approval",
		"revoked continuation",
	} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	value = strings.TrimSpace(strings.TrimRight(value, "."))
	runes := []rune(value)
	if len(runes) > 72 {
		value = strings.TrimSpace(string(runes[:72])) + "..."
	}
	return value
}

func continuationLooksLikeSystemIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	if strings.Contains(lower, "lease-") || strings.Contains(lower, "aprop-") {
		return true
	}
	if len(strings.Fields(value)) == 1 && len(value) > 32 && strings.ContainsAny(value, "-_") {
		return true
	}
	return false
}

func continuationUserFacingPhaseLabel(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	candidates := make([]string, 0, 8)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		candidates = append(candidates, phase.OperationPhaseID, phase.ID, phase.Summary)
	}
	candidates = append(candidates,
		state.ActionProposal.OperationID,
		state.ActionProposal.Summary,
		state.StageSummary,
		state.DecisionID,
		state.ContinuationLease.ProposalID,
		state.ActionProposal.ID,
	)
	for _, candidate := range candidates {
		if token := continuationPhaseTokenFromText(candidate); token != "" {
			return "Phase " + token
		}
	}
	return ""
}

func continuationPhaseTokenFromText(raw string) string {
	fields := continuationSubjectFields(raw)
	for i := 0; i < len(fields); i++ {
		field := strings.ToLower(strings.TrimSpace(fields[i]))
		if field == "phase" && i+1 < len(fields) {
			if token := normalizeContinuationPhaseToken(fields[i+1]); token != "" {
				return token
			}
		}
		if strings.HasPrefix(field, "phase") && len(field) > len("phase") {
			if token := normalizeContinuationPhaseToken(field[len("phase"):]); token != "" {
				return token
			}
		}
	}
	return ""
}

func continuationTitleContainsPhase(title string, phase string) bool {
	title = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(title)), " "))
	phase = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(phase)), " "))
	return phase != "" && strings.Contains(title, phase)
}

func currentContinuationBundlePhase(bundle session.ContinuationApprovalBundle) (session.ContinuationApprovalBundlePhase, bool) {
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if len(bundle.Phases) == 0 {
		return session.ContinuationApprovalBundlePhase{}, false
	}
	currentID := strings.TrimSpace(bundle.CurrentPhaseID)
	if currentID != "" {
		for _, phase := range bundle.Phases {
			if strings.TrimSpace(phase.ID) == currentID {
				return phase, true
			}
		}
	}
	for _, phase := range bundle.Phases {
		if phase.Status == session.ContinuationLeaseStatusActive || phase.Status == session.ContinuationLeaseStatusPending || phase.Status == "" {
			return phase, true
		}
	}
	return bundle.Phases[0], true
}

func continuationApprovalBundleAfterTurnConsumed(bundle session.ContinuationApprovalBundle, now time.Time) session.ContinuationApprovalBundle {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	bundle = session.NormalizeContinuationApprovalBundle(bundle)
	if !bundle.Active() || len(bundle.Phases) == 0 {
		return bundle
	}
	currentID := strings.TrimSpace(bundle.CurrentPhaseID)
	currentIndex := -1
	for i := range bundle.Phases {
		if strings.TrimSpace(bundle.Phases[i].ID) == currentID {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		currentIndex = 0
	}
	bundle.Phases[currentIndex].Status = session.ContinuationLeaseStatusConsumed
	nextIndex := -1
	for i := currentIndex + 1; i < len(bundle.Phases); i++ {
		if bundle.Phases[i].Status == session.ContinuationLeaseStatusPending || bundle.Phases[i].Status == "" {
			nextIndex = i
			break
		}
	}
	if nextIndex >= 0 {
		bundle.Phases[nextIndex].Status = session.ContinuationLeaseStatusActive
		bundle.CurrentPhaseID = strings.TrimSpace(bundle.Phases[nextIndex].ID)
		if bundle.Status != session.ContinuationLeaseStatusRevoked && bundle.Status != session.ContinuationLeaseStatusExpired {
			bundle.Status = session.ContinuationLeaseStatusActive
		}
	} else if bundle.Status != session.ContinuationLeaseStatusRevoked && bundle.Status != session.ContinuationLeaseStatusExpired {
		bundle.Status = session.ContinuationLeaseStatusConsumed
		bundle.ConsumedAt = now
		bundle.CurrentPhaseID = ""
	}
	bundle.UpdatedAt = now
	return session.NormalizeContinuationApprovalBundle(bundle)
}

func continuationButtonStateExpired(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return state.ActionProposal.Status == session.ProposalStatusExpired ||
		state.ContinuationLease.Status == session.ContinuationLeaseStatusExpired
}

func continuationButtonStateIsPlanLease(state session.ContinuationState) bool {
	return continuationActionIsPlanLeaseApproval(state)
}

func continuationActionIsPlanLeaseApproval(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return strings.TrimSpace(state.ActionProposal.RiskClass) == "plan_lease" ||
		actionListContains(state.ActionProposal.AllowedActions, "approve_operation_plan_lease") ||
		actionListContains(state.ContinuationLease.AllowedActions, "approve_operation_plan_lease")
}

func continuationButtonStateIsPhasePlan(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if strings.HasPrefix(strings.TrimSpace(state.ActionProposal.OperationID), "phase-") {
		return true
	}
	return actionListContains(state.ActionProposal.AllowedActions, "update_operation_phase_plan") ||
		actionListContains(state.ContinuationLease.AllowedActions, "update_operation_phase_plan") ||
		actionListContains(state.ActionProposal.AllowedActions, "execute_phase_once") ||
		actionListContains(state.ContinuationLease.AllowedActions, "execute_phase_once")
}

func continuationApprovalButtonSubject(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	candidates := []string{
		state.ActionProposal.Summary,
		state.StageSummary,
		state.ActionProposal.OperationID,
		state.DecisionID,
		state.ContinuationLease.ProposalID,
		state.ActionProposal.ID,
	}
	for _, candidate := range candidates {
		if subject := compactContinuationPhaseSubject(candidate); subject != "" {
			return subject
		}
	}
	return ""
}

func compactContinuationPhaseSubject(raw string) string {
	fields := continuationSubjectFields(raw)
	if len(fields) == 0 {
		return ""
	}
	for i := 0; i < len(fields); i++ {
		field := strings.ToLower(strings.TrimSpace(fields[i]))
		if field == "" {
			continue
		}
		phaseToken := ""
		restStart := i + 1
		if field == "phase" && i+1 < len(fields) {
			phaseToken = normalizeContinuationPhaseToken(fields[i+1])
			restStart = i + 2
		} else if strings.HasPrefix(field, "phase") && len(field) > len("phase") {
			phaseToken = normalizeContinuationPhaseToken(field[len("phase"):])
		}
		if phaseToken == "" {
			continue
		}
		words := make([]string, 0, 3)
		for j := restStart; j < len(fields) && len(words) < 3; j++ {
			word := normalizeContinuationSubjectWord(fields[j])
			if word == "" || continuationSubjectStopWord(strings.ToLower(word)) {
				continue
			}
			words = append(words, word)
		}
		subject := "Phase " + phaseToken
		if len(words) > 0 {
			subject += " " + strings.Join(words, " ")
		}
		return subject
	}
	return ""
}

func continuationSubjectFields(raw string) []string {
	replacer := strings.NewReplacer(
		"-", " ",
		"_", " ",
		":", " ",
		"/", " ",
		"\\", " ",
		".", " ",
		",", " ",
		";", " ",
		"(", " ",
		")", " ",
		"[", " ",
		"]", " ",
	)
	return strings.Fields(replacer.Replace(strings.TrimSpace(raw)))
}

func normalizeContinuationPhaseToken(token string) string {
	var b strings.Builder
	hasDigit := false
	for _, r := range strings.TrimSpace(token) {
		if unicode.IsDigit(r) {
			hasDigit = true
			b.WriteRune(r)
			continue
		}
		if unicode.IsLetter(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	if !hasDigit {
		return ""
	}
	return b.String()
}

func normalizeContinuationSubjectWord(word string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(word) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	out := b.String()
	switch out {
	case "ui":
		return "UI"
	case "ux":
		return "UX"
	case "id":
		return "ID"
	default:
		return out
	}
}

func continuationSubjectStopWord(word string) bool {
	switch strings.ToLower(strings.TrimSpace(word)) {
	case "", "a", "an", "the", "and", "or", "to", "of", "for", "in", "on", "one", "next", "safe", "bounded", "bundle", "bundled", "rebundled", "read", "readonly", "only", "adapter", "local", "child", "idolum", "status", "check", "lane", "remaining", "run":
		return true
	default:
		return false
	}
}

func approvedContinuationEventTextForState(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	lines := []string{approvedContinuationEventText}
	fields := []struct {
		name  string
		value string
	}{
		{name: "proposal_id", value: state.ActionProposal.ID},
		{name: "operation_id", value: state.ActionProposal.OperationID},
		{name: "lease_id", value: state.ContinuationLease.ID},
		{name: "approved_step", value: firstNonEmptyContinuation(state.StageSummary, state.ActionProposal.Summary)},
		{name: "bounded_effect", value: state.ActionProposal.BoundedEffect},
		{name: "risk_class", value: state.ActionProposal.RiskClass},
	}
	appended := false
	for _, field := range fields {
		value := strings.TrimSpace(field.value)
		if value == "" {
			continue
		}
		if !appended {
			lines = append(lines, "", "Approved continuation lease:")
			appended = true
		}
		lines = append(lines, field.name+": "+value)
	}
	if bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle); bundle.Active() {
		if !appended {
			lines = append(lines, "", "Approved continuation lease:")
			appended = true
		}
		lines = append(lines, "bundle_id: "+strings.TrimSpace(bundle.ID))
		lines = append(lines, fmt.Sprintf("bundle_phase_count: %d", len(bundle.Phases)))
		if phase, ok := currentContinuationBundlePhase(bundle); ok {
			lines = append(lines, "bundle_phase_id: "+strings.TrimSpace(phase.ID))
			lines = append(lines, "bundle_operation_phase_id: "+strings.TrimSpace(phase.OperationPhaseID))
			lines = append(lines, fmt.Sprintf("bundle_phase_index: %d", phase.Index))
			if authority := strings.TrimSpace(phase.AuthorityClass); authority != "" {
				lines = append(lines, "bundle_phase_authority_class: "+authority)
			}
			if effect := strings.TrimSpace(phase.BoundedEffect); effect != "" {
				lines = append(lines, "bundle_phase_bounded_effect: "+effect)
			}
		}
		if len(bundle.Phases) > 0 {
			parts := make([]string, 0, len(bundle.Phases))
			for _, phase := range bundle.Phases {
				label := fmt.Sprintf("%d", phase.Index)
				if summary := strings.TrimSpace(phase.Summary); summary != "" {
					label += ":" + summary
				}
				parts = append(parts, label)
			}
			lines = append(lines, "bundle_phases: "+strings.Join(parts, " | "))
		}
	}
	if continuationActionIsPlanLeaseApproval(state) {
		if !appended {
			lines = append(lines, "", "Approved continuation lease:")
			appended = true
		}
		lines = append(lines, "plan_lease_authority: bounded_plan_envelope_not_capability_grant")
		if state.ApprovalBundle.Active() {
			lines = append(lines, "plan_lease_activation: runnable_budget_lane")
		} else {
			lines = append(lines, "plan_lease_activation: approval_record_only")
		}
	}
	return strings.Join(lines, "\n")
}

func newContinuationDecisionID() string {
	return fmt.Sprintf("%x", time.Now().UTC().UnixNano())
}

func encodeContinuationCallbackData(decisionID string, action string) string {
	return core.EncodeContinuationCallbackData(decisionID, action)
}
