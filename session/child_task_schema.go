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
			input_fingerprint TEXT NOT NULL DEFAULT '',
			active_attempt_id TEXT NOT NULL DEFAULT '',
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_generation INTEGER NOT NULL DEFAULT 0,
			fencing_token TEXT NOT NULL DEFAULT '',
			lease_expires_at TEXT NOT NULL DEFAULT '',
			lease_heartbeat_at TEXT NOT NULL DEFAULT '',
			lease_released_at TEXT,
			result_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			terminal_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS child_task_results (
			result_id TEXT PRIMARY KEY,
			packet_id TEXT NOT NULL,
			attempt_id TEXT NOT NULL DEFAULT '',
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_generation INTEGER NOT NULL DEFAULT 0,
			fencing_token TEXT NOT NULL DEFAULT '',
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
			result_fingerprint TEXT NOT NULL DEFAULT '',
			intent_set_fingerprint TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (packet_id) REFERENCES child_task_packets(packet_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS child_task_outcome_intents (
			intent_id TEXT PRIMARY KEY,
			packet_id TEXT NOT NULL DEFAULT '',
			result_id TEXT NOT NULL DEFAULT '',
			attempt_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			sequence INTEGER NOT NULL DEFAULT 100,
			payload_json TEXT NOT NULL DEFAULT '{}',
			result_ref TEXT NOT NULL DEFAULT '',
			idempotency_key TEXT NOT NULL DEFAULT '',
			lease_owner TEXT NOT NULL DEFAULT '',
			lease_generation INTEGER NOT NULL DEFAULT 0,
			fencing_token TEXT NOT NULL DEFAULT '',
			lease_expires_at TEXT NOT NULL DEFAULT '',
			next_attempt_at TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			applied_at TEXT,
			dead_letter_at TEXT,
			FOREIGN KEY (packet_id) REFERENCES child_task_packets(packet_id) ON DELETE CASCADE,
			FOREIGN KEY (result_id) REFERENCES child_task_results(result_id) ON DELETE CASCADE
		)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure child task tables: %w", err)
		}
	}
	if err := ensureChildTaskLeaseColumns(tx); err != nil {
		return err
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_child_task_packets_agent_status ON child_task_packets(agent_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_packets_authority ON child_task_packets(authority_kind, authority_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_packets_session ON child_task_packets(session_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_results_packet ON child_task_results(packet_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_results_packet_attempt ON child_task_results(packet_id, attempt_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_child_task_results_packet_attempt ON child_task_results(packet_id, attempt_id)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_results_agent ON child_task_results(agent_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_outcome_intents_status ON child_task_outcome_intents(status, next_attempt_at ASC, updated_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_outcome_intents_packet ON child_task_outcome_intents(packet_id, status, updated_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_child_task_outcome_intents_result ON child_task_outcome_intents(result_id, sequence ASC, intent_id ASC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure child task tables: %w", err)
		}
	}
	return nil
}

func ensureChildTaskLeaseColumns(tx *sql.Tx) error {
	packetsExist, err := schemaTableExists(tx, "child_task_packets")
	if err != nil {
		return err
	}
	if packetsExist {
		for _, column := range []schemaColumnMigration{
			{table: "child_task_packets", column: "active_attempt_id", statement: `ALTER TABLE child_task_packets ADD COLUMN active_attempt_id TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_packets", column: "lease_owner", statement: `ALTER TABLE child_task_packets ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_packets", column: "lease_generation", statement: `ALTER TABLE child_task_packets ADD COLUMN lease_generation INTEGER NOT NULL DEFAULT 0`},
			{table: "child_task_packets", column: "fencing_token", statement: `ALTER TABLE child_task_packets ADD COLUMN fencing_token TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_packets", column: "lease_expires_at", statement: `ALTER TABLE child_task_packets ADD COLUMN lease_expires_at TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_packets", column: "lease_heartbeat_at", statement: `ALTER TABLE child_task_packets ADD COLUMN lease_heartbeat_at TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_packets", column: "lease_released_at", statement: `ALTER TABLE child_task_packets ADD COLUMN lease_released_at TEXT`},
			{table: "child_task_packets", column: "input_fingerprint", statement: `ALTER TABLE child_task_packets ADD COLUMN input_fingerprint TEXT NOT NULL DEFAULT ''`},
		} {
			if err := addSchemaColumnIfMissing(tx, column); err != nil {
				return err
			}
		}
	}

	resultsExist, err := schemaTableExists(tx, "child_task_results")
	if err != nil {
		return err
	}
	if resultsExist {
		for _, column := range []schemaColumnMigration{
			{table: "child_task_results", column: "attempt_id", statement: `ALTER TABLE child_task_results ADD COLUMN attempt_id TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_results", column: "lease_owner", statement: `ALTER TABLE child_task_results ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_results", column: "lease_generation", statement: `ALTER TABLE child_task_results ADD COLUMN lease_generation INTEGER NOT NULL DEFAULT 0`},
			{table: "child_task_results", column: "fencing_token", statement: `ALTER TABLE child_task_results ADD COLUMN fencing_token TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_results", column: "result_fingerprint", statement: `ALTER TABLE child_task_results ADD COLUMN result_fingerprint TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_results", column: "intent_set_fingerprint", statement: `ALTER TABLE child_task_results ADD COLUMN intent_set_fingerprint TEXT NOT NULL DEFAULT ''`},
		} {
			if err := addSchemaColumnIfMissing(tx, column); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`
			UPDATE child_task_results
			SET attempt_id = 'child_attempt:' || substr(result_id, instr(result_id, ':') + 1)
			WHERE attempt_id = ''
		`); err != nil {
			return fmt.Errorf("backfill child task result attempt ids: %w", err)
		}
	}
	intentsExist, err := schemaTableExists(tx, "child_task_outcome_intents")
	if err != nil {
		return err
	}
	if intentsExist {
		for _, column := range []schemaColumnMigration{
			{table: "child_task_outcome_intents", column: "sequence", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN sequence INTEGER NOT NULL DEFAULT 100`},
			{table: "child_task_outcome_intents", column: "idempotency_key", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_outcome_intents", column: "lease_owner", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN lease_owner TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_outcome_intents", column: "lease_generation", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN lease_generation INTEGER NOT NULL DEFAULT 0`},
			{table: "child_task_outcome_intents", column: "fencing_token", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN fencing_token TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_outcome_intents", column: "lease_expires_at", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN lease_expires_at TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_outcome_intents", column: "next_attempt_at", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN next_attempt_at TEXT NOT NULL DEFAULT ''`},
			{table: "child_task_outcome_intents", column: "dead_letter_at", statement: `ALTER TABLE child_task_outcome_intents ADD COLUMN dead_letter_at TEXT`},
		} {
			if err := addSchemaColumnIfMissing(tx, column); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE child_task_outcome_intents SET idempotency_key = intent_id WHERE idempotency_key = ''`); err != nil {
			return fmt.Errorf("backfill child task outcome intent idempotency keys: %w", err)
		}
	}
	return nil
}

func migrateSchemaV77ToV78(tx *sql.Tx) error {
	return ensureChildTaskTables(tx)
}

func migrateSchemaV78ToV79(tx *sql.Tx) error {
	return ensureChildTaskTables(tx)
}

func migrateSchemaV79ToV80(tx *sql.Tx) error {
	return ensureChildTaskTables(tx)
}

func migrateSchemaV80ToV81(tx *sql.Tx) error {
	if err := ensureChildTaskTables(tx); err != nil {
		return err
	}
	return ensureReviewEventIdempotencyKey(tx)
}

func ensureReviewEventIdempotencyKey(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "review_events")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := addSchemaColumnIfMissing(tx, schemaColumnMigration{
		table:     "review_events",
		column:    "idempotency_key",
		statement: `ALTER TABLE review_events ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT ''`,
	}); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ux_review_events_idempotency_key ON review_events(idempotency_key) WHERE idempotency_key != ''`); err != nil {
		return fmt.Errorf("ensure review event idempotency index: %w", err)
	}
	return nil
}
