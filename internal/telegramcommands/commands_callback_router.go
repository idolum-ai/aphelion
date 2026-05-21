//go:build linux

package telegramcommands

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func handleTelegramCommandCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery) (bool, error) {
	if sender == nil || router == nil {
		return false, nil
	}
	if streamID, action, ok := core.DecodeStreamControlCallbackData(cb.Data); ok {
		return handleStreamControlCallback(ctx, sender, router, cb, streamID, action)
	}
	if runID, action, ok := core.DecodeDeliberationControlCallbackData(cb.Data); ok {
		return handleDeliberationControlCallback(ctx, sender, router, cb, runID, action)
	}
	if command, ok := decodeCommandMenuCallbackData(cb.Data); ok {
		return handleCommandMenuCallback(ctx, sender, router, cb, command)
	}
	if req, ok := decodeTelegramPageCallbackData(cb.Data); ok {
		return handleTelegramPageCallback(ctx, sender, router, cb, req)
	}
	if decodeTelegramThreadSummaryCallback(cb.Data) {
		return handleTelegramThreadSummaryCallback(ctx, sender, router, cb)
	}
	if threadID, ok := decodeTelegramThreadAbsorbCallback(cb.Data); ok {
		return handleTelegramThreadCallback(ctx, sender, router, cb, threadID)
	}
	if offerID, action, ok := decodeApprovalWindowCallbackData(cb.Data); ok {
		return handleApprovalWindowCallback(ctx, sender, router, cb, offerID, action)
	}
	if action, ok := decodeHealthCallbackData(cb.Data); ok {
		return handleHealthCallback(ctx, sender, router, cb, action)
	}
	if action, token, ok := decodeTailnetRevokeTokenCallbackData(cb.Data); ok {
		return handleTailnetRevokeTokenCallback(ctx, sender, router, cb, action, token)
	}
	if action, surfaceID, ok := decodeTailnetRevokeCallbackData(cb.Data); ok {
		return handleTailnetRevokeCallback(ctx, sender, router, cb, action, surfaceID)
	}
	if action, ok := decodeTailnetCallbackData(cb.Data); ok {
		return handleTailnetCallback(ctx, sender, router, cb, action)
	}
	if view, targetChatID, ok := decodeStatusCallbackData(cb.Data); ok {
		return handleStatusCallback(ctx, sender, router, cb, view, targetChatID)
	}
	if action, token, ok := decodeMissionCallbackData(cb.Data); ok {
		return handleMissionCallback(ctx, sender, router, cb, action, token)
	}
	if proposalID, action, ok := decodeActionProposalCallbackData(cb.Data); ok {
		return handleActionProposalCallback(ctx, sender, router, cb, proposalID, action)
	}
	if decisionID, action, ok := decodeContinuationCallbackData(cb.Data); ok {
		return handleContinuationCallback(ctx, sender, router, cb, decisionID, action)
	}
	if action, step, option, ok := decodeDurableWizardCallbackData(cb.Data); ok {
		return handleDurableWizardCallback(ctx, sender, router, cb, action, step, option)
	}
	if action, agentID, ok := decodeDurableAgentsCallbackData(cb.Data); ok {
		return handleDurableAgentsCallback(ctx, sender, router, cb, action, agentID)
	}
	if action, source, index, ok := decodeMemoryReviewCallbackData(cb.Data); ok {
		return handleMemoryReviewCallback(ctx, sender, router, cb, action, source, index)
	}
	if action, slot, value, ok := decodeModelCallbackData(cb.Data); ok {
		return handleModelCallback(ctx, sender, router, cb, action, slot, value)
	}
	return false, nil
}

func handleStatusCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, view statusView, targetChatID int64) (bool, error) {
	chatID := int64(0)
	messageID := int64(0)
	if cb.Message != nil {
		messageID = cb.Message.MessageID
		if cb.Message.Chat != nil {
			chatID = cb.Message.Chat.ID
		}
	}
	senderID := int64(0)
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if statusViewRequiresAdmin(view, chatID, targetChatID) && !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), adminStatusOnlyText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	personaEffort, governorEffort := router.CurrentEfforts()
	rendered, rows, err := renderStatusView(ctx, router, chatID, senderID, view, targetChatID, personaEffort, governorEffort)
	if err != nil {
		return true, err
	}
	if chatID == 0 {
		chatID = targetChatID
	}
	if chatID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleStatusCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if err := deliverStatusCallbackView(ctx, sender, chatID, messageID, rendered, rows); err != nil {
		return true, err
	}
	return true, nil
}

func handleActionProposalCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, proposalID string, action string) (bool, error) {
	targetMsg, targetErr := telegramCallbackTargetMessage(router, cb)
	if targetErr != nil {
		return true, targetErr
	}
	chatID := targetMsg.ChatID
	messageID := targetMsg.MessageID
	senderID := int64(0)
	if cb.From != nil {
		senderID = cb.From.ID
	}
	if targetMsg.SenderID == 0 {
		targetMsg.SenderID = senderID
	}
	missionID := missionIDFromActionProposalID(proposalID)
	if missionID == "" || chatID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleActionProposalCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	proposal, err := router.MissionActionProposal(ctx, chatID, senderID, missionID)
	if err != nil || strings.TrimSpace(proposal.ID) != proposalID {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleActionProposalCallbackText); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	mission, changed, err := router.ApplyMissionActionProposalDecision(ctx, chatID, senderID, missionID, action)
	if err != nil {
		return true, err
	}
	if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), ""); err != nil && !telegram.IsStaleCallbackQueryError(err) {
		return true, err
	}
	if messageID != 0 {
		text := continuationCallbackDisplayText(targetMsg, renderActionProposalDecision(proposal, mission, action, changed))
		if action == "approve" {
			rows, err := approvalWindowOfferRowsForSource(ctx, router, targetMsg, session.ApprovalWindowOfferSourceMission, proposalID, "mission_action")
			if err != nil {
				return true, err
			}
			if len(rows) > 0 {
				if err := sender.EditMessageTextWithInlineKeyboard(ctx, chatID, messageID, text, "", rows); err != nil {
					return true, err
				}
			} else if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
				return true, err
			}
		} else if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
			return true, err
		}
	}
	return true, nil
}
