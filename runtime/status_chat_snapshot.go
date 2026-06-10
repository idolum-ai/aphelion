//go:build linux

package runtime

import (
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/tool"
	"strings"
	"time"
)

func (r *Runtime) ChatStatusSnapshot(chatID int64, router core.RouterStatusSnapshot) (core.ChatStatusSnapshot, error) {
	system, err := r.SystemStatusSnapshot(router)
	if err != nil {
		return core.ChatStatusSnapshot{}, err
	}

	key := session.SessionKey{ChatID: chatID, UserID: 0, Scope: telegramDMScopeRef(chatID)}
	snapshot := core.ChatStatusSnapshot{
		GeneratedAt:    system.GeneratedAt,
		ChatID:         chatID,
		SessionID:      session.SessionIDForKey(key),
		ScopeKind:      string(key.Scope.Kind),
		ScopeID:        key.Scope.ID,
		DurableAgentID: key.Scope.DurableAgentID,
		QueueDepth:     system.QueueDepthByChat[chatID],
		RestartHealth:  system.RestartHealth,
		Authority:      authorityStatusSnapshotForChat(system.Authority, chatID),
	}
	if ids := system.ActiveTurnsByChat[chatID]; len(ids) > 0 {
		snapshot.ActiveTurnIDs = append(snapshot.ActiveTurnIDs, ids...)
	}
	for _, item := range system.PendingItems {
		if item.ChatID != chatID {
			continue
		}
		if itemSessionID := strings.TrimSpace(item.SessionID); itemSessionID != "" && itemSessionID != snapshot.SessionID {
			continue
		}
		snapshot.PendingItems = append(snapshot.PendingItems, item)
	}
	for _, continuation := range system.Continuations {
		if continuation.ChatID != chatID {
			continue
		}
		if contSessionID := strings.TrimSpace(continuation.SessionID); contSessionID != "" && contSessionID != snapshot.SessionID {
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
		if latestPerception, ok := latestPerceptionBudgetForSessionFromExecutionEvents(events, key.ChatID); ok {
			latestPerception.SessionID = snapshot.SessionID
			latestPerception.ScopeKind = string(key.Scope.Kind)
			latestPerception.ScopeID = key.Scope.ID
			latestPerception.AgentID = key.Scope.DurableAgentID
			snapshot.LatestPerceptionBudget = &latestPerception
		}
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
			len(snapshot.OperationEvidence) == 0 ||
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
				if len(snapshot.OperationEvidence) == 0 {
					snapshot.OperationEvidence = operationEvidenceStatusFields(statusState.OperationState)
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

func (r *Runtime) ChatStatusSnapshotForKey(key session.SessionKey, router core.RouterStatusSnapshot) (core.ChatStatusSnapshot, error) {
	if key.ChatID == 0 {
		return core.ChatStatusSnapshot{}, fmt.Errorf("chat id is required")
	}
	key.Scope = session.NormalizeScopeRef(key.Scope)
	if key.Scope.IsZero() {
		key.Scope = telegramDMScopeRef(key.ChatID)
	}
	snapshot, err := r.ChatStatusSnapshot(key.ChatID, router)
	if err != nil {
		return core.ChatStatusSnapshot{}, err
	}
	sessionID := session.SessionIDForKey(key)
	snapshot.SessionID = sessionID
	snapshot.ScopeKind = string(key.Scope.Kind)
	snapshot.ScopeID = key.Scope.ID
	snapshot.DurableAgentID = key.Scope.DurableAgentID
	if r == nil || r.store == nil {
		return snapshot, nil
	}
	if latest, err := r.store.LatestTurnRun(key); err == nil && latest != nil {
		run := turnRunStatusSnapshotFromRun(*latest)
		snapshot.LatestTurnRun = &run
	}
	if state, exists, err := r.store.ContinuationStateIfExists(key); err != nil {
		return core.ChatStatusSnapshot{}, err
	} else if exists {
		cont := continuationStatusSnapshotFromState(key, state, time.Now().UTC(), "operational_current_state_store:continuation_state_json")
		snapshot.Continuation = &cont
		if key.Scope.Kind == session.ScopeKindTelegramThread && (state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved) {
			updatedAt := coalesceTime(state.UpdatedAt)
			snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
				Kind:           core.PendingItemKindContinuation,
				ChatID:         key.ChatID,
				SessionID:      sessionID,
				ScopeKind:      string(key.Scope.Kind),
				ScopeID:        key.Scope.ID,
				DurableAgentID: key.Scope.DurableAgentID,
				ID:             continuationItemID(state, key.ChatID),
				Summary:        renderContinuationSummary(state),
				Age:            statusAge(time.Now().UTC(), updatedAt, time.Time{}),
				UpdatedAt:      updatedAt,
				SourceClass:    "operational_current_state_store",
				SourceSurface:  "continuation_state_json",
			})
		}
	}
	if key.Scope.Kind == session.ScopeKindTelegramThread {
		pendingDecisions, err := r.store.PendingDecisions()
		if err != nil {
			return core.ChatStatusSnapshot{}, err
		}
		for _, pending := range pendingDecisions {
			if strings.TrimSpace(pending.SessionID) != sessionID {
				continue
			}
			age := statusAge(time.Now().UTC(), pending.UpdatedAt, pending.CreatedAt)
			timeout := time.Duration(pending.TimeoutNanos)
			snapshot.PendingItems = append(snapshot.PendingItems, core.PendingItem{
				Kind:           core.PendingItemKindDecision,
				ChatID:         pending.ChatID,
				SessionID:      strings.TrimSpace(pending.SessionID),
				ScopeKind:      strings.TrimSpace(pending.ScopeKind),
				ScopeID:        strings.TrimSpace(pending.ScopeID),
				DurableAgentID: strings.TrimSpace(pending.DurableAgentID),
				ID:             strings.TrimSpace(pending.ID),
				Summary:        renderDecisionSummary(pending),
				Age:            age,
				CreatedAt:      pending.CreatedAt,
				UpdatedAt:      pending.UpdatedAt,
				Stale:          timeout > 0 && age > timeout,
				SourceClass:    "operational_current_state_store",
				SourceSurface:  "pending_decisions",
			})
		}
	}
	events, eventsErr := r.store.ExecutionEventsBySession(key, 0, 500)
	if eventsErr != nil {
		return core.ChatStatusSnapshot{}, eventsErr
	}
	if len(events) > 0 {
		snapshot.RecentExecution = summarizeExecutionEvents(events, 12)
		snapshot.RecentAdjudications = statusAdjudicationsFromExecutionEvents(events, 6)
		if latestPerception, ok := latestPerceptionBudgetForSessionFromExecutionEvents(events, key.ChatID); ok {
			latestPerception.SessionID = snapshot.SessionID
			latestPerception.ScopeKind = string(key.Scope.Kind)
			latestPerception.ScopeID = key.Scope.ID
			latestPerception.AgentID = key.Scope.DurableAgentID
			snapshot.LatestPerceptionBudget = &latestPerception
		}
		if latestFromEvents, ok := latestTurnSnapshotForChatFromExecutionEvents(events, key.ChatID); ok {
			latestFromEvents.SessionID = sessionID
			latestFromEvents.ScopeKind = string(key.Scope.Kind)
			latestFromEvents.ScopeID = key.Scope.ID
			latestFromEvents.DurableAgentID = key.Scope.DurableAgentID
			copied := latestFromEvents
			snapshot.LatestTurnRun = &copied
		}
	}
	filteredPending := snapshot.PendingItems[:0]
	for _, item := range snapshot.PendingItems {
		itemSessionID := strings.TrimSpace(item.SessionID)
		if itemSessionID != "" && itemSessionID != sessionID {
			continue
		}
		if itemSessionID == "" && key.Scope.Kind == session.ScopeKindTelegramThread {
			continue
		}
		filteredPending = append(filteredPending, item)
	}
	snapshot.PendingItems = filteredPending
	return snapshot, nil
}

func turnRunStatusSnapshotFromRun(run session.TurnRun) core.TurnRunStatusSnapshot {
	scope := session.NormalizeScopeRef(run.Scope)
	return core.TurnRunStatusSnapshot{
		ID:                       run.ID,
		SessionID:                strings.TrimSpace(run.SessionID),
		ChatID:                   run.ChatID,
		ScopeKind:                string(scope.Kind),
		ScopeID:                  scope.ID,
		DurableAgentID:           scope.DurableAgentID,
		Kind:                     strings.TrimSpace(string(run.Kind)),
		TurnIndex:                run.TurnIndex,
		Status:                   strings.TrimSpace(string(run.Status)),
		RequestText:              truncateStatusDiagnostic(strings.TrimSpace(run.RequestText), 220),
		LastActivityAt:           run.LastActivityAt,
		ProgressMessageID:        run.ProgressMessageID,
		LastToolName:             strings.TrimSpace(run.LastToolName),
		LastToolPreview:          truncateStatusDiagnostic(strings.TrimSpace(run.LastToolPreview), 220),
		LastToolResultPreview:    truncateStatusDiagnostic(strings.TrimSpace(run.LastToolResultPreview), 220),
		LastToolError:            truncateStatusDiagnostic(strings.TrimSpace(run.LastToolError), 220),
		ErrorText:                truncateStatusDiagnostic(strings.TrimSpace(run.ErrorText), 220),
		TotalToolCharsIn:         run.TotalToolCharsIn,
		TotalAssistantCharsOut:   run.TotalAssistantCharsOut,
		ProviderInputTokens:      run.ProviderInputTokens,
		ProviderOutputTokens:     run.ProviderOutputTokens,
		ProviderCacheReadTokens:  run.ProviderCacheReadTokens,
		ProviderCacheWriteTokens: run.ProviderCacheWriteTokens,
		AssistantToolRatio:       assistantToolCharRatio(run.TotalAssistantCharsOut, run.TotalToolCharsIn),
		StartedAt:                run.StartedAt,
		Source:                   "operational_current_state_store:turn_runs",
	}
}

func assistantToolCharRatio(assistantChars int64, toolChars int64) float64 {
	if toolChars <= 0 {
		return 0
	}
	return float64(assistantChars) / float64(toolChars)
}

func continuationStatusSnapshotFromState(key session.SessionKey, state session.ContinuationState, updatedAt time.Time, source string) core.ContinuationStatusSnapshot {
	key.Scope = session.NormalizeScopeRef(key.Scope)
	state = session.NormalizeContinuationState(state)
	return core.ContinuationStatusSnapshot{
		SessionID:        session.SessionIDForKey(key),
		ChatID:           key.ChatID,
		ScopeKind:        string(key.Scope.Kind),
		ScopeID:          key.Scope.ID,
		DurableAgentID:   key.Scope.DurableAgentID,
		Status:           strings.TrimSpace(string(state.Status)),
		RemainingTurns:   state.RemainingTurns,
		DecisionID:       strings.TrimSpace(state.DecisionID),
		ApprovedBy:       state.ApprovedBy,
		PersonaIntent:    strings.TrimSpace(string(state.PersonaIntent.Decision)),
		GovernorIntent:   strings.TrimSpace(string(state.GovernorIntent.Decision)),
		GovernorRatified: state.GovernorIntent.Ratified,
		BlockedReason:    strings.TrimSpace(state.HandshakeBlockedReason),
		UpdatedAt:        coalesceTime(updatedAt, state.UpdatedAt),
		Source:           strings.TrimSpace(source),
	}
}

func authorityStatusSnapshotForChat(snapshot core.AuthorityStatusSnapshot, chatID int64) core.AuthorityStatusSnapshot {
	if chatID == 0 || len(snapshot.Findings) == 0 {
		return snapshot
	}
	out := snapshot
	out.Findings = make([]core.AuthorityFindingSnapshot, 0, len(snapshot.Findings))
	out.FindingCount = 0
	out.ErrorCount = 0
	out.WarningCount = 0
	for _, finding := range snapshot.Findings {
		if finding.ChatID != 0 && finding.ChatID != chatID {
			continue
		}
		out.Findings = append(out.Findings, finding)
		out.FindingCount++
		switch strings.ToLower(strings.TrimSpace(finding.Severity)) {
		case "error":
			out.ErrorCount++
		case "warning":
			out.WarningCount++
		}
	}
	out.Status = "healthy"
	if out.FindingCount > 0 || out.TruncatedCapabilitySet {
		out.Status = "needs_attention"
	}
	return out
}

func operationEvidenceStatusFields(state session.OperationState) []core.OperationEvidenceStatus {
	statuses := tool.OperationCompletionEvidenceStatus(state)
	if len(statuses) == 0 {
		return nil
	}
	out := make([]core.OperationEvidenceStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, core.OperationEvidenceStatus{
			PhaseID:        status.PhaseID,
			AuthorityClass: status.AuthorityClass,
			Status:         string(status.Status),
			EvidenceKind:   status.EvidenceKind,
			Satisfied:      status.Satisfied,
			Reason:         status.Reason,
			CompletedAt:    status.CompletedAt,
			WorkMode:       status.WorkMode,
			LeaseID:        status.LeaseID,
		})
	}
	return out
}
