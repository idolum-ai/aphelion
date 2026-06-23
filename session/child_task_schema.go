//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureChildTaskTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS child_task_packets (
			packet_id TEXT PRIMARY KEY,
			task_lease_id TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			task_kind TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'queued',
			authority_kind TEXT NOT NULL DEFAULT '',
			authority_id TEXT NOT NULL DEFAULT '',
			grant_id TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			target_resource TEXT NOT NULL DEFAULT '',
			required_action TEXT NOT NULL DEFAULT '',
			input_json TEXT NOT NULL DEFAULT '{}',
			result_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			terminal_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_packets_agent_status ON child_task_packets(agent_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_packets_authority ON child_task_packets(authority_kind, authority_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_packets_session ON child_task_packets(session_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS child_task_results (
			result_id TEXT PRIMARY KEY,
			packet_id TEXT NOT NULL,
			task_lease_id TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			result_kind TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			blocker_kind TEXT NOT NULL DEFAULT '',
			error_text TEXT NOT NULL DEFAULT '',
			evidence_refs_json TEXT NOT NULL DEFAULT '[]',
			next_state TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (packet_id) REFERENCES child_task_packets(packet_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_results_packet ON child_task_results(packet_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_results_agent ON child_task_results(agent_id, created_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure child task tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV75ToV76(tx *sql.Tx) error {
	return ensureChildTaskTables(tx)
}
