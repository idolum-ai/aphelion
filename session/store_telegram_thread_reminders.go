//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type TelegramThreadReminderStatus string

const (
	TelegramThreadReminderStatusPending  TelegramThreadReminderStatus = "pending"
	TelegramThreadReminderStatusIgnored  TelegramThreadReminderStatus = "ignored"
	TelegramThreadReminderStatusAbsorbed TelegramThreadReminderStatus = "absorbed"
	TelegramThreadReminderStatusResumed  TelegramThreadReminderStatus = "resumed"
	TelegramThreadReminderStatusExpired  TelegramThreadReminderStatus = "expired"
)

type TelegramThreadReminder struct {
	ID                   int64
	ChatID               int64
	ThreadID             int64
	MessageID            int64
	Status               TelegramThreadReminderStatus
	SummaryText          string
	SummaryKind          string
	SourceLastActivityAt time.Time
	CreatedBySenderID    int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (s *SQLiteStore) RecordTelegramThreadReminder(chatID int64, threadID int64, messageID int64, summaryText string, summaryKind string, sourceLastActivityAt time.Time, createdBySenderID int64, at time.Time) (TelegramThreadReminder, error) {
	if chatID == 0 || threadID <= 0 || messageID <= 0 {
		return TelegramThreadReminder{}, fmt.Errorf("telegram thread reminder chat, thread, and message id are required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	atRaw := at.UTC().Format(time.RFC3339Nano)
	sourceRaw := ""
	if !sourceLastActivityAt.IsZero() {
		sourceRaw = sourceLastActivityAt.UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TelegramThreadReminder{}, fmt.Errorf("begin telegram thread reminder record: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := recordTelegramCallbackMessageTx(tx, chatID, messageID, threadID, "thread_reminder", at); err != nil {
		return TelegramThreadReminder{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO telegram_thread_reminders(
			chat_id, thread_id, message_id, status, summary_text, summary_kind,
			source_last_activity_at, created_by_sender_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, message_id) DO UPDATE SET
			thread_id = excluded.thread_id,
			status = excluded.status,
			summary_text = excluded.summary_text,
			summary_kind = excluded.summary_kind,
			source_last_activity_at = excluded.source_last_activity_at,
			created_by_sender_id = excluded.created_by_sender_id,
			updated_at = excluded.updated_at
	`, chatID, threadID, messageID, string(TelegramThreadReminderStatusPending), clampStoreText(summaryText, 500), clampStoreText(summaryKind, 80), sourceRaw, createdBySenderID, atRaw, atRaw); err != nil {
		return TelegramThreadReminder{}, fmt.Errorf("record telegram thread reminder: %w", err)
	}
	reminder, ok, err := telegramThreadReminderByMessageTx(tx, chatID, messageID)
	if err != nil {
		return TelegramThreadReminder{}, err
	}
	if !ok {
		return TelegramThreadReminder{}, fmt.Errorf("telegram thread reminder %d/%d missing after record", chatID, messageID)
	}
	if err := tx.Commit(); err != nil {
		return TelegramThreadReminder{}, fmt.Errorf("commit telegram thread reminder record: %w", err)
	}
	return reminder, nil
}

func (s *SQLiteStore) MarkTelegramThreadReminderStatus(chatID int64, messageID int64, next TelegramThreadReminderStatus, at time.Time) (TelegramThreadReminder, bool, error) {
	if chatID == 0 || messageID <= 0 {
		return TelegramThreadReminder{}, false, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	next = normalizeTelegramThreadReminderStatus(next)
	res, err := s.db.Exec(`
		UPDATE telegram_thread_reminders
		SET status = ?, updated_at = ?
		WHERE chat_id = ? AND message_id = ? AND status = ?
	`, string(next), at.UTC().Format(time.RFC3339Nano), chatID, messageID, string(TelegramThreadReminderStatusPending))
	if err != nil {
		return TelegramThreadReminder{}, false, fmt.Errorf("mark telegram thread reminder %s: %w", next, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return TelegramThreadReminder{}, false, fmt.Errorf("mark telegram thread reminder rows affected: %w", err)
	}
	reminder, ok, err := s.TelegramThreadReminderByMessage(chatID, messageID)
	return reminder, ok && affected > 0, err
}

func (s *SQLiteStore) TelegramThreadReminderByMessage(chatID int64, messageID int64) (TelegramThreadReminder, bool, error) {
	if chatID == 0 || messageID <= 0 {
		return TelegramThreadReminder{}, false, nil
	}
	return telegramThreadReminderByMessageDB(s.db, chatID, messageID)
}

func (s *SQLiteStore) ListTelegramThreadReminders(chatID int64, status TelegramThreadReminderStatus, limit int) ([]TelegramThreadReminder, error) {
	if chatID == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	status = normalizeTelegramThreadReminderStatus(status)
	query := telegramThreadReminderSelectSQL() + ` WHERE chat_id = ?`
	args := []any{chatID}
	if status != "" {
		query += ` AND status = ?`
		args = append(args, string(status))
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list telegram thread reminders: %w", err)
	}
	defer rows.Close()
	return scanTelegramThreadReminderRows(rows)
}

func normalizeTelegramThreadReminderStatus(status TelegramThreadReminderStatus) TelegramThreadReminderStatus {
	switch TelegramThreadReminderStatus(strings.ToLower(strings.TrimSpace(string(status)))) {
	case TelegramThreadReminderStatusIgnored:
		return TelegramThreadReminderStatusIgnored
	case TelegramThreadReminderStatusAbsorbed:
		return TelegramThreadReminderStatusAbsorbed
	case TelegramThreadReminderStatusResumed:
		return TelegramThreadReminderStatusResumed
	case TelegramThreadReminderStatusExpired:
		return TelegramThreadReminderStatusExpired
	case TelegramThreadReminderStatusPending:
		return TelegramThreadReminderStatusPending
	default:
		return status
	}
}

func telegramThreadReminderByMessageDB(db *sql.DB, chatID int64, messageID int64) (TelegramThreadReminder, bool, error) {
	return scanTelegramThreadReminderOptional(db.QueryRow(telegramThreadReminderSelectSQL()+` WHERE chat_id = ? AND message_id = ?`, chatID, messageID))
}

func telegramThreadReminderByMessageTx(tx *sql.Tx, chatID int64, messageID int64) (TelegramThreadReminder, bool, error) {
	return scanTelegramThreadReminderOptional(tx.QueryRow(telegramThreadReminderSelectSQL()+` WHERE chat_id = ? AND message_id = ?`, chatID, messageID))
}

func telegramThreadReminderSelectSQL() string {
	return `SELECT id, chat_id, thread_id, message_id, status, summary_text, summary_kind,
		source_last_activity_at, created_by_sender_id, created_at, updated_at
		FROM telegram_thread_reminders`
}

func scanTelegramThreadReminderOptional(row *sql.Row) (TelegramThreadReminder, bool, error) {
	reminder, err := scanTelegramThreadReminder(row)
	if err == sql.ErrNoRows {
		return TelegramThreadReminder{}, false, nil
	}
	if err != nil {
		return TelegramThreadReminder{}, false, err
	}
	return reminder, true, nil
}

func scanTelegramThreadReminderRows(rows *sql.Rows) ([]TelegramThreadReminder, error) {
	var out []TelegramThreadReminder
	for rows.Next() {
		reminder, err := scanTelegramThreadReminder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, reminder)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanTelegramThreadReminder(scanner interface{ Scan(dest ...any) error }) (TelegramThreadReminder, error) {
	var reminder TelegramThreadReminder
	var status string
	var sourceRaw, createdRaw, updatedRaw string
	if err := scanner.Scan(&reminder.ID, &reminder.ChatID, &reminder.ThreadID, &reminder.MessageID, &status, &reminder.SummaryText, &reminder.SummaryKind, &sourceRaw, &reminder.CreatedBySenderID, &createdRaw, &updatedRaw); err != nil {
		return TelegramThreadReminder{}, err
	}
	reminder.Status = normalizeTelegramThreadReminderStatus(TelegramThreadReminderStatus(status))
	if strings.TrimSpace(sourceRaw) != "" {
		if t, err := parseSQLiteTime(sourceRaw); err == nil {
			reminder.SourceLastActivityAt = t
		}
	}
	if t, err := parseSQLiteTime(createdRaw); err == nil {
		reminder.CreatedAt = t
	}
	if t, err := parseSQLiteTime(updatedRaw); err == nil {
		reminder.UpdatedAt = t
	}
	return reminder, nil
}

func (s *SQLiteStore) ExpirePendingTelegramThreadReminders(chatID int64, before time.Time, at time.Time) (int, error) {
	if chatID == 0 || before.IsZero() {
		return 0, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	res, err := s.db.Exec(`
		UPDATE telegram_thread_reminders
		SET status = ?, updated_at = ?
		WHERE chat_id = ? AND status = ? AND created_at < ?
	`, string(TelegramThreadReminderStatusExpired), at.UTC().Format(time.RFC3339Nano), chatID, string(TelegramThreadReminderStatusPending), before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("expire pending telegram thread reminders: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("expire pending telegram thread reminder rows affected: %w", err)
	}
	return int(affected), nil
}
