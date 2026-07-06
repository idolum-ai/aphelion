//go:build linux

package face

import (
	"fmt"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func RenderTelegramStatusSystemOperatorCard(snapshot core.SystemStatusSnapshot, personaEffort string, governorEffort string) string {
	state := operatorSystemStatusState(snapshot)
	panel := OperatorPanel{
		Title:    "System Status",
		State:    state,
		Why:      operatorSystemStatusWhy(snapshot),
		Next:     operatorSystemStatusNext(snapshot, state),
		Details:  operatorSystemStatusDetails(snapshot, personaEffort, governorEffort),
		Evidence: []string{operatorSystemStatusEvidence(snapshot)},
	}
	return RenderCompactOperatorPanel(panel, OperatorPanelCompactOptions{DetailLimit: 10, EvidenceLimit: 1})
}

func RenderTelegramStatusHotChatsOperatorCard(snapshot core.SystemStatusSnapshot) string {
	state := "none"
	if len(snapshot.HotChats) > 0 {
		state = fmt.Sprintf("%d active or pending chat(s)", len(snapshot.HotChats))
	}
	panel := OperatorPanel{
		Title:    "Hot Chats",
		State:    state,
		Why:      "these chats have active work, queued work, or pending operator items",
		Next:     "open Find Chat to inspect one chat, or use /health trace for raw evidence",
		Details:  operatorHotChatDetails(snapshot),
		Evidence: []string{operatorSystemStatusEvidence(snapshot)},
	}
	return RenderCompactOperatorPanel(panel, OperatorPanelCompactOptions{DetailLimit: 10, EvidenceLimit: 1})
}

func RenderTelegramStatusFindChatOperatorCard(snapshot core.SystemStatusSnapshot) string {
	state := "ready"
	if len(snapshot.HotChats) == 0 {
		state = "none"
	}
	panel := OperatorPanel{
		Title:    "Find Chat",
		State:    state,
		Why:      "chat drilldown starts from chats with active work, queued work, or pending operator items",
		Next:     "choose a chat button below to inspect that chat's status",
		Details:  operatorFindChatDetails(snapshot),
		Evidence: []string{operatorSystemStatusEvidence(snapshot)},
	}
	return RenderCompactOperatorPanel(panel, OperatorPanelCompactOptions{DetailLimit: 12, EvidenceLimit: 1})
}

func RenderTelegramStatusDurablesOperatorCard(snapshot core.DurableAgentsStatusSnapshot) string {
	state := operatorDurablesStatusState(snapshot)
	panel := OperatorPanel{
		Title:    "Durable Agents",
		State:    state,
		Why:      "durable children need visible health, policy, approvals, wake, and enrollment posture",
		Next:     operatorDurablesStatusNext(snapshot),
		Details:  operatorDurableAgentDetails(snapshot),
		Evidence: []string{operatorDurablesStatusEvidence(snapshot)},
	}
	return RenderCompactOperatorPanel(panel, OperatorPanelCompactOptions{DetailLimit: 12, EvidenceLimit: 1})
}

func operatorSystemStatusState(snapshot core.SystemStatusSnapshot) string {
	switch {
	case len(snapshot.StaleRunningTurns) > 0:
		return "needs recovery"
	case len(snapshot.PendingItems) > 0:
		return "needs attention"
	case snapshot.ActiveTurnCount > 0:
		return "working"
	case len(snapshot.QueueDepthByChat) > 0:
		return "queued"
	default:
		return "idle"
	}
}

func operatorSystemStatusWhy(snapshot core.SystemStatusSnapshot) string {
	return fmt.Sprintf(
		"system projection has %d active turn(s), %d queued chat(s), and %d pending item(s)",
		snapshot.ActiveTurnCount,
		len(snapshot.QueueDepthByChat),
		len(snapshot.PendingItems),
	)
}

func operatorSystemStatusNext(snapshot core.SystemStatusSnapshot, state string) string {
	if snapshot.ReleaseNotice.Available {
		return "newer release known from local metadata; approve install/release/deploy separately before updating"
	}
	switch state {
	case "needs recovery":
		return "run /health diagnose for recovery guidance, or inspect /health trace"
	case "needs attention":
		return "open Hot Chats or Find Chat to inspect pending operator items"
	case "working", "queued":
		return "tap Refresh to re-check, or inspect Hot Chats for active lanes"
	default:
		return "send the next request; use /health trace when raw evidence is needed"
	}
}

func operatorSystemStatusDetails(snapshot core.SystemStatusSnapshot, personaEffort string, governorEffort string) []string {
	details := []string{
		fmt.Sprintf("active chats: %s", formatOperatorInt64List(snapshot.ActiveChatIDs, "none")),
		fmt.Sprintf("hot chats: %d", len(snapshot.HotChats)),
		fmt.Sprintf("pending items: %d", len(snapshot.PendingItems)),
		fmt.Sprintf("runtime: persona %s, governor %s", firstNonEmpty(strings.TrimSpace(personaEffort), "unknown"), firstNonEmpty(strings.TrimSpace(governorEffort), "unknown")),
	}
	if len(snapshot.QueueDepthByChat) > 0 {
		details = append(details, fmt.Sprintf("queued chats: %d", len(snapshot.QueueDepthByChat)))
	}
	if provider := operatorProviderHealthDetail(snapshot.ProviderHealth); provider != "" {
		details = append(details, provider)
	}
	if persistence := operatorPersistenceHealthDetail(snapshot.PersistenceHealth); persistence != "" {
		details = append(details, persistence)
	}
	if snapshot.ReleaseNotice.Available {
		details = append(details, fmt.Sprintf("update available: %s -> %s", firstNonEmpty(snapshot.ReleaseNotice.CurrentVersion, "unknown"), snapshot.ReleaseNotice.LatestVersion))
	}
	if authority := operatorAuthorityStatusDetail(snapshot.Authority); authority != "" {
		details = append(details, authority)
	}
	if len(snapshot.Sandbox.Issues) > 0 {
		details = append(details, fmt.Sprintf("sandbox readiness: %d issue(s)", len(snapshot.Sandbox.Issues)))
	}
	if len(snapshot.TelegramIngressUpdates) > 0 {
		details = append(details, fmt.Sprintf("telegram ingress: %d recent update row(s)", len(snapshot.TelegramIngressUpdates)))
	}
	if len(snapshot.TelegramIngress) > 0 {
		details = append(details, fmt.Sprintf("telegram ingress failures: %d recent failure(s)", len(snapshot.TelegramIngress)))
	}
	if tailnet := operatorTailnetStatusDetail(snapshot.Tailnet); tailnet != "" {
		details = append(details, tailnet)
	}
	if watchdog := operatorWatchdogStatusDetail(snapshot.RestartHealth); watchdog != "" {
		details = append(details, watchdog)
	}
	return details
}

func operatorSystemStatusEvidence(snapshot core.SystemStatusSnapshot) string {
	return fmt.Sprintf("as of %s; source: sessions + execution_events projection (TES-preferred); /health trace for raw evidence", formatStatusTime(snapshot.GeneratedAt))
}

func operatorHotChatDetails(snapshot core.SystemStatusSnapshot) []string {
	if len(snapshot.HotChats) == 0 {
		return []string{"no active or pending chats right now"}
	}
	limit := len(snapshot.HotChats)
	if limit > 12 {
		limit = 12
	}
	details := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		hot := snapshot.HotChats[i]
		line := fmt.Sprintf("chat %d: pending %d, active %d, queued %d", hot.ChatID, hot.PendingCount, hot.ActiveTurnCount, hot.QueueDepth)
		if status := strings.TrimSpace(hot.LatestStatus); status != "" {
			line += ", latest " + status
		}
		if !hot.LastActivityAt.IsZero() {
			line += ", last activity " + formatStatusTime(hot.LastActivityAt)
		}
		details = append(details, line)
	}
	return details
}

