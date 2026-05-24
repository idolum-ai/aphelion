//go:build linux

package face

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func RenderTelegramStatusChatOperatorCard(snapshot core.ChatStatusSnapshot, personaEffort string, governorEffort string, pendingOnly bool) string {
	lines := []string{"Chat Status"}
	if pendingOnly {
		lines = append(lines, renderOperatorAttentionLines(snapshot, true)...)
		lines = append(lines, renderOperatorBacklogLines(snapshot, true)...)
		if next := operatorNextLine(snapshot, "pending"); next != "" {
			lines = append(lines, "next: "+next)
		}
		lines = append(lines, operatorEvidenceLine(snapshot))
		lines = append(lines, renderOperatorRuntimeLine(personaEffort, governorEffort))
		lines = append(lines, "details: /health trace has the full execution trace and source attribution.")
		return strings.Join(compactStatusLines(lines), "\n")
	}

	state := chatSummaryState(snapshot)
	lines = append(lines, "status: "+operatorStatusLabel(state))
	if why := operatorStatusWhy(snapshot, state); why != "" {
		lines = append(lines, "why: "+why)
	}
	if now := operatorNowLine(snapshot, state); now != "" {
		lines = append(lines, "now: "+now)
	}
	if next := operatorNextLine(snapshot, state); next != "" {
		lines = append(lines, "next: "+next)
	}
	if work := operatorLastKnownWork(snapshot); work != "" {
		lines = append(lines, "last_known_work: "+work)
	}
	if continuation := operatorContinuationLine(snapshot.Continuation); continuation != "" {
		lines = append(lines, continuation)
	}
	if auto := operatorAutoApprovalLine(snapshot.AutoApproval); auto != "" {
		lines = append(lines, auto)
	}
	if authority := operatorAuthorityLine(snapshot.Authority); authority != "" {
		lines = append(lines, authority)
	}
	lines = append(lines, renderOperatorAttentionLines(snapshot, false)...)
	lines = append(lines, operatorQueueLine(snapshot))
	lines = append(lines, renderOperatorBacklogLines(snapshot, false)...)
	lines = append(lines, operatorEvidenceLine(snapshot))
	lines = append(lines, renderOperatorRuntimeLine(personaEffort, governorEffort))
	lines = append(lines, "details: /health trace has the full execution trace and source attribution.")
	return strings.Join(compactStatusLines(lines), "\n")
}

func operatorStatusLabel(state string) string {
	switch strings.TrimSpace(state) {
	case "needs_recovery":
		return "needs recovery"
	case "working":
		return "working"
	case "blocked":
		return "blocked"
	case "interrupted":
		return "interrupted"
	case "queued":
		return "queued"
	case "failed":
		return "failed"
	case "idle":
		return "idle"
	default:
		return firstNonEmpty(strings.TrimSpace(state), "unknown")
	}
}

func operatorStatusWhy(snapshot core.ChatStatusSnapshot, state string) string {
	switch strings.TrimSpace(state) {
	case "needs_recovery":
		if reason := operatorStaleReason(snapshot); reason != "" {
			return reason
		}
		return "status has stale active work evidence"
	case "blocked":
		if summary := strings.TrimSpace(snapshot.OperationSummary); summary != "" {
			return truncateStatusField(summary, 160)
		}
		if hasBlockingPendingItem(snapshot.PendingItems) {
			return "waiting for an operator decision"
		}
	case "working":
		if latest := snapshot.LatestTurnRun; latest != nil {
			if tool := strings.TrimSpace(latest.LastToolName); tool != "" {
				return "running tool " + tool
			}
		}
		if phase := strings.TrimSpace(snapshot.TurnPhase); phase != "" {
			return "turn phase is " + phase
		}
	case "queued":
		return fmt.Sprintf("%d queued turn(s)", snapshot.QueueDepth)
	case "failed":
		if latest := snapshot.LatestTurnRun; latest != nil && strings.TrimSpace(latest.ErrorText) != "" {
			return truncateStatusField(latest.ErrorText, 160)
		}
	case "interrupted":
		return "latest turn was interrupted"
	case "idle":
		return "no active turn or operator action is required"
	}
	return ""
}

