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
	"github.com/idolum-ai/aphelion/session"
)

func staleRunningTurnSnapshotsFromExecutionEvents(latest map[int64]core.TurnRunStatusSnapshot, now time.Time, threshold time.Duration) []core.TurnRunStatusSnapshot {
	if threshold <= 0 || now.IsZero() || len(latest) == 0 {
		return nil
	}
	out := make([]core.TurnRunStatusSnapshot, 0, len(latest))
	for _, run := range latest {
		if run.ChatID == 0 || !strings.EqualFold(strings.TrimSpace(run.Status), string(session.TurnRunStatusRunning)) {
			continue
		}
		if run.LastActivityAt.IsZero() || !now.After(run.LastActivityAt.Add(threshold)) {
			continue
		}
		if strings.TrimSpace(run.Source) == "" {
			run.Source = "canonical:execution_events.turn"
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ChatID == out[j].ChatID {
			return out[i].ID < out[j].ID
		}
		return out[i].ChatID < out[j].ChatID
	})
	return out
}

func staleTurnSnapshotCovered(existing []core.TurnRunStatusSnapshot, candidate core.TurnRunStatusSnapshot) bool {
	for _, row := range existing {
		if candidate.ID > 0 && row.ID == candidate.ID {
			return true
		}
		if candidate.ChatID != 0 && row.ChatID == candidate.ChatID {
			return true
		}
	}
	return false
}

func tesStaleTurnItemID(run core.TurnRunStatusSnapshot) string {
	if run.ID > 0 {
		return fmt.Sprintf("stale:tes:%d", run.ID)
	}
	return fmt.Sprintf("stale:tes:chat:%d", run.ChatID)
}

func liveRouterSignalsFromExecutionEvents(events []session.ExecutionEvent) (map[int64][]uint64, map[int64]int) {
	activeByChat := make(map[int64][]uint64, 16)
	queueByChat := make(map[int64]int, 16)
	if len(events) == 0 {
		return activeByChat, queueByChat
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })
	for _, event := range ordered {
		chatID := event.ChatID
		if chatID == 0 {
			continue
		}
		payload := executionEventPayload(event.PayloadJSON)
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventIngressAccepted,
			core.ExecutionEventIngressQueued,
			core.ExecutionEventIngressCompacted,
			core.ExecutionEventIngressSelected:
			if depth, ok := payloadInt64(payload, "queue_depth"); ok {
				if depth > 0 {
					queueByChat[chatID] = int(depth)
				} else {
					delete(queueByChat, chatID)
				}
			}
		case core.ExecutionEventTurnStarted:
			runID, _ := payloadInt64(payload, "run_id")
			if runID <= 0 {
				runID = event.Seq
			}
			activeByChat[chatID] = []uint64{uint64(runID)}
		case core.ExecutionEventTurnCompleted, core.ExecutionEventTurnFailed, core.ExecutionEventTurnInterrupted:
			delete(activeByChat, chatID)
		}
	}
	return activeByChat, queueByChat
}

type statusSidecarProjection struct {
	OperationStatus       string
	OperationStage        string
	OperationSummary      string
	PlanStepStatus        string
	PlanStep              string
	PlanCompletedSteps    int
	PlanTotalSteps        int
	PlanFullyExecuted     bool
	HiddenInputCategories []string
	HiddenInputSummary    string
}

func latestStatusSidecarsFromExecutionEvents(events []session.ExecutionEvent) (statusSidecarProjection, bool) {
	if len(events) == 0 {
		return statusSidecarProjection{}, false
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })

	projection := statusSidecarProjection{}
	found := false
	for _, event := range ordered {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventTurnSidecarsCaptured {
			continue
		}
		payload := executionEventPayload(event.PayloadJSON)
		projection.OperationStatus = strings.TrimSpace(payloadString(payload, "operation_status"))
		projection.OperationStage = strings.TrimSpace(payloadString(payload, "operation_stage"))
		projection.OperationSummary = strings.TrimSpace(payloadString(payload, "operation_summary"))
		projection.PlanStepStatus = strings.TrimSpace(payloadString(payload, "plan_step_status"))
		projection.PlanStep = strings.TrimSpace(payloadString(payload, "plan_step"))
		if value, ok := payloadInt64(payload, "plan_completed_steps"); ok {
			projection.PlanCompletedSteps = int(value)
		}
		if value, ok := payloadInt64(payload, "plan_total_steps"); ok {
			projection.PlanTotalSteps = int(value)
		}
		if value, ok := payloadBool(payload, "plan_fully_executed"); ok {
			projection.PlanFullyExecuted = value
		}
		projection.HiddenInputCategories = payloadStringSlice(payload, "hidden_input_categories")
		if len(projection.HiddenInputCategories) == 0 {
			projection.HiddenInputCategories = splitStatusCSV(payloadString(payload, "hidden_input_category"))
		}
		projection.HiddenInputSummary = strings.TrimSpace(payloadString(payload, "hidden_input_summary"))
		found = true
	}
	return projection, found
}

