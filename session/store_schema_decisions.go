//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

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
			opened_lease_id TEXT NOT NULL DEFAULT '',
			opened_override_id TEXT NOT NULL DEFAULT '',
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

func ensureApprovalWindowOfferOpenedColumns(tx *sql.Tx) error {
	for _, column := range []schemaColumnMigration{
		{table: "approval_window_offers", column: "opened_lease_id", statement: `ALTER TABLE approval_window_offers ADD COLUMN opened_lease_id TEXT NOT NULL DEFAULT ''`},
		{table: "approval_window_offers", column: "opened_override_id", statement: `ALTER TABLE approval_window_offers ADD COLUMN opened_override_id TEXT NOT NULL DEFAULT ''`},
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
	return nil
}
