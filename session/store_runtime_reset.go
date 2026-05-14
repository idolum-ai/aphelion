//go:build linux

package session

import "fmt"

func (s *SQLiteStore) DeleteSession(key SessionKey) (int, error) {
	sessionID := SessionIDForKey(key)
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin delete session tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(`
		DELETE FROM review_events
		WHERE
			source_session_id = ?
			OR target_session_id = ?
	`, sessionID, sessionID); err != nil {
		return 0, fmt.Errorf("delete related review events: %w", err)
	}
	if _, err := tx.Exec(`
		DELETE FROM execution_events
		WHERE session_id = ?
	`, sessionID); err != nil {
		return 0, fmt.Errorf("delete related execution events: %w", err)
	}

	res, err := tx.Exec(`
		DELETE FROM sessions
		WHERE session_id = ?
	`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("delete session: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete session rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit delete session tx: %w", err)
	}
	return int(rows), nil
}

func (s *SQLiteStore) ResetRuntime() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reset runtime tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	statements := []string{
		`DELETE FROM operator_autonomy_overrides`,
		`DELETE FROM operator_auto_approvals`,
		`DELETE FROM pending_decisions`,
		`DELETE FROM review_events`,
		`DELETE FROM execution_events`,
		`DELETE FROM turn_runs`,
		`DELETE FROM outbound_messages`,
		`DELETE FROM compaction_log`,
		`DELETE FROM messages`,
		`DELETE FROM sessions`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("reset runtime with %q: %w", stmt, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset runtime tx: %w", err)
	}
	return nil
}
