//go:build linux

package face

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

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
		if strings.TrimSpace(agent.TailnetMode) != "" {
			lines = append(lines, fmt.Sprintf(
				"  tailnet mode=%s hostname=%s surface_policy=%s surface_id=%s tags=%s",
				strings.TrimSpace(agent.TailnetMode),
				firstNonEmpty(strings.TrimSpace(agent.TailnetHostname), "-"),
				firstNonEmpty(strings.TrimSpace(agent.TailnetSurfacePolicy), "-"),
				firstNonEmpty(strings.TrimSpace(agent.TailnetSurfaceID), "-"),
				formatStringList(agent.TailnetTags),
			))
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
		if crumb := core.NormalizeDebugBreadcrumb(item.DebugBreadcrumb); crumb.Active() {
			if traceID := strings.TrimSpace(crumb.TraceID); traceID != "" {
				line += " trace_id=" + quoteStatusField(traceID)
			}
			if inspect := strings.TrimSpace(crumb.InspectCommand); inspect != "" {
				line += " inspect_command=" + quoteStatusField(inspect)
			}
			if repair := strings.TrimSpace(crumb.NextRepairAction); repair != "" {
				line += " next_repair_action=" + quoteStatusField(truncateStatusField(repair, 120))
			}
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