func deliveryStatusFromExecutionEvents(events []session.ExecutionEvent) (string, string, bool) {
	if len(events) == 0 {
		return "", "", false
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })

	status := ""
	summary := ""
	found := false
	for _, event := range ordered {
		payload := executionEventPayload(event.PayloadJSON)
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventDeliveryFinalSent:
			messageID, _ := payloadInt64(payload, "message_id")
			kind := strings.TrimSpace(payloadString(payload, "kind"))
			status = "delivered"
			if messageID != 0 || kind != "" {
				summary = fmt.Sprintf("outbound message_id=%d kind=%s", messageID, firstNonEmpty(kind, "telegram"))
			} else {
				summary = "outbound delivery recorded in TES"
			}
			found = true
		case core.ExecutionEventDeliveryFinalFailed:
			status = "delivery_failed"
			if errText := strings.TrimSpace(payloadString(payload, "error")); errText != "" {
				summary = truncateStatusDiagnostic(errText, 220)
			} else {
				summary = "delivery finalization failed"
			}
			found = true
		case core.ExecutionEventDeliveryProgressSent, core.ExecutionEventDeliveryProgressEdited:
			if !found {
				status = "in_flight"
				summary = "progress delivery is active"
			}
		case core.ExecutionEventDeliveryProgressFailed:
			if !found {
				status = "delivery_failed"
				if errText := strings.TrimSpace(payloadString(payload, "error")); errText != "" {
					summary = truncateStatusDiagnostic(errText, 220)
				} else {
					summary = "progress delivery failed"
				}
				found = true
			}
		}
	}
	if strings.TrimSpace(status) == "" {
		return "", "", false
	}
	return status, summary, true
}

