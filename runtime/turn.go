//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/prompt"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"github.com/idolum-ai/aphelion/turn"
)

const maxReviewEventsPerTurn = 10

type reviewEventInlineSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}

func (r *Runtime) HandleInbound(ctx context.Context, msg core.InboundMessage) (result *core.TurnResult, err error) {
	return r.handleInteractiveInbound(ctx, msg, nil)
}

func (r *Runtime) handleInternalContinuation(ctx context.Context, actor principal.Principal, msg core.InboundMessage) (result *core.TurnResult, err error) {
	if actor.TelegramUserID <= 0 && strings.TrimSpace(actor.DurableAgentID) == "" {
		return nil, ErrPrincipalDenied
	}
	return r.handleInteractiveInbound(ctx, msg, &actor)
}

func (r *Runtime) handleInteractiveInbound(ctx context.Context, msg core.InboundMessage, forcedActor *principal.Principal) (result *core.TurnResult, err error) {
	if strings.TrimSpace(msg.DurableAgentID) != "" {
		return r.handleDurableTelegramGroupInbound(ctx, msg)
	}
	actor := principal.Principal{}
	if forcedActor != nil {
		actor = *forcedActor
	} else {
		resolved, ok := r.resolver.ResolveTelegramUser(msg.SenderID)
		if !ok {
			return nil, ErrPrincipalDenied
		}
		actor = resolved
	}
	if handled, result, err := r.maybeHandleTypedContinuationApproval(ctx, msg, actor); handled {
		return result, err
	}
	stopTyping := r.startChatActionLoop(ctx, msg.ChatID, "typing")
	defer stopTyping()
	defer r.clearChatTurnPhase(msg.ChatID)

	key := session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramDMScopeRef(msg.ChatID)}
	unlock := r.lockSession(key)
	defer unlock()

	tools := r.toolsForPrincipal(actor, key)

	scope, err := r.scopeForPrincipal(actor)
	if err != nil {
		return nil, fmt.Errorf("resolve principal scope: %w", err)
	}
	if handled, result, err := r.maybeHandleOperationArtifactRequest(ctx, key, scope, msg); handled {
		return result, err
	}
	eventAwareness := turn.EventAwareness{Origin: inboundOriginLabel(msg)}
	if msg.Origin == core.InboundOriginTurnAuthorization {
		eventAwareness.TurnAuthorizationKind = inboundOriginDetailLabel(msg)
	}
	assembler := r.interactiveDMAssembler
	if assembler == nil {
		assembler = newInteractiveDMTurnAssembler(r)
	}
	return assembler.Run(ctx, interactiveDMTurnAssemblyInput{
		Msg:            msg,
		Actor:          actor,
		Key:            key,
		Scope:          scope,
		Tools:          tools,
		EventAwareness: eventAwareness,
	})
}

type faceUsageConsumer interface {
	ConsumeLastUsage() core.TokenUsage
}

func consumeFaceUsage(model face.Renderer) core.TokenUsage {
	consumer, ok := model.(faceUsageConsumer)
	if !ok {
		return core.TokenUsage{}
	}
	return consumer.ConsumeLastUsage()
}

func addTokenUsage(dst core.TokenUsage, src core.TokenUsage) core.TokenUsage {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.TotalTokens += src.TotalTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	return dst
}

func replaceLastAssistantWithSceneText(messages []session.Message, sceneText string) []session.Message {
	trimmed := strings.TrimSpace(sceneText)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			messages[i].Content = trimmed
			messages[i].ContentChars = len(trimmed)
			return messages
		}
	}
	if trimmed == "" {
		return messages
	}

	turnIndex := 0
	if len(messages) > 0 {
		turnIndex = messages[len(messages)-1].TurnIndex
	}
	return append(messages, session.Message{
		Role:         "assistant",
		Content:      trimmed,
		ContentChars: len(trimmed),
		TurnIndex:    turnIndex,
	})
}

func shouldGenerateReviewEvent(actor principal.Principal, key session.SessionKey) bool {
	if actor.Role != principal.RoleAdmin {
		return true
	}
	// Future-compatible hook: subordinate sessions from admin principals still produce digests.
	return key.UserID != 0
}

