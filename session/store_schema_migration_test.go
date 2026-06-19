//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestMigratesSchemaV43ToCurrent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v43.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v43 db: %v", err)
	}
	createSchemaV43Fixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close v43 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v43) err = %v", err)
	}

	assertSchemaVersion(t, store.db, schemaVersion)
	for _, column := range []struct {
		table string
		name  string
	}{
		{"durable_agent_remote_enrollments", "key_fingerprint"},
		{"durable_agent_remote_enrollments", "tailnet_stable_node_id"},
		{"durable_agent_remote_enrollments", "tailnet_node_name"},
		{"durable_agent_remote_enrollments", "tailnet_computed_name"},
		{"durable_agent_remote_enrollments", "tailnet_login_name"},
		{"durable_agent_remote_enrollments", "tailnet_tags_json"},
		{"durable_agent_control_receipts", "signature"},
		{"durable_agent_control_receipts", "response_status"},
		{"durable_agent_control_receipts", "response_json"},
	} {
		assertSQLiteColumn(t, store.db, column.table, column.name)
	}

	agent, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() after migration err = %v", err)
	}
	if agent.AgentID != "family-group" || agent.ChannelKind != "telegram_group" || agent.PolicyVersion != 7 {
		t.Fatalf("DurableAgent() = %#v, want preserved v43 durable agent", agent)
	}

	enrollment, err := store.DurableAgentRemoteEnrollment("family-group")
	if err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() after migration err = %v", err)
	}
	if enrollment.ParentControlURL != "https://parent.example.test" {
		t.Fatalf("ParentControlURL = %q, want preserved value", enrollment.ParentControlURL)
	}
	if enrollment.ProtocolVersion != "v1" || enrollment.Status != "active" || enrollment.LastSequence != 41 {
		t.Fatalf("enrollment = %#v, want preserved protocol/status/sequence", enrollment)
	}
	if enrollment.TailnetIdentity.StableNodeID != "" || len(enrollment.TailnetIdentity.Tags) != 0 {
		t.Fatalf("TailnetIdentity = %#v, want safe empty defaults", enrollment.TailnetIdentity)
	}

	receipt, err := store.DurableAgentControlReceipt("family-group", "msg-v43-1")
	if err != nil {
		t.Fatalf("DurableAgentControlReceipt() after migration err = %v", err)
	}
	if receipt.MessageKind != "policy_poll" || receipt.Sequence != 39 {
		t.Fatalf("receipt = %#v, want preserved kind and sequence", receipt)
	}
	if receipt.Signature != "" || receipt.ResponseStatus != 0 || receipt.ResponseJSON != "" {
		t.Fatalf("receipt replay fields = signature:%q status:%d json:%q, want safe defaults", receipt.Signature, receipt.ResponseStatus, receipt.ResponseJSON)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}
	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen migrated) err = %v", err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.db, schemaVersion)
	if _, err := reopened.DurableAgentRemoteEnrollment("family-group"); err != nil {
		t.Fatalf("DurableAgentRemoteEnrollment() after migrated reopen err = %v", err)
	}
}