func operatorFindChatDetails(snapshot core.SystemStatusSnapshot) []string {
	if len(snapshot.HotChats) == 0 {
		return []string{"no active or pending chats found"}
	}
	limit := len(snapshot.HotChats)
	if limit > 12 {
		limit = 12
	}
	details := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		hot := snapshot.HotChats[i]
		details = append(details, fmt.Sprintf("chat %d: pending %d, queued %d", hot.ChatID, hot.PendingCount, hot.QueueDepth))
	}
	return details
}

func operatorDurablesStatusState(snapshot core.DurableAgentsStatusSnapshot) string {
	switch {
	case snapshot.DegradedAgents > 0:
		return fmt.Sprintf("%d degraded durable agent(s)", snapshot.DegradedAgents)
	case snapshot.TotalAgents == 0:
		return "none"
	case snapshot.ActiveAgents > 0:
		return fmt.Sprintf("%d active durable agent(s)", snapshot.ActiveAgents)
	default:
		return "idle"
	}
}

func operatorDurablesStatusNext(snapshot core.DurableAgentsStatusSnapshot) string {
	if snapshot.DegradedAgents > 0 {
		return "inspect the degraded child with /agents or run /health diagnose"
	}
	return "refresh after child wake, policy, approval, or Tailnet changes"
}

