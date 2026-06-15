//go:build linux

package session

import (
	"database/sql"
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
	prepareSessionForSave(session, usage, now)

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin save tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := saveSessionInTx(tx, session, newMessages, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save tx: %w", err)
	}
	return nil
}

func prepareSessionForSave(session *Session, usage core.TokenUsage, now time.Time) {
	if session == nil {
		return
	}
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
}

func saveSessionInTx(tx *sql.Tx, session *Session, newMessages []Message, now time.Time) error {
	if err := upsertSessionRow(tx, session, now); err != nil {
		return err
	}
	if err := insertSessionMessages(tx, session, newMessages, now); err != nil {
		return err
	}
	if err := upsertArtifactIndexRecords(tx, session); err != nil {
		return err
	}
	if err := upsertSessionEvidenceSnapshotsTx(tx, session, now); err != nil {
		return err
	}
	return nil
}

func insertSessionMessages(tx *sql.Tx, session *Session, newMessages []Message, now time.Time) error {
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
		res, err := tx.Exec(`
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
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("message last insert id: %w", err)
		}
		msg.ID = id
		if _, err := upsertEvidenceObjectTx(tx, messageEvidenceInput(msg, session.Scope)); err != nil {
			return fmt.Errorf("write message evidence: %w", err)
		}
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

func ensureSessionRowTx(tx *sql.Tx, key SessionKey, chatType string, now time.Time) error {
	if tx == nil {
		return fmt.Errorf("ensure session row: transaction is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	scope := defaultScopeForKey(key)
	sessionID := SessionIDFromParts(key.ChatID, key.UserID, scope)
	_, err := tx.Exec(`
		INSERT OR IGNORE INTO sessions(
			session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			system_prompt, last_floor_text, plan_state_json, operation_state_json, continuation_state_json,
			created_at, updated_at, turn_count, chat_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID, key.ChatID, key.UserID, string(scope.Kind), scope.ID, scope.DurableAgentID,
		"", "", "{}", "{}", "{}", now.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), 0, defaultChatType(chatType),
	)
	if err != nil {
		return fmt.Errorf("ensure session row %s: %w", sessionID, err)
	}
	return nil
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
