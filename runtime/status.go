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
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) IsTelegramAdmin(userID int64) bool {
	if r == nil || r.resolver == nil || userID <= 0 {
		return false
	}
	actor, ok := r.resolver.ResolveTelegramUser(userID)
	return ok && actor.Role == principal.RoleAdmin
}

func (r *Runtime) StatusDiagnostics(chatID int64) ([]string, error) {
	if r == nil || r.store == nil || chatID == 0 {
		return nil, nil
	}

	chatSnapshot, err := r.ChatStatusSnapshot(chatID, core.RouterStatusSnapshot{})
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, 8)
	if latest := chatSnapshot.LatestTurnRun; latest != nil {
		lines = append(lines, fmt.Sprintf("Latest persisted turn: %s (%s).", strings.TrimSpace(latest.Status), strings.TrimSpace(latest.Kind)))
		if !latest.LastActivityAt.IsZero() {
			lines = append(lines, "Last activity: "+latest.LastActivityAt.UTC().Format(time.RFC3339)+".")
		}
		if tool := strings.TrimSpace(latest.LastToolName); tool != "" {
			lines = append(lines, "Last tool: "+tool+".")
		}
		if latest.ProgressMessageID != 0 {
			lines = append(lines, fmt.Sprintf("Progress message id: %d.", latest.ProgressMessageID))
		}
		if errorText := strings.TrimSpace(latest.ErrorText); errorText != "" {
			lines = append(lines, "Last error: "+truncateStatusDiagnostic(errorText, 180)+".")
		}
	}
	if continuation := chatSnapshot.Continuation; continuation != nil {
		status := strings.ToLower(strings.TrimSpace(continuation.Status))
		if status == "pending" || status == "approved" || status == "revoked" {
			line := "Continuation: " + status
			if continuation.RemainingTurns > 0 {
				if continuation.RemainingTurns == 1 {
					line += " (1 turn remaining)"
				} else {
					line += fmt.Sprintf(" (%d turns remaining)", continuation.RemainingTurns)
				}
			}
			lines = append(lines, line+".")
		}
	}
	return lines, nil
}

