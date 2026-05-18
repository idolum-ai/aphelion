//go:build linux

package session

import (
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) InterruptRunningTurnRunIDs(ids []int64, reason string) ([]TurnRun, error) {
	ids = normalizeTurnRunIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(reason) == "" {
		reason = "stale turn interrupted"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin selected interrupt turn runs tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	placeholders, args := turnRunIDPlaceholders(ids)
	queryArgs := append([]any{string(TurnRunStatusRunning)}, args...)
	rows, err := tx.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE status = ? AND id IN (`+placeholders+`)
		ORDER BY started_at ASC, id ASC
	`, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("query selected running turn runs: %w", err)
	}
	defer rows.Close()

	var interrupted []TurnRun
	for rows.Next() {
		run, err := scanTurnRun(rows)
		if err != nil {
			return nil, err
		}
		interrupted = append(interrupted, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate selected running turn runs: %w", err)
	}
	if len(interrupted) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty selected interrupt turn runs tx: %w", err)
		}
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	updateArgs := append([]any{
		string(TurnRunStatusInterrupted),
		now,
		now,
		reason,
		string(TurnRunStatusRunning),
	}, args...)
	if _, err := tx.Exec(`
		UPDATE turn_runs
		SET
			status = ?,
			completed_at = ?,
			last_activity_at = ?,
			error_text = COALESCE(NULLIF(error_text, ''), ?)
		WHERE status = ? AND id IN (`+placeholders+`)
	`, updateArgs...); err != nil {
		return nil, fmt.Errorf("interrupt selected running turn runs: %w", err)
	}
	for _, run := range interrupted {
		if run.ID <= 0 {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE telegram_ingress_updates
			SET
				status = ?,
				error_text = COALESCE(NULLIF(error_text, ''), ?),
				completed_at = COALESCE(completed_at, ?),
				updated_at = ?
			WHERE turn_run_id = ? AND status = ?
		`,
			string(TelegramIngressUpdateInterrupted),
			reason,
			now,
			now,
			run.ID,
			string(TelegramIngressUpdateRunning),
		); err != nil {
			return nil, fmt.Errorf("interrupt telegram ingress update for turn run %d: %w", run.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit selected interrupt turn runs tx: %w", err)
	}
	completedAt := mustParseSQLiteTime(now)
	for i := range interrupted {
		interrupted[i].Status = TurnRunStatusInterrupted
		interrupted[i].CompletedAt = completedAt
		interrupted[i].LastActivityAt = completedAt
		if strings.TrimSpace(interrupted[i].ErrorText) == "" {
			interrupted[i].ErrorText = reason
		}
	}
	return interrupted, nil
}

func normalizeTurnRunIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func turnRunIDPlaceholders(ids []int64) (string, []any) {
	parts := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, "?")
		args = append(args, id)
	}
	return strings.Join(parts, ","), args
}
