//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureReentryRecommendationTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS reentry_recommendations (
			id TEXT PRIMARY KEY,
			owner TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			scope_durable_agent_id TEXT NOT NULL DEFAULT '',
			source_turn_run_id INTEGER NOT NULL DEFAULT 0,
			terminal_fingerprint TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			candidates_json TEXT NOT NULL DEFAULT '[]',
			selected_candidate_id TEXT NOT NULL DEFAULT '',
			result_summary TEXT NOT NULL DEFAULT '',
			delivery_message_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			shown_at TEXT,
			selected_at TEXT,
			ignored_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reentry_recommendations_session_status ON reentry_recommendations(session_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_reentry_recommendations_owner_fingerprint ON reentry_recommendations(owner, terminal_fingerprint, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_reentry_recommendations_turn_run ON reentry_recommendations(source_turn_run_id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure reentry recommendation tables: %w", err)
		}
	}
	return nil
}
