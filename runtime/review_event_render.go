//go:build linux

package runtime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

type reviewEventArtifactMetadata struct {
	AgentID           string            `json:"agent_id"`
	Summary           string            `json:"summary"`
	IntervalLabel     string            `json:"interval_label"`
	LocalActions      []string          `json:"local_actions"`
	Questions         []string          `json:"questions"`
	RiskFlags         []string          `json:"risk_flags"`
	ArtifactRefs      []string          `json:"artifact_refs"`
	Metadata          map[string]string `json:"metadata"`
	Contract          string            `json:"contract"`
	Constraints       string            `json:"constraints"`
	Purpose           string            `json:"purpose"`
	Kind              string            `json:"kind"`
	TargetResource    string            `json:"target_resource"`
	RiskClass         string            `json:"risk_class"`
	RequestedBy       string            `json:"requested_by"`
	RequestedFor      string            `json:"requested_for"`
	RequestVia        string            `json:"request_via"`
	ChannelKind       string            `json:"channel_kind"`
	DelegationSurface string            `json:"delegation_surface"`
}

func FormatReviewEventCompactMessage(event session.ReviewEvent) string {
	if proposal, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return FormatMissionControlProposalMessage(proposal)
	}
	meta, _ := parseReviewEventArtifactMetadata(event)
	lines := []string{"**" + reviewEventCompactTitle(event, meta) + "**"}
	if context := reviewEventCompactContext(meta); context != "" {
		lines = append(lines, context)
	}
	if status := reviewEventCompactStatus(event, meta); status != "" {
		lines = append(lines, "", "**Status**", status)
	}
	if summary := reviewEventCompactSummary(event, meta); summary != "" {
		lines = append(lines, "", "**Summary**", truncateReviewEventText(summary, 420))
	}
	if points := reviewEventCompactPoints(meta); len(points) > 0 {
		lines = append(lines, "", "**Key points**")
		for _, point := range points {
			lines = append(lines, "- "+truncateReviewEventText(point, 180))
		}
	}
	if next := reviewEventCompactNextAction(meta); next != "" {
		lines = append(lines, "", "**"+reviewEventCompactNextActionHeading(meta)+"**", "- "+truncateReviewEventText(next, 220))
	}
	lines = append(lines, "", reviewEventCompactFooter(meta))
	return truncateReviewEventBlock(strings.Join(lines, "\n"), 1800)
}

func FormatReviewEventDetailsMessage(event session.ReviewEvent) string {
	return formatReviewEventDetails(event)
}

func FormatReviewEventDetailsMarkdown(event session.ReviewEvent) string {
	return formatReviewEventDetails(event)
}

func formatReviewEventDetails(event session.ReviewEvent) string {
	lines := []string{FormatReviewEventMessage(event)}
	meta, ok := parseReviewEventArtifactMetadata(event)
	if ok && len(meta.ArtifactRefs) > 0 {
		lines = append(lines, "", "**Artifacts**")
		for _, ref := range meta.ArtifactRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			lines = append(lines, "- "+truncateReviewEventText(ref, 220))
		}
	}
	if ok {
		if capability := reviewEventCapabilityDetailsLines(meta); len(capability) > 0 {
			lines = append(lines, "", "**Capability request**")
			lines = append(lines, capability...)
		}
	}
	if ok && len(meta.Metadata) > 0 {
		keys := make([]string, 0, len(meta.Metadata))
		for key := range meta.Metadata {
			key = strings.TrimSpace(key)
			if key != "" {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			lines = append(lines, "", "**Metadata**")
			for _, key := range keys {
				value := strings.TrimSpace(meta.Metadata[key])
				if value == "" {
					continue
				}
				lines = append(lines, "- "+key+": "+truncateReviewEventText(value, 220))
			}
		}
	}
	if debug := reviewEventDebugBreadcrumbLines(event, meta, ok); len(debug) > 0 {
		lines = append(lines, "", "**Debug**")
		lines = append(lines, debug...)
	}
	lines = append(lines, "", reviewEventDetailsFooter(meta))
	return truncateReviewEventBlock(strings.Join(lines, "\n"), 3900)
}

