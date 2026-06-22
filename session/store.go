//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

const schemaVersion = 74

type SQLiteStore struct {
	db     *sql.DB
	dbPath string
}

func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", sqliteOpenDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &SQLiteStore{db: db, dbPath: strings.TrimSpace(dbPath)}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) DBPath() string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.dbPath)
}

func (s *SQLiteStore) init() error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("apply %q: %w", p, err)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin schema tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	existingTables, err := existingUserTableCount(tx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("ensure schema_version table: %w", err)
	}
	currentVersion, err := validateCurrentSchemaVersion(tx, existingTables)
	if err != nil {
		return err
	}
	currentVersion, err = migrateCurrentSchemaVersion(tx, currentVersion)
	if err != nil {
		return err
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			system_prompt TEXT,
			last_floor_text TEXT,
			last_floor_metadata TEXT,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			operation_state_json TEXT NOT NULL DEFAULT '{}',
			continuation_state_json TEXT NOT NULL DEFAULT '{}',
			working_objective_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_count INTEGER NOT NULL DEFAULT 0,
			chat_type TEXT NOT NULL DEFAULT 'dm',
			chat_title TEXT,
			user_name TEXT,
			cache_last_write_block INTEGER NOT NULL DEFAULT 0,
			cache_blocks_since INTEGER NOT NULL DEFAULT 0,
			cache_last_write_time TEXT,
			cache_hit_rate REAL NOT NULL DEFAULT 0.0,
			cache_consecutive_misses INTEGER NOT NULL DEFAULT 0,
			total_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_output_tokens INTEGER NOT NULL DEFAULT 0,
			total_cache_read INTEGER NOT NULL DEFAULT 0,
			total_cache_write INTEGER NOT NULL DEFAULT 0,
			last_provider TEXT,
			last_model TEXT,
			active_tool_calls INTEGER NOT NULL DEFAULT 0,
			last_error TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			actor_user_id INTEGER NOT NULL DEFAULT 0,
			actor_role TEXT NOT NULL DEFAULT '',
			event_origin TEXT NOT NULL DEFAULT '',
			event_origin_detail TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL CHECK(role IN ('user', 'assistant', 'tool')),
			content TEXT NOT NULL,
			floor_content TEXT,
			floor_metadata TEXT,
			tool_calls TEXT,
			tool_id TEXT,
			tool_name TEXT,
			thinking TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			turn_index INTEGER NOT NULL,
			content_chars INTEGER NOT NULL DEFAULT 0,
			compacted INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS outbound_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			turn_index INTEGER NOT NULL,
			telegram_msg_id INTEGER NOT NULL,
			msg_type TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS review_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_session_id TEXT,
			source_chat_id INTEGER NOT NULL DEFAULT 0,
			source_user_id INTEGER NOT NULL DEFAULT 0,
			source_role TEXT NOT NULL,
			source_scope_kind TEXT NOT NULL DEFAULT '',
			source_scope_id TEXT NOT NULL DEFAULT '',
			source_durable_agent_id TEXT NOT NULL DEFAULT '',
			target_session_id TEXT,
			target_chat_id INTEGER NOT NULL DEFAULT 0,
			target_scope_kind TEXT NOT NULL DEFAULT '',
			target_scope_id TEXT NOT NULL DEFAULT '',
			target_durable_agent_id TEXT NOT NULL DEFAULT '',
			turn_from INTEGER,
			turn_to INTEGER,
			summary TEXT NOT NULL,
			metadata_json TEXT,
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending', 'delivered', 'dismissed')),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			delivered_at TEXT,
			delivery_message_id INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS plan_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			event_kind TEXT NOT NULL,
			plan_state_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS registered_tools (
			tool_name TEXT PRIMARY KEY,
			implementation_ref TEXT NOT NULL DEFAULT '',
			registered INTEGER NOT NULL DEFAULT 0 CHECK(registered IN (0, 1)),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS model_slot_overrides (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			slot TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			previous_config_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'superseded', 'cleared')),
			created_by TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_model_slot_overrides_slot_status ON model_slot_overrides(slot, status, id DESC)`,
		`CREATE TABLE IF NOT EXISTS capability_requests (
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
		`CREATE INDEX IF NOT EXISTS idx_capability_requests_status ON capability_requests(review_status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_requests_kind ON capability_requests(kind, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_requests_principals ON capability_requests(requested_by, requested_for, parent_principal, admin_principal)`,
		`CREATE TABLE IF NOT EXISTS durable_child_agreements (
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
		)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_agreements_agent ON durable_child_agreements(agent_id, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_durable_child_agreements_request ON durable_child_agreements(source_request_id)`,
		`CREATE TABLE IF NOT EXISTS capability_reviews (
			review_id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL,
			reviewer TEXT NOT NULL DEFAULT '',
			reviewer_role TEXT NOT NULL DEFAULT '',
			review_status TEXT NOT NULL CHECK(review_status IN ('proposed', 'parent_approved', 'approved', 'rejected')),
			rationale TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_reviews_request ON capability_reviews(request_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS capability_grants (
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
		`CREATE INDEX IF NOT EXISTS idx_capability_grants_lookup ON capability_grants(kind, target_resource, granted_to, status)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_grants_status ON capability_grants(status, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS capability_invocations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			grant_id TEXT NOT NULL,
			principal TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			error_text TEXT NOT NULL DEFAULT '',
			outcome_status TEXT NOT NULL DEFAULT '',
			outcome_error_text TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			turn_run_id INTEGER NOT NULL DEFAULT 0,
			continuation_lease_id TEXT NOT NULL DEFAULT '',
			operation_plan_lease_id TEXT NOT NULL DEFAULT '',
			authority_source TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_invocations_grant ON capability_invocations(grant_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_invocations_authority_session ON capability_invocations(session_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_capability_invocations_lease ON capability_invocations(continuation_lease_id, operation_plan_lease_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS execution_run_authority (
			turn_run_id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			principal TEXT NOT NULL DEFAULT '',
			principal_role TEXT NOT NULL DEFAULT '',
			execution_species TEXT NOT NULL DEFAULT '',
			lease_kind TEXT NOT NULL CHECK(lease_kind IN ('continuation_lease', 'operation_plan_lease')),
			continuation_lease_id TEXT NOT NULL DEFAULT '',
			operation_plan_lease_id TEXT NOT NULL DEFAULT '',
			lease_status TEXT NOT NULL DEFAULT '',
			lease_remaining_turns INTEGER NOT NULL DEFAULT 0,
			lease_expires_at TEXT,
			admitted_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (turn_run_id) REFERENCES turn_runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_run_authority_session ON execution_run_authority(session_id, admitted_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_run_authority_lease ON execution_run_authority(lease_kind, continuation_lease_id, operation_plan_lease_id)`,
		`CREATE TABLE IF NOT EXISTS tool_install_records (
			tool_name TEXT PRIMARY KEY,
			installer TEXT NOT NULL DEFAULT '',
			install_ref TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '' CHECK(status IN ('', 'pending', 'installed', 'verified', 'failed', 'stale')),
			probe_status TEXT NOT NULL DEFAULT '' CHECK(probe_status IN ('', 'passed', 'failed')),
				probe_output TEXT NOT NULL DEFAULT '',
				rationale TEXT NOT NULL DEFAULT '',
				artifact_refs_json TEXT NOT NULL DEFAULT '[]',
				baseline_fingerprint TEXT NOT NULL DEFAULT '',
				current_fingerprint TEXT NOT NULL DEFAULT '',
				baseline_install_ref TEXT NOT NULL DEFAULT '',
				current_install_ref TEXT NOT NULL DEFAULT '',
				baseline_manifest_hash TEXT NOT NULL DEFAULT '',
				current_manifest_hash TEXT NOT NULL DEFAULT '',
				baseline_workspace_fingerprint TEXT NOT NULL DEFAULT '',
				current_workspace_fingerprint TEXT NOT NULL DEFAULT '',
				stale_reason TEXT NOT NULL DEFAULT '',
				drift_source TEXT NOT NULL DEFAULT '',
				consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			installed_at TEXT,
			last_probed_at TEXT,
			last_failure_at TEXT,
			attested_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS tool_probe_records (
			tool_name TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT '' CHECK(status IN ('', 'passed', 'failed')),
			probe_output TEXT NOT NULL DEFAULT '',
			rationale TEXT NOT NULL DEFAULT '',
			artifact_refs_json TEXT NOT NULL DEFAULT '[]',
			baseline_fingerprint TEXT NOT NULL DEFAULT '',
			current_fingerprint TEXT NOT NULL DEFAULT '',
			baseline_install_ref TEXT NOT NULL DEFAULT '',
			current_install_ref TEXT NOT NULL DEFAULT '',
			baseline_manifest_hash TEXT NOT NULL DEFAULT '',
			current_manifest_hash TEXT NOT NULL DEFAULT '',
			baseline_workspace_fingerprint TEXT NOT NULL DEFAULT '',
			current_workspace_fingerprint TEXT NOT NULL DEFAULT '',
			stale_reason TEXT NOT NULL DEFAULT '',
			drift_source TEXT NOT NULL DEFAULT '',
			consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			probed_at TEXT,
			last_failure_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS tool_audit_records (
			tool_name TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT '' CHECK(status IN ('', 'passed', 'failed')),
				audit_output TEXT NOT NULL DEFAULT '',
				rationale TEXT NOT NULL DEFAULT '',
				artifact_refs_json TEXT NOT NULL DEFAULT '[]',
				baseline_fingerprint TEXT NOT NULL DEFAULT '',
				current_fingerprint TEXT NOT NULL DEFAULT '',
				baseline_install_ref TEXT NOT NULL DEFAULT '',
				current_install_ref TEXT NOT NULL DEFAULT '',
				baseline_manifest_hash TEXT NOT NULL DEFAULT '',
				current_manifest_hash TEXT NOT NULL DEFAULT '',
				baseline_workspace_fingerprint TEXT NOT NULL DEFAULT '',
				current_workspace_fingerprint TEXT NOT NULL DEFAULT '',
				stale_reason TEXT NOT NULL DEFAULT '',
				drift_source TEXT NOT NULL DEFAULT '',
				consecutive_failures INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now')),
			audited_at TEXT,
			last_failure_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS turn_runs (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL,
			turn_index INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'failed', 'interrupted')),
			request_text TEXT NOT NULL,
			started_at TEXT NOT NULL DEFAULT (datetime('now')),
			completed_at TEXT,
			last_activity_at TEXT NOT NULL DEFAULT (datetime('now')),
			last_tool_name TEXT,
			last_tool_preview TEXT,
			tool_calls_started INTEGER NOT NULL DEFAULT 0,
			tool_calls_finished INTEGER NOT NULL DEFAULT 0,
			total_tool_chars_in INTEGER NOT NULL DEFAULT 0,
			total_assistant_chars_out INTEGER NOT NULL DEFAULT 0,
			provider_input_tokens INTEGER NOT NULL DEFAULT 0,
			provider_output_tokens INTEGER NOT NULL DEFAULT 0,
			provider_cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			provider_cache_write_tokens INTEGER NOT NULL DEFAULT 0,
			last_tool_result_preview TEXT,
			last_tool_error TEXT,
			progress_message_id INTEGER,
				error_text TEXT,
				recovery_summary TEXT,
				recovery_logged_at TEXT
			)`,
		`CREATE TABLE IF NOT EXISTS turn_progress_views (
				run_id INTEGER PRIMARY KEY,
				message_id INTEGER NOT NULL DEFAULT 0,
				selected_view TEXT NOT NULL DEFAULT 'summary' CHECK(selected_view IN ('summary', 'details')),
				summary_text TEXT NOT NULL DEFAULT '',
				details_text TEXT NOT NULL DEFAULT '',
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (run_id) REFERENCES turn_runs(id) ON DELETE CASCADE
			)`,
		`CREATE TABLE IF NOT EXISTS execution_events (
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
			)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS pending_decisions (
			decision_id TEXT PRIMARY KEY,
			decision_seq INTEGER NOT NULL DEFAULT 0,
			owner_key TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			message_id INTEGER NOT NULL DEFAULT 0,
			prompt TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			rationale TEXT NOT NULL DEFAULT '',
			artifact_refs_json TEXT NOT NULL DEFAULT '[]',
			choices_json TEXT NOT NULL DEFAULT '[]',
			default_choice TEXT NOT NULL DEFAULT '',
			timeout_ns INTEGER NOT NULL DEFAULT 0,
			delivery_message_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pending_decisions_owner_seq ON pending_decisions(owner_key, decision_seq DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_pending_decisions_session_seq ON pending_decisions(session_id, decision_seq DESC)`,
		`CREATE TABLE IF NOT EXISTS operator_auto_approvals (
			lease_id TEXT PRIMARY KEY,
			admin_user_id INTEGER NOT NULL DEFAULT 0,
			chat_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
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
		`CREATE INDEX IF NOT EXISTS idx_operator_auto_approvals_scope_active ON operator_auto_approvals(chat_id, scope_kind, scope_id, expires_at DESC, revoked_at, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS approval_window_offers (
			offer_id TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			admin_user_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			source_kind TEXT NOT NULL DEFAULT '',
			source_id TEXT NOT NULL DEFAULT '',
			source_decision_kind TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL,
			used_at TEXT,
			opened_lease_id TEXT NOT NULL DEFAULT '',
			opened_override_id TEXT NOT NULL DEFAULT '',
			closed_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_approval_window_offers_source_active ON approval_window_offers(chat_id, source_kind, source_id, expires_at DESC, closed_at, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_approval_window_offers_scope_active ON approval_window_offers(chat_id, scope_kind, scope_id, expires_at DESC, closed_at, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS operator_autonomy_overrides (
			override_id TEXT PRIMARY KEY,
			admin_user_id INTEGER NOT NULL DEFAULT 0,
			chat_id INTEGER NOT NULL DEFAULT 0,
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'leased',
			scope TEXT NOT NULL DEFAULT 'all',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_operator_autonomy_overrides_chat_active ON operator_autonomy_overrides(chat_id, mode, expires_at DESC, revoked_at, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_operator_autonomy_overrides_admin_active ON operator_autonomy_overrides(admin_user_id, mode, expires_at DESC, revoked_at, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_operator_autonomy_overrides_scope_active ON operator_autonomy_overrides(chat_id, scope_kind, scope_id, mode, expires_at DESC, revoked_at, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS pending_artifact_retention (
			owner_key TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			message_id INTEGER NOT NULL DEFAULT 0,
			inbound_message_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pending_artifact_retention_message ON pending_artifact_retention(chat_id, sender_id, message_id)`,
		`CREATE TABLE IF NOT EXISTS pending_busy_decisions (
			owner_key TEXT PRIMARY KEY,
			chat_id INTEGER NOT NULL DEFAULT 0,
			sender_id INTEGER NOT NULL DEFAULT 0,
			session_id TEXT NOT NULL DEFAULT '',
			scope_kind TEXT NOT NULL DEFAULT '',
			scope_id TEXT NOT NULL DEFAULT '',
			durable_agent_id TEXT NOT NULL DEFAULT '',
			message_id INTEGER NOT NULL DEFAULT 0,
			inbound_message_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_pending_busy_decisions_session ON pending_busy_decisions(session_id, updated_at DESC)`,
		`CREATE TABLE IF NOT EXISTS durable_agents (
			agent_id TEXT PRIMARY KEY,
			parent_agent_id TEXT,
			parent_scope_kind TEXT,
			parent_scope_id TEXT,
			review_target_chat_id INTEGER NOT NULL DEFAULT 0,
			channel_kind TEXT NOT NULL,
			live_policy_json TEXT NOT NULL DEFAULT '{}',
			channel_config_json TEXT NOT NULL DEFAULT '{}',
			bootstrap_ceiling_json TEXT NOT NULL DEFAULT '{}',
			bootstrap_provider_json TEXT NOT NULL DEFAULT '{}',
			control_plane_secret TEXT NOT NULL DEFAULT '',
			policy_version INTEGER NOT NULL DEFAULT 1,
			policy_hash TEXT NOT NULL DEFAULT '',
			policy_issued_at TEXT,
			local_storage_roots_json TEXT NOT NULL DEFAULT '[]',
			network_policy TEXT,
			wakeup_mode TEXT,
			secret_scopes_json TEXT NOT NULL DEFAULT '[]',
			allowed_telegram_user_ids_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_tombstones (
			agent_id TEXT PRIMARY KEY,
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_state (
				agent_id TEXT PRIMARY KEY,
				cursor TEXT,
				status TEXT,
				state_json TEXT,
				last_apply_status TEXT NOT NULL DEFAULT '',
				last_apply_error TEXT NOT NULL DEFAULT '',
				last_wake_at TEXT,
				last_review_at TEXT,
				dormant_at TEXT,
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
			)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_identity_state (
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
			)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_policy_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			source_review_event_id INTEGER NOT NULL DEFAULT 0,
			previous_version INTEGER NOT NULL DEFAULT 0,
			new_version INTEGER NOT NULL,
			policy_hash TEXT NOT NULL,
			policy_json TEXT NOT NULL,
			reason TEXT,
			applied_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_bootstrap_updates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			source_review_event_id INTEGER NOT NULL DEFAULT 0,
			actor_user_id INTEGER NOT NULL DEFAULT 0,
			actor_role TEXT NOT NULL DEFAULT '',
			update_kind TEXT NOT NULL DEFAULT '',
			previous_bootstrap_json TEXT NOT NULL DEFAULT '{}',
			new_bootstrap_json TEXT NOT NULL DEFAULT '{}',
			reason TEXT,
			applied_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_remote_enrollments (
				agent_id TEXT PRIMARY KEY,
				parent_control_url TEXT NOT NULL DEFAULT '',
				protocol_version TEXT NOT NULL DEFAULT 'v1',
				status TEXT NOT NULL DEFAULT 'active',
				last_sequence INTEGER NOT NULL DEFAULT 0,
				enrolled_at TEXT,
				last_seen_at TEXT,
				revoked_at TEXT,
				tailnet_stable_node_id TEXT NOT NULL DEFAULT '',
				tailnet_node_name TEXT NOT NULL DEFAULT '',
				tailnet_computed_name TEXT NOT NULL DEFAULT '',
				tailnet_login_name TEXT NOT NULL DEFAULT '',
				tailnet_tags_json TEXT NOT NULL DEFAULT '[]',
				updated_at TEXT NOT NULL DEFAULT (datetime('now')),
				FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
			)`,
		`CREATE TABLE IF NOT EXISTS durable_agent_control_receipts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			message_kind TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			signature TEXT NOT NULL DEFAULT '',
			received_at TEXT NOT NULL DEFAULT (datetime('now')),
			response_status INTEGER NOT NULL DEFAULT 0,
			response_json TEXT NOT NULL DEFAULT '',
			UNIQUE(agent_id, message_id),
			FOREIGN KEY (agent_id) REFERENCES durable_agents(agent_id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS compaction_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			chat_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			timestamp TEXT NOT NULL DEFAULT (datetime('now')),
			turns_before INTEGER,
			turns_after INTEGER,
			tokens_before INTEGER,
			tokens_after INTEGER,
			summary TEXT,
			strategy TEXT NOT NULL DEFAULT 'summarize'
		)`,
		`CREATE TABLE IF NOT EXISTS rhizome_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			name TEXT NOT NULL,
			event_count INTEGER NOT NULL DEFAULT 0,
			last_seen_at TEXT NOT NULL DEFAULT (datetime('now')),
			UNIQUE(scope, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rhizome_nodes_scope ON rhizome_nodes(scope, name)`,
		`CREATE TABLE IF NOT EXISTS rhizome_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			source TEXT NOT NULL,
			salience REAL NOT NULL DEFAULT 1.0,
			concepts_json TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rhizome_events_scope ON rhizome_events(scope, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS rhizome_edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scope TEXT NOT NULL,
			left_concept TEXT NOT NULL,
			right_concept TEXT NOT NULL,
			strength REAL NOT NULL DEFAULT 0,
			recurrence_count INTEGER NOT NULL DEFAULT 0,
			last_reinforced_at TEXT NOT NULL DEFAULT (datetime('now')),
			decay_state TEXT NOT NULL DEFAULT 'hot',
			last_source TEXT,
			UNIQUE(scope, left_concept, right_concept)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_rhizome_edges_scope ON rhizome_edges(scope, strength DESC, recurrence_count DESC)`,
		`CREATE TABLE IF NOT EXISTS artifact_index (
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
		)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_session_turn ON artifact_index(session_id, turn_index DESC, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_index_summary ON artifact_index(summary, kind, updated_at DESC)`,
	}

	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("apply schema statement: %w", err)
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
	if err := ensureTelegramThreadTables(tx); err != nil {
		return err
	}
	if err := ensureTelegramCallbackMessageTables(tx); err != nil {
		return err
	}
	if err := ensureTelegramMediaPickerTables(tx); err != nil {
		return err
	}
	if err := ensureTelegramAgentMessageTables(tx); err != nil {
		return err
	}
	if err := ensureTelegramThreadPromotionHandoffTables(tx); err != nil {
		return err
	}
	if err := ensureTelegramThreadReminderTables(tx); err != nil {
		return err
	}
	if err := ensureReentryRecommendationTables(tx); err != nil {
		return err
	}
	if err := ensureInteriorSignalTables(tx); err != nil {
		return err
	}
	if err := ensureCuriosityTables(tx); err != nil {
		return err
	}
	if err := ensureEvidenceLedgerTables(tx); err != nil {
		return err
	}
	if err := ensureEffectAttemptTables(tx); err != nil {
		return err
	}
	if err := ensureJudgmentUseTables(tx); err != nil {
		return err
	}
	if err := ensureJudgmentTables(tx); err != nil {
		return err
	}
	for _, stmt := range telegramIngressSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure telegram ingress ledger tables: %w", err)
		}
	}
	if err := ensureSessionIdentityIndexes(tx); err != nil {
		return err
	}
	if err := recordCurrentSchemaVersion(tx, currentVersion); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema tx: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
