//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureJudgmentUseTables(tx *sql.Tx) error {
	for _, stmt := range judgmentUseSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure judgment use tables: %w", err)
		}
	}
	return nil
}

func judgmentUseSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS judgment_uses (
			use_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			operation_id TEXT NOT NULL DEFAULT '',
			phase_id TEXT NOT NULL DEFAULT '',
			lease_id TEXT NOT NULL DEFAULT '',
			proposal_id TEXT NOT NULL DEFAULT '',
			consumer_id TEXT NOT NULL DEFAULT '',
			consequence TEXT NOT NULL DEFAULT '',
			judgment_refs_json TEXT NOT NULL DEFAULT '[]',
			dependency_refs_json TEXT NOT NULL DEFAULT '[]',
			policy_ref TEXT NOT NULL DEFAULT '',
			dependency_snapshot TEXT NOT NULL DEFAULT '',
			result_ref TEXT NOT NULL DEFAULT '',
			irreversible INTEGER NOT NULL DEFAULT 0,
			qualification_status TEXT NOT NULL DEFAULT 'qualified',
			reconciliation_status TEXT NOT NULL DEFAULT 'not_required',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_judgment_uses_session_updated ON judgment_uses(session_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgment_uses_result ON judgment_uses(result_ref, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgment_uses_consumer ON judgment_uses(consumer_id, consequence, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgment_uses_reconciliation ON judgment_uses(reconciliation_status, updated_at DESC)`,
	}
}

func migrateSchemaV72ToV73(tx *sql.Tx) error {
	return ensureJudgmentUseTables(tx)
}
