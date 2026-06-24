//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureDurableAgentWakeClaimTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS durable_agent_wake_claims (
			claim_id TEXT PRIMARY KEY,
			lease_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			message_batch_hash TEXT NOT NULL DEFAULT '',
			message_ids_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_durable_agent_wake_claims_lease ON durable_agent_wake_claims(lease_id)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_agent_wake_claims_agent ON durable_agent_wake_claims(agent_id, created_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure durable agent wake claim tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV81ToV82(tx *sql.Tx) error {
	if err := ensureExecutionRunAuthorityTables(tx); err != nil {
		return err
	}
	return ensureDurableAgentWakeClaimTables(tx)
}
