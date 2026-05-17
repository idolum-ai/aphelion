//go:build linux

package face

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func RenderTelegramStatusChat(snapshot core.ChatStatusSnapshot, personaEffort string, governorEffort string, pendingOnly bool) string {
	lines := []string{
		fmt.Sprintf("status_scope=chat chat_id=%d generated_at=%s", snapshot.ChatID, formatStatusTime(snapshot.GeneratedAt)),
	}
	if pendingOnly {
		actionable, backlog := statusPendingItemCounts(snapshot.PendingItems)
		lines = append(lines, fmt.Sprintf("summary pending_items=%d action_items=%d backlog_items=%d", len(snapshot.PendingItems), actionable, backlog))
	} else {
		state := chatSummaryState(snapshot)
		actionable, backlog := statusPendingItemCounts(snapshot.PendingItems)
		lines = append(lines, fmt.Sprintf("summary state=%s active_turns=%d queue_depth=%d pending_items=%d action_items=%d backlog_items=%d", state, len(snapshot.ActiveTurnIDs), snapshot.QueueDepth, len(snapshot.PendingItems), actionable, backlog))
		if snapshot.LatestTurnRun != nil {
			latest := snapshot.LatestTurnRun
			latestLine := fmt.Sprintf("latest_turn status=%s kind=%s last_activity=%s", latest.Status, latest.Kind, formatStatusTime(latest.LastActivityAt))
			if source := strings.TrimSpace(latest.Source); source != "" {
				latestLine += " source=" + source
			}
			if latest.LastToolName != "" {
				latestLine += " last_tool=" + latest.LastToolName
			}
			if latest.ErrorText != "" {
				latestLine += " error=" + quoteStatusField(truncateStatusField(latest.ErrorText, 120))
			}
			lines = append(lines, latestLine)
		}
		if phaseLine := renderTurnPhaseLine(snapshot); phaseLine != "" {
			lines = append(lines, phaseLine)
		}
		if operationLine := renderOperationStatusLine(snapshot); operationLine != "" {
			lines = append(lines, operationLine)
		}
		if planLine := renderPlanStatusLine(snapshot); planLine != "" {
			lines = append(lines, planLine)
		}
		if planProgressLine := renderPlanProgressLine(snapshot); planProgressLine != "" {
			lines = append(lines, planProgressLine)
		}
		if missionLine := renderMissionLedgerStatusLine(snapshot.MissionLedger); missionLine != "" {
			lines = append(lines, missionLine)
		}
		if authorityLine := renderAuthorityStatusLine(snapshot.Authority); authorityLine != "" {
			lines = append(lines, authorityLine)
		}
		if hiddenInputLine := renderHiddenInputStatusLine(snapshot); hiddenInputLine != "" {
			lines = append(lines, hiddenInputLine)
		}
		if deliveryLine := renderDeliveryStatusLine(snapshot); deliveryLine != "" {
			lines = append(lines, deliveryLine)
		}
		if detachedLine := renderDetachedWorkLine(snapshot); detachedLine != "" {
			lines = append(lines, detachedLine)
		}
		if autoApprovalLine := renderAutoApprovalStatusLine(snapshot.AutoApproval); autoApprovalLine != "" {
			lines = append(lines, autoApprovalLine)
		}
		lines = append(lines, renderToolLifecycleCurrentStateBlock(snapshot.ToolLifecycle, 5)...)
		lines = append(lines, renderExternalToolInvocationReadinessBlock(snapshot.ExternalToolInvocationReadiness, 5)...)
		lines = append(lines, renderCapabilityRequestStateBlock(snapshot.CapabilityRequests, 5)...)
		lines = append(lines, renderCapabilityGrantStateBlock(snapshot.CapabilityGrants, 5)...)
		lines = append(lines, renderToolAuthorityLifecycleBlock(snapshot.RecentExecution, 3)...)
		lines = append(lines, renderCapabilityLifecycleBlock(snapshot.RecentExecution, 3)...)
		if snapshot.Continuation != nil {
			cont := snapshot.Continuation
			line := fmt.Sprintf("continuation status=%s remaining_turns=%d", cont.Status, cont.RemainingTurns)
			if source := strings.TrimSpace(cont.Source); source != "" {
				line += " source=" + source
			}
			if cont.DecisionID != "" {
				line += " decision_id=" + cont.DecisionID
			}
			if cont.ApprovedBy > 0 {
				line += fmt.Sprintf(" approved_by=%d", cont.ApprovedBy)
			}
			if cont.PersonaIntent != "" {
				line += " persona_intent=" + cont.PersonaIntent
			}
			if cont.GovernorIntent != "" {
				line += " governor_intent=" + cont.GovernorIntent
			}
			if cont.GovernorRatified {
				line += " governor_ratified=true"
			}
			if cont.BlockedReason != "" {
				line += " blocked_reason=" + cont.BlockedReason
			}
			lines = append(lines, line)
		}
		lines = append(lines, "current_signal="+chatCurrentSignal(snapshot, state))
		lines = append(lines, renderWatchdogHealthLine(snapshot.RestartHealth))
	}
	lines = append(lines, renderPendingItemBlock(snapshot.PendingItems, 12)...)
	lines = append(lines,
		fmt.Sprintf("effort persona=%s governor=%s", strings.TrimSpace(personaEffort), strings.TrimSpace(governorEffort)),
	)
	return strings.Join(lines, "\n")
}

