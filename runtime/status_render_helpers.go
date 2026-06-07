//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/decisionprojection"
	"github.com/idolum-ai/aphelion/session"
)

func renderDecisionSummaryFromFields(kind string, prompt string, details string) string {
	return decisionprojection.DecisionSummary(kind, prompt, details)
}

func continuationSnapshotIsPending(state core.ContinuationStatusSnapshot) bool {
	status := strings.ToLower(strings.TrimSpace(state.Status))
	return status == "pending" || status == "approved"
}

func continuationSnapshotItemID(state core.ContinuationStatusSnapshot, chatID int64) string {
	if decisionID := strings.TrimSpace(state.DecisionID); decisionID != "" {
		return decisionID
	}
	return "continuation:" + strconv.FormatInt(chatID, 10)
}

func renderContinuationSnapshotSummary(state core.ContinuationStatusSnapshot) string {
	parts := []string{
		fmt.Sprintf("status=%s", strings.TrimSpace(state.Status)),
		fmt.Sprintf("remaining_turns=%d", state.RemainingTurns),
	}
	if decisionID := strings.TrimSpace(state.DecisionID); decisionID != "" {
		parts = append(parts, "decision_id="+decisionID)
	}
	if state.ApprovedBy != 0 {
		parts = append(parts, fmt.Sprintf("approved_by=%d", state.ApprovedBy))
	}
	if reason := strings.TrimSpace(state.BlockedReason); reason != "" {
		parts = append(parts, "blocked_reason="+reason)
	}
	return strings.Join(parts, " ")
}

func statusAdjudicationDiagnosticLine(adjudication core.AdjudicationStatusSnapshot) string {
	label := firstNonEmpty(strings.TrimSpace(adjudication.OperatorLabel), "Runtime adjudication")
	action := strings.TrimSpace(adjudication.VisibleAction)
	detail := ""
	if len(adjudication.Findings) > 0 {
		finding := core.NormalizeRuntimeFinding(adjudication.Findings[0])
		detail = firstNonEmpty(finding.Detail, finding.RequiredBehavior, finding.Kind)
	}
	parts := []string{"Runtime adjudication: " + label}
	if action != "" {
		parts = append(parts, "action="+action)
	}
	if subject := strings.TrimSpace(adjudication.SubjectID); subject != "" && subject != "latest_turn" {
		parts = append(parts, "subject="+subject)
	}
	if len(adjudication.EvidenceRefs) > 0 {
		refs := make([]string, 0, minStatusInt(3, len(adjudication.EvidenceRefs)))
		for _, ref := range adjudication.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			refs = append(refs, ref)
			if len(refs) == 3 {
				break
			}
		}
		if len(refs) > 0 {
			parts = append(parts, "refs="+strings.Join(refs, ","))
		}
	}
	if detail != "" {
		parts = append(parts, "detail="+strconv.Quote(truncateStatusDiagnostic(detail, 180)))
	}
	if next := statusAdjudicationNextAction(adjudication); next != "" {
		parts = append(parts, "next="+strconv.Quote(next))
	}
	return strings.Join(parts, " ") + "."
}

func statusAdjudicationNextAction(adjudication core.AdjudicationStatusSnapshot) string {
	switch strings.TrimSpace(adjudication.VisibleAction) {
	case "repair_completed_or_superseded_approval":
		return "Ask for a new bounded follow-up if more work remains."
	case "repair_invalid_pending_approval", "repair_stale_continuation_projection":
		return "Use the fresh eligible proposal; do not press stale approval buttons."
	case "blocked_status":
		return "Resolve the named blocker, then request a fresh bounded approval."
	default:
		return ""
	}
}

