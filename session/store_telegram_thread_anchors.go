//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type TelegramThreadMessageAnchor struct {
	ChatID    int64
	ThreadID  int64
	MessageID int64
	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *SQLiteStore) RecordTelegramThreadLastMessage(chatID int64, threadID int64, messageID int64, source string, at time.Time) error {
	if s == nil || s.db == nil || chatID == 0 || threadID <= 0 || messageID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	atRaw := at.UTC().Format(time.RFC3339Nano)
	source = clampStoreText(strings.TrimSpace(source), 120)
	if source == "" {
		source = "unknown"
	}
	if _, err := s.db.Exec(`
		INSERT INTO telegram_thread_last_messages(chat_id, thread_id, message_id, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, thread_id) DO UPDATE SET
			message_id = CASE
				WHEN excluded.message_id > telegram_thread_last_messages.message_id THEN excluded.message_id
				ELSE telegram_thread_last_messages.message_id
			END,
			source = CASE
				WHEN excluded.message_id > telegram_thread_last_messages.message_id THEN excluded.source
				ELSE telegram_thread_last_messages.source
			END,
			updated_at = CASE
				WHEN excluded.message_id > telegram_thread_last_messages.message_id THEN excluded.updated_at
				ELSE telegram_thread_last_messages.updated_at
			END
	`, chatID, threadID, messageID, source, atRaw, atRaw); err != nil {
		return fmt.Errorf("record telegram thread last message: %w", err)
	}
	if _, err := s.db.Exec(`
		UPDATE telegram_threads
		SET last_activity_at = ?, updated_at = ?
		WHERE chat_id = ? AND thread_id = ? AND status = ?
	`, atRaw, atRaw, chatID, threadID, string(TelegramThreadStatusOpen)); err != nil {
		return fmt.Errorf("touch telegram thread last message: %w", err)
	}
	return nil
}

func (s *SQLiteStore) TelegramThreadLastMessage(chatID int64, threadID int64) (TelegramThreadMessageAnchor, bool, error) {
	if s == nil || s.db == nil || chatID == 0 || threadID <= 0 {
		return TelegramThreadMessageAnchor{}, false, nil
	}
	var anchor TelegramThreadMessageAnchor
	var createdRaw, updatedRaw string
	err := s.db.QueryRow(`
		SELECT chat_id, thread_id, message_id, source, created_at, updated_at
		FROM telegram_thread_last_messages
		WHERE chat_id = ? AND thread_id = ?
	`, chatID, threadID).Scan(&anchor.ChatID, &anchor.ThreadID, &anchor.MessageID, &anchor.Source, &createdRaw, &updatedRaw)
	if err == sql.ErrNoRows {
		return TelegramThreadMessageAnchor{}, false, nil
	}
	if err != nil {
		return TelegramThreadMessageAnchor{}, false, fmt.Errorf("load telegram thread last message: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return TelegramThreadMessageAnchor{}, false, fmt.Errorf("parse telegram thread last message created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return TelegramThreadMessageAnchor{}, false, fmt.Errorf("parse telegram thread last message updated_at: %w", err)
	}
	anchor.CreatedAt = createdAt
	anchor.UpdatedAt = updatedAt
	return anchor, anchor.MessageID > 0, nil
}
