//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureJudgmentTables(tx *sql.Tx) error {
	for _, stmt := range judgmentSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure judgment tables: %w", err)
		}
	}
	return nil
}

func judgmentSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS judgments (
			judgment_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			operation_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			schema_version TEXT NOT NULL DEFAULT '',
			subject_key TEXT NOT NULL DEFAULT '',
			claim_key TEXT NOT NULL DEFAULT '',
			interpreter_id TEXT NOT NULL DEFAULT '',
			interpreter_version TEXT NOT NULL DEFAULT '',
			input_refs_json TEXT NOT NULL DEFAULT '[]',
			input_hash TEXT NOT NULL DEFAULT '',
			result_json TEXT NOT NULL DEFAULT '{}',
			completeness TEXT NOT NULL DEFAULT 'complete',
			unknowns_json TEXT NOT NULL DEFAULT '[]',
			dependency_refs_json TEXT NOT NULL DEFAULT '[]',
			source_fault_domains_json TEXT NOT NULL DEFAULT '[]',
			sensitivity TEXT NOT NULL DEFAULT 'ordinary',
			content_hash TEXT NOT NULL DEFAULT '',
			as_of TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_judgments_session_created ON judgments(session_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgments_kind_subject ON judgments(kind, subject_key, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgments_claim ON judgments(session_id, claim_key, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgments_operation ON judgments(operation_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgments_content_hash ON judgments(content_hash)`,
		`CREATE TABLE IF NOT EXISTS judgment_challenge_events (
			event_id TEXT PRIMARY KEY,
			challenge_id TEXT NOT NULL DEFAULT '',
			judgment_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			event_kind TEXT NOT NULL DEFAULT '',
			ground_refs_json TEXT NOT NULL DEFAULT '[]',
			disposition TEXT NOT NULL DEFAULT 'unresolved',
			eligibility_status TEXT NOT NULL DEFAULT 'suspended',
			operational_response TEXT NOT NULL DEFAULT 'none',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_judgment_challenge_events_judgment ON judgment_challenge_events(judgment_id, created_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgment_challenge_events_challenge ON judgment_challenge_events(challenge_id, created_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_judgment_challenge_events_session ON judgment_challenge_events(session_id, created_at DESC)`,
	}
}

func migrateSchemaV73ToV74(tx *sql.Tx) error {
	return ensureJudgmentTables(tx)
}
