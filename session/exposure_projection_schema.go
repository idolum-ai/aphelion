//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureExposureProjectionTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS exposure_projection_events (
			projection_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			invocation_id TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			audience TEXT NOT NULL DEFAULT '',
			purpose TEXT NOT NULL DEFAULT '',
			policy_ref TEXT NOT NULL DEFAULT '',
			projection_kind TEXT NOT NULL DEFAULT '',
			sensitivity TEXT NOT NULL DEFAULT '',
			sensitivity_provenance_json TEXT NOT NULL DEFAULT '[]',
			source_kind TEXT NOT NULL DEFAULT '',
			source_ref TEXT NOT NULL DEFAULT '',
			source_hash TEXT NOT NULL DEFAULT '',
			protected_evidence_ref TEXT NOT NULL DEFAULT '',
			projected_text TEXT NOT NULL DEFAULT '',
			projected_hash TEXT NOT NULL DEFAULT '',
			raw_bytes INTEGER NOT NULL DEFAULT 0,
			projected_bytes INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_exposure_projection_session ON exposure_projection_events(session_id, created_at DESC, projection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_exposure_projection_turn_run ON exposure_projection_events(turn_run_id, created_at DESC, projection_id)`,
		`CREATE INDEX IF NOT EXISTS idx_exposure_projection_protected_ref ON exposure_projection_events(protected_evidence_ref)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure exposure projection tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV76ToV77(tx *sql.Tx) error {
	return ensureExposureProjectionTables(tx)
}
