//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	runtimepkg "github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func (h *telegramDecisionHandler) handleReviewEventCallback(ctx context.Context, cb telegram.CallbackQuery, eventID int64, action core.ReviewEventAction) error {
	if h == nil || h.sender == nil || h.store == nil {
		return nil
	}
	event, err := h.store.ReviewEventByID(eventID)
	if err != nil {
		return err
	}
	if event == nil {
		return h.answerReviewEventCallback(ctx, cb, "This review item is no longer available.")
	}
	if action == core.ReviewEventActionExpand || action == core.ReviewEventActionHide {
		if text, err := h.reviewEventDetailAuthorizationFailure(*event, cb); err != nil {
			return err
		} else if text != "" {
			return h.answerReviewEventCallback(ctx, cb, text)
		}
		return h.handleReviewEventDetailToggle(ctx, cb, *event, action == core.ReviewEventActionExpand)
	}
	if proposal, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		if reviewEventCallbackExpired(*event, time.Now()) {
			_ = h.editReviewEventCallbackMessage(ctx, cb, "Mission Control proposal timed out — use a fresh prompt.")
			return h.answerReviewEventCallback(ctx, cb, "Proposal timed out. Use a fresh prompt.")
		}
		return h.handleMissionControlProposalCallback(ctx, cb, *event, proposal, action)
	}
	if reviewEventCallbackExpired(*event, time.Now()) {
		_ = h.editReviewEventCallbackMessage(ctx, cb, "Approval timed out — use a fresh prompt.")
		return h.answerReviewEventCallback(ctx, cb, "Approval timed out. Use a fresh prompt.")
	}
	requestID := reviewEventCallbackCapabilityRequestID(*event)
	if requestID == "" {
		return h.answerReviewEventCallback(ctx, cb, "This review item is not actionable yet.")
	}
	record, ok, err := h.store.CapabilityRequest(requestID)
	if err != nil {
		return err
	}
	if !ok {
		return h.answerReviewEventCallback(ctx, cb, "Capability request not found.")
	}
	if !reviewEventRequestStillActionable(record, action) {
		return h.answerReviewEventCallback(ctx, cb, "This approval is no longer active. Use the newest prompt.")
	}
	fromID := int64(0)
	if cb.From != nil {
		fromID = cb.From.ID
	}
	status, reviewerRole, err := reviewEventCapabilityStatusForAction(record, action, fromID, event.TargetAdminChatID)
	if err != nil {
		_ = h.answerReviewEventCallback(ctx, cb, err.Error())
		return nil
	}
	review, err := h.store.AppendCapabilityReview(session.CapabilityReview{
		ReviewID:     fmt.Sprintf("capr-review-event-%d-%d", event.ID, time.Now().UnixNano()),
		RequestID:    record.RequestID,
		Reviewer:     fmt.Sprintf("telegram:%d", fromID),
		ReviewerRole: reviewerRole,
		Status:       status,
		Rationale:    fmt.Sprintf("telegram inline review event %d", event.ID),
	})
	if err != nil {
		return err
	}
	label := "approved"
	if review.Status == session.CapabilityReviewStatusParentApproved {
		label = "parent-approved"
	} else if review.Status == session.CapabilityReviewStatusRejected {
		label = "rejected"
	}
	_ = h.editReviewEventCallbackMessage(ctx, cb, reviewEventConfirmationText(label, record, *event))
	return h.answerReviewEventCallback(ctx, cb, "")
}