type decisionEventProjection struct {
	DecisionID    string
	ChatID        int64
	Kind          string
	Prompt        string
	Details       string
	Summary       string
	LastEventType string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (p decisionEventProjection) pending() bool {
	return strings.TrimSpace(p.DecisionID) != "" && strings.TrimSpace(p.LastEventType) == core.ExecutionEventDecisionOpened
}

func (r *Runtime) decisionEventStates(since time.Time, limit int) (map[string]decisionEventProjection, error) {
	if r == nil || r.store == nil {
		return map[string]decisionEventProjection{}, nil
	}
	events, err := r.store.ExecutionEventsByTypes([]string{
		core.ExecutionEventDecisionOpened,
		core.ExecutionEventDecisionResolved,
		core.ExecutionEventDecisionExpired,
		core.ExecutionEventDecisionDetached,
	}, since, limit)
	if err != nil {
		return nil, err
	}
	sort.Slice(events, func(i, j int) bool { return executionEventBefore(events[i], events[j]) })

	out := make(map[string]decisionEventProjection, len(events))
	for _, event := range events {
		payload := executionEventPayload(event.PayloadJSON)
		decisionID := payloadString(payload, "decision_id")
		if decisionID == "" {
			continue
		}

		state := out[decisionID]
		state.DecisionID = decisionID
		if state.ChatID == 0 {
			state.ChatID = event.ChatID
		}
		if state.Kind == "" {
			state.Kind = payloadString(payload, "decision_kind")
		}
		if state.Prompt == "" {
			state.Prompt = payloadString(payload, "prompt")
		}
		if state.Details == "" {
			state.Details = payloadString(payload, "details")
		}
		if state.Summary == "" {
			state.Summary = payloadString(payload, "summary")
		}
		if state.CreatedAt.IsZero() {
			state.CreatedAt = event.CreatedAt
		}
		if strings.TrimSpace(event.EventType) == core.ExecutionEventDecisionOpened {
			state.CreatedAt = event.CreatedAt
		}
		state.LastEventType = strings.TrimSpace(event.EventType)
		state.UpdatedAt = event.CreatedAt
		out[decisionID] = state
	}
	return out, nil
}

func (r *Runtime) continuationEventStates(since time.Time, limit int) (map[int64]core.ContinuationStatusSnapshot, error) {
	if r == nil || r.store == nil {
		return map[int64]core.ContinuationStatusSnapshot{}, nil
	}
	events, err := r.store.ExecutionEventsByTypes([]string{
		core.ExecutionEventContinuationOffered,
		core.ExecutionEventContinuationApproved,
		core.ExecutionEventContinuationRevoked,
		core.ExecutionEventContinuationConsumed,
		core.ExecutionEventContinuationBlocked,
	}, since, limit)
	if err != nil {
		return nil, err
	}
	sort.Slice(events, func(i, j int) bool { return executionEventBefore(events[i], events[j]) })

	out := make(map[int64]core.ContinuationStatusSnapshot, len(events))
	for _, event := range events {
		chatID := event.ChatID
		if chatID == 0 {
			continue
		}
		state := out[chatID]
		state.ChatID = chatID
		if strings.TrimSpace(state.Source) == "" {
			state.Source = "canonical:execution_events.continuation"
		}

		payload := executionEventPayload(event.PayloadJSON)
		if decisionID := payloadString(payload, "decision_id"); decisionID != "" {
			state.DecisionID = decisionID
		}
		if remaining, ok := payloadInt64(payload, "remaining_turns"); ok {
			state.RemainingTurns = int(remaining)
		}
		if approvedBy, ok := payloadInt64(payload, "approved_by_user"); ok {
			state.ApprovedBy = approvedBy
		}
		if reason := payloadString(payload, "reason"); reason != "" {
			state.BlockedReason = reason
		}

		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventContinuationOffered:
			state.Status = "pending"
		case core.ExecutionEventContinuationApproved:
			state.Status = "approved"
		case core.ExecutionEventContinuationRevoked:
			state.Status = "revoked"
		case core.ExecutionEventContinuationConsumed:
			state.Status = "consumed"
		case core.ExecutionEventContinuationBlocked:
			state.Status = "blocked"
		}
		state.UpdatedAt = event.CreatedAt
		out[chatID] = state
	}
	return out, nil
}

func (r *Runtime) recoveryPendingFromEvents(since time.Time, limit int) (core.PendingItem, bool, error) {
	if r == nil || r.store == nil {
		return core.PendingItem{}, false, nil
	}
	events, err := r.store.ExecutionEventsByTypes([]string{
		core.ExecutionEventRecoveryIssued,
		core.ExecutionEventRecoveryCompleted,
		core.ExecutionEventRecoveryFailed,
	}, since, limit)
	if err != nil {
		return core.PendingItem{}, false, err
	}
	sort.Slice(events, func(i, j int) bool { return executionEventBefore(events[i], events[j]) })

	var latestIssued session.ExecutionEvent
	var latestTerminal session.ExecutionEvent
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventRecoveryIssued:
			latestIssued = event
		case core.ExecutionEventRecoveryCompleted, core.ExecutionEventRecoveryFailed:
			if latestIssued.ID != 0 && !event.CreatedAt.Before(latestIssued.CreatedAt) {
				latestTerminal = event
			}
		}
	}
	if latestIssued.ID == 0 {
		return core.PendingItem{}, false, nil
	}
	if latestTerminal.ID != 0 && !latestTerminal.CreatedAt.Before(latestIssued.CreatedAt) {
		return core.PendingItem{}, false, nil
	}

	payload := executionEventPayload(latestIssued.PayloadJSON)
	pendingCount, _ := payloadInt64(payload, "pending_count")
	summary := "status=issued"
	if pendingCount > 0 {
		summary = fmt.Sprintf("status=issued pending_count=%d", pendingCount)
	}
	updatedAt := latestIssued.CreatedAt
	return core.PendingItem{
		Kind:          core.PendingItemKindRecovery,
		ChatID:        latestIssued.ChatID,
		ID:            "recovery:startup",
		Summary:       summary,
		Age:           statusAge(time.Now().UTC(), updatedAt, time.Time{}),
		CreatedAt:     latestIssued.CreatedAt,
		UpdatedAt:     updatedAt,
		SourceClass:   "canonical",
		SourceSurface: "execution_events.recovery",
	}, true, nil
}

