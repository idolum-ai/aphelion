//go:build linux

package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
)

const schemaVersion = 4

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteStore{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) init() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("apply %q: %w", p, err)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			system_prompt TEXT,
			last_canonical_reply TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			chat_type TEXT NOT NULL DEFAULT 'dm',
			chat_title TEXT,
			user_name TEXT,
			cache_last_write_block INTEGER NOT NULL DEFAULT 0,
			cache_blocks_since INTEGER NOT NULL DEFAULT 0,
			cache_last_write_time TEXT,
			cache_hit_rate REAL NOT NULL DEFAULT 0.0,
			cache_consecutive_misses INTEGER NOT NULL DEFAULT 0,
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_read INTEGER NOT NULL DEFAULT 0,
			total_cache_write INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			active_tool_calls INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			PRIMARY KEY (chat_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				chat_id INTEGER NOT NULL,
				user_id INTEGER NOT NULL DEFAULT 0,
				role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool')),
				content TEXT NOT NULL,
				canonical_content TEXT,
				tool_calls TEXT,
				tool_id TEXT,
				tool_name TEXT,
				thinking TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0,
			FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(chat_id, user_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_active ON messages(chat_id, user_id, compacted, turn_index)`,
		`CREATE TABLE IF NOT EXISTS outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_outbound_session ON outbound_messages(chat_id, user_id, turn_index)`,
		`CREATE TABLE IF NOT EXISTS review_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_chat_id INTEGER NOT NULL,
			source_user_id INTEGER NOT NULL DEFAULT 0,
			source_role TEXT NOT NULL,
			target_chat_id INTEGER NOT NULL,
			turn_from INTEGER,
			turn_to INTEGER,
			summary TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'delivered', 'dismissed')),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			delivered_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_review_events_target ON review_events(target_chat_id, status, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS turn_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			kind TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'failed', 'interrupted')),
			request_text TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT,
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_tool_name TEXT,
			last_tool_preview TEXT,
			tool_calls_started INTEGER NOT NULL DEFAULT 0,
			progress_message_id INTEGER,
			error_text TEXT,
			recovery_summary TEXT,
			recovery_logged_at TEXT,
			FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_session ON turn_runs(chat_id, user_id, started_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_recovery ON turn_runs(status, recovery_logged_at, started_at, id)`,
		`CREATE TABLE IF NOT EXISTS compaction_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			timestamp TEXT NOT NULL DEFAULT (datetime('now')),
			turns_before INTEGER,
			turns_after INTEGER,
			tokens_before INTEGER,
			tokens_after INTEGER,
			summary TEXT,
			strategy TEXT NOT NULL DEFAULT 'summarize',
			FOREIGN KEY (chat_id, user_id) REFERENCES sessions(chat_id, user_id) ON DELETE CASCADE
		)`,
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema statement: %w", err)
		}
	}

	if err := applyMigrations(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Load(key SessionKey) (*Session, error) {
	row := s.db.QueryRow(`
		SELECT
			chat_id, user_id, system_prompt, last_canonical_reply, created_at, updated_at, turn_count,
			chat_type, chat_title, user_name,
			cache_last_write_block, cache_blocks_since, cache_last_write_time, cache_hit_rate, cache_consecutive_misses,
			total_input_tokens, total_output_tokens, total_cache_read, total_cache_write,
			last_provider, last_model, active_tool_calls, last_error
		FROM sessions
		WHERE chat_id = ? AND user_id = ?
	`, key.ChatID, key.UserID)

	sess := &Session{}
	var (
		createdAtRaw         string
		updatedAtRaw         string
		cacheLastWriteRaw    sql.NullString
		systemPrompt         sql.NullString
		lastCanonicalReply   sql.NullString
		chatType             sql.NullString
		chatTitle            sql.NullString
		userName             sql.NullString
		lastProvider         sql.NullString
		lastModel            sql.NullString
		lastError            sql.NullString
		consecutiveMissesRaw sql.NullInt64
	)

	err := row.Scan(
		&sess.ChatID, &sess.UserID, &systemPrompt, &lastCanonicalReply, &createdAtRaw, &updatedAtRaw, &sess.TurnCount,
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
	sess.LastCanonicalReply = nullToString(lastCanonicalReply)
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
			SELECT id, chat_id, user_id, role, content, canonical_content, tool_calls, tool_id, tool_name, thinking, created_at, turn_index, content_chars, compacted
			FROM messages
			WHERE chat_id = ? AND user_id = ?
			ORDER BY turn_index, id
	`, key.ChatID, key.UserID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer msgRows.Close()

	for msgRows.Next() {
		var (
			m            Message
			createdRaw   string
			canonicalRaw sql.NullString
			toolCallsRaw sql.NullString
			toolIDRaw    sql.NullString
			toolNameRaw  sql.NullString
			thinkingRaw  sql.NullString
			compactedRaw int
		)

		if err := msgRows.Scan(
			&m.ID, &m.ChatID, &m.UserID, &m.Role, &m.Content, &canonicalRaw, &toolCallsRaw, &toolIDRaw, &toolNameRaw, &thinkingRaw,
			&createdRaw, &m.TurnIndex, &m.ContentChars, &compactedRaw,
		); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		m.CanonicalContent = nullToString(canonicalRaw)
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
		WHERE chat_id = ? AND user_id = ?
		ORDER BY timestamp ASC, id ASC
	`, key.ChatID, key.UserID)
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
	session.UpdatedAt = now
	session.TotalInputTokens += usage.InputTokens
	session.TotalOutputTokens += usage.OutputTokens

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

		_, err := tx.Exec(`
				INSERT INTO messages(
					chat_id, user_id, role, content, canonical_content, tool_calls, tool_id, tool_name, thinking,
					created_at, turn_index, content_chars, compacted
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`,
			msg.ChatID, msg.UserID, msg.Role, msg.Content, nullableString(msg.CanonicalContent),
			nullableString(msg.ToolCalls), nullableString(msg.ToolID), nullableString(msg.ToolName), nullableString(msg.Thinking),
			msg.CreatedAt.UTC().Format(time.RFC3339Nano), msg.TurnIndex, msg.ContentChars, boolToInt(msg.Compacted),
		)
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpdateCacheState(key SessionKey, state CacheState) error {
	_, err := s.db.Exec(`
		UPDATE sessions
		SET
			cache_last_write_block = ?,
			cache_blocks_since = ?,
			cache_last_write_time = ?,
			cache_hit_rate = ?,
			cache_consecutive_misses = ?,
			updated_at = ?
		WHERE chat_id = ? AND user_id = ?
	`,
		state.LastWriteBlock,
		state.BlocksSinceWrite,
		nullableTime(state.LastWriteTime),
		state.HitRate,
		state.ConsecutiveMisses,
		time.Now().UTC().Format(time.RFC3339Nano),
		key.ChatID, key.UserID,
	)
	if err != nil {
		return fmt.Errorf("update cache state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Compact(key SessionKey, summary string, keepFromTurn int) error {
	if keepFromTurn < 0 {
		return fmt.Errorf("keepFromTurn must be >= 0")
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
		WHERE chat_id = ? AND user_id = ? AND compacted = 0
	`, key.ChatID, key.UserID).Scan(&turnsBefore, &charsBefore); err != nil {
		return fmt.Errorf("query pre-compaction stats: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE messages
		SET compacted = 1
		WHERE chat_id = ? AND user_id = ? AND turn_index < ? AND compacted = 0
	`, key.ChatID, key.UserID, keepFromTurn); err != nil {
		return fmt.Errorf("compact old messages: %w", err)
	}

	if strings.TrimSpace(summary) != "" {
		_, err := tx.Exec(`
			INSERT INTO messages(
				chat_id, user_id, role, content, created_at, turn_index, content_chars, compacted
			) VALUES (?, ?, 'assistant', ?, ?, ?, ?, 0)
		`, key.ChatID, key.UserID, summary, time.Now().UTC().Format(time.RFC3339Nano), keepFromTurn, len(summary))
		if err != nil {
			return fmt.Errorf("insert compaction summary: %w", err)
		}
	}

	var turnsAfter, charsAfter int
	if err := tx.QueryRow(`
		SELECT COUNT(1), COALESCE(SUM(content_chars), 0)
		FROM messages
		WHERE chat_id = ? AND user_id = ? AND compacted = 0
	`, key.ChatID, key.UserID).Scan(&turnsAfter, &charsAfter); err != nil {
		return fmt.Errorf("query post-compaction stats: %w", err)
	}

	if _, err := tx.Exec(`
		INSERT INTO compaction_log(
			chat_id, user_id, timestamp, turns_before, turns_after, tokens_before, tokens_after, summary, strategy
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'summarize')
	`,
		key.ChatID, key.UserID, time.Now().UTC().Format(time.RFC3339Nano),
		turnsBefore, turnsAfter, charsBefore/4, charsAfter/4, summary,
	); err != nil {
		return fmt.Errorf("insert compaction log: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE sessions
		SET
			cache_blocks_since = 0,
			updated_at = ?
		WHERE chat_id = ? AND user_id = ?
	`, time.Now().UTC().Format(time.RFC3339Nano), key.ChatID, key.UserID); err != nil {
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
		SELECT chat_id, user_id
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
		var key SessionKey
		if err := rows.Scan(&key.ChatID, &key.UserID); err != nil {
			return nil, fmt.Errorf("scan active session key: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active sessions: %w", err)
	}
	return keys, nil
}

func (s *SQLiteStore) ForkAt(key SessionKey, turnIndex int, newContent string) error {
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
		WHERE chat_id = ? AND user_id = ? AND turn_index > ?
	`, key.ChatID, key.UserID, turnIndex); err != nil {
		return fmt.Errorf("compact fork tail: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE messages
		SET content = ?, content_chars = ?, compacted = 0
		WHERE chat_id = ? AND user_id = ? AND turn_index = ? AND role = 'user'
	`, newContent, len(newContent), key.ChatID, key.UserID, turnIndex); err != nil {
		return fmt.Errorf("update forked user message: %w", err)
	}

	if _, err := tx.Exec(`
		UPDATE sessions
		SET turn_count = ?, updated_at = ?
		WHERE chat_id = ? AND user_id = ?
	`, turnIndex, time.Now().UTC().Format(time.RFC3339Nano), key.ChatID, key.UserID); err != nil {
		return fmt.Errorf("update session turn_count after fork: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fork tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) OutboundAfterTurn(key SessionKey, turnIndex int) ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT telegram_msg_id
		FROM outbound_messages
		WHERE chat_id = ? AND user_id = ? AND turn_index > ?
		ORDER BY telegram_msg_id
	`, key.ChatID, key.UserID, turnIndex)
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

func (s *SQLiteStore) RecordOutbound(key SessionKey, turnIndex int, telegramMsgID int64, msgType string) error {
	if telegramMsgID == 0 {
		return fmt.Errorf("record outbound: telegram_msg_id is required")
	}
	if strings.TrimSpace(msgType) == "" {
		msgType = "text"
	}

	_, err := s.db.Exec(`
		INSERT INTO outbound_messages(chat_id, user_id, turn_index, telegram_msg_id, msg_type)
		VALUES (?, ?, ?, ?, ?)
	`, key.ChatID, key.UserID, turnIndex, telegramMsgID, msgType)
	if err != nil {
		return fmt.Errorf("record outbound: %w", err)
	}
	return nil
}

func (s *SQLiteStore) EnqueueReviewEvent(event ReviewEvent) error {
	if event.SourceChatID == 0 {
		return fmt.Errorf("enqueue review event: source_chat_id is required")
	}
	if strings.TrimSpace(event.SourceRole) == "" {
		return fmt.Errorf("enqueue review event: source_role is required")
	}
	if event.TargetAdminChatID == 0 {
		return fmt.Errorf("enqueue review event: target_chat_id is required")
	}
	if strings.TrimSpace(event.Summary) == "" {
		return fmt.Errorf("enqueue review event: summary is required")
	}

	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "pending"
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
		INSERT INTO review_events(
			source_chat_id, source_user_id, source_role, target_chat_id,
			turn_from, turn_to, summary, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.SourceChatID, event.SourceUserID, event.SourceRole, event.TargetAdminChatID,
		event.TurnFrom, event.TurnTo, event.Summary, status, now,
	)
	if err != nil {
		return fmt.Errorf("enqueue review event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PendingReviewEvents(targetChatID int64, limit int) ([]ReviewEvent, error) {
	if targetChatID == 0 {
		return nil, fmt.Errorf("pending review events: target_chat_id is required")
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT
			id, source_chat_id, source_user_id, source_role, target_chat_id,
			turn_from, turn_to, summary, status, created_at, delivered_at
		FROM review_events
		WHERE target_chat_id = ? AND status = 'pending'
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, targetChatID, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending review events: %w", err)
	}
	defer rows.Close()

	events := make([]ReviewEvent, 0, limit)
	for rows.Next() {
		var (
			event           ReviewEvent
			createdAtRaw    string
			deliveredAtRaw  sql.NullString
			turnFromRaw     sql.NullInt64
			turnToRaw       sql.NullInt64
			targetChatIDRaw int64
		)

		if err := rows.Scan(
			&event.ID, &event.SourceChatID, &event.SourceUserID, &event.SourceRole, &targetChatIDRaw,
			&turnFromRaw, &turnToRaw, &event.Summary, &event.Status, &createdAtRaw, &deliveredAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan pending review event: %w", err)
		}

		event.TargetAdminChatID = targetChatIDRaw
		if turnFromRaw.Valid {
			event.TurnFrom = int(turnFromRaw.Int64)
		}
		if turnToRaw.Valid {
			event.TurnTo = int(turnToRaw.Int64)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse review event created_at: %w", err)
		}
		event.CreatedAt = createdAt
		if deliveredAtRaw.Valid && deliveredAtRaw.String != "" {
			deliveredAt, err := parseSQLiteTime(deliveredAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse review event delivered_at: %w", err)
			}
			event.DeliveredAt = deliveredAt
		}

		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending review events: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) MarkReviewDelivered(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin mark review delivered tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.Prepare(`
		UPDATE review_events
		SET status = 'delivered', delivered_at = ?
		WHERE id = ? AND status = 'pending'
	`)
	if err != nil {
		return fmt.Errorf("prepare mark review delivered statement: %w", err)
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, err := stmt.Exec(now, id); err != nil {
			return fmt.Errorf("mark review delivered id=%d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mark review delivered tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) BeginTurnRun(key SessionKey, kind TurnRunKind, requestText string) (*TurnRun, error) {
	now := time.Now().UTC()
	kind = TurnRunKind(strings.TrimSpace(string(kind)))
	if kind == "" {
		kind = TurnRunKindInteractive
	}

	res, err := s.db.Exec(`
		INSERT INTO turn_runs(
			chat_id, user_id, kind, status, request_text, started_at, last_activity_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`,
		key.ChatID, key.UserID, string(kind), string(TurnRunStatusRunning), requestText,
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
		ChatID:         key.ChatID,
		UserID:         key.UserID,
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
			id, chat_id, user_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started,
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

func (s *SQLiteStore) PendingRecoveryTurnRuns(limit int) ([]TurnRun, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT
			id, chat_id, user_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started,
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
	rows, err := s.db.Query(`
		SELECT
			id, chat_id, user_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE chat_id = ? AND user_id = ?
		ORDER BY id DESC
		LIMIT 1
	`, key.ChatID, key.UserID)
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

func (s *SQLiteStore) TurnRun(id int64) (*TurnRun, error) {
	rows, err := s.db.Query(`
		SELECT
			id, chat_id, user_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started,
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

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) createEmptySession(key SessionKey) (*Session, error) {
	now := time.Now().UTC()
	sess := &Session{
		ChatID:    key.ChatID,
		UserID:    key.UserID,
		CreatedAt: now,
		UpdatedAt: now,
		ChatType:  "dm",
	}

	if _, err := s.db.Exec(`
		INSERT INTO sessions(
			chat_id, user_id, system_prompt, last_canonical_reply, created_at, updated_at, turn_count, chat_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		key.ChatID, key.UserID, "", "", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), 0, sess.ChatType,
	); err != nil {
		return nil, fmt.Errorf("insert empty session: %w", err)
	}
	return sess, nil
}

func upsertSessionRow(tx *sql.Tx, session *Session, now time.Time) error {
	_, err := tx.Exec(`
		INSERT INTO sessions(
			chat_id, user_id, system_prompt, last_canonical_reply, created_at, updated_at, turn_count,
			chat_type, chat_title, user_name,
			cache_last_write_block, cache_blocks_since, cache_last_write_time, cache_hit_rate, cache_consecutive_misses,
			total_input_tokens, total_output_tokens, total_cache_read, total_cache_write,
			last_provider, last_model, active_tool_calls, last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, user_id) DO UPDATE SET
			system_prompt = excluded.system_prompt,
			last_canonical_reply = excluded.last_canonical_reply,
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
		session.ChatID, session.UserID, session.SystemPrompt, nullableString(session.LastCanonicalReply),
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

func applyMigrations(tx *sql.Tx) error {
	if err := ensureSessionColumn(tx, "last_canonical_reply", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.last_canonical_reply: %w", err)
	}
	if err := ensureTableColumn(tx, "messages", "canonical_content", "TEXT"); err != nil {
		return fmt.Errorf("ensure messages.canonical_content: %w", err)
	}

	currentVersion, err := currentSchemaVersion(tx)
	if err != nil {
		return err
	}
	if currentVersion >= schemaVersion {
		return nil
	}

	for version := currentVersion + 1; version <= schemaVersion; version++ {
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("insert schema version %d: %w", version, err)
		}
	}
	return nil
}

func currentSchemaVersion(tx *sql.Tx) (int, error) {
	var maxVersion sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&maxVersion); err != nil {
		return 0, fmt.Errorf("query schema version: %w", err)
	}
	if !maxVersion.Valid {
		return 0, nil
	}
	return int(maxVersion.Int64), nil
}

func ensureSessionColumn(tx *sql.Tx, name string, columnType string) error {
	return ensureTableColumn(tx, "sessions", name, columnType)
}

func ensureTableColumn(tx *sql.Tx, table string, name string, columnType string) error {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("query table_info(%s): %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid      int
			column   string
			typ      string
			notNull  int
			defaultV sql.NullString
			primaryK int
		)
		if err := rows.Scan(&cid, &column, &typ, &notNull, &defaultV, &primaryK); err != nil {
			return fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if strings.EqualFold(column, name) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table_info(%s): %w", table, err)
	}

	stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, columnType)
	if _, err := tx.Exec(stmt); err != nil {
		return fmt.Errorf("alter %s add column %s: %w", table, name, err)
	}
	return nil
}

func defaultChatType(chatType string) string {
	if strings.TrimSpace(chatType) == "" {
		return "dm"
	}
	return chatType
}

func nullToString(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}

func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}

func scanTurnRun(scanner interface{ Scan(dest ...any) error }) (TurnRun, error) {
	var (
		run                 TurnRun
		kindRaw             string
		statusRaw           string
		startedAtRaw        string
		completedAtRaw      sql.NullString
		lastActivityAtRaw   string
		lastToolNameRaw     sql.NullString
		lastToolPreviewRaw  sql.NullString
		progressMessageRaw  sql.NullInt64
		errorTextRaw        sql.NullString
		recoverySummaryRaw  sql.NullString
		recoveryLoggedAtRaw sql.NullString
	)

	if err := scanner.Scan(
		&run.ID, &run.ChatID, &run.UserID, &kindRaw, &statusRaw, &run.RequestText, &startedAtRaw, &completedAtRaw,
		&lastActivityAtRaw, &lastToolNameRaw, &lastToolPreviewRaw, &run.ToolCallsStarted,
		&progressMessageRaw, &errorTextRaw, &recoverySummaryRaw, &recoveryLoggedAtRaw,
	); err != nil {
		return TurnRun{}, fmt.Errorf("scan turn run: %w", err)
	}

	var err error
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
	run.ErrorText = nullToString(errorTextRaw)
	run.RecoverySummary = nullToString(recoverySummaryRaw)
	return run, nil
}

func mustParseSQLiteTime(raw string) time.Time {
	t, err := parseSQLiteTime(raw)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nonZeroTimeOrNow(t, now time.Time) time.Time {
	if t.IsZero() {
		return now
	}
	return t
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func sqliteNegativeDuration(d time.Duration) string {
	seconds := int64(d.Seconds())
	if seconds < 0 {
		seconds = -seconds
	}
	return fmt.Sprintf("-%d seconds", seconds)
}

func parseSQLiteTime(raw string) (time.Time, error) {
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format %q", raw)
}
