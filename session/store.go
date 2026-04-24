//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	_ "github.com/mattn/go-sqlite3"
)

const (
	schemaVersion                       = 32
	minimumSupportedLegacySchemaVersion = 11
)

type SQLiteStore struct {
	db     *sql.DB
	dbPath string
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteStore{db: db, dbPath: strings.TrimSpace(dbPath)}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) DBPath() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.dbPath)
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
			session_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			system_prompt TEXT,
			last_floor_text TEXT,
			last_floor_metadata TEXT,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			operation_state_json TEXT NOT NULL DEFAULT '{}',
			continuation_state_json TEXT NOT NULL DEFAULT '{}',
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
			last_error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			actor_user_id INTEGER NOT NULL DEFAULT 0,
			actor_role TEXT NOT NULL DEFAULT '',
			event_origin TEXT NOT NULL DEFAULT '',
			event_origin_detail TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool')),
			content TEXT NOT NULL,
			floor_content TEXT,
			floor_metadata TEXT,
			tool_calls TEXT,
			tool_id TEXT,
			tool_name TEXT,
			thinking TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS review_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_session_id TEXT,
			source_chat_id INTEGER NOT NULL DEFAULT 0,
			source_user_id INTEGER NOT NULL DEFAULT 0,
			source_role TEXT NOT NULL,
			source_scope_kind TEXT NOT NULL DEFAULT '',
			source_scope_id TEXT NOT NULL DEFAULT '',
			source_durable_agent_id TEXT NOT NULL DEFAULT '',
			target_session_id TEXT,
			target_chat_id INTEGER NOT NULL DEFAULT 0,
			target_scope_kind TEXT NOT NULL DEFAULT '',
			target_scope_id TEXT NOT NULL DEFAULT '',
			target_durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_from INTEGER,
			turn_to INTEGER,
			summary TEXT NOT NULL,
			metadata_json TEXT,
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'delivered', 'dismissed')),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			delivered_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS plan_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			event_kind TEXT NOT NULL,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS tool_proposals (
			proposal_id TEXT PRIMARY KEY,
			proposed_by TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL,
			why_now TEXT NOT NULL DEFAULT '',
			contract_json TEXT NOT NULL DEFAULT '{}',
			review_status TEXT NOT NULL DEFAULT 'proposed' CHECK(review_status IN ('proposed', 'approved', 'rejected')),
			registered_tool_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS registered_tools (
			tool_name TEXT PRIMARY KEY,
			implementation_ref TEXT NOT NULL DEFAULT '',
			registered INTEGER NOT NULL DEFAULT 0 CHECK(registered IN (0, 1)),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS tool_exposures (
			tool_name TEXT NOT NULL,
			principal TEXT NOT NULL,
			active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0, 1)),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (tool_name, principal)
		)`,
		`CREATE TABLE IF NOT EXISTS tool_install_records (
			tool_name TEXT PRIMARY KEY,
			installer TEXT NOT NULL DEFAULT '',
			install_ref TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '' CHECK(status IN ('', 'pending', 'installed', 'verified', 'failed', 'stale')),
			probe_status TEXT NOT NULL DEFAULT '' CHECK(probe_status IN ('', 'passed', 'failed')),
			probe_output TEXT NOT NULL DEFAULT '',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			installed_at TEXT,
			last_probed_at TEXT,
			last_failure_at TEXT,
			attested_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS tool_probe_records (
			tool_name TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT '' CHECK(status IN ('', 'passed', 'failed')),
			probe_output TEXT NOT NULL DEFAULT '',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			probed_at TEXT,
			last_failure_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS tool_audit_records (
			tool_name TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT '' CHECK(status IN ('', 'passed', 'failed')),
			audit_output TEXT NOT NULL DEFAULT '',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			audited_at TEXT,
			last_failure_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS turn_runs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'failed', 'interrupted')),
			request_text TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT,
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_tool_name TEXT,
			last_tool_preview TEXT,
			tool_calls_started INTEGER NOT NULL DEFAULT 0,
			tool_calls_finished INTEGER NOT NULL DEFAULT 0,
			last_tool_result_preview TEXT,
			last_tool_error TEXT,
			progress_message_id INTEGER,
				error_text TEXT,
				recovery_summary TEXT,
				recovery_logged_at TEXT
			)`,
		`CREATE TABLE IF NOT EXISTS execution_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL,
				chat_id INTEGER NOT NULL DEFAULT 0,
				user_id INTEGER NOT NULL DEFAULT 0,
				scope_kind TEXT NOT NULL DEFAULT '',
				scope_id TEXT NOT NULL DEFAULT '',
				durable_agent_id TEXT NOT NULL DEFAULT '',
				seq INTEGER NOT NULL,
				event_type TEXT NOT NULL,
				stage TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT '',
				caused_by_seq INTEGER NOT NULL DEFAULT 0,
				payload_json TEXT NOT NULL DEFAULT '{}',
				created_at TEXT NOT NULL DEFAULT (datetime('now'))
			)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS pending_decisions (
				decision_id TEXT PRIMARY KEY,
				decision_seq INTEGER NOT NULL DEFAULT 0,
			owner_key TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			message_id INTEGER NOT NULL DEFAULT 0,
			prompt TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			rationale TEXT NOT NULL DEFAULT '',
			artifact_refs_json TEXT NOT NULL DEFAULT '[]',
			choices_json TEXT NOT NULL DEFAULT '[]',
			default_choice TEXT NOT NULL DEFAULT '',
			timeout_ns INTEGER NOT NULL DEFAULT 0,
			delivery_message_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pending_decisions_owner_seq ON pending_decisions(owner_key, decision_seq DESC)`,
		`CREATE TABLE IF NOT EXISTS pending_artifact_retention (
			owner_key TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			inbound_message_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS pending_busy_decisions (
			owner_key TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			inbound_message_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agents (
			agent_id TEXT PRIMARY KEY,
			parent_agent_id TEXT,
			parent_scope_kind TEXT,
			parent_scope_id TEXT,
			review_target_chat_id INTEGER NOT NULL DEFAULT 0,
			channel_kind TEXT NOT NULL,
			live_policy_json TEXT NOT NULL DEFAULT '{}',
			channel_config_json TEXT NOT NULL DEFAULT '{}',
			bootstrap_ceiling_json TEXT NOT NULL DEFAULT '{}',
			bootstrap_provider_json TEXT NOT NULL DEFAULT '{}',
			control_plane_secret TEXT NOT NULL DEFAULT '',
			policy_version INTEGER NOT NULL DEFAULT 1,
			policy_hash TEXT NOT NULL DEFAULT '',
			policy_issued_at TEXT,
			local_storage_roots_json TEXT NOT NULL DEFAULT '[]',
			network_policy TEXT,
			wakeup_mode TEXT,
			secret_scopes_json TEXT NOT NULL DEFAULT '[]',
			allowed_telegram_user_ids_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_state (
				agent_id TEXT PRIMARY KEY,
				cursor TEXT,
				status TEXT,
				state_json TEXT,
				last_apply_status TEXT NOT NULL DEFAULT '',
				last_apply_error TEXT NOT NULL DEFAULT '',
				last_wake_at TEXT,
				last_review_at TEXT,
				dormant_at TEXT,
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
			)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_identity_state (
				agent_id TEXT PRIMARY KEY,
				last_offered_policy_version INTEGER NOT NULL DEFAULT 0,
				last_offered_policy_hash TEXT NOT NULL DEFAULT '',
				last_offered_policy_at TEXT,
				last_acknowledged_policy_version INTEGER NOT NULL DEFAULT 0,
				last_acknowledged_policy_hash TEXT NOT NULL DEFAULT '',
				last_acknowledged_policy_at TEXT,
				last_applied_policy_version INTEGER NOT NULL DEFAULT 0,
				last_applied_policy_hash TEXT NOT NULL DEFAULT '',
				last_applied_policy_at TEXT,
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
			)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_policy_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			source_review_event_id INTEGER NOT NULL DEFAULT 0,
			previous_version INTEGER NOT NULL DEFAULT 0,
			new_version INTEGER NOT NULL,
			policy_hash TEXT NOT NULL,
			policy_json TEXT NOT NULL,
			reason TEXT,
			applied_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_bootstrap_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			source_review_event_id INTEGER NOT NULL DEFAULT 0,
			actor_user_id INTEGER NOT NULL DEFAULT 0,
			actor_role TEXT NOT NULL DEFAULT '',
			update_kind TEXT NOT NULL DEFAULT '',
			previous_bootstrap_json TEXT NOT NULL DEFAULT '{}',
			new_bootstrap_json TEXT NOT NULL DEFAULT '{}',
			reason TEXT,
			applied_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_remote_enrollments (
			agent_id TEXT PRIMARY KEY,
			parent_control_url TEXT NOT NULL DEFAULT '',
			key_fingerprint TEXT NOT NULL DEFAULT '',
			protocol_version TEXT NOT NULL DEFAULT 'v1',
			status TEXT NOT NULL DEFAULT 'active',
			last_sequence INTEGER NOT NULL DEFAULT 0,
			enrolled_at TEXT,
			last_seen_at TEXT,
			revoked_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_control_receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			message_kind TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			received_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(agent_id, message_id),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS compaction_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			timestamp TEXT NOT NULL DEFAULT (datetime('now')),
			turns_before INTEGER,
			turns_after INTEGER,
			tokens_before INTEGER,
			tokens_after INTEGER,
			summary TEXT,
			strategy TEXT NOT NULL DEFAULT 'summarize'
		)`,
		`CREATE TABLE IF NOT EXISTS rhizome_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			name TEXT NOT NULL,
			event_count INTEGER NOT NULL DEFAULT 0,
			last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(scope, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rhizome_nodes_scope ON rhizome_nodes(scope, name)`,
		`CREATE TABLE IF NOT EXISTS rhizome_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			source TEXT NOT NULL,
			salience REAL NOT NULL DEFAULT 1.0,
			concepts_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rhizome_events_scope ON rhizome_events(scope, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS rhizome_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			left_concept TEXT NOT NULL,
			right_concept TEXT NOT NULL,
			strength REAL NOT NULL DEFAULT 0,
			recurrence_count INTEGER NOT NULL DEFAULT 0,
			last_reinforced_at TEXT NOT NULL DEFAULT (datetime('now')),
			decay_state TEXT NOT NULL DEFAULT 'hot',
			last_source TEXT,
			UNIQUE(scope, left_concept, right_concept)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rhizome_edges_scope ON rhizome_edges(scope, strength DESC, recurrence_count DESC)`,
		`CREATE TABLE IF NOT EXISTS artifact_index (
			session_id TEXT NOT NULL,
			turn_index INTEGER NOT NULL DEFAULT 0,
			artifact_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			source_type TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			handling TEXT NOT NULL DEFAULT '',
			retention TEXT NOT NULL DEFAULT '',
			fetch_state TEXT NOT NULL DEFAULT '',
			materialized_path TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (session_id, turn_index, artifact_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_session_turn ON artifact_index(session_id, turn_index DESC, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_summary ON artifact_index(summary, kind, updated_at DESC)`,
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema statement: %w", err)
		}
	}

	if err := applyMigrations(tx); err != nil {
		return err
	}
	if err := ensureSessionIdentityIndexes(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema tx: %w", err)
	}
	return nil
}

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
	return decodeContinuationState(raw.String), true, nil
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

func (s *SQLiteStore) UpsertPendingArtifactRetention(record PendingArtifactRetentionRecord) error {
	record.OwnerKey = strings.TrimSpace(record.OwnerKey)
	if record.OwnerKey == "" {
		return fmt.Errorf("pending artifact retention owner_key is required")
	}
	record.InboundMessageJSON = strings.TrimSpace(record.InboundMessageJSON)
	if record.InboundMessageJSON == "" {
		record.InboundMessageJSON = "{}"
	}
	now := time.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	_, err := s.db.Exec(`
		INSERT INTO pending_artifact_retention(
			owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_key) DO UPDATE SET
			chat_id = excluded.chat_id,
			sender_id = excluded.sender_id,
			inbound_message_json = excluded.inbound_message_json,
			updated_at = excluded.updated_at
	`, record.OwnerKey, record.ChatID, record.SenderID, record.InboundMessageJSON, createdAt.UTC().Format(time.RFC3339Nano), updatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert pending artifact retention: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PendingArtifactRetention(ownerKey string) (*PendingArtifactRetentionRecord, error) {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil, sql.ErrNoRows
	}
	var record PendingArtifactRetentionRecord
	var createdAtRaw, updatedAtRaw string
	err := s.db.QueryRow(`
		SELECT owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		FROM pending_artifact_retention
		WHERE owner_key = ?
	`, ownerKey).Scan(&record.OwnerKey, &record.ChatID, &record.SenderID, &record.InboundMessageJSON, &createdAtRaw, &updatedAtRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("load pending artifact retention: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending artifact retention created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending artifact retention updated_at: %w", err)
	}
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return &record, nil
}

func (s *SQLiteStore) DeletePendingArtifactRetention(ownerKey string) error {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM pending_artifact_retention WHERE owner_key = ?`, ownerKey); err != nil {
		return fmt.Errorf("delete pending artifact retention: %w", err)
	}
	return nil
}

func (s *SQLiteStore) UpsertPendingBusyDecision(record PendingBusyDecisionRecord) error {
	record.OwnerKey = strings.TrimSpace(record.OwnerKey)
	if record.OwnerKey == "" {
		return fmt.Errorf("pending busy decision owner_key is required")
	}
	record.InboundMessageJSON = strings.TrimSpace(record.InboundMessageJSON)
	if record.InboundMessageJSON == "" {
		record.InboundMessageJSON = "{}"
	}
	now := time.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	_, err := s.db.Exec(`
		INSERT INTO pending_busy_decisions(
			owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_key) DO UPDATE SET
			chat_id = excluded.chat_id,
			sender_id = excluded.sender_id,
			inbound_message_json = excluded.inbound_message_json,
			updated_at = excluded.updated_at
	`, record.OwnerKey, record.ChatID, record.SenderID, record.InboundMessageJSON, createdAt.UTC().Format(time.RFC3339Nano), updatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("upsert pending busy decision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) PendingBusyDecision(ownerKey string) (*PendingBusyDecisionRecord, error) {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil, sql.ErrNoRows
	}
	var record PendingBusyDecisionRecord
	var createdAtRaw, updatedAtRaw string
	err := s.db.QueryRow(`
		SELECT owner_key, chat_id, sender_id, inbound_message_json, created_at, updated_at
		FROM pending_busy_decisions
		WHERE owner_key = ?
	`, ownerKey).Scan(&record.OwnerKey, &record.ChatID, &record.SenderID, &record.InboundMessageJSON, &createdAtRaw, &updatedAtRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		return nil, fmt.Errorf("load pending busy decision: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending busy decision created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return nil, fmt.Errorf("parse pending busy decision updated_at: %w", err)
	}
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return &record, nil
}

func (s *SQLiteStore) DeletePendingBusyDecision(ownerKey string) error {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM pending_busy_decisions WHERE owner_key = ?`, ownerKey); err != nil {
		return fmt.Errorf("delete pending busy decision: %w", err)
	}
	return nil
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

func (s *SQLiteStore) UpsertToolProposal(proposal ToolProposal) (ToolProposal, error) {
	proposal = NormalizeToolProposal(proposal)
	if proposal.ProposalID == "" {
		return ToolProposal{}, fmt.Errorf("tool proposal id is required")
	}
	if proposal.ToolName == "" {
		return ToolProposal{}, fmt.Errorf("tool proposal tool_name is required")
	}
	if proposal.ReviewStatus == "" {
		proposal.ReviewStatus = ToolProposalReviewStatusProposed
	}
	if proposal.Contract == "" {
		proposal.Contract = "{}"
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(proposal.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(proposal.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO tool_proposals(
			proposal_id, proposed_by, tool_name, why_now, contract_json, review_status, registered_tool_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(proposal_id) DO UPDATE SET
			proposed_by = excluded.proposed_by,
			tool_name = excluded.tool_name,
			why_now = excluded.why_now,
			contract_json = excluded.contract_json,
			review_status = excluded.review_status,
			registered_tool_id = excluded.registered_tool_id,
			updated_at = excluded.updated_at
	`,
		proposal.ProposalID,
		proposal.ProposedBy,
		proposal.ToolName,
		proposal.WhyNow,
		proposal.Contract,
		string(proposal.ReviewStatus),
		proposal.RegisteredToolID,
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return ToolProposal{}, fmt.Errorf("upsert tool proposal: %w", err)
	}
	stored, ok, err := s.ToolProposal(proposal.ProposalID)
	if err != nil {
		return ToolProposal{}, err
	}
	if !ok {
		return ToolProposal{}, fmt.Errorf("tool proposal %q not found after upsert", proposal.ProposalID)
	}
	return stored, nil
}

func (s *SQLiteStore) ToolProposal(proposalID string) (ToolProposal, bool, error) {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return ToolProposal{}, false, nil
	}
	var (
		record       ToolProposal
		reviewStatus string
		createdAtRaw string
		updatedAtRaw string
	)
	err := s.db.QueryRow(`
		SELECT proposal_id, proposed_by, tool_name, why_now, contract_json, review_status, registered_tool_id, created_at, updated_at
		FROM tool_proposals
		WHERE proposal_id = ?
	`, proposalID).Scan(
		&record.ProposalID,
		&record.ProposedBy,
		&record.ToolName,
		&record.WhyNow,
		&record.Contract,
		&reviewStatus,
		&record.RegisteredToolID,
		&createdAtRaw,
		&updatedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolProposal{}, false, nil
	}
	if err != nil {
		return ToolProposal{}, false, fmt.Errorf("load tool proposal: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolProposal{}, false, fmt.Errorf("parse tool proposal created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolProposal{}, false, fmt.Errorf("parse tool proposal updated_at: %w", err)
	}
	record.ReviewStatus = ToolProposalReviewStatus(reviewStatus)
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return NormalizeToolProposal(record), true, nil
}

func (s *SQLiteStore) ToolProposals(limit int, reviewStatus ToolProposalReviewStatus) ([]ToolProposal, error) {
	if limit <= 0 {
		limit = 20
	}
	reviewStatus = NormalizeToolProposalReviewStatus(reviewStatus)
	query := `
		SELECT proposal_id, proposed_by, tool_name, why_now, contract_json, review_status, registered_tool_id, created_at, updated_at
		FROM tool_proposals
	`
	args := []any{}
	if reviewStatus != "" {
		query += ` WHERE review_status = ?`
		args = append(args, string(reviewStatus))
	}
	query += ` ORDER BY updated_at DESC, proposal_id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool proposals: %w", err)
	}
	defer rows.Close()

	out := make([]ToolProposal, 0, limit)
	for rows.Next() {
		var (
			record       ToolProposal
			statusRaw    string
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(
			&record.ProposalID,
			&record.ProposedBy,
			&record.ToolName,
			&record.WhyNow,
			&record.Contract,
			&statusRaw,
			&record.RegisteredToolID,
			&createdAtRaw,
			&updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan tool proposal: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool proposal created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool proposal updated_at: %w", err)
		}
		record.ReviewStatus = ToolProposalReviewStatus(statusRaw)
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		out = append(out, NormalizeToolProposal(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool proposals: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) UpsertRegisteredTool(record RegisteredTool) (RegisteredTool, error) {
	record = NormalizeRegisteredTool(record)
	if record.ToolName == "" {
		return RegisteredTool{}, fmt.Errorf("registered tool name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO registered_tools(tool_name, implementation_ref, registered, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			implementation_ref = excluded.implementation_ref,
			registered = excluded.registered,
			updated_at = excluded.updated_at
	`,
		record.ToolName,
		record.ImplementationRef,
		boolToInt(record.Registered),
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return RegisteredTool{}, fmt.Errorf("upsert registered tool: %w", err)
	}
	stored, ok, err := s.RegisteredTool(record.ToolName)
	if err != nil {
		return RegisteredTool{}, err
	}
	if !ok {
		return RegisteredTool{}, fmt.Errorf("registered tool %q not found after upsert", record.ToolName)
	}
	return stored, nil
}

func (s *SQLiteStore) RegisteredTool(toolName string) (RegisteredTool, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return RegisteredTool{}, false, nil
	}
	var (
		record       RegisteredTool
		registered   int
		createdAtRaw string
		updatedAtRaw string
	)
	err := s.db.QueryRow(`
		SELECT tool_name, implementation_ref, registered, created_at, updated_at
		FROM registered_tools
		WHERE tool_name = ?
	`, toolName).Scan(
		&record.ToolName,
		&record.ImplementationRef,
		&registered,
		&createdAtRaw,
		&updatedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return RegisteredTool{}, false, nil
	}
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("load registered tool: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("parse registered tool created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return RegisteredTool{}, false, fmt.Errorf("parse registered tool updated_at: %w", err)
	}
	record.Registered = registered != 0
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return NormalizeRegisteredTool(record), true, nil
}

func (s *SQLiteStore) RegisteredTools(limit int) ([]RegisteredTool, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT tool_name, implementation_ref, registered, created_at, updated_at
		FROM registered_tools
		ORDER BY updated_at DESC, tool_name ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query registered tools: %w", err)
	}
	defer rows.Close()

	out := make([]RegisteredTool, 0, limit)
	for rows.Next() {
		var (
			record       RegisteredTool
			registered   int
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(
			&record.ToolName,
			&record.ImplementationRef,
			&registered,
			&createdAtRaw,
			&updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan registered tool: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse registered tool created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse registered tool updated_at: %w", err)
		}
		record.Registered = registered != 0
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		out = append(out, NormalizeRegisteredTool(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate registered tools: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) UpsertToolExposure(record ToolExposure) (ToolExposure, error) {
	record = NormalizeToolExposure(record)
	if record.ToolName == "" {
		return ToolExposure{}, fmt.Errorf("tool exposure tool_name is required")
	}
	if record.Principal == "" {
		return ToolExposure{}, fmt.Errorf("tool exposure principal is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO tool_exposures(tool_name, principal, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tool_name, principal) DO UPDATE SET
			active = excluded.active,
			updated_at = excluded.updated_at
	`,
		record.ToolName,
		record.Principal,
		boolToInt(record.Active),
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return ToolExposure{}, fmt.Errorf("upsert tool exposure: %w", err)
	}
	stored, ok, err := s.ToolExposure(record.ToolName, record.Principal)
	if err != nil {
		return ToolExposure{}, err
	}
	if !ok {
		return ToolExposure{}, fmt.Errorf("tool exposure %q/%q not found after upsert", record.ToolName, record.Principal)
	}
	return stored, nil
}

func (s *SQLiteStore) ToolExposure(toolName string, principal string) (ToolExposure, bool, error) {
	toolName = strings.TrimSpace(toolName)
	principal = strings.TrimSpace(principal)
	if toolName == "" || principal == "" {
		return ToolExposure{}, false, nil
	}
	var (
		record       ToolExposure
		active       int
		createdAtRaw string
		updatedAtRaw string
	)
	err := s.db.QueryRow(`
		SELECT tool_name, principal, active, created_at, updated_at
		FROM tool_exposures
		WHERE tool_name = ? AND principal = ?
	`, toolName, principal).Scan(
		&record.ToolName,
		&record.Principal,
		&active,
		&createdAtRaw,
		&updatedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolExposure{}, false, nil
	}
	if err != nil {
		return ToolExposure{}, false, fmt.Errorf("load tool exposure: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolExposure{}, false, fmt.Errorf("parse tool exposure created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolExposure{}, false, fmt.Errorf("parse tool exposure updated_at: %w", err)
	}
	record.Active = active != 0
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	return NormalizeToolExposure(record), true, nil
}

func (s *SQLiteStore) ToolExposures(toolName string, principal string, limit int) ([]ToolExposure, error) {
	if limit <= 0 {
		limit = 50
	}
	toolName = strings.TrimSpace(toolName)
	principal = strings.TrimSpace(principal)

	query := `
		SELECT tool_name, principal, active, created_at, updated_at
		FROM tool_exposures
	`
	args := make([]any, 0, 3)
	clauses := make([]string, 0, 2)
	if toolName != "" {
		clauses = append(clauses, "tool_name = ?")
		args = append(args, toolName)
	}
	if principal != "" {
		clauses = append(clauses, "principal = ?")
		args = append(args, principal)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY updated_at DESC, tool_name ASC, principal ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool exposures: %w", err)
	}
	defer rows.Close()

	out := make([]ToolExposure, 0, limit)
	for rows.Next() {
		var (
			record       ToolExposure
			active       int
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(
			&record.ToolName,
			&record.Principal,
			&active,
			&createdAtRaw,
			&updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan tool exposure: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool exposure created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool exposure updated_at: %w", err)
		}
		record.Active = active != 0
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		out = append(out, NormalizeToolExposure(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool exposures: %w", err)
	}
	return out, nil
}

func (s *SQLiteStore) UpsertToolInstallRecord(record ToolInstallRecord) (ToolInstallRecord, error) {
	record = NormalizeToolInstallRecord(record)
	if record.ToolName == "" {
		return ToolInstallRecord{}, fmt.Errorf("tool install record tool_name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO tool_install_records(tool_name, installer, install_ref, status, probe_status, probe_output, consecutive_failures, created_at, updated_at, installed_at, last_probed_at, last_failure_at, attested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			installer = excluded.installer,
			install_ref = excluded.install_ref,
			status = excluded.status,
			probe_status = excluded.probe_status,
			probe_output = excluded.probe_output,
			consecutive_failures = excluded.consecutive_failures,
			updated_at = excluded.updated_at,
			installed_at = excluded.installed_at,
			last_probed_at = excluded.last_probed_at,
			last_failure_at = excluded.last_failure_at,
			attested_at = excluded.attested_at
	`,
		record.ToolName,
		record.Installer,
		record.InstallRef,
		string(record.Status),
		string(record.ProbeStatus),
		record.ProbeOutput,
		record.ConsecutiveFailures,
		createdAt.Format(time.RFC3339Nano),
		updatedAt.Format(time.RFC3339Nano),
		nullableTimeRFC3339(record.InstalledAt),
		nullableTimeRFC3339(record.LastProbedAt),
		nullableTimeRFC3339(record.LastFailureAt),
		nullableTimeRFC3339(record.AttestedAt),
	); err != nil {
		return ToolInstallRecord{}, fmt.Errorf("upsert tool install record: %w", err)
	}
	stored, ok, err := s.ToolInstallRecord(record.ToolName)
	if err != nil {
		return ToolInstallRecord{}, err
	}
	if !ok {
		return ToolInstallRecord{}, fmt.Errorf("tool install record %q not found after upsert", record.ToolName)
	}
	return stored, nil
}
func (s *SQLiteStore) ToolInstallRecord(toolName string) (ToolInstallRecord, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolInstallRecord{}, false, nil
	}
	var (
		record                 ToolInstallRecord
		statusRaw              string
		probeStatusRaw         string
		consecutiveFailuresRaw int
		createdAtRaw           string
		updatedAtRaw           string
		installedAtRaw         sql.NullString
		lastProbedAtRaw        sql.NullString
		lastFailureAtRaw       sql.NullString
		attestedAtRaw          sql.NullString
	)
	err := s.db.QueryRow(`
		SELECT tool_name, installer, install_ref, status, probe_status, probe_output, consecutive_failures, created_at, updated_at, installed_at, last_probed_at, last_failure_at, attested_at
		FROM tool_install_records
		WHERE tool_name = ?
	`, toolName).Scan(
		&record.ToolName,
		&record.Installer,
		&record.InstallRef,
		&statusRaw,
		&probeStatusRaw,
		&record.ProbeOutput,
		&consecutiveFailuresRaw,
		&createdAtRaw,
		&updatedAtRaw,
		&installedAtRaw,
		&lastProbedAtRaw,
		&lastFailureAtRaw,
		&attestedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolInstallRecord{}, false, nil
	}
	if err != nil {
		return ToolInstallRecord{}, false, fmt.Errorf("load tool install record: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record updated_at: %w", err)
	}
	record.Status = NormalizeToolInstallStatus(ToolInstallStatus(statusRaw))
	record.ProbeStatus = NormalizeToolProbeStatus(ToolProbeStatus(probeStatusRaw))
	record.ConsecutiveFailures = consecutiveFailuresRaw
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	if installedAtRaw.Valid {
		record.InstalledAt, err = parseSQLiteTime(installedAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record installed_at: %w", err)
		}
	}
	if lastProbedAtRaw.Valid {
		record.LastProbedAt, err = parseSQLiteTime(lastProbedAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record last_probed_at: %w", err)
		}
	}
	if lastFailureAtRaw.Valid {
		record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record last_failure_at: %w", err)
		}
	}
	if attestedAtRaw.Valid {
		record.AttestedAt, err = parseSQLiteTime(attestedAtRaw.String)
		if err != nil {
			return ToolInstallRecord{}, false, fmt.Errorf("parse tool install record attested_at: %w", err)
		}
	}
	return NormalizeToolInstallRecord(record), true, nil
}
func (s *SQLiteStore) ToolInstallRecords(status ToolInstallStatus, limit int) ([]ToolInstallRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	status = NormalizeToolInstallStatus(status)
	query := `
		SELECT tool_name, installer, install_ref, status, probe_status, probe_output, consecutive_failures, created_at, updated_at, installed_at, last_probed_at, last_failure_at, attested_at
		FROM tool_install_records
	`
	args := make([]any, 0, 2)
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, string(status))
	}
	query += " ORDER BY updated_at DESC, tool_name ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool install records: %w", err)
	}
	defer rows.Close()
	out := make([]ToolInstallRecord, 0, limit)
	for rows.Next() {
		var (
			record                 ToolInstallRecord
			statusRaw              string
			probeStatusRaw         string
			consecutiveFailuresRaw int
			createdAtRaw           string
			updatedAtRaw           string
			installedAtRaw         sql.NullString
			lastProbedAtRaw        sql.NullString
			lastFailureAtRaw       sql.NullString
			attestedAtRaw          sql.NullString
		)
		if err := rows.Scan(&record.ToolName, &record.Installer, &record.InstallRef, &statusRaw, &probeStatusRaw, &record.ProbeOutput, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &installedAtRaw, &lastProbedAtRaw, &lastFailureAtRaw, &attestedAtRaw); err != nil {
			return nil, fmt.Errorf("scan tool install record: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool install record created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool install record updated_at: %w", err)
		}
		record.Status = NormalizeToolInstallStatus(ToolInstallStatus(statusRaw))
		record.ProbeStatus = NormalizeToolProbeStatus(ToolProbeStatus(probeStatusRaw))
		record.ConsecutiveFailures = consecutiveFailuresRaw
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		if installedAtRaw.Valid {
			record.InstalledAt, err = parseSQLiteTime(installedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record installed_at: %w", err)
			}
		}
		if lastProbedAtRaw.Valid {
			record.LastProbedAt, err = parseSQLiteTime(lastProbedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record last_probed_at: %w", err)
			}
		}
		if lastFailureAtRaw.Valid {
			record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record last_failure_at: %w", err)
			}
		}
		if attestedAtRaw.Valid {
			record.AttestedAt, err = parseSQLiteTime(attestedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool install record attested_at: %w", err)
			}
		}
		out = append(out, NormalizeToolInstallRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool install records: %w", err)
	}
	return out, nil
}

func nullableTimeRFC3339(ts time.Time) any {
	if ts.IsZero() {
		return nil
	}
	return ts.UTC().Format(time.RFC3339Nano)
}
func (s *SQLiteStore) UpsertToolProbeRecord(record ToolProbeRecord) (ToolProbeRecord, error) {
	record = NormalizeToolProbeRecord(record)
	if record.ToolName == "" {
		return ToolProbeRecord{}, fmt.Errorf("tool probe record tool_name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO tool_probe_records(tool_name, status, probe_output, consecutive_failures, created_at, updated_at, probed_at, last_failure_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			status = excluded.status,
			probe_output = excluded.probe_output,
			consecutive_failures = excluded.consecutive_failures,
			updated_at = excluded.updated_at,
			probed_at = excluded.probed_at,
			last_failure_at = excluded.last_failure_at
	`, record.ToolName, string(record.Status), record.ProbeOutput, record.ConsecutiveFailures, createdAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano), nullableTimeRFC3339(record.ProbedAt), nullableTimeRFC3339(record.LastFailureAt)); err != nil {
		return ToolProbeRecord{}, fmt.Errorf("upsert tool probe record: %w", err)
	}
	stored, ok, err := s.ToolProbeRecord(record.ToolName)
	if err != nil {
		return ToolProbeRecord{}, err
	}
	if !ok {
		return ToolProbeRecord{}, fmt.Errorf("tool probe record %q not found after upsert", record.ToolName)
	}
	return stored, nil
}
func (s *SQLiteStore) ToolProbeRecord(toolName string) (ToolProbeRecord, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolProbeRecord{}, false, nil
	}
	var (
		record                 ToolProbeRecord
		statusRaw              string
		consecutiveFailuresRaw int
		createdAtRaw           string
		updatedAtRaw           string
		probedAtRaw            sql.NullString
		lastFailureAtRaw       sql.NullString
	)
	err := s.db.QueryRow(`SELECT tool_name, status, probe_output, consecutive_failures, created_at, updated_at, probed_at, last_failure_at FROM tool_probe_records WHERE tool_name = ?`, toolName).Scan(&record.ToolName, &statusRaw, &record.ProbeOutput, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &probedAtRaw, &lastFailureAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolProbeRecord{}, false, nil
	}
	if err != nil {
		return ToolProbeRecord{}, false, fmt.Errorf("load tool probe record: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record updated_at: %w", err)
	}
	record.Status = NormalizeToolProbeStatus(ToolProbeStatus(statusRaw))
	record.ConsecutiveFailures = consecutiveFailuresRaw
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	if probedAtRaw.Valid {
		record.ProbedAt, err = parseSQLiteTime(probedAtRaw.String)
		if err != nil {
			return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record probed_at: %w", err)
		}
	}
	if lastFailureAtRaw.Valid {
		record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
		if err != nil {
			return ToolProbeRecord{}, false, fmt.Errorf("parse tool probe record last_failure_at: %w", err)
		}
	}
	return NormalizeToolProbeRecord(record), true, nil
}
func (s *SQLiteStore) ToolProbeRecords(status ToolProbeStatus, limit int) ([]ToolProbeRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	status = NormalizeToolProbeStatus(status)
	query := `SELECT tool_name, status, probe_output, consecutive_failures, created_at, updated_at, probed_at, last_failure_at FROM tool_probe_records`
	args := make([]any, 0, 2)
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, string(status))
	}
	query += " ORDER BY updated_at DESC, tool_name ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool probe records: %w", err)
	}
	defer rows.Close()
	out := make([]ToolProbeRecord, 0, limit)
	for rows.Next() {
		var (
			record                 ToolProbeRecord
			statusRaw              string
			consecutiveFailuresRaw int
			createdAtRaw           string
			updatedAtRaw           string
			probedAtRaw            sql.NullString
			lastFailureAtRaw       sql.NullString
		)
		if err := rows.Scan(&record.ToolName, &statusRaw, &record.ProbeOutput, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &probedAtRaw, &lastFailureAtRaw); err != nil {
			return nil, fmt.Errorf("scan tool probe record: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool probe record created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool probe record updated_at: %w", err)
		}
		record.Status = NormalizeToolProbeStatus(ToolProbeStatus(statusRaw))
		record.ConsecutiveFailures = consecutiveFailuresRaw
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		if probedAtRaw.Valid {
			record.ProbedAt, err = parseSQLiteTime(probedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool probe record probed_at: %w", err)
			}
		}
		if lastFailureAtRaw.Valid {
			record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool probe record last_failure_at: %w", err)
			}
		}
		out = append(out, NormalizeToolProbeRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool probe records: %w", err)
	}
	return out, nil
}
func (s *SQLiteStore) UpsertToolAuditRecord(record ToolAuditRecord) (ToolAuditRecord, error) {
	record = NormalizeToolAuditRecord(record)
	if record.ToolName == "" {
		return ToolAuditRecord{}, fmt.Errorf("tool audit record tool_name is required")
	}
	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(record.CreatedAt, now).UTC()
	updatedAt := nonZeroTimeOrNow(record.UpdatedAt, now).UTC()
	if _, err := s.db.Exec(`
		INSERT INTO tool_audit_records(tool_name, status, audit_output, consecutive_failures, created_at, updated_at, audited_at, last_failure_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tool_name) DO UPDATE SET
			status = excluded.status,
			audit_output = excluded.audit_output,
			consecutive_failures = excluded.consecutive_failures,
			updated_at = excluded.updated_at,
			audited_at = excluded.audited_at,
			last_failure_at = excluded.last_failure_at
	`, record.ToolName, string(record.Status), record.AuditOutput, record.ConsecutiveFailures, createdAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano), nullableTimeRFC3339(record.AuditedAt), nullableTimeRFC3339(record.LastFailureAt)); err != nil {
		return ToolAuditRecord{}, fmt.Errorf("upsert tool audit record: %w", err)
	}
	stored, ok, err := s.ToolAuditRecord(record.ToolName)
	if err != nil {
		return ToolAuditRecord{}, err
	}
	if !ok {
		return ToolAuditRecord{}, fmt.Errorf("tool audit record %q not found after upsert", record.ToolName)
	}
	return stored, nil
}
func (s *SQLiteStore) ToolAuditRecord(toolName string) (ToolAuditRecord, bool, error) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return ToolAuditRecord{}, false, nil
	}
	var (
		record                 ToolAuditRecord
		statusRaw              string
		consecutiveFailuresRaw int
		createdAtRaw           string
		updatedAtRaw           string
		auditedAtRaw           sql.NullString
		lastFailureAtRaw       sql.NullString
	)
	err := s.db.QueryRow(`SELECT tool_name, status, audit_output, consecutive_failures, created_at, updated_at, audited_at, last_failure_at FROM tool_audit_records WHERE tool_name = ?`, toolName).Scan(&record.ToolName, &statusRaw, &record.AuditOutput, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &auditedAtRaw, &lastFailureAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ToolAuditRecord{}, false, nil
	}
	if err != nil {
		return ToolAuditRecord{}, false, fmt.Errorf("load tool audit record: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record updated_at: %w", err)
	}
	record.Status = NormalizeToolAuditStatus(ToolAuditStatus(statusRaw))
	record.ConsecutiveFailures = consecutiveFailuresRaw
	record.CreatedAt = createdAt
	record.UpdatedAt = updatedAt
	if auditedAtRaw.Valid {
		record.AuditedAt, err = parseSQLiteTime(auditedAtRaw.String)
		if err != nil {
			return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record audited_at: %w", err)
		}
	}
	if lastFailureAtRaw.Valid {
		record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
		if err != nil {
			return ToolAuditRecord{}, false, fmt.Errorf("parse tool audit record last_failure_at: %w", err)
		}
	}
	return NormalizeToolAuditRecord(record), true, nil
}
func (s *SQLiteStore) ToolAuditRecords(status ToolAuditStatus, limit int) ([]ToolAuditRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	status = NormalizeToolAuditStatus(status)
	query := `SELECT tool_name, status, audit_output, consecutive_failures, created_at, updated_at, audited_at, last_failure_at FROM tool_audit_records`
	args := make([]any, 0, 2)
	if status != "" {
		query += " WHERE status = ?"
		args = append(args, string(status))
	}
	query += " ORDER BY updated_at DESC, tool_name ASC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool audit records: %w", err)
	}
	defer rows.Close()
	out := make([]ToolAuditRecord, 0, limit)
	for rows.Next() {
		var (
			record                 ToolAuditRecord
			statusRaw              string
			consecutiveFailuresRaw int
			createdAtRaw           string
			updatedAtRaw           string
			auditedAtRaw           sql.NullString
			lastFailureAtRaw       sql.NullString
		)
		if err := rows.Scan(&record.ToolName, &statusRaw, &record.AuditOutput, &consecutiveFailuresRaw, &createdAtRaw, &updatedAtRaw, &auditedAtRaw, &lastFailureAtRaw); err != nil {
			return nil, fmt.Errorf("scan tool audit record: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool audit record created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse tool audit record updated_at: %w", err)
		}
		record.Status = NormalizeToolAuditStatus(ToolAuditStatus(statusRaw))
		record.ConsecutiveFailures = consecutiveFailuresRaw
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		if auditedAtRaw.Valid {
			record.AuditedAt, err = parseSQLiteTime(auditedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool audit record audited_at: %w", err)
			}
		}
		if lastFailureAtRaw.Valid {
			record.LastFailureAt, err = parseSQLiteTime(lastFailureAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse tool audit record last_failure_at: %w", err)
			}
		}
		out = append(out, NormalizeToolAuditRecord(record))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tool audit records: %w", err)
	}
	return out, nil
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

func (s *SQLiteStore) SearchArtifacts(query string, limit int, scope *SessionKey) ([]ArtifactRecord, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("artifact search query is required")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	pattern := "%" + query + "%"
	base := `
		SELECT artifact_id, session_id, chat_id, user_id, turn_index, source_type, kind, summary,
			handling, retention, fetch_state, materialized_path, created_at, updated_at
		FROM artifact_index
		WHERE (
			LOWER(summary) LIKE LOWER(?)
			OR LOWER(kind) LIKE LOWER(?)
			OR LOWER(source_type) LIKE LOWER(?)
			OR LOWER(materialized_path) LIKE LOWER(?)
		)
	`
	args := []any{pattern, pattern, pattern, pattern}
	if scope != nil {
		base += ` AND session_id = ?`
		args = append(args, SessionIDForKey(*scope))
	}
	base += ` ORDER BY updated_at DESC, turn_index DESC, artifact_id DESC, session_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(base, args...)
	if err != nil {
		return nil, fmt.Errorf("search artifacts: %w", err)
	}
	defer rows.Close()

	hits := make([]ArtifactRecord, 0, limit)
	for rows.Next() {
		var (
			hit          ArtifactRecord
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(
			&hit.ArtifactID, &hit.SessionID, &hit.ChatID, &hit.UserID, &hit.TurnIndex,
			&hit.SourceType, &hit.Kind, &hit.Summary, &hit.Handling, &hit.Retention,
			&hit.FetchState, &hit.MaterializedPath, &createdAtRaw, &updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan artifact search hit: %w", err)
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse artifact search created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse artifact search updated_at: %w", err)
		}
		hit.CreatedAt = createdAt
		hit.UpdatedAt = updatedAt
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifact search hits: %w", err)
	}
	return hits, nil
}

func (s *SQLiteStore) RecordRhizomeEvent(scope string, source string, salience float64, concepts []string) error {
	scope = strings.TrimSpace(scope)
	source = strings.TrimSpace(source)
	normalized := normalizeRhizomeConcepts(concepts)
	if scope == "" {
		return fmt.Errorf("rhizome scope is required")
	}
	if source == "" {
		source = "reflection"
	}
	if len(normalized) < 2 {
		return nil
	}
	if salience <= 0 {
		salience = 1
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin rhizome event tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, concept := range normalized {
		if _, err := tx.Exec(`
			INSERT INTO rhizome_nodes(scope, name, event_count, last_seen_at)
			VALUES (?, ?, 1, ?)
			ON CONFLICT(scope, name) DO UPDATE SET
				event_count = event_count + 1,
				last_seen_at = excluded.last_seen_at
		`, scope, concept, now); err != nil {
			return fmt.Errorf("upsert rhizome node %q: %w", concept, err)
		}
	}

	conceptsJSON, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("marshal rhizome concepts: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO rhizome_events(scope, source, salience, concepts_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, scope, source, salience, string(conceptsJSON), now); err != nil {
		return fmt.Errorf("insert rhizome event: %w", err)
	}

	for i := 0; i < len(normalized); i++ {
		for j := i + 1; j < len(normalized); j++ {
			left, right := orderedPair(normalized[i], normalized[j])
			if _, err := tx.Exec(`
				INSERT INTO rhizome_edges(
					scope, left_concept, right_concept, strength, recurrence_count, last_reinforced_at, decay_state, last_source
				) VALUES (?, ?, ?, ?, 1, ?, ?, ?)
				ON CONFLICT(scope, left_concept, right_concept) DO UPDATE SET
					strength = rhizome_edges.strength + excluded.strength,
					recurrence_count = rhizome_edges.recurrence_count + 1,
					last_reinforced_at = excluded.last_reinforced_at,
					decay_state = CASE
						WHEN rhizome_edges.recurrence_count + 1 >= 8 THEN 'frozen'
						WHEN rhizome_edges.recurrence_count + 1 >= 5 THEN 'cold'
						WHEN rhizome_edges.recurrence_count + 1 >= 3 THEN 'warm'
						ELSE 'hot'
					END,
					last_source = excluded.last_source
			`, scope, left, right, salience, now, classifyRhizomeDecayState(1), source); err != nil {
				return fmt.Errorf("upsert rhizome edge %q/%q: %w", left, right, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rhizome event tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) TopRhizomeEdges(scope string, limit int) ([]RhizomeEdge, error) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return nil, fmt.Errorf("rhizome scope is required")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, scope, left_concept, right_concept, strength, recurrence_count, last_reinforced_at, decay_state, COALESCE(last_source, '')
		FROM rhizome_edges
		WHERE scope = ?
		ORDER BY strength DESC, recurrence_count DESC, last_reinforced_at DESC
		LIMIT ?
	`, scope, limit)
	if err != nil {
		return nil, fmt.Errorf("query rhizome edges: %w", err)
	}
	defer rows.Close()

	edges := make([]RhizomeEdge, 0, limit)
	for rows.Next() {
		var edge RhizomeEdge
		var ts string
		if err := rows.Scan(&edge.ID, &edge.Scope, &edge.LeftConcept, &edge.RightConcept, &edge.Strength, &edge.RecurrenceCount, &ts, &edge.DecayState, &edge.LastSource); err != nil {
			return nil, fmt.Errorf("scan rhizome edge: %w", err)
		}
		tm, err := parseSQLiteTime(ts)
		if err != nil {
			return nil, fmt.Errorf("parse rhizome edge ts: %w", err)
		}
		edge.LastReinforcedAt = tm
		edges = append(edges, edge)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rhizome edges: %w", err)
	}
	return edges, nil
}

func (s *SQLiteStore) ResetRhizome(scope string) error {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return fmt.Errorf("rhizome scope is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reset rhizome tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM rhizome_edges WHERE scope = ?`, []any{scope}},
		{`DELETE FROM rhizome_events WHERE scope = ?`, []any{scope}},
		{`DELETE FROM rhizome_nodes WHERE scope = ?`, []any{scope}},
	} {
		if _, err := tx.Exec(stmt.query, stmt.args...); err != nil {
			return fmt.Errorf("reset rhizome scope %s: %w", scope, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset rhizome tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ResetAllRhizome() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reset all rhizome tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range []string{
		`DELETE FROM rhizome_edges`,
		`DELETE FROM rhizome_events`,
		`DELETE FROM rhizome_nodes`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("reset all rhizome with %q: %w", stmt, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset all rhizome tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) RecordOutbound(key SessionKey, turnIndex int, telegramMsgID int64, msgType string) error {
	if telegramMsgID == 0 {
		return fmt.Errorf("record outbound: telegram_msg_id is required")
	}
	if strings.TrimSpace(msgType) == "" {
		msgType = "text"
	}
	sessionID := SessionIDForKey(key)

	_, err := s.db.Exec(`
		INSERT INTO outbound_messages(session_id, chat_id, user_id, turn_index, telegram_msg_id, msg_type)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sessionID, key.ChatID, key.UserID, turnIndex, telegramMsgID, msgType)
	if err != nil {
		return fmt.Errorf("record outbound: %w", err)
	}
	return nil
}

func (s *SQLiteStore) InsertReviewEvent(event ReviewEvent) (int64, error) {
	if strings.TrimSpace(event.SourceRole) == "" {
		return 0, fmt.Errorf("enqueue review event: source_role is required")
	}
	event.SourceScope = NormalizeScopeRef(event.SourceScope)
	event.TargetScope = NormalizeScopeRef(event.TargetScope)
	if event.SourceChatID == 0 && event.SourceScope.IsZero() {
		return 0, fmt.Errorf("enqueue review event: source provenance is required")
	}
	if event.TargetAdminChatID == 0 {
		return 0, fmt.Errorf("enqueue review event: target_chat_id is required")
	}
	if strings.TrimSpace(event.Summary) == "" {
		return 0, fmt.Errorf("enqueue review event: summary is required")
	}

	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "pending"
	}
	if strings.TrimSpace(event.SourceSessionID) == "" {
		event.SourceSessionID = SessionIDFromParts(event.SourceChatID, event.SourceUserID, event.SourceScope)
	}
	if strings.TrimSpace(event.TargetSessionID) == "" {
		event.TargetSessionID = SessionIDFromParts(event.TargetAdminChatID, 0, event.TargetScope)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`
		INSERT INTO review_events(
			source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		nullableString(event.SourceSessionID), event.SourceChatID, event.SourceUserID, event.SourceRole,
		string(event.SourceScope.Kind), event.SourceScope.ID, event.SourceScope.DurableAgentID,
		nullableString(event.TargetSessionID), event.TargetAdminChatID,
		string(event.TargetScope.Kind), event.TargetScope.ID, event.TargetScope.DurableAgentID,
		event.TurnFrom, event.TurnTo, event.Summary, nullableString(event.MetadataJSON), status, now,
	)
	if err != nil {
		return 0, fmt.Errorf("enqueue review event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("review event last insert id: %w", err)
	}
	return id, nil
}

func (s *SQLiteStore) EnqueueReviewEvent(event ReviewEvent) error {
	_, err := s.InsertReviewEvent(event)
	return err
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
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at
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
		event, err := scanReviewEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending review events: %w", err)
	}
	return events, nil
}

func (s *SQLiteStore) PendingReviewEventsAll(limit int) ([]ReviewEvent, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at
		FROM review_events
		WHERE status = 'pending'
		ORDER BY created_at ASC, id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query pending review events: %w", err)
	}
	defer rows.Close()

	events := make([]ReviewEvent, 0, limit)
	for rows.Next() {
		event, err := scanReviewEvent(rows)
		if err != nil {
			return nil, err
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

func encodeRecordReferences(refs []RecordReference) string {
	normalized := NormalizeRecordReferences(refs)
	if len(normalized) == 0 {
		return "[]"
	}
	buf, err := json.Marshal(normalized)
	if err != nil {
		return "[]"
	}
	return string(buf)
}

func decodeRecordReferences(raw string) []RecordReference {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var refs []RecordReference
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	return NormalizeRecordReferences(refs)
}

func (s *SQLiteStore) UpsertPendingDecision(record PendingDecisionRecord) error {
	record.ID = strings.TrimSpace(record.ID)
	if record.ID == "" {
		return fmt.Errorf("pending decision id is required")
	}
	if record.Sequence > 9223372036854775807 {
		return fmt.Errorf("pending decision sequence is too large")
	}

	now := time.Now().UTC()
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	record.OwnerKey = strings.TrimSpace(record.OwnerKey)
	record.Kind = strings.TrimSpace(record.Kind)
	record.Prompt = strings.TrimSpace(record.Prompt)
	record.Details = strings.TrimSpace(record.Details)
	record.Rationale = strings.TrimSpace(record.Rationale)
	record.ArtifactRefs = NormalizeRecordReferences(record.ArtifactRefs)
	record.DefaultChoice = strings.TrimSpace(record.DefaultChoice)
	choicesJSON := strings.TrimSpace(record.ChoicesJSON)
	if choicesJSON == "" {
		choicesJSON = "[]"
	}

	_, err := s.db.Exec(`
		INSERT INTO pending_decisions(
			decision_id, decision_seq, owner_key, kind, chat_id, sender_id, message_id,
			prompt, details, rationale, artifact_refs_json, choices_json, default_choice, timeout_ns, delivery_message_id,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(decision_id) DO UPDATE SET
			decision_seq = excluded.decision_seq,
			owner_key = excluded.owner_key,
			kind = excluded.kind,
			chat_id = excluded.chat_id,
			sender_id = excluded.sender_id,
			message_id = excluded.message_id,
			prompt = excluded.prompt,
			details = excluded.details,
			rationale = excluded.rationale,
			artifact_refs_json = excluded.artifact_refs_json,
			choices_json = excluded.choices_json,
			default_choice = excluded.default_choice,
			timeout_ns = excluded.timeout_ns,
			delivery_message_id = excluded.delivery_message_id,
			updated_at = excluded.updated_at
	`,
		record.ID,
		int64(record.Sequence),
		record.OwnerKey,
		record.Kind,
		record.ChatID,
		record.SenderID,
		record.MessageID,
		record.Prompt,
		record.Details,
		record.Rationale,
		encodeRecordReferences(record.ArtifactRefs),
		choicesJSON,
		record.DefaultChoice,
		record.TimeoutNanos,
		record.DeliveryMessageID,
		createdAt.UTC().Format(time.RFC3339Nano),
		updatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert pending decision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeletePendingDecision(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if _, err := s.db.Exec(`DELETE FROM pending_decisions WHERE decision_id = ?`, id); err != nil {
		return fmt.Errorf("delete pending decision: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeletePendingDecisionsByOwner(ownerKey string) (int, error) {
	ownerKey = strings.TrimSpace(ownerKey)
	if ownerKey == "" {
		return 0, nil
	}
	res, err := s.db.Exec(`DELETE FROM pending_decisions WHERE owner_key = ?`, ownerKey)
	if err != nil {
		return 0, fmt.Errorf("delete pending decisions by owner: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected deleting pending decisions by owner: %w", err)
	}
	return int(affected), nil
}

func (s *SQLiteStore) DeleteAllPendingDecisions() (int, error) {
	res, err := s.db.Exec(`DELETE FROM pending_decisions`)
	if err != nil {
		return 0, fmt.Errorf("delete all pending decisions: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected deleting all pending decisions: %w", err)
	}
	return int(affected), nil
}

func (s *SQLiteStore) PendingDecisions() ([]PendingDecisionRecord, error) {
	rows, err := s.db.Query(`
		SELECT
			decision_id, decision_seq, owner_key, kind, chat_id, sender_id, message_id,
			prompt, details, rationale, artifact_refs_json, choices_json, default_choice, timeout_ns, delivery_message_id,
			created_at, updated_at
		FROM pending_decisions
		ORDER BY decision_seq ASC, decision_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query pending decisions: %w", err)
	}
	defer rows.Close()

	records := make([]PendingDecisionRecord, 0)
	for rows.Next() {
		var (
			record          PendingDecisionRecord
			sequenceRaw     int64
			artifactRefsRaw string
			createdAtRaw    string
			updatedAtRaw    string
		)
		if err := rows.Scan(
			&record.ID, &sequenceRaw, &record.OwnerKey, &record.Kind, &record.ChatID, &record.SenderID, &record.MessageID,
			&record.Prompt, &record.Details, &record.Rationale, &artifactRefsRaw, &record.ChoicesJSON, &record.DefaultChoice, &record.TimeoutNanos, &record.DeliveryMessageID,
			&createdAtRaw, &updatedAtRaw,
		); err != nil {
			return nil, fmt.Errorf("scan pending decision: %w", err)
		}
		if sequenceRaw > 0 {
			record.Sequence = uint64(sequenceRaw)
		}
		record.Prompt = strings.TrimSpace(record.Prompt)
		record.Details = strings.TrimSpace(record.Details)
		record.OwnerKey = strings.TrimSpace(record.OwnerKey)
		record.Kind = strings.TrimSpace(record.Kind)
		record.Rationale = strings.TrimSpace(record.Rationale)
		record.ArtifactRefs = decodeRecordReferences(artifactRefsRaw)
		record.DefaultChoice = strings.TrimSpace(record.DefaultChoice)
		record.ChoicesJSON = strings.TrimSpace(record.ChoicesJSON)
		if record.ChoicesJSON == "" {
			record.ChoicesJSON = "[]"
		}
		createdAt, err := parseSQLiteTime(createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse pending decision created_at: %w", err)
		}
		updatedAt, err := parseSQLiteTime(updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse pending decision updated_at: %w", err)
		}
		record.CreatedAt = createdAt
		record.UpdatedAt = updatedAt
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending decisions: %w", err)
	}
	return records, nil
}

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
	if cutoff.IsZero() {
		return nil, fmt.Errorf("stale turn run cutoff is required")
	}
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT
			id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id, kind, status, request_text, started_at, completed_at,
			last_activity_at, last_tool_name, last_tool_preview, tool_calls_started, tool_calls_finished, last_tool_result_preview, last_tool_error,
			progress_message_id, error_text, recovery_summary, recovery_logged_at
		FROM turn_runs
		WHERE status = ? AND last_activity_at <= ?
		ORDER BY last_activity_at ASC, id ASC
		LIMIT ?
	`, string(TurnRunStatusRunning), cutoff.UTC().Format(time.RFC3339Nano), limit)
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

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) UpsertDurableAgent(agent core.DurableAgent) error {
	_, err := upsertDurableAgentExec(s.db, agent)
	return err
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type sqlQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func upsertDurableAgentExec(exec sqlExecer, agent core.DurableAgent) (core.DurableAgent, error) {
	agent.AgentID = strings.TrimSpace(agent.AgentID)
	if agent.AgentID == "" {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent: agent_id is required")
	}
	agent.ChannelKind = strings.TrimSpace(agent.ChannelKind)
	if agent.ChannelKind == "" {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent: channel_kind is required")
	}
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	if strings.TrimSpace(agent.Status) == "" {
		agent.Status = "active"
	}
	if agent.BootstrapCeiling.IsZero() {
		agent.BootstrapCeiling = core.DefaultDurableAgentBootstrapCeiling(agent.ChannelKind, agent.LivePolicy)
	}
	if err := validateDurableAgentChannelConfig(agent.ChannelKind, agent.ChannelConfig); err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent channel_config: %w", err)
	}
	requiresBootstrap := strings.TrimSpace(agent.Status) != "draft" && strings.TrimSpace(agent.ChannelKind) != "email"
	if requiresBootstrap {
		if err := core.ValidateNodeLLMBootstrap(agent.BootstrapLLM); err != nil {
			return core.DurableAgent{}, fmt.Errorf("upsert durable agent bootstrap_llm: %w", err)
		}
	}
	agent.BootstrapCeiling = core.NormalizeDurableAgentBootstrapCeiling(agent.BootstrapCeiling)
	agent.BootstrapLLM = core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	if err := core.ValidateDurableAgentLivePolicyWithinCeiling(agent.LivePolicy, agent.BootstrapCeiling); err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent live_policy: %w", err)
	}
	agent.AllowedTelegramUserIDs = core.NormalizeDurableAgentAllowedTelegramUserIDs(agent.AllowedTelegramUserIDs)

	livePolicyJSON, policyHash, err := marshalDurableAgentLivePolicy(agent.LivePolicy)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent live_policy: %w", err)
	}
	channelConfigJSON, err := marshalDurableAgentChannelConfig(agent.ChannelConfig)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent channel_config: %w", err)
	}
	bootstrapCeilingJSON, err := marshalDurableAgentBootstrapCeiling(agent.BootstrapCeiling)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent bootstrap_ceiling: %w", err)
	}
	bootstrapProviderJSON, err := marshalDurableAgentBootstrapLLM(agent.BootstrapLLM)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent bootstrap_llm: %w", err)
	}
	storageRootsJSON, err := marshalStringSlice(agent.LocalStorageRoots)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent local_storage_roots: %w", err)
	}
	secretScopesJSON, err := marshalStringSlice(agent.SecretScopes)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent secret_scopes: %w", err)
	}
	allowedTelegramUserIDsJSON, err := marshalInt64Slice(agent.AllowedTelegramUserIDs)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent allowed_telegram_user_ids: %w", err)
	}

	now := time.Now().UTC()
	createdAt := nonZeroTimeOrNow(agent.CreatedAt, now).UTC().Format(time.RFC3339Nano)
	updatedAt := now.UTC().Format(time.RFC3339Nano)
	policyVersion := agent.PolicyVersion
	if policyVersion <= 0 {
		policyVersion = 1
	}
	policyIssuedAt := nonZeroTimeOrNow(agent.PolicyIssuedAt, now)
	_, err = exec.Exec(`
		INSERT INTO durable_agents(
			agent_id, parent_agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id,
			channel_kind, live_policy_json, channel_config_json, bootstrap_ceiling_json, bootstrap_provider_json, control_plane_secret, policy_version, policy_hash, policy_issued_at,
			local_storage_roots_json, network_policy, wakeup_mode, secret_scopes_json, allowed_telegram_user_ids_json, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			parent_agent_id = excluded.parent_agent_id,
			parent_scope_kind = excluded.parent_scope_kind,
			parent_scope_id = excluded.parent_scope_id,
			review_target_chat_id = excluded.review_target_chat_id,
			channel_kind = excluded.channel_kind,
			live_policy_json = excluded.live_policy_json,
			channel_config_json = excluded.channel_config_json,
			bootstrap_ceiling_json = excluded.bootstrap_ceiling_json,
			bootstrap_provider_json = excluded.bootstrap_provider_json,
			control_plane_secret = excluded.control_plane_secret,
			policy_version = excluded.policy_version,
			policy_hash = excluded.policy_hash,
			policy_issued_at = excluded.policy_issued_at,
			local_storage_roots_json = excluded.local_storage_roots_json,
			network_policy = excluded.network_policy,
			wakeup_mode = excluded.wakeup_mode,
			secret_scopes_json = excluded.secret_scopes_json,
			allowed_telegram_user_ids_json = excluded.allowed_telegram_user_ids_json,
			status = excluded.status,
			updated_at = excluded.updated_at
	`,
		agent.AgentID, nullableString(agent.ParentAgentID), nullableString(agent.ParentScopeKind), nullableString(agent.ParentScopeID), agent.ReviewTargetChatID,
		agent.ChannelKind, livePolicyJSON, channelConfigJSON, bootstrapCeilingJSON, bootstrapProviderJSON, strings.TrimSpace(agent.ControlPlaneSecret), policyVersion, policyHash, nullableTime(policyIssuedAt), string(storageRootsJSON),
		nullableString(agent.NetworkPolicy), nullableString(agent.WakeupMode), string(secretScopesJSON), string(allowedTelegramUserIDsJSON), nullableString(agent.Status), createdAt, updatedAt,
	)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("upsert durable agent: %w", err)
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(agent.LivePolicy)
	agent.ChannelConfig = core.NormalizeDurableAgentChannelConfig(agent.ChannelConfig)
	agent.BootstrapCeiling = core.NormalizeDurableAgentBootstrapCeiling(agent.BootstrapCeiling)
	agent.PolicyVersion = policyVersion
	agent.PolicyHash = policyHash
	agent.PolicyIssuedAt = policyIssuedAt
	agent.CreatedAt = mustParseSQLiteTime(createdAt)
	agent.UpdatedAt = mustParseSQLiteTime(updatedAt)
	return agent, nil
}

func (s *SQLiteStore) DurableAgent(agentID string) (*core.DurableAgent, error) {
	return queryDurableAgent(s.db, strings.TrimSpace(agentID))
}

func (s *SQLiteStore) SetDurableAgentLivePolicy(agentID string, policy core.DurableAgentLivePolicy) error {
	agent, err := s.DurableAgent(agentID)
	if err != nil {
		return err
	}
	agent.LivePolicy = core.NormalizeDurableAgentLivePolicy(policy)
	agent.PolicyVersion++
	if agent.PolicyVersion <= 0 {
		agent.PolicyVersion = 1
	}
	agent.PolicyIssuedAt = time.Now().UTC()
	updated, err := upsertDurableAgentExec(s.db, *agent)
	if err != nil {
		return err
	}
	state, err := s.DurableAgentState(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agent.AgentID}
	}
	state.LastOfferedPolicyVersion = updated.PolicyVersion
	state.LastOfferedPolicyHash = updated.PolicyHash
	state.LastOfferedPolicyAt = updated.PolicyIssuedAt
	state.LastApplyStatus = "pending"
	state.LastApplyError = ""
	return s.SaveDurableAgentState(*state)
}

func (s *SQLiteStore) ApplyDurableAgentLivePolicy(agentID string, policy core.DurableAgentLivePolicy, sourceReviewEventID int64, reason string) (*core.DurableAgent, *DurableAgentPolicyUpdate, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("begin apply durable agent live policy tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	agent, err := queryDurableAgent(tx, agentID)
	if err != nil {
		return nil, nil, err
	}
	nextPolicy := core.NormalizeDurableAgentLivePolicy(policy)
	nextPolicyHash, err := core.DurableAgentPolicyHash(nextPolicy)
	if err != nil {
		return nil, nil, fmt.Errorf("hash durable agent live policy: %w", err)
	}
	if agent.PolicyHash == "" {
		agent.PolicyHash, err = core.DurableAgentPolicyHash(agent.LivePolicy)
		if err != nil {
			return nil, nil, fmt.Errorf("hash current durable agent live policy: %w", err)
		}
	}
	if agent.PolicyHash == nextPolicyHash {
		if err := tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit no-op durable agent live policy apply: %w", err)
		}
		return agent, nil, nil
	}
	previousVersion := agent.PolicyVersion
	agent.LivePolicy = nextPolicy
	agent.PolicyVersion++
	if agent.PolicyVersion <= 0 {
		agent.PolicyVersion = 1
	}
	agent.PolicyIssuedAt = time.Now().UTC()
	updated, err := upsertDurableAgentExec(tx, *agent)
	if err != nil {
		return nil, nil, err
	}

	policyJSON, policyHash, err := marshalDurableAgentLivePolicy(updated.LivePolicy)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal applied durable agent live policy: %w", err)
	}
	now := time.Now().UTC()
	res, err := tx.Exec(`
		INSERT INTO durable_agent_policy_updates(
			agent_id, source_review_event_id, previous_version, new_version, policy_hash, policy_json, reason, applied_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		updated.AgentID, maxInt64(sourceReviewEventID, 0), previousVersion, updated.PolicyVersion, policyHash, policyJSON, nullableString(reason), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("insert durable agent policy update: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, nil, fmt.Errorf("durable agent policy update last insert id: %w", err)
	}
	state, err := queryDurableAgentState(tx, updated.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("load durable agent state for policy apply: %w", err)
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: updated.AgentID}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("parse durable agent continuity for policy apply: %w", err)
	}
	summary := strings.TrimSpace(reason)
	if summary == "" {
		summary = "Ratified durable-agent live policy update."
	}
	continuity = continuity.WithRatifiedOutcome(summary, updated.PolicyVersion, policyHash, maxInt64(sourceReviewEventID, 0), now)
	stateJSON, err := continuity.Marshal()
	if err != nil {
		return nil, nil, fmt.Errorf("marshal durable agent continuity for policy apply: %w", err)
	}
	state.StateJSON = stateJSON
	state.LastOfferedPolicyVersion = updated.PolicyVersion
	state.LastOfferedPolicyHash = policyHash
	state.LastOfferedPolicyAt = now
	state.LastApplyStatus = "pending"
	state.LastApplyError = ""
	if err := saveDurableAgentRuntimeStateExec(tx, core.DurableAgentRuntimeStateFrom(*state)); err != nil {
		return nil, nil, fmt.Errorf("save durable agent runtime state for policy apply: %w", err)
	}
	if err := saveDurableAgentIdentityStateExec(tx, core.DurableAgentIdentityStateFrom(*state)); err != nil {
		return nil, nil, fmt.Errorf("save durable agent identity state for policy apply: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit durable agent live policy apply: %w", err)
	}
	return &updated, &DurableAgentPolicyUpdate{
		ID:                  id,
		AgentID:             updated.AgentID,
		SourceReviewEventID: maxInt64(sourceReviewEventID, 0),
		PreviousVersion:     previousVersion,
		NewVersion:          updated.PolicyVersion,
		PolicyHash:          policyHash,
		PolicyJSON:          policyJSON,
		Reason:              strings.TrimSpace(reason),
		AppliedAt:           now,
	}, nil
}

func (s *SQLiteStore) DurableAgentPolicyUpdates(agentID string, limit int) ([]DurableAgentPolicyUpdate, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("durable agent policy updates: agent_id is required")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, agent_id, source_review_event_id, previous_version, new_version, policy_hash, policy_json, reason, applied_at
		FROM durable_agent_policy_updates
		WHERE agent_id = ?
		ORDER BY applied_at DESC, id DESC
		LIMIT ?
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("query durable agent policy updates: %w", err)
	}
	defer rows.Close()
	var updates []DurableAgentPolicyUpdate
	for rows.Next() {
		update, err := scanDurableAgentPolicyUpdate(rows)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable agent policy updates: %w", err)
	}
	return updates, nil
}

func (s *SQLiteStore) ApplyDurableAgentBootstrap(agentID string, next core.NodeLLMBootstrap, sourceReviewEventID int64, actorUserID int64, actorRole string, updateKind string, reason string) (*core.DurableAgent, *DurableAgentBootstrapUpdate, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("begin apply durable agent bootstrap tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	agent, err := queryDurableAgent(tx, agentID)
	if err != nil {
		return nil, nil, err
	}
	previous := core.NormalizeNodeLLMBootstrap(agent.BootstrapLLM)
	next = core.NormalizeNodeLLMBootstrap(next)
	if err := core.ValidateNodeLLMBootstrap(next); err != nil {
		return nil, nil, fmt.Errorf("validate durable agent bootstrap_llm: %w", err)
	}
	if previous == next {
		if err := tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit no-op durable agent bootstrap apply: %w", err)
		}
		return agent, nil, nil
	}

	agent.BootstrapLLM = next
	updated, err := upsertDurableAgentExec(tx, *agent)
	if err != nil {
		return nil, nil, err
	}
	previousAudit := redactDurableAgentBootstrapSecrets(previous)
	newAudit := redactDurableAgentBootstrapSecrets(updated.BootstrapLLM)
	prevJSON, err := marshalDurableAgentBootstrapLLM(previousAudit)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal previous durable agent bootstrap: %w", err)
	}
	nextJSON, err := marshalDurableAgentBootstrapLLM(newAudit)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal updated durable agent bootstrap: %w", err)
	}
	now := time.Now().UTC()
	res, err := tx.Exec(`
		INSERT INTO durable_agent_bootstrap_updates(
			agent_id, source_review_event_id, actor_user_id, actor_role, update_kind, previous_bootstrap_json, new_bootstrap_json, reason, applied_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		updated.AgentID, maxInt64(sourceReviewEventID, 0), maxInt64(actorUserID, 0), strings.TrimSpace(actorRole), strings.TrimSpace(updateKind), prevJSON, nextJSON, nullableString(reason), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("insert durable agent bootstrap update: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, nil, fmt.Errorf("durable agent bootstrap update last insert id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit durable agent bootstrap apply: %w", err)
	}
	return &updated, &DurableAgentBootstrapUpdate{
		ID:                  id,
		AgentID:             updated.AgentID,
		SourceReviewEventID: maxInt64(sourceReviewEventID, 0),
		ActorUserID:         maxInt64(actorUserID, 0),
		ActorRole:           strings.TrimSpace(actorRole),
		UpdateKind:          strings.TrimSpace(updateKind),
		PreviousBootstrap:   previousAudit,
		NewBootstrap:        newAudit,
		Reason:              strings.TrimSpace(reason),
		AppliedAt:           now,
	}, nil
}

func (s *SQLiteStore) DurableAgentBootstrapUpdates(agentID string, limit int) ([]DurableAgentBootstrapUpdate, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("durable agent bootstrap updates: agent_id is required")
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, agent_id, source_review_event_id, actor_user_id, actor_role, update_kind, previous_bootstrap_json, new_bootstrap_json, reason, applied_at
		FROM durable_agent_bootstrap_updates
		WHERE agent_id = ?
		ORDER BY applied_at DESC, id DESC
		LIMIT ?
	`, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("query durable agent bootstrap updates: %w", err)
	}
	defer rows.Close()
	var updates []DurableAgentBootstrapUpdate
	for rows.Next() {
		update, err := scanDurableAgentBootstrapUpdate(rows)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable agent bootstrap updates: %w", err)
	}
	return updates, nil
}

func (s *SQLiteStore) UpsertDurableAgentRemoteEnrollment(enrollment core.DurableAgentRemoteEnrollment) error {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	if enrollment.AgentID == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: agent_id is required")
	}
	if enrollment.ParentControlURL == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: parent_control_url is required")
	}
	if enrollment.KeyFingerprint == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: key_fingerprint is required")
	}
	now := time.Now().UTC()
	if enrollment.EnrolledAt.IsZero() {
		enrollment.EnrolledAt = now
	}
	_, err := s.db.Exec(`
		INSERT INTO durable_agent_remote_enrollments(
			agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			parent_control_url = excluded.parent_control_url,
			key_fingerprint = excluded.key_fingerprint,
			protocol_version = excluded.protocol_version,
			status = excluded.status,
			last_sequence = excluded.last_sequence,
			enrolled_at = excluded.enrolled_at,
			last_seen_at = excluded.last_seen_at,
			revoked_at = excluded.revoked_at,
			updated_at = excluded.updated_at
	`,
		enrollment.AgentID, enrollment.ParentControlURL, enrollment.KeyFingerprint, enrollment.ProtocolVersion, enrollment.Status,
		maxInt64(enrollment.LastSequence, 0), nullableTime(enrollment.EnrolledAt), nullableTime(enrollment.LastSeenAt), nullableTime(enrollment.RevokedAt), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert durable agent remote enrollment: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentRemoteEnrollment(agentID string) (*core.DurableAgentRemoteEnrollment, error) {
	rows, err := s.db.Query(`
		SELECT agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at
		FROM durable_agent_remote_enrollments
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent remote enrollment: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	enrollment, err := scanDurableAgentRemoteEnrollment(rows)
	if err != nil {
		return nil, err
	}
	return &enrollment, nil
}

func (s *SQLiteStore) AcceptDurableAgentControlEnvelope(envelope core.DurableAgentControlEnvelope, receivedAt time.Time) error {
	envelope = core.NormalizeDurableAgentControlEnvelope(envelope)
	if err := core.ValidateDurableAgentControlEnvelope(envelope); err != nil {
		return err
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin durable agent control envelope tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	enrollment, err := queryDurableAgentRemoteEnrollment(tx, envelope.AgentID)
	if err != nil {
		return err
	}
	if enrollment.Status != "active" {
		return fmt.Errorf("durable agent remote enrollment %s is not active", enrollment.AgentID)
	}
	_, err = tx.Exec(`
		INSERT INTO durable_agent_control_receipts(agent_id, message_id, message_kind, sequence, received_at)
		VALUES (?, ?, ?, ?, ?)
	`,
		envelope.AgentID, envelope.MessageID, envelope.MessageKind, envelope.Sequence, receivedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("replay durable agent control envelope for %s", envelope.AgentID)
		}
		return fmt.Errorf("insert durable agent control receipt: %w", err)
	}
	if envelope.Sequence <= enrollment.LastSequence {
		return fmt.Errorf("out-of-order durable agent control envelope for %s", enrollment.AgentID)
	}
	enrollment.LastSequence = envelope.Sequence
	enrollment.LastSeenAt = receivedAt.UTC()
	if err := upsertDurableAgentRemoteEnrollmentExec(tx, *enrollment); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit durable agent control envelope tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ReviewEventByID(id int64) (*ReviewEvent, error) {
	if id <= 0 {
		return nil, fmt.Errorf("review event id is required")
	}
	rows, err := s.db.Query(`
		SELECT
			id, source_session_id, source_chat_id, source_user_id, source_role, source_scope_kind, source_scope_id, source_durable_agent_id,
			target_session_id, target_chat_id, target_scope_kind, target_scope_id, target_durable_agent_id,
			turn_from, turn_to, summary, metadata_json, status, created_at, delivered_at
		FROM review_events
		WHERE id = ?
	`, id)
	if err != nil {
		return nil, fmt.Errorf("query review event: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	event, err := scanReviewEvent(rows)
	if err != nil {
		return nil, err
	}
	return &event, nil
}

func (s *SQLiteStore) ListDurableAgents() ([]core.DurableAgent, error) {
	rows, err := s.db.Query(`
		SELECT
			agent_id, parent_agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id,
			channel_kind, live_policy_json, COALESCE(channel_config_json, ''), COALESCE(bootstrap_ceiling_json, ''), COALESCE(bootstrap_provider_json, ''), COALESCE(control_plane_secret, ''), policy_version, policy_hash, policy_issued_at, local_storage_roots_json, network_policy,
			wakeup_mode, secret_scopes_json, COALESCE(allowed_telegram_user_ids_json, '[]'), status, created_at, updated_at
		FROM durable_agents
		ORDER BY created_at ASC, agent_id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list durable agents: %w", err)
	}
	defer rows.Close()

	var agents []core.DurableAgent
	for rows.Next() {
		agent, err := scanDurableAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable agents: %w", err)
	}
	return agents, nil
}

func (s *SQLiteStore) SaveDurableAgentState(state core.DurableAgentState) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin save durable agent state tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := saveDurableAgentRuntimeStateExec(tx, core.DurableAgentRuntimeStateFrom(state)); err != nil {
		return err
	}
	if err := saveDurableAgentIdentityStateExec(tx, core.DurableAgentIdentityStateFrom(state)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save durable agent state tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveDurableAgentRuntimeState(state core.DurableAgentRuntimeState) error {
	return saveDurableAgentRuntimeStateExec(s.db, state)
}

func saveDurableAgentRuntimeStateExec(exec sqlExecer, state core.DurableAgentRuntimeState) error {
	state.AgentID = strings.TrimSpace(state.AgentID)
	if state.AgentID == "" {
		return fmt.Errorf("save durable agent runtime state: agent_id is required")
	}
	now := time.Now().UTC()
	_, err := exec.Exec(`
		INSERT INTO durable_agent_state(
			agent_id, cursor, status, state_json,
			last_apply_status, last_apply_error,
			last_wake_at, last_review_at, dormant_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			cursor = excluded.cursor,
			status = excluded.status,
			state_json = excluded.state_json,
			last_apply_status = excluded.last_apply_status,
			last_apply_error = excluded.last_apply_error,
			last_wake_at = excluded.last_wake_at,
			last_review_at = excluded.last_review_at,
			dormant_at = excluded.dormant_at,
			updated_at = excluded.updated_at
	`,
		state.AgentID, nullableString(state.Cursor), nullableString(state.Status), nullableString(state.StateJSON),
		strings.TrimSpace(state.LastApplyStatus), strings.TrimSpace(state.LastApplyError),
		nullableTime(state.LastWakeAt), nullableTime(state.LastReviewAt), nullableTime(state.DormantAt), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save durable agent runtime state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SaveDurableAgentIdentityState(state core.DurableAgentIdentityState) error {
	return saveDurableAgentIdentityStateExec(s.db, state)
}

func saveDurableAgentIdentityStateExec(exec sqlExecer, state core.DurableAgentIdentityState) error {
	state.AgentID = strings.TrimSpace(state.AgentID)
	if state.AgentID == "" {
		return fmt.Errorf("save durable agent identity state: agent_id is required")
	}
	now := time.Now().UTC()
	_, err := exec.Exec(`
		INSERT INTO durable_agent_identity_state(
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			last_offered_policy_version = excluded.last_offered_policy_version,
			last_offered_policy_hash = excluded.last_offered_policy_hash,
			last_offered_policy_at = excluded.last_offered_policy_at,
			last_acknowledged_policy_version = excluded.last_acknowledged_policy_version,
			last_acknowledged_policy_hash = excluded.last_acknowledged_policy_hash,
			last_acknowledged_policy_at = excluded.last_acknowledged_policy_at,
			last_applied_policy_version = excluded.last_applied_policy_version,
			last_applied_policy_hash = excluded.last_applied_policy_hash,
			last_applied_policy_at = excluded.last_applied_policy_at,
			updated_at = excluded.updated_at
	`,
		state.AgentID,
		state.LastOfferedPolicyVersion, strings.TrimSpace(state.LastOfferedPolicyHash), nullableTime(state.LastOfferedPolicyAt),
		state.LastAcknowledgedPolicyVersion, strings.TrimSpace(state.LastAcknowledgedPolicyHash), nullableTime(state.LastAcknowledgedPolicyAt),
		state.LastAppliedPolicyVersion, strings.TrimSpace(state.LastAppliedPolicyHash), nullableTime(state.LastAppliedPolicyAt),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("save durable agent identity state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DurableAgentRuntimeState(agentID string) (*core.DurableAgentRuntimeState, error) {
	return queryDurableAgentRuntimeState(s.db, agentID)
}

func queryDurableAgentRuntimeState(queryer sqlQueryer, agentID string) (*core.DurableAgentRuntimeState, error) {
	rows, err := queryer.Query(`
		SELECT
			agent_id, cursor, status, state_json,
			last_apply_status, last_apply_error,
			last_wake_at, last_review_at, dormant_at, updated_at
		FROM durable_agent_state
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent runtime state: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	state, err := scanDurableAgentRuntimeState(rows)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *SQLiteStore) DurableAgentIdentityState(agentID string) (*core.DurableAgentIdentityState, error) {
	return queryDurableAgentIdentityState(s.db, agentID)
}

func queryDurableAgentIdentityState(queryer sqlQueryer, agentID string) (*core.DurableAgentIdentityState, error) {
	rows, err := queryer.Query(`
		SELECT
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		FROM durable_agent_identity_state
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent identity state: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	state, err := scanDurableAgentIdentityState(rows)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *SQLiteStore) DurableAgentState(agentID string) (*core.DurableAgentState, error) {
	return queryDurableAgentState(s.db, agentID)
}

func queryDurableAgentState(queryer sqlQueryer, agentID string) (*core.DurableAgentState, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, sql.ErrNoRows
	}

	runtimeState, runtimeErr := queryDurableAgentRuntimeState(queryer, agentID)
	if runtimeErr != nil && !errors.Is(runtimeErr, sql.ErrNoRows) {
		return nil, runtimeErr
	}
	identityState, identityErr := queryDurableAgentIdentityState(queryer, agentID)
	if identityErr != nil && !errors.Is(identityErr, sql.ErrNoRows) {
		return nil, identityErr
	}
	if errors.Is(runtimeErr, sql.ErrNoRows) && errors.Is(identityErr, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}

	state := core.DurableAgentState{AgentID: agentID}
	if runtimeState != nil {
		state.Cursor = runtimeState.Cursor
		state.Status = runtimeState.Status
		state.StateJSON = runtimeState.StateJSON
		state.LastApplyStatus = runtimeState.LastApplyStatus
		state.LastApplyError = runtimeState.LastApplyError
		state.LastWakeAt = runtimeState.LastWakeAt
		state.LastReviewAt = runtimeState.LastReviewAt
		state.DormantAt = runtimeState.DormantAt
		state.UpdatedAt = runtimeState.UpdatedAt
	}
	if identityState != nil {
		state.LastOfferedPolicyVersion = identityState.LastOfferedPolicyVersion
		state.LastOfferedPolicyHash = identityState.LastOfferedPolicyHash
		state.LastOfferedPolicyAt = identityState.LastOfferedPolicyAt
		state.LastAcknowledgedPolicyVersion = identityState.LastAcknowledgedPolicyVersion
		state.LastAcknowledgedPolicyHash = identityState.LastAcknowledgedPolicyHash
		state.LastAcknowledgedPolicyAt = identityState.LastAcknowledgedPolicyAt
		state.LastAppliedPolicyVersion = identityState.LastAppliedPolicyVersion
		state.LastAppliedPolicyHash = identityState.LastAppliedPolicyHash
		state.LastAppliedPolicyAt = identityState.LastAppliedPolicyAt
		if state.UpdatedAt.IsZero() || (!identityState.UpdatedAt.IsZero() && identityState.UpdatedAt.After(state.UpdatedAt)) {
			state.UpdatedAt = identityState.UpdatedAt
		}
	}
	return &state, nil
}

func (s *SQLiteStore) DeleteDurableAgent(agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return fmt.Errorf("delete durable agent: agent_id is required")
	}
	if _, err := s.db.Exec(`DELETE FROM durable_agents WHERE agent_id = ?`, agentID); err != nil {
		return fmt.Errorf("delete durable agent: %w", err)
	}
	return nil
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

func applyMigrations(tx *sql.Tx) error {
	currentVersion, err := currentSchemaVersion(tx)
	if err != nil {
		return err
	}
	if err := rejectUnsupportedLegacySchema(tx, currentVersion); err != nil {
		return err
	}

	if err := ensureSessionColumn(tx, "last_floor_text", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.last_floor_text: %w", err)
	}
	if err := ensureSessionColumn(tx, "last_floor_metadata", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.last_floor_metadata: %w", err)
	}
	if err := ensureSessionColumn(tx, "operation_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.operation_state_json: %w", err)
	}
	if err := ensureSessionColumn(tx, "continuation_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.continuation_state_json: %w", err)
	}
	if err := ensureSessionColumn(tx, "session_id", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.session_id: %w", err)
	}
	if err := ensureSessionColumn(tx, "scope_kind", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.scope_kind: %w", err)
	}
	if err := ensureSessionColumn(tx, "scope_id", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.scope_id: %w", err)
	}
	if err := ensureSessionColumn(tx, "durable_agent_id", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.durable_agent_id: %w", err)
	}
	if err := ensureSessionColumn(tx, "plan_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.plan_state_json: %w", err)
	}
	if err := ensureTableColumn(tx, "messages", "floor_content", "TEXT"); err != nil {
		return fmt.Errorf("ensure messages.floor_content: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS artifact_index (
		session_id TEXT NOT NULL,
		turn_index INTEGER NOT NULL DEFAULT 0,
		artifact_id TEXT NOT NULL,
		chat_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		source_type TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		handling TEXT NOT NULL DEFAULT '',
		retention TEXT NOT NULL DEFAULT '',
		fetch_state TEXT NOT NULL DEFAULT '',
		materialized_path TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (session_id, turn_index, artifact_id)
	)`); err != nil {
		return fmt.Errorf("ensure artifact_index table: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_session_turn ON artifact_index(session_id, turn_index DESC, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_summary ON artifact_index(summary, kind, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure artifact index support: %w", err)
		}
	}
	if err := ensureTableColumn(tx, "messages", "floor_metadata", "TEXT"); err != nil {
		return fmt.Errorf("ensure messages.floor_metadata: %w", err)
	}
	for _, column := range []struct {
		name string
		typ  string
	}{
		{"actor_user_id", "INTEGER NOT NULL DEFAULT 0"},
		{"actor_role", "TEXT NOT NULL DEFAULT ''"},
		{"event_origin", "TEXT NOT NULL DEFAULT ''"},
		{"event_origin_detail", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureTableColumn(tx, "messages", column.name, column.typ); err != nil {
			return fmt.Errorf("ensure messages.%s: %w", column.name, err)
		}
	}
	for _, column := range []struct {
		table string
		name  string
		typ   string
	}{
		{"messages", "session_id", "TEXT"},
		{"review_events", "source_scope_kind", "TEXT"},
		{"review_events", "source_scope_id", "TEXT"},
		{"review_events", "source_durable_agent_id", "TEXT"},
		{"review_events", "source_session_id", "TEXT"},
		{"review_events", "target_scope_kind", "TEXT"},
		{"review_events", "target_scope_id", "TEXT"},
		{"review_events", "target_durable_agent_id", "TEXT"},
		{"review_events", "target_session_id", "TEXT"},
		{"review_events", "metadata_json", "TEXT"},
		{"outbound_messages", "session_id", "TEXT"},
		{"turn_runs", "scope_kind", "TEXT"},
		{"turn_runs", "scope_id", "TEXT"},
		{"turn_runs", "durable_agent_id", "TEXT"},
		{"turn_runs", "session_id", "TEXT"},
		{"turn_runs", "tool_calls_finished", "INTEGER NOT NULL DEFAULT 0"},
		{"turn_runs", "last_tool_result_preview", "TEXT"},
		{"turn_runs", "last_tool_error", "TEXT"},
		{"compaction_log", "session_id", "TEXT"},
	} {
		if err := ensureTableColumn(tx, column.table, column.name, column.typ); err != nil {
			return fmt.Errorf("ensure %s.%s: %w", column.table, column.name, err)
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS execution_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		chat_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		scope_kind TEXT NOT NULL DEFAULT '',
		scope_id TEXT NOT NULL DEFAULT '',
		durable_agent_id TEXT NOT NULL DEFAULT '',
		seq INTEGER NOT NULL,
		event_type TEXT NOT NULL,
		stage TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		caused_by_seq INTEGER NOT NULL DEFAULT 0,
		payload_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure execution_events table: %w", err)
	}
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure execution_events index: %w", err)
		}
	}

	if err := ensureSessionColumn(tx, "plan_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.plan_state_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "bootstrap_ceiling_json", "TEXT"); err != nil {
		return fmt.Errorf("ensure durable_agents.bootstrap_ceiling_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "channel_config_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure durable_agents.channel_config_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "bootstrap_provider_json", "TEXT"); err != nil {
		return fmt.Errorf("ensure durable_agents.bootstrap_provider_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "control_plane_secret", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ensure durable_agents.control_plane_secret: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "allowed_telegram_user_ids_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return fmt.Errorf("ensure durable_agents.allowed_telegram_user_ids_json: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pending_artifact_retention (
		owner_key TEXT PRIMARY KEY,
		chat_id INTEGER NOT NULL DEFAULT 0,
		sender_id INTEGER NOT NULL DEFAULT 0,
		inbound_message_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure pending_artifact_retention table: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pending_busy_decisions (
		owner_key TEXT PRIMARY KEY,
		chat_id INTEGER NOT NULL DEFAULT 0,
		sender_id INTEGER NOT NULL DEFAULT 0,
		inbound_message_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure pending_busy_decisions table: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS durable_agent_identity_state (
		agent_id TEXT PRIMARY KEY,
		last_offered_policy_version INTEGER NOT NULL DEFAULT 0,
		last_offered_policy_hash TEXT NOT NULL DEFAULT '',
		last_offered_policy_at TEXT,
		last_acknowledged_policy_version INTEGER NOT NULL DEFAULT 0,
		last_acknowledged_policy_hash TEXT NOT NULL DEFAULT '',
		last_acknowledged_policy_at TEXT,
		last_applied_policy_version INTEGER NOT NULL DEFAULT 0,
		last_applied_policy_hash TEXT NOT NULL DEFAULT '',
		last_applied_policy_at TEXT,
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("ensure durable_agent_identity_state table: %w", err)
	}
	for _, column := range []struct {
		name string
		typ  string
	}{
		{"cursor", "TEXT"},
		{"status", "TEXT"},
		{"state_json", "TEXT"},
		{"last_apply_status", "TEXT NOT NULL DEFAULT ''"},
		{"last_apply_error", "TEXT NOT NULL DEFAULT ''"},
		{"last_wake_at", "TEXT"},
		{"last_review_at", "TEXT"},
		{"dormant_at", "TEXT"},
	} {
		if err := ensureTableColumn(tx, "durable_agent_state", column.name, column.typ); err != nil {
			return fmt.Errorf("ensure durable_agent_state.%s: %w", column.name, err)
		}
	}
	for _, column := range []struct {
		name string
		typ  string
	}{
		{"last_offered_policy_version", "INTEGER NOT NULL DEFAULT 0"},
		{"last_offered_policy_hash", "TEXT NOT NULL DEFAULT ''"},
		{"last_offered_policy_at", "TEXT"},
		{"last_acknowledged_policy_version", "INTEGER NOT NULL DEFAULT 0"},
		{"last_acknowledged_policy_hash", "TEXT NOT NULL DEFAULT ''"},
		{"last_acknowledged_policy_at", "TEXT"},
		{"last_applied_policy_version", "INTEGER NOT NULL DEFAULT 0"},
		{"last_applied_policy_hash", "TEXT NOT NULL DEFAULT ''"},
		{"last_applied_policy_at", "TEXT"},
	} {
		if currentVersion > 0 && currentVersion < 26 {
			if err := ensureTableColumn(tx, "durable_agent_state", column.name, column.typ); err != nil {
				return fmt.Errorf("ensure durable_agent_state.%s: %w", column.name, err)
			}
		}
		if err := ensureTableColumn(tx, "durable_agent_identity_state", column.name, column.typ); err != nil {
			return fmt.Errorf("ensure durable_agent_identity_state.%s: %w", column.name, err)
		}
	}
	if currentVersion < 13 {
		if err := backfillDurableAgentBootstrapCeilings(tx); err != nil {
			return err
		}
	}
	if currentVersion < 22 {
		if err := migrateArtifactIndexOccurrenceIdentity(tx); err != nil {
			return err
		}
	}
	if currentVersion > 0 && currentVersion < 26 {
		if err := backfillDurableAgentIdentityState(tx); err != nil {
			return err
		}
	}
	for _, spec := range []struct{ table, name, typ string }{
		{table: "tool_install_records", name: "consecutive_failures", typ: "INTEGER NOT NULL DEFAULT 0"},
		{table: "tool_install_records", name: "last_failure_at", typ: "TEXT"},
		{table: "tool_probe_records", name: "consecutive_failures", typ: "INTEGER NOT NULL DEFAULT 0"},
		{table: "tool_probe_records", name: "last_failure_at", typ: "TEXT"},
		{table: "tool_audit_records", name: "consecutive_failures", typ: "INTEGER NOT NULL DEFAULT 0"},
		{table: "tool_audit_records", name: "last_failure_at", typ: "TEXT"},
		{table: "pending_decisions", name: "rationale", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "pending_decisions", name: "artifact_refs_json", typ: "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := ensureTableColumn(tx, spec.table, spec.name, spec.typ); err != nil {
			return err
		}
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

func ensureSessionIdentityIndexes(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_transport_scope ON sessions(chat_id, user_id, scope_kind, scope_id, durable_agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_active ON messages(session_id, compacted, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_outbound_session ON outbound_messages(session_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_review_events_target ON review_events(target_chat_id, status, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_review_events_target_session ON review_events(target_session_id, status, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_events_session ON plan_events(session_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_proposals_tool_status ON tool_proposals(tool_name, review_status, updated_at, proposal_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_proposals_status ON tool_proposals(review_status, updated_at, proposal_id)`,
		`CREATE INDEX IF NOT EXISTS idx_registered_tools_state ON registered_tools(registered, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_exposures_principal ON tool_exposures(principal, active, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_install_records_status ON tool_install_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_probe_records_status ON tool_probe_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_audit_records_status ON tool_audit_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_session ON turn_runs(session_id, started_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_recovery ON turn_runs(status, recovery_logged_at, started_at, id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure session identity index: %w", err)
		}
	}
	return nil
}

func backfillDurableAgentBootstrapCeilings(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT agent_id, channel_kind, live_policy_json, COALESCE(bootstrap_ceiling_json, '')
		FROM durable_agents
	`)
	if err != nil {
		return fmt.Errorf("query durable agents for bootstrap ceiling backfill: %w", err)
	}
	defer rows.Close()

	type row struct {
		agentID        string
		channelKind    string
		livePolicyJSON string
		bootstrapJSON  string
	}
	var updates []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.agentID, &item.channelKind, &item.livePolicyJSON, &item.bootstrapJSON); err != nil {
			return fmt.Errorf("scan durable agent bootstrap ceiling row: %w", err)
		}
		ceiling, err := unmarshalDurableAgentBootstrapCeiling(item.bootstrapJSON)
		if err != nil {
			return fmt.Errorf("decode durable agent bootstrap ceiling agent_id=%s: %w", item.agentID, err)
		}
		if !ceiling.IsZero() {
			continue
		}
		updates = append(updates, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate durable agents for bootstrap ceiling backfill: %w", err)
	}
	for _, item := range updates {
		policy, err := unmarshalDurableAgentLivePolicy(item.livePolicyJSON)
		if err != nil {
			return fmt.Errorf("decode durable agent live policy agent_id=%s: %w", item.agentID, err)
		}
		bootstrapJSON, err := marshalDurableAgentBootstrapCeiling(core.DefaultDurableAgentBootstrapCeiling(item.channelKind, policy))
		if err != nil {
			return fmt.Errorf("marshal durable agent bootstrap ceiling agent_id=%s: %w", item.agentID, err)
		}
		if _, err := tx.Exec(`UPDATE durable_agents SET bootstrap_ceiling_json = ? WHERE agent_id = ?`, bootstrapJSON, item.agentID); err != nil {
			return fmt.Errorf("backfill durable agent bootstrap ceiling agent_id=%s: %w", item.agentID, err)
		}
	}
	return nil
}

func backfillDurableAgentIdentityState(tx *sql.Tx) error {
	for _, column := range []string{
		"last_offered_policy_version",
		"last_offered_policy_hash",
		"last_offered_policy_at",
		"last_acknowledged_policy_version",
		"last_acknowledged_policy_hash",
		"last_acknowledged_policy_at",
		"last_applied_policy_version",
		"last_applied_policy_hash",
		"last_applied_policy_at",
	} {
		has, err := tableHasColumn(tx, "durable_agent_state", column)
		if err != nil {
			return err
		}
		if !has {
			return nil
		}
	}
	_, err := tx.Exec(`
		INSERT OR REPLACE INTO durable_agent_identity_state(
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		)
		SELECT
			agent_id,
			COALESCE(last_offered_policy_version, 0), COALESCE(last_offered_policy_hash, ''), last_offered_policy_at,
			COALESCE(last_acknowledged_policy_version, 0), COALESCE(last_acknowledged_policy_hash, ''), last_acknowledged_policy_at,
			COALESCE(last_applied_policy_version, 0), COALESCE(last_applied_policy_hash, ''), last_applied_policy_at,
			COALESCE(updated_at, datetime('now'))
		FROM durable_agent_state
	`)
	if err != nil {
		return fmt.Errorf("backfill durable agent identity state: %w", err)
	}
	return nil
}

func migrateArtifactIndexOccurrenceIdentity(tx *sql.Tx) error {
	if _, err := tx.Exec(`DROP TABLE IF EXISTS artifact_index_v22_old`); err != nil {
		return fmt.Errorf("drop prior artifact index backup: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE artifact_index RENAME TO artifact_index_v22_old`); err != nil {
		return fmt.Errorf("rename artifact_index for rebuild: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE artifact_index (
		session_id TEXT NOT NULL,
		turn_index INTEGER NOT NULL DEFAULT 0,
		artifact_id TEXT NOT NULL,
		chat_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		source_type TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		handling TEXT NOT NULL DEFAULT '',
		retention TEXT NOT NULL DEFAULT '',
		fetch_state TEXT NOT NULL DEFAULT '',
		materialized_path TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (session_id, turn_index, artifact_id)
	)`); err != nil {
		return fmt.Errorf("create rebuilt artifact_index: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_session_turn ON artifact_index(session_id, turn_index DESC, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_summary ON artifact_index(summary, kind, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create rebuilt artifact_index support: %w", err)
		}
	}
	if err := rebuildArtifactIndexFromMessages(tx); err != nil {
		return err
	}
	if err := backfillArtifactIndexFromSessions(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE artifact_index_v22_old`); err != nil {
		return fmt.Errorf("drop rebuilt artifact index backup: %w", err)
	}
	return nil
}

func rebuildArtifactIndexFromMessages(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT session_id, chat_id, user_id, turn_index, floor_metadata
		FROM messages
		WHERE TRIM(COALESCE(floor_metadata, '')) <> ''
	`)
	if err != nil {
		return fmt.Errorf("query messages for artifact index rebuild: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			sessionID     string
			chatID        int64
			userID        int64
			turnIndex     int
			floorMetadata sql.NullString
		)
		if err := rows.Scan(&sessionID, &chatID, &userID, &turnIndex, &floorMetadata); err != nil {
			return fmt.Errorf("scan message artifact rebuild row: %w", err)
		}
		if err := upsertArtifactIndexFloorMetadata(tx, strings.TrimSpace(sessionID), chatID, userID, turnIndex, nullToString(floorMetadata)); err != nil {
			return fmt.Errorf("rebuild artifact index from message session_id=%s turn_index=%d: %w", strings.TrimSpace(sessionID), turnIndex, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate messages for artifact index rebuild: %w", err)
	}
	return nil
}

func backfillArtifactIndexFromSessions(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT session_id, chat_id, user_id, turn_count, last_floor_metadata
		FROM sessions
		WHERE TRIM(COALESCE(last_floor_metadata, '')) <> ''
	`)
	if err != nil {
		return fmt.Errorf("query sessions for artifact index backfill: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			sessionID         string
			chatID            int64
			userID            int64
			turnCount         int
			lastFloorMetadata sql.NullString
		)
		if err := rows.Scan(&sessionID, &chatID, &userID, &turnCount, &lastFloorMetadata); err != nil {
			return fmt.Errorf("scan session artifact backfill row: %w", err)
		}
		if err := upsertArtifactIndexFloorMetadata(tx, strings.TrimSpace(sessionID), chatID, userID, turnCount, nullToString(lastFloorMetadata)); err != nil {
			return fmt.Errorf("backfill artifact index session_id=%s: %w", strings.TrimSpace(sessionID), err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sessions for artifact index backfill: %w", err)
	}
	return nil
}

func tableHasColumn(tx *sql.Tx, table string, name string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("query table_info(%s): %w", table, err)
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
			return false, fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if strings.EqualFold(column, name) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate table_info(%s): %w", table, err)
	}
	return false, nil
}

func rejectUnsupportedLegacySchema(tx *sql.Tx, currentVersion int) error {
	if currentVersion > 0 && currentVersion < minimumSupportedLegacySchemaVersion {
		return fmt.Errorf(
			"unsupported legacy database schema version %d (minimum supported existing schema version is %d); reinstall from a clean state",
			currentVersion,
			minimumSupportedLegacySchemaVersion,
		)
	}
	if currentVersion != 0 {
		return nil
	}

	// Version 0 is valid for freshly initialized databases in this release.
	// Distinguish that from unversioned legacy layouts by checking modern markers.
	for _, marker := range []struct {
		table  string
		column string
	}{
		{table: "sessions", column: "scope_kind"},
		{table: "sessions", column: "session_id"},
		{table: "durable_agents", column: "live_policy_json"},
	} {
		has, err := tableHasColumn(tx, marker.table, marker.column)
		if err != nil {
			return err
		}
		if !has {
			return fmt.Errorf(
				"unsupported legacy database schema (missing %s.%s); reinstall from a clean state",
				marker.table,
				marker.column,
			)
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

func upsertArtifactIndexRecords(tx *sql.Tx, session *Session) error {
	if tx == nil || session == nil {
		return nil
	}
	return upsertArtifactIndexFloorMetadata(tx, strings.TrimSpace(session.SessionID), session.ChatID, session.UserID, session.TurnCount, session.LastFloorMetadata)
}

func upsertArtifactIndexFloorMetadata(tx *sql.Tx, sessionID string, chatID int64, userID int64, turnIndex int, metadata string) error {
	if tx == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	metadata = strings.TrimSpace(metadata)
	if sessionID == "" || metadata == "" {
		return nil
	}
	var floor core.FloorMetadata
	if err := json.Unmarshal([]byte(metadata), &floor); err != nil {
		return nil
	}
	for _, ref := range floor.Artifacts {
		retention := strings.TrimSpace(ref.Retention)
		if retention != "session_reference" && retention != "child_local" {
			continue
		}
		artifactID := strings.TrimSpace(ref.ArtifactID)
		if artifactID == "" {
			continue
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`
			INSERT INTO artifact_index(
				session_id, turn_index, artifact_id, chat_id, user_id, source_type, kind, summary,
				handling, retention, fetch_state, materialized_path, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, turn_index, artifact_id) DO UPDATE SET
				chat_id = excluded.chat_id,
				user_id = excluded.user_id,
				source_type = excluded.source_type,
				kind = excluded.kind,
				summary = excluded.summary,
				handling = excluded.handling,
				retention = excluded.retention,
				fetch_state = excluded.fetch_state,
				materialized_path = excluded.materialized_path,
				updated_at = excluded.updated_at
		`, sessionID, turnIndex, artifactID, chatID, userID,
			strings.TrimSpace(ref.SourceType), strings.TrimSpace(ref.Kind), strings.TrimSpace(ref.Summary),
			strings.TrimSpace(ref.Handling), retention, strings.TrimSpace(ref.FetchState), strings.TrimSpace(ref.MaterializedPath), now, now); err != nil {
			return fmt.Errorf("upsert artifact index record %s/%d/%s: %w", sessionID, turnIndex, artifactID, err)
		}
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

func defaultJSONString(raw string, fallback string) string {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	return raw
}

func marshalStringSlice(values []string) ([]byte, error) {
	if len(values) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(values)
}

func marshalInt64Slice(values []int64) ([]byte, error) {
	values = core.NormalizeDurableAgentAllowedTelegramUserIDs(values)
	if len(values) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal(values)
}

func queryDurableAgent(q interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, agentID string) (*core.DurableAgent, error) {
	rows, err := q.Query(`
		SELECT
			agent_id, parent_agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id,
			channel_kind, live_policy_json, COALESCE(channel_config_json, ''), COALESCE(bootstrap_ceiling_json, ''), COALESCE(bootstrap_provider_json, ''), COALESCE(control_plane_secret, ''), policy_version, policy_hash, policy_issued_at, local_storage_roots_json, network_policy,
			wakeup_mode, secret_scopes_json, COALESCE(allowed_telegram_user_ids_json, '[]'), status, created_at, updated_at
		FROM durable_agents
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	agent, err := scanDurableAgent(rows)
	if err != nil {
		return nil, err
	}
	return &agent, nil
}

func upsertDurableAgentRemoteEnrollmentExec(exec sqlExecer, enrollment core.DurableAgentRemoteEnrollment) error {
	enrollment = core.NormalizeDurableAgentRemoteEnrollment(enrollment)
	if enrollment.AgentID == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: agent_id is required")
	}
	if enrollment.ParentControlURL == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: parent_control_url is required")
	}
	if enrollment.KeyFingerprint == "" {
		return fmt.Errorf("upsert durable agent remote enrollment: key_fingerprint is required")
	}
	now := time.Now().UTC()
	if enrollment.EnrolledAt.IsZero() {
		enrollment.EnrolledAt = now
	}
	_, err := exec.Exec(`
		INSERT INTO durable_agent_remote_enrollments(
			agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET
			parent_control_url = excluded.parent_control_url,
			key_fingerprint = excluded.key_fingerprint,
			protocol_version = excluded.protocol_version,
			status = excluded.status,
			last_sequence = excluded.last_sequence,
			enrolled_at = excluded.enrolled_at,
			last_seen_at = excluded.last_seen_at,
			revoked_at = excluded.revoked_at,
			updated_at = excluded.updated_at
	`,
		enrollment.AgentID, enrollment.ParentControlURL, enrollment.KeyFingerprint, enrollment.ProtocolVersion, enrollment.Status,
		maxInt64(enrollment.LastSequence, 0), nullableTime(enrollment.EnrolledAt), nullableTime(enrollment.LastSeenAt), nullableTime(enrollment.RevokedAt), now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert durable agent remote enrollment: %w", err)
	}
	return nil
}

func queryDurableAgentRemoteEnrollment(q sqlQueryer, agentID string) (*core.DurableAgentRemoteEnrollment, error) {
	rows, err := q.Query(`
		SELECT agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, revoked_at
		FROM durable_agent_remote_enrollments
		WHERE agent_id = ?
	`, strings.TrimSpace(agentID))
	if err != nil {
		return nil, fmt.Errorf("query durable agent remote enrollment: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	enrollment, err := scanDurableAgentRemoteEnrollment(rows)
	if err != nil {
		return nil, err
	}
	return &enrollment, nil
}

func marshalDurableAgentLivePolicy(policy core.DurableAgentLivePolicy) (string, string, error) {
	normalized := core.NormalizeDurableAgentLivePolicy(policy)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", "", err
	}
	hash, err := core.DurableAgentPolicyHash(normalized)
	if err != nil {
		return "", "", err
	}
	return string(raw), hash, nil
}

func marshalDurableAgentChannelConfig(cfg core.DurableAgentChannelConfig) (string, error) {
	normalized := core.NormalizeDurableAgentChannelConfig(cfg)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalDurableAgentBootstrapCeiling(ceiling core.DurableAgentBootstrapCeiling) (string, error) {
	normalized := core.NormalizeDurableAgentBootstrapCeiling(ceiling)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalDurableAgentBootstrapLLM(bootstrap core.NodeLLMBootstrap) (string, error) {
	normalized := core.NormalizeNodeLLMBootstrap(bootstrap)
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func redactDurableAgentBootstrapSecrets(bootstrap core.NodeLLMBootstrap) core.NodeLLMBootstrap {
	redacted := core.NormalizeNodeLLMBootstrap(bootstrap)
	redacted.APIKey = ""
	return redacted
}

func unmarshalDurableAgentLivePolicy(raw string) (core.DurableAgentLivePolicy, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{}), nil
	}
	var policy core.DurableAgentLivePolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return core.DurableAgentLivePolicy{}, err
	}
	return core.NormalizeDurableAgentLivePolicy(policy), nil
}

func unmarshalDurableAgentChannelConfig(raw string) (core.DurableAgentChannelConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeDurableAgentChannelConfig(core.DurableAgentChannelConfig{}), nil
	}
	var cfg core.DurableAgentChannelConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return core.DurableAgentChannelConfig{}, err
	}
	return core.NormalizeDurableAgentChannelConfig(cfg), nil
}

func unmarshalDurableAgentBootstrapCeiling(raw string) (core.DurableAgentBootstrapCeiling, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeDurableAgentBootstrapCeiling(core.DurableAgentBootstrapCeiling{}), nil
	}
	var ceiling core.DurableAgentBootstrapCeiling
	if err := json.Unmarshal([]byte(raw), &ceiling); err != nil {
		return core.DurableAgentBootstrapCeiling{}, err
	}
	return core.NormalizeDurableAgentBootstrapCeiling(ceiling), nil
}

func unmarshalDurableAgentBootstrapLLM(raw string) (core.NodeLLMBootstrap, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.NormalizeNodeLLMBootstrap(core.NodeLLMBootstrap{}), nil
	}
	var bootstrap core.NodeLLMBootstrap
	if err := json.Unmarshal([]byte(raw), &bootstrap); err != nil {
		return core.NodeLLMBootstrap{}, err
	}
	return core.NormalizeNodeLLMBootstrap(bootstrap), nil
}

func unmarshalStringSlice(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func unmarshalInt64Slice(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var values []int64
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return core.NormalizeDurableAgentAllowedTelegramUserIDs(values), nil
}

func scanDurableAgent(scanner interface{ Scan(dest ...any) error }) (core.DurableAgent, error) {
	var (
		agent                 core.DurableAgent
		parentAgentID         sql.NullString
		parentScopeKind       sql.NullString
		parentScopeID         sql.NullString
		livePolicyJSON        string
		channelConfigJSON     string
		bootstrapCeilingJSON  string
		bootstrapProviderJSON string
		controlPlaneSecret    sql.NullString
		policyVersion         int64
		policyHash            string
		policyIssuedAt        sql.NullString
		storageRootsJSON      string
		networkPolicy         sql.NullString
		wakeupMode            sql.NullString
		secretScopesJSON      string
		allowedUserIDsJSON    string
		status                sql.NullString
		createdAtRaw          string
		updatedAtRaw          string
	)
	if err := scanner.Scan(
		&agent.AgentID, &parentAgentID, &parentScopeKind, &parentScopeID, &agent.ReviewTargetChatID,
		&agent.ChannelKind, &livePolicyJSON, &channelConfigJSON, &bootstrapCeilingJSON, &bootstrapProviderJSON, &controlPlaneSecret, &policyVersion, &policyHash, &policyIssuedAt, &storageRootsJSON, &networkPolicy,
		&wakeupMode, &secretScopesJSON, &allowedUserIDsJSON, &status, &createdAtRaw, &updatedAtRaw,
	); err != nil {
		return core.DurableAgent{}, fmt.Errorf("scan durable agent: %w", err)
	}
	var err error
	agent.ParentAgentID = nullToString(parentAgentID)
	agent.ParentScopeKind = nullToString(parentScopeKind)
	agent.ParentScopeID = nullToString(parentScopeID)
	agent.LivePolicy, err = unmarshalDurableAgentLivePolicy(livePolicyJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent live policy: %w", err)
	}
	agent.ChannelConfig, err = unmarshalDurableAgentChannelConfig(channelConfigJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent channel config: %w", err)
	}
	agent.BootstrapCeiling, err = unmarshalDurableAgentBootstrapCeiling(bootstrapCeilingJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent bootstrap ceiling: %w", err)
	}
	agent.BootstrapLLM, err = unmarshalDurableAgentBootstrapLLM(bootstrapProviderJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent bootstrap llm: %w", err)
	}
	agent.ControlPlaneSecret = nullToString(controlPlaneSecret)
	if agent.BootstrapCeiling.IsZero() {
		agent.BootstrapCeiling = core.DefaultDurableAgentBootstrapCeiling(agent.ChannelKind, agent.LivePolicy)
	}
	agent.PolicyVersion = policyVersion
	agent.PolicyHash = strings.TrimSpace(policyHash)
	if agent.PolicyHash == "" {
		agent.PolicyHash, err = core.DurableAgentPolicyHash(agent.LivePolicy)
		if err != nil {
			return core.DurableAgent{}, fmt.Errorf("hash durable agent live policy: %w", err)
		}
	}
	if policyIssuedAt.Valid && strings.TrimSpace(policyIssuedAt.String) != "" {
		agent.PolicyIssuedAt, err = parseSQLiteTime(policyIssuedAt.String)
		if err != nil {
			return core.DurableAgent{}, fmt.Errorf("parse durable agent policy_issued_at: %w", err)
		}
	}
	agent.NetworkPolicy = nullToString(networkPolicy)
	agent.WakeupMode = nullToString(wakeupMode)
	agent.Status = nullToString(status)
	agent.LocalStorageRoots, err = unmarshalStringSlice(storageRootsJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent storage roots: %w", err)
	}
	agent.SecretScopes, err = unmarshalStringSlice(secretScopesJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent secret scopes: %w", err)
	}
	agent.AllowedTelegramUserIDs, err = unmarshalInt64Slice(allowedUserIDsJSON)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("decode durable agent allowed telegram user ids: %w", err)
	}
	agent.CreatedAt, err = parseSQLiteTime(createdAtRaw)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("parse durable agent created_at: %w", err)
	}
	agent.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgent{}, fmt.Errorf("parse durable agent updated_at: %w", err)
	}
	return agent, nil
}

func validateDurableAgentChannelConfig(channelKind string, cfg core.DurableAgentChannelConfig) error {
	cfg = core.NormalizeDurableAgentChannelConfig(cfg)
	switch strings.TrimSpace(channelKind) {
	case "email":
		if cfg.Email == nil {
			return nil
		}
		if strings.TrimSpace(cfg.Email.PollInterval) != "" {
			if _, err := time.ParseDuration(strings.TrimSpace(cfg.Email.PollInterval)); err != nil {
				return fmt.Errorf("invalid email poll_interval %q: %w", cfg.Email.PollInterval, err)
			}
		}
	}
	return nil
}

func scanReviewEvent(scanner interface{ Scan(dest ...any) error }) (ReviewEvent, error) {
	var (
		event           ReviewEvent
		createdAtRaw    string
		deliveredAtRaw  sql.NullString
		turnFromRaw     sql.NullInt64
		turnToRaw       sql.NullInt64
		targetChatIDRaw int64
		sourceSessionID sql.NullString
		sourceScopeKind sql.NullString
		sourceScopeID   sql.NullString
		sourceAgentID   sql.NullString
		targetSessionID sql.NullString
		targetScopeKind sql.NullString
		targetScopeID   sql.NullString
		targetAgentID   sql.NullString
		metadataJSON    sql.NullString
	)

	if err := scanner.Scan(
		&event.ID, &sourceSessionID, &event.SourceChatID, &event.SourceUserID, &event.SourceRole, &sourceScopeKind, &sourceScopeID, &sourceAgentID,
		&targetSessionID, &targetChatIDRaw, &targetScopeKind, &targetScopeID, &targetAgentID,
		&turnFromRaw, &turnToRaw, &event.Summary, &metadataJSON, &event.Status, &createdAtRaw, &deliveredAtRaw,
	); err != nil {
		return ReviewEvent{}, fmt.Errorf("scan review event: %w", err)
	}

	event.SourceSessionID = nullToString(sourceSessionID)
	event.TargetAdminChatID = targetChatIDRaw
	event.TargetSessionID = nullToString(targetSessionID)
	event.SourceScope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(sourceScopeKind)),
		ID:             nullToString(sourceScopeID),
		DurableAgentID: nullToString(sourceAgentID),
	})
	event.TargetScope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(nullToString(targetScopeKind)),
		ID:             nullToString(targetScopeID),
		DurableAgentID: nullToString(targetAgentID),
	})
	event.MetadataJSON = nullToString(metadataJSON)
	if turnFromRaw.Valid {
		event.TurnFrom = int(turnFromRaw.Int64)
	}
	if turnToRaw.Valid {
		event.TurnTo = int(turnToRaw.Int64)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ReviewEvent{}, fmt.Errorf("parse review event created_at: %w", err)
	}
	event.CreatedAt = createdAt
	if deliveredAtRaw.Valid && deliveredAtRaw.String != "" {
		deliveredAt, err := parseSQLiteTime(deliveredAtRaw.String)
		if err != nil {
			return ReviewEvent{}, fmt.Errorf("parse review event delivered_at: %w", err)
		}
		event.DeliveredAt = deliveredAt
	}
	return event, nil
}

func scanDurableAgentPolicyUpdate(scanner interface{ Scan(dest ...any) error }) (DurableAgentPolicyUpdate, error) {
	var (
		update       DurableAgentPolicyUpdate
		reason       sql.NullString
		appliedAtRaw string
	)
	if err := scanner.Scan(&update.ID, &update.AgentID, &update.SourceReviewEventID, &update.PreviousVersion, &update.NewVersion, &update.PolicyHash, &update.PolicyJSON, &reason, &appliedAtRaw); err != nil {
		return DurableAgentPolicyUpdate{}, fmt.Errorf("scan durable agent policy update: %w", err)
	}
	update.Reason = nullToString(reason)
	appliedAt, err := parseSQLiteTime(appliedAtRaw)
	if err != nil {
		return DurableAgentPolicyUpdate{}, fmt.Errorf("parse durable agent policy update applied_at: %w", err)
	}
	update.AppliedAt = appliedAt
	return update, nil
}

func scanDurableAgentBootstrapUpdate(scanner interface{ Scan(dest ...any) error }) (DurableAgentBootstrapUpdate, error) {
	var (
		update                DurableAgentBootstrapUpdate
		actorRole             sql.NullString
		updateKind            sql.NullString
		previousBootstrapJSON string
		newBootstrapJSON      string
		reason                sql.NullString
		appliedAtRaw          string
	)
	if err := scanner.Scan(&update.ID, &update.AgentID, &update.SourceReviewEventID, &update.ActorUserID, &actorRole, &updateKind, &previousBootstrapJSON, &newBootstrapJSON, &reason, &appliedAtRaw); err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("scan durable agent bootstrap update: %w", err)
	}
	var err error
	update.ActorRole = nullToString(actorRole)
	update.UpdateKind = nullToString(updateKind)
	update.PreviousBootstrap, err = unmarshalDurableAgentBootstrapLLM(previousBootstrapJSON)
	if err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("decode previous durable agent bootstrap update: %w", err)
	}
	update.NewBootstrap, err = unmarshalDurableAgentBootstrapLLM(newBootstrapJSON)
	if err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("decode new durable agent bootstrap update: %w", err)
	}
	update.Reason = nullToString(reason)
	appliedAt, err := parseSQLiteTime(appliedAtRaw)
	if err != nil {
		return DurableAgentBootstrapUpdate{}, fmt.Errorf("parse durable agent bootstrap update applied_at: %w", err)
	}
	update.AppliedAt = appliedAt
	return update, nil
}

func scanDurableAgentRuntimeState(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentRuntimeState, error) {
	var (
		state         core.DurableAgentRuntimeState
		cursorRaw     sql.NullString
		statusRaw     sql.NullString
		stateJSONRaw  sql.NullString
		lastStatusRaw sql.NullString
		lastErrorRaw  sql.NullString
		lastWakeAtRaw sql.NullString
		lastReviewRaw sql.NullString
		dormantAtRaw  sql.NullString
		updatedAtRaw  string
	)
	if err := scanner.Scan(
		&state.AgentID, &cursorRaw, &statusRaw, &stateJSONRaw,
		&lastStatusRaw, &lastErrorRaw,
		&lastWakeAtRaw, &lastReviewRaw, &dormantAtRaw, &updatedAtRaw,
	); err != nil {
		return core.DurableAgentRuntimeState{}, fmt.Errorf("scan durable agent runtime state: %w", err)
	}
	state.Cursor = nullToString(cursorRaw)
	state.Status = nullToString(statusRaw)
	state.StateJSON = nullToString(stateJSONRaw)
	state.LastApplyStatus = nullToString(lastStatusRaw)
	state.LastApplyError = nullToString(lastErrorRaw)
	var err error
	if lastWakeAtRaw.Valid && lastWakeAtRaw.String != "" {
		state.LastWakeAt, err = parseSQLiteTime(lastWakeAtRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime last_wake_at: %w", err)
		}
	}
	if lastReviewRaw.Valid && lastReviewRaw.String != "" {
		state.LastReviewAt, err = parseSQLiteTime(lastReviewRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime last_review_at: %w", err)
		}
	}
	if dormantAtRaw.Valid && dormantAtRaw.String != "" {
		state.DormantAt, err = parseSQLiteTime(dormantAtRaw.String)
		if err != nil {
			return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime dormant_at: %w", err)
		}
	}
	state.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgentRuntimeState{}, fmt.Errorf("parse durable agent runtime updated_at: %w", err)
	}
	return state, nil
}

func scanDurableAgentIdentityState(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentIdentityState, error) {
	var (
		state                         core.DurableAgentIdentityState
		lastOfferedPolicyHashRaw      sql.NullString
		lastOfferedPolicyAtRaw        sql.NullString
		lastAcknowledgedPolicyHashRaw sql.NullString
		lastAcknowledgedPolicyAtRaw   sql.NullString
		lastAppliedPolicyHashRaw      sql.NullString
		lastAppliedPolicyAtRaw        sql.NullString
		updatedAtRaw                  string
	)
	if err := scanner.Scan(
		&state.AgentID,
		&state.LastOfferedPolicyVersion, &lastOfferedPolicyHashRaw, &lastOfferedPolicyAtRaw,
		&state.LastAcknowledgedPolicyVersion, &lastAcknowledgedPolicyHashRaw, &lastAcknowledgedPolicyAtRaw,
		&state.LastAppliedPolicyVersion, &lastAppliedPolicyHashRaw, &lastAppliedPolicyAtRaw,
		&updatedAtRaw,
	); err != nil {
		return core.DurableAgentIdentityState{}, fmt.Errorf("scan durable agent identity state: %w", err)
	}
	state.LastOfferedPolicyHash = nullToString(lastOfferedPolicyHashRaw)
	state.LastAcknowledgedPolicyHash = nullToString(lastAcknowledgedPolicyHashRaw)
	state.LastAppliedPolicyHash = nullToString(lastAppliedPolicyHashRaw)
	var err error
	if lastOfferedPolicyAtRaw.Valid && lastOfferedPolicyAtRaw.String != "" {
		state.LastOfferedPolicyAt, err = parseSQLiteTime(lastOfferedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_offered_policy_at: %w", err)
		}
	}
	if lastAcknowledgedPolicyAtRaw.Valid && lastAcknowledgedPolicyAtRaw.String != "" {
		state.LastAcknowledgedPolicyAt, err = parseSQLiteTime(lastAcknowledgedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_acknowledged_policy_at: %w", err)
		}
	}
	if lastAppliedPolicyAtRaw.Valid && lastAppliedPolicyAtRaw.String != "" {
		state.LastAppliedPolicyAt, err = parseSQLiteTime(lastAppliedPolicyAtRaw.String)
		if err != nil {
			return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity last_applied_policy_at: %w", err)
		}
	}
	state.UpdatedAt, err = parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return core.DurableAgentIdentityState{}, fmt.Errorf("parse durable agent identity updated_at: %w", err)
	}
	return state, nil
}

func scanDurableAgentRemoteEnrollment(scanner interface{ Scan(dest ...any) error }) (core.DurableAgentRemoteEnrollment, error) {
	var (
		enrollment      core.DurableAgentRemoteEnrollment
		protocolVersion sql.NullString
		statusRaw       sql.NullString
		enrolledAtRaw   sql.NullString
		lastSeenAtRaw   sql.NullString
		revokedAtRaw    sql.NullString
	)
	if err := scanner.Scan(
		&enrollment.AgentID, &enrollment.ParentControlURL, &enrollment.KeyFingerprint, &protocolVersion, &statusRaw, &enrollment.LastSequence, &enrolledAtRaw, &lastSeenAtRaw, &revokedAtRaw,
	); err != nil {
		return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("scan durable agent remote enrollment: %w", err)
	}
	enrollment.ProtocolVersion = nullToString(protocolVersion)
	enrollment.Status = nullToString(statusRaw)
	var err error
	if enrolledAtRaw.Valid && enrolledAtRaw.String != "" {
		enrollment.EnrolledAt, err = parseSQLiteTime(enrolledAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment enrolled_at: %w", err)
		}
	}
	if lastSeenAtRaw.Valid && lastSeenAtRaw.String != "" {
		enrollment.LastSeenAt, err = parseSQLiteTime(lastSeenAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment last_seen_at: %w", err)
		}
	}
	if revokedAtRaw.Valid && revokedAtRaw.String != "" {
		enrollment.RevokedAt, err = parseSQLiteTime(revokedAtRaw.String)
		if err != nil {
			return core.DurableAgentRemoteEnrollment{}, fmt.Errorf("parse durable agent remote enrollment revoked_at: %w", err)
		}
	}
	return core.NormalizeDurableAgentRemoteEnrollment(enrollment), nil
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

func normalizeRhizomeConcepts(concepts []string) []string {
	seen := make(map[string]struct{}, len(concepts))
	out := make([]string, 0, len(concepts))
	for _, concept := range concepts {
		normalized := strings.ToLower(strings.TrimSpace(concept))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func orderedPair(a string, b string) (string, string) {
	if a <= b {
		return a, b
	}
	return b, a
}

func classifyRhizomeDecayState(recurrence int) string {
	switch {
	case recurrence >= 8:
		return "frozen"
	case recurrence >= 5:
		return "cold"
	case recurrence >= 3:
		return "warm"
	default:
		return "hot"
	}
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
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ContinuationState{}
	}
	var state ContinuationState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return ContinuationState{}
	}
	return NormalizeContinuationState(state)
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

func maxInt64(a int64, b int64) int64 {
	if a >= b {
		return a
	}
	return b
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