func summarizeExecutionEventPayload(eventType string, eventStatus string, payload map[string]any) string {
	switch strings.TrimSpace(eventType) {
	case core.ExecutionEventFaceRenderSkipped:
		parts := make([]string, 0, 4)
		if reason := strings.TrimSpace(payloadString(payload, "reason")); reason != "" {
			parts = append(parts, "reason="+reason)
		}
		if fallbackChars, ok := payloadInt64(payload, "fallback_chars"); ok {
			parts = append(parts, "fallback_chars="+strconv.FormatInt(fallbackChars, 10))
		}
		if mediaCount, ok := payloadInt64(payload, "media_count"); ok {
			parts = append(parts, "media="+strconv.FormatInt(mediaCount, 10))
		}
		if len(parts) == 0 {
			return strings.TrimSpace(eventStatus)
		}
		return strings.Join(parts, " ")
	case core.ExecutionEventReplyClaimAdjudicated:
		label := firstNonEmpty(payloadString(payload, "operator_label"), executionClaimOperatorLabel(payloadString(payload, "visible_action")))
		action := strings.TrimSpace(payloadString(payload, "visible_action"))
		parts := make([]string, 0, 3)
		if label != "" {
			parts = append(parts, "label="+label)
		}
		if action != "" {
			parts = append(parts, "action="+action)
		}
		claimTypes := payloadStringSlice(payload, "claim_types")
		if len(claimTypes) == 0 {
			for _, finding := range payloadRuntimeFindings(payload, "findings") {
				finding = core.NormalizeRuntimeFinding(finding)
				if finding.Kind != "" {
					claimTypes = append(claimTypes, finding.Kind)
				}
			}
		}
		if len(claimTypes) > 0 {
			parts = append(parts, "findings="+strings.Join(claimTypes, ","))
		}
		return strings.Join(parts, " ")
	case core.ExecutionEventContinuationBundleNarrowed:
		return continuationBundleNarrowingSummary(payload)
	case core.ExecutionEventContinuationCompileRepaired, core.ExecutionEventContinuationCompileRepairExhausted, core.ExecutionEventContinuationCompileUnknownReason:
		return continuationCompileRepairEventSummary(eventType, payload)
	case core.ExecutionEventToolRegistered:
		registered := strings.TrimSpace(eventStatus) == "enabled"
		if value, ok := payloadBool(payload, "registered"); ok {
			registered = value
		}
		parts := make([]string, 0, 5)
		if toolName := strings.TrimSpace(payloadString(payload, "tool_name")); toolName != "" {
			parts = append(parts, "tool_name="+toolName)
		}
		parts = append(parts, "registered="+strconv.FormatBool(registered))
		if ref := strings.TrimSpace(payloadString(payload, "implementation_ref")); ref != "" {
			parts = append(parts, "implementation_ref="+ref)
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	case core.ExecutionEventToolInstallUpdated:
		parts := make([]string, 0, 5)
		if toolName := strings.TrimSpace(payloadString(payload, "tool_name")); toolName != "" {
			parts = append(parts, "tool_name="+toolName)
		}
		if status := firstNonEmpty(strings.TrimSpace(payloadString(payload, "status")), strings.TrimSpace(eventStatus)); status != "" {
			parts = append(parts, "status="+status)
		}
		if probeStatus := strings.TrimSpace(payloadString(payload, "probe_status")); probeStatus != "" {
			parts = append(parts, "probe_status="+probeStatus)
		}
		if installRef := strings.TrimSpace(payloadString(payload, "install_ref")); installRef != "" {
			parts = append(parts, "install_ref="+installRef)
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	case core.ExecutionEventToolAuditUpdated:
		parts := make([]string, 0, 3)
		if toolName := strings.TrimSpace(payloadString(payload, "tool_name")); toolName != "" {
			parts = append(parts, "tool_name="+toolName)
		}
		if status := firstNonEmpty(strings.TrimSpace(payloadString(payload, "status")), strings.TrimSpace(eventStatus)); status != "" {
			parts = append(parts, "status="+status)
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	case core.ExecutionEventProgressSurface:
		parts := make([]string, 0, 4)
		if source := strings.TrimSpace(payloadString(payload, "progress_source")); source != "" {
			parts = append(parts, "source="+source)
		}
		if label := strings.TrimSpace(payloadString(payload, "progress_label")); label != "" {
			parts = append(parts, "label="+truncateStatusDiagnostic(label, 80))
		}
		if toolName := strings.TrimSpace(payloadString(payload, "tool")); toolName != "" {
			parts = append(parts, "tool="+toolName)
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	if len(payload) == 0 {
		return ""
	}
	for _, key := range []string{"summary", "error", "reason", "prompt", "request_text", "decision_id"} {
		if value := payloadString(payload, key); value != "" {
			return truncateStatusDiagnostic(value, 160)
		}
	}
	return ""
}

func continuationCompileRepairEventSummary(eventType string, payload map[string]any) string {
	parts := make([]string, 0, 5)
	if kind := strings.TrimSpace(payloadString(payload, "repair_kind")); kind != "" {
		parts = append(parts, "kind="+kind)
	}
	if reason := firstNonEmpty(payloadString(payload, "normalized_reason"), payloadString(payload, "reason")); strings.TrimSpace(reason) != "" {
		parts = append(parts, "reason="+strings.TrimSpace(reason))
	}
	if phaseID := strings.TrimSpace(payloadString(payload, "repair_phase_id")); phaseID != "" {
		parts = append(parts, "repair_phase="+phaseID)
	} else if phaseID := strings.TrimSpace(payloadString(payload, "phase_id")); phaseID != "" {
		parts = append(parts, "phase="+phaseID)
	}
	if source := strings.TrimSpace(payloadString(payload, "materialization_source")); source != "" {
		parts = append(parts, "source="+source)
	}
	switch strings.TrimSpace(eventType) {
	case core.ExecutionEventContinuationCompileRepaired:
		parts = append([]string{"status=repaired"}, parts...)
	case core.ExecutionEventContinuationCompileRepairExhausted:
		parts = append([]string{"status=exhausted"}, parts...)
	case core.ExecutionEventContinuationCompileUnknownReason:
		parts = append([]string{"status=unknown_reason"}, parts...)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func minStatusInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func missionOwnerChatID(owner string) int64 {
	owner = strings.TrimSpace(owner)
	if !strings.HasPrefix(owner, telegramMissionOwnerPrefix) {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(owner, telegramMissionOwnerPrefix)), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func renderMissionPendingSummary(mission session.MissionState) string {
	parts := []string{
		"status=" + strings.TrimSpace(string(mission.Status)),
	}
	if title := strings.TrimSpace(mission.Title); title != "" {
		parts = append(parts, "title="+truncateStatusDiagnostic(title, 80))
	}
	if action := strings.TrimSpace(mission.NextAllowedAction); action != "" {
		parts = append(parts, "next="+truncateStatusDiagnostic(action, 100))
	}
	if mission.Authority.RequiresUserReview {
		parts = append(parts, "requires_user_review=true")
	}
	return strings.Join(parts, " ")
}

func renderDecisionSummary(record session.PendingDecisionRecord) string {
	return decisionprojection.DecisionSummary(record.Kind, record.Prompt, record.Details)
}

func renderPendingReviewSummary(event session.ReviewEvent) string {
	parts := []string{
		"status=pending",
		fmt.Sprintf("target_chat=%d", event.TargetAdminChatID),
	}
	if source := strings.TrimSpace(event.SourceScope.DurableAgentID); source != "" {
		parts = append(parts, "source_agent="+source)
	}
	if summary := strings.TrimSpace(event.Summary); summary != "" {
		parts = append(parts, "summary="+truncateStatusDiagnostic(summary, 80))
	}
	return strings.Join(parts, " ")
}

func continuationItemID(state session.ContinuationState, chatID int64) string {
	if decisionID := strings.TrimSpace(state.DecisionID); decisionID != "" {
		return decisionID
	}
	return "continuation:" + strconv.FormatInt(chatID, 10)
}

func renderContinuationSummary(state session.ContinuationState) string {
	state = session.NormalizeContinuationState(state)
	parts := []string{
		fmt.Sprintf("status=%s", strings.TrimSpace(string(state.Status))),
		fmt.Sprintf("remaining_turns=%d", state.RemainingTurns),
	}
	if decision := strings.TrimSpace(string(state.PersonaIntent.Decision)); decision != "" {
		parts = append(parts, "persona_intent="+decision)
	}
	if decision := strings.TrimSpace(string(state.GovernorIntent.Decision)); decision != "" {
		parts = append(parts, "governor_intent="+decision)
	}
	if state.GovernorIntent.Ratified {
		parts = append(parts, "governor_ratified=true")
	}
	if reason := strings.TrimSpace(state.HandshakeBlockedReason); reason != "" {
		parts = append(parts, "blocked_reason="+reason)
	}
	return strings.Join(parts, " ")
}

func statusAge(now time.Time, preferred time.Time, fallback time.Time) time.Duration {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ts := preferred
	if ts.IsZero() {
		ts = fallback
	}
	if ts.IsZero() {
		return 0
	}
	age := now.Sub(ts)
	if age < 0 {
		return 0
	}
	return age
}

func coalesceTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func firstNonEmptyStatus(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func operationStatusFields(state session.OperationState) (status string, stage string, summary string) {
	normalized := session.NormalizeOperationState(state)
	status = strings.TrimSpace(string(normalized.Status))
	stage = strings.TrimSpace(normalized.Stage)
	summary = strings.TrimSpace(firstNonEmptyStatus(normalized.Summary, normalized.Objective))
	summary = truncateStatusDiagnostic(summary, 160)
	return status, stage, summary
}

func operationPhasePlanStatusFields(state session.OperationState) (currentID string, currentStatus string, currentSummary string, completed int, total int, active bool) {
	normalized := session.NormalizeOperationState(state)
	phases := normalized.PhasePlan.Phases
	total = len(phases)
	if total == 0 {
		return "", "", "", 0, 0, false
	}
	active = true
	for _, phase := range phases {
		if phase.Status == session.PlanStatusCompleted {
			completed++
		}
	}
	currentID = strings.TrimSpace(normalized.PhasePlan.CurrentPhaseID)
	var current session.OperationPhase
	for _, phase := range phases {
		if currentID != "" && strings.TrimSpace(phase.ID) == currentID {
			current = phase
			break
		}
	}
	if strings.TrimSpace(current.ID) == "" && strings.TrimSpace(current.Summary) == "" {
		for _, phase := range phases {
			if phase.Status == session.PlanStatusInProgress || phase.Status == session.PlanStatusPending {
				current = phase
				break
			}
		}
	}
	currentID = strings.TrimSpace(current.ID)
	currentStatus = strings.TrimSpace(string(current.Status))
	currentSummary = truncateStatusDiagnostic(strings.TrimSpace(current.Summary), 160)
	return currentID, currentStatus, currentSummary, completed, total, active
}

func planStatusFields(state session.PlanState) (status string, step string) {
	normalized := session.NormalizePlanState(state)
	if len(normalized.Steps) == 0 {
		explanation := strings.TrimSpace(normalized.Explanation)
		if explanation != "" {
			return "", truncateStatusDiagnostic(explanation, 160)
		}
		return "", ""
	}

	picked := normalized.Steps[0]
	for _, candidate := range normalized.Steps {
		if candidate.Status == session.PlanStatusInProgress {
			picked = candidate
			break
		}
		if candidate.Status == session.PlanStatusPending && picked.Status == session.PlanStatusCompleted {
			picked = candidate
		}
	}
	return strings.TrimSpace(string(picked.Status)), truncateStatusDiagnostic(strings.TrimSpace(picked.Step), 160)
}

func planProgressFields(state session.PlanState) (completed int, total int, fullyExecuted bool) {
	normalized := session.NormalizePlanState(state)
	total = len(normalized.Steps)
	if total == 0 {
		return 0, 0, false
	}
	for _, step := range normalized.Steps {
		if session.NormalizePlanStatus(step.Status) == session.PlanStatusCompleted {
			completed++
		}
	}
	return completed, total, completed == total
}

func hiddenInputStatusFields(raw string) ([]string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	var metadata core.FloorMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return nil, ""
	}

	seen := map[string]struct{}{}
	categories := make([]string, 0, len(metadata.HiddenInputs))
	for _, input := range metadata.HiddenInputs {
		category := strings.TrimSpace(input.Category)
		if category == "" {
			continue
		}
		if _, ok := seen[category]; ok {
			continue
		}
		seen[category] = struct{}{}
		categories = append(categories, category)
	}
	sort.Strings(categories)

	summary := strings.TrimSpace(metadata.ProvenanceSummary)
	if summary == "" {
		parts := make([]string, 0, 2)
		for _, input := range metadata.HiddenInputs {
			if detail := strings.TrimSpace(input.Summary); detail != "" {
				parts = append(parts, detail)
			}
			if len(parts) == 2 {
				break
			}
		}
		summary = strings.Join(parts, "; ")
	}
	return categories, truncateStatusDiagnostic(summary, 160)
}

func deliveryStatusFields(latest *core.TurnRunStatusSnapshot, outboundCountAtTurn int) (status string, summary string) {
	if latest == nil {
		return "", ""
	}
	runStatus := strings.ToLower(strings.TrimSpace(latest.Status))
	switch runStatus {
	case "running":
		return "in_flight", "turn is still executing"
	case "completed":
		if outboundCountAtTurn > 0 {
			return "delivered", "latest persisted turn has a recorded outbound delivery"
		}
		return "persisted_not_delivered", "latest turn persisted but no outbound delivery is recorded"
	case "failed":
		errText := strings.ToLower(strings.TrimSpace(latest.ErrorText))
		if strings.Contains(errText, "send outbound reply") || strings.Contains(errText, "send durable group reply") {
			if outboundCountAtTurn > 0 {
				return "delivery_error_recovered", "delivery reported an error, but outbound delivery is recorded"
			}
			return "delivery_failed", "persisted turn failed during delivery; no retry queue is active"
		}
		if outboundCountAtTurn > 0 {
			return "failed_after_delivery", "turn failed after outbound delivery was recorded"
		}
		return "failed_before_delivery", "turn failed before outbound delivery was recorded"
	case "interrupted":
		if outboundCountAtTurn > 0 {
			return "interrupted_after_delivery", "turn was interrupted after outbound delivery was recorded"
		}
		return "interrupted_before_delivery", "turn was interrupted before outbound delivery was recorded"
	default:
		if outboundCountAtTurn > 0 {
			return "delivered", "outbound delivery is recorded for the latest turn"
		}
		return "", ""
	}
}

func truncateStatusDiagnostic(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
