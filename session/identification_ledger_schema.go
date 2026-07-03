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
			status TEXT NOT NULL DEFAULT '' CHECK(status IN ('', 'unidentified', 'partial', 'proposed', 'approved', 'consumed', 'expired', 'invalidated')),
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
			method TEXT NOT NULL DEFAULT '' CHECK(method IN ('collision', 'static', 'lookahead', 'operator')),
			property TEXT NOT NULL DEFAULT '' CHECK(property IN ('approval_class', 'resource', 'timeout', 'retryability', 'bundle_fit', 'contract', 'operator_action', 'tool')),
			value TEXT NOT NULL DEFAULT '',
			evidence_ref TEXT NOT NULL DEFAULT '',
			actor_kind TEXT NOT NULL DEFAULT '',
			actor_principal TEXT NOT NULL DEFAULT '',
			actor_action TEXT NOT NULL DEFAULT '',
			expires_at TEXT,
			observed_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_observed_at TEXT NOT NULL DEFAULT (datetime('now')),
			occurrence_count INTEGER NOT NULL DEFAULT 1 CHECK(occurrence_count >= 1),
			FOREIGN KEY(entry_id) REFERENCES identification_ledger_entries(entry_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_observations_entry ON identification_ledger_observations(entry_id, observed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_observations_method_property ON identification_ledger_observations(method, property)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_observations_evidence ON identification_ledger_observations(evidence_ref)`,
		`CREATE INDEX IF NOT EXISTS idx_identification_ledger_observations_actor ON identification_ledger_observations(actor_principal, actor_action)`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure identification ledger tables: %w", err)
		}
	}
	return nil
}

func ensureLookaheadAllowanceTables(tx *sql.Tx) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS authority_lookahead_allowances (
			allowance_id TEXT PRIMARY KEY,
			admin_chat_id INTEGER NOT NULL DEFAULT 0,
			review_event_id INTEGER NOT NULL DEFAULT 0,
			source_session_id TEXT NOT NULL DEFAULT '',
			target_session_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'reserved' CHECK(status IN ('reserved', 'open', 'released', 'expired')),
			next_action_record_id TEXT NOT NULL DEFAULT '',
			entry_id TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT,
			released_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_authority_lookahead_allowances_admin_status ON authority_lookahead_allowances(admin_chat_id, status, expires_at, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_authority_lookahead_allowances_review ON authority_lookahead_allowances(review_event_id, admin_chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_authority_lookahead_allowances_next_action ON authority_lookahead_allowances(next_action_record_id)`,
		`CREATE INDEX IF NOT EXISTS idx_authority_lookahead_allowances_entry ON authority_lookahead_allowances(entry_id)`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure authority lookahead allowance tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV87ToV88(tx *sql.Tx) error {
	return ensureIdentificationLedgerTables(tx)
}

func migrateSchemaV88ToV89(tx *sql.Tx) error {
	if err := rebuildIdentificationLedgerObservationsV89(tx); err != nil {
		return err
	}
	return ensureLookaheadAllowanceTables(tx)
}

func rebuildIdentificationLedgerObservationsV89(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "identification_ledger_observations")
	if err != nil || !exists {
		if err != nil {
			return err
		}
		return ensureIdentificationLedgerTables(tx)
	}
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_identification_ledger_observations_entry`,
		`DROP INDEX IF EXISTS idx_identification_ledger_observations_method_property`,
		`DROP INDEX IF EXISTS idx_identification_ledger_observations_evidence`,
		`DROP INDEX IF EXISTS idx_identification_ledger_observations_actor`,
		`ALTER TABLE identification_ledger_observations RENAME TO identification_ledger_observations_v88`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("rebuild identification ledger observations v89: %w", err)
		}
	}
	if err := ensureIdentificationLedgerTables(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO identification_ledger_observations(
			observation_id, entry_id, method, property, value, evidence_ref,
			actor_kind, actor_principal, actor_action,
			expires_at, observed_at, last_observed_at, occurrence_count
		)
		SELECT observation_id, entry_id, method, property, value, evidence_ref,
			'', '', '',
			expires_at, observed_at, last_observed_at, occurrence_count
		FROM identification_ledger_observations_v88
	`); err != nil {
		return fmt.Errorf("copy identification ledger observations v89: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE identification_ledger_observations_v88`); err != nil {
		return fmt.Errorf("drop identification ledger observations v88: %w", err)
	}
	return nil
}
