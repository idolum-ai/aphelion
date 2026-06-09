//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
)

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
	faceName := r.faceName()
	workspaceRoot := ""
	if r.cfg != nil {
		workspaceRoot = strings.TrimSpace(r.cfg.Agent.PromptRoot)
	}

	rendered, err := renderer.Render(ctx, face.RenderRequest{
		GovernorName:    r.governorName(),
		FaceName:        faceName,
		Channel:         "telegram",
		Mode:            "repair",
		PrincipalRole:   "approved_user",
		WorkspaceRoot:   workspaceRoot,
		FloorText:       fallback,
		LatestUserInput: strings.TrimSpace(msg.Text),
		CandidateReply:  fallback,
		RepairNotes: []string{
			continuationFaceRepairIdentityNote(faceName),
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

func continuationFaceRepairIdentityNote(faceName string) string {
	faceName = strings.TrimSpace(faceName)
	if faceName == "" {
		faceName = face.DefaultFaceName
	}
	return fmt.Sprintf("Keep this in first person as %s.", faceName)
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
	events, err := r.store.LatestExecutionEventsBySession(key, 300)
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
		"Scope: " + session.ContinuationLeaseClassLabel(class),
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
	state = session.NormalizeContinuationState(state)
	lines := []string{"I can continue from here."}
	reasons := make([]string, 0, 2)
	if reason := strings.TrimSpace(state.PersonaIntent.Rationale); reason != "" {
		reasons = appendUniqueContinuationLine(reasons, reason)
	}
	if reason := strings.TrimSpace(state.GovernorIntent.Rationale); reason != "" {
		reasons = appendUniqueContinuationLine(reasons, reason)
	}
	if len(reasons) > 0 {
		lines = append(lines, "", "Why this needs approval:", strings.Join(reasons, " "))
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	constraints := strings.TrimSpace(state.GovernorIntent.Constraints)
	effect := ""
	if proposal.Active() {
		effect = strings.TrimSpace(proposal.BoundedEffect)
	}
	scope := firstNonEmptyContinuation(constraints, effect)
	if scope != "" {
		lines = append(lines, "", "Scope:", scope)
	}
	if effect != "" && constraints != "" && !continuationTextEqual(effect, constraints) {
		lines = append(lines, "", "Exact step:", effect)
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

func appendUniqueContinuationLine(lines []string, line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return lines
	}
	for _, existing := range lines {
		if continuationTextEqual(existing, line) {
			return lines
		}
	}
	return append(lines, line)
}

func continuationTextEqual(left string, right string) bool {
	left = strings.Join(strings.Fields(strings.TrimSpace(left)), " ")
	right = strings.Join(strings.Fields(strings.TrimSpace(right)), " ")
	return strings.EqualFold(left, right)
}
