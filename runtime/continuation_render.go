//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

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
	governorName := prompt.DefaultGovernorName
	if r != nil {
		governorName = r.governorName()
	}
	fallback := renderContinuationBlockedFallback(state, governorName)
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
		GovernorName:    governorName,
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
	governorName := prompt.DefaultGovernorName
	if r != nil {
		governorName = r.governorName()
	}
	fallback := renderContinuationBlockedFallback(state, governorName)
	if candidate == "" {
		return fallback, "rendered continuation blocked notice is empty"
	}
	if r == nil || r.store == nil {
		return candidate, ""
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

func renderContinuationBlockedFallback(state session.ContinuationState, governorName string) string {
	reason := strings.TrimSpace(state.HandshakeBlockedReason)
	governorName = continuationBlockedFallbackGovernorName(governorName)
	switch reason {
	case "persona_intent_missing":
		return "I can't continue yet because I did not publish a continuation intent for this turn."
	case "persona_rationale_missing":
		return "I can't continue yet because I did not provide a clear continuation rationale."
	case "persona_not_willing":
		return "I can't continue yet because I chose to hold this thread instead of auto-continuing."
	case "governor_intent_missing":
		return fmt.Sprintf("I can't continue yet because %s did not publish a continuation intent for this turn.", governorName)
	case "governor_rationale_missing":
		return fmt.Sprintf("I can't continue yet because %s did not provide a continuation rationale.", governorName)
	case "governor_not_ratified":
		return fmt.Sprintf("I can't continue yet because %s did not ratify continuation for this turn.", governorName)
	case "governor_not_willing":
		return fmt.Sprintf("I can't continue yet because %s explicitly held continuation for this turn.", governorName)
	default:
		return "I can't continue this thread yet because the continuation handshake is still blocked."
	}
}

func continuationBlockedFallbackGovernorName(governorName string) string {
	if trimmed := strings.TrimSpace(governorName); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(prompt.DefaultGovernorName); trimmed != "" {
		return trimmed
	}
	return "System"
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
		lines = append(lines, "", "Why continuing makes sense:", strings.Join(reasons, " "))
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
		lines = append(lines, "", "Bounded effect:", effect)
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
				{Text: "Refresh", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskNextLease)},
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
				{Text: "Run", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionResumeEdge)},
				{Text: "Status", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Pause", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
				{Text: "Stop", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStop)},
			},
		}
	}
	if state.Status == session.ContinuationStatusPending {
		return [][]telegram.InlineButton{
			{
				{Text: "Start", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionApproveLease)},
				{Text: "Details", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStatusOnly)},
			},
			{
				{Text: "Change", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionAskEdit)},
				{Text: "Pause", CallbackData: encodeContinuationCallbackData(decisionID, continuationActionStopPark)},
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

func ContinuationApprovalButtonRows(state session.ContinuationState) [][]telegram.InlineButton {
	return continuationApprovalButtonRows(state)
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
	if title := firstNonEmptyContinuation(
		state.ActionProposal.OperatorTitle,
		state.ActionProposal.PlanTitle,
		state.ContinuationLease.OperatorTitle,
		state.ContinuationLease.PlanTitle,
	); title != "" {
		return continuationPlanTitleFromText(title)
	}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if title := firstNonEmptyContinuation(phase.OperatorTitle, phase.PlanTitle); title != "" {
			return continuationPlanTitleFromText(title)
		}
	}
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

func continuationPlanTitleFromText(text string) string {
	return cleanContinuationPlanTitleCandidate(text)
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
	lines := []string{approvedContinuationEventText, "", "Approved work:"}
	if label := continuationUserFacingPlanLabel(state); label != "" {
		lines = append(lines, label)
	}
	if next := continuationApprovedNextStepLine(state); next != "" {
		lines = append(lines, "Next: "+next)
	}
	if scope := continuationApprovedScopeLine(state); scope != "" {
		lines = append(lines, "Scope: "+scope)
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Budget: up to %d %s.", state.RemainingTurns, continuationTurnWord(state.RemainingTurns)))
	}
	if stops := continuationApprovalPromptStops(state); len(stops) > 0 {
		lines = append(lines, "Stops before: "+strings.Join(stops, ", ")+".")
	}
	if continuationActionIsPlanLeaseApproval(state) {
		if state.ApprovalBundle.Active() {
			lines = append(lines, "This approval covers the named plan budget only.")
		} else {
			lines = append(lines, "This records the plan budget approval; execution still stops at hard gates.")
		}
	}
	return strings.Join(lines, "\n")
}

func continuationApprovedNextStepLine(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	candidates := []string{state.StageSummary, state.ActionProposal.Summary}
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		candidates = append([]string{phase.Summary}, candidates...)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || continuationLooksLikeSystemIdentifier(candidate) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(candidate), "approve ") {
			if idx := strings.Index(candidate, ":"); idx >= 0 && idx+1 < len(candidate) {
				candidate = strings.TrimSpace(candidate[idx+1:])
			}
		}
		if line := continuationPromptCompactLine(candidate, 180); line != "" {
			return line
		}
	}
	return ""
}

func continuationApprovedScopeLine(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	if phase, ok := currentContinuationBundlePhase(state.ApprovalBundle); ok {
		if scope := continuationPromptCompactLine(phase.BoundedEffect, 220); scope != "" {
			return scope
		}
	}
	for _, candidate := range []string{state.ActionProposal.BoundedEffect, state.GovernorIntent.Constraints} {
		if scope := continuationPromptCompactLine(candidate, 240); scope != "" {
			return scope
		}
	}
	return ""
}

func encodeContinuationCallbackData(decisionID string, action string) string {
	return core.EncodeContinuationCallbackData(decisionID, action)
}