func (h *telegramDecisionHandler) handleMissionControlProposalCallback(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, proposal core.MissionControlProposal, action core.ReviewEventAction) error {
	if h == nil || h.store == nil {
		return nil
	}
	fromID := int64(0)
	if cb.From != nil {
		fromID = cb.From.ID
	}
	if fromID <= 0 || (event.TargetAdminChatID > 0 && fromID != event.TargetAdminChatID) {
		return h.answerReviewEventCallback(ctx, cb, "Only the target admin can review this Mission Control proposal.")
	}
	proposal = core.NormalizeMissionControlProposal(proposal)
	switch action {
	case core.ReviewEventActionMissionAdd:
		missionID := strings.TrimSpace(proposal.MissionID)
		if missionID == "" {
			missionID = fmt.Sprintf("mission-proposal-%d", event.ID)
		}
		owner := strings.TrimSpace(proposal.Owner)
		if owner == "" {
			owner = fmt.Sprintf("telegram:%d", fromID)
		}
		refs := append([]string(nil), proposal.SourceRefs...)
		refs = append(refs, fmt.Sprintf("review_event:%d", event.ID))
		mission, err := h.store.UpsertMission(session.MissionState{
			ID:                missionID,
			Title:             proposal.Title,
			Objective:         proposal.Objective,
			Origin:            firstTelegramDecisionNonEmpty(proposal.Origin, "proposed"),
			Scope:             firstTelegramDecisionNonEmpty(proposal.Scope, "principal"),
			Owner:             owner,
			Status:            session.MissionStatusCandidate,
			Pinned:            false,
			Tags:              proposal.Tags,
			SourceRefs:        refs,
			SuccessCriteria:   proposal.SuccessCriteria,
			NextAllowedAction: proposal.NextAllowedAction,
			Authority:         session.DefaultMissionAuthority(),
			Decay:             session.DefaultMissionDecay(),
		}, fmt.Sprintf("telegram:%d", fromID), "Mission Control proposal approved; candidate mission added")
		if err != nil {
			return err
		}
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("added", mission, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionAskEdit:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("ask_edit", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionPark:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("parked", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	case core.ReviewEventActionMissionReject:
		_ = h.editReviewEventCallbackMessage(ctx, cb, renderMissionControlProposalCallbackResult("rejected", session.MissionState{}, proposal))
		return h.answerReviewEventCallback(ctx, cb, "")
	default:
		return h.answerReviewEventCallback(ctx, cb, "This Mission Control proposal action is not available.")
	}
}

func renderMissionControlProposalCallbackResult(status string, mission session.MissionState, proposal core.MissionControlProposal) string {
	proposal = core.NormalizeMissionControlProposal(proposal)
	title := strings.TrimSpace(mission.Title)
	if title == "" {
		title = strings.TrimSpace(proposal.Title)
	}
	if title == "" {
		title = strings.TrimSpace(proposal.Objective)
	}
	switch strings.TrimSpace(status) {
	case "added":
		lines := []string{"Mission Control proposal added."}
		if mission.ID != "" {
			lines = append(lines, "Mission: "+mission.ID)
		}
		if title != "" {
			lines = append(lines, "Title: "+title)
		}
		lines = append(lines, "Status: candidate", "No execution or self-continuation authority was granted.")
		return strings.Join(lines, "\n")
	case "ask_edit":
		return "Mission Control proposal needs edits. I will revise it before asking again. No mission was created."
	case "parked":
		return "Mission Control proposal parked. No mission was created and no execution authority was granted."
	case "rejected":
		return "Mission Control proposal rejected. No mission was created."
	default:
		return "Mission Control proposal reviewed."
	}
}

func firstTelegramDecisionNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (h *telegramDecisionHandler) handleReviewEventDetailToggle(ctx context.Context, cb telegram.CallbackQuery, event session.ReviewEvent, expanded bool) error {
	if !runtimepkg.ReviewEventDetailsExpandable(event) {
		return h.answerReviewEventCallback(ctx, cb, "This review item has no expandable details.")
	}
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return h.answerReviewEventCallback(ctx, cb, "")
	}
	text := runtimepkg.FormatReviewEventCompactMessage(event)
	if expanded {
		text = runtimepkg.FormatReviewEventDetailsMessage(event)
	}
	rows := runtimepkg.ReviewEventInlineRowsExpanded(event, expanded)
	if editor, ok := h.sender.(telegramDecisionKeyboardEditor); ok && len(rows) > 0 {
		if err := editor.EditMessageTextWithInlineKeyboard(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, "", rows); err != nil {
			return err
		}
	} else if err := editDecisionMessageClearingInlineKeyboard(ctx, h.sender, cb.Message.Chat.ID, cb.Message.MessageID, text); err != nil {
		return err
	}
	return h.answerReviewEventCallback(ctx, cb, "")
}

func (h *telegramDecisionHandler) reviewEventDetailAuthorizationFailure(event session.ReviewEvent, cb telegram.CallbackQuery) (string, error) {
	fromID := callbackSenderID(cb)
	if fromID <= 0 {
		return "Only the target reviewer can view these review details.", nil
	}
	if _, ok := core.MissionControlProposalFromMetadataJSON(event.MetadataJSON); ok {
		if event.TargetAdminChatID > 0 && fromID == event.TargetAdminChatID {
			return "", nil
		}
		return "Only the target admin can view this Mission Control proposal.", nil
	}
	requestID := reviewEventCallbackCapabilityRequestID(event)
	if requestID != "" {
		record, ok, err := h.store.CapabilityRequest(requestID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "Capability request not found.", nil
		}
		if reviewEventCapabilityActorCanViewDetails(record, fromID, event.TargetAdminChatID) {
			return "", nil
		}
		return "Only the admin or parent can view these review details.", nil
	}
	if event.TargetAdminChatID > 0 && fromID == event.TargetAdminChatID {
		return "", nil
	}
	return "Only the target admin can view these review details.", nil
}

func reviewEventCapabilityActorCanViewDetails(record session.CapabilityRequest, fromID int64, targetChatID int64) bool {
	if fromID <= 0 {
		return false
	}
	record = session.NormalizeCapabilityRequest(record)
	isAdmin := telegramPrincipalMatches(record.AdminPrincipal, fromID) || (strings.TrimSpace(record.AdminPrincipal) == "" && targetChatID == fromID)
	isParent := telegramPrincipalMatches(record.ParentPrincipal, fromID)
	return isAdmin || isParent
}

func reviewEventConfirmationText(label string, record session.CapabilityRequest, event session.ReviewEvent) string {
	record = session.NormalizeCapabilityRequest(record)
	label = strings.TrimSpace(label)
	if label == "" {
		label = "reviewed"
	}
	lines := []string{"Capability request " + label + "."}
	if record.RequestID != "" {
		lines = append(lines, "Request: "+record.RequestID)
	}
	if event.ID > 0 {
		lines = append(lines, fmt.Sprintf("Review event: %d", event.ID))
	}
	meta := make([]string, 0, 3)
	if record.Kind != "" {
		meta = append(meta, "Kind: "+string(record.Kind))
	}
	if target := strings.TrimSpace(record.TargetResource); target != "" {
		meta = append(meta, "Target: "+target)
	}
	if risk := strings.TrimSpace(record.RiskClass); risk != "" {
		meta = append(meta, "Risk: "+risk)
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, " · "))
	}
	if purpose := strings.TrimSpace(record.Purpose); purpose != "" {
		lines = append(lines, "Purpose: "+compactSentence(purpose))
	}
	if summary := strings.TrimSpace(event.Summary); summary != "" {
		lines = append(lines, "", "Approved content:", summary)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (h *telegramDecisionHandler) answerReviewEventCallback(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil {
		return nil
	}
	if err := h.sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), text); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return err
	}
	return nil
}

