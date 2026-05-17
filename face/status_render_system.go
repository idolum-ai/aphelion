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

func RenderTelegramStatusSystem(snapshot core.SystemStatusSnapshot, personaEffort string, governorEffort string) string {
	lines := []string{
		fmt.Sprintf("status_scope=system generated_at=%s", formatStatusTime(snapshot.GeneratedAt)),
		fmt.Sprintf("summary active_turns=%d active_chats=%d queued_chats=%d pending_items=%d continuations=%d stale_running=%d", snapshot.ActiveTurnCount, len(snapshot.ActiveChatIDs), len(snapshot.QueueDepthByChat), len(snapshot.PendingItems), len(snapshot.Continuations), len(snapshot.StaleRunningTurns)),
		fmt.Sprintf("active_chat_ids=%s", formatInt64List(snapshot.ActiveChatIDs)),
	}
	if authorityLine := renderAuthorityStatusLine(snapshot.Authority); authorityLine != "" {
		lines = append(lines, authorityLine)
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
	lines = append(lines, renderAutonomyStatusBlock(snapshot.Autonomy)...)
	lines = append(lines, renderSandboxReadinessBlock(snapshot.Sandbox)...)
	lines = append(lines, renderTelegramIngressUpdateBlock(snapshot.TelegramIngressUpdates)...)
	lines = append(lines, renderTelegramIngressFailureBlock(snapshot.TelegramIngress)...)
	lines = append(lines, renderTailnetStatusBlock(snapshot.Tailnet)...)
	lines = append(lines, renderWatchdogHealthLine(snapshot.RestartHealth))
	lines = append(lines, fmt.Sprintf("effort persona=%s governor=%s", strings.TrimSpace(personaEffort), strings.TrimSpace(governorEffort)))
	return strings.Join(lines, "\n")
}

func renderTelegramIngressUpdateBlock(updates []core.TelegramIngressUpdateSnapshot) []string {
	if len(updates) == 0 {
		return nil
	}
	lines := []string{"telegram_ingress_updates:"}
	limit := len(updates)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		update := updates[i]
		line := fmt.Sprintf(
			"- surface=%s update_id=%d kind=%s status=%s chat_id=%d message_id=%d",
			firstNonEmpty(strings.TrimSpace(update.Surface), "-"),
			update.UpdateID,
			firstNonEmpty(strings.TrimSpace(update.UpdateKind), "-"),
			firstNonEmpty(strings.TrimSpace(update.Status), "-"),
			update.ChatID,
			update.MessageID,
		)
		if update.TurnRunID > 0 {
			line += " turn_run_id=" + strconv.FormatInt(update.TurnRunID, 10)
		}
		if !update.UpdatedAt.IsZero() {
			line += " updated_at=" + formatStatusTime(update.UpdatedAt)
		}
		if errText := strings.TrimSpace(update.ErrorText); errText != "" {
			line += " error=" + quoteStatusField(truncateStatusField(errText, 120))
		}
		lines = append(lines, line)
	}
	if len(updates) > limit {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(updates)-limit))
	}
	return lines
}

func renderTelegramIngressFailureBlock(failures []core.TelegramIngressFailureSnapshot) []string {
	if len(failures) == 0 {
		return nil
	}
	lines := []string{"telegram_ingress_failures:"}
	limit := len(failures)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		failure := failures[i]
		line := fmt.Sprintf(
			"- surface=%s update_id=%d kind=%s chat_id=%d message_id=%d",
			firstNonEmpty(strings.TrimSpace(failure.Surface), "-"),
			failure.UpdateID,
			firstNonEmpty(strings.TrimSpace(failure.UpdateKind), "-"),
			failure.ChatID,
			failure.MessageID,
		)
		if !failure.CreatedAt.IsZero() {
			line += " at=" + formatStatusTime(failure.CreatedAt)
		}
		if errText := strings.TrimSpace(failure.ErrorText); errText != "" {
			line += " error=" + quoteStatusField(truncateStatusField(errText, 120))
		}
		lines = append(lines, line)
	}
	if len(failures) > limit {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(failures)-limit))
	}
	return lines
}

