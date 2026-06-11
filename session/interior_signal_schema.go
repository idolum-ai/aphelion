//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureInteriorSignalTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS interior_signal_observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			scope_durable_agent_id TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			subject_key TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			evidence_json TEXT NOT NULL DEFAULT '[]',
			source_fingerprint TEXT NOT NULL DEFAULT '',
			weight REAL NOT NULL DEFAULT 0,
			applied_weight REAL NOT NULL DEFAULT 0,
			confidence REAL NOT NULL DEFAULT 0,
			observed_at TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_interior_signal_observations_session ON interior_signal_observations(session_id, observed_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_interior_signal_observations_fingerprint ON interior_signal_observations(session_id, category, subject_key, source_fingerprint, observed_at DESC)`,
		`CREATE TABLE IF NOT EXISTS interior_signal_states (
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			scope_durable_agent_id TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			subject_key TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			evidence_json TEXT NOT NULL DEFAULT '[]',
			intensity REAL NOT NULL DEFAULT 0,
			confidence REAL NOT NULL DEFAULT 0,
			observation_count INTEGER NOT NULL DEFAULT 0,
			last_observed_at TEXT,
			last_decayed_at TEXT,
			last_surfaced_at TEXT,
			cooldown_until TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(session_id, category, subject_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_interior_signal_states_session ON interior_signal_states(session_id, intensity DESC, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_interior_signal_states_cooldown ON interior_signal_states(session_id, cooldown_until)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure interior signal tables: %w", err)
		}
	}
	return nil
}
