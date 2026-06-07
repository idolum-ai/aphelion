//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

type TelegramThreadStatus string

const (
	TelegramThreadStatusOpen   TelegramThreadStatus = "open"
	TelegramThreadStatusClosed TelegramThreadStatus = "closed"
)

type TelegramThread struct {
	ChatID              int64
	ThreadID            int64
	DisplaySlot         int64
	ArchivedDisplayName string
	Status              TelegramThreadStatus
	CreatedBySenderID   int64
	CreatedFromUpdateID int64
	CreatedMessageID    int64
	CreatedText         string
	LastActivityAt      time.Time
	ClosedAt            time.Time
	AbsorbSummary       string
	AbsorbedAt          time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (t TelegramThread) Open() bool {
	return normalizeTelegramThreadStatus(t.Status) == TelegramThreadStatusOpen
}

type TelegramThreadReminderPolicy struct {
	// StaleAfter is the passive threshold after which an open thread is eligible
	// for a reminder. It is diagnostic/accounting only; it does not schedule or
	// send reminders.
	StaleAfter time.Duration
}

type TelegramThreadReminderEligibility struct {
	ChatID         int64
	ThreadID       int64
	DisplaySlot    int64
	Eligible       bool
	Reason         string
	LastActivityAt time.Time
	Age            time.Duration
	StaleAfter     time.Duration
	SummaryKind    string
}

func DefaultTelegramThreadReminderPolicy() TelegramThreadReminderPolicy {
	return TelegramThreadReminderPolicy{StaleAfter: 24 * time.Hour}
}

func (t TelegramThread) ReminderEligibility(now time.Time, policy TelegramThreadReminderPolicy) TelegramThreadReminderEligibility {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if policy.StaleAfter <= 0 {
		policy = DefaultTelegramThreadReminderPolicy()
	}
	lastActivity := t.LastActivityAt
	if lastActivity.IsZero() {
		lastActivity = t.CreatedAt
	}
	result := TelegramThreadReminderEligibility{
		ChatID:         t.ChatID,
		ThreadID:       t.ThreadID,
		DisplaySlot:    t.DisplaySlot,
		LastActivityAt: lastActivity.UTC(),
		StaleAfter:     policy.StaleAfter,
		SummaryKind:    TelegramThreadReminderSummaryKind(t),
	}
	if !t.Open() {
		result.Reason = "thread_not_open"
		return result
	}
	if lastActivity.IsZero() {
		result.Reason = "missing_activity"
		return result
	}
	age := now.UTC().Sub(lastActivity.UTC())
	if age < 0 {
		age = 0
	}
	result.Age = age
	if age < policy.StaleAfter {
		result.Reason = "fresh"
		return result
	}
	result.Eligible = true
	result.Reason = "stale_open_thread"
	return result
}

func TelegramThreadReminderSummaryKind(t TelegramThread) string {
	text := strings.ToLower(strings.TrimSpace(t.CreatedText))
	if text == "" {
		return "generic"
	}
	for _, marker := range []string{"therapy", "therapist", "doctor", "medical", "health", "personal", "private", "family"} {
		if strings.Contains(text, marker) {
			return "privacy_softened"
		}
	}
	return "specific"
}

func (s *SQLiteStore) CreateTelegramThreadForUpdate(chatID int64, senderID int64, updateID int64, messageID int64, text string, now time.Time) (TelegramThread, bool, error) {
	if chatID == 0 {
		return TelegramThread{}, false, fmt.Errorf("telegram thread chat id is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if updateID > 0 {
		thread, ok, err := s.TelegramThreadByCreatedUpdate(chatID, updateID)
		if err != nil {
			return TelegramThread{}, false, err
		}
		if ok {
			return thread, false, nil
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return TelegramThread{}, false, fmt.Errorf("begin telegram thread create: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if updateID > 0 {
		thread, ok, err := telegramThreadByCreatedUpdateTx(tx, chatID, updateID)
		if err != nil {
			return TelegramThread{}, false, err
		}
		if ok {
			if err := tx.Commit(); err != nil {
				return TelegramThread{}, false, fmt.Errorf("commit telegram thread lookup: %w", err)
			}
			return thread, false, nil
		}
	}

	var nextThreadID int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(thread_id), 0) + 1 FROM telegram_threads WHERE chat_id = ?`, chatID).Scan(&nextThreadID); err != nil {
		return TelegramThread{}, false, fmt.Errorf("allocate telegram thread id: %w", err)
	}
	if nextThreadID <= 0 {
		nextThreadID = 1
	}
	displaySlot, err := nextTelegramThreadDisplaySlotTx(tx, chatID)
	if err != nil {
		return TelegramThread{}, false, err
	}
	nowRaw := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		INSERT INTO telegram_threads(
			chat_id, thread_id, display_slot, status, created_by_sender_id, created_from_update_id,
			created_message_id, created_text, last_activity_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, chatID, nextThreadID, displaySlot, string(TelegramThreadStatusOpen), senderID, updateID, messageID, clampStoreText(text, 2000), nowRaw, nowRaw, nowRaw); err != nil {
		return TelegramThread{}, false, fmt.Errorf("insert telegram thread: %w", err)
	}
	if err := ensureTelegramThreadSessionTx(tx, chatID, nextThreadID, now); err != nil {
		return TelegramThread{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TelegramThread{}, false, fmt.Errorf("commit telegram thread create: %w", err)
	}
	thread, ok, err := s.TelegramThread(chatID, nextThreadID)
	if err != nil {
		return TelegramThread{}, false, err
	}
	if !ok {
		return TelegramThread{}, false, fmt.Errorf("telegram thread %d/%d missing after create", chatID, nextThreadID)
	}
	return thread, true, nil
}

func (s *SQLiteStore) TelegramThread(chatID int64, threadID int64) (TelegramThread, bool, error) {
	if chatID == 0 || threadID <= 0 {
		return TelegramThread{}, false, nil
	}
	row := s.db.QueryRow(telegramThreadSelectSQL()+` WHERE chat_id = ? AND thread_id = ?`, chatID, threadID)
	thread, err := scanTelegramThread(row)
	if err == sql.ErrNoRows {
		return TelegramThread{}, false, nil
	}
	if err != nil {
		return TelegramThread{}, false, err
	}
	return thread, true, nil
}

func (s *SQLiteStore) TelegramThreadByCreatedUpdate(chatID int64, updateID int64) (TelegramThread, bool, error) {
	if chatID == 0 || updateID <= 0 {
		return TelegramThread{}, false, nil
	}
	return telegramThreadByCreatedUpdateDB(s.db, chatID, updateID)
}

func (s *SQLiteStore) TelegramThreadIsOpen(chatID int64, threadID int64) (bool, bool, error) {
	thread, ok, err := s.TelegramThread(chatID, threadID)
	if err != nil || !ok {
		return false, ok, err
	}
	return thread.Open(), true, nil
}

func (s *SQLiteStore) ListTelegramThreads(chatID int64, limit int) ([]TelegramThread, error) {
	return s.ListTelegramThreadsByView(chatID, "all", limit)
}

func (s *SQLiteStore) ListTelegramThreadsByView(chatID int64, view string, limit int) ([]TelegramThread, error) {
	if chatID == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	where := "WHERE chat_id = ?"
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "open", "":
		where += " AND status = 'open'"
	case "nonopen", "non-open", "closed":
		where += " AND status != 'open'"
	case "all":
	default:
		where += " AND status = 'open'"
	}
	query := telegramThreadSelectSQL() + "\n\t\t" + where + `
		ORDER BY
			CASE status WHEN 'open' THEN display_slot ELSE thread_id END ASC,
			updated_at DESC,
			thread_id DESC
		LIMIT ?
	`
	rows, err := s.db.Query(query, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("list telegram threads: %w", err)
	}
	defer rows.Close()
	return scanTelegramThreadRows(rows)
}

func (s *SQLiteStore) TouchTelegramThread(chatID int64, threadID int64, at time.Time) error {
	if chatID == 0 || threadID <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	atRaw := at.UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`
		UPDATE telegram_threads
		SET last_activity_at = ?, updated_at = ?
		WHERE chat_id = ? AND thread_id = ? AND status = ?
	`, atRaw, atRaw, chatID, threadID, string(TelegramThreadStatusOpen)); err != nil {
		return fmt.Errorf("touch telegram thread: %w", err)
	}
	return nil
}

func (s *SQLiteStore) CloseTelegramThread(chatID int64, threadID int64, summary string, at time.Time) (TelegramThread, bool, error) {
	if chatID == 0 || threadID <= 0 {
		return TelegramThread{}, false, nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TelegramThread{}, false, fmt.Errorf("begin telegram thread close: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	atRaw := at.UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`
		UPDATE telegram_threads
		SET status = ?, closed_at = COALESCE(closed_at, ?), absorbed_at = COALESCE(absorbed_at, ?),
			absorb_summary = ?, archived_display_name = COALESCE(NULLIF(archived_display_name, ''), ?), display_slot = 0, updated_at = ?
		WHERE chat_id = ? AND thread_id = ? AND status = ?
	`, string(TelegramThreadStatusClosed), atRaw, atRaw, clampStoreText(summary, 4000), telegramThreadArchivedDisplayNameTx(tx, chatID, threadID, at), atRaw, chatID, threadID, string(TelegramThreadStatusOpen))
	if err != nil {
		return TelegramThread{}, false, fmt.Errorf("close telegram thread: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return TelegramThread{}, false, fmt.Errorf("close telegram thread rows affected: %w", err)
	}
	if affected > 0 {
		if _, err := dropPendingTelegramIngressForTelegramThreadTx(tx, chatID, threadID, TelegramIngressDropReasonTelegramThreadClosed, at); err != nil {
			return TelegramThread{}, false, err
		}
	}
	thread, ok, err := telegramThreadTx(tx, chatID, threadID)
	if err != nil {
		return TelegramThread{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return TelegramThread{}, false, fmt.Errorf("commit telegram thread close: %w", err)
	}
	return thread, ok && affected > 0, nil
}

func (s *SQLiteStore) RecordTelegramThreadAbsorb(chatID int64, threadID int64, summary string, defaultSession *Session, newMessages []Message, at time.Time) (TelegramThread, bool, error) {
	if chatID == 0 || threadID <= 0 {
		return TelegramThread{}, false, nil
	}
	if defaultSession == nil {
		return TelegramThread{}, false, fmt.Errorf("record telegram thread absorb: default session is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	prepareSessionForSave(defaultSession, core.TokenUsage{}, at)

	tx, err := s.db.Begin()
	if err != nil {
		return TelegramThread{}, false, fmt.Errorf("begin telegram thread absorb: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	atRaw := at.UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`
		UPDATE telegram_threads
		SET status = ?, closed_at = COALESCE(closed_at, ?), absorbed_at = COALESCE(absorbed_at, ?),
			absorb_summary = ?, archived_display_name = COALESCE(NULLIF(archived_display_name, ''), ?), display_slot = 0, updated_at = ?
		WHERE chat_id = ? AND thread_id = ? AND status = ?
	`, string(TelegramThreadStatusClosed), atRaw, atRaw, clampStoreText(summary, 4000), telegramThreadArchivedDisplayNameTx(tx, chatID, threadID, at), atRaw, chatID, threadID, string(TelegramThreadStatusOpen))
	if err != nil {
		return TelegramThread{}, false, fmt.Errorf("record telegram thread absorb close: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return TelegramThread{}, false, fmt.Errorf("record telegram thread absorb rows affected: %w", err)
	}
	if affected == 0 {
		thread, ok, err := telegramThreadTx(tx, chatID, threadID)
		if err != nil {
			return TelegramThread{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return TelegramThread{}, false, fmt.Errorf("commit telegram thread absorb no-op: %w", err)
		}
		return thread, ok && affected > 0, nil
	}
	if _, err := dropPendingTelegramIngressForTelegramThreadTx(tx, chatID, threadID, TelegramIngressDropReasonTelegramThreadClosed, at); err != nil {
		return TelegramThread{}, false, err
	}
	if err := saveSessionInTx(tx, defaultSession, newMessages, at); err != nil {
		return TelegramThread{}, false, fmt.Errorf("record telegram thread absorb note: %w", err)
	}
	thread, ok, err := telegramThreadTx(tx, chatID, threadID)
	if err != nil {
		return TelegramThread{}, false, err
	}
	if !ok {
		return TelegramThread{}, false, fmt.Errorf("telegram thread %d/%d missing after absorb", chatID, threadID)
	}
	if err := tx.Commit(); err != nil {
		return TelegramThread{}, false, fmt.Errorf("commit telegram thread absorb: %w", err)
	}
	return thread, true, nil
}

func (s *SQLiteStore) TelegramThreadIDForReplyMessage(chatID int64, messageID int64) (int64, bool, error) {
	if chatID == 0 || messageID <= 0 {
		return 0, false, nil
	}
	var anchorThreadID int64
	err := s.db.QueryRow(`
		SELECT thread_id
		FROM telegram_thread_last_messages
		WHERE chat_id = ? AND message_id = ? AND thread_id > 0
		LIMIT 1
	`, chatID, messageID).Scan(&anchorThreadID)
	if err != nil && err != sql.ErrNoRows {
		return 0, false, fmt.Errorf("lookup telegram thread last message: %w", err)
	}
	if err == nil {
		return anchorThreadID, true, nil
	}

	var callbackThreadID int64
	err = s.db.QueryRow(`
		SELECT thread_id
		FROM telegram_callback_messages
		WHERE chat_id = ? AND message_id = ? AND thread_id > 0
		LIMIT 1
	`, chatID, messageID).Scan(&callbackThreadID)
	if err != nil && err != sql.ErrNoRows {
		return 0, false, fmt.Errorf("lookup telegram callback message thread: %w", err)
	}
	if err == nil {
		return callbackThreadID, true, nil
	}

	for _, lookup := range []struct {
		name  string
		query string
	}{
		{
			name: "pending decision",
			query: `
				SELECT session_id
				FROM pending_decisions
				WHERE chat_id = ? AND delivery_message_id = ? AND session_id != ''
				ORDER BY decision_seq DESC
				LIMIT 1
			`,
		},
		{
			name: "outbound",
			query: `
				SELECT session_id
				FROM outbound_messages
				WHERE chat_id = ? AND telegram_msg_id = ?
				ORDER BY id DESC
				LIMIT 1
			`,
		},
		{
			name: "ingress",
			query: `
				SELECT session_id
				FROM telegram_ingress_updates
				WHERE chat_id = ? AND message_id = ? AND session_id != ''
				ORDER BY update_id DESC
				LIMIT 1
			`,
		},
		{
			name: "progress",
			query: `
				SELECT session_id
				FROM turn_runs
				WHERE chat_id = ? AND progress_message_id = ? AND session_id != ''
				ORDER BY id DESC
				LIMIT 1
			`,
		},
	} {
		var sessionID string
		err := s.db.QueryRow(lookup.query, chatID, messageID).Scan(&sessionID)
		if err != nil && err != sql.ErrNoRows {
			return 0, false, fmt.Errorf("lookup telegram reply %s message: %w", lookup.name, err)
		}
		if err == nil {
			if threadID, ok := telegramThreadIDFromSessionID(chatID, sessionID); ok {
				return threadID, true, nil
			}
			continue
		}
	}

	var threadID int64
	err = s.db.QueryRow(`
		SELECT thread_id
		FROM telegram_threads
		WHERE chat_id = ? AND created_message_id = ?
		ORDER BY thread_id DESC
		LIMIT 1
	`, chatID, messageID).Scan(&threadID)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("lookup telegram reply created thread message: %w", err)
	}
	return threadID, threadID > 0, nil
}

func nextTelegramThreadDisplaySlotTx(tx *sql.Tx, chatID int64) (int64, error) {
	rows, err := tx.Query(`
		SELECT display_slot
		FROM telegram_threads
		WHERE chat_id = ? AND status = 'open' AND display_slot > 0
		ORDER BY display_slot ASC
	`, chatID)
	if err != nil {
		return 0, fmt.Errorf("query telegram thread display slots: %w", err)
	}
	defer rows.Close()
	used := map[int64]struct{}{}
	for rows.Next() {
		var slot int64
		if err := rows.Scan(&slot); err != nil {
			return 0, err
		}
		if slot > 0 {
			used[slot] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate telegram thread display slots: %w", err)
	}
	for slot := int64(1); ; slot++ {
		if _, ok := used[slot]; !ok {
			return slot, nil
		}
	}
}

func telegramThreadArchivedDisplayNameForSlot(slot int64, at time.Time) string {
	if at.IsZero() {
		at = time.Now()
	}
	date := at.Local().Format("2006-01-02")
	if slot <= 0 {
		return date
	}
	return fmt.Sprintf("%d-%s", slot, date)
}

func (s *SQLiteStore) telegramThreadArchivedDisplayName(chatID int64, threadID int64, at time.Time) string {
	thread, ok, err := s.TelegramThread(chatID, threadID)
	if err != nil || !ok {
		return telegramThreadArchivedDisplayNameForSlot(threadID, at)
	}
	return uniqueTelegramThreadArchivedDisplayName(s.db, chatID, threadID, telegramThreadArchivedDisplayNameForSlot(firstNonZeroInt(thread.DisplaySlot, thread.ThreadID), at))
}

func telegramThreadArchivedDisplayNameTx(tx *sql.Tx, chatID int64, threadID int64, at time.Time) string {
	thread, ok, err := telegramThreadTx(tx, chatID, threadID)
	if err != nil || !ok {
		return telegramThreadArchivedDisplayNameForSlot(threadID, at)
	}
	return uniqueTelegramThreadArchivedDisplayName(tx, chatID, threadID, telegramThreadArchivedDisplayNameForSlot(firstNonZeroInt(thread.DisplaySlot, thread.ThreadID), at))
}

type archivedNameQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func uniqueTelegramThreadArchivedDisplayName(q archivedNameQueryer, chatID int64, threadID int64, base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "closed-thread"
	}
	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		var existing int64
		err := q.QueryRow(`SELECT thread_id FROM telegram_threads WHERE chat_id = ? AND archived_display_name = ? AND thread_id != ? LIMIT 1`, chatID, candidate, threadID).Scan(&existing)
		if err == sql.ErrNoRows {
			return candidate
		}
		if err != nil {
			return candidate
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

func (s *SQLiteStore) SanitizeTelegramThreadDisplaySlots(now time.Time, apply bool) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := s.db.Query(`SELECT DISTINCT chat_id FROM telegram_threads ORDER BY chat_id`)
	if err != nil {
		return 0, fmt.Errorf("query telegram thread chats: %w", err)
	}
	defer rows.Close()
	changed := 0
	for rows.Next() {
		var chatID int64
		if err := rows.Scan(&chatID); err != nil {
			return changed, err
		}
		threads, err := s.ListTelegramThreadsByView(chatID, "all", 50)
		if err != nil {
			return changed, err
		}
		nextSlot := int64(1)
		for _, thread := range threads {
			if thread.Open() {
				if thread.DisplaySlot != nextSlot {
					changed++
					if apply {
						if _, err := s.db.Exec(`UPDATE telegram_threads SET display_slot = ?, updated_at = ? WHERE chat_id = ? AND thread_id = ?`, nextSlot, now.Format(time.RFC3339Nano), chatID, thread.ThreadID); err != nil {
							return changed, fmt.Errorf("sanitize telegram thread display slot: %w", err)
						}
					}
				}
				nextSlot++
			} else if thread.DisplaySlot != 0 || strings.TrimSpace(thread.ArchivedDisplayName) == "" {
				changed++
				if apply {
					name := thread.ArchivedDisplayName
					if strings.TrimSpace(name) == "" {
						name = telegramThreadArchivedDisplayNameForSlot(firstNonZeroInt(thread.DisplaySlot, thread.ThreadID), firstNonZeroTime(thread.AbsorbedAt, thread.ClosedAt, now))
					}
					if _, err := s.db.Exec(`UPDATE telegram_threads SET display_slot = 0, archived_display_name = ?, updated_at = ? WHERE chat_id = ? AND thread_id = ?`, name, now.Format(time.RFC3339Nano), chatID, thread.ThreadID); err != nil {
						return changed, fmt.Errorf("sanitize archived telegram thread display name: %w", err)
					}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return changed, err
	}
	return changed, nil
}

func firstNonZeroInt(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func TelegramThreadIDFromSessionID(chatID int64, sessionID string) (int64, bool) {
	return telegramThreadIDFromSessionID(chatID, sessionID)
}

func telegramThreadIDFromSessionID(chatID int64, sessionID string) (int64, bool) {
	sessionID = strings.TrimSpace(sessionID)
	const prefix = "telegram_thread:"
	if !strings.HasPrefix(sessionID, prefix) {
		return 0, false
	}
	raw := strings.TrimPrefix(sessionID, prefix)
	if idx := strings.Index(raw, "/"); idx >= 0 {
		raw = raw[:idx]
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, false
	}
	sessionChatID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || sessionChatID != chatID {
		return 0, false
	}
	threadID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || threadID <= 0 {
		return 0, false
	}
	return threadID, true
}

func (s *SQLiteStore) RebindTelegramIngressSession(surface string, updateID int64, sessionID string, inboundJSON string, updatedAt time.Time) error {
	surface = strings.TrimSpace(surface)
	sessionID = strings.TrimSpace(sessionID)
	if surface == "" || updateID <= 0 || sessionID == "" {
		return nil
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`
		UPDATE telegram_ingress_updates
		SET
			session_id = ?,
			inbound_json = CASE
				WHEN ? != '' THEN ?
				ELSE inbound_json
			END,
			updated_at = ?
		WHERE surface = ? AND update_id = ? AND status IN (?, ?)
	`, sessionID, strings.TrimSpace(inboundJSON), strings.TrimSpace(inboundJSON), updatedAt.UTC().Format(time.RFC3339Nano),
		surface, updateID, string(TelegramIngressUpdateAccepted), string(TelegramIngressUpdateQueued))
	if err != nil {
		return fmt.Errorf("rebind telegram ingress session: %w", err)
	}
	return nil
}

func telegramThreadByCreatedUpdateDB(db *sql.DB, chatID int64, updateID int64) (TelegramThread, bool, error) {
	row := db.QueryRow(telegramThreadSelectSQL()+` WHERE chat_id = ? AND created_from_update_id = ?`, chatID, updateID)
	thread, err := scanTelegramThread(row)
	if err == sql.ErrNoRows {
		return TelegramThread{}, false, nil
	}
	if err != nil {
		return TelegramThread{}, false, err
	}
	return thread, true, nil
}

func telegramThreadByCreatedUpdateTx(tx *sql.Tx, chatID int64, updateID int64) (TelegramThread, bool, error) {
	row := tx.QueryRow(telegramThreadSelectSQL()+` WHERE chat_id = ? AND created_from_update_id = ?`, chatID, updateID)
	thread, err := scanTelegramThread(row)
	if err == sql.ErrNoRows {
		return TelegramThread{}, false, nil
	}
	if err != nil {
		return TelegramThread{}, false, err
	}
	return thread, true, nil
}

func telegramThreadTx(tx *sql.Tx, chatID int64, threadID int64) (TelegramThread, bool, error) {
	row := tx.QueryRow(telegramThreadSelectSQL()+` WHERE chat_id = ? AND thread_id = ?`, chatID, threadID)
	thread, err := scanTelegramThread(row)
	if err == sql.ErrNoRows {
		return TelegramThread{}, false, nil
	}
	if err != nil {
		return TelegramThread{}, false, err
	}
	return thread, true, nil
}

func telegramThreadSelectSQL() string {
	return `SELECT chat_id, thread_id, display_slot, archived_display_name, status, created_by_sender_id, created_from_update_id,
		created_message_id, created_text, last_activity_at, closed_at, absorb_summary,
		absorbed_at, created_at, updated_at
		FROM telegram_threads`
}

func scanTelegramThreadRows(rows *sql.Rows) ([]TelegramThread, error) {
	var out []TelegramThread
	for rows.Next() {
		thread, err := scanTelegramThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate telegram threads: %w", err)
	}
	return out, nil
}

func scanTelegramThread(scanner interface{ Scan(dest ...any) error }) (TelegramThread, error) {
	var thread TelegramThread
	var statusRaw string
	var lastActivityRaw string
	var closedAtRaw sql.NullString
	var absorbedAtRaw sql.NullString
	var createdAtRaw string
	var updatedAtRaw string
	if err := scanner.Scan(
		&thread.ChatID,
		&thread.ThreadID,
		&thread.DisplaySlot,
		&thread.ArchivedDisplayName,
		&statusRaw,
		&thread.CreatedBySenderID,
		&thread.CreatedFromUpdateID,
		&thread.CreatedMessageID,
		&thread.CreatedText,
		&lastActivityRaw,
		&closedAtRaw,
		&thread.AbsorbSummary,
		&absorbedAtRaw,
		&createdAtRaw,
		&updatedAtRaw,
	); err != nil {
		return TelegramThread{}, err
	}
	lastActivity, err := parseSQLiteTime(lastActivityRaw)
	if err != nil {
		return TelegramThread{}, fmt.Errorf("parse telegram thread last_activity_at: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return TelegramThread{}, fmt.Errorf("parse telegram thread created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return TelegramThread{}, fmt.Errorf("parse telegram thread updated_at: %w", err)
	}
	thread.Status = normalizeTelegramThreadStatus(TelegramThreadStatus(statusRaw))
	thread.LastActivityAt = lastActivity
	thread.CreatedAt = createdAt
	thread.UpdatedAt = updatedAt
	if closedAtRaw.Valid && strings.TrimSpace(closedAtRaw.String) != "" {
		closedAt, err := parseSQLiteTime(closedAtRaw.String)
		if err != nil {
			return TelegramThread{}, fmt.Errorf("parse telegram thread closed_at: %w", err)
		}
		thread.ClosedAt = closedAt
	}
	if absorbedAtRaw.Valid && strings.TrimSpace(absorbedAtRaw.String) != "" {
		absorbedAt, err := parseSQLiteTime(absorbedAtRaw.String)
		if err != nil {
			return TelegramThread{}, fmt.Errorf("parse telegram thread absorbed_at: %w", err)
		}
		thread.AbsorbedAt = absorbedAt
	}
	return thread, nil
}

func normalizeTelegramThreadStatus(status TelegramThreadStatus) TelegramThreadStatus {
	switch TelegramThreadStatus(strings.TrimSpace(strings.ToLower(string(status)))) {
	case TelegramThreadStatusOpen:
		return TelegramThreadStatusOpen
	case TelegramThreadStatusClosed:
		return TelegramThreadStatusClosed
	default:
		return ""
	}
}

func (s *SQLiteStore) ListTelegramThreadChatIDs(limit int) ([]int64, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT DISTINCT chat_id
		FROM telegram_threads
		WHERE status = ?
		ORDER BY chat_id
		LIMIT ?
	`, string(TelegramThreadStatusOpen), limit)
	if err != nil {
		return nil, fmt.Errorf("list telegram thread chat ids: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var chatID int64
		if err := rows.Scan(&chatID); err != nil {
			return nil, err
		}
		if chatID != 0 {
			out = append(out, chatID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
