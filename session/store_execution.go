//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) NextExecutionSeq(key SessionKey) (int64, error) {
	sessionID := SessionIDForKey(key)
	var maxSeq sql.NullInt64
	err := s.db.QueryRow(`
		SELECT MAX(seq)
		FROM execution_events
		WHERE session_id = ?
	`, sessionID).Scan(&maxSeq)
	if err != nil {
		return 0, fmt.Errorf("query latest execution sequence: %w", err)
	}
	next := int64(1)
	if maxSeq.Valid && maxSeq.Int64 > 0 {
		next = maxSeq.Int64 + 1
	}
	return next, nil
}

func (s *SQLiteStore) AppendExecutionEvent(key SessionKey, input ExecutionEventInput) (ExecutionEvent, error) {
	events, err := s.AppendExecutionEvents(key, []ExecutionEventInput{input})
	if err != nil {
		return ExecutionEvent{}, err
	}
	if len(events) == 0 {
		return ExecutionEvent{}, fmt.Errorf("append execution event: no events written")
	}
	return events[0], nil
}

func (s *SQLiteStore) AppendExecutionEvents(key SessionKey, inputs []ExecutionEventInput) ([]ExecutionEvent, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin append execution events tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	events, err := appendExecutionEventsTx(tx, key, inputs)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit append execution events tx: %w", err)
	}
	return events, nil
}

