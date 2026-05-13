//go:build linux

package session

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestMigratesSchemaV43ToV44(t *testing.T) {
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

func createSchemaV43Fixture(t *testing.T, db *sql.DB) {
	t.Helper()
	ddl := []string{
		`CREATE TABLE schema_version (
			version INTEGER NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`INSERT INTO schema_version(version) VALUES (43)`,
		`CREATE TABLE durable_agents (
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
		`CREATE TABLE durable_agent_remote_enrollments (
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
		`CREATE TABLE durable_agent_control_receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			message_kind TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			received_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(agent_id, message_id),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec v43 fixture stmt: %v", err)
		}
	}

	createdAt := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	updatedAt := time.Date(2026, 5, 12, 10, 5, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO durable_agents(
			agent_id, parent_agent_id, parent_scope_kind, parent_scope_id, review_target_chat_id,
			channel_kind, live_policy_json, channel_config_json, bootstrap_ceiling_json, bootstrap_provider_json, control_plane_secret,
			policy_version, policy_hash, policy_issued_at, local_storage_roots_json, network_policy, wakeup_mode,
			secret_scopes_json, allowed_telegram_user_ids_json, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"family-group", "house", "telegram_dm", "1001", int64(1001),
		"telegram_group",
		`{"charter":"v43 durable child","capability_envelope":["group_reply"],"outbound_mode":"read_only","drift_policy":"admin_review"}`,
		`{}`,
		`{"capability_envelope":["group_reply"],"allowed_outbound_modes":["read_only"]}`,
		`{"backend":"native","native_provider":"openrouter","api_key":"sk-or-v43","model":"openrouter/test"}`,
		"secret-v43", int64(7), "policy-hash-v43", createdAt, `["/tmp/family-group"]`, "restricted", "remote_control_plane",
		`["telegram_bot"]`, `[1001]`, "active", createdAt, updatedAt,
	); err != nil {
		t.Fatalf("insert v43 durable agent: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO durable_agent_remote_enrollments(
			agent_id, parent_control_url, key_fingerprint, protocol_version, status, last_sequence, enrolled_at, last_seen_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"family-group", "https://parent.example.test", "fingerprint-v43", "v1", "active", int64(41), createdAt, updatedAt, updatedAt,
	); err != nil {
		t.Fatalf("insert v43 enrollment: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO durable_agent_control_receipts(agent_id, message_id, message_kind, sequence, received_at)
		VALUES (?, ?, ?, ?, ?)
	`, "family-group", "msg-v43-1", "policy_poll", int64(39), updatedAt); err != nil {
		t.Fatalf("insert v43 receipt: %v", err)
	}
}

func assertSchemaVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&got); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if got != want {
		t.Fatalf("schema version = %d, want %d", got, want)
	}
}

func assertSQLiteColumn(t *testing.T, db *sql.DB, tableName string, columnName string) {
	t.Helper()
	var count int
	query := "SELECT COUNT(1) FROM pragma_table_info(" + sqliteStringLiteral(tableName) + ") WHERE name = ?"
	if err := db.QueryRow(query, columnName).Scan(&count); err != nil {
		t.Fatalf("query column %s.%s: %v", tableName, columnName, err)
	}
	if count != 1 {
		t.Fatalf("column %s.%s count = %d, want 1", tableName, columnName, count)
	}
}
