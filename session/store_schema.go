//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func applyMigrations(tx *sql.Tx) error {
	currentVersion, err := currentSchemaVersion(tx)
	if err != nil {
		return err
	}
	if err := rejectUnsupportedLegacySchema(tx, currentVersion); err != nil {
		return err
	}

	if err := ensureSessionColumn(tx, "last_floor_text", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.last_floor_text: %w", err)
	}
	if err := ensureSessionColumn(tx, "last_floor_metadata", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.last_floor_metadata: %w", err)
	}
	if err := ensureSessionColumn(tx, "operation_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.operation_state_json: %w", err)
	}
	if err := ensureSessionColumn(tx, "continuation_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.continuation_state_json: %w", err)
	}
	if err := ensureSessionColumn(tx, "session_id", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.session_id: %w", err)
	}
	if err := ensureSessionColumn(tx, "scope_kind", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.scope_kind: %w", err)
	}
	if err := ensureSessionColumn(tx, "scope_id", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.scope_id: %w", err)
	}
	if err := ensureSessionColumn(tx, "durable_agent_id", "TEXT"); err != nil {
		return fmt.Errorf("ensure sessions.durable_agent_id: %w", err)
	}
	if err := ensureSessionColumn(tx, "plan_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.plan_state_json: %w", err)
	}
	if err := ensureSessionColumn(tx, "working_objective_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.working_objective_json: %w", err)
	}
	if err := ensureTableColumn(tx, "messages", "floor_content", "TEXT"); err != nil {
		return fmt.Errorf("ensure messages.floor_content: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS artifact_index (
		session_id TEXT NOT NULL,
		turn_index INTEGER NOT NULL DEFAULT 0,
		artifact_id TEXT NOT NULL,
		chat_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		source_type TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		handling TEXT NOT NULL DEFAULT '',
		retention TEXT NOT NULL DEFAULT '',
		fetch_state TEXT NOT NULL DEFAULT '',
		materialized_path TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (session_id, turn_index, artifact_id)
	)`); err != nil {
		return fmt.Errorf("ensure artifact_index table: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_session_turn ON artifact_index(session_id, turn_index DESC, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_summary ON artifact_index(summary, kind, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure artifact index support: %w", err)
		}
	}
	if err := ensureTableColumn(tx, "messages", "floor_metadata", "TEXT"); err != nil {
		return fmt.Errorf("ensure messages.floor_metadata: %w", err)
	}
	for _, column := range []struct {
		name string
		typ  string
	}{
		{"actor_user_id", "INTEGER NOT NULL DEFAULT 0"},
		{"actor_role", "TEXT NOT NULL DEFAULT ''"},
		{"event_origin", "TEXT NOT NULL DEFAULT ''"},
		{"event_origin_detail", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureTableColumn(tx, "messages", column.name, column.typ); err != nil {
			return fmt.Errorf("ensure messages.%s: %w", column.name, err)
		}
	}
	for _, column := range []struct {
		table string
		name  string
		typ   string
	}{
		{"messages", "session_id", "TEXT"},
		{"review_events", "source_scope_kind", "TEXT"},
		{"review_events", "source_scope_id", "TEXT"},
		{"review_events", "source_durable_agent_id", "TEXT"},
		{"review_events", "source_session_id", "TEXT"},
		{"review_events", "target_scope_kind", "TEXT"},
		{"review_events", "target_scope_id", "TEXT"},
		{"review_events", "target_durable_agent_id", "TEXT"},
		{"review_events", "target_session_id", "TEXT"},
		{"review_events", "metadata_json", "TEXT"},
		{"outbound_messages", "session_id", "TEXT"},
		{"turn_runs", "scope_kind", "TEXT"},
		{"turn_runs", "scope_id", "TEXT"},
		{"turn_runs", "durable_agent_id", "TEXT"},
		{"turn_runs", "session_id", "TEXT"},
		{"turn_runs", "tool_calls_finished", "INTEGER NOT NULL DEFAULT 0"},
		{"turn_runs", "last_tool_result_preview", "TEXT"},
		{"turn_runs", "last_tool_error", "TEXT"},
		{"compaction_log", "session_id", "TEXT"},
	} {
		if err := ensureTableColumn(tx, column.table, column.name, column.typ); err != nil {
			return fmt.Errorf("ensure %s.%s: %w", column.table, column.name, err)
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS execution_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		chat_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		scope_kind TEXT NOT NULL DEFAULT '',
		scope_id TEXT NOT NULL DEFAULT '',
		durable_agent_id TEXT NOT NULL DEFAULT '',
		seq INTEGER NOT NULL,
		event_type TEXT NOT NULL,
		stage TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		caused_by_seq INTEGER NOT NULL DEFAULT 0,
		payload_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure execution_events table: %w", err)
	}
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure execution_events index: %w", err)
		}
	}
	if err := ensureOperatorAutoApprovalTables(tx); err != nil {
		return err
	}

	if err := ensureSessionColumn(tx, "plan_state_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure sessions.plan_state_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "bootstrap_ceiling_json", "TEXT"); err != nil {
		return fmt.Errorf("ensure durable_agents.bootstrap_ceiling_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "channel_config_json", "TEXT NOT NULL DEFAULT '{}'"); err != nil {
		return fmt.Errorf("ensure durable_agents.channel_config_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "bootstrap_provider_json", "TEXT"); err != nil {
		return fmt.Errorf("ensure durable_agents.bootstrap_provider_json: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "control_plane_secret", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("ensure durable_agents.control_plane_secret: %w", err)
	}
	if err := ensureTableColumn(tx, "durable_agents", "allowed_telegram_user_ids_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return fmt.Errorf("ensure durable_agents.allowed_telegram_user_ids_json: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS durable_child_agreements (
		agreement_id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL DEFAULT '',
		parent_principal TEXT NOT NULL DEFAULT '',
		child_principal TEXT NOT NULL DEFAULT '',
		source_surface TEXT NOT NULL DEFAULT '',
		source_request_id TEXT NOT NULL DEFAULT '',
		source_review_event_id INTEGER NOT NULL DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '',
		bounded_effect TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'proposed' CHECK(status IN ('proposed', 'approved', 'rejected', 'superseded')),
		artifact_refs_json TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure durable_child_agreements table: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_durable_child_agreements_agent ON durable_child_agreements(agent_id, updated_at DESC)`); err != nil {
		return fmt.Errorf("ensure durable_child_agreements agent index: %w", err)
	}
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_durable_child_agreements_request ON durable_child_agreements(source_request_id)`); err != nil {
		return fmt.Errorf("ensure durable_child_agreements request index: %w", err)
	}

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pending_artifact_retention (
		owner_key TEXT PRIMARY KEY,
		chat_id INTEGER NOT NULL DEFAULT 0,
		sender_id INTEGER NOT NULL DEFAULT 0,
		inbound_message_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure pending_artifact_retention table: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pending_busy_decisions (
		owner_key TEXT PRIMARY KEY,
		chat_id INTEGER NOT NULL DEFAULT 0,
		sender_id INTEGER NOT NULL DEFAULT 0,
		inbound_message_json TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure pending_busy_decisions table: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS durable_agent_identity_state (
		agent_id TEXT PRIMARY KEY,
		last_offered_policy_version INTEGER NOT NULL DEFAULT 0,
		last_offered_policy_hash TEXT NOT NULL DEFAULT '',
		last_offered_policy_at TEXT,
		last_acknowledged_policy_version INTEGER NOT NULL DEFAULT 0,
		last_acknowledged_policy_hash TEXT NOT NULL DEFAULT '',
		last_acknowledged_policy_at TEXT,
		last_applied_policy_version INTEGER NOT NULL DEFAULT 0,
		last_applied_policy_hash TEXT NOT NULL DEFAULT '',
		last_applied_policy_at TEXT,
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
	)`); err != nil {
		return fmt.Errorf("ensure durable_agent_identity_state table: %w", err)
	}
	for _, column := range []struct {
		name string
		typ  string
	}{
		{"cursor", "TEXT"},
		{"status", "TEXT"},
		{"state_json", "TEXT"},
		{"last_apply_status", "TEXT NOT NULL DEFAULT ''"},
		{"last_apply_error", "TEXT NOT NULL DEFAULT ''"},
		{"last_wake_at", "TEXT"},
		{"last_review_at", "TEXT"},
		{"dormant_at", "TEXT"},
	} {
		if err := ensureTableColumn(tx, "durable_agent_state", column.name, column.typ); err != nil {
			return fmt.Errorf("ensure durable_agent_state.%s: %w", column.name, err)
		}
	}
	for _, column := range []struct {
		name string
		typ  string
	}{
		{"last_offered_policy_version", "INTEGER NOT NULL DEFAULT 0"},
		{"last_offered_policy_hash", "TEXT NOT NULL DEFAULT ''"},
		{"last_offered_policy_at", "TEXT"},
		{"last_acknowledged_policy_version", "INTEGER NOT NULL DEFAULT 0"},
		{"last_acknowledged_policy_hash", "TEXT NOT NULL DEFAULT ''"},
		{"last_acknowledged_policy_at", "TEXT"},
		{"last_applied_policy_version", "INTEGER NOT NULL DEFAULT 0"},
		{"last_applied_policy_hash", "TEXT NOT NULL DEFAULT ''"},
		{"last_applied_policy_at", "TEXT"},
	} {
		if currentVersion > 0 && currentVersion < 26 {
			if err := ensureTableColumn(tx, "durable_agent_state", column.name, column.typ); err != nil {
				return fmt.Errorf("ensure durable_agent_state.%s: %w", column.name, err)
			}
		}
		if err := ensureTableColumn(tx, "durable_agent_identity_state", column.name, column.typ); err != nil {
			return fmt.Errorf("ensure durable_agent_identity_state.%s: %w", column.name, err)
		}
	}
	if currentVersion < 13 {
		if err := backfillDurableAgentBootstrapCeilings(tx); err != nil {
			return err
		}
	}
	if currentVersion < 22 {
		if err := migrateArtifactIndexOccurrenceIdentity(tx); err != nil {
			return err
		}
	}
	if currentVersion > 0 && currentVersion < 26 {
		if err := backfillDurableAgentIdentityState(tx); err != nil {
			return err
		}
	}
	for _, spec := range []struct{ table, name, typ string }{
		{table: "tool_install_records", name: "consecutive_failures", typ: "INTEGER NOT NULL DEFAULT 0"},
		{table: "tool_install_records", name: "last_failure_at", typ: "TEXT"},
		{table: "tool_install_records", name: "rationale", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "artifact_refs_json", typ: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "tool_install_records", name: "baseline_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "current_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "baseline_install_ref", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "current_install_ref", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "baseline_manifest_hash", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "current_manifest_hash", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "baseline_workspace_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "current_workspace_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "stale_reason", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_install_records", name: "drift_source", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "consecutive_failures", typ: "INTEGER NOT NULL DEFAULT 0"},
		{table: "tool_probe_records", name: "last_failure_at", typ: "TEXT"},
		{table: "tool_probe_records", name: "rationale", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "artifact_refs_json", typ: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "tool_probe_records", name: "baseline_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "current_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "baseline_install_ref", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "current_install_ref", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "baseline_manifest_hash", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "current_manifest_hash", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "baseline_workspace_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "current_workspace_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "stale_reason", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_probe_records", name: "drift_source", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "consecutive_failures", typ: "INTEGER NOT NULL DEFAULT 0"},
		{table: "tool_audit_records", name: "last_failure_at", typ: "TEXT"},
		{table: "tool_audit_records", name: "rationale", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "artifact_refs_json", typ: "TEXT NOT NULL DEFAULT '[]'"},
		{table: "tool_audit_records", name: "baseline_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "current_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "baseline_install_ref", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "current_install_ref", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "baseline_manifest_hash", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "current_manifest_hash", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "baseline_workspace_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "current_workspace_fingerprint", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "stale_reason", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "tool_audit_records", name: "drift_source", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "pending_decisions", name: "rationale", typ: "TEXT NOT NULL DEFAULT ''"},
		{table: "pending_decisions", name: "artifact_refs_json", typ: "TEXT NOT NULL DEFAULT '[]'"},
	} {
		if err := ensureTableColumn(tx, spec.table, spec.name, spec.typ); err != nil {
			return err
		}
	}
	if currentVersion > 0 && currentVersion < 35 {
		if err := migrateLegacyToolAuthorityTables(tx); err != nil {
			return err
		}
	}
	if currentVersion < 36 {
		if err := migrateDurableChildAuthorityCanonicalization(tx); err != nil {
			return err
		}
	}
	if currentVersion < 37 {
		if err := migrateCapabilityKindSystemChange(tx); err != nil {
			return err
		}
	}
	if currentVersion < 38 {
		if err := ensureModelSlotOverridesTable(tx); err != nil {
			return err
		}
	}
	if err := ensureTailnetSurfaceTables(tx); err != nil {
		return err
	}
	if err := ensureTailnetGrantBindingTables(tx); err != nil {
		return err
	}
	if err := ensureMissionLedgerTables(tx); err != nil {
		return err
	}
	if err := ensureCapabilityInvocationAuthorityUseColumns(tx); err != nil {
		return err
	}
	if currentVersion >= schemaVersion {
		return nil
	}

	for version := currentVersion + 1; version <= schemaVersion; version++ {
		if _, err := tx.Exec(`INSERT INTO schema_version(version) VALUES (?)`, version); err != nil {
			return fmt.Errorf("insert schema version %d: %w", version, err)
		}
	}
	return nil
}

func ensureCapabilityInvocationAuthorityUseColumns(tx *sql.Tx) error {
	for _, column := range []struct {
		name string
		typ  string
	}{
		{"session_id", "TEXT NOT NULL DEFAULT ''"},
		{"turn_run_id", "INTEGER NOT NULL DEFAULT 0"},
		{"continuation_lease_id", "TEXT NOT NULL DEFAULT ''"},
		{"operation_plan_lease_id", "TEXT NOT NULL DEFAULT ''"},
		{"authority_source", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := ensureTableColumn(tx, "capability_invocations", column.name, column.typ); err != nil {
			return fmt.Errorf("ensure capability_invocations.%s: %w", column.name, err)
		}
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_capability_invocations_authority_session ON capability_invocations(session_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_invocations_lease ON capability_invocations(continuation_lease_id, operation_plan_lease_id, created_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure capability invocation authority index: %w", err)
		}
	}
	return nil
}

func ensureModelSlotOverridesTable(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS model_slot_overrides (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slot TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			previous_config_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','superseded','rolled_back','cleared','expired')),
			created_by TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			expires_at TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_model_slot_overrides_slot_status ON model_slot_overrides(slot, status, id DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure model slot override table: %w", err)
		}
	}
	return nil
}

func ensureOperatorAutoApprovalTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS operator_auto_approvals (
			lease_id TEXT PRIMARY KEY,
			admin_user_id INTEGER NOT NULL DEFAULT 0,
			chat_id INTEGER NOT NULL DEFAULT 0,
			scope TEXT NOT NULL DEFAULT 'all',
			reason TEXT NOT NULL DEFAULT '',
			max_uses INTEGER NOT NULL DEFAULT 0,
			used_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_operator_auto_approvals_chat_active ON operator_auto_approvals(chat_id, expires_at DESC, revoked_at, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_operator_auto_approvals_admin_active ON operator_auto_approvals(admin_user_id, expires_at DESC, revoked_at, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure operator auto approvals table: %w", err)
		}
	}
	return nil
}

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

func ensureSessionIdentityIndexes(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_transport_scope ON sessions(chat_id, user_id, scope_kind, scope_id, durable_agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_active ON messages(session_id, compacted, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_outbound_session ON outbound_messages(session_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_review_events_target ON review_events(target_chat_id, status, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_review_events_target_session ON review_events(target_session_id, status, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_plan_events_session ON plan_events(session_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_registered_tools_state ON registered_tools(registered, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_install_records_status ON tool_install_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_probe_records_status ON tool_probe_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_audit_records_status ON tool_audit_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_session ON turn_runs(session_id, started_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_recovery ON turn_runs(status, recovery_logged_at, started_at, id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure session identity index: %w", err)
		}
	}
	return nil
}

func backfillDurableAgentBootstrapCeilings(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT agent_id, channel_kind, live_policy_json, COALESCE(bootstrap_ceiling_json, '')
		FROM durable_agents
	`)
	if err != nil {
		return fmt.Errorf("query durable agents for bootstrap ceiling backfill: %w", err)
	}
	defer rows.Close()

	type row struct {
		agentID        string
		channelKind    string
		livePolicyJSON string
		bootstrapJSON  string
	}
	var updates []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.agentID, &item.channelKind, &item.livePolicyJSON, &item.bootstrapJSON); err != nil {
			return fmt.Errorf("scan durable agent bootstrap ceiling row: %w", err)
		}
		ceiling, err := unmarshalDurableAgentBootstrapCeiling(item.bootstrapJSON)
		if err != nil {
			return fmt.Errorf("decode durable agent bootstrap ceiling agent_id=%s: %w", item.agentID, err)
		}
		if !ceiling.IsZero() {
			continue
		}
		updates = append(updates, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate durable agents for bootstrap ceiling backfill: %w", err)
	}
	for _, item := range updates {
		policy, err := unmarshalDurableAgentLivePolicy(item.livePolicyJSON)
		if err != nil {
			return fmt.Errorf("decode durable agent live policy agent_id=%s: %w", item.agentID, err)
		}
		bootstrapJSON, err := marshalDurableAgentBootstrapCeiling(core.DefaultDurableAgentBootstrapCeiling(item.channelKind, policy))
		if err != nil {
			return fmt.Errorf("marshal durable agent bootstrap ceiling agent_id=%s: %w", item.agentID, err)
		}
		if _, err := tx.Exec(`UPDATE durable_agents SET bootstrap_ceiling_json = ? WHERE agent_id = ?`, bootstrapJSON, item.agentID); err != nil {
			return fmt.Errorf("backfill durable agent bootstrap ceiling agent_id=%s: %w", item.agentID, err)
		}
	}
	return nil
}

func backfillDurableAgentIdentityState(tx *sql.Tx) error {
	for _, column := range []string{
		"last_offered_policy_version",
		"last_offered_policy_hash",
		"last_offered_policy_at",
		"last_acknowledged_policy_version",
		"last_acknowledged_policy_hash",
		"last_acknowledged_policy_at",
		"last_applied_policy_version",
		"last_applied_policy_hash",
		"last_applied_policy_at",
	} {
		has, err := tableHasColumn(tx, "durable_agent_state", column)
		if err != nil {
			return err
		}
		if !has {
			return nil
		}
	}
	_, err := tx.Exec(`
		INSERT OR REPLACE INTO durable_agent_identity_state(
			agent_id,
			last_offered_policy_version, last_offered_policy_hash, last_offered_policy_at,
			last_acknowledged_policy_version, last_acknowledged_policy_hash, last_acknowledged_policy_at,
			last_applied_policy_version, last_applied_policy_hash, last_applied_policy_at,
			updated_at
		)
		SELECT
			agent_id,
			COALESCE(last_offered_policy_version, 0), COALESCE(last_offered_policy_hash, ''), last_offered_policy_at,
			COALESCE(last_acknowledged_policy_version, 0), COALESCE(last_acknowledged_policy_hash, ''), last_acknowledged_policy_at,
			COALESCE(last_applied_policy_version, 0), COALESCE(last_applied_policy_hash, ''), last_applied_policy_at,
			COALESCE(updated_at, datetime('now'))
		FROM durable_agent_state
	`)
	if err != nil {
		return fmt.Errorf("backfill durable agent identity state: %w", err)
	}
	return nil
}

func migrateDurableChildAuthorityCanonicalization(tx *sql.Tx) error {
	ids, err := durableAgentIDsForMigration(tx)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		for _, spec := range []struct{ table, column, idColumn string }{
			{table: "capability_requests", column: "requested_by", idColumn: "request_id"},
			{table: "capability_requests", column: "requested_for", idColumn: "request_id"},
			{table: "capability_grants", column: "granted_by", idColumn: "grant_id"},
			{table: "capability_grants", column: "granted_to", idColumn: "grant_id"},
		} {
			if err := canonicalizeDurableAgentPrincipalColumn(tx, spec.table, spec.column, spec.idColumn, ids); err != nil {
				return err
			}
		}
	}
	for _, spec := range []struct{ table, idColumn string }{
		{table: "capability_requests", idColumn: "request_id"},
		{table: "capability_grants", idColumn: "grant_id"},
	} {
		if err := canonicalizeChildRuntimeJSONColumns(tx, spec.table, spec.idColumn); err != nil {
			return err
		}
	}
	return nil
}

func migrateCapabilityKindSystemChange(tx *sql.Tx) error {
	if err := migrateCapabilityKindTable(tx, capabilityKindTableMigration{
		table: "capability_requests",
		createSQL: `CREATE TABLE capability_requests (
			request_id TEXT PRIMARY KEY,
			requested_by TEXT NOT NULL DEFAULT '',
			requested_for TEXT NOT NULL DEFAULT '',
			parent_principal TEXT NOT NULL DEFAULT '',
			admin_principal TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'generic_delegation' CHECK(kind IN ('tool', 'local_device', 'external_account', 'purchase', 'public_web', 'communication', 'file_access', 'network_access', 'generic_delegation', 'system_change')),
			target_resource TEXT NOT NULL DEFAULT '',
			purpose TEXT NOT NULL DEFAULT '',
			risk_class TEXT NOT NULL DEFAULT '',
			contract_json TEXT NOT NULL DEFAULT '{}',
			constraints_json TEXT NOT NULL DEFAULT '{}',
			review_status TEXT NOT NULL DEFAULT 'proposed' CHECK(review_status IN ('proposed', 'parent_approved', 'approved', 'rejected')),
			grant_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		columns: []string{
			"request_id", "requested_by", "requested_for", "parent_principal", "admin_principal",
			"kind", "target_resource", "purpose", "risk_class", "contract_json", "constraints_json",
			"review_status", "grant_id", "created_at", "updated_at",
		},
		indexes: []string{
			`CREATE INDEX IF NOT EXISTS idx_capability_requests_status ON capability_requests(review_status, updated_at DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_capability_requests_kind ON capability_requests(kind, updated_at DESC)`,
			`CREATE INDEX IF NOT EXISTS idx_capability_requests_principals ON capability_requests(requested_by, requested_for, parent_principal, admin_principal)`,
		},
	}); err != nil {
		return err
	}
	if err := migrateCapabilityKindTable(tx, capabilityKindTableMigration{
		table: "capability_grants",
		createSQL: `CREATE TABLE capability_grants (
			grant_id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL DEFAULT '',
			granted_by TEXT NOT NULL DEFAULT '',
			granted_to TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT 'generic_delegation' CHECK(kind IN ('tool', 'local_device', 'external_account', 'purchase', 'public_web', 'communication', 'file_access', 'network_access', 'generic_delegation', 'system_change')),
			target_resource TEXT NOT NULL DEFAULT '',
			allowed_actions_json TEXT NOT NULL DEFAULT '[]',
			contract_json TEXT NOT NULL DEFAULT '{}',
			constraints_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'active', 'stale', 'revoked', 'expired', 'failed')),
			baseline_policy_hash TEXT NOT NULL DEFAULT '',
			current_policy_hash TEXT NOT NULL DEFAULT '',
			anchor_fingerprint TEXT NOT NULL DEFAULT '',
			drift_source TEXT NOT NULL DEFAULT '',
			stale_reason TEXT NOT NULL DEFAULT '',
			invocation_count INTEGER NOT NULL DEFAULT 0,
			failure_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			granted_at TEXT,
			expires_at TEXT,
			revoked_at TEXT,
			last_invoked_at TEXT,
			last_failure_at TEXT
		)`,
		columns: []string{
			"grant_id", "request_id", "granted_by", "granted_to", "kind", "target_resource",
			"allowed_actions_json", "contract_json", "constraints_json", "status", "baseline_policy_hash",
			"current_policy_hash", "anchor_fingerprint", "drift_source", "stale_reason", "invocation_count",
			"failure_count", "created_at", "updated_at", "granted_at", "expires_at", "revoked_at",
			"last_invoked_at", "last_failure_at",
		},
		indexes: []string{
			`CREATE INDEX IF NOT EXISTS idx_capability_grants_lookup ON capability_grants(kind, target_resource, granted_to, status)`,
			`CREATE INDEX IF NOT EXISTS idx_capability_grants_status ON capability_grants(status, updated_at DESC)`,
		},
	}); err != nil {
		return err
	}
	return nil
}

type capabilityKindTableMigration struct {
	table     string
	createSQL string
	columns   []string
	indexes   []string
}

func migrateCapabilityKindTable(tx *sql.Tx, spec capabilityKindTableMigration) error {
	needs, err := capabilityKindConstraintNeedsSystemChange(tx, spec.table)
	if err != nil {
		return err
	}
	if !needs {
		return nil
	}
	oldTable := spec.table + "_v37_old"
	if _, err := tx.Exec(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, oldTable)); err != nil {
		return fmt.Errorf("drop prior %s migration table: %w", spec.table, err)
	}
	if _, err := tx.Exec(fmt.Sprintf(`ALTER TABLE %s RENAME TO %s`, spec.table, oldTable)); err != nil {
		return fmt.Errorf("rename %s for capability kind migration: %w", spec.table, err)
	}
	if _, err := tx.Exec(spec.createSQL); err != nil {
		return fmt.Errorf("recreate %s with system_change capability kind: %w", spec.table, err)
	}
	columns := strings.Join(spec.columns, ", ")
	if _, err := tx.Exec(fmt.Sprintf(`INSERT INTO %s(%s) SELECT %s FROM %s`, spec.table, columns, columns, oldTable)); err != nil {
		return fmt.Errorf("copy %s capability kind migration rows: %w", spec.table, err)
	}
	if _, err := tx.Exec(fmt.Sprintf(`DROP TABLE %s`, oldTable)); err != nil {
		return fmt.Errorf("drop old %s after capability kind migration: %w", spec.table, err)
	}
	for _, stmt := range spec.indexes {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("recreate %s capability kind migration index: %w", spec.table, err)
		}
	}
	return nil
}

func capabilityKindConstraintNeedsSystemChange(tx *sql.Tx, table string) (bool, error) {
	var createSQL sql.NullString
	if err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&createSQL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query %s create sql for capability kind migration: %w", table, err)
	}
	text := strings.ToLower(createSQL.String)
	if strings.Contains(text, "system_change") {
		return false, nil
	}
	return strings.Contains(text, "kind") && strings.Contains(text, "check"), nil
}

func durableAgentIDsForMigration(tx *sql.Tx) (map[string]struct{}, error) {
	rows, err := tx.Query(`SELECT agent_id FROM durable_agents`)
	if err != nil {
		return nil, fmt.Errorf("query durable agent ids for migration: %w", err)
	}
	defer rows.Close()
	ids := map[string]struct{}{}
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, fmt.Errorf("scan durable agent id for migration: %w", err)
		}
		agentID = strings.TrimSpace(agentID)
		if agentID != "" {
			ids[agentID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate durable agent ids for migration: %w", err)
	}
	return ids, nil
}

func canonicalizeDurableAgentPrincipalColumn(tx *sql.Tx, table string, column string, idColumn string, ids map[string]struct{}) error {
	rows, err := tx.Query(fmt.Sprintf(`SELECT %s, %s FROM %s`, idColumn, column, table))
	if err != nil {
		return fmt.Errorf("query %s.%s for durable principal migration: %w", table, column, err)
	}
	defer rows.Close()
	type update struct{ id, value string }
	updates := []update{}
	for rows.Next() {
		var id, value string
		if err := rows.Scan(&id, &value); err != nil {
			return fmt.Errorf("scan %s.%s for durable principal migration: %w", table, column, err)
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, core.DurableAgentPrincipalPrefix) {
			canonical := core.DurableAgentPrincipal(value)
			if canonical != "" && canonical != value {
				updates = append(updates, update{id: id, value: canonical})
			}
			continue
		}
		if _, ok := ids[value]; ok {
			updates = append(updates, update{id: id, value: core.DurableAgentPrincipal(value)})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s.%s for durable principal migration: %w", table, column, err)
	}
	for _, update := range updates {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET %s = ?, updated_at = datetime('now') WHERE %s = ?`, table, column, idColumn), update.value, update.id); err != nil {
			return fmt.Errorf("update %s.%s for durable principal migration: %w", table, column, err)
		}
	}
	return nil
}

func canonicalizeChildRuntimeJSONColumns(tx *sql.Tx, table string, idColumn string) error {
	rows, err := tx.Query(fmt.Sprintf(`SELECT %s, contract_json, constraints_json FROM %s`, idColumn, table))
	if err != nil {
		return fmt.Errorf("query %s child_runtime json for migration: %w", table, err)
	}
	defer rows.Close()
	type update struct{ id, contract, constraints string }
	updates := []update{}
	for rows.Next() {
		var id, contract, constraints string
		if err := rows.Scan(&id, &contract, &constraints); err != nil {
			return fmt.Errorf("scan %s child_runtime json for migration: %w", table, err)
		}
		nextContract, changedContract, err := canonicalizeChildRuntimeJSONBlob(contract)
		if err != nil {
			return fmt.Errorf("canonicalize %s.%s contract_json: %w", table, id, err)
		}
		nextConstraints, changedConstraints, err := canonicalizeChildRuntimeJSONBlob(constraints)
		if err != nil {
			return fmt.Errorf("canonicalize %s.%s constraints_json: %w", table, id, err)
		}
		if changedContract || changedConstraints {
			updates = append(updates, update{id: id, contract: nextContract, constraints: nextConstraints})
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s child_runtime json for migration: %w", table, err)
	}
	for _, update := range updates {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET contract_json = ?, constraints_json = ?, updated_at = datetime('now') WHERE %s = ?`, table, idColumn), update.contract, update.constraints, update.id); err != nil {
			return fmt.Errorf("update %s child_runtime json migration: %w", table, err)
		}
	}
	return nil
}

func canonicalizeChildRuntimeJSONBlob(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}", true, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		return "", false, err
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	changed := false
	var runtime core.ChildRuntimeContract
	found := false
	if rawChild, ok := obj["child_runtime"]; ok && len(rawChild) > 0 && string(rawChild) != "null" {
		if err := json.Unmarshal(rawChild, &runtime); err != nil {
			return "", false, err
		}
		found = true
	}
	if rawLegacy, ok := obj["runtime_materialization"]; ok && len(rawLegacy) > 0 && string(rawLegacy) != "null" {
		var legacy core.ChildRuntimeContract
		if err := json.Unmarshal(rawLegacy, &legacy); err != nil {
			return "", false, err
		}
		runtime = core.MergeChildRuntimeContract(runtime, legacy)
		found = true
		delete(obj, "runtime_materialization")
		changed = true
	}
	if found {
		runtime = core.NormalizeChildRuntimeContract(runtime)
		if err := core.ValidateChildRuntimeContract(runtime); err != nil {
			return "", false, err
		}
		encoded, err := json.Marshal(runtime)
		if err != nil {
			return "", false, err
		}
		if string(obj["child_runtime"]) != string(encoded) {
			obj["child_runtime"] = encoded
			changed = true
		}
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

func migrateLegacyToolAuthorityTables(tx *sql.Tx) error {
	hasProposals, err := tableExists(tx, "tool_proposals")
	if err != nil {
		return err
	}
	if hasProposals {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO capability_requests(
				request_id, requested_by, requested_for, kind, target_resource,
				purpose, risk_class, contract_json, constraints_json, review_status,
				created_at, updated_at
			)
			SELECT
				'legacy-tool-proposal:' || proposal_id,
				COALESCE(NULLIF(proposed_by, ''), 'legacy:tool_proposal'),
				COALESCE(NULLIF(proposed_by, ''), 'legacy:tool_proposal'),
				'tool',
				tool_name,
				COALESCE(NULLIF(why_now, ''), 'migrated legacy tool proposal'),
				'',
				COALESCE(NULLIF(contract_json, ''), '{}'),
				'{}',
				CASE review_status
					WHEN 'approved' THEN 'approved'
					WHEN 'rejected' THEN 'rejected'
					ELSE 'proposed'
				END,
				created_at,
				updated_at
			FROM tool_proposals
		`); err != nil {
			return fmt.Errorf("migrate legacy tool proposals to capability requests: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE tool_proposals`); err != nil {
			return fmt.Errorf("drop legacy tool_proposals table: %w", err)
		}
	}

	hasExposures, err := tableExists(tx, "tool_exposures")
	if err != nil {
		return err
	}
	if hasExposures {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO capability_grants(
				grant_id, request_id, granted_by, granted_to, kind, target_resource,
				allowed_actions_json, contract_json, constraints_json, status,
				created_at, updated_at, granted_at, revoked_at
			)
			SELECT
				'legacy-tool-exposure:' || tool_name || ':' || principal,
				'',
				'legacy:tool_exposure',
				principal,
				'tool',
				tool_name,
				'["invoke"]',
				'{}',
				'{}',
				CASE active WHEN 1 THEN 'active' ELSE 'revoked' END,
				created_at,
				updated_at,
				CASE active WHEN 1 THEN created_at ELSE NULL END,
				CASE active WHEN 1 THEN NULL ELSE updated_at END
			FROM tool_exposures
		`); err != nil {
			return fmt.Errorf("migrate legacy tool exposures to capability grants: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE tool_exposures`); err != nil {
			return fmt.Errorf("drop legacy tool_exposures table: %w", err)
		}
	}
	return nil
}

func migrateArtifactIndexOccurrenceIdentity(tx *sql.Tx) error {
	if _, err := tx.Exec(`DROP TABLE IF EXISTS artifact_index_v22_old`); err != nil {
		return fmt.Errorf("drop prior artifact index backup: %w", err)
	}
	if _, err := tx.Exec(`ALTER TABLE artifact_index RENAME TO artifact_index_v22_old`); err != nil {
		return fmt.Errorf("rename artifact_index for rebuild: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE artifact_index (
		session_id TEXT NOT NULL,
		turn_index INTEGER NOT NULL DEFAULT 0,
		artifact_id TEXT NOT NULL,
		chat_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		source_type TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL DEFAULT '',
		summary TEXT NOT NULL DEFAULT '',
		handling TEXT NOT NULL DEFAULT '',
		retention TEXT NOT NULL DEFAULT '',
		fetch_state TEXT NOT NULL DEFAULT '',
		materialized_path TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now')),
		PRIMARY KEY (session_id, turn_index, artifact_id)
	)`); err != nil {
		return fmt.Errorf("create rebuilt artifact_index: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_session_turn ON artifact_index(session_id, turn_index DESC, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_summary ON artifact_index(summary, kind, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create rebuilt artifact_index support: %w", err)
		}
	}
	if err := rebuildArtifactIndexFromMessages(tx); err != nil {
		return err
	}
	if err := backfillArtifactIndexFromSessions(tx); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE artifact_index_v22_old`); err != nil {
		return fmt.Errorf("drop rebuilt artifact index backup: %w", err)
	}
	return nil
}

func rebuildArtifactIndexFromMessages(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT session_id, chat_id, user_id, turn_index, floor_metadata
		FROM messages
		WHERE TRIM(COALESCE(floor_metadata, '')) <> ''
	`)
	if err != nil {
		return fmt.Errorf("query messages for artifact index rebuild: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			sessionID     string
			chatID        int64
			userID        int64
			turnIndex     int
			floorMetadata sql.NullString
		)
		if err := rows.Scan(&sessionID, &chatID, &userID, &turnIndex, &floorMetadata); err != nil {
			return fmt.Errorf("scan message artifact rebuild row: %w", err)
		}
		if err := upsertArtifactIndexFloorMetadata(tx, strings.TrimSpace(sessionID), chatID, userID, turnIndex, nullToString(floorMetadata)); err != nil {
			return fmt.Errorf("rebuild artifact index from message session_id=%s turn_index=%d: %w", strings.TrimSpace(sessionID), turnIndex, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate messages for artifact index rebuild: %w", err)
	}
	return nil
}

func backfillArtifactIndexFromSessions(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT session_id, chat_id, user_id, turn_count, last_floor_metadata
		FROM sessions
		WHERE TRIM(COALESCE(last_floor_metadata, '')) <> ''
	`)
	if err != nil {
		return fmt.Errorf("query sessions for artifact index backfill: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			sessionID         string
			chatID            int64
			userID            int64
			turnCount         int
			lastFloorMetadata sql.NullString
		)
		if err := rows.Scan(&sessionID, &chatID, &userID, &turnCount, &lastFloorMetadata); err != nil {
			return fmt.Errorf("scan session artifact backfill row: %w", err)
		}
		if err := upsertArtifactIndexFloorMetadata(tx, strings.TrimSpace(sessionID), chatID, userID, turnCount, nullToString(lastFloorMetadata)); err != nil {
			return fmt.Errorf("backfill artifact index session_id=%s: %w", strings.TrimSpace(sessionID), err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sessions for artifact index backfill: %w", err)
	}
	return nil
}

func tableHasColumn(tx *sql.Tx, table string, name string) (bool, error) {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("query table_info(%s): %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid      int
			column   string
			typ      string
			notNull  int
			defaultV sql.NullString
			primaryK int
		)
		if err := rows.Scan(&cid, &column, &typ, &notNull, &defaultV, &primaryK); err != nil {
			return false, fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if strings.EqualFold(column, name) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate table_info(%s): %w", table, err)
	}
	return false, nil
}

func tableExists(tx *sql.Tx, table string) (bool, error) {
	table = strings.TrimSpace(table)
	if table == "" {
		return false, nil
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		return false, fmt.Errorf("query sqlite_master table %q: %w", table, err)
	}
	return count > 0, nil
}

func rejectUnsupportedLegacySchema(tx *sql.Tx, currentVersion int) error {
	if currentVersion > 0 && currentVersion < minimumSupportedLegacySchemaVersion {
		return fmt.Errorf(
			"unsupported legacy database schema version %d (minimum supported existing schema version is %d); reinstall from a clean state",
			currentVersion,
			minimumSupportedLegacySchemaVersion,
		)
	}
	if currentVersion != 0 {
		return nil
	}

	// Version 0 is valid for freshly initialized databases in this release.
	// Distinguish that from unversioned legacy layouts by checking modern markers.
	for _, marker := range []struct {
		table  string
		column string
	}{
		{table: "sessions", column: "scope_kind"},
		{table: "sessions", column: "session_id"},
		{table: "durable_agents", column: "live_policy_json"},
	} {
		has, err := tableHasColumn(tx, marker.table, marker.column)
		if err != nil {
			return err
		}
		if !has {
			return fmt.Errorf(
				"unsupported legacy database schema (missing %s.%s); reinstall from a clean state",
				marker.table,
				marker.column,
			)
		}
	}
	return nil
}

func currentSchemaVersion(tx *sql.Tx) (int, error) {
	var maxVersion sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&maxVersion); err != nil {
		return 0, fmt.Errorf("query schema version: %w", err)
	}
	if !maxVersion.Valid {
		return 0, nil
	}
	return int(maxVersion.Int64), nil
}

func ensureSessionColumn(tx *sql.Tx, name string, columnType string) error {
	return ensureTableColumn(tx, "sessions", name, columnType)
}

func ensureTableColumn(tx *sql.Tx, table string, name string, columnType string) error {
	rows, err := tx.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return fmt.Errorf("query table_info(%s): %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid      int
			column   string
			typ      string
			notNull  int
			defaultV sql.NullString
			primaryK int
		)
		if err := rows.Scan(&cid, &column, &typ, &notNull, &defaultV, &primaryK); err != nil {
			return fmt.Errorf("scan table_info(%s): %w", table, err)
		}
		if strings.EqualFold(column, name) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate table_info(%s): %w", table, err)
	}

	stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, name, columnType)
	if _, err := tx.Exec(stmt); err != nil {
		return fmt.Errorf("alter %s add column %s: %w", table, name, err)
	}
	return nil
}
