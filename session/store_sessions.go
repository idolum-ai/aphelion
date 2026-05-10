//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (s *SQLiteStore) Load(key SessionKey) (*Session, error) {
	sessionID := SessionIDForKey(key)
	row := s.db.QueryRow(`
		SELECT
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, system_prompt, last_floor_text, last_floor_metadata, plan_state_json, operation_state_json, continuation_state_json,
			created_at, updated_at, turn_count,
			chat_type, chat_title, user_name,
			cache_last_write_block, cache_blocks_since, cache_last_write_time, cache_hit_rate, cache_consecutive_misses,
			total_input_tokens, total_output_tokens, total_cache_read, total_cache_write,
			last_provider, last_model, active_tool_calls, last_error
		FROM sessions
		WHERE session_id = ?
	`, sessionID)

	sess := &Session{}
	var (
		createdAtRaw          string
		updatedAtRaw          string
		cacheLastWriteRaw     sql.NullString
		scopeKind             sql.NullString
		scopeID               sql.NullString
		durableAgentID        sql.NullString
		systemPrompt          sql.NullString
		lastFloorText         sql.NullString
		lastFloorMetadata     sql.NullString
		planStateJSON         sql.NullString
		operationStateJSON    sql.NullString
		continuationStateJSON sql.NullString
		chatType              sql.NullString
		chatTitle             sql.NullString
		userName              sql.NullString
		lastProvider          sql.NullString
		lastModel             sql.NullString
		lastError             sql.NullString
		consecutiveMissesRaw  sql.NullInt64
	)

	err := row.Scan(
		&sess.SessionID, &sess.ChatID, &sess.UserID, &scopeKind, &scopeID, &durableAgentID, &systemPrompt, &lastFloorText, &lastFloorMetadata, &planStateJSON, &operationStateJSON, &continuationStateJSON, &createdAtRaw, &updatedAtRaw, &sess.TurnCount,
		&chatType, &chatTitle, &userName,
		&sess.CacheState.LastWriteBlock, &sess.CacheState.BlocksSinceWrite, &cacheLastWriteRaw, &sess.CacheState.HitRate, &consecutiveMissesRaw,
		&sess.TotalInputTokens, &sess.TotalOutputTokens, &sess.TotalCacheRead, &sess.TotalCacheWrite,
		&lastProvider, &lastModel, &sess.ActiveToolCalls, &lastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return s.createEmptySession(key)
	}
	if err != nil {
		return nil, fmt.Errorf("load session row: %w", err)
	}

	sess.SystemPrompt = nullToString(systemPrompt)
	sess.Scope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(scopeKind)),
		ID:             nullToString(scopeID),
		DurableAgentID: nullToString(durableAgentID),
	})
	if sess.Scope.IsZero() {
		sess.Scope = defaultScopeForKey(key)
	}
	sess.LastFloorText = nullToString(lastFloorText)
	sess.LastFloorMetadata = nullToString(lastFloorMetadata)
	sess.PlanState = decodePlanState(planStateJSON.String)
	sess.OperationState = decodeOperationState(operationStateJSON.String)
	sess.ContinuationState = decodeContinuationState(continuationStateJSON.String)
	if len(sess.PlanState.Steps) == 0 && sess.PlanState.Explanation == "" {
		if rehydrated, ok, rehydrateErr := s.rehydratePlanState(sessionID); rehydrateErr != nil {
			return nil, rehydrateErr
		} else if ok {
			sess.PlanState = rehydrated
		}
	}
	sess.ChatType = nullToString(chatType)
	sess.ChatTitle = nullToString(chatTitle)
	sess.UserName = nullToString(userName)
	sess.LastProvider = nullToString(lastProvider)
	sess.LastModel = nullToString(lastModel)
	sess.LastError = nullToString(lastError)
	if consecutiveMissesRaw.Valid {
		sess.CacheState.ConsecutiveMisses = int(consecutiveMissesRaw.Int64)
	}

	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	sess.CreatedAt = createdAt
	sess.UpdatedAt = updatedAt
	if cacheLastWriteRaw.Valid && cacheLastWriteRaw.String != "" {
		t, err := parseSQLiteTime(cacheLastWriteRaw.String)
		if err != nil {
			return nil, fmt.Errorf("parse cache_last_write_time: %w", err)
		}
		sess.CacheState.LastWriteTime = t
	}

	msgRows, err := s.db.Query(`
			SELECT id, session_id, chat_id, user_id, actor_user_id, actor_role, event_origin, event_origin_detail, role, content, floor_content, floor_metadata, tool_calls, tool_id, tool_name, thinking, created_at, turn_index, content_chars, compacted
			FROM messages
			WHERE session_id = ?
			ORDER BY turn_index, id
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer msgRows.Close()

	for msgRows.Next() {
		var (
			m            Message
			createdRaw   string
			actorRoleRaw sql.NullString
			originRaw    sql.NullString
			originDetRaw sql.NullString
			floorRaw     sql.NullString
			floorMetaRaw sql.NullString
			toolCallsRaw sql.NullString
			toolIDRaw    sql.NullString
			toolNameRaw  sql.NullString
			thinkingRaw  sql.NullString
			compactedRaw int
		)

		if err := msgRows.Scan(
			&m.ID, &m.SessionID, &m.ChatID, &m.UserID, &m.ActorUserID, &actorRoleRaw, &originRaw, &originDetRaw, &m.Role, &m.Content, &floorRaw, &floorMetaRaw, &toolCallsRaw, &toolIDRaw, &toolNameRaw, &thinkingRaw,
			&createdRaw, &m.TurnIndex, &m.ContentChars, &compactedRaw,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		m.ActorRole = nullToString(actorRoleRaw)
		m.EventOrigin = nullToString(originRaw)
		m.EventOriginDetail = nullToString(originDetRaw)
		m.FloorContent = nullToString(floorRaw)
		m.FloorMetadata = nullToString(floorMetaRaw)
		m.ToolCalls = nullToString(toolCallsRaw)
		m.ToolID = nullToString(toolIDRaw)
		m.ToolName = nullToString(toolNameRaw)
		m.Thinking = nullToString(thinkingRaw)
		m.Compacted = compactedRaw != 0
		m.CreatedAt, err = parseSQLiteTime(createdRaw)
		if err != nil {
			return nil, fmt.Errorf("parse message created_at: %w", err)
		}
		sess.Messages = append(sess.Messages, m)
	}
	if err := msgRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}

	compRows, err := s.db.Query(`
		SELECT timestamp, turns_before, turns_after, tokens_before, tokens_after, summary, strategy
		FROM compaction_log
		WHERE session_id = ?
		ORDER BY timestamp ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query compaction log: %w", err)
	}
	defer compRows.Close()

	for compRows.Next() {
		var (
			entry        CompactionEntry
			timestampRaw string
			summaryRaw   sql.NullString
			strategyRaw  sql.NullString
		)
		if err := compRows.Scan(
			&timestampRaw, &entry.TurnsBefore, &entry.TurnsAfter,
			&entry.TokensBefore, &entry.TokensAfter, &summaryRaw, &strategyRaw,
		); err != nil {
			return nil, fmt.Errorf("scan compaction entry: %w", err)
		}
		entry.Summary = nullToString(summaryRaw)
		entry.Strategy = nullToString(strategyRaw)
		entry.Timestamp, err = parseSQLiteTime(timestampRaw)
		if err != nil {
			return nil, fmt.Errorf("parse compaction timestamp: %w", err)
		}
		sess.CompactionLog = append(sess.CompactionLog, entry)
	}
	if err := compRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate compaction log: %w", err)
	}

	return sess, nil
}

