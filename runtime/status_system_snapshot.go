//go:build linux

package runtime

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
		Autonomy:             r.AutonomyStatusSnapshot(),
		Sandbox:              r.sandboxReadinessSnapshot(now),
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

	authority, err := r.AuthorityStatusSnapshot(now)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	snapshot.Authority = authority
	telegramUpdates, err := r.store.RecentTelegramIngressUpdates(8)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, update := range telegramUpdates {
		snapshot.TelegramIngressUpdates = append(snapshot.TelegramIngressUpdates, core.TelegramIngressUpdateSnapshot{
			Surface:     strings.TrimSpace(update.Surface),
			UpdateID:    update.UpdateID,
			UpdateKind:  strings.TrimSpace(update.UpdateKind),
			ChatID:      update.ChatID,
			MessageID:   update.MessageID,
			SessionID:   strings.TrimSpace(update.SessionID),
			Status:      strings.TrimSpace(string(update.Status)),
			TurnRunID:   update.TurnRunID,
			ErrorText:   strings.TrimSpace(update.ErrorText),
			AcceptedAt:  update.AcceptedAt,
			QueuedAt:    update.QueuedAt,
			StartedAt:   update.StartedAt,
			CompletedAt: update.CompletedAt,
			UpdatedAt:   update.UpdatedAt,
		})
	}
	telegramFailures, err := r.store.RecentTelegramIngressFailures(5)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	for _, failure := range telegramFailures {
		snapshot.TelegramIngress = append(snapshot.TelegramIngress, core.TelegramIngressFailureSnapshot{
			Surface:    strings.TrimSpace(failure.Surface),
			UpdateID:   failure.UpdateID,
			UpdateKind: strings.TrimSpace(failure.UpdateKind),
			ChatID:     failure.ChatID,
			SenderID:   failure.SenderID,
			MessageID:  failure.MessageID,
			ErrorText:  strings.TrimSpace(failure.ErrorText),
			CreatedAt:  failure.CreatedAt,
		})
	}

	recentEvents, err := r.store.ExecutionEventsRecent(500)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	snapshot.RecentExecution = summarizeExecutionEvents(recentEvents, 20)
	snapshot.RestartHealth = restartHealthWithLatestWatchdogEvent(snapshot.RestartHealth, recentEvents)
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
			Kind:           core.PendingItemKindDecision,
			ChatID:         pending.ChatID,
			SessionID:      strings.TrimSpace(pending.SessionID),
			ScopeKind:      strings.TrimSpace(pending.ScopeKind),
			ScopeID:        strings.TrimSpace(pending.ScopeID),
			DurableAgentID: strings.TrimSpace(pending.DurableAgentID),
			ID:             decisionID,
			Summary:        renderDecisionSummary(pending),
			Age:            age,
			CreatedAt:      pending.CreatedAt,
			UpdatedAt:      pending.UpdatedAt,
			Stale:          stale,
			SourceClass:    "operational_current_state_store",
			SourceSurface:  "pending_decisions",
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
			operationalSnapshot := continuationStatusSnapshotFromState(row.Key, state, row.UpdatedAt, "operational_current_state_store:continuation_state_json")
			snapshot.Continuations = append(snapshot.Continuations, operationalSnapshot)
			continuationChatSeen[chatID] = struct{}{}
			if state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved {
				updatedAt := coalesceTime(row.UpdatedAt, state.UpdatedAt)
				snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
					Kind:           core.PendingItemKindContinuation,
					ChatID:         chatID,
					SessionID:      session.SessionIDForKey(row.Key),
					ScopeKind:      string(session.NormalizeScopeRef(row.Key.Scope).Kind),
					ScopeID:        session.NormalizeScopeRef(row.Key.Scope).ID,
					DurableAgentID: session.NormalizeScopeRef(row.Key.Scope).DurableAgentID,
					ID:             continuationItemID(state, chatID),
					Summary:        renderContinuationSummary(state),
					Age:            statusAge(now, updatedAt, time.Time{}),
					UpdatedAt:      updatedAt,
					SourceClass:    "operational_current_state_store",
					SourceSurface:  "continuation_state_json",
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

	recoveryPending, recoveryPendingOK, err := r.recoveryPendingFromEvents(now.Add(-7*24*time.Hour), 2000)
	if err != nil {
		return core.SystemStatusSnapshot{}, err
	}
	if recoveryPendingOK {
		snapshot.PendingItems = append(snapshot.PendingItems, recoveryPending)
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
