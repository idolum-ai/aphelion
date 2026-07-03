//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) ReserveLookaheadAllowance(adminChatID int64, reviewEventID int64, sourceSessionID string, targetSessionID string, maxOutstanding int, now time.Time, expiresAt time.Time) (LookaheadAllowance, bool, error) {
	if s == nil || s.db == nil || adminChatID == 0 {
		return LookaheadAllowance{}, false, nil
	}
	if maxOutstanding <= 0 {
		return LookaheadAllowance{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if expiresAt.IsZero() {
		expiresAt = now.Add(DefaultLookaheadAllowanceTTL)
	}
	allowanceID, err := NewLookaheadAllowanceID()
	if err != nil {
		return LookaheadAllowance{}, false, err
	}
	input := NormalizeLookaheadAllowanceInput(LookaheadAllowanceInput{
		AllowanceID:     allowanceID,
		AdminChatID:     adminChatID,
		ReviewEventID:   reviewEventID,
		SourceSessionID: sourceSessionID,
		TargetSessionID: targetSessionID,
		Status:          LookaheadAllowanceReserved,
		CreatedAt:       now,
		UpdatedAt:       now,
		ExpiresAt:       expiresAt,
	})
	if err := ValidateLookaheadAllowanceInput(input); err != nil {
		return LookaheadAllowance{}, false, err
	}
	result, err := s.db.Exec(`
		INSERT INTO authority_lookahead_allowances(
			allowance_id, admin_chat_id, review_event_id, source_session_id, target_session_id,
			status, next_action_record_id, entry_id, reason,
			created_at, updated_at, expires_at, released_at
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE (
			SELECT COUNT(*)
			FROM authority_lookahead_allowances
			WHERE admin_chat_id = ?
				AND status IN (?, ?)
				AND (expires_at IS NULL OR expires_at = '' OR expires_at > ?)
		) < ?
	`, input.AllowanceID, input.AdminChatID, input.ReviewEventID, input.SourceSessionID, input.TargetSessionID,
		string(input.Status), input.NextActionRecordID, input.EntryID, input.Reason,
		input.CreatedAt.Format(time.RFC3339Nano), input.UpdatedAt.Format(time.RFC3339Nano),
		nullableTime(input.ExpiresAt), nullableTime(input.ReleasedAt),
		input.AdminChatID, string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen),
		now.Format(time.RFC3339Nano), maxOutstanding)
	if err != nil {
		return LookaheadAllowance{}, false, fmt.Errorf("reserve lookahead allowance %s: %w", input.AllowanceID, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return LookaheadAllowance{}, false, nil
	}
	return LookaheadAllowance(input), true, nil
}

func (s *SQLiteStore) BindLookaheadAllowance(allowanceID string, nextActionRecordID string, entryID string, now time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin bind lookahead allowance tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := bindLookaheadAllowanceTx(tx, allowanceID, nextActionRecordID, entryID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit bind lookahead allowance tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) BindLookaheadAllowanceOrReleaseOnFailure(allowanceID string, nextActionRecordID string, entryID string, releaseReason string, now time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin bind lookahead allowance tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := bindLookaheadAllowanceTx(tx, allowanceID, nextActionRecordID, entryID, now); err != nil {
		if releaseErr := releaseLookaheadAllowanceTx(tx, allowanceID, releaseReason, now); releaseErr != nil {
			return fmt.Errorf("bind lookahead allowance: %w; release after bind failure: %v", err, releaseErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return fmt.Errorf("commit released lookahead allowance after bind failure: %w", commitErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit bind lookahead allowance tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ReleaseLookaheadAllowance(allowanceID string, reason string, now time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin release lookahead allowance tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := releaseLookaheadAllowanceTx(tx, allowanceID, reason, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit release lookahead allowance tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ExpireLookaheadAllowancesForAdmin(adminChatID int64, now time.Time) ([]LookaheadAllowance, error) {
	if s == nil || s.db == nil || adminChatID == 0 {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin expire lookahead allowances tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`
		SELECT
			allowance_id, admin_chat_id, review_event_id, source_session_id, target_session_id,
			status, next_action_record_id, entry_id, reason,
			created_at, updated_at, expires_at, released_at
		FROM authority_lookahead_allowances
		WHERE admin_chat_id = ?
			AND status IN (?, ?)
			AND expires_at IS NOT NULL
			AND expires_at != ''
			AND expires_at <= ?
		ORDER BY expires_at ASC, allowance_id ASC
	`, adminChatID, string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen), now.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("query expired lookahead allowances: %w", err)
	}
	expired := []LookaheadAllowance{}
	for rows.Next() {
		record, err := scanLookaheadAllowance(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		expired = append(expired, record)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close expired lookahead allowance rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired lookahead allowances: %w", err)
	}
	for i, record := range expired {
		if _, err := tx.Exec(`
			UPDATE authority_lookahead_allowances
			SET status = ?, reason = ?, released_at = ?, updated_at = ?
			WHERE allowance_id = ?
				AND status IN (?, ?)
		`, string(LookaheadAllowanceExpired), "expired_unreviewed", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
			record.AllowanceID, string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen)); err != nil {
			return nil, fmt.Errorf("expire lookahead allowance %s: %w", record.AllowanceID, err)
		}
		expired[i].Status = LookaheadAllowanceExpired
		expired[i].Reason = "expired_unreviewed"
		expired[i].ReleasedAt = now
		expired[i].UpdatedAt = now
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit expire lookahead allowances tx: %w", err)
	}
	return expired, nil
}

func (s *SQLiteStore) OutstandingLookaheadApprovalFrontierCountAt(adminChatID int64, now time.Time) (int, error) {
	if s == nil || s.db == nil || adminChatID == 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return outstandingLookaheadAllowanceCountTx(s.db, adminChatID, now.UTC())
}

func (s *SQLiteStore) LookaheadAllowancesForAdmin(adminChatID int64, limit int) ([]LookaheadAllowance, error) {
	if s == nil || s.db == nil || adminChatID == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT
			allowance_id, admin_chat_id, review_event_id, source_session_id, target_session_id,
			status, next_action_record_id, entry_id, reason,
			created_at, updated_at, expires_at, released_at
		FROM authority_lookahead_allowances
		WHERE admin_chat_id = ?
			AND status IN (?, ?, ?)
		ORDER BY
			CASE
				WHEN status IN (?, ?) THEN 0
				ELSE 1
			END ASC,
			CASE
				WHEN status IN (?, ?) THEN created_at
				ELSE ''
			END ASC,
			CASE
				WHEN status = ? THEN updated_at
				ELSE ''
			END DESC,
			allowance_id ASC
		LIMIT ?
	`, adminChatID,
		string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen), string(LookaheadAllowanceExpired),
		string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen),
		string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen),
		string(LookaheadAllowanceExpired),
		limit)
	if err != nil {
		return nil, fmt.Errorf("query lookahead allowances for admin: %w", err)
	}
	defer rows.Close()
	records := make([]LookaheadAllowance, 0, limit)
	for rows.Next() {
		record, err := scanLookaheadAllowance(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate lookahead allowances for admin: %w", err)
	}
	return records, nil
}

func insertLookaheadAllowanceTx(tx *sql.Tx, input LookaheadAllowanceInput) (LookaheadAllowance, error) {
	input = NormalizeLookaheadAllowanceInput(input)
	if err := ValidateLookaheadAllowanceInput(input); err != nil {
		return LookaheadAllowance{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO authority_lookahead_allowances(
			allowance_id, admin_chat_id, review_event_id, source_session_id, target_session_id,
			status, next_action_record_id, entry_id, reason,
			created_at, updated_at, expires_at, released_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.AllowanceID, input.AdminChatID, input.ReviewEventID, input.SourceSessionID, input.TargetSessionID,
		string(input.Status), input.NextActionRecordID, input.EntryID, input.Reason,
		input.CreatedAt.Format(time.RFC3339Nano), input.UpdatedAt.Format(time.RFC3339Nano),
		nullableTime(input.ExpiresAt), nullableTime(input.ReleasedAt)); err != nil {
		return LookaheadAllowance{}, fmt.Errorf("insert lookahead allowance %s: %w", input.AllowanceID, err)
	}
	return LookaheadAllowance(input), nil
}

func scanLookaheadAllowance(scanner interface{ Scan(dest ...any) error }) (LookaheadAllowance, error) {
	var (
		record        LookaheadAllowance
		expiresAtRaw  sql.NullString
		releasedAtRaw sql.NullString
		createdAtRaw  string
		updatedAtRaw  string
		statusRaw     string
	)
	if err := scanner.Scan(
		&record.AllowanceID, &record.AdminChatID, &record.ReviewEventID, &record.SourceSessionID, &record.TargetSessionID,
		&statusRaw, &record.NextActionRecordID, &record.EntryID, &record.Reason,
		&createdAtRaw, &updatedAtRaw, &expiresAtRaw, &releasedAtRaw,
	); err != nil {
		return LookaheadAllowance{}, fmt.Errorf("scan lookahead allowance: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return LookaheadAllowance{}, fmt.Errorf("parse lookahead allowance created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return LookaheadAllowance{}, fmt.Errorf("parse lookahead allowance updated_at: %w", err)
	}
	record.Status = NormalizeLookaheadAllowanceStatus(LookaheadAllowanceStatus(statusRaw))
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	if raw := strings.TrimSpace(nullToString(expiresAtRaw)); raw != "" {
		expiresAt, err := parseSQLiteTime(raw)
		if err != nil {
			return LookaheadAllowance{}, fmt.Errorf("parse lookahead allowance expires_at: %w", err)
		}
		record.ExpiresAt = expiresAt
	}
	if raw := strings.TrimSpace(nullToString(releasedAtRaw)); raw != "" {
		releasedAt, err := parseSQLiteTime(raw)
		if err != nil {
			return LookaheadAllowance{}, fmt.Errorf("parse lookahead allowance released_at: %w", err)
		}
		record.ReleasedAt = releasedAt
	}
	return NormalizeLookaheadAllowance(record), nil
}

func bindLookaheadAllowanceTx(tx *sql.Tx, allowanceID string, nextActionRecordID string, entryID string, now time.Time) error {
	allowanceID = strings.TrimSpace(allowanceID)
	if allowanceID == "" {
		return nil
	}
	nextActionRecordID = strings.TrimSpace(nextActionRecordID)
	if nextActionRecordID == "" {
		return fmt.Errorf("lookahead allowance %s requires next_action_record_id", allowanceID)
	}
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return fmt.Errorf("lookahead allowance %s requires entry_id", allowanceID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := tx.Exec(`
		UPDATE authority_lookahead_allowances
		SET status = ?, next_action_record_id = ?, entry_id = ?, updated_at = ?
		WHERE allowance_id = ?
			AND status IN (?, ?)
	`, string(LookaheadAllowanceOpen), nextActionRecordID, entryID, now.UTC().Format(time.RFC3339Nano),
		allowanceID, string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen))
	if err != nil {
		return fmt.Errorf("bind lookahead allowance %s: %w", allowanceID, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("lookahead allowance %s is not bindable", allowanceID)
	}
	return nil
}

func releaseLookaheadAllowanceTx(tx interface {
	Exec(string, ...any) (sql.Result, error)
}, allowanceID string, reason string, now time.Time) error {
	allowanceID = strings.TrimSpace(allowanceID)
	if allowanceID == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	reason = normalizeEnumValue(reason)
	if reason == "" {
		reason = "released"
	}
	_, err := tx.Exec(`
		UPDATE authority_lookahead_allowances
		SET status = ?, reason = ?, updated_at = ?, released_at = ?
		WHERE allowance_id = ?
			AND status IN (?, ?)
	`, string(LookaheadAllowanceReleased), reason, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano),
		allowanceID, string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen))
	if err != nil {
		return fmt.Errorf("release lookahead allowance %s: %w", allowanceID, err)
	}
	return nil
}

func releaseLookaheadAllowancesForEntryTx(tx *sql.Tx, entryID string, reason string, now time.Time) error {
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	reason = normalizeEnumValue(reason)
	if reason == "" {
		reason = "ledger_status_changed"
	}
	_, err := tx.Exec(`
		UPDATE authority_lookahead_allowances
		SET status = ?, reason = ?, updated_at = ?, released_at = ?
		WHERE entry_id = ?
			AND status IN (?, ?)
	`, string(LookaheadAllowanceReleased), reason, now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano),
		entryID, string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen))
	if err != nil {
		return fmt.Errorf("release lookahead allowances for entry %s: %w", entryID, err)
	}
	return nil
}

type lookaheadAllowanceCounter interface {
	QueryRow(string, ...any) *sql.Row
}

func outstandingLookaheadAllowanceCountTx(db lookaheadAllowanceCounter, adminChatID int64, now time.Time) (int, error) {
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM authority_lookahead_allowances
		WHERE admin_chat_id = ?
			AND status IN (?, ?)
			AND (expires_at IS NULL OR expires_at = '' OR expires_at > ?)
	`, adminChatID, string(LookaheadAllowanceReserved), string(LookaheadAllowanceOpen), now.UTC().Format(time.RFC3339Nano)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count outstanding lookahead allowances: %w", err)
	}
	return count, nil
}