func renderTurnPhaseLine(snapshot core.ChatStatusSnapshot) string {
	phase := strings.TrimSpace(snapshot.TurnPhase)
	if phase == "" {
		return ""
	}
	line := "turn_phase phase=" + phase
	if summary := strings.TrimSpace(snapshot.TurnPhaseSummary); summary != "" {
		line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
	}
	if !snapshot.TurnPhaseUpdatedAt.IsZero() {
		line += " updated_at=" + formatStatusTime(snapshot.TurnPhaseUpdatedAt)
	}
	return line
}

func renderOperationStatusLine(snapshot core.ChatStatusSnapshot) string {
	status := strings.TrimSpace(snapshot.OperationStatus)
	stage := strings.TrimSpace(snapshot.OperationStage)
	summary := strings.TrimSpace(snapshot.OperationSummary)
	if status == "" && stage == "" && summary == "" {
		return ""
	}
	line := "operation"
	if status != "" {
		line += " status=" + status
	}
	if stage != "" {
		line += " stage=" + stage
	}
	if summary != "" {
		line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
	}
	return line
}

func renderPlanStatusLine(snapshot core.ChatStatusSnapshot) string {
	step := strings.TrimSpace(snapshot.PlanStep)
	status := strings.TrimSpace(snapshot.PlanStepStatus)
	if step == "" && status == "" {
		return ""
	}
	line := "plan_step"
	if status != "" {
		line += " status=" + status
	}
	if step != "" {
		line += " step=" + quoteStatusField(truncateStatusField(step, 120))
	}
	return line
}

func renderPlanProgressLine(snapshot core.ChatStatusSnapshot) string {
	if snapshot.PlanTotalSteps <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"plan_progress completed=%d total=%d fully_executed=%t",
		snapshot.PlanCompletedSteps,
		snapshot.PlanTotalSteps,
		snapshot.PlanFullyExecuted,
	)
}

func renderMissionLedgerStatusLine(snapshot core.MissionLedgerStatusSnapshot) string {
	if snapshot.ActiveCount == 0 && snapshot.CandidateCount == 0 && snapshot.PinnedCount == 0 && snapshot.RecurringCount == 0 && snapshot.BlockedCount == 0 && snapshot.SelfContinuationEnabledCount == 0 && snapshot.StaleCandidateCount == 0 && snapshot.PendingHandoffCount == 0 && strings.TrimSpace(snapshot.WorkingObjective) == "" {
		return ""
	}
	line := fmt.Sprintf("mission_ledger active=%d candidates=%d pinned=%d recurring=%d blocked=%d self_continue=%d stale_candidates=%d pending_handoffs=%d", snapshot.ActiveCount, snapshot.CandidateCount, snapshot.PinnedCount, snapshot.RecurringCount, snapshot.BlockedCount, snapshot.SelfContinuationEnabledCount, snapshot.StaleCandidateCount, snapshot.PendingHandoffCount)
	if objective := strings.TrimSpace(snapshot.WorkingObjective); objective != "" {
		line += " working_objective=" + quoteStatusField(truncateStatusField(objective, 120))
	}
	return line
}

func renderAuthorityStatusLine(snapshot core.AuthorityStatusSnapshot) string {
	status := strings.TrimSpace(snapshot.Status)
	if status == "" && snapshot.GeneratedAt.IsZero() && snapshot.FindingCount == 0 {
		return ""
	}
	if status == "" {
		status = "healthy"
	}
	line := fmt.Sprintf("authority status=%s findings=%d errors=%d warnings=%d active_leases=%d active_plan_leases=%d active_grants=%d", status, snapshot.FindingCount, snapshot.ErrorCount, snapshot.WarningCount, snapshot.ActiveLeases, snapshot.ActivePlanLeases, snapshot.CapabilityGrants)
	if len(snapshot.Findings) > 0 {
		first := snapshot.Findings[0]
		line += " first_code=" + strings.TrimSpace(first.Code)
		if findingID := strings.TrimSpace(first.FindingID); findingID != "" {
			line += " first_finding_id=" + findingID
		}
		if first.ChatID != 0 {
			line += fmt.Sprintf(" first_chat_id=%d", first.ChatID)
		}
		if action := strings.TrimSpace(first.ApplyAction); action != "" {
			line += " apply_action=" + action
		}
		if scope := strings.TrimSpace(first.ApplyScope); scope != "" {
			line += " apply_scope=" + scope
		}
	}
	return line
}

