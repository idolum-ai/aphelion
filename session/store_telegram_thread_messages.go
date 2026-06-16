//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type TelegramCallbackMessage struct {
	ChatID    int64
	MessageID int64
	ThreadID  int64
	Surface   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *SQLiteStore) RecordTelegramCallbackMessage(chatID int64, messageID int64, threadID int64, surface string, at time.Time) error {
	if chatID == 0 || messageID <= 0 {
		return nil
	}
	if threadID < 0 {
		threadID = 0
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return recordTelegramCallbackMessageTx(s.db, chatID, messageID, threadID, surface, at)
}

func (s *SQLiteStore) RecordTelegramCallbackMessageThread(chatID int64, messageID int64, threadID int64, surface string, at time.Time) error {
	if chatID == 0 || messageID <= 0 || threadID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return recordTelegramCallbackMessageTx(s.db, chatID, messageID, threadID, surface, at)
}

func (s *SQLiteStore) ClearTelegramCallbackMessageThread(chatID int64, messageID int64, surface string, at time.Time) error {
	if chatID == 0 || messageID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	atRaw := at.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO telegram_callback_messages(chat_id, message_id, thread_id, surface, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			thread_id = 0,
			surface = excluded.surface,
			updated_at = excluded.updated_at
	`, chatID, messageID, clampStoreText(surface, 120), atRaw, atRaw); err != nil {
		return fmt.Errorf("clear telegram callback message thread: %w", err)
	}
	return nil
}

func (s *SQLiteStore) MarkTelegramCallbackMessageSurface(chatID int64, messageID int64, surface string, at time.Time) error {
	if chatID == 0 || messageID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	atRaw := at.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		INSERT INTO telegram_callback_messages(chat_id, message_id, thread_id, surface, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			surface = excluded.surface,
			updated_at = excluded.updated_at
	`, chatID, messageID, clampStoreText(surface, 120), atRaw, atRaw); err != nil {
		return fmt.Errorf("mark telegram callback message surface: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ListTelegramCallbackMessages(chatID int64, surface string, since time.Time, limit int) ([]TelegramCallbackMessage, error) {
	return s.listTelegramCallbackMessages(chatID, nil, surface, since, limit)
}

func (s *SQLiteStore) ListTelegramCallbackMessagesForThread(chatID int64, threadID int64, surface string, since time.Time, limit int) ([]TelegramCallbackMessage, error) {
	if threadID < 0 {
		threadID = 0
	}
	return s.listTelegramCallbackMessages(chatID, &threadID, surface, since, limit)
}

func (s *SQLiteStore) listTelegramCallbackMessages(chatID int64, threadID *int64, surface string, since time.Time, limit int) ([]TelegramCallbackMessage, error) {
	if chatID == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	surface = strings.TrimSpace(surface)
	args := []any{chatID}
	where := "WHERE chat_id = ?"
	if threadID != nil {
		where += " AND thread_id = ?"
		args = append(args, *threadID)
	}
	if surface != "" {
		where += " AND surface = ?"
		args = append(args, surface)
	}
	if !since.IsZero() {
		where += " AND updated_at >= ?"
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT chat_id, message_id, thread_id, surface, created_at, updated_at
		FROM telegram_callback_messages
		`+where+`
		ORDER BY updated_at DESC, message_id DESC
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list telegram callback messages: %w", err)
	}
	defer rows.Close()
	var out []TelegramCallbackMessage
	for rows.Next() {
		record, err := scanTelegramCallbackMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan telegram callback messages: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) RecordTelegramThreadMessage(chatID int64, threadID int64, messageID int64, msgType string, surface string, at time.Time) error {
	if chatID == 0 || threadID <= 0 || messageID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin telegram thread message record: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureTelegramThreadSessionTx(tx, chatID, threadID, at); err != nil {
		return err
	}
	key := telegramThreadSessionKey(chatID, threadID)
	if err := recordOutboundTx(tx, key, 0, messageID, msgType); err != nil {
		return err
	}
	if err := recordTelegramCallbackMessageTx(tx, chatID, messageID, threadID, surface, at); err != nil {
		return err
	}
	if err := recordTelegramThreadLastMessageTx(tx, chatID, threadID, messageID, msgType, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit telegram thread message record: %w", err)
	}
	return nil
}

func recordTelegramCallbackMessageTx(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, chatID int64, messageID int64, threadID int64, surface string, at time.Time) error {
	if chatID == 0 || messageID <= 0 {
		return nil
	}
	if threadID < 0 {
		threadID = 0
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	atRaw := at.UTC().Format(time.RFC3339Nano)
	if _, err := exec.Exec(`
		INSERT INTO telegram_callback_messages(chat_id, message_id, thread_id, surface, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			thread_id = excluded.thread_id,
			surface = excluded.surface,
			updated_at = excluded.updated_at
	`, chatID, messageID, threadID, clampStoreText(surface, 120), atRaw, atRaw); err != nil {
		return fmt.Errorf("record telegram callback message thread: %w", err)
	}
	return nil
}

func scanTelegramCallbackMessage(scanner interface {
	Scan(dest ...any) error
}) (TelegramCallbackMessage, error) {
	var record TelegramCallbackMessage
	var createdRaw, updatedRaw string
	if err := scanner.Scan(&record.ChatID, &record.MessageID, &record.ThreadID, &record.Surface, &createdRaw, &updatedRaw); err != nil {
		return TelegramCallbackMessage{}, fmt.Errorf("scan telegram callback message: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdRaw)
	if err != nil {
		return TelegramCallbackMessage{}, fmt.Errorf("parse telegram callback message created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedRaw)
	if err != nil {
		return TelegramCallbackMessage{}, fmt.Errorf("parse telegram callback message updated_at: %w", err)
	}
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return record, nil
}

func recordTelegramThreadLastMessageTx(exec interface {
	Exec(query string, args ...any) (sql.Result, error)
}, chatID int64, threadID int64, messageID int64, source string, at time.Time) error {
	if chatID == 0 || threadID <= 0 || messageID <= 0 {
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
	if _, err := exec.Exec(`
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
	if _, err := exec.Exec(`
		UPDATE telegram_threads
		SET last_activity_at = ?, updated_at = ?
		WHERE chat_id = ? AND thread_id = ? AND status = ?
	`, atRaw, atRaw, chatID, threadID, string(TelegramThreadStatusOpen)); err != nil {
		return fmt.Errorf("touch telegram thread last message: %w", err)
	}
	return nil
}
