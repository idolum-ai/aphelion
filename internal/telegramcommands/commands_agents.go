//go:build linux

package telegramcommands

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	staleAgentsCallbackText = "This durable-agent action is no longer active. Run /agents again."
	durableAgentsPageSize   = 5
)

func renderDurableAgentsCommand(agents []core.DurableAgentStatusSnapshot) (string, [][]telegram.InlineButton) {
	return renderDurableAgentsCommandPage(agents, 1)
}

func renderDurableAgentsCommandPage(agents []core.DurableAgentStatusSnapshot, page int) (string, [][]telegram.InlineButton) {
	return renderDurableAgentsCommandViewPage(agents, telegramPageViewList, page)
}

func renderDurableAgentsCommandViewPage(agents []core.DurableAgentStatusSnapshot, view string, page int) (string, [][]telegram.InlineButton) {
	view = normalizeDurableAgentsView(view)
	visibleAgents := filterDurableAgentsView(agents, view)
	visible, info := telegramPageItems(visibleAgents, page, durableAgentsPageSize)
	details := make([]string, 0, len(visible))
	evidence := durableAgentsBoardEvidence(agents)
	rows := make([][]telegram.InlineButton, 0, len(visible)+4)
	if len(visibleAgents) == 0 {
		details = append(details, durableAgentsEmptyDetail(view))
		rows = append(rows, durableAgentsBoardTopRow(view, info.Page))
		return renderTelegramCompactPanel(face.OperatorPanel{
			Title:   durableAgentsBoardTitle(view),
			State:   "none",
			Why:     "Durable children only appear here after they are declared in governed configuration or state.",
			Next:    durableAgentsBoardNext(view),
			Details: details,
		}, false), rows
	}
	for i, agent := range visible {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			continue
		}
		line := fmt.Sprintf("%d. %s (%s | %s | %s)", info.Start+i+1, agentID, firstNonEmpty(strings.TrimSpace(agent.ChannelKind), "-"), firstNonEmpty(strings.TrimSpace(agent.Status), "-"), firstNonEmpty(strings.TrimSpace(agent.Health), "-"))
		if mode := strings.TrimSpace(agent.TailnetMode); mode != "" {
			line += "; tailnet:" + mode
		}
		if reason := strings.TrimSpace(agent.ChildRuntimeBlockedReason); reason != "" {
			line += "; blocked: " + truncateOperatorLine(reason, 110)
		}
		if !agent.LastWakeAt.IsZero() {
			line += "; last wake " + agent.LastWakeAt.UTC().Format("2006-01-02 15:04Z")
		}
		details = append(details, line)
	}
	rows = append(rows, durableAgentsBoardTopRow(view, info.Page))
	rows = append(rows, durableAgentDetailRows(visible, info.Start, view, info.Page)...)
	rows = append(rows, telegramPageNavigationRows(info, telegramPageSurfaceAgents, view)...)
	state := fmt.Sprintf("%d shown; %d total", len(visibleAgents), len(agents))
	if info.PageCount > 1 {
		state = fmt.Sprintf("%d shown; page %d of %d; %d total", len(visibleAgents), info.Page, info.PageCount, len(agents))
	}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    durableAgentsBoardTitle(view),
		State:    state,
		Why:      "Durable children run with their own policy, memory, approvals, and audit history.",
		Next:     durableAgentsBoardNext(view),
		Details:  details,
		Evidence: evidence,
	}, durableAgentsPageSize, 4), rows
}

