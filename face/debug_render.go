//go:build linux

package face

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func RenderTelegramDebug(chat core.ChatStatusSnapshot, system *core.SystemStatusSnapshot, durables *core.DurableAgentsStatusSnapshot, personaEffort string, governorEffort string) string {
	sections := make([]string, 0, 6)

	sections = append(sections, RenderTelegramStatusChat(chat, personaEffort, governorEffort, false))
	sections = append(sections, renderTelegramDebugChatDetails(chat))

	if system != nil {
		sections = append(sections, RenderTelegramStatusSystem(*system, personaEffort, governorEffort))
		sections = append(sections, renderTelegramDebugSystemDetails(*system))
	}

	if durables != nil {
		sections = append(sections, RenderTelegramStatusDurables(*durables))
		sections = append(sections, renderTelegramDebugDurablesDetails(*durables))
	}

	trimmed := make([]string, 0, len(sections))
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}
		trimmed = append(trimmed, section)
	}
	return strings.Join(trimmed, "\n\n")
}

func renderTelegramDebugChatDetails(snapshot core.ChatStatusSnapshot) string {
	lines := []string{"debug_chat:"}
	latest := snapshot.LatestTurnRun
	if latest == nil {
		lines = append(lines, "latest_turn=none")
		lines = append(lines, renderAuthorityDebugBlock(snapshot.Authority, 8)...)
		lines = append(lines, renderExecutionTimelineBlock(snapshot.RecentExecution, 12)...)
		return strings.Join(lines, "\n")
	}

	line := fmt.Sprintf(
		"latest_turn id=%d status=%s kind=%s started_at=%s last_activity=%s",
		latest.ID,
		firstNonEmpty(strings.TrimSpace(latest.Status), "-"),
		firstNonEmpty(strings.TrimSpace(latest.Kind), "-"),
		formatStatusTime(latest.StartedAt),
		formatStatusTime(latest.LastActivityAt),
	)
	if latest.ProgressMessageID != 0 {
		line += fmt.Sprintf(" progress_message_id=%d", latest.ProgressMessageID)
	}
	lines = append(lines, line)

	if request := strings.TrimSpace(latest.RequestText); request != "" {
		lines = append(lines, "latest_request="+quoteStatusField(truncateStatusField(request, 220)))
	}
	if preview := strings.TrimSpace(latest.LastToolPreview); preview != "" {
		lines = append(lines, "last_tool_preview="+quoteStatusField(truncateStatusField(preview, 220)))
		if command := extractDebugExecCommand(preview); command != "" {
			lines = append(lines, "last_exec_command="+quoteStatusField(truncateStatusField(command, 220)))
		}
	}
	if result := strings.TrimSpace(latest.LastToolResultPreview); result != "" {
		lines = append(lines, "last_tool_result="+quoteStatusField(truncateStatusField(result, 220)))
	}
	if toolErr := strings.TrimSpace(latest.LastToolError); toolErr != "" {
		lines = append(lines, "last_tool_error="+quoteStatusField(truncateStatusField(toolErr, 220)))
	}
	if turnErr := strings.TrimSpace(latest.ErrorText); turnErr != "" {
		lines = append(lines, "turn_error="+quoteStatusField(truncateStatusField(turnErr, 220)))
	}

	if stale := len(snapshot.StaleRunningTurns); stale > 0 {
		lines = append(lines, fmt.Sprintf("stale_turns=%d", stale))
	}
	lines = append(lines, renderAuthorityDebugBlock(snapshot.Authority, 8)...)
	lines = append(lines, renderChatSourceAttributionBlock()...)
	lines = append(lines, renderExecutionTimelineBlock(snapshot.RecentExecution, 12)...)
	return strings.Join(lines, "\n")
}