func operatorDurableAgentDetails(snapshot core.DurableAgentsStatusSnapshot) []string {
	if len(snapshot.Agents) == 0 {
		return []string{"agents: none"}
	}
	limit := len(snapshot.Agents)
	if limit > 12 {
		limit = 12
	}
	details := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		agent := snapshot.Agents[i]
		line := fmt.Sprintf(
			"%s: %s health, %s status, enrollment %s",
			firstNonEmpty(strings.TrimSpace(agent.AgentID), "agent"),
			firstNonEmpty(strings.TrimSpace(agent.Health), "ok"),
			firstNonEmpty(strings.TrimSpace(agent.Status), "active"),
			firstNonEmpty(strings.TrimSpace(agent.EnrollmentStatus), "none"),
		)
		if agent.PolicyVersion > 0 {
			line += fmt.Sprintf(", policy v%d", agent.PolicyVersion)
		}
		if blocked := strings.TrimSpace(agent.ChildRuntimeBlockedReason); blocked != "" {
			line += ", blocked: " + truncateStatusField(blocked, 100)
		}
		if applyErr := strings.TrimSpace(agent.LastApplyError); applyErr != "" {
			line += ", apply error: " + truncateStatusField(applyErr, 100)
		}
		details = append(details, line)
	}
	return details
}

func operatorDurablesStatusEvidence(snapshot core.DurableAgentsStatusSnapshot) string {
	return fmt.Sprintf("as of %s; source: durable-agent registry, policy state, runtime state, and TES projections", formatStatusTime(snapshot.GeneratedAt))
}

func operatorProviderHealthDetail(health core.ProviderHealthSnapshot) string {
	status := strings.TrimSpace(health.Status)
	if status == "" && health.GeneratedAt.IsZero() && health.RecentFailures == 0 && health.RecentRetries == 0 && health.RecentFailovers == 0 {
		return ""
	}
	if status == "" {
		status = "healthy"
	}
	if status == "healthy" && health.RecentFailures == 0 && health.RecentRetries == 0 && health.RecentFailovers == 0 && health.LastFailureAt.IsZero() &&
		(statusClassCurrentOrEmpty(health.StatusClass) && failureClassNoneOrEmpty(health.FailureClass) && retryPolicyNoneOrEmpty(health.RetryPolicy)) {
		return ""
	}
	line := fmt.Sprintf("provider health: %s, failures %d, retries %d, failovers %d", status, health.RecentFailures, health.RecentRetries, health.RecentFailovers)
	if reason := strings.TrimSpace(health.LastFailureReason); reason != "" {
		line += ", latest failure " + truncateStatusField(reason, 90)
	}
	if retry := strings.TrimSpace(health.RetryPolicy); retry != "" && retry != core.ReliabilityRetryNone {
		line += ", retry " + retry
	}
	if next := strings.TrimSpace(health.NextAction); next != "" && next != "none" {
		line += ", next " + truncateStatusField(next, 90)
	}
	return line
}

