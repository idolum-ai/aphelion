//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func durableExternalRuntimeSchemaStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS durable_child_runtime_specs (
			spec_id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL DEFAULT '',
			spec_hash TEXT NOT NULL DEFAULT '',
			runtime_kind TEXT NOT NULL DEFAULT '',
			runtime_mode TEXT NOT NULL DEFAULT '',
			source_ref TEXT NOT NULL DEFAULT '',
			spec_json TEXT NOT NULL DEFAULT '{}',
			install_status TEXT NOT NULL DEFAULT 'pending',
			probe_status TEXT NOT NULL DEFAULT '',
			drift_status TEXT NOT NULL DEFAULT '',
			source_request_id TEXT NOT NULL DEFAULT '',
			source_review_event_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			verified_at TEXT,
			stale_at TEXT,
			UNIQUE(agent_id, spec_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_runtime_specs_agent ON durable_child_runtime_specs(agent_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_runtime_specs_status ON durable_child_runtime_specs(install_status, probe_status, drift_status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS durable_child_work_agreement_versions (
			agreement_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed', 'active', 'rejected', 'superseded', 'revoked')),
			authority_principal TEXT NOT NULL DEFAULT '',
			review_principal TEXT NOT NULL DEFAULT '',
			runtime_kind TEXT NOT NULL DEFAULT '',
			agreement_hash TEXT NOT NULL DEFAULT '',
			agreement_json TEXT NOT NULL DEFAULT '{}',
			source_request_id TEXT NOT NULL DEFAULT '',
			source_review_event_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			activated_at TEXT,
			superseded_at TEXT,
			revoked_at TEXT,
			PRIMARY KEY(agreement_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_work_agreements_agent_status ON durable_child_work_agreement_versions(agent_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_work_agreements_request ON durable_child_work_agreement_versions(source_request_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_durable_child_work_agreements_one_active ON durable_child_work_agreement_versions(agent_id, agreement_id) WHERE status = 'active'`,
		`CREATE TABLE IF NOT EXISTS durable_child_conditional_grants (
			agreement_id TEXT NOT NULL,
			agreement_version INTEGER NOT NULL,
			grant_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'active',
			capability TEXT NOT NULL DEFAULT '',
			tool TEXT NOT NULL DEFAULT '',
			actions_json TEXT NOT NULL DEFAULT '[]',
			triggers_json TEXT NOT NULL DEFAULT '[]',
			credential_scope TEXT NOT NULL DEFAULT '',
			grant_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(agreement_id, agreement_version, grant_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_conditional_grants_agent ON durable_child_conditional_grants(agent_id, agreement_id, agreement_version)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_conditional_grants_trigger ON durable_child_conditional_grants(agreement_id, agreement_version, status)`,
		`CREATE TABLE IF NOT EXISTS durable_child_lease_materializations (
			lease_id TEXT PRIMARY KEY,
			materialization_id TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			agreement_id TEXT NOT NULL DEFAULT '',
			agreement_version INTEGER NOT NULL DEFAULT 0,
			conditional_grant_id TEXT NOT NULL DEFAULT '',
			conditional_grant_agreement_version INTEGER NOT NULL DEFAULT 0,
			capability TEXT NOT NULL DEFAULT '',
			lease_kind TEXT NOT NULL DEFAULT '',
			review_route TEXT NOT NULL DEFAULT '',
			runtime_spec_hash TEXT NOT NULL DEFAULT '',
			trigger TEXT NOT NULL DEFAULT '',
			single_use INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'consumed', 'revoked', 'expired')),
			materialization_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT,
			consumed_at TEXT,
			revoked_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_lease_materializations_materialization ON durable_child_lease_materializations(materialization_id, lease_id)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_lease_materializations_agent_status ON durable_child_lease_materializations(agent_id, status, expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_lease_materializations_grant ON durable_child_lease_materializations(agreement_id, agreement_version, conditional_grant_id, conditional_grant_agreement_version)`,
		`CREATE TABLE IF NOT EXISTS durable_child_work_agreement_amendments (
			amendment_id TEXT PRIMARY KEY,
			agreement_id TEXT NOT NULL DEFAULT '',
			from_version INTEGER NOT NULL DEFAULT 0,
			proposed_version INTEGER NOT NULL DEFAULT 0,
			proposed_by TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed', 'approved', 'rejected', 'superseded')),
			change_class_json TEXT NOT NULL DEFAULT '[]',
			amendment_json TEXT NOT NULL DEFAULT '{}',
			source_request_id TEXT NOT NULL DEFAULT '',
			source_review_event_id INTEGER NOT NULL DEFAULT 0,
			result_review_event_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_work_agreement_amendments_agreement ON durable_child_work_agreement_amendments(agreement_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_work_agreement_amendments_request ON durable_child_work_agreement_amendments(source_request_id)`,
	}
}

func ensureDurableExternalRuntimeTables(tx *sql.Tx) error {
	for _, stmt := range durableExternalRuntimeSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure durable external runtime tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV90ToV91(tx *sql.Tx) error {
	return ensureDurableExternalRuntimeTables(tx)
}