func renderTelegramDebugSystemDetails(snapshot core.SystemStatusSnapshot) string {
	lines := []string{"debug_system:"}

	queueCount := 0
	decisionCount := 0
	continuationCount := 0
	reviewCount := 0
	recoveryCount := 0
	staleCount := 0
	for _, item := range snapshot.PendingItems {
		switch item.Kind {
		case core.PendingItemKindQueue:
			queueCount++
		case core.PendingItemKindDecision:
			decisionCount++
		case core.PendingItemKindContinuation:
			continuationCount++
		case core.PendingItemKindReview:
			reviewCount++
		case core.PendingItemKindRecovery:
			recoveryCount++
		case core.PendingItemKindStaleTurn:
			staleCount++
		}
	}
	lines = append(lines, fmt.Sprintf(
		"pending_counts queue=%d decision=%d continuation=%d review=%d recovery=%d stale_turn=%d",
		queueCount,
		decisionCount,
		continuationCount,
		reviewCount,
		recoveryCount,
		staleCount,
	))

	if len(snapshot.LatestTurnRunsByChat) == 0 {
		lines = append(lines, "latest_turns=none")
		lines = append(lines, renderAuthorityDebugBlock(snapshot.Authority, 20)...)
		lines = append(lines, renderSandboxReadinessBlock(snapshot.Sandbox)...)
		lines = append(lines, renderExecutionTimelineBlock(snapshot.RecentExecution, 20)...)
		return strings.Join(lines, "\n")
	}

	chatIDs := make([]int64, 0, len(snapshot.LatestTurnRunsByChat))
	for chatID := range snapshot.LatestTurnRunsByChat {
		chatIDs = append(chatIDs, chatID)
	}
	sort.Slice(chatIDs, func(i, j int) bool { return chatIDs[i] < chatIDs[j] })

	lines = append(lines, "latest_turns:")
	max := len(chatIDs)
	if max > 12 {
		max = 12
	}
	for i := 0; i < max; i++ {
		chatID := chatIDs[i]
		run := snapshot.LatestTurnRunsByChat[chatID]
		line := fmt.Sprintf(
			"- chat_id=%d status=%s kind=%s last_activity=%s",
			chatID,
			firstNonEmpty(strings.TrimSpace(run.Status), "-"),
			firstNonEmpty(strings.TrimSpace(run.Kind), "-"),
			formatStatusTime(run.LastActivityAt),
		)
		if tool := strings.TrimSpace(run.LastToolName); tool != "" {
			line += " last_tool=" + tool
		}
		if request := strings.TrimSpace(run.RequestText); request != "" {
			line += " request=" + quoteStatusField(truncateStatusField(request, 100))
		}
		if errText := firstNonEmpty(strings.TrimSpace(run.LastToolError), strings.TrimSpace(run.ErrorText)); errText != "" {
			line += " error=" + quoteStatusField(truncateStatusField(errText, 100))
		}
		lines = append(lines, line)
	}
	if len(chatIDs) > max {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(chatIDs)-max))
	}
	lines = append(lines, renderSystemSourceAttributionBlock()...)
	lines = append(lines, renderAuthorityDebugBlock(snapshot.Authority, 20)...)
	lines = append(lines, renderSandboxReadinessBlock(snapshot.Sandbox)...)
	lines = append(lines, renderExecutionTimelineBlock(snapshot.RecentExecution, 20)...)
	return strings.Join(lines, "\n")
}

func renderAuthorityDebugBlock(snapshot core.AuthorityStatusSnapshot, limit int) []string {
	lines := []string{"authority_projection:"}
	status := strings.TrimSpace(snapshot.Status)
	if status == "" {
		status = "healthy"
	}
	lines = append(lines, fmt.Sprintf("status=%s findings=%d errors=%d warnings=%d generated_at=%s", status, snapshot.FindingCount, snapshot.ErrorCount, snapshot.WarningCount, formatStatusTime(snapshot.GeneratedAt)))
	if len(snapshot.Findings) == 0 {
		lines = append(lines, "- none")
		return lines
	}
	max := len(snapshot.Findings)
	if limit > 0 && max > limit {
		max = limit
	}
	for i := 0; i < max; i++ {
		finding := snapshot.Findings[i]
		line := fmt.Sprintf("- code=%s severity=%s source=%s:%s", strings.TrimSpace(finding.Code), strings.TrimSpace(finding.Severity), strings.TrimSpace(finding.SourceKind), strings.TrimSpace(finding.SourceID))
		if finding.ChatID != 0 {
			line += fmt.Sprintf(" chat_id=%d", finding.ChatID)
		}
		if sessionID := strings.TrimSpace(finding.SessionID); sessionID != "" {
			line += " session_id=" + quoteStatusField(sessionID)
		}
		if repair := strings.TrimSpace(finding.RepairAction); repair != "" {
			line += " repair_action=" + repair
		}
		if finding.Repairable {
			line += " repairable=true"
		}
		if detail := strings.TrimSpace(finding.Detail); detail != "" {
			line += " detail=" + quoteStatusField(truncateStatusField(detail, 160))
		}
		if next := strings.TrimSpace(finding.NextRepairAction); next != "" {
			line += " next_repair=" + quoteStatusField(truncateStatusField(next, 160))
		}
		lines = append(lines, line)
	}
	if len(snapshot.Findings) > max {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(snapshot.Findings)-max))
	}
	return lines
}

