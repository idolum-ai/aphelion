//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureTailnetSurfaceTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS tailnet_surfaces (
			surface_id TEXT PRIMARY KEY,
			owner_kind TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			surface_kind TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			hostname TEXT NOT NULL DEFAULT '',
			tailnet_name TEXT NOT NULL DEFAULT '',
			listen_addr TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'declared' CHECK(status IN ('declared', 'active', 'degraded', 'revoked')),
			last_error TEXT NOT NULL DEFAULT '',
			declared_at TEXT NOT NULL DEFAULT (datetime('now')),
			activated_at TEXT,
			last_observed_at TEXT,
			revoked_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(owner_kind, owner_id, surface_kind, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_surfaces_status ON tailnet_surfaces(status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_surfaces_owner ON tailnet_surfaces(owner_kind, owner_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS tailnet_surface_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			surface_id TEXT NOT NULL,
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_surface_events_surface ON tailnet_surface_events(surface_id, id DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure tailnet surface tables: %w", err)
		}
	}
	return nil
}

func ensureTailnetGrantBindingTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS tailnet_grant_bindings (
			binding_id TEXT PRIMARY KEY,
			grant_id TEXT NOT NULL DEFAULT '',
			surface_id TEXT NOT NULL DEFAULT '',
			granted_to TEXT NOT NULL DEFAULT '',
			capability_kind TEXT NOT NULL DEFAULT '',
			target_resource TEXT NOT NULL DEFAULT '',
			desired_policy_json TEXT NOT NULL DEFAULT '{}',
			applied_policy_hash TEXT NOT NULL DEFAULT '',
			observed_policy_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed', 'applied', 'drifted', 'revoked', 'failed')),
			drift_reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			applied_at TEXT,
			revoked_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_grant ON tailnet_grant_bindings(grant_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_surface ON tailnet_grant_bindings(surface_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_status ON tailnet_grant_bindings(status, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tailnet_grant_bindings_active_pair ON tailnet_grant_bindings(grant_id, surface_id) WHERE status != 'revoked'`,
		`CREATE TABLE IF NOT EXISTS tailnet_grant_binding_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			binding_id TEXT NOT NULL,
			event_type TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tailnet_grant_binding_events_binding ON tailnet_grant_binding_events(binding_id, id DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure tailnet grant binding tables: %w", err)
		}
	}
	return nil
}