func TestMigratesSchemaV44ToV45AutonomyOverrides(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v44.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v44 db: %v", err)
	}
	createSchemaV44AutoApprovalFixture(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close v44 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v44) err = %v", err)
	}
	defer store.Close()

	assertSchemaVersion(t, store.db, schemaVersion)
	activeModes, err := store.ActiveOperatorAutonomyOverrides(99170, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverrides() err = %v", err)
	}
	if len(activeModes) != 1 {
		t.Fatalf("active autonomy overrides = %#v, want one migrated active grant", activeModes)
	}
	if activeModes[0].ID != "mode-auto-active" || activeModes[0].Mode != "leased" || activeModes[0].Scope != OperatorAutoApprovalScopeWorkspace {
		t.Fatalf("active autonomy override = %#v, want copied active workspace gate", activeModes[0])
	}
	scopeKind, scopeID := OperatorAutoScopeForKey(SessionKey{ChatID: 99170})
	scopedModes, err := store.ActiveOperatorAutonomyOverridesForScope(99170, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutonomyOverridesForScope() err = %v", err)
	}
	if len(scopedModes) != 1 || scopedModes[0].ID != "mode-auto-active" {
		t.Fatalf("scoped autonomy overrides = %#v, want migrated default-chat gate", scopedModes)
	}
	activeApprovals, err := store.ActiveOperatorAutoApprovalLeases(99170, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeases() err = %v", err)
	}
	if len(activeApprovals) != 1 || activeApprovals[0].ID != "auto-active" {
		t.Fatalf("active approvals = %#v, want original active approval preserved", activeApprovals)
	}
	scopedApprovals, err := store.ActiveOperatorAutoApprovalLeasesForScope(99170, scopeKind, scopeID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ActiveOperatorAutoApprovalLeasesForScope() err = %v", err)
	}
	if len(scopedApprovals) != 1 || scopedApprovals[0].ID != "auto-active" {
		t.Fatalf("scoped approvals = %#v, want migrated default-chat approval", scopedApprovals)
	}
	if expired, ok, err := store.OperatorAutoApprovalLease("auto-expired"); err != nil || !ok || expired.ID != "auto-expired" {
		t.Fatalf("expired approval = lease:%#v ok:%v err:%v, want preserved approval history", expired, ok, err)
	}
}

func TestMigratesSchemaV45ToV46TelegramIngressLedger(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v45.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v45 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (45)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v45 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v45 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v45) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	for _, table := range []string{"telegram_ingress_offsets", "telegram_ingress_failures", "telegram_ingress_updates"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("query table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
}

func TestMigratesSchemaV46ToV47TelegramAcceptedUpdateLedger(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v46.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v46 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (46)`,
		`CREATE TABLE telegram_ingress_offsets (
			surface TEXT PRIMARY KEY,
			next_update_id INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE telegram_ingress_failures (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			surface TEXT NOT NULL DEFAULT '',
			update_id INTEGER NOT NULL DEFAULT 0,
			update_kind TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			message_id INTEGER NOT NULL DEFAULT 0,
			error_text TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO telegram_ingress_offsets(surface, next_update_id) VALUES ('telegram:primary', 123)`,
		`INSERT INTO telegram_ingress_failures(surface, update_id, update_kind, chat_id, message_id, error_text) VALUES ('telegram:primary', 122, 'message', 7001, 200, 'fixture')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v46 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v46 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v46) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "telegram_ingress_updates")
	if next, err := store.TelegramIngressNextUpdateID("telegram:primary"); err != nil || next != 123 {
		t.Fatalf("TelegramIngressNextUpdateID() = %d, err=%v, want preserved 123", next, err)
	}
	failures, err := store.RecentTelegramIngressFailures(5)
	if err != nil {
		t.Fatalf("RecentTelegramIngressFailures() err = %v", err)
	}
	if len(failures) != 1 || failures[0].UpdateID != 122 {
		t.Fatalf("failures = %#v, want preserved fixture", failures)
	}
}

func TestMigratesSchemaV47ToV48TurnProgressViews(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v47.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v47 db: %v", err)
	}
	sessionID := SessionIDFromParts(99331, 1001, ScopeRef{})
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (47)`,
		`CREATE TABLE turn_runs (
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
		`CREATE TABLE execution_events (
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
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v47 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO turn_runs(
			session_id, chat_id, user_id, kind, status, request_text, started_at, last_activity_at, progress_message_id
		) VALUES (?, 99331, 1001, 'interactive', 'running', 'inspect progress state', ?, ?, 8744)
	`, sessionID, time.Date(2026, 5, 16, 2, 37, 0, 0, time.UTC).Format(time.RFC3339Nano), time.Date(2026, 5, 16, 2, 38, 0, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert v47 turn run: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO execution_events(session_id, chat_id, user_id, seq, event_type, stage, status, payload_json, created_at)
		VALUES (?, 99331, 1001, 1, 'tool.started', 'tool', 'started', '{"run_id":1,"tool":"exec"}', ?)
	`, sessionID, time.Date(2026, 5, 16, 2, 38, 1, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert v47 execution event: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v47 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v47) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "turn_progress_views")
	run, err := store.TurnRun(1)
	if err != nil {
		t.Fatalf("TurnRun() after v48 migration err = %v", err)
	}
	if run.ProgressMessageID != 8744 || run.RequestText != "inspect progress state" {
		t.Fatalf("run = %#v, want preserved v47 turn run", run)
	}
	events, err := store.ExecutionEventsByTurnRun(SessionKey{ChatID: 99331, UserID: 1001}, 1, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsByTurnRun() err = %v", err)
	}
	if len(events) != 1 || events[0].EventType != "tool.started" {
		t.Fatalf("events = %#v, want preserved v47 execution event", events)
	}
}

func TestMigratesSchemaV48ToV49TelegramThreads(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v48.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v48 db: %v", err)
	}
	sessionID := SessionIDFromParts(99332, 0, ScopeRef{Kind: ScopeKindTelegramDM, ID: "99332"})
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (48)`,
		`CREATE TABLE sessions (
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
			working_objective_json TEXT NOT NULL DEFAULT '{}',
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
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v48 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO sessions(session_id, chat_id, user_id, scope_kind, scope_id, turn_count, created_at, updated_at)
		VALUES (?, 99332, 0, 'telegram_dm', '99332', 3, ?, ?)
	`, sessionID, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert v48 session: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v48 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v48) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "telegram_threads")
	loaded, err := store.Load(SessionKey{ChatID: 99332, UserID: 0, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "99332"}})
	if err != nil {
		t.Fatalf("Load(existing session) err = %v", err)
	}
	if loaded.TurnCount != 3 {
		t.Fatalf("TurnCount = %d, want preserved v48 session", loaded.TurnCount)
	}
	thread, created, err := store.CreateTelegramThreadForUpdate(99332, 1001, 44, 55, "new scoped work", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if !created || thread.ThreadID != 1 || !thread.Open() {
		t.Fatalf("thread = %#v created=%v, want new open thread after migration", thread, created)
	}
}

