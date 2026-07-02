//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureAuthorityBundleTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS authority_bundle_contracts (
			bundle_id TEXT PRIMARY KEY,
			contract_version TEXT NOT NULL DEFAULT '',
			request_instance_id TEXT NOT NULL DEFAULT '',
			contract_hash TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			principal TEXT NOT NULL DEFAULT '',
			objective TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			source_next_action_record_ids_json TEXT NOT NULL DEFAULT '[]',
			allowed_actions_json TEXT NOT NULL DEFAULT '[]',
			forbidden_actions_json TEXT NOT NULL DEFAULT '[]',
			stop_conditions_json TEXT NOT NULL DEFAULT '[]',
			primary_continuation_contract_id TEXT NOT NULL DEFAULT '',
			required_capability_grants_json TEXT NOT NULL DEFAULT '[]',
			components_json TEXT NOT NULL DEFAULT '[]',
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_authority_bundle_contracts_session ON authority_bundle_contracts(session_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_authority_bundle_contracts_request ON authority_bundle_contracts(request_instance_id, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure authority bundle tables: %w", err)
		}
	}
	return nil
}
