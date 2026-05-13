//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRejectsUnsupportedExistingSessionSchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "unsupported-v1.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open existing db: %v", err)
	}

	ddl := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (1)`,
		`CREATE TABLE sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			system_prompt TEXT,
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
	}
	for _, ddl := range ddl {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply ddl: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close existing db: %v", err)
	}

	_, err = NewSQLiteStore(dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore() err = nil, want unsupported old schema error")
	}
	if !strings.Contains(err.Error(), "unsupported database schema version 1") {
		t.Fatalf("NewSQLiteStore() err = %v, want unsupported old schema version message", err)
	}
}

func TestInitRejectsUnsupportedUnversionedExistingSchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "unversioned.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open existing db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		chat_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL DEFAULT 0,
		system_prompt TEXT,
		PRIMARY KEY (chat_id, user_id)
	)`); err != nil {
		t.Fatalf("create unversioned sessions table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close existing db: %v", err)
	}

	_, err = NewSQLiteStore(dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore() err = nil, want unsupported unversioned schema error")
	}
	if !strings.Contains(err.Error(), "unsupported unversioned database schema") {
		t.Fatalf("NewSQLiteStore() err = %v, want unsupported unversioned schema message", err)
	}
}

func TestInitRejectsUnsupportedExistingSessionIdentitySchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "existing-plan-events.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open existing db: %v", err)
	}

	chatID := int64(321)
	sessionID := SessionIDFromParts(chatID, 0, ScopeRef{})

	ddl := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (9)`,
		`CREATE TABLE sessions (
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			system_prompt TEXT,
			last_floor_text TEXT,
			last_floor_metadata TEXT,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
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
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			role TEXT NOT NULL,
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
		`CREATE TABLE outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE review_events (
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
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			delivered_at TEXT
		)`,
		`CREATE TABLE turn_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
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
		`CREATE TABLE compaction_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT,
			timestamp TEXT NOT NULL DEFAULT (datetime('now')),
			turns_before INTEGER,
			turns_after INTEGER,
			tokens_before INTEGER,
			tokens_after INTEGER,
			summary TEXT,
			strategy TEXT NOT NULL DEFAULT 'summarize'
		)`,
		`CREATE TABLE plan_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			event_kind TEXT NOT NULL,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
		)`,
	}
	for _, ddl := range ddl {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatalf("apply ddl: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO sessions(session_id, chat_id, user_id, system_prompt)
		VALUES (?, ?, 0, 'old prompt')
	`, sessionID, chatID); err != nil {
		t.Fatalf("insert old session: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO plan_events(session_id, event_kind, plan_state_json)
		VALUES (?, 'update_plan', '{"steps":[{"step":"repair startup","status":"pending"}]}')
	`, sessionID); err != nil {
		t.Fatalf("insert old plan event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close existing db: %v", err)
	}

	_, err = NewSQLiteStore(dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore() err = nil, want unsupported old schema error")
	}
	if !strings.Contains(err.Error(), "unsupported database schema version 9") {
		t.Fatalf("NewSQLiteStore() err = %v, want unsupported old schema version message", err)
	}
}

func TestInitRejectsUnsupportedExistingDurableAgentSchema(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "existing-durable.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open existing db: %v", err)
	}

	ddl := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (10)`,
		`CREATE TABLE durable_agents (
			agent_id TEXT PRIMARY KEY,
			parent_agent_id TEXT,
			parent_scope_kind TEXT,
			parent_scope_id TEXT,
			review_target_chat_id INTEGER NOT NULL DEFAULT 0,
			channel_kind TEXT NOT NULL,
			charter TEXT NOT NULL DEFAULT '',
			capability_envelope_json TEXT NOT NULL DEFAULT '[]',
			local_storage_roots_json TEXT NOT NULL DEFAULT '[]',
			network_policy TEXT,
			wakeup_mode TEXT,
			outbound_mode TEXT,
			drift_policy TEXT,
			secret_scopes_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE durable_agent_state (
			agent_id TEXT PRIMARY KEY,
			cursor TEXT,
			status TEXT,
			state_json TEXT,
			last_wake_at TEXT,
			last_review_at TEXT,
			dormant_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO durable_agents(
			agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id, channel_kind, charter,
			capability_envelope_json, local_storage_roots_json, wakeup_mode, outbound_mode, drift_policy, secret_scopes_json, status,
			created_at, updated_at
		) VALUES (
			'family-group', 'heartbeat', 'admin-house', 1001, 'telegram_group', 'old charter',
			'["group_reply","bounded_review_artifact"]', '["/tmp/family-group"]', 'telegram_update', 'reply_within_charter', 'admin_review', '["telegram_bot"]', 'active',
			'2026-04-12T00:00:00Z', '2026-04-12T00:10:00Z'
		)`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec old durable stmt %q: %v", stmt, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close existing db: %v", err)
	}

	_, err = NewSQLiteStore(dbPath)
	if err == nil {
		t.Fatal("NewSQLiteStore() err = nil, want unsupported old schema error")
	}
	if !strings.Contains(err.Error(), "unsupported database schema version 10") {
		t.Fatalf("NewSQLiteStore() err = %v, want unsupported old schema version message", err)
	}
}

func TestCurrentSchemaIncludesCapabilityAuthorityColumnsAndSystemChangeKind(t *testing.T) {
	t.Parallel()

	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "current-schema.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()

	var version int
	if err := store.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("query schema version err = %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}

	if _, err := store.UpsertCapabilityRequest(CapabilityRequest{
		RequestID:      "cap-system-change",
		RequestedBy:    "durable_agent:child-alpha",
		RequestedFor:   "durable_agent:child-alpha",
		Kind:           CapabilityKindSystemChange,
		TargetResource: "child-runtime-contract",
		Purpose:        "Request bounded system contract update.",
	}); err != nil {
		t.Fatalf("UpsertCapabilityRequest(system_change) err = %v", err)
	}
	if _, err := store.UpsertCapabilityGrant(CapabilityGrant{
		GrantID:        "capg-system-change",
		RequestID:      "cap-system-change",
		GrantedBy:      "telegram:1001",
		GrantedTo:      "durable_agent:child-alpha",
		Kind:           CapabilityKindSystemChange,
		TargetResource: "child-runtime-contract",
		AllowedActions: []string{"propose"},
		Status:         CapabilityGrantStatusActive,
	}); err != nil {
		t.Fatalf("UpsertCapabilityGrant(system_change) err = %v", err)
	}

	for _, column := range []string{
		"session_id",
		"turn_run_id",
		"continuation_lease_id",
		"operation_plan_lease_id",
		"authority_source",
	} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('capability_invocations') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatalf("query current capability_invocations.%s err = %v", column, err)
		}
		if count != 1 {
			t.Fatalf("capability_invocations.%s count = %d, want 1", column, count)
		}
	}

	for _, indexName := range []string{
		"idx_capability_invocations_authority_session",
		"idx_capability_invocations_lease",
	} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'index' AND name = ?`, indexName).Scan(&count); err != nil {
			t.Fatalf("query current index %s err = %v", indexName, err)
		}
		if count != 1 {
			t.Fatalf("index %s count = %d, want 1", indexName, count)
		}
	}
}