func TestMigratesSchemaV49ToV50TelegramReplyRoutingIndexes(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v49.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v49 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (49)`,
		`CREATE TABLE outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE telegram_ingress_updates (
			surface TEXT NOT NULL,
			update_id INTEGER NOT NULL,
			update_kind TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			message_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'accepted',
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			error_text TEXT NOT NULL DEFAULT '',
			inbound_json TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			accepted_at TEXT NOT NULL DEFAULT (datetime('now')),
			queued_at TEXT,
			started_at TEXT,
			completed_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(surface, update_id)
		)`,
		`CREATE TABLE telegram_threads (
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open', 'closed')),
			created_by_sender_id INTEGER NOT NULL DEFAULT 0,
			created_from_update_id INTEGER NOT NULL DEFAULT 0,
			created_message_id INTEGER NOT NULL DEFAULT 0,
			created_text TEXT NOT NULL DEFAULT '',
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			closed_at TEXT,
			absorb_summary TEXT NOT NULL DEFAULT '',
			absorbed_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, thread_id)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v49 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v49 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v49) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteIndex(t, store.db, "idx_outbound_chat_message")
	assertSQLiteIndex(t, store.db, "idx_telegram_ingress_updates_message")
	assertSQLiteIndex(t, store.db, "idx_telegram_threads_created_message")

	thread, created, err := store.CreateTelegramThreadForUpdate(99333, 1001, 44, 55, "new scoped work", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if !created || thread.ThreadID != 1 {
		t.Fatalf("thread = %#v created=%v, want created thread after v50 migration", thread, created)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(99333, 55); err != nil || !ok || got != 1 {
		t.Fatalf("TelegramThreadIDForReplyMessage(created) = %d ok=%v err=%v, want thread 1", got, ok, err)
	}
}

func TestMigratesSchemaV50ToV51ScopedPendingDecisions(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v50.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v50 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (50)`,
		`CREATE TABLE pending_decisions (
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
		`CREATE TABLE pending_artifact_retention (
			owner_key TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			inbound_message_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE pending_busy_decisions (
			owner_key TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			inbound_message_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO pending_decisions(decision_id, decision_seq, owner_key, kind, chat_id, sender_id, message_id, choices_json, default_choice)
			VALUES ('decision-1', 1, 'chat:7:sender:42', 'interrupt', 7, 42, 99, '[{"ID":"queue","Label":"Queue"}]', 'queue')`,
		`INSERT INTO pending_artifact_retention(owner_key, chat_id, sender_id, inbound_message_json)
			VALUES ('chat:7:sender:42', 7, 42, '{"ChatID":7}')`,
		`INSERT INTO pending_busy_decisions(owner_key, chat_id, sender_id, inbound_message_json)
			VALUES ('chat:8:sender:42', 8, 42, '{"ChatID":8}')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v50 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v50 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v50) err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	assertSchemaVersion(t, store.db, schemaVersion)

	decisions, err := store.PendingDecisions()
	if err != nil {
		t.Fatalf("PendingDecisions() err = %v", err)
	}
	if len(decisions) != 1 || decisions[0].ID != "decision-1" || decisions[0].SessionID != "" {
		t.Fatalf("decisions = %#v, want preserved v50 row with empty scope defaults", decisions)
	}
	busy, err := store.PendingBusyDecision("chat:8:sender:42")
	if err != nil {
		t.Fatalf("PendingBusyDecision() err = %v", err)
	}
	if busy.SessionID != "" || busy.MessageID != 0 {
		t.Fatalf("busy = %#v, want empty v51 defaults", busy)
	}
	artifact, err := store.PendingArtifactRetention("chat:7:sender:42")
	if err != nil {
		t.Fatalf("PendingArtifactRetention() err = %v", err)
	}
	if artifact.ScopeKind != "" || artifact.MessageID != 0 {
		t.Fatalf("artifact = %#v, want empty v51 defaults", artifact)
	}
}

func TestMigratesSchemaV51ToV52TelegramCallbackMessages(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v51.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v51 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (51)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v51 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v51 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v51) err = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "telegram_callback_messages")
	assertSQLiteIndex(t, store.db, "idx_telegram_callback_messages_thread")

	thread, _, err := store.CreateTelegramThreadForUpdate(1001, 2002, 301, 401, "callback thread", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateTelegramThreadForUpdate() err = %v", err)
	}
	if err := store.RecordTelegramCallbackMessageThread(1001, 9001, thread.ThreadID, "memory", time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramCallbackMessageThread() err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9001); err != nil || !ok || got != thread.ThreadID {
		t.Fatalf("TelegramThreadIDForReplyMessage(callback) = %d ok=%v err=%v, want thread %d", got, ok, err, thread.ThreadID)
	}
}

func TestMigratesSchemaV53ToV54BackfillsTelegramThreadSessions(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v53.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v53 db: %v", err)
	}
	now := time.Date(2026, 5, 18, 2, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for _, stmt := range []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (53)`,
		`CREATE TABLE sessions (
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
			working_objective_json TEXT NOT NULL DEFAULT '{}',
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
		`CREATE TABLE outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE telegram_threads (
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open', 'closed')),
			created_by_sender_id INTEGER NOT NULL DEFAULT 0,
			created_from_update_id INTEGER NOT NULL DEFAULT 0,
			created_message_id INTEGER NOT NULL DEFAULT 0,
			created_text TEXT NOT NULL DEFAULT '',
			display_slot INTEGER NOT NULL DEFAULT 0,
			archived_display_name TEXT NOT NULL DEFAULT '',
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			closed_at TEXT,
			absorb_summary TEXT NOT NULL DEFAULT '',
			absorbed_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, thread_id)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v53 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO telegram_threads(
			chat_id, thread_id, display_slot, status, created_by_sender_id, created_from_update_id,
			created_message_id, created_text, last_activity_at, created_at, updated_at
		) VALUES (1001, 7, 7, 'open', 2002, 385535816, 9253, '', ?, ?, ?)
	`, now, now, now); err != nil {
		t.Fatalf("insert v53 thread fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v53 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v53) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)

	key := SessionKey{ChatID: 1001, Scope: TelegramThreadScopeRef(1001, 7)}
	sessionID := SessionIDForKey(key)
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(1) FROM sessions WHERE session_id = ?`, sessionID).Scan(&count); err != nil {
		t.Fatalf("query migrated thread session: %v", err)
	}
	if count != 1 {
		t.Fatalf("thread session count = %d, want migrated session row", count)
	}
	if err := store.RecordTelegramThreadMessage(1001, 7, 9254, "thread_guide", "thread_guide", time.Now().UTC()); err != nil {
		t.Fatalf("RecordTelegramThreadMessage(live-style FK) err = %v", err)
	}
	if got, ok, err := store.TelegramThreadIDForReplyMessage(1001, 9254); err != nil || !ok || got != 7 {
		t.Fatalf("TelegramThreadIDForReplyMessage(guide) = %d ok=%v err=%v, want thread 7", got, ok, err)
	}
}

func TestMigratesSchemaV55ToV57TelegramThreadPromotionHandoffs(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v55.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v55 db: %v", err)
	}
	now := time.Date(2026, 5, 23, 2, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for _, stmt := range []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (55)`,
		`CREATE TABLE sessions (
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
			working_objective_json TEXT NOT NULL DEFAULT '{}',
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
		`CREATE TABLE telegram_threads (
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open', 'closed')),
			created_by_sender_id INTEGER NOT NULL DEFAULT 0,
			created_from_update_id INTEGER NOT NULL DEFAULT 0,
			created_message_id INTEGER NOT NULL DEFAULT 0,
			created_text TEXT NOT NULL DEFAULT '',
			display_slot INTEGER NOT NULL DEFAULT 0,
			archived_display_name TEXT NOT NULL DEFAULT '',
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			closed_at TEXT,
			absorb_summary TEXT NOT NULL DEFAULT '',
			absorbed_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, thread_id)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v55 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO telegram_threads(
			chat_id, thread_id, display_slot, status, created_by_sender_id, created_from_update_id,
			created_message_id, created_text, last_activity_at, created_at, updated_at
		) VALUES (1001, 7, 2, 'open', 2002, 385535816, 9253, 'promote me safely', ?, ?, ?)
	`, now, now, now); err != nil {
		t.Fatalf("insert v55 thread fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v55 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v55) err = %v", err)
	}
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "telegram_thread_promotion_handoffs")
	assertSQLiteIndex(t, store.db, "idx_telegram_thread_promotion_handoffs_thread")
	assertSQLiteIndex(t, store.db, "idx_telegram_thread_promotion_handoffs_status")
	thread, ok, err := store.TelegramThread(1001, 7)
	if err != nil || !ok {
		t.Fatalf("TelegramThread() ok=%t err=%v, want preserved v55 thread", ok, err)
	}
	if thread.DisplaySlot != 2 || thread.CreatedText != "promote me safely" || !thread.Open() {
		t.Fatalf("thread = %#v, want preserved v55 thread data", thread)
	}
	handoff, created, err := store.CreateTelegramThreadPromotionDraft(1001, 7, 2002, time.Date(2026, 5, 23, 2, 5, 0, 0, time.UTC))
	if err != nil || !created {
		t.Fatalf("CreateTelegramThreadPromotionDraft() created=%t err=%v", created, err)
	}
	if handoff.HandoffID == "" || handoff.Status != TelegramThreadPromotionStatusDraft || handoff.SourceSessionID != "telegram_thread:1001:7" {
		t.Fatalf("handoff = %#v, want usable migrated promotion handoff", handoff)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated v55 store: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen v55 migrated) err = %v", err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.db, schemaVersion)
	loaded, ok, err := reopened.TelegramThreadPromotionHandoff(handoff.HandoffID)
	if err != nil || !ok {
		t.Fatalf("TelegramThreadPromotionHandoff(reopen) ok=%t err=%v", ok, err)
	}
	if loaded.ThreadID != 7 || loaded.ChatID != 1001 || loaded.Status != TelegramThreadPromotionStatusDraft {
		t.Fatalf("reopened handoff = %#v, want preserved migrated handoff", loaded)
	}
	if thread, ok, err := reopened.TelegramThread(1001, 7); err != nil || !ok || thread.CreatedText != "promote me safely" {
		t.Fatalf("TelegramThread(reopen) = %#v ok=%t err=%v, want preserved thread", thread, ok, err)
	}
}

