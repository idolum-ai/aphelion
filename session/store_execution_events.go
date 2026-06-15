//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
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
	for _, event := range events {
		if _, err := upsertEvidenceObjectTx(tx, executionEventEvidenceInput(event)); err != nil {
			return nil, fmt.Errorf("write execution event evidence seq=%d: %w", event.Seq, err)
		}
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

func (s *SQLiteStore) ExecutionEventsByTurnRun(key SessionKey, runID int64, limit int) ([]ExecutionEvent, error) {
	if runID <= 0 {
		return nil, nil
	}
	sessionID := SessionIDForKey(key)
	if limit <= 0 {
		limit = 2000
	}
	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, seq, event_type, stage, status, caused_by_seq, payload_json, created_at
		FROM execution_events
		WHERE session_id = ?
			AND CAST(json_extract(payload_json, '$.run_id') AS INTEGER) = ?
		ORDER BY seq DESC, id DESC
		LIMIT ?
	`, sessionID, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("query execution events by turn run: %w", err)
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
		return nil, fmt.Errorf("iterate execution events by turn run: %w", err)
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