func (r *Runtime) enqueueReviewEventsForTurn(
	actor principal.Principal,
	msg core.InboundMessage,
	turnIndex int,
	userText string,
	sceneText string,
	toolLog []string,
) error {
	targets := uniquePositiveIDs(r.cfg.Principals.Telegram.AdminUserIDs)
	if len(targets) == 0 {
		return nil
	}

	summary := session.BuildReviewSummary(session.ReviewSummaryInput{
		SourceChatID: msg.ChatID,
		SourceUserID: msg.SenderID,
		SourceRole:   string(actor.Role),
		SourceScope:  telegramDMScopeRef(msg.ChatID),
		TurnIndex:    turnIndex,
		UserText:     userText,
		SceneText:    sceneText,
		ToolLog:      toolLog,
	}, session.DefaultReviewSummaryMaxChars)

	for _, adminChatID := range targets {
		if err := r.store.EnqueueReviewEvent(session.ReviewEvent{
			SourceChatID:      msg.ChatID,
			SourceUserID:      msg.SenderID,
			SourceRole:        string(actor.Role),
			SourceScope:       telegramDMScopeRef(msg.ChatID),
			TargetAdminChatID: adminChatID,
			TargetScope:       telegramDMScopeRef(adminChatID),
			TurnFrom:          turnIndex,
			TurnTo:            turnIndex,
			Summary:           summary,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) deliverReviewEvents(ctx context.Context, key session.SessionKey, sess *session.Session) error {
	events, err := r.store.PendingReviewEvents(key.ChatID, maxReviewEventsPerTurn)
	if err != nil {
		return err
	}
	for _, event := range events {
		text := FormatReviewEventMessage(event)
		if ReviewEventDetailsExpandable(event) {
			text = FormatReviewEventCompactMessage(event)
		}
		rows := ReviewEventInlineRows(event)
		msgID := int64(0)
		if len(rows) > 0 {
			inline, ok := r.outbound.(reviewEventInlineSender)
			if !ok && reviewEventCapabilityRequestID(event) != "" {
				return fmt.Errorf("review event %d requires inline approval delivery but outbound sender does not support inline keyboards", event.ID)
			}
			if ok {
				var sendErr error
				msgID, sendErr = inline.SendInlineKeyboard(ctx, key.ChatID, text, rows, nil)
				if sendErr != nil {
					return sendErr
				}
			} else {
				var sendErr error
				msgID, sendErr = r.outbound.SendMessage(ctx, core.OutboundMessage{
					ChatID: key.ChatID,
					Text:   text,
				})
				if sendErr != nil {
					return sendErr
				}
			}
		} else {
			var sendErr error
			msgID, sendErr = r.outbound.SendMessage(ctx, core.OutboundMessage{
				ChatID: key.ChatID,
				Text:   text,
			})
			if sendErr != nil {
				return sendErr
			}
		}
		newMessages := appendAssistantTurn(sess, text, text, "")
		if err := r.store.Save(sess, newMessages, core.TokenUsage{}); err != nil {
			return err
		}
		if err := r.store.RecordOutbound(key, sess.TurnCount, msgID, "review_digest"); err != nil {
			return err
		}
		if err := r.store.MarkReviewDelivered([]int64{event.ID}); err != nil {
			return err
		}
	}
	return nil
}

func ReviewEventInlineRows(event session.ReviewEvent) [][]telegram.InlineButton {
	return ReviewEventInlineRowsExpanded(event, false)
}

func ReviewEventInlineRowsExpanded(event session.ReviewEvent, expanded bool) [][]telegram.InlineButton {
	rows := [][]telegram.InlineButton{}
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return [][]telegram.InlineButton{{
			{Text: "Add mission", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionAdd)},
			{Text: "Ask edit", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionAskEdit)},
		}, {
			{Text: "Park", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionPark)},
			{Text: "Reject", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionReject)},
		}}
	}
	if ReviewEventDetailsExpandable(event) {
		action := core.ReviewEventActionExpand
		label := "Details"
		if expanded {
			action = core.ReviewEventActionHide
			label = "Hide details"
		}
		rows = append(rows, []telegram.InlineButton{{Text: label, CallbackData: core.EncodeReviewEventCallbackData(event.ID, action)}})
	}
	requestID := reviewEventCapabilityRequestID(event)
	if requestID == "" {
		return rows
	}
	if reviewEventMetadataString(event, "parent_principal") != "" && reviewEventMetadataString(event, "review_status") == string(session.CapabilityReviewStatusProposed) {
		rows = append(rows, []telegram.InlineButton{{Text: "Parent approve", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionParentApprove)}})
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "Reject", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionReject)},
		{Text: "Approve", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionApprove)},
	})
	return rows
}

