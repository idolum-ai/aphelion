//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureIdentificationLedgerTables(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS identification_ledger_entries (
			entry_id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL DEFAULT '',
			plan_version TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			step_ref TEXT NOT NULL DEFAULT '',
			shape_hash TEXT NOT NULL DEFAULT '',
			label_ref TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(plan_id, plan_version, session_id, step_ref, shape_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_entries_run ON identification_ledger_entries(session_id, plan_id, plan_version, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_entries_label ON identification_ledger_entries(label_ref)`,
		`CREATE TABLE IF NOT EXISTS identification_ledger_observations (
			observation_id TEXT PRIMARY KEY,
			entry_id TEXT NOT NULL,
			method TEXT NOT NULL DEFAULT '',
			property TEXT NOT NULL DEFAULT '',
			value TEXT NOT NULL DEFAULT '',
			evidence_ref TEXT NOT NULL DEFAULT '',
			expires_at TEXT,
			observed_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY(entry_id) REFERENCES identification_ledger_entries(entry_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_observations_entry ON identification_ledger_observations(entry_id, observed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_observations_method_property ON identification_ledger_observations(method, property)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_observations_evidence ON identification_ledger_observations(evidence_ref)`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure identification ledger tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV87ToV88(tx *sql.Tx) error {
	return ensureIdentificationLedgerTables(tx)
}
