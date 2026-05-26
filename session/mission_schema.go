//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureMissionLedgerTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS mission_ledger (
			id TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			owner TEXT NOT NULL,
			title TEXT NOT NULL,
			objective TEXT NOT NULL,
			origin TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			pinned INTEGER NOT NULL DEFAULT 0,
			recurrence_json TEXT,
			authority_json TEXT NOT NULL DEFAULT '{}',
			budget_json TEXT NOT NULL DEFAULT '{}',
			decay_json TEXT NOT NULL DEFAULT '{}',
			success_criteria_json TEXT,
			evidence_json TEXT,
			current_plan_json TEXT,
			next_allowed_action TEXT,
			blocked_reason TEXT,
			waiting_for TEXT,
			tags_json TEXT,
			source_refs_json TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_touched_at TEXT,
			last_summoned_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS mission_events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT,
			mission_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS mission_handoffs (
			id TEXT PRIMARY KEY,
			mission_id TEXT,
			operation_id TEXT,
			planned_action TEXT NOT NULL,
			expected_evidence_json TEXT,
			recovery_question TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS mission_results (
			id TEXT PRIMARY KEY,
			handoff_id TEXT NOT NULL,
			mission_id TEXT,
			operation_id TEXT,
			status TEXT NOT NULL,
			evidence_refs_json TEXT,
			summary TEXT NOT NULL,
			remaining_risk TEXT,
			recorded_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(handoff_id) REFERENCES mission_handoffs(id) ON DELETE CASCADE,
			FOREIGN KEY(mission_id) REFERENCES mission_ledger(id) ON DELETE SET NULL
		)`,
		`CREATE TABLE IF NOT EXISTS mission_ask_prompts (
			id TEXT PRIMARY KEY,
			owner TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			scope_durable_agent_id TEXT NOT NULL DEFAULT '',
			source_message_id INTEGER NOT NULL DEFAULT 0,
			source_turn_run_id INTEGER NOT NULL DEFAULT 0,
			mission_id TEXT NOT NULL DEFAULT '',
			confidence TEXT NOT NULL DEFAULT 'low',
			status TEXT NOT NULL DEFAULT 'pending',
			question_text TEXT NOT NULL DEFAULT '',
			source_fingerprint TEXT NOT NULL DEFAULT '',
			evidence_json TEXT NOT NULL DEFAULT '{}',
			result_summary TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			asked_at TEXT,
			resolved_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ledger_scope_owner_status ON mission_ledger(scope, owner, status)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ledger_pinned ON mission_ledger(scope, owner, pinned)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ledger_last_touched ON mission_ledger(last_touched_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_events_mission_id_seq ON mission_events(mission_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_handoffs_status ON mission_handoffs(status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_results_handoff ON mission_results(handoff_id, recorded_at)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ask_owner_status ON mission_ask_prompts(owner, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ask_owner_confidence ON mission_ask_prompts(owner, confidence, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ask_owner_fingerprint ON mission_ask_prompts(owner, source_fingerprint, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_mission_ask_session_status ON mission_ask_prompts(session_id, status, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure mission ledger tables: %w", err)
		}
	}
	return nil
}
