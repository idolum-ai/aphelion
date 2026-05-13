//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpdateCacheState(key SessionKey, state CacheState) error {
	sessionID := SessionIDForKey(key)
	_, err := s.db.Exec(`
		UPDATE sessions
		SET
			cache_last_write_block = ?,
			cache_blocks_since = ?,
			cache_last_write_time = ?,
			cache_hit_rate = ?,
			cache_consecutive_misses = ?,
			updated_at = ?
		WHERE session_id = ?
	`,
		state.LastWriteBlock,
		state.BlocksSinceWrite,
		nullableTime(state.LastWriteTime),
		state.HitRate,
		state.ConsecutiveMisses,
		time.Now().UTC().Format(time.RFC3339Nano),
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("update cache state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdatePlanState(key SessionKey, state PlanState) error {
	return s.updatePlanState(key, state, "")
}

func (s *SQLiteStore) UpdateOperationState(key SessionKey, state OperationState) error {
	if _, err := s.Load(key); err != nil {
		return err
	}
	sessionID := SessionIDForKey(key)
	state = NormalizeOperationState(state)
	_, err := s.db.Exec(`
		UPDATE sessions
		SET
			operation_state_json = ?,
			updated_at = ?
		WHERE session_id = ?
	`,
		encodeOperationState(state),
		time.Now().UTC().Format(time.RFC3339Nano),
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("update operation state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateContinuationState(key SessionKey, state ContinuationState) error {
	if _, err := s.Load(key); err != nil {
		return err
	}
	sessionID := SessionIDForKey(key)
	state = NormalizeContinuationState(state)
	_, err := s.db.Exec(`
		UPDATE sessions
		SET
			continuation_state_json = ?,
			updated_at = ?
		WHERE session_id = ?
	`,
		encodeContinuationState(state),
		time.Now().UTC().Format(time.RFC3339Nano),
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("update continuation state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ContinuationState(key SessionKey) (ContinuationState, error) {
	state, exists, err := s.ContinuationStateIfExists(key)
	if err != nil {
		return ContinuationState{}, err
	}
	if exists {
		return state, nil
	}
	sess, createErr := s.createEmptySession(key)
	if createErr != nil {
		return ContinuationState{}, createErr
	}
	return sess.ContinuationState, nil
}

func (s *SQLiteStore) ContinuationStateIfExists(key SessionKey) (ContinuationState, bool, error) {
	sessionID := SessionIDForKey(key)
	var raw sql.NullString
	err := s.db.QueryRow(`
		SELECT continuation_state_json
		FROM sessions
		WHERE session_id = ?
	`, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ContinuationState{}, false, nil
	}
	if err != nil {
		return ContinuationState{}, false, fmt.Errorf("load continuation state: %w", err)
	}
	state, decodeErr := decodeContinuationStateStrict(raw.String)
	if decodeErr != nil {
		return ContinuationState{}, true, fmt.Errorf("decode continuation state: %w", decodeErr)
	}
	return state, true, nil
}

func (s *SQLiteStore) PlanAndOperationStateIfExists(key SessionKey) (PlanState, OperationState, bool, error) {
	state, exists, err := s.StatusStateIfExists(key)
	if err != nil {
		return PlanState{}, OperationState{}, false, err
	}
	if !exists {
		return PlanState{}, OperationState{}, false, nil
	}
	return state.PlanState, state.OperationState, true, nil
}

func (s *SQLiteStore) StatusStateIfExists(key SessionKey) (SessionStatusState, bool, error) {
	sessionID := SessionIDForKey(key)
	var (
		planRaw           sql.NullString
		operationRaw      sql.NullString
		lastFloorMetadata sql.NullString
		turnCount         int
		outboundCount     int
	)
	err := s.db.QueryRow(`
		SELECT
			plan_state_json,
			operation_state_json,
			last_floor_metadata,
			turn_count,
			(
				SELECT COUNT(1)
				FROM outbound_messages o
				WHERE o.session_id = sessions.session_id AND o.turn_index = sessions.turn_count
			) AS outbound_count_at_turn
		FROM sessions
		WHERE session_id = ?
	`, sessionID).Scan(&planRaw, &operationRaw, &lastFloorMetadata, &turnCount, &outboundCount)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionStatusState{}, false, nil
	}
	if err != nil {
		return SessionStatusState{}, false, fmt.Errorf("load status state: %w", err)
	}
	return SessionStatusState{
		PlanState:           decodePlanState(planRaw.String),
		OperationState:      decodeOperationState(operationRaw.String),
		LastFloorMetadata:   strings.TrimSpace(lastFloorMetadata.String),
		TurnCount:           turnCount,
		OutboundCountAtTurn: outboundCount,
	}, true, nil
}

func (s *SQLiteStore) LatestDoctorReport(key SessionKey) (DoctorReportRecord, bool, error) {
	sessionID := SessionIDForKey(key)
	var (
		record       DoctorReportRecord
		floorRaw     sql.NullString
		floorMetaRaw sql.NullString
		createdAtRaw string
	)
	err := s.db.QueryRow(`
		SELECT
			a.session_id,
			a.chat_id,
			a.user_id,
			a.turn_index,
			a.content,
			a.floor_content,
			a.floor_metadata,
			a.created_at
		FROM messages a
		JOIN messages u
			ON u.session_id = a.session_id
			AND u.turn_index = a.turn_index
			AND u.role = 'user'
			AND u.content = '/health diagnose'
		WHERE a.session_id = ?
			AND a.role = 'assistant'
			AND a.compacted = 0
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT 1
	`, sessionID).Scan(
		&record.SessionID,
		&record.ChatID,
		&record.UserID,
		&record.TurnIndex,
		&record.FullReport,
		&floorRaw,
		&floorMetaRaw,
		&createdAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DoctorReportRecord{}, false, nil
	}
	if err != nil {
		return DoctorReportRecord{}, false, fmt.Errorf("load latest doctor report: %w", err)
	}
	record.TelegramReport = strings.TrimSpace(floorRaw.String)
	record.FloorMetadata = strings.TrimSpace(floorMetaRaw.String)
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return DoctorReportRecord{}, false, fmt.Errorf("parse latest doctor report created_at: %w", err)
	}
	record.CreatedAt = createdAt
	return record, true, nil
}

func (s *SQLiteStore) ContinuationStates() ([]ContinuationStateRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			chat_id, user_id, scope_kind, scope_id, durable_agent_id, continuation_state_json, updated_at
		FROM sessions
		ORDER BY updated_at DESC, session_id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query continuation states: %w", err)
	}
	defer rows.Close()

	records := make([]ContinuationStateRecord, 0, 16)
	for rows.Next() {
		var (
			record          ContinuationStateRecord
			scopeKind       sql.NullString
			scopeID         sql.NullString
			durableAgentID  sql.NullString
			continuationRaw sql.NullString
			updatedRaw      string
		)
		if err := rows.Scan(
			&record.Key.ChatID, &record.Key.UserID, &scopeKind, &scopeID, &durableAgentID, &continuationRaw, &updatedRaw,
		); err != nil {
			return nil, fmt.Errorf("scan continuation state record: %w", err)
		}
		record.Key.Scope = NormalizeScopeRef(ScopeRef{
			Kind:           ScopeKind(nullToString(scopeKind)),
			ID:             nullToString(scopeID),
			DurableAgentID: nullToString(durableAgentID),
		})
		record.RawJSON = strings.TrimSpace(continuationRaw.String)
		record.State = decodeContinuationState(continuationRaw.String)
		record.State = NormalizeContinuationState(record.State)
		switch record.State.Status {
		case ContinuationStatusPending, ContinuationStatusApproved, ContinuationStatusRevoked:
		default:
			continue
		}
		updatedAt, err := parseSQLiteTime(updatedRaw)
		if err != nil {
			return nil, fmt.Errorf("parse continuation state updated_at: %w", err)
		}
		record.UpdatedAt = updatedAt
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate continuation states: %w", err)
	}
	return records, nil
}

func (s *SQLiteStore) OperationStates() ([]OperationStateRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			chat_id, user_id, scope_kind, scope_id, durable_agent_id, operation_state_json, updated_at
		FROM sessions
		ORDER BY updated_at DESC, session_id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query operation states: %w", err)
	}
	defer rows.Close()

	records := make([]OperationStateRecord, 0, 16)
	for rows.Next() {
		var (
			record         OperationStateRecord
			scopeKind      sql.NullString
			scopeID        sql.NullString
			durableAgentID sql.NullString
			operationRaw   sql.NullString
			updatedRaw     string
		)
		if err := rows.Scan(
			&record.Key.ChatID, &record.Key.UserID, &scopeKind, &scopeID, &durableAgentID, &operationRaw, &updatedRaw,
		); err != nil {
			return nil, fmt.Errorf("scan operation state record: %w", err)
		}
		record.Key.Scope = NormalizeScopeRef(ScopeRef{
			Kind:           ScopeKind(nullToString(scopeKind)),
			ID:             nullToString(scopeID),
			DurableAgentID: nullToString(durableAgentID),
		})
		record.State = decodeOperationState(operationRaw.String)
		record.State = NormalizeOperationState(record.State)
		if !record.State.Active() {
			continue
		}
		updatedAt, err := parseSQLiteTime(updatedRaw)
		if err != nil {
			return nil, fmt.Errorf("parse operation state updated_at: %w", err)
		}
		record.UpdatedAt = updatedAt
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operation states: %w", err)
	}
	return records, nil
}

func (s *SQLiteStore) UpdatePlanStateWithEvent(key SessionKey, state PlanState, kind PlanEventKind) error {
	return s.updatePlanState(key, state, kind)
}

func (s *SQLiteStore) updatePlanState(key SessionKey, state PlanState, kind PlanEventKind) error {
	if _, err := s.Load(key); err != nil {
		return err
	}
	sessionID := SessionIDForKey(key)
	state = NormalizePlanState(state)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin update plan state tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := updatePlanStateTx(tx, sessionID, state, kind); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update plan state tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PlanState(key SessionKey) (PlanState, error) {
	sessionID := SessionIDForKey(key)
	var raw sql.NullString
	err := s.db.QueryRow(`
		SELECT plan_state_json
		FROM sessions
		WHERE session_id = ?
	`, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		sess, createErr := s.createEmptySession(key)
		if createErr != nil {
			return PlanState{}, createErr
		}
		return sess.PlanState, nil
	}
	if err != nil {
		return PlanState{}, fmt.Errorf("load plan state: %w", err)
	}
	state := decodePlanState(raw.String)
	if len(state.Steps) > 0 || state.Explanation != "" {
		return state, nil
	}
	rehydrated, ok, err := s.rehydratePlanState(sessionID)
	if err != nil {
		return PlanState{}, err
	}
	if ok {
		return rehydrated, nil
	}
	return state, nil
}

func (s *SQLiteStore) OperationState(key SessionKey) (OperationState, error) {
	sessionID := SessionIDForKey(key)
	var raw sql.NullString
	err := s.db.QueryRow(`
		SELECT operation_state_json
		FROM sessions
		WHERE session_id = ?
	`, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		sess, createErr := s.createEmptySession(key)
		if createErr != nil {
			return OperationState{}, createErr
		}
		return sess.OperationState, nil
	}
	if err != nil {
		return OperationState{}, fmt.Errorf("load operation state: %w", err)
	}
	return decodeOperationState(raw.String), nil
}

func (s *SQLiteStore) PlanEvents(key SessionKey, limit int) ([]PlanEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	sessionID := SessionIDForKey(key)
	rows, err := s.db.Query(`
		SELECT id, event_kind, plan_state_json, created_at
		FROM plan_events
		WHERE session_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query plan events: %w", err)
	}
	defer rows.Close()

	var out []PlanEvent
	for rows.Next() {
		var (
			event   PlanEvent
			rawPlan sql.NullString
			rawTime string
		)
		if err := rows.Scan(&event.ID, &event.Kind, &rawPlan, &rawTime); err != nil {
			return nil, fmt.Errorf("scan plan event: %w", err)
		}
		event.SessionID = sessionID
		event.PlanState = decodePlanState(rawPlan.String)
		event.CreatedAt = mustParseSQLiteTime(rawTime)
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plan events: %w", err)
	}
	return out, nil
}

func updatePlanStateTx(tx *sql.Tx, sessionID string, state PlanState, kind PlanEventKind) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.Exec(`
		UPDATE sessions
		SET
			plan_state_json = ?,
			updated_at = ?
		WHERE session_id = ?
	`,
		encodePlanState(state),
		now,
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("update plan state: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("plan state rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("update plan state: session %q not found", sessionID)
	}
	if strings.TrimSpace(string(kind)) == "" {
		return nil
	}
	if err := recordPlanEventTx(tx, sessionID, kind, state); err != nil {
		return err
	}
	return nil
}

func recordPlanEventTx(tx *sql.Tx, sessionID string, kind PlanEventKind, state PlanState) error {
	if _, err := tx.Exec(`
		INSERT INTO plan_events(session_id, event_kind, plan_state_json, created_at)
		VALUES (?, ?, ?, ?)
	`, sessionID, string(kind), encodePlanState(state), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("insert plan event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) rehydratePlanState(sessionID string) (PlanState, bool, error) {
	state, ok, err := s.latestPlanEventState(sessionID)
	if err != nil {
		return PlanState{}, false, err
	}
	if !ok {
		state, ok, err = s.latestTranscriptPlanState(sessionID)
		if err != nil {
			return PlanState{}, false, err
		}
	}
	if !ok {
		return PlanState{}, false, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return PlanState{}, false, fmt.Errorf("begin rehydrate plan tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := updatePlanStateTx(tx, sessionID, state, PlanEventKindRehydrated); err != nil {
		return PlanState{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PlanState{}, false, fmt.Errorf("commit rehydrate plan tx: %w", err)
	}
	return state, true, nil
}

func (s *SQLiteStore) latestPlanEventState(sessionID string) (PlanState, bool, error) {
	var raw sql.NullString
	err := s.db.QueryRow(`
		SELECT plan_state_json
		FROM plan_events
		WHERE session_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanState{}, false, nil
	}
	if err != nil {
		return PlanState{}, false, fmt.Errorf("load latest plan event: %w", err)
	}
	state := decodePlanState(raw.String)
	if len(state.Steps) == 0 && state.Explanation == "" {
		return PlanState{}, false, nil
	}
	return state, true, nil
}

func (s *SQLiteStore) latestTranscriptPlanState(sessionID string) (PlanState, bool, error) {
	var content sql.NullString
	err := s.db.QueryRow(`
		SELECT content
		FROM messages
		WHERE session_id = ? AND role = 'tool' AND tool_name = 'update_plan'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, sessionID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanState{}, false, nil
	}
	if err != nil {
		return PlanState{}, false, fmt.Errorf("load transcript plan state: %w", err)
	}
	state, ok := parseRenderedPlanState(content.String)
	return state, ok, nil
}