func renderHiddenInputStatusLine(snapshot core.ChatStatusSnapshot) string {
	categories := snapshot.HiddenInputCategories
	summary := strings.TrimSpace(snapshot.HiddenInputSummary)
	if len(categories) == 0 && summary == "" {
		return ""
	}
	line := "hidden_inputs"
	if len(categories) > 0 {
		line += " categories=" + strings.Join(categories, ",")
	}
	if summary != "" {
		line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
	}
	return line
}

func renderDeliveryStatusLine(snapshot core.ChatStatusSnapshot) string {
	status := strings.TrimSpace(snapshot.DeliveryStatus)
	summary := strings.TrimSpace(snapshot.DeliverySummary)
	if status == "" && summary == "" {
		return ""
	}
	line := "delivery"
	if status != "" {
		line += " status=" + status
	}
	if summary != "" {
		line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
	}
	return line
}

func renderDetachedWorkLine(snapshot core.ChatStatusSnapshot) string {
	decisions := 0
	continuations := 0
	recoveries := 0
	reviews := 0
	for _, item := range snapshot.PendingItems {
		switch item.Kind {
		case core.PendingItemKindDecision:
			decisions++
		case core.PendingItemKindContinuation:
			continuations++
		case core.PendingItemKindReview:
			reviews++
		case core.PendingItemKindRecovery:
			recoveries++
		}
	}
	staleTurns := len(snapshot.StaleRunningTurns)
	if decisions == 0 && continuations == 0 && recoveries == 0 && reviews == 0 && staleTurns == 0 {
		return ""
	}
	return fmt.Sprintf(
		"detached_work decisions=%d continuations=%d recoveries=%d stale_turns=%d reviews=%d",
		decisions,
		continuations,
		recoveries,
		staleTurns,
		reviews,
	)
}

func renderAutoApprovalStatusLine(snapshot *core.AutoApprovalStatusSnapshot) string {
	if snapshot == nil || !snapshot.Active {
		return ""
	}
	line := "auto_approval status=active"
	if !snapshot.Usable && strings.TrimSpace(snapshot.BlockedReason) != "" {
		line += " usable=false blocked_reason=" + quoteStatusField(truncateStatusField(snapshot.BlockedReason, 120))
	}
	if scope := strings.TrimSpace(snapshot.Scope); scope != "" {
		line += " scope=" + scope
	}
	if !snapshot.ExpiresAt.IsZero() {
		line += " expires_at=" + formatStatusTime(snapshot.ExpiresAt)
	}
	if snapshot.MaxUses > 0 {
		line += fmt.Sprintf(" used=%d/%d", snapshot.UsedCount, snapshot.MaxUses)
	} else {
		line += fmt.Sprintf(" used=%d", snapshot.UsedCount)
	}
	if reason := strings.TrimSpace(snapshot.Reason); reason != "" {
		line += " reason=" + quoteStatusField(truncateStatusField(reason, 80))
	}
	return line
}

func chatSummaryState(snapshot core.ChatStatusSnapshot) string {
	if chatHasStaleWorkEvidence(snapshot) {
		return "needs_recovery"
	}
	if len(snapshot.ActiveTurnIDs) > 0 {
		return "working"
	}
	if chatHasFreshTurnPhase(snapshot) {
		return "working"
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.OperationStatus), "blocked") || hasBlockingPendingItem(snapshot.PendingItems) {
		return "blocked"
	}
	if latest := snapshot.LatestTurnRun; latest != nil && strings.EqualFold(strings.TrimSpace(latest.Status), "interrupted") {
		return "interrupted"
	}
	if snapshot.QueueDepth > 0 {
		return "queued"
	}
	if latest := snapshot.LatestTurnRun; latest != nil && strings.EqualFold(strings.TrimSpace(latest.Status), "failed") {
		return "failed"
	}
	return "idle"
}

