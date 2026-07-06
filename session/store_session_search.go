//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

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
				session_id, chat_id, user_id, actor_role, event_origin, event_origin_detail, role, content, created_at, turn_index, content_chars, compacted
			) VALUES (?, ?, ?, 'runtime', 'continuity', 'compaction_summary', 'assistant', ?, ?, ?, ?, 0)
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
			AND COALESCE(scope_kind, '') NOT IN ('telegram_thread', 'heartbeat', 'cron', 'recovery', 'curiosity', 'durable_agent')
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