func (h *telegramDecisionHandler) editReviewEventCallbackMessage(ctx context.Context, cb telegram.CallbackQuery, text string) error {
	if h == nil || h.sender == nil || cb.Message == nil || cb.Message.Chat == nil || cb.Message.MessageID == 0 {
		return nil
	}
	return editDecisionMessageClearingInlineKeyboard(ctx, h.sender, cb.Message.Chat.ID, cb.Message.MessageID, text)
}

func reviewEventCallbackExpired(event session.ReviewEvent, now time.Time) bool {
	start := event.DeliveredAt
	if start.IsZero() {
		start = event.CreatedAt
	}
	if start.IsZero() {
		return false
	}
	return now.After(start.Add(defaultUserApprovalTimeout))
}

func reviewEventCallbackCapabilityRequestID(event session.ReviewEvent) string {
	if id := reviewEventCallbackMetadataString(event, "request_id"); id != "" {
		return id
	}
	return reviewEventCallbackMetadataString(event, "capability_request_id")
}

func reviewEventCallbackMetadataString(event session.ReviewEvent, key string) string {
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

func reviewEventRequestStillActionable(record session.CapabilityRequest, action core.ReviewEventAction) bool {
	record = session.NormalizeCapabilityRequest(record)
	switch action {
	case core.ReviewEventActionParentApprove:
		return record.ReviewStatus == session.CapabilityReviewStatusProposed
	case core.ReviewEventActionApprove:
		if strings.TrimSpace(record.ParentPrincipal) != "" {
			return record.ReviewStatus == session.CapabilityReviewStatusParentApproved
		}
		return record.ReviewStatus == session.CapabilityReviewStatusProposed
	case core.ReviewEventActionReject:
		return record.ReviewStatus == session.CapabilityReviewStatusProposed || record.ReviewStatus == session.CapabilityReviewStatusParentApproved
	default:
		return false
	}
}

func reviewEventCapabilityStatusForAction(record session.CapabilityRequest, action core.ReviewEventAction, fromID int64, targetChatID int64) (session.CapabilityReviewStatus, string, error) {
	if fromID <= 0 {
		return "", "", fmt.Errorf("Telegram reviewer is unknown.")
	}
	isAdmin := telegramPrincipalMatches(record.AdminPrincipal, fromID) || (strings.TrimSpace(record.AdminPrincipal) == "" && targetChatID == fromID)
	isParent := telegramPrincipalMatches(record.ParentPrincipal, fromID)
	switch action {
	case core.ReviewEventActionParentApprove:
		if strings.TrimSpace(record.ParentPrincipal) == "" {
			return "", "", fmt.Errorf("This request has no parent approval step.")
		}
		if !isParent && !isAdmin {
			return "", "", fmt.Errorf("Only the parent or admin can parent-approve this request.")
		}
		return session.CapabilityReviewStatusParentApproved, reviewerRoleForReview(isAdmin && !isParent), nil
	case core.ReviewEventActionApprove:
		if strings.TrimSpace(record.ParentPrincipal) != "" && record.ReviewStatus == session.CapabilityReviewStatusProposed {
			return "", "", fmt.Errorf("Parent approval is required first.")
		}
		if !isAdmin {
			return "", "", fmt.Errorf("Only the admin can approve this request.")
		}
		return session.CapabilityReviewStatusApproved, string(principal.RoleAdmin), nil
	case core.ReviewEventActionReject:
		if !isAdmin && !isParent {
			return "", "", fmt.Errorf("Only the admin or parent can reject this request.")
		}
		return session.CapabilityReviewStatusRejected, reviewerRoleForReview(isAdmin), nil
	default:
		return "", "", fmt.Errorf("Unknown review action.")
	}
}

func reviewerRoleForReview(admin bool) string {
	if admin {
		return string(principal.RoleAdmin)
	}
	return string(principal.RoleApprovedUser)
}

func telegramPrincipalMatches(target string, userID int64) bool {
	return userID > 0 && strings.TrimSpace(target) == fmt.Sprintf("telegram:%d", userID)
}
