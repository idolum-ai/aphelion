//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const maxReviewEventsPerTurn = 10

type reviewEventInlineSender interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}

type reviewEventKeyboardClearer interface {
	EditMessageTextWithoutInlineKeyboard(ctx context.Context, chatID int64, messageID int64, text string, parseMode string) error
}

type reviewEventDocumentSender interface {
	SendDocumentMessage(ctx context.Context, chatID int64, media core.Media, caption string, replyTo *int64) (int64, error)
}

func shouldGenerateReviewEvent(actor principal.Principal, key session.SessionKey) bool {
	if actor.Role != principal.RoleAdmin {
		return true
	}
	// Subordinate sessions from admin principals still produce digests.
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
		compact := ReviewEventDetailsExpandable(event)
		text := FormatReviewEventMessage(event)
		if compact {
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
		if err := r.store.MarkReviewDeliveredWithMessage(event.ID, msgID); err != nil {
			return err
		}
		if compact && !ReviewEventDetailsButtonOnly(event) {
			if _, err := r.sendReviewEventDetailsAttachment(ctx, key.ChatID, msgID, event); err != nil {
				return err
			}
		}
		if staleEvents, err := r.store.DismissPendingCapabilityReviewEvents(key.ChatID, reviewEventCapabilityRequestID(event), event.ID); err != nil {
			return err
		} else if len(staleEvents) > 0 {
			r.markStaleReviewEventMessages(ctx, key.ChatID, staleEvents)
		}
	}
	return nil
}

func (r *Runtime) sendReviewEventDetailsAttachment(ctx context.Context, chatID int64, replyTo int64, event session.ReviewEvent) (int64, error) {
	if r.outbound == nil || chatID == 0 || !ReviewEventDetailsExpandable(event) {
		return 0, nil
	}
	sender, ok := r.outbound.(reviewEventDocumentSender)
	if !ok {
		return 0, nil
	}
	text := strings.TrimSpace(FormatReviewEventDetailsMarkdown(event))
	if text == "" {
		return 0, nil
	}
	media := core.Media{
		Type:     "document",
		Data:     []byte(text),
		MimeType: "text/markdown; charset=utf-8",
		Filename: reviewEventDetailsAttachmentFilename(event),
	}
	caption := "Full child review details (safe projection)."
	return sender.SendDocumentMessage(ctx, chatID, media, caption, replyToMessageID(replyTo))
}

func reviewEventDetailsAttachmentFilename(event session.ReviewEvent) string {
	meta, _ := parseReviewEventArtifactMetadata(event)
	parts := []string{"review"}
	if agent := strings.TrimSpace(meta.AgentID); agent != "" {
		parts = append([]string{agent}, "review")
	} else if scope := session.NormalizeScopeRef(event.SourceScope); strings.TrimSpace(scope.DurableAgentID) != "" {
		parts = append([]string{strings.TrimSpace(scope.DurableAgentID)}, "review")
	}
	if !event.CreatedAt.IsZero() {
		parts = append(parts, event.CreatedAt.UTC().Format("20060102T150405Z"))
	} else if label := strings.TrimSpace(meta.IntervalLabel); label != "" {
		parts = append(parts, label)
	} else if event.ID > 0 {
		parts = append(parts, fmt.Sprintf("%d", event.ID))
	}
	return sanitizeReviewEventAttachmentFilename(strings.Join(parts, "-")) + ".md"
}

var reviewEventAttachmentFilenameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeReviewEventAttachmentFilename(value string) string {
	value = strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".-_")
	value = reviewEventAttachmentFilenameUnsafe.ReplaceAllString(value, "-")
	value = strings.Trim(value, ".-_")
	if value == "" {
		value = "review-event-details"
	}
	if len(value) > 96 {
		value = strings.Trim(value[:96], ".-_")
	}
	return value
}

func ReviewEventInlineRows(event session.ReviewEvent) [][]telegram.InlineButton {
	return ReviewEventInlineRowsExpanded(event, false)
}

func ReviewEventInlineRowsExpanded(event session.ReviewEvent, expanded bool) [][]telegram.InlineButton {
	rows := [][]telegram.InlineButton{}
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return [][]telegram.InlineButton{{
			{Text: "Reject", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionReject)},
			{Text: "Add mission", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionAdd)},
		}, {
			{Text: "Park", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionPark)},
			{Text: "Change", CallbackData: core.EncodeReviewEventCallbackData(event.ID, core.ReviewEventActionMissionAskEdit)},
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

func ReviewEventDetailsButtonOnly(event session.ReviewEvent) bool {
	switch reviewEventMetadataString(event, "request_via") {
	case "capability_request", "durable_agent.delegation_request":
		return true
	default:
		return false
	}
}

func ReviewEventDetailsExpandable(event session.ReviewEvent) bool {
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		return false
	}
	if strings.TrimSpace(event.Summary) == "" {
		return false
	}
	if ReviewEventDetailsButtonOnly(event) {
		return true
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

func (r *Runtime) markStaleReviewEventMessages(ctx context.Context, chatID int64, events []session.ReviewEvent) {
	clearer, ok := r.outbound.(reviewEventKeyboardClearer)
	if !ok {
		return
	}
	for _, event := range events {
		if event.DeliveryMessageID <= 0 {
			continue
		}
		text := FormatReviewEventCompactMessage(event) + "\n\nStale approval card — use the newest prompt."
		_ = clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, event.DeliveryMessageID, text, "")
	}
}