func (s *SQLiteStore) Save(session *Session, newMessages []Message, usage core.TokenUsage) error {
	now := time.Now().UTC()
	session.Scope = defaultScopeForKey(SessionKey{
		ChatID: session.ChatID,
		UserID: session.UserID,
		Scope:  session.Scope,
	})
	session.SessionID = SessionIDFromParts(session.ChatID, session.UserID, session.Scope)
	session.PlanState = NormalizePlanState(session.PlanState)
	session.OperationState = NormalizeOperationState(session.OperationState)
	session.ContinuationState = NormalizeContinuationState(session.ContinuationState)
	session.UpdatedAt = now
	session.TotalInputTokens += usage.InputTokens
	session.TotalOutputTokens += usage.OutputTokens
	session.TotalCacheRead += usage.CacheReadTokens
	session.TotalCacheWrite += usage.CacheWriteTokens
	updateCacheStateForUsage(session, usage, now)

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin save tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := upsertSessionRow(tx, session, now); err != nil {
		return err
	}

	for i := range newMessages {
		msg := newMessages[i]
		msg.SessionID = session.SessionID
		if msg.ChatID == 0 {
			msg.ChatID = session.ChatID
		}
		if msg.UserID == 0 && session.UserID != 0 {
			msg.UserID = session.UserID
		}
		if msg.ContentChars == 0 {
			msg.ContentChars = len(msg.Content)
		}
		if msg.CreatedAt.IsZero() {
			msg.CreatedAt = now
		}
		if msg.TurnIndex == 0 {
			msg.TurnIndex = session.TurnCount
		}

		actorRole := strings.TrimSpace(msg.ActorRole)
		eventOrigin := strings.TrimSpace(msg.EventOrigin)
		eventOriginDetail := strings.TrimSpace(msg.EventOriginDetail)
		_, err := tx.Exec(`
				INSERT INTO messages(
					session_id, chat_id, user_id, actor_user_id, actor_role, event_origin, event_origin_detail, role, content, floor_content, floor_metadata, tool_calls, tool_id, tool_name, thinking,
					created_at, turn_index, content_chars, compacted
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
			msg.SessionID, msg.ChatID, msg.UserID, msg.ActorUserID, actorRole, eventOrigin, eventOriginDetail, msg.Role, msg.Content, nullableString(msg.FloorContent), nullableString(msg.FloorMetadata),
			nullableString(msg.ToolCalls), nullableString(msg.ToolID), nullableString(msg.ToolName), nullableString(msg.Thinking),
			msg.CreatedAt.UTC().Format(time.RFC3339Nano), msg.TurnIndex, msg.ContentChars, boolToInt(msg.Compacted),
		)
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
	}

	if err := upsertArtifactIndexRecords(tx, session); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save tx: %w", err)
	}
	return nil
}

func updateCacheStateForUsage(session *Session, usage core.TokenUsage, now time.Time) {
	if session == nil {
		return
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 && usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 {
		return
	}

	turnCount := session.TurnCount
	if turnCount < 1 {
		turnCount = 1
	}
	previousTurns := turnCount - 1
	previousHits := session.CacheState.HitRate * float64(previousTurns)
	if usage.CacheReadTokens > 0 {
		previousHits++
		session.CacheState.ConsecutiveMisses = 0
	} else {
		session.CacheState.ConsecutiveMisses++
	}
	session.CacheState.HitRate = previousHits / float64(turnCount)

	if usage.CacheWriteTokens > 0 {
		session.CacheState.LastWriteBlock = turnCount
		session.CacheState.BlocksSinceWrite = 0
		session.CacheState.LastWriteTime = now
		return
	}

	if session.CacheState.LastWriteBlock > 0 {
		session.CacheState.BlocksSinceWrite = turnCount - session.CacheState.LastWriteBlock
		if session.CacheState.BlocksSinceWrite < 0 {
			session.CacheState.BlocksSinceWrite = 0
		}
	}
}

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
			AND u.content = '/doctor'
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

func (s *SQLiteStore) Compact(key SessionKey, summary string, keepFromTurn int) error {
	if keepFromTurn < 0 {
		return fmt.Errorf("keepFromTurn must be >= 0")
	}
	summary = strings.TrimSpace(summary)
	sessionID := SessionIDForKey(key)
	strategy := "truncate"
	if summary != "" {
		strategy = "summarize"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin compact tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var turnsBefore, charsBefore int
	if err := tx.QueryRow(`
		SELECT COUNT(1), COALESCE(SUM(content_chars), 0)
		FROM messages
		WHERE session_id = ? AND compacted = 0
	`, sessionID).Scan(&turnsBefore, &charsBefore); err != nil {
		return fmt.Errorf("query pre-compaction stats: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE messages
		SET compacted = 1
		WHERE session_id = ? AND turn_index < ? AND compacted = 0
	`, sessionID, keepFromTurn); err != nil {
		return fmt.Errorf("compact old messages: %w", err)
	}

	if summary != "" {
		_, err := tx.Exec(`
			INSERT INTO messages(
				session_id, chat_id, user_id, role, content, created_at, turn_index, content_chars, compacted
			) VALUES (?, ?, ?, 'assistant', ?, ?, ?, ?, 0)
		`, sessionID, key.ChatID, key.UserID, summary, time.Now().UTC().Format(time.RFC3339Nano), keepFromTurn, len(summary))
		if err != nil {
			return fmt.Errorf("insert compaction summary: %w", err)
		}
	}

	var turnsAfter, charsAfter int
	if err := tx.QueryRow(`
		SELECT COUNT(1), COALESCE(SUM(content_chars), 0)
		FROM messages
		WHERE session_id = ? AND compacted = 0
	`, sessionID).Scan(&turnsAfter, &charsAfter); err != nil {
		return fmt.Errorf("query post-compaction stats: %w", err)
	}

	if _, err := tx.Exec(`
		INSERT INTO compaction_log(
			session_id, chat_id, user_id, timestamp, turns_before, turns_after, tokens_before, tokens_after, summary, strategy
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID, key.ChatID, key.UserID, time.Now().UTC().Format(time.RFC3339Nano),
		turnsBefore, turnsAfter, charsBefore/4, charsAfter/4, summary, strategy,
	); err != nil {
		return fmt.Errorf("insert compaction log: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE sessions
		SET
			cache_last_write_block = 0,
			cache_blocks_since = 0,
			cache_hit_rate = 0,
			cache_consecutive_misses = 0,
			updated_at = ?
		WHERE session_id = ?
	`, time.Now().UTC().Format(time.RFC3339Nano), sessionID); err != nil {
		return fmt.Errorf("update session after compaction: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit compact tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ExpireIdle(maxIdle time.Duration) (int, error) {
	if maxIdle < 0 {
		return 0, fmt.Errorf("maxIdle must be >= 0")
	}

	res, err := s.db.Exec(`
		DELETE FROM sessions
		WHERE updated_at < datetime('now', ?)
	`, sqliteNegativeDuration(maxIdle))
	if err != nil {
		return 0, fmt.Errorf("expire idle sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected expiring sessions: %w", err)
	}
	return int(n), nil
}

func (s *SQLiteStore) ListActive(since time.Duration) ([]SessionKey, error) {
	if since < 0 {
		return nil, fmt.Errorf("since must be >= 0")
	}

	rows, err := s.db.Query(`
		SELECT chat_id, user_id, scope_kind, scope_id, durable_agent_id
		FROM sessions
		WHERE updated_at >= datetime('now', ?)
		ORDER BY updated_at DESC
	`, sqliteNegativeDuration(since))
	if err != nil {
		return nil, fmt.Errorf("list active sessions: %w", err)
	}
	defer rows.Close()

	keys := make([]SessionKey, 0, 32)
	for rows.Next() {
		var (
			key            SessionKey
			scopeKind      sql.NullString
			scopeID        sql.NullString
			durableAgentID sql.NullString
		)
		if err := rows.Scan(&key.ChatID, &key.UserID, &scopeKind, &scopeID, &durableAgentID); err != nil {
			return nil, fmt.Errorf("scan active session key: %w", err)
		}
		key.Scope = NormalizeScopeRef(ScopeRef{
			Kind:           ScopeKind(nullToString(scopeKind)),
			ID:             nullToString(scopeID),
			DurableAgentID: nullToString(durableAgentID),
		})
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active sessions: %w", err)
	}
	return keys, nil
}

func (s *SQLiteStore) ForkAt(key SessionKey, turnIndex int, newContent string) error {
	sessionID := SessionIDForKey(key)
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin fork tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.Exec(`
		UPDATE messages
		SET compacted = 1
		WHERE session_id = ? AND turn_index > ?
	`, sessionID, turnIndex); err != nil {
		return fmt.Errorf("compact fork tail: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE messages
		SET content = ?, content_chars = ?, compacted = 0
		WHERE session_id = ? AND turn_index = ? AND role = 'user'
	`, newContent, len(newContent), sessionID, turnIndex); err != nil {
		return fmt.Errorf("update forked user message: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE sessions
		SET turn_count = ?, updated_at = ?
		WHERE session_id = ?
	`, turnIndex, time.Now().UTC().Format(time.RFC3339Nano), sessionID); err != nil {
		return fmt.Errorf("update session turn_count after fork: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fork tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) OutboundAfterTurn(key SessionKey, turnIndex int) ([]int64, error) {
	sessionID := SessionIDForKey(key)
	rows, err := s.db.Query(`
		SELECT telegram_msg_id
		FROM outbound_messages
		WHERE session_id = ? AND turn_index > ?
		ORDER BY telegram_msg_id
	`, sessionID, turnIndex)
	if err != nil {
		return nil, fmt.Errorf("query outbound after turn: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan outbound message id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbound message ids: %w", err)
	}
	return ids, nil
}

func (s *SQLiteStore) SearchMessages(query string, limit int, scope *SessionKey) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search query is required")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	pattern := "%" + query + "%"
	base := `
		SELECT session_id, chat_id, user_id, turn_index, role, content, floor_content, created_at
		FROM messages
		WHERE compacted = 0
			AND (
				LOWER(content) LIKE LOWER(?)
				OR LOWER(COALESCE(floor_content, '')) LIKE LOWER(?)
			)
	`
	args := []any{pattern, pattern}
	if scope != nil {
		base += ` AND session_id = ?`
		args = append(args, SessionIDForKey(*scope))
	}
	base += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(base, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()

	hits := make([]SearchHit, 0, limit)
	for rows.Next() {
		var (
			hit          SearchHit
			createdAtRaw string
			floorContent sql.NullString
		)
		if err := rows.Scan(
			&hit.SessionID, &hit.ChatID, &hit.UserID, &hit.TurnIndex, &hit.Role,
			&hit.Content, &floorContent, &createdAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan search hit: %w", err)
		}
		hit.FloorContent = nullToString(floorContent)
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse search hit created_at: %w", err)
		}
		hit.CreatedAt = createdAt
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search hits: %w", err)
	}
	return hits, nil
}

func (s *SQLiteStore) MessagesInWindow(start time.Time, end time.Time, limit int) ([]SearchHit, error) {
	if start.IsZero() || end.IsZero() {
		return nil, fmt.Errorf("message window requires non-zero start and end")
	}
	start = start.UTC()
	end = end.UTC()
	if !start.Before(end) {
		return nil, fmt.Errorf("message window requires start < end")
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	rows, err := s.db.Query(`
		SELECT session_id, chat_id, user_id, turn_index, role, content, floor_content, created_at
		FROM messages
		WHERE compacted = 0
			AND created_at >= ?
			AND created_at < ?
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, fmt.Errorf("messages in window: %w", err)
	}
	defer rows.Close()

	hits := make([]SearchHit, 0, limit)
	for rows.Next() {
		var (
			hit          SearchHit
			createdAtRaw string
			floorContent sql.NullString
		)
		if err := rows.Scan(
			&hit.SessionID, &hit.ChatID, &hit.UserID, &hit.TurnIndex, &hit.Role,
			&hit.Content, &floorContent, &createdAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan window message hit: %w", err)
		}
		hit.FloorContent = nullToString(floorContent)
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse window message created_at: %w", err)
		}
		hit.CreatedAt = createdAt
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate window message hits: %w", err)
	}
	return hits, nil
}

func (s *SQLiteStore) createEmptySession(key SessionKey) (*Session, error) {
	now := time.Now().UTC()
	scope := defaultScopeForKey(key)
	sess := &Session{
		SessionID: SessionIDFromParts(key.ChatID, key.UserID, scope),
		ChatID:    key.ChatID,
		UserID:    key.UserID,
		Scope:     scope,
		CreatedAt: now,
		UpdatedAt: now,
		ChatType:  "dm",
	}

	if _, err := s.db.Exec(`
		INSERT INTO sessions(
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, system_prompt, last_floor_text, plan_state_json, operation_state_json, continuation_state_json, created_at, updated_at, turn_count, chat_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sess.SessionID, key.ChatID, key.UserID, string(sess.Scope.Kind), sess.Scope.ID, sess.Scope.DurableAgentID,
		"", "", encodePlanState(sess.PlanState), encodeOperationState(sess.OperationState), encodeContinuationState(sess.ContinuationState), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), 0, sess.ChatType,
	); err != nil {
		return nil, fmt.Errorf("insert empty session: %w", err)
	}
	return sess, nil
}

func upsertSessionRow(tx *sql.Tx, session *Session, now time.Time) error {
	session.Scope = defaultScopeForKey(SessionKey{
		ChatID: session.ChatID,
		UserID: session.UserID,
		Scope:  session.Scope,
	})
	session.SessionID = SessionIDFromParts(session.ChatID, session.UserID, session.Scope)
	_, err := tx.Exec(`
		INSERT INTO sessions(
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, system_prompt, last_floor_text, last_floor_metadata, plan_state_json, operation_state_json, continuation_state_json, created_at, updated_at, turn_count,
			chat_type, chat_title, user_name,
			cache_last_write_block, cache_blocks_since, cache_last_write_time, cache_hit_rate, cache_consecutive_misses,
			total_input_tokens, total_output_tokens, total_cache_read, total_cache_write,
			last_provider, last_model, active_tool_calls, last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			chat_id = excluded.chat_id,
			user_id = excluded.user_id,
			scope_kind = excluded.scope_kind,
			scope_id = excluded.scope_id,
			durable_agent_id = excluded.durable_agent_id,
			system_prompt = excluded.system_prompt,
			last_floor_text = excluded.last_floor_text,
			last_floor_metadata = excluded.last_floor_metadata,
			plan_state_json = excluded.plan_state_json,
			operation_state_json = excluded.operation_state_json,
			continuation_state_json = excluded.continuation_state_json,
			updated_at = excluded.updated_at,
			turn_count = excluded.turn_count,
			chat_type = excluded.chat_type,
			chat_title = excluded.chat_title,
			user_name = excluded.user_name,
			cache_last_write_block = excluded.cache_last_write_block,
			cache_blocks_since = excluded.cache_blocks_since,
			cache_last_write_time = excluded.cache_last_write_time,
			cache_hit_rate = excluded.cache_hit_rate,
			cache_consecutive_misses = excluded.cache_consecutive_misses,
			total_input_tokens = excluded.total_input_tokens,
			total_output_tokens = excluded.total_output_tokens,
			total_cache_read = excluded.total_cache_read,
			total_cache_write = excluded.total_cache_write,
			last_provider = excluded.last_provider,
			last_model = excluded.last_model,
			active_tool_calls = excluded.active_tool_calls,
			last_error = excluded.last_error
	`,
		session.SessionID, session.ChatID, session.UserID, string(session.Scope.Kind), session.Scope.ID, session.Scope.DurableAgentID,
		session.SystemPrompt, nullableString(session.LastFloorText), nullableString(session.LastFloorMetadata), encodePlanState(session.PlanState), encodeOperationState(session.OperationState), encodeContinuationState(session.ContinuationState),
		nonZeroTimeOrNow(session.CreatedAt, now).UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), session.TurnCount,
		defaultChatType(session.ChatType), nullableString(session.ChatTitle), nullableString(session.UserName),
		session.CacheState.LastWriteBlock, session.CacheState.BlocksSinceWrite, nullableTime(session.CacheState.LastWriteTime), session.CacheState.HitRate, session.CacheState.ConsecutiveMisses,
		session.TotalInputTokens, session.TotalOutputTokens, session.TotalCacheRead, session.TotalCacheWrite,
		nullableString(session.LastProvider), nullableString(session.LastModel), session.ActiveToolCalls, nullableString(session.LastError),
	)
	if err != nil {
		return fmt.Errorf("upsert session row: %w", err)
	}
	return nil
}

func encodePlanState(state PlanState) string {
	normalized := NormalizePlanState(state)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func encodeOperationState(state OperationState) string {
	normalized := NormalizeOperationState(state)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func decodePlanState(raw string) PlanState {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PlanState{}
	}
	var state PlanState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return PlanState{}
	}
	return NormalizePlanState(state)
}

func decodeOperationState(raw string) OperationState {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return OperationState{}
	}
	var state OperationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return OperationState{}
	}
	return NormalizeOperationState(state)
}

func encodeContinuationState(state ContinuationState) string {
	normalized := NormalizeContinuationState(state)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func decodeContinuationState(raw string) ContinuationState {
	state, err := decodeContinuationStateStrict(raw)
	if err != nil {
		return ContinuationState{}
	}
	return state
}

func decodeContinuationStateStrict(raw string) (ContinuationState, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ContinuationState{}, nil
	}
	var state ContinuationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return ContinuationState{}, err
	}
	return NormalizeContinuationState(state), nil
}

func parseRenderedPlanState(raw string) (PlanState, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PlanState{}, false
	}

	var (
		state  PlanState
		header bool
	)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "[PLAN"):
			header = true
		case strings.HasPrefix(strings.ToLower(line), "explanation:"):
			state.Explanation = strings.TrimSpace(strings.TrimPrefix(line, "explanation:"))
		case strings.HasPrefix(line, "- ["):
			end := strings.Index(line, "]")
			if end <= 3 {
				continue
			}
			status := NormalizePlanStatus(PlanStatus(line[3:end]))
			step := strings.TrimSpace(line[end+1:])
			if status == "" || step == "" {
				continue
			}
			state.Steps = append(state.Steps, PlanStep{Step: step, Status: status})
		}
	}
	state = NormalizePlanState(state)
	if !header || (len(state.Steps) == 0 && state.Explanation == "") {
		return PlanState{}, false
	}
	return state, true
}
