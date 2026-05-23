//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const schemaVersion43 = 43
const schemaVersion44 = 44
const schemaVersion45 = 45
const schemaVersion46 = 46
const schemaVersion47 = 47
const schemaVersion48 = 48
const schemaVersion49 = 49
const schemaVersion50 = 50
const schemaVersion51 = 51
const schemaVersion52 = 52
const schemaVersion53 = 53
const schemaVersion54 = 54

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
		if currentVersion == schemaVersion43 || currentVersion == schemaVersion44 || currentVersion == schemaVersion45 || currentVersion == schemaVersion46 || currentVersion == schemaVersion47 || currentVersion == schemaVersion48 || currentVersion == schemaVersion49 || currentVersion == schemaVersion50 || currentVersion == schemaVersion51 || currentVersion == schemaVersion52 || currentVersion == schemaVersion53 || currentVersion == schemaVersion54 {
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
	version := currentVersion
	if version == schemaVersion43 {
		if err := migrateSchemaV43ToV44(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion44); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion44, err)
		}
		version = schemaVersion44
	}
	if version == schemaVersion44 {
		if err := migrateSchemaV44ToV45(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion45); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion45, err)
		}
		version = schemaVersion45
	}
	if version == schemaVersion45 {
		if err := migrateSchemaV45ToV46(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion46); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion46, err)
		}
		version = schemaVersion46
	}
	if version == schemaVersion46 {
		if err := migrateSchemaV46ToV47(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion47); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion47, err)
		}
		version = schemaVersion47
	}
	if version == schemaVersion47 {
		if err := migrateSchemaV47ToV48(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion48); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion48, err)
		}
		version = schemaVersion48
	}
	if version == schemaVersion48 {
		if err := migrateSchemaV48ToV49(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion49); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion49, err)
		}
		version = schemaVersion49
	}
	if version == schemaVersion49 {
		if err := migrateSchemaV49ToV50(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion50); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion50, err)
		}
		version = schemaVersion50
	}
	if version == schemaVersion50 {
		if err := migrateSchemaV50ToV51(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion51); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion51, err)
		}
		version = schemaVersion51
	}
	if version == schemaVersion51 {
		if err := migrateSchemaV51ToV52(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion52); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion52, err)
		}
		version = schemaVersion52
	}
	if version == schemaVersion52 {
		if err := migrateSchemaV52ToV53(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion53); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion53, err)
		}
		version = schemaVersion53
	}
	if version == schemaVersion53 {
		if err := migrateSchemaV53ToV54(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion54); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion54, err)
		}
		version = schemaVersion54
	}
	if version == schemaVersion54 {
		if err := migrateSchemaV54ToV55(tx); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, schemaVersion); err != nil {
			return 0, fmt.Errorf("insert schema version %d: %w", schemaVersion, err)
		}
		version = schemaVersion
	}
	return version, nil
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