func (r *Runtime) ChatStatusSnapshot(chatID int64, router core.RouterStatusSnapshot) (core.ChatStatusSnapshot, error) {
	system, err := r.SystemStatusSnapshot(router)
	if err != nil {
		return core.ChatStatusSnapshot{}, err
	}

	snapshot := core.ChatStatusSnapshot{
		GeneratedAt:   system.GeneratedAt,
		ChatID:        chatID,
		QueueDepth:    system.QueueDepthByChat[chatID],
		RestartHealth: system.RestartHealth,
	}
	if ids := system.ActiveTurnsByChat[chatID]; len(ids) > 0 {
		snapshot.ActiveTurnIDs = append(snapshot.ActiveTurnIDs, ids...)
	}
	for _, item := range system.PendingItems {
		if item.ChatID == chatID {
			snapshot.PendingItems = append(snapshot.PendingItems, item)
		}
	}
	for _, continuation := range system.Continuations {
		if continuation.ChatID != chatID {
			continue
		}
		copied := continuation
		snapshot.Continuation = &copied
		break
	}
	if run, ok := system.LatestTurnRunsByChat[chatID]; ok {
		copied := run
		snapshot.LatestTurnRun = &copied
	}
	if r != nil && r.store != nil {
		key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
		events, eventsErr := r.store.ExecutionEventsBySession(key, 0, 500)
		if eventsErr != nil {
			return core.ChatStatusSnapshot{}, eventsErr
		}
		snapshot.RecentExecution = summarizeExecutionEvents(events, 12)
		if latestFromEvents, ok := latestTurnSnapshotForChatFromExecutionEvents(events, chatID); ok {
			copied := latestFromEvents
			snapshot.LatestTurnRun = &copied
		}
		if phase, ok := latestTurnPhaseFromExecutionEvents(events); ok {
			snapshot.TurnPhase = strings.TrimSpace(phase.Phase)
			snapshot.TurnPhaseSummary = strings.TrimSpace(phase.Summary)
			snapshot.TurnPhaseUpdatedAt = phase.UpdatedAt
		}
		if sidecars, ok := latestStatusSidecarsFromExecutionEvents(events); ok {
			snapshot.OperationStatus = sidecars.OperationStatus
			snapshot.OperationStage = sidecars.OperationStage
			snapshot.OperationSummary = sidecars.OperationSummary
			snapshot.PlanStepStatus = sidecars.PlanStepStatus
			snapshot.PlanStep = sidecars.PlanStep
			snapshot.PlanCompletedSteps = sidecars.PlanCompletedSteps
			snapshot.PlanTotalSteps = sidecars.PlanTotalSteps
			snapshot.PlanFullyExecuted = sidecars.PlanFullyExecuted
			snapshot.HiddenInputCategories = append(snapshot.HiddenInputCategories[:0], sidecars.HiddenInputCategories...)
			snapshot.HiddenInputSummary = sidecars.HiddenInputSummary
		}
		if deliveryStatus, deliverySummary, ok := deliveryStatusFromExecutionEvents(events); ok {
			snapshot.DeliveryStatus = deliveryStatus
			snapshot.DeliverySummary = deliverySummary
		}

		if (snapshot.OperationStatus == "" && snapshot.OperationStage == "" && snapshot.OperationSummary == "") ||
			(snapshot.PlanStepStatus == "" && snapshot.PlanStep == "" && snapshot.PlanTotalSteps == 0) ||
			(len(snapshot.HiddenInputCategories) == 0 && snapshot.HiddenInputSummary == "") ||
			(snapshot.DeliveryStatus == "" && snapshot.DeliverySummary == "") {
			statusState, exists, stateErr := r.store.StatusStateIfExists(key)
			if stateErr != nil {
				return core.ChatStatusSnapshot{}, stateErr
			}
			if exists {
				if snapshot.OperationStatus == "" && snapshot.OperationStage == "" && snapshot.OperationSummary == "" {
					snapshot.OperationStatus, snapshot.OperationStage, snapshot.OperationSummary = operationStatusFields(statusState.OperationState)
				}
				if snapshot.PlanStepStatus == "" && snapshot.PlanStep == "" && snapshot.PlanTotalSteps == 0 {
					snapshot.PlanStepStatus, snapshot.PlanStep = planStatusFields(statusState.PlanState)
					snapshot.PlanCompletedSteps, snapshot.PlanTotalSteps, snapshot.PlanFullyExecuted = planProgressFields(statusState.PlanState)
				}
				if len(snapshot.HiddenInputCategories) == 0 && snapshot.HiddenInputSummary == "" {
					snapshot.HiddenInputCategories, snapshot.HiddenInputSummary = hiddenInputStatusFields(statusState.LastFloorMetadata)
				}
				if snapshot.DeliveryStatus == "" && snapshot.DeliverySummary == "" {
					snapshot.DeliveryStatus, snapshot.DeliverySummary = deliveryStatusFields(snapshot.LatestTurnRun, statusState.OutboundCountAtTurn)
				}
			}
		}
	}
	for _, stale := range system.StaleRunningTurns {
		if stale.ChatID == chatID {
			snapshot.StaleRunningTurns = append(snapshot.StaleRunningTurns, stale)
		}
	}
	if r != nil && r.store != nil {
		if toolRows, err := r.toolLifecycleStatusSnapshot(20); err != nil {
			return core.ChatStatusSnapshot{}, err
		} else {
			snapshot.ToolLifecycle = toolRows
		}
	}
	return snapshot, nil
}