func operatorStaleReason(snapshot core.ChatStatusSnapshot) string {
	if len(snapshot.StaleRunningTurns) > 0 {
		stale := snapshot.StaleRunningTurns[0]
		if !stale.LastActivityAt.IsZero() && !snapshot.GeneratedAt.IsZero() {
			return "last active turn record is stale by " + formatOperatorAge(snapshot.GeneratedAt.Sub(stale.LastActivityAt))
		}
		return "stale active turn record is pending recovery"
	}
	if latest := snapshot.LatestTurnRun; latest != nil && strings.EqualFold(strings.TrimSpace(latest.Status), "running") && statusTimeIsStale(snapshot.GeneratedAt, latest.LastActivityAt, snapshot.RestartHealth.StaleTurnThreshold) {
		return "last active turn record is stale by " + formatOperatorAge(snapshot.GeneratedAt.Sub(latest.LastActivityAt))
	}
	if strings.TrimSpace(snapshot.TurnPhase) != "" && statusTimeIsStale(snapshot.GeneratedAt, snapshot.TurnPhaseUpdatedAt, snapshot.RestartHealth.StaleTurnThreshold) {
		return "last turn phase update is stale by " + formatOperatorAge(snapshot.GeneratedAt.Sub(snapshot.TurnPhaseUpdatedAt))
	}
	return ""
}

func operatorNowLine(snapshot core.ChatStatusSnapshot, state string) string {
	switch strings.TrimSpace(state) {
	case "needs_recovery":
		return "no fresh active turn is visible"
	case "blocked":
		if step := strings.TrimSpace(snapshot.PlanStep); step != "" {
			return truncateStatusField(step, 160)
		}
		return "waiting before continuing"
	case "working":
		if latest := snapshot.LatestTurnRun; latest != nil {
			if tool := strings.TrimSpace(latest.LastToolName); tool != "" {
				return "tool " + tool
			}
			if request := strings.TrimSpace(latest.RequestText); request != "" {
				return truncateStatusField(request, 160)
			}
		}
		if step := strings.TrimSpace(snapshot.PlanStep); step != "" {
			return truncateStatusField(step, 160)
		}
	case "queued":
		return fmt.Sprintf("%d queued turn(s)", snapshot.QueueDepth)
	}
	return ""
}

func operatorLastKnownWork(snapshot core.ChatStatusSnapshot) string {
	for _, value := range []string{snapshot.PlanStep, snapshot.OperationSummary} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return truncateStatusField(trimmed, 180)
		}
	}
	if latest := snapshot.LatestTurnRun; latest != nil {
		if request := strings.TrimSpace(latest.RequestText); request != "" {
			return truncateStatusField(request, 180)
		}
	}
	return ""
}

func operatorNextLine(snapshot core.ChatStatusSnapshot, state string) string {
	attention := operatorAttentionItems(snapshot.PendingItems)
	if strings.TrimSpace(state) == "needs_recovery" || (chatHasStaleWorkEvidence(snapshot) && !pendingItemsContainKind(attention, core.PendingItemKindStaleTurn)) {
		return "run /health diagnose, or use /stop if this stale work should be cleared"
	}
	if len(attention) > 0 {
		return operatorAttentionNextLine(attention[0], len(attention))
	}

	switch strings.TrimSpace(state) {
	case "pending":
		if len(operatorBacklogItems(snapshot.PendingItems)) > 0 {
			return "review backlog when ready; no immediate operator action is visible"
		}
		return "return to This Chat; no pending operator action is visible"
	case "blocked":
		return "resolve the blocker above before continuing"
	case "working":
		return "wait for the active turn; tap Refresh to re-check, or use /stop if it should not continue"
	case "queued":
		return "wait for queued work to start, or use /stop to drop queued work"
	case "failed":
		return "run /health trace for error evidence or /health diagnose for repair guidance"
	case "interrupted":
		return "send the next instruction, or run /health diagnose if recovery is unclear"
	case "idle":
		if len(operatorBacklogItems(snapshot.PendingItems)) > 0 {
			return "no immediate action; review backlog when ready"
		}
		return "send the next request; no operator action needed"
	default:
		return "tap Refresh to re-check status"
	}
}

