//go:build linux

package telegramcommands

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const staleReentryRecommendationCallback = "This recommendation is no longer active."

func handleReentryRecommendationCallback(ctx context.Context, sender commandCallbackSender, router commandRouter, cb telegram.CallbackQuery, recommendationID string, candidateID string, action string) (bool, error) {
	targetMsg, err := telegramCallbackTargetMessage(router, cb)
	if err != nil {
		return true, err
	}
	senderID := callbackSenderID(cb)
	if targetMsg.ChatID == 0 || targetMsg.MessageID == 0 || senderID == 0 {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleReentryRecommendationCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	if !router.CanRestart(senderID) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Re-entry controls are available to Telegram admins only."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		return true, nil
	}
	record, ok, err := router.ReentryRecommendation(ctx, senderID, recommendationID)
	if err != nil {
		return true, err
	}
	if !ok || session.ReentryRecommendationStatusTerminal(record.Status) {
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleReentryRecommendationCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		editReentryRecommendationCallbackMessage(ctx, sender, router, targetMsg.ChatID, targetMsg.MessageID, "reentry_recommendation.stale", "Recommendation is no longer active.")
		return true, nil
	}
	switch action {
	case core.ReentryRecommendationCallbackIgnore:
		if _, err := router.IgnoreReentryRecommendation(ctx, senderID, recommendationID); err != nil {
			return true, err
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Ignored."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		editReentryRecommendationCallbackMessage(ctx, sender, router, targetMsg.ChatID, targetMsg.MessageID, "reentry_recommendation.ignore", "Ignored recommendation.")
		return true, nil
	case core.ReentryRecommendationCallbackSelect:
		candidate, ok := record.Candidate(candidateID)
		if !ok {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleReentryRecommendationCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			return true, nil
		}
		queued := targetMsg
		queued.SenderID = senderID
		queued.Text = reentryRecommendationSelectionPrompt(record, candidate)
		queued.IngressSurface = telegramReentryRecommendationIngressSurface
		queued.IngressUpdateID = cb.UpdateID
		_, selectedCandidate, selected, err := router.QueueReentryRecommendation(ctx, queued, recommendationID, candidate.ID)
		if err != nil {
			return true, err
		}
		if !selected {
			if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), staleReentryRecommendationCallback); err != nil && !telegram.IsStaleCallbackQueryError(err) {
				return true, err
			}
			editReentryRecommendationCallbackMessage(ctx, sender, router, targetMsg.ChatID, targetMsg.MessageID, "reentry_recommendation.select_stale", "Recommendation is stale. Send a fresh instruction if you still want to continue.")
			return true, nil
		}
		if selectedCandidate.ID != "" {
			candidate = selectedCandidate
		}
		if err := sender.AnswerCallbackQuery(ctx, strings.TrimSpace(cb.ID), "Queued."); err != nil && !telegram.IsStaleCallbackQueryError(err) {
			return true, err
		}
		editReentryRecommendationCallbackMessage(ctx, sender, router, targetMsg.ChatID, targetMsg.MessageID, "reentry_recommendation.select", fmt.Sprintf("Queued re-entry path: %s.", strings.TrimSpace(candidate.Label)))
		return true, nil
	default:
		return false, nil
	}
}

func editReentryRecommendationCallbackMessage(ctx context.Context, sender commandCallbackSender, router commandRouter, chatID int64, messageID int64, callbackKind string, text string) {
	if messageID == 0 {
		return
	}
	if err := editCallbackMessageClearingInlineKeyboard(ctx, sender, chatID, messageID, text); err != nil {
		recordTelegramCallbackError(router, chatID, callbackKind+".edit", err)
		log.Printf("WARN reentry recommendation callback message update failed chat_id=%d message_id=%d kind=%s err=%v", chatID, messageID, strings.TrimSpace(callbackKind), err)
	}
}

func reentryRecommendationSelectionPrompt(record session.ReentryRecommendation, candidate session.ReentryRecommendationCandidate) string {
	parts := []string{
		strings.TrimSpace(candidate.PromptText),
		fmt.Sprintf("Recommendation id: %s", strings.TrimSpace(record.ID)),
		fmt.Sprintf("Candidate id: %s", strings.TrimSpace(candidate.ID)),
	}
	if summary := strings.TrimSpace(candidate.Summary); summary != "" {
		parts = append(parts, "Candidate summary: "+summary)
	}
	return strings.Join(parts, "\n\n")
}