func (r *Runtime) SystemStatusSnapshot(router core.RouterStatusSnapshot) (core.SystemStatusSnapshot, error) {
	now := time.Now().UTC()
	snapshot := core.SystemStatusSnapshot{
		GeneratedAt:          now,
		ActiveTurnsByChat:    make(map[int64][]uint64),
		QueueDepthByChat:     make(map[int64]int),
		PendingItems:         make([]core.PendingItem, 0, 16),
		Continuations:        make([]core.ContinuationStatusSnapshot, 0, 8),
		LatestTurnRunsByChat: make(map[int64]core.TurnRunStatusSnapshot),
		StaleRunningTurns:    make([]core.TurnRunStatusSnapshot, 0, 8),
		HotChats:             make([]core.ChatStatusRollup, 0, 8),
		RestartHealth:        r.restartHealthSnapshot(),
	}

	if r == nil || r.store == nil {
		snapshot.ActiveTurnsByChat = cloneActiveTurnMap(router.ActiveTurnsByChat)
		snapshot.QueueDepthByChat = cloneQueueDepthMap(router.QueueDepthByChat)
		for _, ids := range snapshot.ActiveTurnsByChat {
			snapshot.ActiveTurnCount += len(ids)
		}
		snapshot.ActiveChatIDs = sortedInt64Keys(snapshot.ActiveTurnsByChat)
		for _, chatID := range sortedInt64KeysFromInt(snapshot.QueueDepthByChat) {
			depth := snapshot.QueueDepthByChat[chatID]
			if depth <= 0 {
				continue
			}
			snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
				Kind:    core.PendingItemKindQueue,
				ChatID:  chatID,
				ID:      "queue:" + strconv.FormatInt(chatID, 10),
				Summary: fmt.Sprintf("queue_depth=%d", depth),
			})
		}
		sortPendingItems(snapshot.PendingItems)
		snapshot.HotChats = buildHotChatRollups(snapshot)
		return snapshot, nil
	}

	recentEvents, err := r.store.ExecutionEventsRecent(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	snapshot.RecentExecution = summarizeExecutionEvents(recentEvents, 20)
	activeByChat, queueByChat := liveRouterSignalsFromExecutionEvents(recentEvents)
	snapshot.ActiveTurnsByChat = activeByChat
	snapshot.QueueDepthByChat = queueByChat
	for chatID, ids := range cloneActiveTurnMap(router.ActiveTurnsByChat) {
		if _, exists := snapshot.ActiveTurnsByChat[chatID]; exists {
			continue
		}
		snapshot.ActiveTurnsByChat[chatID] = ids
	}
	for chatID, depth := range cloneQueueDepthMap(router.QueueDepthByChat) {
		if _, exists := snapshot.QueueDepthByChat[chatID]; exists {
			continue
		}
		snapshot.QueueDepthByChat[chatID] = depth
	}
	for _, ids := range snapshot.ActiveTurnsByChat {
		snapshot.ActiveTurnCount += len(ids)
	}
	snapshot.ActiveChatIDs = sortedInt64Keys(snapshot.ActiveTurnsByChat)
	for _, chatID := range sortedInt64KeysFromInt(snapshot.QueueDepthByChat) {
		depth := snapshot.QueueDepthByChat[chatID]
		if depth <= 0 {
			continue
		}
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:    core.PendingItemKindQueue,
			ChatID:  chatID,
			ID:      "queue:" + strconv.FormatInt(chatID, 10),
			Summary: fmt.Sprintf("queue_depth=%d", depth),
		})
	}
	for chatID, run := range latestTurnSnapshotsByChatFromExecutionEvents(recentEvents) {
		snapshot.LatestTurnRunsByChat[chatID] = run
	}

	decisionEventState, err := r.decisionEventStates(now.Add(-7*24*time.Hour), 2000)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	pendingDecisions, err := r.store.PendingDecisions()
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	pendingDecisionSeen := make(map[string]struct{}, len(pendingDecisions))
	for _, pending := range pendingDecisions {
		decisionID := strings.TrimSpace(pending.ID)
		if decisionID != "" {
			pendingDecisionSeen[decisionID] = struct{}{}
		}
		age := statusAge(now, pending.UpdatedAt, pending.CreatedAt)
		timeout := time.Duration(pending.TimeoutNanos)
		stale := timeout > 0 && age > timeout
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:          core.PendingItemKindDecision,
			ChatID:        pending.ChatID,
			ID:            decisionID,
			Summary:       renderDecisionSummary(pending),
			Age:           age,
			CreatedAt:     pending.CreatedAt,
			UpdatedAt:     pending.UpdatedAt,
			Stale:         stale,
			SourceClass:   "operational_current_state_store",
			SourceSurface: "pending_decisions",
		})
	}
	for _, state := range decisionEventState {
		if !state.pending() {
			continue
		}
		decisionID := strings.TrimSpace(state.DecisionID)
		if decisionID != "" {
			if _, covered := pendingDecisionSeen[decisionID]; covered {
				continue
			}
		}
		updatedAt := coalesceTime(state.UpdatedAt, state.CreatedAt)
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:          core.PendingItemKindDecision,
			ChatID:        state.ChatID,
			ID:            decisionID,
			Summary:       renderDecisionSummaryFromFields(state.Kind, state.Prompt),
			Age:           statusAge(now, updatedAt, state.CreatedAt),
			CreatedAt:     state.CreatedAt,
			UpdatedAt:     updatedAt,
			SourceClass:   "canonical",
			SourceSurface: "execution_events.decision",
		})
	}

	continuationEventState, err := r.continuationEventStates(now.Add(-7*24*time.Hour), 2000)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	continuations, err := r.store.ContinuationStates()
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	continuationChatSeen := make(map[int64]struct{}, len(continuationEventState))
	for _, row := range continuations {
		state := session.NormalizeContinuationState(row.State)
		status := strings.TrimSpace(string(state.Status))
		chatID := row.Key.ChatID
		if status != "" {
			operationalSnapshot := core.ContinuationStatusSnapshot{
				ChatID:           chatID,
				Status:           status,
				RemainingTurns:   state.RemainingTurns,
				DecisionID:       strings.TrimSpace(state.DecisionID),
				ApprovedBy:       state.ApprovedBy,
				PersonaIntent:    strings.TrimSpace(string(state.PersonaIntent.Decision)),
				GovernorIntent:   strings.TrimSpace(string(state.GovernorIntent.Decision)),
				GovernorRatified: state.GovernorIntent.Ratified,
				BlockedReason:    strings.TrimSpace(state.HandshakeBlockedReason),
				UpdatedAt:        coalesceTime(row.UpdatedAt, state.UpdatedAt),
				Source:           "operational_current_state_store:continuation_state_json",
			}
			snapshot.Continuations = append(snapshot.Continuations, operationalSnapshot)
			continuationChatSeen[chatID] = struct{}{}
			if state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved {
				updatedAt := coalesceTime(row.UpdatedAt, state.UpdatedAt)
				snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
					Kind:          core.PendingItemKindContinuation,
					ChatID:        chatID,
					ID:            continuationItemID(state, chatID),
					Summary:       renderContinuationSummary(state),
					Age:           statusAge(now, updatedAt, time.Time{}),
					UpdatedAt:     updatedAt,
					SourceClass:   "operational_current_state_store",
					SourceSurface: "continuation_state_json",
				})
			}
			continue
		}
		if eventState, covered := continuationEventState[chatID]; covered {
			snapshot.Continuations = append(snapshot.Continuations, eventState)
			continuationChatSeen[chatID] = struct{}{}
			if continuationSnapshotIsPending(eventState) {
				snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
					Kind:          core.PendingItemKindContinuation,
					ChatID:        chatID,
					ID:            continuationSnapshotItemID(eventState, chatID),
					Summary:       renderContinuationSnapshotSummary(eventState),
					Age:           statusAge(now, eventState.UpdatedAt, time.Time{}),
					UpdatedAt:     eventState.UpdatedAt,
					SourceClass:   "canonical",
					SourceSurface: "execution_events.continuation",
				})
			}
		}
	}
	for chatID, eventState := range continuationEventState {
		if _, seen := continuationChatSeen[chatID]; seen {
			continue
		}
		snapshot.Continuations = append(snapshot.Continuations, eventState)
		if continuationSnapshotIsPending(eventState) {
			snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
				Kind:          core.PendingItemKindContinuation,
				ChatID:        chatID,
				ID:            continuationSnapshotItemID(eventState, chatID),
				Summary:       renderContinuationSnapshotSummary(eventState),
				Age:           statusAge(now, eventState.UpdatedAt, time.Time{}),
				UpdatedAt:     eventState.UpdatedAt,
				SourceClass:   "canonical",
				SourceSurface: "execution_events.continuation",
			})
		}
	}

	pendingReviews, err := r.store.PendingReviewEventsAll(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, event := range pendingReviews {
		updatedAt := coalesceTime(event.CreatedAt)
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:          core.PendingItemKindReview,
			ChatID:        event.TargetAdminChatID,
			ID:            fmt.Sprintf("review:%d", event.ID),
			Summary:       renderPendingReviewSummary(event),
			Age:           statusAge(now, updatedAt, time.Time{}),
			CreatedAt:     event.CreatedAt,
			UpdatedAt:     updatedAt,
			SourceClass:   "operational_current_state_store",
			SourceSurface: "review_events.pending",
		})
	}

	latestRuns, err := r.store.LatestTurnRunsByChat(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, run := range latestRuns {
		if _, exists := snapshot.LatestTurnRunsByChat[run.ChatID]; exists {
			continue
		}
		snapshot.LatestTurnRunsByChat[run.ChatID] = turnRunSnapshot(run)
	}

	pendingRecovery, err := r.store.PendingRecoveryTurnRuns(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, run := range pendingRecovery {
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:          core.PendingItemKindRecovery,
			ChatID:        run.ChatID,
			ID:            fmt.Sprintf("recovery:%d", run.ID),
			Summary:       fmt.Sprintf("turn_run_id=%d status=%s", run.ID, run.Status),
			Age:           statusAge(now, run.LastActivityAt, run.StartedAt),
			CreatedAt:     run.StartedAt,
			UpdatedAt:     run.LastActivityAt,
			SourceClass:   "compatibility_fallback",
			SourceSurface: "turn_runs",
		})
	}
	recoveryPending, recoveryPendingOK, err := r.recoveryPendingFromEvents(now.Add(-7*24*time.Hour), 2000)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	if recoveryPendingOK {
		snapshot.PendingItems = append(snapshot.PendingItems, recoveryPending)
	}

	staleRuns, err := r.staleRunningTurnRuns(now)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, run := range staleRuns {
		snapshot.StaleRunningTurns = append(snapshot.StaleRunningTurns, turnRunSnapshot(run))
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:          core.PendingItemKindStaleTurn,
			ChatID:        run.ChatID,
			ID:            fmt.Sprintf("stale:%d", run.ID),
			Summary:       fmt.Sprintf("turn_run_id=%d last_activity=%s", run.ID, run.LastActivityAt.UTC().Format(time.RFC3339)),
			Age:           statusAge(now, run.LastActivityAt, run.StartedAt),
			CreatedAt:     run.StartedAt,
			UpdatedAt:     run.LastActivityAt,
			Stale:         true,
			SourceClass:   "operational_current_state_store",
			SourceSurface: "turn_runs",
		})
	}

	sort.Slice(snapshot.Continuations, func(i, j int) bool {
		if snapshot.Continuations[i].ChatID == snapshot.Continuations[j].ChatID {
			return snapshot.Continuations[i].Status < snapshot.Continuations[j].Status
		}
		return snapshot.Continuations[i].ChatID < snapshot.Continuations[j].ChatID
	})
	sort.Slice(snapshot.StaleRunningTurns, func(i, j int) bool {
		if snapshot.StaleRunningTurns[i].ChatID == snapshot.StaleRunningTurns[j].ChatID {
			return snapshot.StaleRunningTurns[i].ID < snapshot.StaleRunningTurns[j].ID
		}
		return snapshot.StaleRunningTurns[i].ChatID < snapshot.StaleRunningTurns[j].ChatID
	})
	sortPendingItems(snapshot.PendingItems)
	snapshot.HotChats = buildHotChatRollups(snapshot)
	return snapshot, nil
}

