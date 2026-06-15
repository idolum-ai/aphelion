//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureEvidenceLedgerTables(tx *sql.Tx) error {
	for _, stmt := range evidenceLedgerSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure universal evidence ledger tables: %w", err)
		}
	}
	return nil
}

func evidenceLedgerSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS evidence_objects (
			evidence_id TEXT PRIMARY KEY,
			evidence_type TEXT NOT NULL DEFAULT '',
			source_kind TEXT NOT NULL DEFAULT '',
			source_ref TEXT NOT NULL DEFAULT '',
			source_table TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			authority_class TEXT NOT NULL DEFAULT '',
			epistemic_status TEXT NOT NULL DEFAULT 'observed',
			redaction_class TEXT NOT NULL DEFAULT 'none',
			subject_key TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			digest TEXT NOT NULL DEFAULT '',
			payload_json TEXT NOT NULL DEFAULT '{}',
			payload_hash TEXT NOT NULL DEFAULT '',
			observed_at TEXT NOT NULL DEFAULT (datetime('now')),
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_evidence_objects_source ON evidence_objects(source_kind, source_ref)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_objects_session ON evidence_objects(session_id, observed_at DESC, evidence_id)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_objects_scope ON evidence_objects(chat_id, scope_kind, scope_id, observed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_objects_subject ON evidence_objects(session_id, subject_key, observed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_objects_type ON evidence_objects(evidence_type, observed_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_objects_payload_hash ON evidence_objects(payload_hash)`,
		`CREATE TABLE IF NOT EXISTS evidence_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			from_evidence_id TEXT NOT NULL,
			to_evidence_id TEXT NOT NULL,
			link_type TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 1.0,
			metadata_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(from_evidence_id, to_evidence_id, link_type)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_links_from ON evidence_links(from_evidence_id, link_type)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_links_to ON evidence_links(to_evidence_id, link_type)`,
		`CREATE TABLE IF NOT EXISTS evidence_hydration_runs (
			run_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			operation_id TEXT NOT NULL DEFAULT '',
			query_text TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'deterministic',
			status TEXT NOT NULL DEFAULT '',
			selected_evidence_ids_json TEXT NOT NULL DEFAULT '[]',
			missing_evidence_ids_json TEXT NOT NULL DEFAULT '[]',
			fallback_used INTEGER NOT NULL DEFAULT 0,
			fallback_reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_hydration_runs_session ON evidence_hydration_runs(session_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_hydration_runs_operation ON evidence_hydration_runs(operation_id, created_at DESC)`,
	}
}

func migrateSchemaV68ToV69(tx *sql.Tx) error {
	if err := ensureEvidenceLedgerTables(tx); err != nil {
		return err
	}
	// Keep boot migration bounded. Historical backfill is available through
	// BackfillEvidenceLedger, but startup only records current durable snapshots.
	if err := backfillSessionStateEvidenceTx(tx); err != nil {
		return fmt.Errorf("backfill current session evidence snapshots: %w", err)
	}
	return nil
}