func renderTelegramDebugDurablesDetails(_ core.DurableAgentsStatusSnapshot) string {
	lines := []string{"debug_durables:"}
	lines = append(lines, renderDurablesSourceAttributionBlock()...)
	return strings.Join(lines, "\n")
}

func renderExecutionTimelineBlock(events []core.ExecutionEventSummary, limit int) []string {
	lines := []string{"execution_timeline:"}
	if len(events) == 0 {
		lines = append(lines, "- none")
		return lines
	}
	max := len(events)
	if limit > 0 && max > limit {
		max = limit
	}
	for i := 0; i < max; i++ {
		event := events[i]
		line := fmt.Sprintf(
			"- at=%s type=%s stage=%s status=%s chat_id=%d",
			formatStatusTime(event.CreatedAt),
			firstNonEmpty(strings.TrimSpace(event.EventType), "-"),
			firstNonEmpty(strings.TrimSpace(event.Stage), "-"),
			firstNonEmpty(strings.TrimSpace(event.Status), "-"),
			event.ChatID,
		)
		if event.Seq > 0 {
			line += fmt.Sprintf(" seq=%d", event.Seq)
		}
		if scope := strings.TrimSpace(event.ScopeKind); scope != "" {
			line += " scope=" + scope
		}
		if agentID := strings.TrimSpace(event.AgentID); agentID != "" {
			line += " agent=" + agentID
		}
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
		}
		if isTransportLedgerDeliveryEvent(event.EventType) {
			line += " source_class=canonical"
			line += " source_surface=outbound_transport_ledger"
			line += " visibility=human_render_unknown"
		}
		lines = append(lines, line)
	}
	if len(events) > max {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(events)-max))
	}
	return lines
}

func renderChatSourceAttributionBlock() []string {
	return []string{
		"source_attribution_chat:",
		"- field=summary_state class=projection",
		"- field=latest_turn class=projection preferred=canonical:execution_events.turn fallback=compatibility_fallback:turn_runs",
		"- field=operation_plan_hidden_inputs class=projection preferred=canonical:execution_events.turn_sidecars fallback=operational_current_state_store:status_state_json",
		"- field=delivery class=projection preferred=canonical:execution_events.delivery fallback=operational_current_state_store:status_state_json note=transport_ledger_only",
	}
}

func renderSystemSourceAttributionBlock() []string {
	return []string{
		"source_attribution_system:",
		"- field=active_turns_queue_depth class=projection preferred=canonical:execution_events.router fallback=operational_current_state_store:router_runtime",
		"- field=latest_turns class=projection preferred=canonical:execution_events.turn fallback=compatibility_fallback:turn_runs",
		"- field=pending_decisions class=projection preferred=operational_current_state_store:pending_decisions fallback=canonical:execution_events.decision",
		"- field=pending_continuations class=projection preferred=operational_current_state_store:continuation_state_json fallback=canonical:execution_events.continuation",
		"- field=authority_projection class=projection preferred=typed_authority_records fallback=none",
	}
}

func renderDurablesSourceAttributionBlock() []string {
	return []string{
		"source_attribution_durables:",
		"- field=durable_identity class=canonical store=session.durable_agents",
		"- field=durable_runtime_posture class=operational_current_state_store preferred=session.durable_agent_state overlay=projection:tes_execution_events",
	}
}

func isTransportLedgerDeliveryEvent(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	return strings.HasPrefix(eventType, "delivery.")
}

func extractDebugExecCommand(preview string) string {
	preview = strings.TrimSpace(preview)
	if preview == "" || (!strings.HasPrefix(preview, "{") && !strings.HasPrefix(preview, "[")) {
		return ""
	}
	var payload struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(preview), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Command)
}
