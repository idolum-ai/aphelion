//go:build linux

package face

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func RenderTelegramStatusChat(snapshot core.ChatStatusSnapshot, personaEffort string, governorEffort string, pendingOnly bool) string {
	lines := []string{
		fmt.Sprintf("status_scope=chat chat_id=%d generated_at=%s", snapshot.ChatID, formatStatusTime(snapshot.GeneratedAt)),
	}
	if pendingOnly {
		lines = append(lines, fmt.Sprintf("summary pending_items=%d", len(snapshot.PendingItems)))
	} else {
		state := chatSummaryState(snapshot)
		lines = append(lines, fmt.Sprintf("summary state=%s active_turns=%d queue_depth=%d pending_items=%d", state, len(snapshot.ActiveTurnIDs), snapshot.QueueDepth, len(snapshot.PendingItems)))
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
		if hiddenInputLine := renderHiddenInputStatusLine(snapshot); hiddenInputLine != "" {
			lines = append(lines, hiddenInputLine)
		}
		if deliveryLine := renderDeliveryStatusLine(snapshot); deliveryLine != "" {
			lines = append(lines, deliveryLine)
		}
		if detachedLine := renderDetachedWorkLine(snapshot); detachedLine != "" {
			lines = append(lines, detachedLine)
		}
		lines = append(lines, renderToolLifecycleCurrentStateBlock(snapshot.ToolLifecycle, 5)...)
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
		lines = append(lines, fmt.Sprintf("watchdog triggered=%t stale_threshold=%s stale_limit=%d", snapshot.RestartHealth.WatchdogTriggered, snapshot.RestartHealth.StaleTurnThreshold, snapshot.RestartHealth.StaleTurnLimit))
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

func chatSummaryState(snapshot core.ChatStatusSnapshot) string {
	if len(snapshot.ActiveTurnIDs) > 0 {
		return "working"
	}
	if strings.TrimSpace(snapshot.TurnPhase) != "" {
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

func RenderTelegramStatusSystem(snapshot core.SystemStatusSnapshot, personaEffort string, governorEffort string) string {
	lines := []string{
		fmt.Sprintf("status_scope=system generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		fmt.Sprintf("summary active_turns=%d active_chats=%d queued_chats=%d pending_items=%d continuations=%d stale_running=%d", snapshot.ActiveTurnCount, len(snapshot.ActiveChatIDs), len(snapshot.QueueDepthByChat), len(snapshot.PendingItems), len(snapshot.Continuations), len(snapshot.StaleRunningTurns)),
		fmt.Sprintf("active_chat_ids=%s", formatInt64List(snapshot.ActiveChatIDs)),
	}
	if len(snapshot.QueueDepthByChat) > 0 {
		queueKeys := make([]int64, 0, len(snapshot.QueueDepthByChat))
		for chatID := range snapshot.QueueDepthByChat {
			queueKeys = append(queueKeys, chatID)
		}
		sort.Slice(queueKeys, func(i, j int) bool { return queueKeys[i] < queueKeys[j] })
		for _, chatID := range queueKeys {
			lines = append(lines, fmt.Sprintf("queue chat_id=%d depth=%d", chatID, snapshot.QueueDepthByChat[chatID]))
		}
	}
	if len(snapshot.HotChats) > 0 {
		lines = append(lines, "hot_chats:")
		max := len(snapshot.HotChats)
		if max > 10 {
			max = 10
		}
		for i := 0; i < max; i++ {
			hot := snapshot.HotChats[i]
			line := fmt.Sprintf("- chat_id=%d pending=%d active_turns=%d queue_depth=%d", hot.ChatID, hot.PendingCount, hot.ActiveTurnCount, hot.QueueDepth)
			if hot.LatestStatus != "" {
				line += " latest=" + hot.LatestStatus
			}
			if !hot.LastActivityAt.IsZero() {
				line += " last_activity=" + formatStatusTime(hot.LastActivityAt)
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, renderToolAuthorityLifecycleBlock(snapshot.RecentExecution, 5)...)
	lines = append(lines, renderCapabilityLifecycleBlock(snapshot.RecentExecution, 5)...)
	lines = append(lines, renderPendingItemBlock(snapshot.PendingItems, 20)...)
	lines = append(lines, renderTailnetStatusBlock(snapshot.Tailnet)...)
	lines = append(lines, fmt.Sprintf("watchdog triggered=%t stale_threshold=%s stale_limit=%d", snapshot.RestartHealth.WatchdogTriggered, snapshot.RestartHealth.StaleTurnThreshold, snapshot.RestartHealth.StaleTurnLimit))
	lines = append(lines, fmt.Sprintf("effort persona=%s governor=%s", strings.TrimSpace(personaEffort), strings.TrimSpace(governorEffort)))
	return strings.Join(lines, "\n")
}

func renderTailnetStatusBlock(snapshot *core.TailnetStatusSnapshot) []string {
	if snapshot == nil {
		return nil
	}
	lines := []string{"tailnet:"}
	line := fmt.Sprintf("- status=%s enabled=%t backend=%s", firstNonEmpty(strings.TrimSpace(snapshot.Status), "unknown"), snapshot.Enabled, firstNonEmpty(strings.TrimSpace(snapshot.Backend), "-"))
	if host := firstNonEmpty(strings.TrimSpace(snapshot.DNSName), strings.TrimSpace(snapshot.HostName)); host != "" {
		line += " node=" + host
	}
	if tailnetName := strings.TrimSpace(snapshot.TailnetName); tailnetName != "" {
		line += " tailnet=" + tailnetName
	}
	if len(snapshot.TailscaleIPs) > 0 {
		line += " ips=" + formatStringList(snapshot.TailscaleIPs)
	}
	if len(snapshot.Tags) > 0 {
		line += " tags=" + formatStringList(snapshot.Tags)
	}
	lines = append(lines, line)
	if summary := strings.TrimSpace(snapshot.Summary); summary != "" {
		lines = append(lines, "  summary="+quoteStatusField(truncateStatusField(summary, 140)))
	}
	if snapshot.Parent != nil {
		parent := snapshot.Parent
		parentLine := fmt.Sprintf("  parent_tsnet enabled=%t running=%t", parent.Enabled, parent.Running)
		if host := strings.TrimSpace(parent.Hostname); host != "" {
			parentLine += " hostname=" + host
		}
		if listen := strings.TrimSpace(parent.ListenAddr); listen != "" {
			parentLine += " listen=" + listen
		}
		if magic := strings.TrimSpace(parent.MagicDNSURL); magic != "" {
			parentLine += " magic_url=" + magic
		}
		if errText := strings.TrimSpace(parent.LastError); errText != "" {
			parentLine += " error=" + quoteStatusField(truncateStatusField(errText, 120))
		}
		lines = append(lines, parentLine)
	}
	if len(snapshot.Surfaces) > 0 {
		lines = append(lines, fmt.Sprintf("  surfaces count=%d", len(snapshot.Surfaces)))
		limit := len(snapshot.Surfaces)
		if limit > 4 {
			limit = 4
		}
		for i := 0; i < limit; i++ {
			surface := snapshot.Surfaces[i]
			surfaceLine := fmt.Sprintf("  surface id=%s status=%s kind=%s name=%s", strings.TrimSpace(surface.SurfaceID), strings.TrimSpace(surface.Status), strings.TrimSpace(surface.SurfaceKind), strings.TrimSpace(surface.Name))
			if url := strings.TrimSpace(surface.URL); url != "" {
				surfaceLine += " url=" + url
			}
			if errText := strings.TrimSpace(surface.LastError); errText != "" {
				surfaceLine += " error=" + quoteStatusField(truncateStatusField(errText, 120))
			}
			lines = append(lines, surfaceLine)
		}
		if len(snapshot.Surfaces) > limit {
			lines = append(lines, fmt.Sprintf("  surfaces_omitted=%d", len(snapshot.Surfaces)-limit))
		}
	}
	limit := len(snapshot.Issues)
	if limit > 4 {
		limit = 4
	}
	for i := 0; i < limit; i++ {
		issue := snapshot.Issues[i]
		lines = append(lines, fmt.Sprintf("  issue code=%s severity=%s summary=%s", strings.TrimSpace(issue.Code), strings.TrimSpace(issue.Severity), quoteStatusField(truncateStatusField(issue.Summary, 120))))
	}
	if len(snapshot.Issues) > limit {
		lines = append(lines, fmt.Sprintf("  issues_omitted=%d", len(snapshot.Issues)-limit))
	}
	return lines
}

func renderToolLifecycleCurrentStateBlock(rows []core.ToolLifecycleStatusSnapshot, maxRows int) []string {
	if len(rows) == 0 {
		return nil
	}
	if maxRows <= 0 {
		maxRows = 5
	}
	lines := []string{"tool_lifecycle source=canonical:session.tool_install_records+tool_audit_records"}
	limit := len(rows)
	if limit > maxRows {
		limit = maxRows
	}
	for i := 0; i < limit; i++ {
		row := rows[i]
		line := fmt.Sprintf("- tool_name=%s install=%s probe=%s audit=%s", row.ToolName, firstNonEmpty(strings.TrimSpace(row.InstallStatus), "-"), firstNonEmpty(strings.TrimSpace(row.ProbeStatus), "-"), firstNonEmpty(strings.TrimSpace(row.AuditStatus), "-"))
		if ref := strings.TrimSpace(row.InstallRef); ref != "" {
			line += " install_ref=" + ref
		}
		if attest := strings.TrimSpace(row.AttestationStatus); attest != "" {
			line += " attestation=" + attest
		}
		if source := strings.TrimSpace(row.DriftSource); source != "" {
			line += " drift_source=" + source
		}
		if reason := strings.TrimSpace(row.StaleReason); reason != "" {
			line += " stale_reason=" + reason
		}
		failures := row.InstallFailures + row.ProbeFailures + row.AuditFailures
		if failures > 0 {
			line += fmt.Sprintf(" failures=install:%d,probe:%d,audit:%d", row.InstallFailures, row.ProbeFailures, row.AuditFailures)
		}
		if hash := shortFingerprint(row.ManifestHash); hash != "" {
			line += " manifest_hash=" + hash
		}
		if hash := shortFingerprint(row.WorkspaceFingerprint); hash != "" {
			line += " workspace_fingerprint=" + hash
		}
		if summary := strings.TrimSpace(row.TraceSummary); summary != "" {
			stage := firstNonEmpty(strings.TrimSpace(row.TraceStage), "-")
			line += " trace=" + stage + ":" + summary
			if row.TraceArtifactCount > 0 {
				line += " refs=" + strconv.Itoa(row.TraceArtifactCount)
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func renderCapabilityRequestStateBlock(rows []core.CapabilityRequestStatusSnapshot, maxRows int) []string {
	if len(rows) == 0 {
		return nil
	}
	if maxRows <= 0 {
		maxRows = 5
	}
	lines := []string{"capability_requests source=canonical:session.capability_requests"}
	limit := len(rows)
	if limit > maxRows {
		limit = maxRows
	}
	for i := 0; i < limit; i++ {
		row := rows[i]
		line := fmt.Sprintf("- request_id=%s kind=%s target_resource=%s status=%s requested_for=%s", row.RequestID, firstNonEmpty(row.Kind, "-"), firstNonEmpty(row.TargetResource, "-"), firstNonEmpty(row.ReviewStatus, "-"), firstNonEmpty(row.RequestedFor, "-"))
		if parent := strings.TrimSpace(row.ParentPrincipal); parent != "" {
			line += " parent_principal=" + parent
		}
		if risk := strings.TrimSpace(row.RiskClass); risk != "" {
			line += " risk_class=" + risk
		}
		if grantID := strings.TrimSpace(row.GrantID); grantID != "" {
			line += " grant_id=" + grantID
		}
		if purpose := strings.TrimSpace(row.Purpose); purpose != "" {
			line += " purpose=" + quoteStatusField(truncateStatusField(purpose, 120))
		}
		lines = append(lines, line)
	}
	return lines
}

func renderCapabilityGrantStateBlock(rows []core.CapabilityGrantStatusSnapshot, maxRows int) []string {
	if len(rows) == 0 {
		return nil
	}
	if maxRows <= 0 {
		maxRows = 5
	}
	lines := []string{"capability_grants source=canonical:session.capability_grants"}
	limit := len(rows)
	if limit > maxRows {
		limit = maxRows
	}
	for i := 0; i < limit; i++ {
		row := rows[i]
		line := fmt.Sprintf("- grant_id=%s kind=%s target_resource=%s status=%s granted_to=%s actions=%s", row.GrantID, firstNonEmpty(row.Kind, "-"), firstNonEmpty(row.TargetResource, "-"), firstNonEmpty(row.Status, "-"), firstNonEmpty(row.GrantedTo, "-"), firstNonEmpty(strings.Join(row.AllowedActions, ","), "-"))
		if requestID := strings.TrimSpace(row.RequestID); requestID != "" {
			line += " request_id=" + requestID
		}
		if source := strings.TrimSpace(row.DriftSource); source != "" {
			line += " drift_source=" + source
		}
		if reason := strings.TrimSpace(row.StaleReason); reason != "" {
			line += " stale_reason=" + quoteStatusField(truncateStatusField(reason, 120))
		}
		if fingerprint := shortFingerprint(row.AnchorFingerprint); fingerprint != "" {
			line += " anchor=" + fingerprint
		}
		if row.InvocationCount > 0 || row.FailureCount > 0 {
			line += fmt.Sprintf(" counters=invocations:%d,failures:%d", row.InvocationCount, row.FailureCount)
		}
		if !row.ExpiresAt.IsZero() {
			line += " expires_at=" + formatStatusTime(row.ExpiresAt)
		}
		lines = append(lines, line)
	}
	return lines
}

func renderToolAuthorityLifecycleBlock(events []core.ExecutionEventSummary, maxPerClass int) []string {
	if len(events) == 0 {
		return nil
	}
	if maxPerClass <= 0 {
		maxPerClass = 3
	}
	registrations := make([]core.ExecutionEventSummary, 0, maxPerClass)
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventToolRegistered:
			if len(registrations) < maxPerClass {
				registrations = append(registrations, event)
			}
		}
		if len(registrations) >= maxPerClass {
			break
		}
	}
	if len(registrations) == 0 {
		return nil
	}

	lines := []string{"tool_authority_lifecycle source=canonical:execution_events.tool_authority"}
	if len(registrations) > 0 {
		lines = append(lines, "tool_registrations:")
		lines = append(lines, renderToolAuthorityEntries(registrations)...)
	}
	return lines
}

func renderCapabilityLifecycleBlock(events []core.ExecutionEventSummary, maxRows int) []string {
	if len(events) == 0 {
		return nil
	}
	if maxRows <= 0 {
		maxRows = 3
	}
	rows := make([]core.ExecutionEventSummary, 0, maxRows)
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventCapabilityRequestCreated,
			core.ExecutionEventCapabilityReviewed,
			core.ExecutionEventCapabilityGrantChanged,
			core.ExecutionEventCapabilityInvocation:
			rows = append(rows, event)
		}
		if len(rows) >= maxRows {
			break
		}
	}
	if len(rows) == 0 {
		return nil
	}
	lines := []string{"capability_lifecycle source=canonical:execution_events.capability_delegation"}
	lines = append(lines, renderToolAuthorityEntries(rows)...)
	return lines
}

func renderToolAuthorityEntries(events []core.ExecutionEventSummary) []string {
	lines := make([]string, 0, len(events))
	for _, event := range events {
		line := fmt.Sprintf(
			"- event=%s status=%s at=%s",
			strings.TrimSpace(event.EventType),
			firstNonEmpty(strings.TrimSpace(event.Status), "-"),
			formatStatusTime(event.CreatedAt),
		)
		if summary := strings.TrimSpace(event.Summary); summary != "" {
			line += " details=" + quoteStatusField(truncateStatusField(summary, 140))
		}
		lines = append(lines, line)
	}
	return lines
}

func RenderTelegramStatusHotChats(snapshot core.SystemStatusSnapshot) string {
	lines := []string{
		fmt.Sprintf("status_scope=hot_chats generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		fmt.Sprintf("summary hot_chats=%d", len(snapshot.HotChats)),
	}
	if len(snapshot.HotChats) == 0 {
		lines = append(lines, "No active or pending chats right now.")
		return strings.Join(lines, "\n")
	}
	max := len(snapshot.HotChats)
	if max > 30 {
		max = 30
	}
	for i := 0; i < max; i++ {
		hot := snapshot.HotChats[i]
		line := fmt.Sprintf("%d. chat_id=%d pending=%d active_turns=%d queue_depth=%d", i+1, hot.ChatID, hot.PendingCount, hot.ActiveTurnCount, hot.QueueDepth)
		if hot.LatestStatus != "" {
			line += " latest=" + hot.LatestStatus
		}
		if !hot.LastActivityAt.IsZero() {
			line += " last_activity=" + formatStatusTime(hot.LastActivityAt)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func RenderTelegramStatusFindChat(snapshot core.SystemStatusSnapshot) string {
	lines := []string{
		fmt.Sprintf("status_scope=find_chat generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		"Select a chat below to drill into scoped status.",
	}
	if len(snapshot.HotChats) == 0 {
		lines = append(lines, "No active or pending chats found.")
		return strings.Join(lines, "\n")
	}
	max := len(snapshot.HotChats)
	if max > 12 {
		max = 12
	}
	for i := 0; i < max; i++ {
		hot := snapshot.HotChats[i]
		lines = append(lines, fmt.Sprintf("%d. chat_id=%d pending=%d queue_depth=%d", i+1, hot.ChatID, hot.PendingCount, hot.QueueDepth))
	}
	return strings.Join(lines, "\n")
}

func RenderTelegramStatusDurables(snapshot core.DurableAgentsStatusSnapshot) string {
	lines := []string{
		fmt.Sprintf("status_scope=durables generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		fmt.Sprintf(
			"summary total=%d active=%d dormant=%d degraded=%d inactive=%d",
			snapshot.TotalAgents,
			snapshot.ActiveAgents,
			snapshot.DormantAgents,
			snapshot.DegradedAgents,
			snapshot.InactiveAgents,
		),
	}
	if len(snapshot.Agents) == 0 {
		lines = append(lines, "agents:")
		lines = append(lines, "- none")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "agents:")
	max := len(snapshot.Agents)
	if max > 20 {
		max = 20
	}
	for i := 0; i < max; i++ {
		agent := snapshot.Agents[i]
		lines = append(lines, fmt.Sprintf(
			"- id=%s channel=%s status=%s health=%s review_chat=%d",
			strings.TrimSpace(agent.AgentID),
			strings.TrimSpace(agent.ChannelKind),
			firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			firstNonEmpty(strings.TrimSpace(agent.Health), "ok"),
			agent.ReviewTargetChatID,
		))
		lines = append(lines, fmt.Sprintf(
			"  policy version=%d hash=%s outbound=%s drift=%s capabilities=%s",
			agent.PolicyVersion,
			formatStatusHash(agent.PolicyHash),
			firstNonEmpty(strings.TrimSpace(agent.PolicyOutboundMode), "-"),
			firstNonEmpty(strings.TrimSpace(agent.PolicyDrift), "-"),
			formatStringList(agent.CapabilityEnvelope),
		))
		lines = append(lines, fmt.Sprintf(
			"  runtime last_wake=%s last_review=%s dormant_at=%s apply_status=%s applied_version=%d applied_at=%s",
			formatStatusTime(agent.LastWakeAt),
			formatStatusTime(agent.LastReviewAt),
			formatStatusTime(agent.DormantAt),
			firstNonEmpty(strings.TrimSpace(agent.LastApplyStatus), "-"),
			agent.LastAppliedPolicyVersion,
			formatStatusTime(agent.LastAppliedPolicyAt),
		))
		lines = append(lines, fmt.Sprintf(
			"  authority principal=%s child_runtime_grants=%d profile_manifest=%s profile_policy_hash=%s profile_files=%d substrate=%s",
			firstNonEmpty(strings.TrimSpace(agent.CanonicalPrincipal), "-"),
			agent.ChildRuntimeGrantCount,
			firstNonEmpty(strings.TrimSpace(agent.ProfileManifestStatus), "-"),
			formatStatusHash(agent.ProfileManifestPolicyHash),
			agent.ProfileManifestFileCount,
			formatStringList(agent.SubstrateLabels),
		))
		if blocked := strings.TrimSpace(agent.ChildRuntimeBlockedReason); blocked != "" {
			line := "  repair child_runtime_blocked=" + quoteStatusField(truncateStatusField(blocked, 120))
			if hint := strings.TrimSpace(agent.ChildRuntimeRepairHint); hint != "" {
				line += " hint=" + quoteStatusField(truncateStatusField(hint, 120))
			}
			lines = append(lines, line)
		}
		if applyErr := strings.TrimSpace(agent.LastApplyError); applyErr != "" {
			lines = append(lines, "  runtime apply_error="+quoteStatusField(truncateStatusField(applyErr, 120)))
		}
		lines = append(lines, fmt.Sprintf(
			"  enrollment status=%s last_seen=%s last_seq=%d revoked_at=%s",
			firstNonEmpty(strings.TrimSpace(agent.EnrollmentStatus), "none"),
			formatStatusTime(agent.EnrollmentLastSeenAt),
			agent.EnrollmentLastSequence,
			formatStatusTime(agent.EnrollmentRevokedAt),
		))
		lines = append(lines, fmt.Sprintf(
			"  sources identity=%s runtime_posture=%s",
			firstNonEmpty(strings.TrimSpace(agent.IdentitySource), "-"),
			firstNonEmpty(strings.TrimSpace(agent.RuntimePostureSource), "-"),
		))
	}
	if len(snapshot.Agents) > max {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(snapshot.Agents)-max))
	}
	return strings.Join(lines, "\n")
}

func renderPendingItemBlock(items []core.PendingItem, max int) []string {
	lines := []string{"pending_items:"}
	if len(items) == 0 {
		lines = append(lines, "- none")
		return lines
	}
	if max <= 0 || max > len(items) {
		max = len(items)
	}
	for i := 0; i < max; i++ {
		item := items[i]
		line := fmt.Sprintf("- kind=%s chat_id=%d", item.Kind, item.ChatID)
		if id := strings.TrimSpace(item.ID); id != "" {
			line += " id=" + id
		}
		if item.Age > 0 {
			line += " age=" + item.Age.Truncate(time.Second).String()
		}
		if item.Stale {
			line += " stale=true"
		}
		if sourceClass := strings.TrimSpace(item.SourceClass); sourceClass != "" {
			line += " source_class=" + sourceClass
		}
		if sourceSurface := strings.TrimSpace(item.SourceSurface); sourceSurface != "" {
			line += " source_surface=" + sourceSurface
		}
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
		}
		lines = append(lines, line)
	}
	if len(items) > max {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(items)-max))
	}
	return lines
}

func formatInt64List(values []int64) string {
	if len(values) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatInt(value, 10))
	}
	return strings.Join(parts, ",")
}

func formatStringList(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		trimmed = append(trimmed, value)
	}
	if len(trimmed) == 0 {
		return "-"
	}
	return strings.Join(trimmed, ",")
}

func formatStatusHash(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "-"
	}
	if len(raw) <= 12 {
		return raw
	}
	return raw[:12]
}

func shortFingerprint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if len(raw) <= 19 {
		return raw
	}
	if strings.HasPrefix(raw, "sha256:") {
		return raw[:19]
	}
	return raw[:12]
}

func formatStatusTime(ts time.Time) string {
	if ts.IsZero() {
		return "-"
	}
	return ts.UTC().Format(time.RFC3339)
}

func quoteStatusField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "\"\""
	}
	value = strings.ReplaceAll(value, `"`, `'`)
	return `"` + value + `"`
}

func truncateStatusField(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