func (r *Runtime) staleRunningTurnRuns(now time.Time) ([]session.TurnRun, error) {
	if r == nil || r.staleTurnSweep == nil || r.staleTurnThreshold <= 0 {
		return nil, nil
	}
	cutoff := now.Add(-r.staleTurnThreshold)
	limit := r.staleTurnLimit
	if limit <= 0 {
		limit = 50
	}
	return r.staleTurnSweep(cutoff, limit)
}

func (r *Runtime) restartHealthSnapshot() core.RestartHealthSnapshot {
	if r == nil {
		return core.RestartHealthSnapshot{}
	}
	return core.RestartHealthSnapshot{
		WatchdogTriggered:  r.staleWatchdogTriggered.Load(),
		StaleTurnThreshold: r.staleTurnThreshold,
		StaleTurnLimit:     r.staleTurnLimit,
	}
}

func cloneActiveTurnMap(in map[int64][]uint64) map[int64][]uint64 {
	out := make(map[int64][]uint64, len(in))
	for chatID, ids := range in {
		if len(ids) == 0 {
			continue
		}
		copied := append([]uint64(nil), ids...)
		sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
		out[chatID] = copied
	}
	return out
}

func cloneQueueDepthMap(in map[int64]int) map[int64]int {
	out := make(map[int64]int, len(in))
	for chatID, depth := range in {
		if depth <= 0 {
			continue
		}
		out[chatID] = depth
	}
	return out
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

func sortedInt64Keys(values map[int64][]uint64) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedInt64KeysFromInt(values map[int64]int) []int64 {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortPendingItems(items []core.PendingItem) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.ChatID != b.ChatID {
			return a.ChatID < b.ChatID
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Summary < b.Summary
	})
}