func statusPendingItemCounts(items []core.PendingItem) (int, int) {
	actionable := 0
	backlog := 0
	for _, item := range items {
		if pendingItemIsBacklog(item) {
			backlog++
			continue
		}
		if pendingItemNeedsAttention(item) {
			actionable++
		}
	}
	return actionable, backlog
}

func pendingItemIsBacklog(item core.PendingItem) bool {
	return item.Kind == core.PendingItemKindMission
}

func pendingItemNeedsAttention(item core.PendingItem) bool {
	switch item.Kind {
	case core.PendingItemKindDecision,
		core.PendingItemKindContinuation,
		core.PendingItemKindReview,
		core.PendingItemKindRecovery,
		core.PendingItemKindStaleTurn:
		return true
	default:
		return false
	}
}

func chatHasStaleWorkEvidence(snapshot core.ChatStatusSnapshot) bool {
	if len(snapshot.StaleRunningTurns) > 0 {
		return true
	}
	if latest := snapshot.LatestTurnRun; latest != nil && strings.EqualFold(strings.TrimSpace(latest.Status), "running") && statusTimeIsStale(snapshot.GeneratedAt, latest.LastActivityAt, snapshot.RestartHealth.StaleTurnThreshold) {
		return true
	}
	if strings.TrimSpace(snapshot.TurnPhase) != "" && statusTimeIsStale(snapshot.GeneratedAt, snapshot.TurnPhaseUpdatedAt, snapshot.RestartHealth.StaleTurnThreshold) {
		return true
	}
	return false
}

func chatHasFreshTurnPhase(snapshot core.ChatStatusSnapshot) bool {
	if strings.TrimSpace(snapshot.TurnPhase) == "" {
		return false
	}
	return !statusTimeIsStale(snapshot.GeneratedAt, snapshot.TurnPhaseUpdatedAt, snapshot.RestartHealth.StaleTurnThreshold)
}

func statusTimeIsStale(generatedAt time.Time, activityAt time.Time, threshold time.Duration) bool {
	if generatedAt.IsZero() || activityAt.IsZero() || threshold <= 0 {
		return false
	}
	return generatedAt.Sub(activityAt) > threshold
}

func hasBlockingPendingItem(items []core.PendingItem) bool {
	for _, item := range items {
		switch item.Kind {
		case core.PendingItemKindDecision, core.PendingItemKindContinuation:
			return true
		}
	}
	return false
}

func chatCurrentSignal(snapshot core.ChatStatusSnapshot, state string) string {
	if state == "needs_recovery" {
		if len(snapshot.StaleRunningTurns) > 0 {
			return "recovery:stale_turn"
		}
		if latest := snapshot.LatestTurnRun; latest != nil && strings.EqualFold(strings.TrimSpace(latest.Status), "running") {
			return "recovery:stale_active_turn"
		}
		if strings.TrimSpace(snapshot.TurnPhase) != "" {
			return "recovery:stale_turn_phase"
		}
		return "recovery:stale_status"
	}
	if latest := snapshot.LatestTurnRun; latest != nil {
		kind := strings.TrimSpace(latest.Kind)
		if kind == "" {
			kind = "interactive"
		}
		status := strings.TrimSpace(latest.Status)
		if status == "" {
			status = "unknown"
		}
		if state == "working" {
			if tool := strings.TrimSpace(latest.LastToolName); tool != "" {
				return "tool:" + tool
			}
			return "turn:" + kind + ":" + status
		}
		if state == "interrupted" {
			return "turn:" + kind + ":interrupted"
		}
	}
	if state == "blocked" {
		opStatus := strings.TrimSpace(snapshot.OperationStatus)
		if opStatus != "" {
			opStage := strings.TrimSpace(snapshot.OperationStage)
			if opStage != "" {
				return "operation:" + opStatus + ":" + opStage
			}
			return "operation:" + opStatus
		}
		if hasBlockingPendingItem(snapshot.PendingItems) {
			return "awaiting_approval"
		}
	}
	if phase := strings.TrimSpace(snapshot.TurnPhase); phase != "" {
		return "phase:" + phase
	}
	if state == "queued" && snapshot.QueueDepth > 0 {
		return fmt.Sprintf("queue:%d", snapshot.QueueDepth)
	}
	if latest := snapshot.LatestTurnRun; latest != nil {
		kind := strings.TrimSpace(latest.Kind)
		if kind == "" {
			kind = "interactive"
		}
		status := strings.TrimSpace(latest.Status)
		if status == "" {
			status = "unknown"
		}
		return "turn:" + kind + ":" + status
	}
	if step := strings.TrimSpace(snapshot.PlanStep); step != "" {
		return "plan_step"
	}
	return state
}
