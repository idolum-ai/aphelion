//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) BeginTurnRun(key SessionKey, kind TurnRunKind, requestText string) (*TurnRun, error) {
	now := time.Now().UTC()
	kind = TurnRunKind(strings.TrimSpace(string(kind)))
	if kind == "" {
		kind = TurnRunKindInteractive
	}
	scope := defaultScopeForKey(key)
	sessionID := SessionIDFromParts(key.ChatID, key.UserID, scope)

	res, err := s.db.Exec(`
		INSERT INTO turn_runs(
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, last_activity_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID, key.ChatID, key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		string(kind), string(TurnRunStatusRunning), requestText,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("begin turn run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("begin turn run last insert id: %w", err)
	}

	run := &TurnRun{
		ID:             id,
		SessionID:      sessionID,
		ChatID:         key.ChatID,
		UserID:         key.UserID,
		Scope:          scope,
		Kind:           kind,
		Status:         TurnRunStatusRunning,
		RequestText:    requestText,
		StartedAt:      now,
		LastActivityAt: now,
	}
	if _, err := s.UpsertEvidenceObject(turnRunEvidenceInput(*run)); err != nil {
		log.Printf("WARN write begin turn run evidence failed run_id=%d session_id=%s err=%v", run.ID, run.SessionID, err)
	}
	return run, nil
}

func (s *SQLiteStore) BeginTurnRunForTelegramIngress(key SessionKey, kind TurnRunKind, requestText string, surface string, updateID int64) (*TurnRun, error) {
	surface = strings.TrimSpace(surface)
	if surface == "" || updateID <= 0 {
		return s.BeginTurnRun(key, kind, requestText)
	}
	now := time.Now().UTC()
	kind = TurnRunKind(strings.TrimSpace(string(kind)))
	if kind == "" {
		kind = TurnRunKindInteractive
	}
	scope := defaultScopeForKey(key)
	sessionID := SessionIDFromParts(key.ChatID, key.UserID, scope)
	if threadID, ok := TelegramThreadIDFromScope(key.ChatID, scope); ok {
		open, found, err := s.TelegramThreadIsOpen(key.ChatID, threadID)
		if err != nil {
			return nil, err
		}
		if !found || !open {
			reason := TelegramIngressDropReasonTelegramThreadClosed
			if !found {
				reason = TelegramIngressDropReasonTelegramThreadMissing
			}
			if _, err := s.MarkTelegramIngressDroppedIfDispatchable(surface, updateID, reason, now); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("telegram ingress update %s/%d targets non-open telegram thread %d/%d: %s", surface, updateID, key.ChatID, threadID, reason)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin telegram ingress turn run tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	res, err := tx.Exec(`
		INSERT INTO turn_runs(
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, last_activity_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID, key.ChatID, key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		string(kind), string(TurnRunStatusRunning), requestText,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, fmt.Errorf("begin telegram ingress turn run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("begin telegram ingress turn run last insert id: %w", err)
	}
	update, err := tx.Exec(`
		UPDATE telegram_ingress_updates
		SET
			status = ?,
			turn_run_id = ?,
			started_at = COALESCE(started_at, ?),
			updated_at = ?
		WHERE surface = ? AND update_id = ? AND status IN (?, ?)
	`,
		string(TelegramIngressUpdateRunning),
		id,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		surface,
		updateID,
		string(TelegramIngressUpdateAccepted),
		string(TelegramIngressUpdateQueued),
	)
	if err != nil {
		return nil, fmt.Errorf("mark telegram ingress running: %w", err)
	}
	affected, err := update.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("mark telegram ingress running rows affected: %w", err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("telegram ingress update %s/%d is not accepted or queued", surface, updateID)
	}
	run := &TurnRun{
		ID:             id,
		SessionID:      sessionID,
		ChatID:         key.ChatID,
		UserID:         key.UserID,
		Scope:          scope,
		Kind:           kind,
		Status:         TurnRunStatusRunning,
		RequestText:    requestText,
		StartedAt:      now,
		LastActivityAt: now,
	}
	if _, err := upsertEvidenceObjectTx(tx, turnRunEvidenceInput(*run)); err != nil {
		log.Printf("WARN write telegram ingress turn run evidence failed run_id=%d session_id=%s err=%v", run.ID, run.SessionID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit telegram ingress turn run tx: %w", err)
	}
	return run, nil
}

func (s *SQLiteStore) NoteTurnRunToolStart(id int64, name string, preview string) error {
	if id == 0 {
		return fmt.Errorf("turn run id is required")
	}

	_, err := s.db.Exec(`
		UPDATE turn_runs
		SET
			last_activity_at = ?,
			last_tool_name = ?,
			last_tool_preview = ?,
			tool_calls_started = tool_calls_started + 1
		WHERE id = ?
	`,
		time.Now().UTC().Format(time.RFC3339Nano), nullableString(name), nullableString(preview), id,
	)
	if err != nil {
		return fmt.Errorf("note turn run tool start: %w", err)
	}
	return nil
}

func (s *SQLiteStore) NoteTurnRunToolFinish(id int64, resultPreview string, toolError string) error {
	if id == 0 {
		return fmt.Errorf("turn run id is required")
	}

	_, err := s.db.Exec(`
		UPDATE turn_runs
		SET
			last_activity_at = ?,
			tool_calls_finished = tool_calls_finished + 1,
			last_tool_result_preview = ?,
			last_tool_error = ?
		WHERE id = ?
	`,
		time.Now().UTC().Format(time.RFC3339Nano), nullableString(resultPreview), nullableString(toolError), id,
	)
	if err != nil {
		return fmt.Errorf("note turn run tool finish: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateTurnRunAccounting(id int64, turnIndex int, messages []Message, usage core.TokenUsage) error {
	if id == 0 {
		return nil
	}
	totalToolChars := int64(0)
	totalAssistantChars := int64(0)
	for _, msg := range messages {
		chars := int64(msg.ContentChars)
		if chars == 0 && msg.Content != "" {
			chars = int64(len(msg.Content))
		}
		switch strings.TrimSpace(msg.Role) {
		case "tool":
			totalToolChars += chars
		case "assistant":
			totalAssistantChars += chars
		}
	}
	_, err := s.db.Exec(`
		UPDATE turn_runs
		SET
			turn_index = ?,
			total_tool_chars_in = ?,
			total_assistant_chars_out = ?,
			provider_input_tokens = ?,
			provider_output_tokens = ?,
			provider_cache_read_tokens = ?,
			provider_cache_write_tokens = ?
		WHERE id = ?
	`,
		turnIndex,
		totalToolChars,
		totalAssistantChars,
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadTokens,
		usage.CacheWriteTokens,
		id,
	)
	if err != nil {
		return fmt.Errorf("update turn run accounting: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateTurnRunProgressMessage(id int64, progressMessageID int64) error {
	if id == 0 {
		return fmt.Errorf("turn run id is required")
	}
	if progressMessageID == 0 {
		return fmt.Errorf("progress_message_id is required")
	}

	_, err := s.db.Exec(`
		UPDATE turn_runs
		SET
			last_activity_at = ?,
			progress_message_id = ?
		WHERE id = ?
	`,
		time.Now().UTC().Format(time.RFC3339Nano), progressMessageID, id,
	)
	if err != nil {
		return fmt.Errorf("update turn run progress message: %w", err)
	}
	return nil
}

func (s *SQLiteStore) TouchTurnRunActivity(id int64) error {
	if id == 0 {
		return fmt.Errorf("turn run id is required")
	}

	_, err := s.db.Exec(`
		UPDATE turn_runs
		SET
			last_activity_at = ?
		WHERE id = ? AND status = ?
	`,
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
		string(TurnRunStatusRunning),
	)
	if err != nil {
		return fmt.Errorf("touch turn run activity: %w", err)
	}
	return nil
}

func (s *SQLiteStore) CompleteTurnRun(id int64, status TurnRunStatus, errorText string) error {
	if id == 0 {
		return fmt.Errorf("turn run id is required")
	}
	switch status {
	case TurnRunStatusCompleted, TurnRunStatusFailed, TurnRunStatusInterrupted:
	default:
		return fmt.Errorf("invalid turn run completion status %q", status)
	}

	now := time.Now().UTC()
	result, err := s.db.Exec(`
		UPDATE turn_runs
		SET
			status = ?,
			completed_at = ?,
			last_activity_at = ?,
			error_text = ?
		WHERE id = ? AND status = ?
	`,
		string(status),
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
		nullableString(errorText),
		id,
		string(TurnRunStatusRunning),
	)
	if err != nil {
		return fmt.Errorf("complete turn run: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows > 0 {
		if err := s.upsertTurnRunEvidenceForID(id); err != nil {
			log.Printf("WARN write complete turn run evidence failed run_id=%d status=%s err=%v", id, status, err)
		}
		return nil
	}
	return s.recordLateTurnCompletion(id, status, errorText, now)
}

func (s *SQLiteStore) upsertTurnRunEvidenceForID(id int64) error {
	run, err := s.TurnRun(id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}
	if _, err := s.UpsertEvidenceObject(turnRunEvidenceInput(*run)); err != nil {
		return fmt.Errorf("write turn run evidence: %w", err)
	}
	return nil
}

func (s *SQLiteStore) recordLateTurnCompletion(id int64, status TurnRunStatus, errorText string, now time.Time) error {
	var run TurnRun
	var durableAgentID string
	err := s.db.QueryRow(`
		SELECT id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, status
		FROM turn_runs
		WHERE id = ?
	`, id).Scan(&run.ID, &run.SessionID, &run.ChatID, &run.UserID, &run.Scope.Kind, &run.Scope.ID, &durableAgentID, &run.Status)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load late completed turn run: %w", err)
	}
	if run.Status == TurnRunStatusRunning {
		return nil
	}
	key := SessionKey{ChatID: run.ChatID, UserID: run.UserID, Scope: ScopeRef{Kind: run.Scope.Kind, ID: run.Scope.ID}}
	payloadRaw, _ := json.Marshal(map[string]any{
		"run_id":          id,
		"terminal_status": string(run.Status),
		"late_status":     string(status),
		"late_error":      clampStoreText(errorText, 500),
	})
	_, err = s.AppendExecutionEvent(key, ExecutionEventInput{EventType: "late_completion_after_interrupt", Stage: "turn_run", Status: "late_completion", PayloadJSON: string(payloadRaw), CreatedAt: now})
	if err != nil {
		return fmt.Errorf("record late turn completion: %w", err)
	}
	return nil
}

func (s *SQLiteStore) InterruptRunningTurnRuns() ([]TurnRun, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin interrupt turn runs tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	rows, err := tx.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, turn_index, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, total_tool_chars_in, total_assistant_chars_out,
			provider_input_tokens, provider_output_tokens, provider_cache_read_tokens, provider_cache_write_tokens, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE status = ?
		ORDER BY started_at ASC, id ASC
	`, string(TurnRunStatusRunning))
	if err != nil {
		return nil, fmt.Errorf("query running turn runs: %w", err)
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
		return nil, fmt.Errorf("iterate running turn runs: %w", err)
	}
	if len(interrupted) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty interrupt turn runs tx: %w", err)
		}
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		UPDATE turn_runs
		SET
			status = ?,
			completed_at = ?,
			last_activity_at = ?,
			error_text = COALESCE(error_text, 'process restarted before turn completed')
		WHERE status = ?
	`,
		string(TurnRunStatusInterrupted), now, now, string(TurnRunStatusRunning),
	); err != nil {
		return nil, fmt.Errorf("interrupt running turn runs: %w", err)
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
			"process restarted before turn completed",
			now,
			now,
			run.ID,
			string(TelegramIngressUpdateRunning),
		); err != nil {
			return nil, fmt.Errorf("interrupt telegram ingress update for turn run %d: %w", run.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit interrupt turn runs tx: %w", err)
	}

	for i := range interrupted {
		interrupted[i].Status = TurnRunStatusInterrupted
		interrupted[i].CompletedAt = mustParseSQLiteTime(now)
		interrupted[i].LastActivityAt = interrupted[i].CompletedAt
		if strings.TrimSpace(interrupted[i].ErrorText) == "" {
			interrupted[i].ErrorText = "process restarted before turn completed"
		}
	}
	return interrupted, nil
}

func (s *SQLiteStore) StaleRunningTurnRuns(cutoff time.Time, limit int) ([]TurnRun, error) {
	return s.StaleRunningTurnRunsWithUnmatchedToolCutoff(cutoff, cutoff, limit)
}

func (s *SQLiteStore) StaleRunningTurnRunsWithUnmatchedToolCutoff(activityCutoff time.Time, unmatchedToolCutoff time.Time, limit int) ([]TurnRun, error) {
	if activityCutoff.IsZero() {
		return nil, fmt.Errorf("stale turn run cutoff is required")
	}
	if unmatchedToolCutoff.IsZero() {
		unmatchedToolCutoff = activityCutoff
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT
			tr.id, tr.session_id, tr.chat_id, tr.user_id, tr.scope_kind, tr.scope_id, tr.durable_agent_id, tr.kind, tr.turn_index, tr.status, tr.request_text, tr.started_at, tr.completed_at,
			tr.last_activity_at, tr.last_tool_name, tr.last_tool_preview, tr.tool_calls_started, tr.tool_calls_finished, tr.total_tool_chars_in, tr.total_assistant_chars_out,
			tr.provider_input_tokens, tr.provider_output_tokens, tr.provider_cache_read_tokens, tr.provider_cache_write_tokens, tr.last_tool_result_preview, tr.last_tool_error,
			tr.progress_message_id, tr.error_text, tr.recovery_summary, tr.recovery_logged_at
		FROM turn_runs tr
		WHERE tr.status = ?
			AND (
				tr.last_activity_at <= ?
				OR EXISTS (
					SELECT 1
					FROM execution_events started
					WHERE started.session_id = tr.session_id
						AND started.event_type = ?
						AND CAST(json_extract(started.payload_json, '$.run_id') AS INTEGER) = tr.id
						AND started.created_at <= ?
						AND NOT EXISTS (
							SELECT 1
							FROM execution_events finished
							WHERE finished.session_id = tr.session_id
								AND finished.event_type IN (?, ?)
								AND CAST(json_extract(finished.payload_json, '$.run_id') AS INTEGER) = tr.id
								AND finished.created_at >= started.created_at
						)
				)
			)
		ORDER BY tr.last_activity_at ASC, tr.id ASC
		LIMIT ?
	`,
		string(TurnRunStatusRunning),
		activityCutoff.UTC().Format(time.RFC3339Nano),
		core.ExecutionEventToolStarted,
		unmatchedToolCutoff.UTC().Format(time.RFC3339Nano),
		core.ExecutionEventToolSucceeded,
		core.ExecutionEventToolFailed,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query stale running turn runs: %w", err)
	}
	defer rows.Close()

	stale := make([]TurnRun, 0, limit)
	for rows.Next() {
		run, err := scanTurnRun(rows)
		if err != nil {
			return nil, err
		}
		stale = append(stale, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stale running turn runs: %w", err)
	}
	return stale, nil
}

func (s *SQLiteStore) PendingRecoveryTurnRuns(limit int) ([]TurnRun, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, turn_index, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, total_tool_chars_in, total_assistant_chars_out,
			provider_input_tokens, provider_output_tokens, provider_cache_read_tokens, provider_cache_write_tokens, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE status = ? AND recovery_logged_at IS NULL
		ORDER BY started_at ASC, id ASC
		LIMIT ?
	`, string(TurnRunStatusInterrupted), limit)
	if err != nil {
		return nil, fmt.Errorf("query pending recovery turn runs: %w", err)
	}
	defer rows.Close()

	runs := make([]TurnRun, 0, limit)
	for rows.Next() {
		run, err := scanTurnRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending recovery turn runs: %w", err)
	}
	return runs, nil
}

func (s *SQLiteStore) MarkTurnRunsRecovered(ids []int64, summary string) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin mark turn runs recovered tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.Prepare(`
		UPDATE turn_runs
		SET
			recovery_summary = ?,
			recovery_logged_at = ?
		WHERE id = ? AND recovery_logged_at IS NULL
	`)
	if err != nil {
		return fmt.Errorf("prepare mark turn runs recovered statement: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, err := stmt.Exec(nullableString(summary), now, id); err != nil {
			return fmt.Errorf("mark turn run recovered id=%d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark turn runs recovered tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) LatestTurnRun(key SessionKey) (*TurnRun, error) {
	sessionID := SessionIDForKey(key)
	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, turn_index, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, total_tool_chars_in, total_assistant_chars_out,
			provider_input_tokens, provider_output_tokens, provider_cache_read_tokens, provider_cache_write_tokens, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE session_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query latest turn run: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	run, err := scanTurnRun(rows)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *SQLiteStore) LatestTurnRunsByChat(limit int) ([]TurnRun, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(`
		SELECT
			tr.id, tr.session_id, tr.chat_id, tr.user_id, tr.scope_kind, tr.scope_id, tr.durable_agent_id, tr.kind, tr.turn_index, tr.status, tr.request_text, tr.started_at, tr.completed_at,
			tr.last_activity_at, tr.last_tool_name, tr.last_tool_preview, tr.tool_calls_started, tr.tool_calls_finished, tr.total_tool_chars_in, tr.total_assistant_chars_out,
			tr.provider_input_tokens, tr.provider_output_tokens, tr.provider_cache_read_tokens, tr.provider_cache_write_tokens, tr.last_tool_result_preview, tr.last_tool_error,
			tr.progress_message_id, tr.error_text, tr.recovery_summary, tr.recovery_logged_at
		FROM turn_runs tr
		INNER JOIN (
			SELECT chat_id, MAX(id) AS max_id
			FROM turn_runs
			WHERE chat_id != 0
			GROUP BY chat_id
		) latest
		ON latest.chat_id = tr.chat_id AND latest.max_id = tr.id
		ORDER BY tr.last_activity_at DESC, tr.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query latest turn runs by chat: %w", err)
	}
	defer rows.Close()

	runs := make([]TurnRun, 0, limit)
	for rows.Next() {
		run, err := scanTurnRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest turn runs by chat: %w", err)
	}
	return runs, nil
}

func (s *SQLiteStore) LatestTurnRunsBySession(limit int) ([]TurnRun, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(`
		SELECT
			tr.id, tr.session_id, tr.chat_id, tr.user_id, tr.scope_kind, tr.scope_id, tr.durable_agent_id, tr.kind, tr.turn_index, tr.status, tr.request_text, tr.started_at, tr.completed_at,
			tr.last_activity_at, tr.last_tool_name, tr.last_tool_preview, tr.tool_calls_started, tr.tool_calls_finished, tr.total_tool_chars_in, tr.total_assistant_chars_out,
			tr.provider_input_tokens, tr.provider_output_tokens, tr.provider_cache_read_tokens, tr.provider_cache_write_tokens, tr.last_tool_result_preview, tr.last_tool_error,
			tr.progress_message_id, tr.error_text, tr.recovery_summary, tr.recovery_logged_at
		FROM turn_runs tr
		INNER JOIN (
			SELECT session_id, MAX(id) AS max_id
			FROM turn_runs
			WHERE chat_id != 0
			GROUP BY session_id
		) latest
		ON latest.session_id = tr.session_id AND latest.max_id = tr.id
		ORDER BY tr.last_activity_at DESC, tr.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query latest turn runs by session: %w", err)
	}
	defer rows.Close()

	runs := make([]TurnRun, 0, limit)
	for rows.Next() {
		run, err := scanTurnRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest turn runs by session: %w", err)
	}
	return runs, nil
}

func (s *SQLiteStore) TurnRun(id int64) (*TurnRun, error) {
	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, turn_index, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, total_tool_chars_in, total_assistant_chars_out,
			provider_input_tokens, provider_output_tokens, provider_cache_read_tokens, provider_cache_write_tokens, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE id = ?
	`, id)
	if err != nil {
		return nil, fmt.Errorf("query turn run: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	run, err := scanTurnRun(rows)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func scanTurnRun(scanner interface{ Scan(dest ...any) error }) (TurnRun, error) {
	var (
		run                   TurnRun
		sessionIDRaw          string
		scopeKindRaw          sql.NullString
		scopeIDRaw            sql.NullString
		durableAgentIDRaw     sql.NullString
		kindRaw               string
		turnIndexRaw          sql.NullInt64
		statusRaw             string
		startedAtRaw          string
		completedAtRaw        sql.NullString
		lastActivityAtRaw     string
		lastToolNameRaw       sql.NullString
		lastToolPreviewRaw    sql.NullString
		lastToolResultRaw     sql.NullString
		lastToolErrorRaw      sql.NullString
		totalToolCharsRaw     sql.NullInt64
		totalAsstCharsRaw     sql.NullInt64
		providerInputRaw      sql.NullInt64
		providerOutputRaw     sql.NullInt64
		providerCacheReadRaw  sql.NullInt64
		providerCacheWriteRaw sql.NullInt64
		progressMessageRaw    sql.NullInt64
		errorTextRaw          sql.NullString
		recoverySummaryRaw    sql.NullString
		recoveryLoggedAtRaw   sql.NullString
	)

	if err := scanner.Scan(
		&run.ID, &sessionIDRaw, &run.ChatID, &run.UserID, &scopeKindRaw, &scopeIDRaw, &durableAgentIDRaw, &kindRaw, &turnIndexRaw, &statusRaw, &run.RequestText, &startedAtRaw, &completedAtRaw,
		&lastActivityAtRaw, &lastToolNameRaw, &lastToolPreviewRaw, &run.ToolCallsStarted, &run.ToolCallsFinished, &totalToolCharsRaw, &totalAsstCharsRaw,
		&providerInputRaw, &providerOutputRaw, &providerCacheReadRaw, &providerCacheWriteRaw, &lastToolResultRaw, &lastToolErrorRaw,
		&progressMessageRaw, &errorTextRaw, &recoverySummaryRaw, &recoveryLoggedAtRaw,
	); err != nil {
		return TurnRun{}, fmt.Errorf("scan turn run: %w", err)
	}

	var err error
	run.SessionID = strings.TrimSpace(sessionIDRaw)
	run.Scope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(scopeKindRaw)),
		ID:             nullToString(scopeIDRaw),
		DurableAgentID: nullToString(durableAgentIDRaw),
	})
	run.Kind = TurnRunKind(kindRaw)
	if turnIndexRaw.Valid {
		run.TurnIndex = int(turnIndexRaw.Int64)
	}
	run.Status = TurnRunStatus(statusRaw)
	run.StartedAt, err = parseSQLiteTime(startedAtRaw)
	if err != nil {
		return TurnRun{}, fmt.Errorf("parse turn run started_at: %w", err)
	}
	run.LastActivityAt, err = parseSQLiteTime(lastActivityAtRaw)
	if err != nil {
		return TurnRun{}, fmt.Errorf("parse turn run last_activity_at: %w", err)
	}
	if completedAtRaw.Valid && completedAtRaw.String != "" {
		run.CompletedAt, err = parseSQLiteTime(completedAtRaw.String)
		if err != nil {
			return TurnRun{}, fmt.Errorf("parse turn run completed_at: %w", err)
		}
	}
	if recoveryLoggedAtRaw.Valid && recoveryLoggedAtRaw.String != "" {
		run.RecoveryLoggedAt, err = parseSQLiteTime(recoveryLoggedAtRaw.String)
		if err != nil {
			return TurnRun{}, fmt.Errorf("parse turn run recovery_logged_at: %w", err)
		}
	}
	if progressMessageRaw.Valid {
		run.ProgressMessageID = progressMessageRaw.Int64
	}
	if totalToolCharsRaw.Valid {
		run.TotalToolCharsIn = totalToolCharsRaw.Int64
	}
	if totalAsstCharsRaw.Valid {
		run.TotalAssistantCharsOut = totalAsstCharsRaw.Int64
	}
	if providerInputRaw.Valid {
		run.ProviderInputTokens = providerInputRaw.Int64
	}
	if providerOutputRaw.Valid {
		run.ProviderOutputTokens = providerOutputRaw.Int64
	}
	if providerCacheReadRaw.Valid {
		run.ProviderCacheReadTokens = providerCacheReadRaw.Int64
	}
	if providerCacheWriteRaw.Valid {
		run.ProviderCacheWriteTokens = providerCacheWriteRaw.Int64
	}
	run.LastToolName = nullToString(lastToolNameRaw)
	run.LastToolPreview = nullToString(lastToolPreviewRaw)
	run.LastToolResultPreview = nullToString(lastToolResultRaw)
	run.LastToolError = nullToString(lastToolErrorRaw)
	run.ErrorText = nullToString(errorTextRaw)
	run.RecoverySummary = nullToString(recoverySummaryRaw)
	return run, nil
}