func migrateSchemaV44ToV45(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS operator_autonomy_overrides (
			override_id TEXT PRIMARY KEY,
			admin_user_id INTEGER NOT NULL DEFAULT 0,
			chat_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'leased',
			scope TEXT NOT NULL DEFAULT 'all',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_operator_autonomy_overrides_chat_active ON operator_autonomy_overrides(chat_id, mode, expires_at DESC, revoked_at, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_operator_autonomy_overrides_admin_active ON operator_autonomy_overrides(admin_user_id, mode, expires_at DESC, revoked_at, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v44 to v45 ensure operator autonomy overrides: %w", err)
		}
	}
	hasAutoApprovals, err := schemaTableExists(tx, "operator_auto_approvals")
	if err != nil {
		return err
	}
	if !hasAutoApprovals {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO operator_autonomy_overrides(
			override_id, admin_user_id, chat_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		)
		SELECT 'mode-' || lease_id, admin_user_id, chat_id, 'leased', scope, reason,
			created_at, expires_at, NULL, updated_at
		FROM operator_auto_approvals
		WHERE revoked_at IS NULL
			AND expires_at > ?
			AND (max_uses <= 0 OR used_count < max_uses)
	`, now); err != nil {
		return fmt.Errorf("migrate schema v44 to v45 copy active auto mode gates: %w", err)
	}
	return nil
}

func migrateSchemaV45ToV46(tx *sql.Tx) error {
	for _, stmt := range telegramIngressSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v45 to v46 ensure telegram ingress ledger: %w", err)
		}
	}
	return nil
}

func migrateSchemaV46ToV47(tx *sql.Tx) error {
	for _, stmt := range telegramIngressSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v46 to v47 ensure telegram ingress accepted-update ledger: %w", err)
		}
	}
	return nil
}

func migrateSchemaV47ToV48(tx *sql.Tx) error {
	if err := ensureTurnProgressViewTables(tx); err != nil {
		return fmt.Errorf("migrate schema v47 to v48 ensure turn progress views: %w", err)
	}
	return nil
}

func migrateSchemaV48ToV49(tx *sql.Tx) error {
	if err := ensureTelegramThreadTables(tx); err != nil {
		return fmt.Errorf("migrate schema v48 to v49 ensure telegram thread tables: %w", err)
	}
	return nil
}

func migrateSchemaV49ToV50(tx *sql.Tx) error {
	if err := ensureTelegramReplyRoutingIndexes(tx); err != nil {
		return fmt.Errorf("migrate schema v49 to v50 ensure telegram reply routing indexes: %w", err)
	}
	return nil
}

func migrateSchemaV50ToV51(tx *sql.Tx) error {
	if err := ensureScopedDecisionColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v50 to v51 ensure scoped pending decisions: %w", err)
	}
	return nil
}

func migrateSchemaV51ToV52(tx *sql.Tx) error {
	if err := ensureTelegramCallbackMessageTables(tx); err != nil {
		return fmt.Errorf("migrate schema v51 to v52 ensure telegram callback message ledger: %w", err)
	}
	return nil
}

func migrateSchemaV52ToV53(tx *sql.Tx) error {
	if err := ensureOperatorAutoScopeColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v52 to v53 ensure auto scope columns: %w", err)
	}
	if err := ensureTelegramThreadTables(tx); err != nil {
		return fmt.Errorf("migrate schema v52 to v53 ensure telegram thread tables: %w", err)
	}
	return nil
}

func migrateSchemaV53ToV54(tx *sql.Tx) error {
	if err := ensureTelegramThreadSessions(tx); err != nil {
		return fmt.Errorf("migrate schema v53 to v54 ensure telegram thread sessions: %w", err)
	}
	return nil
}

func migrateSchemaV54ToV55(tx *sql.Tx) error {
	if err := ensureApprovalWindowOfferTables(tx); err != nil {
		return fmt.Errorf("migrate schema v54 to v55 ensure approval window offers: %w", err)
	}
	return nil
}

func ensureApprovalWindowOfferTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS approval_window_offers (
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
		`CREATE INDEX IF NOT EXISTS idx_approval_window_offers_source_active ON approval_window_offers(chat_id, source_kind, source_id, expires_at DESC, closed_at, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_approval_window_offers_scope_active ON approval_window_offers(chat_id, scope_kind, scope_id, expires_at DESC, closed_at, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureOperatorAutoScopeColumns(tx *sql.Tx) error {
	for _, column := range []schemaColumnMigration{
		{table: "operator_auto_approvals", column: "scope_kind", statement: `ALTER TABLE operator_auto_approvals ADD COLUMN scope_kind TEXT NOT NULL DEFAULT ''`},
		{table: "operator_auto_approvals", column: "scope_id", statement: `ALTER TABLE operator_auto_approvals ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`},
		{table: "operator_autonomy_overrides", column: "scope_kind", statement: `ALTER TABLE operator_autonomy_overrides ADD COLUMN scope_kind TEXT NOT NULL DEFAULT ''`},
		{table: "operator_autonomy_overrides", column: "scope_id", statement: `ALTER TABLE operator_autonomy_overrides ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`},
	} {
		exists, err := schemaTableExists(tx, column.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	for _, backfill := range []struct{ table, stmt string }{
		{table: "operator_auto_approvals", stmt: `UPDATE operator_auto_approvals
		SET scope_kind = 'telegram_dm', scope_id = CAST(chat_id AS TEXT)
		WHERE chat_id != 0 AND (TRIM(scope_kind) = '' OR TRIM(scope_id) = '')`},
		{table: "operator_autonomy_overrides", stmt: `UPDATE operator_autonomy_overrides
		SET scope_kind = 'telegram_dm', scope_id = CAST(chat_id AS TEXT)
		WHERE chat_id != 0 AND (TRIM(scope_kind) = '' OR TRIM(scope_id) = '')`},
	} {
		exists, err := schemaTableExists(tx, backfill.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(backfill.stmt); err != nil {
			return fmt.Errorf("backfill operator auto scope columns: %w", err)
		}
	}
	for _, index := range []struct{ table, stmt string }{
		{table: "operator_auto_approvals", stmt: `CREATE INDEX IF NOT EXISTS idx_operator_auto_approvals_scope_active ON operator_auto_approvals(chat_id, scope_kind, scope_id, expires_at DESC, revoked_at, updated_at DESC)`},
		{table: "operator_autonomy_overrides", stmt: `CREATE INDEX IF NOT EXISTS idx_operator_autonomy_overrides_scope_active ON operator_autonomy_overrides(chat_id, scope_kind, scope_id, mode, expires_at DESC, revoked_at, updated_at DESC)`},
	} {
		exists, err := schemaTableExists(tx, index.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(index.stmt); err != nil {
			return fmt.Errorf("ensure operator auto scope index: %w", err)
		}
	}
	return nil
}

func ensureScopedDecisionColumns(tx *sql.Tx) error {
	for _, tableColumns := range []struct {
		table   string
		columns []schemaColumnMigration
	}{
		{table: "pending_decisions", columns: []schemaColumnMigration{
			{table: "pending_decisions", column: "session_id", statement: `ALTER TABLE pending_decisions ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_decisions", column: "scope_kind", statement: `ALTER TABLE pending_decisions ADD COLUMN scope_kind TEXT NOT NULL DEFAULT ''`},
			{table: "pending_decisions", column: "scope_id", statement: `ALTER TABLE pending_decisions ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_decisions", column: "durable_agent_id", statement: `ALTER TABLE pending_decisions ADD COLUMN durable_agent_id TEXT NOT NULL DEFAULT ''`},
		}},
		{table: "pending_artifact_retention", columns: []schemaColumnMigration{
			{table: "pending_artifact_retention", column: "session_id", statement: `ALTER TABLE pending_artifact_retention ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_artifact_retention", column: "scope_kind", statement: `ALTER TABLE pending_artifact_retention ADD COLUMN scope_kind TEXT NOT NULL DEFAULT ''`},
			{table: "pending_artifact_retention", column: "scope_id", statement: `ALTER TABLE pending_artifact_retention ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_artifact_retention", column: "durable_agent_id", statement: `ALTER TABLE pending_artifact_retention ADD COLUMN durable_agent_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_artifact_retention", column: "message_id", statement: `ALTER TABLE pending_artifact_retention ADD COLUMN message_id INTEGER NOT NULL DEFAULT 0`},
		}},
		{table: "pending_busy_decisions", columns: []schemaColumnMigration{
			{table: "pending_busy_decisions", column: "session_id", statement: `ALTER TABLE pending_busy_decisions ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_busy_decisions", column: "scope_kind", statement: `ALTER TABLE pending_busy_decisions ADD COLUMN scope_kind TEXT NOT NULL DEFAULT ''`},
			{table: "pending_busy_decisions", column: "scope_id", statement: `ALTER TABLE pending_busy_decisions ADD COLUMN scope_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_busy_decisions", column: "durable_agent_id", statement: `ALTER TABLE pending_busy_decisions ADD COLUMN durable_agent_id TEXT NOT NULL DEFAULT ''`},
			{table: "pending_busy_decisions", column: "message_id", statement: `ALTER TABLE pending_busy_decisions ADD COLUMN message_id INTEGER NOT NULL DEFAULT 0`},
		}},
	} {
		exists, err := schemaTableExists(tx, tableColumns.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		for _, column := range tableColumns.columns {
			if err := addSchemaColumnIfMissing(tx, column); err != nil {
				return err
			}
		}
	}
	for _, index := range []struct {
		table string
		stmt  string
	}{
		{table: "pending_decisions", stmt: `CREATE INDEX IF NOT EXISTS idx_pending_decisions_session_seq ON pending_decisions(session_id, decision_seq DESC)`},
		{table: "pending_artifact_retention", stmt: `CREATE INDEX IF NOT EXISTS idx_pending_artifact_retention_message ON pending_artifact_retention(chat_id, sender_id, message_id)`},
		{table: "pending_busy_decisions", stmt: `CREATE INDEX IF NOT EXISTS idx_pending_busy_decisions_session ON pending_busy_decisions(session_id, updated_at DESC)`},
	} {
		exists, err := schemaTableExists(tx, index.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(index.stmt); err != nil {
			return fmt.Errorf("ensure scoped decision index: %w", err)
		}
	}
	return nil
}

func schemaTableExists(tx *sql.Tx, tableName string) (bool, error) {
	tableName = strings.TrimSpace(tableName)
	if tableName == "" {
		return false, fmt.Errorf("schema table lookup requires table")
	}
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, tableName).Scan(&count); err != nil {
		return false, fmt.Errorf("query schema table %s: %w", tableName, err)
	}
	return count > 0, nil
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
		`CREATE INDEX IF NOT EXISTS idx_turn_progress_views_message ON turn_progress_views(message_id, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure session identity index: %w", err)
		}
	}
	if err := ensureTelegramReplyRoutingIndexes(tx); err != nil {
		return err
	}
	return nil
}

func ensureTelegramReplyRoutingIndexes(tx *sql.Tx) error {
	for _, migration := range []struct {
		table string
		stmt  string
	}{
		{table: "outbound_messages", stmt: `CREATE INDEX IF NOT EXISTS idx_outbound_chat_message ON outbound_messages(chat_id, telegram_msg_id)`},
		{table: "telegram_ingress_updates", stmt: `CREATE INDEX IF NOT EXISTS idx_telegram_ingress_updates_message ON telegram_ingress_updates(chat_id, message_id)`},
		{table: "telegram_threads", stmt: `CREATE INDEX IF NOT EXISTS idx_telegram_threads_created_message ON telegram_threads(chat_id, created_message_id) WHERE created_message_id > 0`},
		{table: "pending_decisions", stmt: `CREATE INDEX IF NOT EXISTS idx_pending_decisions_delivery_message ON pending_decisions(chat_id, delivery_message_id) WHERE delivery_message_id > 0`},
	} {
		exists, err := schemaTableExists(tx, migration.table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(migration.stmt); err != nil {
			return fmt.Errorf("ensure telegram reply routing index: %w", err)
		}
	}
	return nil
}

func ensureTurnProgressViewTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS turn_progress_views (
			run_id INTEGER PRIMARY KEY,
			message_id INTEGER NOT NULL DEFAULT 0,
			selected_view TEXT NOT NULL DEFAULT 'summary' CHECK(selected_view IN ('summary', 'details')),
			summary_text TEXT NOT NULL DEFAULT '',
			details_text TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (run_id) REFERENCES turn_runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_progress_views_message ON turn_progress_views(message_id, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure turn progress view table: %w", err)
		}
	}
	return nil
}

func ensureTelegramThreadTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_threads (
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
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_telegram_threads_created_update ON telegram_threads(chat_id, created_from_update_id) WHERE created_from_update_id > 0`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_threads_created_message ON telegram_threads(chat_id, created_message_id) WHERE created_message_id > 0`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_threads_chat_status ON telegram_threads(chat_id, status, updated_at DESC, thread_id DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram thread table: %w", err)
		}
	}
	if err := ensureTelegramThreadDisplayColumns(tx); err != nil {
		return err
	}
	return nil
}

func ensureTelegramThreadDisplayColumns(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "telegram_threads")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, column := range []schemaColumnMigration{
		{table: "telegram_threads", column: "display_slot", statement: `ALTER TABLE telegram_threads ADD COLUMN display_slot INTEGER NOT NULL DEFAULT 0`},
		{table: "telegram_threads", column: "archived_display_name", statement: `ALTER TABLE telegram_threads ADD COLUMN archived_display_name TEXT NOT NULL DEFAULT ''`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		`UPDATE telegram_threads
		SET display_slot = (
			SELECT COUNT(*)
			FROM telegram_threads AS open_threads
			WHERE open_threads.chat_id = telegram_threads.chat_id
				AND open_threads.status = 'open'
				AND open_threads.thread_id <= telegram_threads.thread_id
		)
		WHERE status = 'open' AND display_slot <= 0`,
		`UPDATE telegram_threads
		SET display_slot = 0,
			archived_display_name = CASE
				WHEN TRIM(archived_display_name) != '' THEN archived_display_name
				ELSE thread_id || '-' || SUBSTR(COALESCE(NULLIF(absorbed_at, ''), NULLIF(closed_at, ''), NULLIF(updated_at, ''), NULLIF(created_at, ''), datetime('now')), 1, 10)
			END
		WHERE status != 'open'`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("backfill telegram thread display columns: %w", err)
		}
	}
	return nil
}

func ensureTelegramCallbackMessageTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS telegram_callback_messages (
			chat_id INTEGER NOT NULL,
			message_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL DEFAULT 0,
			surface TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(chat_id, message_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telegram_callback_messages_thread ON telegram_callback_messages(chat_id, thread_id, updated_at DESC) WHERE thread_id > 0`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram callback message table: %w", err)
		}
	}
	return nil
}