func reviewEventCapabilityDetailsLines(meta reviewEventArtifactMetadata) []string {
	var lines []string
	fields := []struct {
		label string
		value string
	}{
		{"Kind", meta.Kind},
		{"Target", meta.TargetResource},
		{"Risk", meta.RiskClass},
		{"Purpose", meta.Purpose},
		{"Requested by", meta.RequestedBy},
		{"Requested for", meta.RequestedFor},
		{"Request via", meta.RequestVia},
		{"Channel", meta.ChannelKind},
		{"Delegation surface", meta.DelegationSurface},
	}
	for _, field := range fields {
		if value := strings.TrimSpace(field.value); value != "" {
			lines = append(lines, fmt.Sprintf("- %s: %s", field.label, truncateReviewEventText(value, 360)))
		}
	}
	if contract := strings.TrimSpace(meta.Contract); contract != "" {
		lines = append(lines, "", "**Contract**", truncateReviewEventText(contract, 1200))
	}
	if constraints := strings.TrimSpace(meta.Constraints); constraints != "" {
		lines = append(lines, "", "**Constraints**", truncateReviewEventText(constraints, 1200))
	}
	return lines
}

func parseReviewEventArtifactMetadata(event session.ReviewEvent) (reviewEventArtifactMetadata, bool) {
	var meta reviewEventArtifactMetadata
	if strings.TrimSpace(event.MetadataJSON) == "" {
		return meta, false
	}
	if err := json.Unmarshal([]byte(event.MetadataJSON), &meta); err != nil {
		return reviewEventArtifactMetadata{}, false
	}
	return meta, true
}

func reviewEventDebugBreadcrumbLines(event session.ReviewEvent, meta reviewEventArtifactMetadata, metaOK bool) []string {
	traceID := "review_event"
	if event.ID > 0 {
		traceID = fmt.Sprintf("review_event:%d", event.ID)
	}
	canonical := "review_events"
	if event.ID > 0 {
		canonical = fmt.Sprintf("review_events id=%d", event.ID)
	}
	inspectCommand := ""
	if metaOK {
		inspectCommand = reviewEventInspectCommand(meta)
	}
	if strings.TrimSpace(inspectCommand) == "" {
		inspectCommand = "/health trace"
	}
	return core.DebugBreadcrumbLines(core.DebugBreadcrumb{
		TraceID:          traceID,
		CanonicalRecord:  canonical,
		Projection:       "runtime.FormatReviewEventDetailsMessage",
		InspectCommand:   inspectCommand,
		CodeOwner:        "runtime/turn.go",
		NextRepairAction: reviewEventNextRepairAction(meta, metaOK),
	})
}

func reviewEventNextRepairAction(meta reviewEventArtifactMetadata, metaOK bool) string {
	if metaOK {
		if next := strings.TrimSpace(meta.Metadata["operator_next_action"]); next != "" {
			return next
		}
		if status := strings.TrimSpace(meta.Metadata["operator_status"]); status != "" {
			return "inspect the review event details and act only if the operator status requires it"
		}
	}
	return "inspect the review event details and the canonical review_events row before taking repair action"
}

func reviewEventInspectCommand(meta reviewEventArtifactMetadata) string {
	agentID := strings.TrimSpace(meta.AgentID)
	ref := strings.TrimSpace(meta.Metadata["forensic_ref"])
	if ref == "" {
		for _, artifactRef := range meta.ArtifactRefs {
			artifactRef = strings.TrimSpace(artifactRef)
			if strings.HasPrefix(artifactRef, "forensic://") {
				ref = artifactRef
				break
			}
		}
	}
	if agentID == "" || ref == "" {
		return ""
	}
	return fmt.Sprintf("aphelion durable-agent forensic --agent %s --ref %s show", shellQuoteDebugToken(agentID), shellQuoteDebugToken(ref))
}

func shellQuoteDebugToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'))
	}) < 0 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func reviewEventCompactTitle(event session.ReviewEvent, meta reviewEventArtifactMetadata) string {
	if title := strings.TrimSpace(meta.Metadata["operator_title"]); title != "" {
		return title
	}
	agent := strings.TrimSpace(meta.AgentID)
	if agent == "" {
		agent = formattedReviewEventAgent(event)
	}
	switch strings.ToLower(agent) {
	case "idolum-daily-review":
		return "Daily review"
	case "":
		return "Child update"
	default:
		return "Review: " + agent
	}
}

