//go:build linux

package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramcommands"
	"github.com/idolum-ai/aphelion/session"
)

const telegramThreadReminderSweepInterval = time.Hour
const telegramThreadReminderSweepChatLimit = 100
const telegramThreadReminderSweepActorID int64 = 0

func startTelegramThreadReminderSweepLoop(ctx context.Context, sender telegramcommands.Sender, router telegramcommands.ThreadRouter, store *session.SQLiteStore) {
	if sender == nil || router == nil || store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(telegramThreadReminderSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := sweepTelegramThreadReminders(ctx, sender, router, store, now.UTC()); err != nil {
					log.Printf("WARN telegram thread reminder sweep failed err=%v", err)
				}
			}
		}
	}()
}

func sweepTelegramThreadReminders(ctx context.Context, sender telegramcommands.Sender, router telegramcommands.ThreadRouter, store *session.SQLiteStore, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	chatIDs, err := store.ListTelegramThreadChatIDs(telegramThreadReminderSweepChatLimit)
	if err != nil {
		return err
	}
	policy := telegramcommands.DefaultTelegramThreadReminderSweepPolicy()
	for _, chatID := range chatIDs {
		result, sweepErr := telegramcommands.SendTelegramThreadReminderSweep(ctx, sender, router, chatID, telegramThreadReminderSweepActorID, now, policy)
		if _, err := store.ExpirePendingTelegramThreadReminders(chatID, now.Add(-7*24*time.Hour), now); err != nil && sweepErr == nil {
			sweepErr = err
		}
		_ = recordTelegramThreadReminderSweepEvent(store, chatID, result, sweepErr, now)
		if sweepErr != nil {
			return sweepErr
		}
	}
	return nil
}

func recordTelegramThreadReminderSweepEvent(store *session.SQLiteStore, chatID int64, result telegramcommands.TelegramThreadReminderSweepResult, sweepErr error, now time.Time) error {
	payload := map[string]any{"sent": result.Sent, "suppressed": result.Suppressed, "candidates": result.Candidates, "scanned": result.Scanned, "thread_ids": result.ThreadIDs}
	status := "completed"
	if sweepErr != nil {
		status = "failed"
		payload["error"] = sweepErr.Error()
	}
	raw, _ := json.Marshal(payload)
	_, err := store.AppendExecutionEvent(session.SessionKey{ChatID: chatID}, session.ExecutionEventInput{EventType: core.ExecutionEventTelegramThreadReminderSweep, Stage: "telegram_thread_reminders", Status: status, PayloadJSON: string(raw), CreatedAt: now})
	return err
}
