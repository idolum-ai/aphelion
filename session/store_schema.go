//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
)

const schemaVersion43 = 43

func existingUserTableCount(tx *sql.Tx) (int, error) {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
			AND name NOT LIKE 'sqlite_%'
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("query existing sqlite tables: %w", err)
	}
	return count, nil
}

func validateCurrentSchemaVersion(tx *sql.Tx, existingTables int) (int, error) {
	currentVersion, err := currentSchemaVersion(tx)
	if err != nil {
		return 0, err
	}
	if currentVersion == 0 {
		if existingTables == 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("unsupported unversioned database schema; reinstall from a clean current state")
	}
	if currentVersion < schemaVersion {
		if currentVersion == schemaVersion43 {
			return currentVersion, nil
		}
		return 0, fmt.Errorf("unsupported database schema version %d (current schema version is %d); reinstall from a clean current state", currentVersion, schemaVersion)
	}
	if currentVersion > schemaVersion {
		return 0, fmt.Errorf("unsupported database schema version %d (binary schema version is %d); install a matching or newer binary", currentVersion, schemaVersion)
	}
	return currentVersion, nil
}

func migrateCurrentSchemaVersion(tx *sql.Tx, currentVersion int) (int, error) {
	switch currentVersion {
	case schemaVersion43:
		if err := migrateSchemaV43ToV44(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion, err)
		}
		return schemaVersion, nil
	default:
		return currentVersion, nil
	}
}

func migrateSchemaV43ToV44(tx *sql.Tx) error {
	for _, column := range []schemaColumnMigration{
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_stable_node_id",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_stable_node_id TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_node_name",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_node_name TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_computed_name",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_computed_name TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_login_name",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_login_name TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_tags_json",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_tags_json TEXT NOT NULL DEFAULT '[]'`,
		},
		{
			table:     "durable_agent_control_receipts",
			column:    "signature",
			statement: `ALTER TABLE durable_agent_control_receipts ADD COLUMN signature TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_control_receipts",
			column:    "response_status",
			statement: `ALTER TABLE durable_agent_control_receipts ADD COLUMN response_status INTEGER NOT NULL DEFAULT 0`,
		},
		{
			table:     "durable_agent_control_receipts",
			column:    "response_json",
			statement: `ALTER TABLE durable_agent_control_receipts ADD COLUMN response_json TEXT NOT NULL DEFAULT ''`,
		},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	return nil
}

type schemaColumnMigration struct {
	table     string
	column    string
	statement string
}

func addSchemaColumnIfMissing(tx *sql.Tx, migration schemaColumnMigration) error {
	exists, err := schemaColumnExists(tx, migration.table, migration.column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(migration.statement); err != nil {
		return fmt.Errorf("migrate schema v43 to v44 add %s.%s: %w", migration.table, migration.column, err)
	}
	return nil
}

func schemaColumnExists(tx *sql.Tx, tableName string, columnName string) (bool, error) {
	tableName = strings.TrimSpace(tableName)
	columnName = strings.TrimSpace(columnName)
	if tableName == "" || columnName == "" {
		return false, fmt.Errorf("schema column lookup requires table and column")
	}
	var count int
	query := fmt.Sprintf("SELECT COUNT(1) FROM pragma_table_info(%s) WHERE name = ?", sqliteStringLiteral(tableName))
	if err := tx.QueryRow(query, columnName).Scan(&count); err != nil {
		return false, fmt.Errorf("query schema column %s.%s: %w", tableName, columnName, err)
	}
	return count > 0, nil
}

func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func recordCurrentSchemaVersion(tx *sql.Tx, currentVersion int) error {
	if currentVersion == schemaVersion {
		return nil
	}
	if currentVersion != 0 {
		return fmt.Errorf("unsupported database schema version %d (current schema version is %d); reinstall from a clean current state", currentVersion, schemaVersion)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion); err != nil {
		return fmt.Errorf("insert schema version %d: %w", schemaVersion, err)
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

func ensureTailnetSurfaceTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS tailnet_surfaces (
			surface_id TEXT PRIMARY KEY,
			owner_kind TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			surface_kind TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			hostname TEXT NOT NULL DEFAULT '',
			tailnet_name TEXT NOT NULL DEFAULT '',
			listen_addr TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'declared' CHECK(status IN ('declared', 'active', 'degraded', 'revoked')),
			last_error TEXT NOT NULL DEFAULT '',
			declared_at TEXT NOT NULL DEFAULT (datetime('now')),
			activated_at TEXT,
			last_observed_at TEXT,
			revoked_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(owner_kind, owner_id, surface_kind, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_surfaces_status ON tailnet_surfaces(status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_surfaces_owner ON tailnet_surfaces(owner_kind, owner_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS tailnet_surface_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			surface_id TEXT NOT NULL,
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_surface_events_surface ON tailnet_surface_events(surface_id, id DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure tailnet surface tables: %w", err)
		}
	}
	return nil
}

func ensureTailnetGrantBindingTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS tailnet_grant_bindings (
			binding_id TEXT PRIMARY KEY,
			grant_id TEXT NOT NULL DEFAULT '',
			surface_id TEXT NOT NULL DEFAULT '',
			granted_to TEXT NOT NULL DEFAULT '',
			capability_kind TEXT NOT NULL DEFAULT '',
			target_resource TEXT NOT NULL DEFAULT '',
			desired_policy_json TEXT NOT NULL DEFAULT '{}',
			applied_policy_hash TEXT NOT NULL DEFAULT '',
			observed_policy_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed', 'applied', 'drifted', 'revoked', 'failed')),
			drift_reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			applied_at TEXT,
			revoked_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_grant ON tailnet_grant_bindings(grant_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_surface ON tailnet_grant_bindings(surface_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_status ON tailnet_grant_bindings(status, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_active_pair ON tailnet_grant_bindings(grant_id, surface_id) WHERE status != 'revoked'`,
		`CREATE TABLE IF NOT EXISTS tailnet_grant_binding_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			binding_id TEXT NOT NULL,
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_binding_events_binding ON tailnet_grant_binding_events(binding_id, id DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure tailnet grant binding tables: %w", err)
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
		`CREATE INDEX IF NOT EXISTS idx_registered_tools_state ON registered_tools(registered, updated_at, tool_name)`,
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