func executionEventBefore(left session.ExecutionEvent, right session.ExecutionEvent) bool {
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.Before(right.CreatedAt)
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return left.Seq < right.Seq
}

func executionEventPayload(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}
	return payload
}

func payloadString(payload map[string]any, key string) string {
	if len(payload) == 0 {
		return ""
	}
	value, ok := payload[strings.TrimSpace(key)]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func payloadInt64(payload map[string]any, key string) (int64, bool) {
	value := payloadString(payload, key)
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func payloadBool(payload map[string]any, key string) (bool, bool) {
	value := strings.ToLower(strings.TrimSpace(payloadString(payload, key)))
	if value == "" {
		return false, false
	}
	switch value {
	case "1", "true", "yes", "y":
		return true, true
	case "0", "false", "no", "n":
		return false, true
	default:
		return false, false
	}
}

func payloadStringSlice(payload map[string]any, key string) []string {
	if len(payload) == 0 {
		return nil
	}
	raw, ok := payload[strings.TrimSpace(key)]
	if !ok || raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			value := strings.TrimSpace(fmt.Sprint(item))
			if value != "" {
				out = append(out, value)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	default:
		return splitStatusCSV(strings.TrimSpace(fmt.Sprint(typed)))
	}
}

func splitStatusCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func summarizeExecutionEvents(events []session.ExecutionEvent, limit int) []core.ExecutionEventSummary {
	if len(events) == 0 || limit == 0 {
		return nil
	}
	if limit < 0 {
		limit = len(events)
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })
	out := make([]core.ExecutionEventSummary, 0, minStatusInt(limit, len(ordered)))
	for i := len(ordered) - 1; i >= 0; i-- {
		event := ordered[i]
		payload := executionEventPayload(event.PayloadJSON)
		out = append(out, core.ExecutionEventSummary{
			SessionID: strings.TrimSpace(event.SessionID),
			ChatID:    event.ChatID,
			ScopeKind: strings.TrimSpace(string(event.Scope.Kind)),
			ScopeID:   strings.TrimSpace(event.Scope.ID),
			AgentID:   strings.TrimSpace(event.Scope.DurableAgentID),
			Seq:       event.Seq,
			EventType: strings.TrimSpace(event.EventType),
			Stage:     strings.TrimSpace(event.Stage),
			Status:    strings.TrimSpace(event.Status),
			Summary:   summarizeExecutionEventPayload(strings.TrimSpace(event.EventType), strings.TrimSpace(event.Status), payload),
			CreatedAt: event.CreatedAt,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func statusAdjudicationsFromExecutionEvents(events []session.ExecutionEvent, limit int) []core.AdjudicationStatusSnapshot {
	if len(events) == 0 || limit == 0 {
		return nil
	}
	if limit < 0 {
		limit = len(events)
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })
	out := make([]core.AdjudicationStatusSnapshot, 0, minStatusInt(limit, len(ordered)))
	for i := len(ordered) - 1; i >= 0; i-- {
		event := ordered[i]
		eventType := strings.TrimSpace(event.EventType)
		if eventType != core.ExecutionEventReplyClaimAdjudicated && eventType != core.ExecutionEventContinuationAdjudicated {
			continue
		}
		adjudication, ok := runtimeAdjudicationFromExecutionEvent(event)
		if !ok {
			continue
		}
		out = append(out, core.AdjudicationStatusSnapshot{
			SessionID:     strings.TrimSpace(event.SessionID),
			ChatID:        event.ChatID,
			Seq:           event.Seq,
			Kind:          adjudication.Kind,
			Surface:       adjudication.Surface,
			SubjectID:     adjudication.SubjectID,
			OperatorLabel: adjudication.OperatorLabel,
			VisibleAction: adjudication.VisibleAction,
			Findings:      append([]core.RuntimeFinding(nil), adjudication.Findings...),
			EvidenceRefs:  append([]string(nil), adjudication.EvidenceRefs...),
			CreatedAt:     event.CreatedAt,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func runtimeAdjudicationFromExecutionEvent(event session.ExecutionEvent) (core.RuntimeAdjudication, bool) {
	payload := executionEventPayload(event.PayloadJSON)
	if len(payload) == 0 {
		return core.RuntimeAdjudication{}, false
	}
	findings := payloadRuntimeFindings(payload, "findings")
	if len(findings) == 0 {
		claimTypes := payloadStringSlice(payload, "claim_types")
		details := payloadStringSlice(payload, "details")
		for i, claimType := range claimTypes {
			detail := ""
			if i < len(details) {
				detail = details[i]
			}
			findings = append(findings, core.RuntimeFinding{
				Kind:           claimType,
				ClaimType:      claimType,
				EvidenceStatus: "not_observed_in_current_turn",
				Detail:         detail,
			})
		}
	}
	adjudication := core.NormalizeRuntimeAdjudication(core.RuntimeAdjudication{
		Kind:          firstNonEmpty(payloadString(payload, "adjudication_kind"), "execution_claim"),
		Surface:       firstNonEmpty(payloadString(payload, "surface"), "final_reply"),
		SubjectID:     firstNonEmpty(payloadString(payload, "subject_id"), "latest_turn"),
		OperatorLabel: firstNonEmpty(payloadString(payload, "operator_label"), executionClaimOperatorLabel(payloadString(payload, "visible_action"))),
		Findings:      findings,
		EvidenceRefs:  payloadStringSlice(payload, "evidence_refs"),
		VisibleAction: payloadString(payload, "visible_action"),
		CreatedAt:     event.CreatedAt,
	})
	if adjudication.Kind == "" && len(adjudication.Findings) == 0 {
		return core.RuntimeAdjudication{}, false
	}
	return adjudication, true
}

func payloadRuntimeFindings(payload map[string]any, key string) []core.RuntimeFinding {
	if len(payload) == 0 {
		return nil
	}
	raw, ok := payload[strings.TrimSpace(key)]
	if !ok || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]core.RuntimeFinding, 0, len(items))
	for _, item := range items {
		rawFinding, err := json.Marshal(item)
		if err != nil {
			continue
		}
		var finding core.RuntimeFinding
		if err := json.Unmarshal(rawFinding, &finding); err != nil {
			continue
		}
		finding = core.NormalizeRuntimeFinding(finding)
		if finding.Kind == "" && finding.Detail == "" {
			continue
		}
		out = append(out, finding)
	}
	return out
}

func latestTurnSnapshotForChatFromExecutionEvents(events []session.ExecutionEvent, chatID int64) (core.TurnRunStatusSnapshot, bool) {
	if chatID == 0 || len(events) == 0 {
		return core.TurnRunStatusSnapshot{}, false
	}
	byChat := latestTurnSnapshotsByChatFromExecutionEvents(events)
	latest, ok := byChat[chatID]
	if !ok {
		return core.TurnRunStatusSnapshot{}, false
	}
	return latest, true
}

func latestTurnSnapshotsByChatFromExecutionEvents(events []session.ExecutionEvent) map[int64]core.TurnRunStatusSnapshot {
	if len(events) == 0 {
		return map[int64]core.TurnRunStatusSnapshot{}
	}
	ordered := append([]session.ExecutionEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool { return executionEventBefore(ordered[i], ordered[j]) })

	out := make(map[int64]core.TurnRunStatusSnapshot, 16)
	for _, event := range ordered {
		chatID := event.ChatID
		if chatID == 0 {
			continue
		}
		eventType := strings.TrimSpace(event.EventType)
		payload := executionEventPayload(event.PayloadJSON)

		switch eventType {
		case core.ExecutionEventTurnStarted:
			runID, _ := payloadInt64(payload, "run_id")
			runKind := firstNonEmpty(payloadString(payload, "run_kind"), "interactive")
			out[chatID] = core.TurnRunStatusSnapshot{
				ID:             runID,
				ChatID:         chatID,
				Kind:           strings.TrimSpace(runKind),
				Status:         string(session.TurnRunStatusRunning),
				RequestText:    truncateStatusDiagnostic(strings.TrimSpace(payloadString(payload, "request_text")), 220),
				StartedAt:      event.CreatedAt,
				LastActivityAt: event.CreatedAt,
				Source:         "canonical:execution_events.turn",
			}
		case core.ExecutionEventTurnStageChanged:
			snapshot := ensureEventTurnSnapshot(out, chatID, event.CreatedAt)
			if strings.TrimSpace(snapshot.Status) == "" {
				snapshot.Status = string(session.TurnRunStatusRunning)
			}
			out[chatID] = snapshot
		case core.ExecutionEventToolStarted:
			snapshot := ensureEventTurnSnapshot(out, chatID, event.CreatedAt)
			if strings.TrimSpace(snapshot.Status) == "" {
				snapshot.Status = string(session.TurnRunStatusRunning)
			}
			toolName := strings.TrimSpace(payloadString(payload, "tool"))
			if toolName != "" {
				snapshot.LastToolName = toolName
			}
			preview := strings.TrimSpace(payloadString(payload, "preview"))
			if preview != "" {
				snapshot.LastToolPreview = truncateStatusDiagnostic(preview, 220)
			}
			out[chatID] = snapshot
		case core.ExecutionEventToolSucceeded:
			snapshot := ensureEventTurnSnapshot(out, chatID, event.CreatedAt)
			if strings.TrimSpace(snapshot.Status) == "" {
				snapshot.Status = string(session.TurnRunStatusRunning)
			}
			toolName := strings.TrimSpace(payloadString(payload, "tool"))
			if toolName != "" {
				snapshot.LastToolName = toolName
			}
			result := strings.TrimSpace(payloadString(payload, "result_preview"))
			if result != "" {
				snapshot.LastToolResultPreview = truncateStatusDiagnostic(result, 220)
			}
			out[chatID] = snapshot
		case core.ExecutionEventToolFailed:
			snapshot := ensureEventTurnSnapshot(out, chatID, event.CreatedAt)
			if strings.TrimSpace(snapshot.Status) == "" {
				snapshot.Status = string(session.TurnRunStatusRunning)
			}
			toolName := strings.TrimSpace(payloadString(payload, "tool"))
			if toolName != "" {
				snapshot.LastToolName = toolName
			}
			result := strings.TrimSpace(payloadString(payload, "result_preview"))
			if result != "" {
				snapshot.LastToolResultPreview = truncateStatusDiagnostic(result, 220)
			}
			errText := strings.TrimSpace(payloadString(payload, "error"))
			if errText != "" {
				snapshot.LastToolError = truncateStatusDiagnostic(errText, 220)
				snapshot.ErrorText = truncateStatusDiagnostic(errText, 220)
			}
			out[chatID] = snapshot
		case core.ExecutionEventTurnCompleted, core.ExecutionEventTurnFailed, core.ExecutionEventTurnInterrupted:
			snapshot := ensureEventTurnSnapshot(out, chatID, event.CreatedAt)
			switch eventType {
			case core.ExecutionEventTurnCompleted:
				snapshot.Status = string(session.TurnRunStatusCompleted)
			case core.ExecutionEventTurnFailed:
				snapshot.Status = string(session.TurnRunStatusFailed)
			case core.ExecutionEventTurnInterrupted:
				snapshot.Status = string(session.TurnRunStatusInterrupted)
			}
			if errText := strings.TrimSpace(payloadString(payload, "error")); errText != "" {
				snapshot.ErrorText = truncateStatusDiagnostic(errText, 220)
			}
			out[chatID] = snapshot
		}
	}
	return out
}

func ensureEventTurnSnapshot(
	byChat map[int64]core.TurnRunStatusSnapshot,
	chatID int64,
	activityAt time.Time,
) core.TurnRunStatusSnapshot {
	snapshot := byChat[chatID]
	snapshot.ChatID = chatID
	if strings.TrimSpace(snapshot.Kind) == "" {
		snapshot.Kind = "interactive"
	}
	if strings.TrimSpace(snapshot.Source) == "" {
		snapshot.Source = "canonical:execution_events.turn"
	}
	if snapshot.StartedAt.IsZero() {
		snapshot.StartedAt = activityAt
	}
	if snapshot.LastActivityAt.IsZero() || snapshot.LastActivityAt.Before(activityAt) {
		snapshot.LastActivityAt = activityAt
	}
	return snapshot
}