func reviewEventCompactContext(meta reviewEventArtifactMetadata) string {
	parts := make([]string, 0, 3)
	if channel := strings.TrimSpace(meta.Metadata["channel_kind"]); channel != "" {
		parts = append(parts, reviewEventHumanChannel(channel))
	}
	if interval := strings.TrimSpace(meta.IntervalLabel); interval != "" {
		parts = append(parts, interval)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " • ")
}

func reviewEventHumanChannel(channel string) string {
	switch strings.TrimSpace(strings.ToLower(channel)) {
	case "scheduled_review":
		return "Daily review"
	case "external_channel":
		return "external channel"
	default:
		return strings.ReplaceAll(strings.TrimSpace(channel), "_", " ")
	}
}

func reviewEventCompactStatus(_ session.ReviewEvent, meta reviewEventArtifactMetadata) string {
	if status := strings.TrimSpace(meta.Metadata["operator_status"]); status != "" {
		return reviewEventOutcomeStatusLabel(status)
	}
	status := firstReviewEventOutcomeStatus(meta)
	if status == "" {
		return "UPDATE"
	}
	return reviewEventOutcomeStatusLabel(status)
}

func firstReviewEventOutcomeStatus(meta reviewEventArtifactMetadata) string {
	if len(meta.Metadata) == 0 {
		return ""
	}
	for _, key := range []string{
		"external_channel_status",
		"status",
		"review_status",
		"outcome",
		"child_outcome",
	} {
		if status := strings.TrimSpace(meta.Metadata[key]); status != "" {
			return status
		}
	}
	if errText := strings.TrimSpace(meta.Metadata["external_channel_error"]); errText != "" {
		return "blocked"
	}
	if errText := strings.TrimSpace(meta.Metadata["blocker"]); errText != "" {
		return "blocked"
	}
	if errText := strings.TrimSpace(meta.Metadata["error"]); errText != "" {
		return "failed"
	}
	if count := parsePositiveReviewEventCount(meta.Metadata["artifact_count"]); count > 0 {
		return "completed"
	}
	if count := parsePositiveReviewEventCount(meta.Metadata["generated_artifact_count"]); count > 0 {
		return "completed"
	}
	return ""
}

func reviewEventOutcomeStatusLabel(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	status = strings.ReplaceAll(status, "-", "_")
	switch status {
	case "wake_completed", "completed", "complete", "success", "succeeded", "ok":
		return "COMPLETED"
	case "paused", "pause", "suppressed", "backoff":
		return "PAUSED"
	case "wake_blocked", "blocked", "blocker", "refused", "unavailable":
		return "BLOCKED"
	case "failed", "failure", "error":
		return "FAILED"
	case "needs_review", "review", "review_required":
		return "NEEDS REVIEW"
	default:
		return "UPDATE"
	}
}

func parsePositiveReviewEventCount(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count <= 0 {
		return 0
	}
	return count
}

func reviewEventCompactSummary(event session.ReviewEvent, meta reviewEventArtifactMetadata) string {
	if summary := strings.TrimSpace(meta.Metadata["operator_summary"]); summary != "" {
		return normalizeReviewEventWhitespace(summary)
	}
	if summary := strings.TrimSpace(meta.Summary); summary != "" {
		return normalizeReviewEventWhitespace(summary)
	}
	for _, line := range strings.Split(event.Summary, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "summary:"); ok {
			return normalizeReviewEventWhitespace(rest)
		}
	}
	return normalizeReviewEventWhitespace(event.Summary)
}

func reviewEventCompactFooter(meta reviewEventArtifactMetadata) string {
	if reviewEventHasRedactions(meta) {
		return "Details shows the safe review record; raw child text is stored locally because it may contain sensitive material."
	}
	return "Details has the full child update."
}

func reviewEventDetailsFooter(meta reviewEventArtifactMetadata) string {
	if reviewEventHasRedactions(meta) {
		return "Use Hide details to return to the compact summary. Raw redacted text is stored only in the local forensic sidecar."
	}
	return "Use Hide details to return to the compact summary."
}

func reviewEventHasRedactions(meta reviewEventArtifactMetadata) bool {
	if strings.TrimSpace(meta.Metadata["redacted_fields"]) != "" {
		return true
	}
	for _, value := range []string{meta.Summary, meta.Metadata["operator_summary"]} {
		if strings.Contains(value, "[REDACTED:") {
			return true
		}
	}
	for _, action := range meta.LocalActions {
		if strings.Contains(action, "[REDACTED:") {
			return true
		}
	}
	for _, question := range meta.Questions {
		if strings.Contains(question, "[REDACTED:") {
			return true
		}
	}
	return false
}

