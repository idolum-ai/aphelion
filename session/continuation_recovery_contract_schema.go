//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureContinuationRecoveryContractTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS continuation_recovery_contracts (
			contract_id TEXT PRIMARY KEY,
			contract_version TEXT NOT NULL DEFAULT '',
			request_instance_id TEXT NOT NULL DEFAULT '',
			contract_hash TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			subject_kind TEXT NOT NULL DEFAULT '',
			subject_ref TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			principal TEXT NOT NULL DEFAULT '',
			lease_class TEXT NOT NULL DEFAULT '',
			allowed_actions_json TEXT NOT NULL DEFAULT '[]',
			constraints_json TEXT NOT NULL DEFAULT '{}',
			tool TEXT NOT NULL DEFAULT '',
			tool_action TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			resource TEXT NOT NULL DEFAULT '',
			grant_id TEXT NOT NULL DEFAULT '',
			grant_target_resource TEXT NOT NULL DEFAULT '',
			retry_operation_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_continuation_recovery_contracts_session ON continuation_recovery_contracts(session_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_continuation_recovery_contracts_subject ON continuation_recovery_contracts(session_id, subject_kind, subject_ref, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_continuation_recovery_contracts_request ON continuation_recovery_contracts(request_instance_id, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure continuation recovery contract tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV82ToV83(tx *sql.Tx) error {
	if err := ensureContinuationRecoveryContractTables(tx); err != nil {
		return err
	}
	return migrateLegacyContinuationRecoveryNextActions(tx)
}
