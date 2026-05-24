//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"time"
)

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

func migrateSchemaV55ToV56(tx *sql.Tx) error {
	if err := ensureTelegramThreadPromotionHandoffTables(tx); err != nil {
		return fmt.Errorf("migrate schema v55 to v56 ensure telegram thread promotion handoffs: %w", err)
	}
	return nil
}
