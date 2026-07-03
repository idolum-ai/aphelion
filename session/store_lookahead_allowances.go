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
		expiresAt = now.Add(30 * time.Minute)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return LookaheadAllowance{}, false, fmt.Errorf("begin lookahead allowance tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	count, err := outstandingLookaheadAllowanceCountTx(tx, adminChatID, now)
	if err != nil {
		return LookaheadAllowance{}, false, err
	}
	if count >= maxOutstanding {
		return LookaheadAllowance{}, false, nil
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
	record, err := insertLookaheadAllowanceTx(tx, input)
	if err != nil {
		return LookaheadAllowance{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return LookaheadAllowance{}, false, fmt.Errorf("commit lookahead allowance tx: %w", err)
	}
	return record, true, nil
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

func (s *SQLiteStore) OutstandingLookaheadApprovalFrontierCountAt(adminChatID int64, now time.Time) (int, error) {
	if s == nil || s.db == nil || adminChatID == 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return outstandingLookaheadAllowanceCountTx(s.db, adminChatID, now.UTC())
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
