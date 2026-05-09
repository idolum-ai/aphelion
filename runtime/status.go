//go:build linux

package runtime

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const telegramMissionOwnerPrefix = "telegram:"

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
	if auto := chatSnapshot.AutoApproval; auto != nil && auto.Active {
		line := "Auto-approval: active"
		if scope := strings.TrimSpace(auto.Scope); scope != "" {
			line += " (" + scope + ")"
		}
		if !auto.ExpiresAt.IsZero() {
			line += ", expires " + auto.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if auto.MaxUses > 0 {
			line += fmt.Sprintf(", used %d/%d", auto.UsedCount, auto.MaxUses)
		} else {
			line += fmt.Sprintf(", used %d", auto.UsedCount)
		}
		lines = append(lines, line+".")
	}
	if stuck, ok := r.operationApprovalAffordanceDiagnostic(chatID, chatSnapshot); ok {
		lines = append(lines, stuck)
	}
	if len(chatSnapshot.RecentAdjudications) > 0 {
		lines = append(lines, statusAdjudicationDiagnosticLine(chatSnapshot.RecentAdjudications[0]))
	}
	return lines, nil
}

func (r *Runtime) operationApprovalAffordanceDiagnostic(chatID int64, snapshot core.ChatStatusSnapshot) (string, bool) {
	if r == nil || r.store == nil || chatID == 0 {
		return "", false
	}
	if snapshot.Continuation != nil {
		status := strings.ToLower(strings.TrimSpace(snapshot.Continuation.Status))
		if status == "pending" || status == "approved" {
			return "", false
		}
	}
	for _, item := range snapshot.PendingItems {
		if item.Kind == core.PendingItemKindContinuation || item.Kind == core.PendingItemKindDecision {
			return "", false
		}
	}
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	_, opState, exists, err := r.store.PlanAndOperationStateIfExists(key)
	if err != nil || !exists {
		return "", false
	}
	opState = session.NormalizeOperationState(opState)
	if !operationStateNeedsApprovalAffordance(opState) {
		return "", false
	}
	currentID := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID)
	staleCount := operationPhasePlanStaleInProgressCount(opState.PhasePlan)
	parts := []string{"Approval affordance gap: operation has pending approval work but no pending continuation or decision."}
	if currentID != "" {
		parts = append(parts, "current_phase="+currentID)
	}
	if staleCount > 0 {
		parts = append(parts, fmt.Sprintf("stale_in_progress_phases=%d", staleCount))
	}
	return strings.Join(parts, " ") + ".", true
}

func operationStateNeedsApprovalAffordance(opState session.OperationState) bool {
	opState = session.NormalizeOperationState(opState)
	if pendingOperationPlanLeaseNeedsButton(opState.PlanLease) || pendingOperationProposalNeedsButton(opState.Proposal) {
		return true
	}
	if _, ok := operationPlanLeaseFromPhasePlan(opState, time.Now().UTC()); ok {
		return true
	}
	if _, ok := nextOperationPhaseBundleForApproval(opState); ok {
		return true
	}
	if _, ok := nextOperationPhaseForApproval(opState); ok {
		return true
	}
	return false
}

func operationPhasePlanStaleInProgressCount(plan session.OperationPhasePlan) int {
	plan = session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan
	currentID := strings.TrimSpace(plan.CurrentPhaseID)
	if currentID == "" {
		return 0
	}
	count := 0
	for _, phase := range plan.Phases {
		phase = normalizeSingleOperationPhase(phase)
		if phase.Status == session.PlanStatusInProgress && strings.TrimSpace(phase.ID) != currentID {
			count++
		}
	}
	return count
}