func operatorPersistenceHealthDetail(health core.PersistenceHealthSnapshot) string {
	status := strings.TrimSpace(health.Status)
	if status == "" && health.GeneratedAt.IsZero() && health.RecentSlow == 0 {
		return ""
	}
	if status == "" {
		status = "healthy"
	}
	if status == "healthy" && health.RecentSlow == 0 && health.LastEventAt.IsZero() &&
		(statusClassCurrentOrEmpty(health.StatusClass) && failureClassNoneOrEmpty(health.FailureClass) && retryPolicyNoneOrEmpty(health.RetryPolicy)) {
		return ""
	}
	line := fmt.Sprintf("persistence health: %s, slow writes %d", status, health.RecentSlow)
	if component := strings.TrimSpace(health.LastComponent); component != "" {
		line += ", latest " + truncateStatusField(component, 80)
	}
	if health.LastLatency > 0 {
		line += fmt.Sprintf(" %dms", health.LastLatency.Milliseconds())
	}
	if retry := strings.TrimSpace(health.RetryPolicy); retry != "" && retry != core.ReliabilityRetryNone {
		line += ", policy " + retry
	}
	if next := strings.TrimSpace(health.NextAction); next != "" && next != "none" {
		line += ", next " + truncateStatusField(next, 90)
	}
	return line
}

func statusClassCurrentOrEmpty(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == core.StatusClassCurrent
}

func failureClassNoneOrEmpty(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == core.ReliabilityFailureNone
}

func retryPolicyNoneOrEmpty(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || value == core.ReliabilityRetryNone
}

func operatorAuthorityStatusDetail(snapshot core.AuthorityStatusSnapshot) string {
	if strings.TrimSpace(snapshot.Status) == "" && snapshot.FindingCount == 0 {
		return ""
	}
	if strings.TrimSpace(snapshot.Status) == "healthy" && snapshot.FindingCount == 0 {
		return "authority: healthy"
	}
	return fmt.Sprintf("authority: %d finding(s), %d error(s), %d active approval(s); run 'aphelion authority repair --apply --finding <id>' to apply repairable fixes", snapshot.FindingCount, snapshot.ErrorCount, snapshot.CapabilityGrants)
}

func operatorTailnetStatusDetail(snapshot *core.TailnetStatusSnapshot) string {
	if snapshot == nil {
		return ""
	}
	line := fmt.Sprintf("tailnet: %s", firstNonEmpty(strings.TrimSpace(snapshot.Status), "unknown"))
	if host := firstNonEmpty(strings.TrimSpace(snapshot.DNSName), strings.TrimSpace(snapshot.HostName)); host != "" {
		line += ", node " + host
	}
	if len(snapshot.Issues) > 0 {
		line += fmt.Sprintf(", %d issue(s)", len(snapshot.Issues))
	}
	return line
}

func operatorWatchdogStatusDetail(snapshot core.RestartHealthSnapshot) string {
	if !snapshot.WatchdogEnabled && !snapshot.WatchdogTriggered && strings.TrimSpace(snapshot.LastWatchdogStatus) == "" {
		return ""
	}
	status := firstNonEmpty(strings.TrimSpace(snapshot.LastWatchdogStatus), "enabled")
	line := "watchdog: " + status
	if snapshot.LastWatchdogStaleCount > 0 || snapshot.LastWatchdogInterruptedCount > 0 {
		line += fmt.Sprintf(", stale %d, interrupted %d", snapshot.LastWatchdogStaleCount, snapshot.LastWatchdogInterruptedCount)
	}
	return line
}

func formatOperatorInt64List(values []int64, empty string) string {
	if len(values) == 0 {
		return empty
	}
	copied := append([]int64(nil), values...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	parts := make([]string, 0, len(copied))
	for _, value := range copied {
		parts = append(parts, fmt.Sprintf("%d", value))
	}
	return strings.Join(parts, ", ")
}