func TestMigratesSchemaV57ToV58ModelSlotOverrides(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v57.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v57 db: %v", err)
	}
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	for _, stmt := range []string{
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (57)`,
		`CREATE TABLE model_slot_overrides (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slot TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			previous_config_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'superseded', 'rolled_back', 'expired', 'cleared')),
			created_by TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX idx_model_slot_overrides_slot_status ON model_slot_overrides(slot, status, id DESC)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v57 fixture: %v", err)
		}
	}
	activeCreated := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	expiredAt := now.Add(-time.Hour).Format(time.RFC3339Nano)
	futureAt := now.Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO model_slot_overrides(
			id, slot, config_json, previous_config_json, status, created_by, reason, expires_at, created_at, updated_at
		) VALUES
			(1, 'governor', '{"slot":"governor","provider":"openai","model":"gpt-5.5","effort":"high","transport":"auto","service_tier":"priority"}', '{"slot":"governor","provider":"anthropic","model":"claude-sonnet-4-6","transport":"auto"}', 'active', 'test', 'preserve active', NULL, ?, ?),
			(2, 'persona', '{"slot":"persona","provider":"openai","model":"gpt-5.4","transport":"auto"}', '{}', 'expired', 'test', 'map terminal', ?, ?, ?),
			(3, 'doctor', '{"slot":"doctor","provider":"codex","model":"gpt-5.5","transport":"auto"}', '{}', 'rolled_back', 'test', 'map terminal', ?, ?, ?),
			(4, 'child_default', '{"slot":"child_default","provider":"openai","model":"gpt-5.5","transport":"auto","service_tier":"priority"}', '{}', 'active', 'test', 'drop time-bound active', ?, ?, ?)
	`, activeCreated, activeCreated, expiredAt, activeCreated, activeCreated, expiredAt, activeCreated, activeCreated, futureAt, activeCreated, activeCreated); err != nil {
		t.Fatalf("insert v57 model slot fixtures: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v57 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v57) err = %v", err)
	}
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteIndex(t, store.db, "idx_model_slot_overrides_slot_status")
	if sqliteColumnExistsInTestDB(t, store.db, "model_slot_overrides", "expires_at") {
		t.Fatalf("model_slot_overrides.expires_at exists after v58 migration")
	}

	active, ok, err := store.ActiveModelSlotOverride(core.ModelSlotGovernor)
	if err != nil || !ok {
		t.Fatalf("ActiveModelSlotOverride(governor) ok=%t err=%v, want active non-expiring row", ok, err)
	}
	if active.Config.Provider != core.ModelProviderOpenAI || active.Config.Model != "gpt-5.5" || active.Config.ServiceTier != core.ModelServiceTierPriority {
		t.Fatalf("active model slot = %#v, want preserved OpenAI fast override", active.Config)
	}

	history, err := store.ModelSlotOverrideHistory("", 10)
	if err != nil {
		t.Fatalf("ModelSlotOverrideHistory() err = %v", err)
	}
	statusByID := map[int64]string{}
	for _, record := range history {
		statusByID[record.ID] = record.Status
	}
	if statusByID[2] != "cleared" || statusByID[3] != "cleared" || statusByID[4] != "cleared" {
		t.Fatalf("migrated statuses = %#v, want expired, rolled_back, and time-bound active rows mapped to cleared", statusByID)
	}
	if _, ok, err := store.ActiveModelSlotOverride(core.ModelSlotChildDefault); err != nil || ok {
		t.Fatalf("ActiveModelSlotOverride(child_default) ok=%t err=%v, want time-bound active row cleared", ok, err)
	}
	if _, err := store.SetModelSlotOverride(ModelSlotOverrideRecord{
		Slot: core.ModelSlotPersona,
		Config: core.ModelSlotConfig{
			Slot:      core.ModelSlotPersona,
			Provider:  core.ModelProviderAnthropic,
			Model:     "claude-sonnet-4-6",
			Transport: core.ModelTransportAuto,
		},
		CreatedBy: "test",
		Reason:    "post-migration insert",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("SetModelSlotOverride() after v58 migration err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated v57 store: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen v57 migrated) err = %v", err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.db, schemaVersion)
	if _, ok, err := reopened.ActiveModelSlotOverride(core.ModelSlotGovernor); err != nil || !ok {
		t.Fatalf("ActiveModelSlotOverride(reopen) ok=%t err=%v, want active row remains non-expiring", ok, err)
	}
}

func TestMigratesSchemaV58ToV59TelegramAgentMessageLedger(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v58.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v58 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (58)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v58 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v58 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v58) err = %v", err)
	}
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "telegram_agent_messages")
	assertSQLiteIndex(t, store.db, "idx_telegram_agent_messages_agent")
	if err := store.RecordTelegramAgentMessage(1001, 7007, "ops-child", "agent_detail", time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("RecordTelegramAgentMessage() after v59 migration err = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated v58 store: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen v58 migrated) err = %v", err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.db, schemaVersion)
	agentID, ok, err := reopened.TelegramAgentIDForReplyMessage(1001, 7007)
	if err != nil || !ok || agentID != "ops-child" {
		t.Fatalf("TelegramAgentIDForReplyMessage(reopen) = %q ok=%t err=%v, want ops-child", agentID, ok, err)
	}
}

func TestMigratesSchemaV59ToV60MissionAskPrompts(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v59.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v59 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (59)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v59 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v59 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v59) err = %v", err)
	}
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteTable(t, store.db, "mission_ask_prompts")
	assertSQLiteIndex(t, store.db, "idx_mission_ask_owner_status")
	key := SessionKey{ChatID: 1001, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "1001"}}
	if _, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(MissionAskPrompt{
		Owner:             "telegram:1001",
		ChatID:            1001,
		SenderID:          1001,
		SessionID:         SessionIDForKey(key),
		Scope:             key.Scope,
		MissionID:         "mission-v60",
		Confidence:        MissionAskConfidenceHigh,
		QuestionText:      "Should this become a mission association?",
		SourceFingerprint: "v60-migration",
	}, time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)); err != nil || !allowed || reason != "" {
		t.Fatalf("CreateMissionAskPromptIfAllowed() after v60 migration allowed=%t reason=%q err=%v, want insert", allowed, reason, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated v59 store: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen v59 migrated) err = %v", err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.db, schemaVersion)
	prompts, err := reopened.MissionAskPrompts(MissionAskPromptFilter{Owner: "telegram:1001", Limit: 10})
	if err != nil {
		t.Fatalf("MissionAskPrompts(reopen) err = %v", err)
	}
	if len(prompts) != 1 || prompts[0].MissionID != "mission-v60" {
		t.Fatalf("prompts after reopen = %#v, want migrated prompt", prompts)
	}
}

func TestMigratesSchemaV60ToV61ApprovalWindowOfferOpenedColumns(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v60.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v60 db: %v", err)
	}
	createdAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	expiresAt := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for _, stmt := range []string{
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (60)`,
		`CREATE TABLE approval_window_offers (
			offer_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			admin_user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			source_kind TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			source_decision_kind TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL,
			used_at TEXT,
			closed_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v60 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO approval_window_offers(
			offer_id, chat_id, admin_user_id, session_id, scope_kind, scope_id, durable_agent_id,
			source_kind, source_id, source_decision_kind, created_at, expires_at, used_at, closed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "offer-v60", int64(9901), int64(1001), "telegram_dm:9901", string(ScopeKindTelegramDM), "9901", "", ApprovalWindowOfferSourceDecision, "decision-v60", "proposal_approval", createdAt, expiresAt, nil, nil, createdAt); err != nil {
		t.Fatalf("insert v60 approval window offer: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v60 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v60) err = %v", err)
	}
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteColumn(t, store.db, "approval_window_offers", "opened_lease_id")
	assertSQLiteColumn(t, store.db, "approval_window_offers", "opened_override_id")
	offer, ok, err := store.ApprovalWindowOffer("offer-v60")
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer(v60 migrated) ok=%t err=%v", ok, err)
	}
	if offer.OpenedLeaseID != "" || offer.OpenedOverrideID != "" || offer.ID != "offer-v60" || offer.SourceID != "decision-v60" {
		t.Fatalf("migrated offer = %#v, want readable row with empty opened IDs", offer)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close migrated v60 store: %v", err)
	}

	reopened, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(reopen v60 migrated) err = %v", err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.db, schemaVersion)
	reopenedOffer, ok, err := reopened.ApprovalWindowOffer("offer-v60")
	if err != nil || !ok {
		t.Fatalf("ApprovalWindowOffer(reopen v60 migrated) ok=%t err=%v", ok, err)
	}
	if reopenedOffer.OpenedLeaseID != "" || reopenedOffer.OpenedOverrideID != "" || reopenedOffer.ID != "offer-v60" {
		t.Fatalf("reopened migrated offer = %#v, want empty opened IDs preserved", reopenedOffer)
	}
}

func TestMigratesSchemaV63ToV64TurnRunAccounting(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v63.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v63 db: %v", err)
	}
	sessionID := SessionIDFromParts(99364, 1001, ScopeRef{})
	for _, stmt := range []string{
		`CREATE TABLE schema_version(version INTEGER NOT NULL, applied_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO schema_version(version) VALUES (63)`,
		`CREATE TABLE turn_runs (
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
		`CREATE TABLE messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool')),
			content TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v63 fixture: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO turn_runs(session_id, chat_id, user_id, kind, status, request_text, started_at, last_activity_at)
		VALUES (?, 99364, 1001, 'interactive', 'completed', 'old accounted turn', ?, ?)
	`, sessionID, time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), time.Date(2026, 6, 1, 1, 1, 0, 0, time.UTC).Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert v63 turn run: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v63 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v63) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	for _, column := range []string{
		"turn_index",
		"total_tool_chars_in",
		"total_assistant_chars_out",
		"provider_input_tokens",
		"provider_output_tokens",
		"provider_cache_read_tokens",
		"provider_cache_write_tokens",
	} {
		assertSQLiteColumn(t, store.db, "turn_runs", column)
	}
	run, err := store.TurnRun(1)
	if err != nil {
		t.Fatalf("TurnRun() after v64 migration err = %v", err)
	}
	if run.RequestText != "old accounted turn" || run.TotalToolCharsIn != 0 || run.ProviderInputTokens != 0 {
		t.Fatalf("run = %#v, want preserved row with zero accounting defaults", run)
	}
	assertSQLiteColumn(t, store.db, "reentry_recommendations", "terminal_fingerprint")
}