func (r *Runtime) autoApprovalStatusSnapshot(chatID int64, now time.Time) (*core.AutoApprovalStatusSnapshot, error) {
	if r == nil || r.store == nil || chatID == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	leases, err := r.store.ActiveOperatorAutoApprovalLeases(chatID, now)
	if err != nil {
		return nil, err
	}
	var selected *session.OperatorAutoApprovalLease
	for _, lease := range leases {
		lease = session.NormalizeOperatorAutoApprovalLease(lease)
		if !lease.ActiveAt(now) {
			continue
		}
		if selected == nil || lease.ExpiresAt.After(selected.ExpiresAt) {
			copied := lease
			selected = &copied
		}
	}
	if selected == nil {
		return nil, nil
	}
	return &core.AutoApprovalStatusSnapshot{
		Active:      true,
		LeaseID:     strings.TrimSpace(selected.ID),
		AdminUserID: selected.AdminUserID,
		Scope:       strings.TrimSpace(selected.Scope),
		UsedCount:   selected.UsedCount,
		MaxUses:     selected.MaxUses,
		Reason:      strings.TrimSpace(selected.Reason),
		CreatedAt:   selected.CreatedAt,
		UpdatedAt:   selected.UpdatedAt,
		ExpiresAt:   selected.ExpiresAt,
	}, nil
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
	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
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
		events, eventsErr := r.store.ExecutionEventsBySession(key, 0, 500)
		if eventsErr != nil {
			return core.ChatStatusSnapshot{}, eventsErr
		}
		snapshot.RecentExecution = summarizeExecutionEvents(events, 12)
		snapshot.RecentAdjudications = statusAdjudicationsFromExecutionEvents(events, 6)
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
		autoApproval, err := r.autoApprovalStatusSnapshot(chatID, system.GeneratedAt)
		if err != nil {
			return core.ChatStatusSnapshot{}, err
		}
		snapshot.AutoApproval = autoApproval
		snapshot.MissionLedger = system.MissionLedger
		if working, err := r.store.WorkingObjective(key); err != nil {
			return core.ChatStatusSnapshot{}, err
		} else {
			snapshot.MissionLedger.WorkingObjective = strings.TrimSpace(working.Objective)
		}
		if toolRows, err := r.toolLifecycleStatusSnapshot(20); err != nil {
			return core.ChatStatusSnapshot{}, err
		} else {
			snapshot.ToolLifecycle = toolRows
		}
		capabilityRequests, capabilityGrants, err := r.capabilityStatusSnapshot(20)
		if err != nil {
			return core.ChatStatusSnapshot{}, err
		}
		snapshot.CapabilityRequests = capabilityRequests
		snapshot.CapabilityGrants = capabilityGrants
		snapshot.ExternalToolInvocationReadiness = r.externalToolInvocationReadinessStatusSnapshot(snapshot.ToolLifecycle, capabilityGrants)
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
		attachPendingItemDebugBreadcrumbs(snapshot.PendingItems)
		sortPendingItems(snapshot.PendingItems)
		snapshot.HotChats = buildHotChatRollups(snapshot)
		return snapshot, nil
	}

	recentEvents, err := r.store.ExecutionEventsRecent(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	snapshot.RecentExecution = summarizeExecutionEvents(recentEvents, 20)
	snapshot.RecentAdjudications = statusAdjudicationsFromExecutionEvents(recentEvents, 12)
	activeByChat, queueByChat := liveRouterSignalsFromExecutionEvents(recentEvents)
	latestFromEvents := latestTurnSnapshotsByChatFromExecutionEvents(recentEvents)
	tesStaleRunningTurns := staleRunningTurnSnapshotsFromExecutionEvents(latestFromEvents, now, snapshot.RestartHealth.StaleTurnThreshold)
	for _, stale := range tesStaleRunningTurns {
		delete(activeByChat, stale.ChatID)
	}
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
	for chatID, run := range latestFromEvents {
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
	for _, stale := range tesStaleRunningTurns {
		if staleTurnSnapshotCovered(snapshot.StaleRunningTurns, stale) {
			continue
		}
		snapshot.StaleRunningTurns = append(snapshot.StaleRunningTurns, stale)
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:          core.PendingItemKindStaleTurn,
			ChatID:        stale.ChatID,
			ID:            tesStaleTurnItemID(stale),
			Summary:       fmt.Sprintf("source=tes status=%s last_activity=%s", firstNonEmptyStatus(strings.TrimSpace(stale.Status), "running"), stale.LastActivityAt.UTC().Format(time.RFC3339)),
			Age:           statusAge(now, stale.LastActivityAt, stale.StartedAt),
			CreatedAt:     stale.StartedAt,
			UpdatedAt:     stale.LastActivityAt,
			Stale:         true,
			SourceClass:   "canonical",
			SourceSurface: "execution_events.turn",
		})
	}

	if health, err := r.store.MissionLedgerHealth(now); err != nil {
		return core.SystemStatusSnapshot{}, err
	} else {
		snapshot.MissionLedger = core.MissionLedgerStatusSnapshot{
			ActiveCount:                  health.ActiveCount,
			CandidateCount:               health.CandidateCount,
			PinnedCount:                  health.PinnedCount,
			RecurringCount:               health.RecurringCount,
			BlockedCount:                 health.BlockedCount,
			SelfContinuationEnabledCount: health.SelfContinuationEnabledCount,
			StaleCandidateCount:          health.StaleCandidateCount,
			PendingHandoffCount:          health.PendingHandoffCount,
		}
	}

	candidateMissions, err := r.store.Missions(session.MissionFilter{Status: session.MissionStatusCandidate, Limit: 20})
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, mission := range candidateMissions {
		updatedAt := coalesceTime(mission.UpdatedAt, mission.CreatedAt)
		snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
			Kind:          core.PendingItemKindMission,
			ChatID:        missionOwnerChatID(mission.Owner),
			ID:            strings.TrimSpace(mission.ID),
			Summary:       renderMissionPendingSummary(mission),
			Age:           statusAge(now, updatedAt, mission.CreatedAt),
			CreatedAt:     mission.CreatedAt,
			UpdatedAt:     updatedAt,
			SourceClass:   "operational_current_state_store",
			SourceSurface: "mission_ledger",
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
	attachPendingItemDebugBreadcrumbs(snapshot.PendingItems)
	sortPendingItems(snapshot.PendingItems)
	snapshot.HotChats = buildHotChatRollups(snapshot)
	if r.cfg != nil && r.cfg.Tailscale.Enabled {
		tailnetSnapshot, err := r.TailnetStatusSnapshot(context.Background())
		if err != nil {
			tailnetSnapshot = core.TailnetStatusSnapshot{
				GeneratedAt: time.Now().UTC(),
				Enabled:     true,
				Backend:     strings.TrimSpace(r.cfg.Tailscale.Backend),
				Status:      "degraded",
				Summary:     "Tailnet status snapshot failed.",
				Issues: []core.TailnetIssue{{
					Code:     "snapshot_failed",
					Severity: "error",
					Summary:  err.Error(),
				}},
			}
		}
		snapshot.Tailnet = &tailnetSnapshot
	}
	return snapshot, nil
}