func renderAutonomyStatusBlock(snapshot core.AutonomyStatusSnapshot) []string {
	if strings.TrimSpace(snapshot.DefaultMode) == "" && strings.TrimSpace(snapshot.Ceiling) == "" {
		return nil
	}
	duration := snapshot.MaxOverrideDuration
	if duration < 0 {
		duration = 0
	}
	line := fmt.Sprintf(
		"- default=%s ceiling=%s live_overrides=%t max_override=%s",
		firstNonEmpty(strings.TrimSpace(snapshot.DefaultMode), "ask_first"),
		firstNonEmpty(strings.TrimSpace(snapshot.Ceiling), "ask_first"),
		snapshot.AllowLiveOverrides,
		duration.Truncate(time.Second).String(),
	)
	if source := strings.TrimSpace(snapshot.Source); source != "" {
		line += " source=" + source
	}
	if behavior := strings.TrimSpace(snapshot.AuthorityBehavior); behavior != "" {
		line += " behavior=" + quoteStatusField(truncateStatusField(behavior, 120))
	}
	lines := []string{"autonomy:", line}
	if override := strings.TrimSpace(snapshot.ActiveOverrideMode); override != "" {
		overrideLine := "  active_override=" + override
		if scope := strings.TrimSpace(snapshot.ActiveOverrideScope); scope != "" {
			overrideLine += " scope=" + scope
		}
		if !snapshot.ActiveOverrideExpiry.IsZero() {
			overrideLine += " expires_at=" + snapshot.ActiveOverrideExpiry.UTC().Format(time.RFC3339)
		}
		lines = append(lines, overrideLine)
	}
	return lines
}

func renderSandboxReadinessBlock(snapshot core.SandboxReadinessSnapshot) []string {
	if len(snapshot.Issues) == 0 {
		return nil
	}
	lines := []string{"sandbox_readiness:"}
	limit := len(snapshot.Issues)
	if limit > 4 {
		limit = 4
	}
	for i := 0; i < limit; i++ {
		issue := snapshot.Issues[i]
		line := fmt.Sprintf(
			"- role=%s code=%s severity=%s mode=%s network=%s",
			strings.TrimSpace(issue.Role),
			strings.TrimSpace(issue.Code),
			strings.TrimSpace(issue.Severity),
			strings.TrimSpace(issue.Mode),
			strings.TrimSpace(issue.Network),
		)
		if summary := strings.TrimSpace(issue.Summary); summary != "" {
			line += " summary=" + quoteStatusField(truncateStatusField(summary, 120))
		}
		if repair := strings.TrimSpace(issue.NextRepairAction); repair != "" {
			line += " next=" + quoteStatusField(truncateStatusField(repair, 120))
		}
		lines = append(lines, line)
	}
	if len(snapshot.Issues) > limit {
		lines = append(lines, fmt.Sprintf("- omitted=%d", len(snapshot.Issues)-limit))
	}
	return lines
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

func renderExternalToolInvocationReadinessBlock(rows []core.ExternalToolInvocationReadinessSnapshot, maxRows int) []string {
	if len(rows) == 0 {
		return nil
	}
	if maxRows <= 0 {
		maxRows = 5
	}
	lines := []string{"external_tool_invocation_readiness source=projection:tool_lifecycle+capability_grants"}
	limit := len(rows)
	if limit > maxRows {
		limit = maxRows
	}
	for i := 0; i < limit; i++ {
		row := rows[i]
		state := "blocked"
		if row.Ready || strings.EqualFold(strings.TrimSpace(row.Status), "ready") {
			state = "ready"
		}
		selector := "-"
		if strings.TrimSpace(row.SelectorName) != "" {
			selector = strings.TrimSpace(row.SelectorName)
			if strings.TrimSpace(row.SelectorValue) != "" {
				selector += "=" + strings.TrimSpace(row.SelectorValue)
			}
		}
		line := fmt.Sprintf(
			"- tool=%s child=%s action=%s selector=%s status=%s why=%s next_repair=%s",
			firstNonEmpty(strings.TrimSpace(row.ToolName), "-"),
			firstNonEmpty(strings.TrimSpace(row.ChildPrincipal), "-"),
			firstNonEmpty(strings.TrimSpace(row.Action), "-"),
			selector,
			state,
			quoteStatusField(truncateStatusField(firstNonEmpty(strings.TrimSpace(row.Why), "-"), 140)),
			quoteStatusField(truncateStatusField(firstNonEmpty(strings.TrimSpace(row.NextRepairAction), "-"), 120)),
		)
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
		if scope := strings.TrimSpace(row.ToolInvocationScope); scope != "" {
			line += " tool_invocation_scope=" + scope
		}
		if row.ChildRuntimePresent {
			line += " child_runtime=present"
		}
		if missing := strings.TrimSpace(row.RuntimeMaterialMissing); missing != "" {
			line += " runtime_missing=" + quoteStatusField(truncateStatusField(missing, 120))
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
