//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureNextActionTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS next_action_records (
			record_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			owner TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT '',
			subject_kind TEXT NOT NULL DEFAULT '',
			subject_ref TEXT NOT NULL DEFAULT '',
			causal_refs_json TEXT NOT NULL DEFAULT '[]',
			next_action TEXT NOT NULL DEFAULT '',
			required_authority TEXT NOT NULL DEFAULT '',
			resource_blocker TEXT NOT NULL DEFAULT '',
			verifier TEXT NOT NULL DEFAULT '',
			retry_policy TEXT NOT NULL DEFAULT '',
			operation_kind TEXT NOT NULL DEFAULT '',
			operation_tool TEXT NOT NULL DEFAULT '',
			operation_input_json TEXT NOT NULL DEFAULT '',
			operator_projection TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_next_action_session_open ON next_action_records(session_id, resolved_at, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_next_action_subject_open ON next_action_records(session_id, subject_kind, subject_ref, resolved_at, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_next_action_turn_run ON next_action_records(turn_run_id, created_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure next action tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV74ToV75(tx *sql.Tx) error {
	return ensureNextActionTables(tx)
}

func migrateSchemaV75ToV76(tx *sql.Tx) error {
	if err := ensureNextActionTables(tx); err != nil {
		return err
	}
	for _, column := range []schemaColumnMigration{
		{table: "next_action_records", column: "operation_kind", statement: `ALTER TABLE next_action_records ADD COLUMN operation_kind TEXT NOT NULL DEFAULT ''`},
		{table: "next_action_records", column: "operation_tool", statement: `ALTER TABLE next_action_records ADD COLUMN operation_tool TEXT NOT NULL DEFAULT ''`},
		{table: "next_action_records", column: "operation_input_json", statement: `ALTER TABLE next_action_records ADD COLUMN operation_input_json TEXT NOT NULL DEFAULT ''`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	return nil
}