func buildHotChatRollups(snapshot core.SystemStatusSnapshot) []core.ChatStatusRollup {
	rollups := map[int64]*core.ChatStatusRollup{}
	ensure := func(chatID int64) *core.ChatStatusRollup {
		rollup := rollups[chatID]
		if rollup == nil {
			rollup = &core.ChatStatusRollup{ChatID: chatID}
			rollups[chatID] = rollup
		}
		return rollup
	}

	for chatID, ids := range snapshot.ActiveTurnsByChat {
		rollup := ensure(chatID)
		rollup.ActiveTurnCount = len(ids)
	}
	for chatID, depth := range snapshot.QueueDepthByChat {
		rollup := ensure(chatID)
		rollup.QueueDepth = depth
	}
	for _, pending := range snapshot.PendingItems {
		rollup := ensure(pending.ChatID)
		rollup.PendingCount++
	}
	for chatID, run := range snapshot.LatestTurnRunsByChat {
		rollup := ensure(chatID)
		rollup.LatestStatus = strings.TrimSpace(run.Status)
		rollup.LastActivityAt = run.LastActivityAt
	}

	out := make([]core.ChatStatusRollup, 0, len(rollups))
	for _, rollup := range rollups {
		out = append(out, *rollup)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := out[i], out[j]
		if left.PendingCount != right.PendingCount {
			return left.PendingCount > right.PendingCount
		}
		if left.ActiveTurnCount != right.ActiveTurnCount {
			return left.ActiveTurnCount > right.ActiveTurnCount
		}
		if left.QueueDepth != right.QueueDepth {
			return left.QueueDepth > right.QueueDepth
		}
		if !left.LastActivityAt.Equal(right.LastActivityAt) {
			return left.LastActivityAt.After(right.LastActivityAt)
		}
		return left.ChatID < right.ChatID
	})
	return out
}