func renderDurableAgentDetail(agent core.DurableAgentStatusSnapshot, view string, page int) (string, [][]telegram.InlineButton) {
	agentID := strings.TrimSpace(agent.AgentID)
	status := strings.ToLower(strings.TrimSpace(agent.Status))
	details := []string{
		"Agent: " + agentID,
		"Channel: " + firstNonEmpty(strings.TrimSpace(agent.ChannelKind), "-"),
		"Status: " + firstNonEmpty(strings.TrimSpace(agent.Status), "-"),
		"Health: " + firstNonEmpty(strings.TrimSpace(agent.Health), "-"),
	}
	if scope := strings.Trim(strings.TrimSpace(agent.ParentScopeKind)+"/"+strings.TrimSpace(agent.ParentScopeID), "/"); scope != "" {
		details = append(details, "Parent scope: "+scope)
	}
	if wake := strings.TrimSpace(agent.WakeupMode); wake != "" {
		details = append(details, "Wake mode: "+wake)
	}
	if network := strings.TrimSpace(agent.NetworkPolicy); network != "" {
		details = append(details, "Network: "+network)
	}
	if agent.PolicyVersion > 0 {
		details = append(details, fmt.Sprintf("Policy: version %d", agent.PolicyVersion))
	}
	if drift := strings.TrimSpace(agent.PolicyDrift); drift != "" {
		details = append(details, "Policy drift: "+drift)
	}
	if outbound := strings.TrimSpace(agent.PolicyOutboundMode); outbound != "" {
		details = append(details, "Outbound: "+outbound)
	}
	if grants := agent.ChildRuntimeGrantCount; grants > 0 {
		details = append(details, fmt.Sprintf("Approvals: %d", grants))
	}
	if reason := strings.TrimSpace(agent.ChildRuntimeBlockedReason); reason != "" {
		details = append(details, "Blocked: "+truncateOperatorLine(reason, 160))
	}
	if repair := strings.TrimSpace(agent.ChildRuntimeRepairHint); repair != "" {
		details = append(details, "Repair: "+truncateOperatorLine(repair, 160))
	}
	if enrollment := strings.TrimSpace(agent.EnrollmentStatus); enrollment != "" {
		details = append(details, "Enrollment: "+enrollment)
	}
	if host := strings.TrimSpace(agent.TailnetHostname); host != "" {
		details = append(details, "Tailnet host: "+host)
	}
	if surface := strings.TrimSpace(agent.TailnetSurfaceID); surface != "" {
		details = append(details, "Tailnet surface: "+truncateOperatorLine(surface, 160))
	}
	if profile := strings.TrimSpace(agent.ProfileManifestStatus); profile != "" {
		details = append(details, fmt.Sprintf("Profile: %s; files %d", profile, agent.ProfileManifestFileCount))
	}
	evidence := durableAgentDetailEvidence(agent)
	next := "Use Brief for a bounded check-in, or lifecycle controls when the child should pause or leave active use."
	if status == "retired" {
		next = "Retired agents are inspect-only here; restore or purge from governed provisioning tools."
	}
	rows := durableAgentDetailButtonRows(agent, view, page)
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Durable Agent",
		State:    firstNonEmpty(strings.TrimSpace(agent.Health), "unknown") + " " + agentID,
		Why:      "Agent authority is separate from the parent and should be changed only through explicit controls.",
		Next:     next,
		Details:  details,
		Evidence: evidence,
	}, 14, 8), rows
}

func renderDurableAgentRetireConfirm(agent core.DurableAgentStatusSnapshot, view string, page int) (string, [][]telegram.InlineButton) {
	agentID := strings.TrimSpace(agent.AgentID)
	details := []string{
		"Agent: " + agentID,
		"Status: " + firstNonEmpty(strings.TrimSpace(agent.Status), "-"),
		"Health: " + firstNonEmpty(strings.TrimSpace(agent.Health), "-"),
		"Retire removes this child from active use but preserves history, files, parent conversation, and audit records.",
		"Retire also revokes active approvals, remote enrollment, and local Tailnet surface trust when present.",
	}
	return renderTelegramCompactPanel(face.OperatorPanel{
		Title:   "Retire Agent?",
		State:   agentID,
		Why:     "Retirement is lifecycle control, not memory absorption or destructive purge.",
		Next:    "Brief first if you want a final status note, or confirm retirement.",
		Details: details,
	}, false), durableAgentRetireConfirmRows(agentID, view, page)
}