func ReviewEventDetailsExpandable(event session.ReviewEvent) bool {
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return false
	}
	if strings.TrimSpace(event.Summary) == "" {
		return false
	}
	scope := session.NormalizeScopeRef(event.SourceScope)
	return scope.Kind == session.ScopeKindDurableAgent || strings.TrimSpace(scope.DurableAgentID) != "" || strings.TrimSpace(event.SourceRole) == "durable_agent"
}

func reviewEventCapabilityRequestID(event session.ReviewEvent) string {
	if id := reviewEventMetadataString(event, "request_id"); id != "" {
		return id
	}
	return reviewEventMetadataString(event, "capability_request_id")
}

func reviewEventMetadataString(event session.ReviewEvent, key string) string {
	key = strings.TrimSpace(key)
	if key == "" || strings.TrimSpace(event.MetadataJSON) == "" {
		return ""
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(event.MetadataJSON), &metadata); err != nil {
		return ""
	}
	if value, ok := metadata[key]; ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}

func uniquePositiveIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

type reviewEventArtifactMetadata struct {
	AgentID       string            `json:"agent_id"`
	Summary       string            `json:"summary"`
	IntervalLabel string            `json:"interval_label"`
	LocalActions  []string          `json:"local_actions"`
	Questions     []string          `json:"questions"`
	RiskFlags     []string          `json:"risk_flags"`
	ArtifactRefs  []string          `json:"artifact_refs"`
	Metadata      map[string]string `json:"metadata"`
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
		inspectCommand = "/debug"
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
	if strings.EqualFold(strings.TrimSpace(meta.Metadata["channel_kind"]), "email") {
		return "Email child review"
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
	case "email":
		return "Email"
	case "daily_review":
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

func FormatMissionControlProposalMessage(proposal core.MissionControlProposal) string {
	proposal = core.NormalizeMissionControlProposal(proposal)
	lines := []string{"Mission Control Proposal"}
	if title := strings.TrimSpace(proposal.Title); title != "" {
		lines = append(lines, "", "Title:", title)
	}
	if objective := strings.TrimSpace(proposal.Objective); objective != "" {
		lines = append(lines, "", "Objective:", objective)
	}
	if why := strings.TrimSpace(proposal.WhyProposed); why != "" {
		lines = append(lines, "", "Why I’m proposing it:", why)
	}
	if scope := strings.TrimSpace(proposal.Scope); scope != "" {
		lines = append(lines, "", "Suggested state:", "candidate mission, review-only", "scope: "+scope)
	}
	if next := strings.TrimSpace(proposal.NextAllowedAction); next != "" {
		lines = append(lines, "", "Next allowed action:", next)
	}
	if len(proposal.NotIncluded) > 0 {
		lines = append(lines, "", "Not included:")
		for _, item := range proposal.NotIncluded {
			lines = append(lines, "- "+item)
		}
	}
	lines = append(lines, "", "Adding this only creates a candidate. It does not start execution or grant self-continuation.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
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

func toolManifest(registry agent.ToolRegistry) string {
	if registry == nil {
		return ""
	}
	type manifestProvider interface {
		Manifest() string
	}
	if provider, ok := registry.(manifestProvider); ok {
		return provider.Manifest()
	}
	return renderToolManifest(registry.Definitions())
}

func toolCapabilities(registry agent.ToolRegistry) prompt.ToolCapabilities {
	if registry == nil {
		return prompt.ToolCapabilities{}
	}
	return prompt.ToolCapabilitiesFromDefs(registry.Definitions())
}

func renderToolManifest(defs []agent.ToolDef) string {
	if len(defs) == 0 {
		return ""
	}

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func inboundOriginLabel(msg core.InboundMessage) string {
	origin := strings.TrimSpace(string(msg.Origin))
	if origin == "" {
		return string(core.InboundOriginUser)
	}
	return origin
}

func inboundOriginDetailLabel(msg core.InboundMessage) string {
	return strings.TrimSpace(msg.OriginDetail)
}