func TestMigratesSchemaV67ToV68MediaPickerSourceIngressColumns(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v67.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v67 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (67)`,
		`CREATE TABLE telegram_media_thread_pickers (
			chat_id INTEGER NOT NULL,
			picker_message_id INTEGER NOT NULL,
			source_message_id INTEGER NOT NULL DEFAULT 0,
			inbound_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'routed', 'cleared')),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, picker_message_id)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v67 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v67 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v67) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteColumn(t, store.db, "telegram_media_thread_pickers", "source_ingress_surface")
	assertSQLiteColumn(t, store.db, "telegram_media_thread_pickers", "source_ingress_update_id")
}

func TestMigratesSchemaV69ToV70EffectAttempts(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions-v69.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open v69 db: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (69)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create v69 fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v69 db: %v", err)
	}

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore(v69) err = %v", err)
	}
	defer store.Close()
	assertSchemaVersion(t, store.db, schemaVersion)
	assertSQLiteColumn(t, store.db, "effect_attempts", "attempt_id")
	assertSQLiteColumn(t, store.db, "effect_attempts", "subject_json")
	assertSQLiteColumn(t, store.db, "effect_attempts", "evidence_refs_json")
}

func sqliteColumnExistsInTestDB(t *testing.T, db *sql.DB, tableName string, columnName string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", tableName, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typeName string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info(%s): %v", tableName, err)
		}
		if name == columnName {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info(%s): %v", tableName, err)
	}
	return false
}
