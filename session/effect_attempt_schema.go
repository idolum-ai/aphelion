//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureEffectAttemptTables(tx *sql.Tx) error {
	for _, stmt := range effectAttemptSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure effect attempt tables: %w", err)
		}
	}
	return nil
}

func effectAttemptSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS effect_attempts (
			attempt_id TEXT PRIMARY KEY,
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
			work_mode TEXT NOT NULL DEFAULT '',
			executor TEXT NOT NULL DEFAULT '',
			tool TEXT NOT NULL DEFAULT '',
			command TEXT NOT NULL DEFAULT '',
			command_hash TEXT NOT NULL DEFAULT '',
			effect_kind TEXT NOT NULL DEFAULT '',
			effect_reason TEXT NOT NULL DEFAULT '',
			boundary_kind TEXT NOT NULL DEFAULT '',
			authorization_reason TEXT NOT NULL DEFAULT '',
			subject_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'attempted',
			error_text TEXT NOT NULL DEFAULT '',
			evidence_refs_json TEXT NOT NULL DEFAULT '[]',
			started_at TEXT,
			completed_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_effect_attempts_session_updated ON effect_attempts(session_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_effect_attempts_turn_run ON effect_attempts(turn_run_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_effect_attempts_operation ON effect_attempts(operation_id, phase_id, lease_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_effect_attempts_status ON effect_attempts(status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_effect_attempts_command_hash ON effect_attempts(command_hash)`,
	}
}

func migrateSchemaV69ToV70(tx *sql.Tx) error {
	return ensureEffectAttemptTables(tx)
}