func appendExecutionEventsTx(tx *sql.Tx, key SessionKey, inputs []ExecutionEventInput) ([]ExecutionEvent, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	sessionID := SessionIDForKey(key)
	scope := defaultScopeForKey(key)
	var maxSeq sql.NullInt64
	if err := tx.QueryRow(`
		SELECT MAX(seq)
		FROM execution_events
		WHERE session_id = ?
	`, sessionID).Scan(&maxSeq); err != nil {
		return nil, fmt.Errorf("query latest execution event seq: %w", err)
	}
	nextSeq := int64(1)
	if maxSeq.Valid && maxSeq.Int64 > 0 {
		nextSeq = maxSeq.Int64 + 1
	}

	stmt, err := tx.Prepare(`
		INSERT INTO execution_events(
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare append execution event statement: %w", err)
	}
	defer stmt.Close()

	events := make([]ExecutionEvent, 0, len(inputs))
	for _, input := range inputs {
		eventType := strings.TrimSpace(input.EventType)
		if eventType == "" {
			return nil, fmt.Errorf("append execution event: event_type is required")
		}
		stage := strings.TrimSpace(input.Stage)
		status := strings.TrimSpace(input.Status)
		payloadJSON, err := normalizeExecutionEventPayloadJSON(input.PayloadJSON)
		if err != nil {
			return nil, fmt.Errorf("append execution event payload: %w", err)
		}
		createdAt := input.CreatedAt.UTC()
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		result, err := stmt.Exec(
			sessionID,
			key.ChatID,
			key.UserID,
			string(scope.Kind),
			scope.ID,
			scope.DurableAgentID,
			nextSeq,
			eventType,
			stage,
			status,
			input.CausedBySeq,
			payloadJSON,
			createdAt.Format(time.RFC3339Nano),
		)
		if err != nil {
			return nil, fmt.Errorf("insert execution event type=%s seq=%d: %w", eventType, nextSeq, err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("execution event last insert id type=%s seq=%d: %w", eventType, nextSeq, err)
		}
		events = append(events, ExecutionEvent{
			ID:          id,
			SessionID:   sessionID,
			ChatID:      key.ChatID,
			UserID:      key.UserID,
			Scope:       scope,
			Seq:         nextSeq,
			EventType:   eventType,
			Stage:       stage,
			Status:      status,
			CausedBySeq: input.CausedBySeq,
			PayloadJSON: payloadJSON,
			CreatedAt:   createdAt,
		})
		nextSeq++
	}
	return events, nil
}

func normalizeExecutionEventPayloadJSON(payload string) (string, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return "{}", nil
	}
	if json.Valid([]byte(trimmed)) {
		return trimmed, nil
	}
	data, err := json.Marshal(map[string]string{"text": trimmed})
	if err != nil {
		return "", fmt.Errorf("marshal payload wrapper: %w", err)
	}
	return string(data), nil
}

func (s *SQLiteStore) ExecutionEventsBySession(key SessionKey, afterSeq int64, limit int) ([]ExecutionEvent, error) {
	sessionID := SessionIDForKey(key)
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
		FROM execution_events
		WHERE session_id = ? AND seq > ?
		ORDER BY seq ASC, id ASC
		LIMIT ?
	`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("query execution events by session: %w", err)
	}
	defer rows.Close()

	events := make([]ExecutionEvent, 0, limit)
	for rows.Next() {
		event, err := scanExecutionEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution events by session: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) LatestExecutionEventsBySession(key SessionKey, limit int) ([]ExecutionEvent, error) {
	sessionID := SessionIDForKey(key)
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
		FROM execution_events
		WHERE session_id = ?
		ORDER BY seq DESC, id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query latest execution events by session: %w", err)
	}
	defer rows.Close()

	events := make([]ExecutionEvent, 0, limit)
	for rows.Next() {
		event, err := scanExecutionEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate latest execution events by session: %w", err)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, nil
}

func (s *SQLiteStore) ExecutionEventsByChat(chatID int64, since time.Time, limit int) ([]ExecutionEvent, error) {
	if chatID == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}

	var (
		rows *sql.Rows
		err  error
	)
	if since.IsZero() {
		rows, err = s.db.Query(`
			SELECT
				id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
			FROM execution_events
			WHERE chat_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		`, chatID, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT
				id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
			FROM execution_events
			WHERE chat_id = ? AND created_at >= ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		`, chatID, since.UTC().Format(time.RFC3339Nano), limit)
	}
	if err != nil {
		return nil, fmt.Errorf("query execution events by chat: %w", err)
	}
	defer rows.Close()

	events := make([]ExecutionEvent, 0, limit)
	for rows.Next() {
		event, err := scanExecutionEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution events by chat: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) ExecutionEventsByTypes(eventTypes []string, since time.Time, limit int) ([]ExecutionEvent, error) {
	if len(eventTypes) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(eventTypes))
	seen := make(map[string]struct{}, len(eventTypes))
	for _, raw := range eventTypes {
		eventType := strings.TrimSpace(raw)
		if eventType == "" {
			continue
		}
		if _, ok := seen[eventType]; ok {
			continue
		}
		seen[eventType] = struct{}{}
		normalized = append(normalized, eventType)
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	sort.Strings(normalized)

	if limit <= 0 {
		limit = 500
	}
	placeholders := make([]string, 0, len(normalized))
	args := make([]any, 0, len(normalized)+2)
	for _, eventType := range normalized {
		placeholders = append(placeholders, "?")
		args = append(args, eventType)
	}
	query := `
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
		FROM execution_events
		WHERE event_type IN (` + strings.Join(placeholders, ",") + `)`
	if !since.IsZero() {
		query += " AND created_at >= ?"
		args = append(args, since.UTC().Format(time.RFC3339Nano))
	}
	query += `
		ORDER BY created_at DESC, id DESC
		LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query execution events by type: %w", err)
	}
	defer rows.Close()

	events := make([]ExecutionEvent, 0, limit)
	for rows.Next() {
		event, err := scanExecutionEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate execution events by type: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) ExecutionEventsRecent(limit int) ([]ExecutionEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
		FROM execution_events
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent execution events: %w", err)
	}
	defer rows.Close()

	events := make([]ExecutionEvent, 0, limit)
	for rows.Next() {
		event, err := scanExecutionEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent execution events: %w", err)
	}
	return events, nil
}

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

	return &TurnRun{
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
	}, nil
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

	_, err := s.db.Exec(`
		UPDATE turn_runs
		SET
			status = ?,
			completed_at = ?,
			last_activity_at = ?,
			error_text = ?
		WHERE id = ?
	`,
		string(status),
		time.Now().UTC().Format(time.RFC3339Nano),
		time.Now().UTC().Format(time.RFC3339Nano),
		nullableString(errorText),
		id,
	)
	if err != nil {
		return fmt.Errorf("complete turn run: %w", err)
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
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
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
			tr.id, tr.session_id, tr.chat_id, tr.user_id, tr.scope_kind, tr.scope_id, tr.durable_agent_id, tr.kind, tr.status, tr.request_text, tr.started_at, tr.completed_at,
			tr.last_activity_at, tr.last_tool_name, tr.last_tool_preview, tr.tool_calls_started, tr.tool_calls_finished, tr.last_tool_result_preview, tr.last_tool_error,
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
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
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
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
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
			tr.id, tr.session_id, tr.chat_id, tr.user_id, tr.scope_kind, tr.scope_id, tr.durable_agent_id, tr.kind, tr.status, tr.request_text, tr.started_at, tr.completed_at,
			tr.last_activity_at, tr.last_tool_name, tr.last_tool_preview, tr.tool_calls_started, tr.tool_calls_finished, tr.last_tool_result_preview, tr.last_tool_error,
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

func (s *SQLiteStore) TurnRun(id int64) (*TurnRun, error) {
	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
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
		run                 TurnRun
		sessionIDRaw        string
		scopeKindRaw        sql.NullString
		scopeIDRaw          sql.NullString
		durableAgentIDRaw   sql.NullString
		kindRaw             string
		statusRaw           string
		startedAtRaw        string
		completedAtRaw      sql.NullString
		lastActivityAtRaw   string
		lastToolNameRaw     sql.NullString
		lastToolPreviewRaw  sql.NullString
		lastToolResultRaw   sql.NullString
		lastToolErrorRaw    sql.NullString
		progressMessageRaw  sql.NullInt64
		errorTextRaw        sql.NullString
		recoverySummaryRaw  sql.NullString
		recoveryLoggedAtRaw sql.NullString
	)

	if err := scanner.Scan(
		&run.ID, &sessionIDRaw, &run.ChatID, &run.UserID, &scopeKindRaw, &scopeIDRaw, &durableAgentIDRaw, &kindRaw, &statusRaw, &run.RequestText, &startedAtRaw, &completedAtRaw,
		&lastActivityAtRaw, &lastToolNameRaw, &lastToolPreviewRaw, &run.ToolCallsStarted, &run.ToolCallsFinished, &lastToolResultRaw, &lastToolErrorRaw,
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
	run.LastToolName = nullToString(lastToolNameRaw)
	run.LastToolPreview = nullToString(lastToolPreviewRaw)
	run.LastToolResultPreview = nullToString(lastToolResultRaw)
	run.LastToolError = nullToString(lastToolErrorRaw)
	run.ErrorText = nullToString(errorTextRaw)
	run.RecoverySummary = nullToString(recoverySummaryRaw)
	return run, nil
}

func scanExecutionEvent(scanner interface{ Scan(dest ...any) error }) (ExecutionEvent, error) {
	var (
		event             ExecutionEvent
		scopeKindRaw      sql.NullString
		scopeIDRaw        sql.NullString
		durableAgentIDRaw sql.NullString
		stageRaw          sql.NullString
		statusRaw         sql.NullString
		payloadRaw        sql.NullString
		createdAtRaw      string
	)
	if err := scanner.Scan(
		&event.ID, &event.SessionID, &event.ChatID, &event.UserID, &scopeKindRaw, &scopeIDRaw, &durableAgentIDRaw,
		&event.Seq, &event.EventType, &stageRaw, &statusRaw, &event.CausedBySeq, &payloadRaw, &createdAtRaw,
	); err != nil {
		return ExecutionEvent{}, fmt.Errorf("scan execution event: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ExecutionEvent{}, fmt.Errorf("parse execution event created_at: %w", err)
	}
	event.Scope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(scopeKindRaw)),
		ID:             nullToString(scopeIDRaw),
		DurableAgentID: nullToString(durableAgentIDRaw),
	})
	event.EventType = strings.TrimSpace(event.EventType)
	event.Stage = nullToString(stageRaw)
	event.Status = nullToString(statusRaw)
	event.PayloadJSON = nullToString(payloadRaw)
	event.CreatedAt = createdAt
	return event, nil
}