func durableAgentsBoardTopRow(view string, page int) []telegram.InlineButton {
	nextView := telegramPageViewRetired
	label := "Show Retired"
	if normalizeDurableAgentsView(view) == telegramPageViewRetired {
		nextView = telegramPageViewList
		label = "Show Current"
	}
	return []telegram.InlineButton{
		{Text: "Refresh", CallbackData: encodeDurableAgentsRefreshCallbackData(view, page)},
		{Text: "Analyze", CallbackData: encodeDurableAgentsAnalyzeCallbackData()},
		{Text: label, CallbackData: encodeDurableAgentsRefreshCallbackData(nextView, 1)},
	}
}

func durableAgentsBoardTitle(view string) string {
	if normalizeDurableAgentsView(view) == telegramPageViewRetired {
		return "Retired Agents"
	}
	return "Durable Agents"
}

func durableAgentsBoardNext(view string) string {
	if normalizeDurableAgentsView(view) == telegramPageViewRetired {
		return "Open a retired agent for evidence; restore or purge from governed tools outside Telegram."
	}
	return "Open an agent before taking lifecycle action, or analyze the board without waking children."
}

func durableAgentsEmptyDetail(view string) string {
	if normalizeDurableAgentsView(view) == telegramPageViewRetired {
		return "No retired durable agents."
	}
	return "No current durable agents are configured."
}

func filterDurableAgentsView(agents []core.DurableAgentStatusSnapshot, view string) []core.DurableAgentStatusSnapshot {
	view = normalizeDurableAgentsView(view)
	out := make([]core.DurableAgentStatusSnapshot, 0, len(agents))
	for _, agent := range agents {
		retired := strings.EqualFold(strings.TrimSpace(agent.Status), "retired")
		if view == telegramPageViewRetired {
			if retired {
				out = append(out, agent)
			}
			continue
		}
		if !retired {
			out = append(out, agent)
		}
	}
	return out
}

func durableAgentsBoardEvidence(agents []core.DurableAgentStatusSnapshot) []string {
	active := 0
	parked := 0
	retired := 0
	degraded := 0
	for _, agent := range agents {
		switch strings.ToLower(strings.TrimSpace(agent.Status)) {
		case "active":
			active++
		case "parked":
			parked++
		case "retired":
			retired++
		}
		if strings.EqualFold(strings.TrimSpace(agent.Health), "degraded") {
			degraded++
		}
	}
	return []string{
		fmt.Sprintf("active: %d", active),
		fmt.Sprintf("parked: %d", parked),
		fmt.Sprintf("retired: %d", retired),
		fmt.Sprintf("degraded: %d", degraded),
	}
}

func durableAgentDetailEvidence(agent core.DurableAgentStatusSnapshot) []string {
	evidence := make([]string, 0, 8)
	if !agent.LastWakeAt.IsZero() {
		evidence = append(evidence, "last wake: "+agent.LastWakeAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if !agent.LastReviewAt.IsZero() {
		evidence = append(evidence, "last review: "+agent.LastReviewAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if !agent.DormantAt.IsZero() {
		evidence = append(evidence, "dormant: "+agent.DormantAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if !agent.LastAppliedPolicyAt.IsZero() {
		evidence = append(evidence, "policy applied: "+agent.LastAppliedPolicyAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if errText := strings.TrimSpace(agent.LastApplyError); errText != "" {
		evidence = append(evidence, "last apply error: "+truncateOperatorLine(errText, 160))
	}
	if !agent.EnrollmentLastSeenAt.IsZero() {
		evidence = append(evidence, "enrollment seen: "+agent.EnrollmentLastSeenAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if !agent.EnrollmentRevokedAt.IsZero() {
		evidence = append(evidence, "enrollment revoked: "+agent.EnrollmentRevokedAt.UTC().Format("2006-01-02 15:04Z"))
	}
	if source := strings.TrimSpace(agent.RuntimePostureSource); source != "" {
		evidence = append(evidence, "posture source: "+truncateOperatorLine(source, 160))
	}
	return evidence
}
