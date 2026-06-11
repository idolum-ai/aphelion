//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureCuriosityTables(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS curiosity_leases (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			scope_durable_agent_id TEXT NOT NULL DEFAULT '',
			lease_class TEXT NOT NULL DEFAULT '',
			work_action TEXT NOT NULL DEFAULT '',
			allowed_source_kinds_json TEXT NOT NULL DEFAULT '[]',
			allowed_source_refs_json TEXT NOT NULL DEFAULT '[]',
			daily_turn_budget INTEGER NOT NULL DEFAULT 0,
			max_looks_per_turn INTEGER NOT NULL DEFAULT 0,
			turns_used INTEGER NOT NULL DEFAULT 0,
			period_start TEXT NOT NULL DEFAULT '',
			approved_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_curiosity_leases_status_expires ON curiosity_leases(status, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_curiosity_leases_scope ON curiosity_leases(scope_kind, scope_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS curiosity_observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			lease_id TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			scope_durable_agent_id TEXT NOT NULL DEFAULT '',
			candidate_id TEXT NOT NULL DEFAULT '',
			source_kind TEXT NOT NULL DEFAULT '',
			source_ref TEXT NOT NULL DEFAULT '',
			subject_key TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			evidence_json TEXT NOT NULL DEFAULT '[]',
			content_hash TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			observed_at TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_curiosity_observations_lease ON curiosity_observations(lease_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_curiosity_observations_session ON curiosity_observations(session_id, observed_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_curiosity_observations_dedupe ON curiosity_observations(lease_id, candidate_id, content_hash)`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure curiosity tables: %w", err)
		}
	}
	return nil
}