func turnRunSnapshot(run session.TurnRun) core.TurnRunStatusSnapshot {
	return core.TurnRunStatusSnapshot{
		ID:                    run.ID,
		ChatID:                run.ChatID,
		Kind:                  strings.TrimSpace(string(run.Kind)),
		Status:                strings.TrimSpace(string(run.Status)),
		RequestText:           truncateStatusDiagnostic(strings.TrimSpace(run.RequestText), 220),
		LastActivityAt:        run.LastActivityAt,
		ProgressMessageID:     run.ProgressMessageID,
		LastToolName:          strings.TrimSpace(run.LastToolName),
		LastToolPreview:       truncateStatusDiagnostic(strings.TrimSpace(run.LastToolPreview), 220),
		LastToolResultPreview: truncateStatusDiagnostic(strings.TrimSpace(run.LastToolResultPreview), 220),
		LastToolError:         truncateStatusDiagnostic(strings.TrimSpace(run.LastToolError), 220),
		ErrorText:             strings.TrimSpace(firstNonEmptyStatus(run.ErrorText, run.LastToolError)),
		StartedAt:             run.StartedAt,
		Source:                "compatibility_fallback:turn_runs",
	}
}

type decisionEventProjection struct {
	DecisionID    string
	ChatID        int64
	Kind          string
	Prompt        string
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

func renderDecisionSummaryFromFields(kind string, prompt string) string {
	kind = strings.TrimSpace(kind)
	prompt = truncateStatusDiagnostic(strings.TrimSpace(prompt), 80)
	if kind == "" {
		return "prompt=" + prompt
	}
	if prompt == "" {
		return "kind=" + kind
	}
	return fmt.Sprintf("kind=%s prompt=%s", kind, prompt)
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

func summarizeExecutionEventPayload(eventType string, eventStatus string, payload map[string]any) string {
	switch strings.TrimSpace(eventType) {
	case core.ExecutionEventToolProposalCreated, core.ExecutionEventToolProposalReviewed:
		reviewStatus := strings.TrimSpace(payloadString(payload, "review_status"))
		if reviewStatus == "" {
			reviewStatus = strings.TrimSpace(eventStatus)
		}
		parts := make([]string, 0, 8)
		if proposalID := strings.TrimSpace(payloadString(payload, "proposal_id")); proposalID != "" {
			parts = append(parts, "proposal_id="+proposalID)
		}
		if toolName := strings.TrimSpace(payloadString(payload, "tool_name")); toolName != "" {
			parts = append(parts, "tool_name="+toolName)
		}
		if reviewStatus != "" {
			parts = append(parts, "review_status="+reviewStatus)
		}
		if registeredToolID := strings.TrimSpace(payloadString(payload, "registered_tool_id")); registeredToolID != "" {
			parts = append(parts, "registered_tool_id="+registeredToolID)
		}
		if ratifiedVia := strings.TrimSpace(payloadString(payload, "ratified_via")); ratifiedVia != "" {
			parts = append(parts, "ratified_via="+ratifiedVia)
		}
		if reviewVia := strings.TrimSpace(payloadString(payload, "review_via")); reviewVia != "" {
			parts = append(parts, "review_via="+reviewVia)
		}
		if reason := strings.TrimSpace(payloadString(payload, "transition_reason")); reason != "" {
			parts = append(parts, "transition_reason="+reason)
		}
		if overrideReason := strings.TrimSpace(payloadString(payload, "override_reason")); overrideReason != "" {
			parts = append(parts, "override_reason="+truncateStatusDiagnostic(overrideReason, 80))
		}
		return strings.TrimSpace(strings.Join(parts, " "))
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
		if proposalID := strings.TrimSpace(payloadString(payload, "proposal_id")); proposalID != "" {
			parts = append(parts, "proposal_id="+proposalID)
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	case core.ExecutionEventToolExposureChanged:
		active := strings.TrimSpace(eventStatus) == "enabled"
		if value, ok := payloadBool(payload, "active"); ok {
			active = value
		}
		parts := make([]string, 0, 4)
		if toolName := strings.TrimSpace(payloadString(payload, "tool_name")); toolName != "" {
			parts = append(parts, "tool_name="+toolName)
		}
		if principal := strings.TrimSpace(payloadString(payload, "principal")); principal != "" {
			parts = append(parts, "principal="+principal)
		}
		parts = append(parts, "active="+strconv.FormatBool(active))
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

func minStatusInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func renderDecisionSummary(record session.PendingDecisionRecord) string {
	kind := strings.TrimSpace(record.Kind)
	prompt := truncateStatusDiagnostic(record.Prompt, 80)
	if kind == "" {
		return "prompt=" + prompt
	}
	if prompt == "" {
		return "kind=" + kind
	}
	return fmt.Sprintf("kind=%s prompt=%s", kind, prompt)
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

func (r *Runtime) toolLifecycleStatusSnapshot(limit int) ([]core.ToolLifecycleStatusSnapshot, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	records, err := r.store.ToolInstallRecords("", limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.ToolLifecycleStatusSnapshot, 0, len(records))
	for _, record := range records {
		probe, _, err := r.store.ToolProbeRecord(record.ToolName)
		if err != nil {
			return nil, err
		}
		audit, _, err := r.store.ToolAuditRecord(record.ToolName)
		if err != nil {
			return nil, err
		}
		out = append(out, core.ToolLifecycleStatusSnapshot{
			ToolName:      record.ToolName,
			InstallStatus: strings.TrimSpace(string(record.Status)),
			ProbeStatus:   strings.TrimSpace(string(probe.Status)),
			AuditStatus:   strings.TrimSpace(string(audit.Status)),
			InstallRef:    strings.TrimSpace(record.InstallRef),
			InstalledAt:   record.InstalledAt,
			LastProbedAt:  probe.ProbedAt,
			AuditedAt:     audit.AuditedAt,
			AttestedAt:    record.AttestedAt,
		})
	}
	return out, nil
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