func reviewEventCompactPoints(meta reviewEventArtifactMetadata) []string {
	points := make([]string, 0, 3)
	seen := map[string]struct{}{}
	add := func(point string) {
		if len(points) >= 3 {
			return
		}
		point = normalizeReviewEventWhitespace(point)
		if point == "" {
			return
		}
		key := strings.ToLower(point)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		points = append(points, point)
	}
	if point := meta.Metadata["operator_point"]; point != "" {
		add(point)
	}
	for _, action := range meta.LocalActions {
		if len(points) >= 3 {
			break
		}
		add(action)
	}
	if len(points) < 3 {
		for _, risk := range meta.RiskFlags {
			if len(points) >= 3 {
				break
			}
			if !reviewEventCompactRiskVisible(meta, risk) {
				continue
			}
			if risk = normalizeReviewEventWhitespace(risk); risk != "" {
				add("risk: " + risk)
			}
		}
	}
	return points
}

func reviewEventCompactNextAction(meta reviewEventArtifactMetadata) string {
	if next := normalizeReviewEventWhitespace(meta.Metadata["operator_next_action"]); next != "" {
		return next
	}
	for _, question := range meta.Questions {
		if question = normalizeReviewEventWhitespace(question); question != "" {
			return question
		}
	}
	if errText := normalizeReviewEventWhitespace(meta.Metadata["external_channel_error"]); errText != "" {
		return errText
	}
	return ""
}

func reviewEventCompactNextActionHeading(meta reviewEventArtifactMetadata) string {
	switch strings.TrimSpace(meta.Metadata["operator_action"]) {
	case "no_action_unless_work_item", "no_action_needed":
		return "No action needed"
	default:
		return "Needs attention"
	}
}

func reviewEventCompactRiskVisible(meta reviewEventArtifactMetadata, risk string) bool {
	risk = strings.ToLower(strings.TrimSpace(risk))
	if risk == "" {
		return false
	}
	channel := strings.ToLower(strings.TrimSpace(meta.Metadata["channel_kind"]))
	switch risk {
	case "external_channel", "adapter_dispatch":
		return channel != "external_channel"
	default:
		return true
	}
}

func normalizeReviewEventWhitespace(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func truncateReviewEventText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-3])) + "..."
}

func truncateReviewEventBlock(s string, limit int) string {
	return truncateReviewEventText(s, limit)
}

func FormatReviewEventMessage(event session.ReviewEvent) string {
	if proposal, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return FormatMissionControlProposalMessage(proposal)
	}
	turnRange := "n/a"
	if event.TurnFrom > 0 && event.TurnTo >= event.TurnFrom {
		turnRange = fmt.Sprintf("%d-%d", event.TurnFrom, event.TurnTo)
	} else if event.TurnFrom > 0 {
		turnRange = fmt.Sprintf("%d", event.TurnFrom)
	}
	return face.RenderReviewDigest(face.ReviewDigestNotice{
		SourceChatID: event.SourceChatID,
		SourceUserID: event.SourceUserID,
		SourceRole:   event.SourceRole,
		SourceScope:  formattedReviewEventScope(event),
		SourceAgent:  formattedReviewEventAgent(event),
		ParentScope:  formattedReviewEventParentScope(event),
		TurnRange:    turnRange,
		Summary:      strings.TrimSpace(event.Summary),
	})
}

func formattedReviewEventScope(event session.ReviewEvent) string {
	scope := session.NormalizeScopeRef(event.SourceScope)
	if scope.IsZero() {
		return ""
	}
	return scope.String()
}

func formattedReviewEventAgent(event session.ReviewEvent) string {
	scope := session.NormalizeScopeRef(event.SourceScope)
	return strings.TrimSpace(scope.DurableAgentID)
}

func formattedReviewEventParentScope(event session.ReviewEvent) string {
	scope := session.NormalizeScopeRef(event.SourceScope)
	if scope.ParentScopeKind == "" && scope.ParentScopeID == "" {
		return ""
	}
	parent := session.NormalizeScopeRef(session.ScopeRef{Kind: scope.ParentScopeKind, ID: scope.ParentScopeID})
	if parent.IsZero() {
		return ""
	}
	return parent.String()
}
