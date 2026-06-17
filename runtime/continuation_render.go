//go:build linux

package runtime

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	continuationCallbackSurface        = "continuation"
	continuationCallbackRetiredSurface = "continuation_retired"
	continuationCardRetireWindow       = 24 * time.Hour
	continuationCardRetireLimit        = 25
)

const retiredContinuationCardText = "This continuation prompt is no longer active. Use the newest prompt."

func (r *Runtime) sendContinuationApprovalPrompt(ctx context.Context, key session.SessionKey, msg core.InboundMessage, state session.ContinuationState, text string) error {
	if _, blocked, err := r.blockInvalidContinuationAuthorityContract(ctx, key, msg, state, "approval_prompt", time.Now().UTC(), false); blocked || err != nil {
		return err
	}
	sender, ok := r.continuationApprovalPromptSender()
	if !ok {
		return nil
	}
	messageID, err := sender.SendInlineKeyboard(
		ctx,
		msg.ChatID,
		r.prefixTelegramPresentedText(r.telegramPresentationForMessage(msg), text),
		continuationApprovalButtonRows(state),
		nil,
	)
	if err != nil {
		return fmt.Errorf("send continuation approval: %w", err)
	}
	if messageID > 0 && r.store != nil {
		now := time.Now().UTC()
		state.DecisionMessageID = messageID
		if err := r.store.UpdateContinuationState(key, session.NormalizeContinuationState(state)); err != nil {
			return fmt.Errorf("record continuation approval message: %w", err)
		}
		threadID := continuationCallbackThreadIDForMessage(key, msg)
		if err := r.store.RecordTelegramCallbackMessage(msg.ChatID, messageID, threadID, continuationCallbackSurface, now); err != nil {
			return fmt.Errorf("record continuation callback message: %w", err)
		}
		r.retireStaleContinuationApprovalCards(ctx, key, msg.ChatID, threadID, messageID, "new_prompt", now)
	}
	return nil
}

func (r *Runtime) continuationApprovalPromptSender() (interface {
	SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
}, bool) {
	if r == nil || r.outbound == nil {
		return nil, false
	}
	sender, ok := r.outbound.(interface {
		SendInlineKeyboard(ctx context.Context, chatID int64, text string, rows [][]telegram.InlineButton, replyTo *int64) (int64, error)
	})
	return sender, ok
}

func continuationCallbackThreadIDForKey(key session.SessionKey) int64 {
	threadID, ok := session.TelegramThreadIDFromScope(key.ChatID, key.Scope)
	if !ok {
		return 0
	}
	return threadID
}

func continuationCallbackThreadIDForMessage(key session.SessionKey, msg core.InboundMessage) int64 {
	if msg.TelegramThreadID > 0 {
		return msg.TelegramThreadID
	}
	return continuationCallbackThreadIDForKey(key)
}

func (r *Runtime) retireStaleContinuationApprovalCards(ctx context.Context, key session.SessionKey, chatID int64, threadID int64, keepMessageID int64, reason string, now time.Time) {
	if r == nil || r.store == nil || r.outbound == nil || chatID == 0 {
		return
	}
	clearer, ok := r.outbound.(messageKeyboardClearer)
	if !ok {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "stale"
	}
	if threadID < 0 {
		threadID = 0
	}
	records, err := r.store.ListTelegramCallbackMessagesForThread(chatID, threadID, continuationCallbackSurface, now.Add(-continuationCardRetireWindow), continuationCardRetireLimit)
	if err != nil {
		r.recordContinuationCardRetirementEvent(key, chatID, threadID, 0, reason, "list_failed", err, now)
		log.Printf("WARN continuation card cleanup list failed chat_id=%d thread_id=%d err=%v", chatID, threadID, err)
		return
	}
	for _, record := range records {
		if record.MessageID <= 0 || record.MessageID == keepMessageID {
			continue
		}
		err := clearer.EditMessageTextWithoutInlineKeyboard(ctx, chatID, record.MessageID, retiredContinuationCardText, "")
		if err != nil && !telegramMessageNotModifiedError(err) {
			r.recordContinuationCardRetirementEvent(key, chatID, threadID, record.MessageID, reason, "edit_failed", err, now)
			log.Printf("WARN continuation card cleanup edit failed chat_id=%d thread_id=%d message_id=%d err=%v", chatID, threadID, record.MessageID, err)
			continue
		}
		if markErr := r.store.RecordTelegramCallbackMessage(chatID, record.MessageID, record.ThreadID, continuationCallbackRetiredSurface, now); markErr != nil {
			r.recordContinuationCardRetirementEvent(key, chatID, threadID, record.MessageID, reason, "mark_failed", markErr, now)
			log.Printf("WARN continuation card cleanup mark failed chat_id=%d thread_id=%d message_id=%d err=%v", chatID, threadID, record.MessageID, markErr)
			continue
		}
		r.recordContinuationCardRetirementEvent(key, chatID, threadID, record.MessageID, reason, "retired", nil, now)
	}
}

func (r *Runtime) recordContinuationCardRetirementEvent(key session.SessionKey, chatID int64, threadID int64, messageID int64, reason string, status string, err error, at time.Time) {
	if r == nil || r.store == nil {
		return
	}
	payload := map[string]any{
		"chat_id": chatID,
		"reason":  strings.TrimSpace(reason),
	}
	if threadID > 0 {
		payload["thread_id"] = threadID
	}
	if messageID > 0 {
		payload["message_id"] = messageID
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	r.recordExecutionEvent(key, core.ExecutionEventTelegramCallbackRetired, "telegram_callback", status, payload, at)
}

func telegramMessageNotModifiedError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "message is not modified") || strings.Contains(value, "not modified")
}