func operatorAttentionNextLine(item core.PendingItem, count int) string {
	base := "tap Pending Only to inspect pending operator items"
	switch item.Kind {
	case core.PendingItemKindDecision:
		base = "tap Pending Only, then approve or deny the pending decision"
	case core.PendingItemKindContinuation:
		base = "tap Pending Only, then approve or stop the continuation"
	case core.PendingItemKindReview:
		base = "tap Pending Only to inspect the pending review"
	case core.PendingItemKindRecovery:
		base = "tap Pending Only or run /health diagnose for repair guidance"
	case core.PendingItemKindStaleTurn:
		base = "run /health diagnose, or use /stop if this stale work should be cleared"
	}
	if count > 1 {
		return fmt.Sprintf("%s (%d attention item(s))", base, count)
	}
	return base
}

func operatorEvidenceLine(snapshot core.ChatStatusSnapshot) string {
	parts := make([]string, 0, 4)
	if !snapshot.GeneratedAt.IsZero() {
		parts = append(parts, "as of "+formatStatusTime(snapshot.GeneratedAt))
	} else {
		parts = append(parts, "as of unavailable")
	}
	parts = append(parts, "source: chat status projection")
	if latest := snapshot.LatestTurnRun; latest != nil {
		if source := strings.TrimSpace(latest.Source); source != "" {
			parts = append(parts, "latest turn: "+truncateStatusField(source, 120))
		}
	}
	if delivery := strings.TrimSpace(snapshot.DeliveryStatus); delivery != "" {
		parts = append(parts, "delivery: "+delivery)
	}
	return "evidence: " + strings.Join(parts, "; ")
}

func operatorContinuationLine(snapshot *core.ContinuationStatusSnapshot) string {
	if snapshot == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(snapshot.Status)) {
	case "revoked":
		return "continuation: stopped"
	case "pending":
		return "continuation: awaiting approval"
	case "approved":
		if snapshot.RemainingTurns > 0 {
			return fmt.Sprintf("continuation: approved, %d turn(s) remaining", snapshot.RemainingTurns)
		}
		return "continuation: approved"
	case "blocked":
		if reason := strings.TrimSpace(snapshot.BlockedReason); reason != "" {
			return "continuation: blocked, " + truncateStatusField(reason, 120)
		}
		return "continuation: blocked"
	case "consumed":
		return "continuation: consumed"
	default:
		return ""
	}
}

func operatorAutoApprovalLine(snapshot *core.AutoApprovalStatusSnapshot) string {
	if snapshot == nil || !snapshot.Active {
		return ""
	}
	parts := []string{"auto_approval: active"}
	if !snapshot.Usable && strings.TrimSpace(snapshot.BlockedReason) != "" {
		parts = append(parts, "blocked by auto mode")
	}
	if !snapshot.ExpiresAt.IsZero() {
		parts = append(parts, "until "+formatStatusTime(snapshot.ExpiresAt))
	}
	if snapshot.MaxUses > 0 {
		parts = append(parts, fmt.Sprintf("used %d/%d", snapshot.UsedCount, snapshot.MaxUses))
	} else {
		parts = append(parts, fmt.Sprintf("used %d", snapshot.UsedCount))
	}
	if !snapshot.Usable && strings.TrimSpace(snapshot.BlockedReason) != "" {
		parts = append(parts, truncateStatusField(strings.TrimSpace(snapshot.BlockedReason), 120))
	}
	return strings.Join(parts, ", ")
}

func renderOperatorAttentionLines(snapshot core.ChatStatusSnapshot, includeDetails bool) []string {
	items := operatorAttentionItems(snapshot.PendingItems)
	hasSyntheticStale := chatHasStaleWorkEvidence(snapshot) && !pendingItemsContainKind(items, core.PendingItemKindStaleTurn)
	if len(items) == 0 && !hasSyntheticStale {
		return []string{"needs_attention: none"}
	}
	lines := []string{"needs_attention:"}
	if hasSyntheticStale {
		lines = append(lines, "- stale active turn record")
	}
	limit := len(items)
	if !includeDetails && limit > 3 {
		limit = 3
	}
	for i := 0; i < limit; i++ {
		lines = append(lines, "- "+operatorPendingItemLine(items[i], includeDetails))
	}
	if len(items) > limit {
		lines = append(lines, fmt.Sprintf("- %d more item(s)", len(items)-limit))
	}
	return lines
}

func operatorAttentionItems(items []core.PendingItem) []core.PendingItem {
	out := make([]core.PendingItem, 0, len(items))
	for _, item := range items {
		if pendingItemNeedsAttention(item) {
			out = append(out, item)
		}
	}
	return out
}

func pendingItemsContainKind(items []core.PendingItem, kind core.PendingItemKind) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func operatorPendingItemLine(item core.PendingItem, includeDetails bool) string {
	label := operatorPendingKindLabel(item.Kind)
	parts := []string{label}
	if item.Age > 0 {
		parts = append(parts, "age "+item.Age.Truncate(time.Second).String())
	}
	if includeDetails {
		if id := strings.TrimSpace(item.ID); id != "" {
			parts = append(parts, id)
		}
	}
	if summary := strings.TrimSpace(item.Summary); summary != "" && (includeDetails || item.Kind != core.PendingItemKindStaleTurn) {
		parts = append(parts, truncateStatusField(summary, 140))
	}
	return strings.Join(parts, ": ")
}

func operatorPendingKindLabel(kind core.PendingItemKind) string {
	switch kind {
	case core.PendingItemKindDecision:
		return "approval needed"
	case core.PendingItemKindContinuation:
		return "continuation"
	case core.PendingItemKindReview:
		return "review"
	case core.PendingItemKindRecovery:
		return "recovery"
	case core.PendingItemKindStaleTurn:
		return "stale active turn"
	default:
		return strings.ReplaceAll(strings.TrimSpace(string(kind)), "_", " ")
	}
}

func operatorQueueLine(snapshot core.ChatStatusSnapshot) string {
	if snapshot.QueueDepth <= 0 {
		return "queue: empty"
	}
	return fmt.Sprintf("queue: %d queued turn(s)", snapshot.QueueDepth)
}

func renderOperatorBacklogLines(snapshot core.ChatStatusSnapshot, includeDetails bool) []string {
	missions := operatorBacklogItems(snapshot.PendingItems)
	if len(missions) == 0 {
		return []string{"backlog: none"}
	}
	if !includeDetails {
		return []string{fmt.Sprintf("backlog: %d candidate mission(s)", len(missions))}
	}
	lines := []string{fmt.Sprintf("backlog: %d candidate mission(s)", len(missions))}
	limit := len(missions)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		lines = append(lines, "- "+operatorPendingItemLine(missions[i], true))
	}
	if len(missions) > limit {
		lines = append(lines, fmt.Sprintf("- %d more mission(s)", len(missions)-limit))
	}
	return lines
}

func operatorBacklogItems(items []core.PendingItem) []core.PendingItem {
	out := make([]core.PendingItem, 0, len(items))
	for _, item := range items {
		if pendingItemIsBacklog(item) {
			out = append(out, item)
		}
	}
	return out
}

func renderOperatorRuntimeLine(personaEffort string, governorEffort string) string {
	return fmt.Sprintf("runtime: persona=%s governor=%s", strings.TrimSpace(personaEffort), strings.TrimSpace(governorEffort))
}

func formatOperatorAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	return age.Truncate(time.Second).String()
}

func compactStatusLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	lastBlank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if len(out) == 0 || lastBlank {
				continue
			}
			out = append(out, "")
			lastBlank = true
			continue
		}
		out = append(out, line)
		lastBlank = false
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func operatorAuthorityLine(snapshot core.AuthorityStatusSnapshot) string {
	if strings.TrimSpace(snapshot.Status) == "" && snapshot.FindingCount == 0 {
		return ""
	}
	if strings.TrimSpace(snapshot.Status) == "healthy" && snapshot.FindingCount == 0 {
		return "authority: healthy"
	}
	return fmt.Sprintf("authority: needs attention (%d finding(s), %d error(s)); /health trace has source and repair details.", snapshot.FindingCount, snapshot.ErrorCount)
}
