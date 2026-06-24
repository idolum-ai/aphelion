//go:build linux

package session

import (
	"database/sql"
	"fmt"
	"time"
)

func migrateSchemaV43ToV44(tx *sql.Tx) error {
	return ensureDurableAgentRemoteEnrollmentTailnetColumns(tx)
}

func ensureDurableAgentRemoteEnrollmentTailnetColumns(tx *sql.Tx) error {
	for _, column := range []schemaColumnMigration{
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_stable_node_id",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_stable_node_id TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_node_name",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_node_name TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_computed_name",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_computed_name TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_login_name",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_login_name TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_remote_enrollments",
			column:    "tailnet_tags_json",
			statement: `ALTER TABLE durable_agent_remote_enrollments ADD COLUMN tailnet_tags_json TEXT NOT NULL DEFAULT '[]'`,
		},
		{
			table:     "durable_agent_control_receipts",
			column:    "signature",
			statement: `ALTER TABLE durable_agent_control_receipts ADD COLUMN signature TEXT NOT NULL DEFAULT ''`,
		},
		{
			table:     "durable_agent_control_receipts",
			column:    "response_status",
			statement: `ALTER TABLE durable_agent_control_receipts ADD COLUMN response_status INTEGER NOT NULL DEFAULT 0`,
		},
		{
			table:     "durable_agent_control_receipts",
			column:    "response_json",
			statement: `ALTER TABLE durable_agent_control_receipts ADD COLUMN response_json TEXT NOT NULL DEFAULT ''`,
		},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	return nil
}

func migrateSchemaV44ToV45(tx *sql.Tx) error {
	for _, stmt := range []string{
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
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v44 to v45 ensure operator autonomy overrides: %w", err)
		}
	}
	hasAutoApprovals, err := schemaTableExists(tx, "operator_auto_approvals")
	if err != nil {
		return err
	}
	if !hasAutoApprovals {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO operator_autonomy_overrides(
			override_id, admin_user_id, chat_id, mode, scope, reason,
			created_at, expires_at, revoked_at, updated_at
		)
		SELECT 'mode-' || lease_id, admin_user_id, chat_id, 'leased', scope, reason,
			created_at, expires_at, NULL, updated_at
		FROM operator_auto_approvals
		WHERE revoked_at IS NULL
			AND expires_at > ?
			AND (max_uses <= 0 OR used_count < max_uses)
	`, now); err != nil {
		return fmt.Errorf("migrate schema v44 to v45 copy active auto mode gates: %w", err)
	}
	return nil
}

func migrateSchemaV45ToV46(tx *sql.Tx) error {
	for _, stmt := range telegramIngressSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v45 to v46 ensure telegram ingress ledger: %w", err)
		}
	}
	return nil
}

func migrateSchemaV46ToV47(tx *sql.Tx) error {
	for _, stmt := range telegramIngressSchemaStatements() {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v46 to v47 ensure telegram ingress accepted-update ledger: %w", err)
		}
	}
	return nil
}

func migrateSchemaV47ToV48(tx *sql.Tx) error {
	if err := ensureTurnProgressViewTables(tx); err != nil {
		return fmt.Errorf("migrate schema v47 to v48 ensure turn progress views: %w", err)
	}
	return nil
}

func migrateSchemaV48ToV49(tx *sql.Tx) error {
	if err := ensureTelegramThreadTables(tx); err != nil {
		return fmt.Errorf("migrate schema v48 to v49 ensure telegram thread tables: %w", err)
	}
	return nil
}

func migrateSchemaV49ToV50(tx *sql.Tx) error {
	if err := ensureTelegramReplyRoutingIndexes(tx); err != nil {
		return fmt.Errorf("migrate schema v49 to v50 ensure telegram reply routing indexes: %w", err)
	}
	return nil
}

func migrateSchemaV50ToV51(tx *sql.Tx) error {
	if err := ensureScopedDecisionColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v50 to v51 ensure scoped pending decisions: %w", err)
	}
	return nil
}

func migrateSchemaV51ToV52(tx *sql.Tx) error {
	if err := ensureTelegramCallbackMessageTables(tx); err != nil {
		return fmt.Errorf("migrate schema v51 to v52 ensure telegram callback message ledger: %w", err)
	}
	return nil
}

func migrateSchemaV52ToV53(tx *sql.Tx) error {
	if err := ensureOperatorAutoScopeColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v52 to v53 ensure auto scope columns: %w", err)
	}
	if err := ensureTelegramThreadTables(tx); err != nil {
		return fmt.Errorf("migrate schema v52 to v53 ensure telegram thread tables: %w", err)
	}
	return nil
}

func migrateSchemaV53ToV54(tx *sql.Tx) error {
	if err := ensureTelegramThreadSessions(tx); err != nil {
		return fmt.Errorf("migrate schema v53 to v54 ensure telegram thread sessions: %w", err)
	}
	return nil
}

func migrateSchemaV54ToV55(tx *sql.Tx) error {
	if err := ensureApprovalWindowOfferTables(tx); err != nil {
		return fmt.Errorf("migrate schema v54 to v55 ensure approval window offers: %w", err)
	}
	return nil
}

func migrateSchemaV55ToV56(tx *sql.Tx) error {
	if err := ensureTelegramThreadPromotionHandoffTables(tx); err != nil {
		return fmt.Errorf("migrate schema v55 to v56 ensure telegram thread promotion handoffs: %w", err)
	}
	return nil
}

func migrateSchemaV57ToV58(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "model_slot_overrides")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	hasExpiresAt, err := schemaColumnExists(tx, "model_slot_overrides", "expires_at")
	if err != nil {
		return err
	}
	if !hasExpiresAt {
		return nil
	}
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_model_slot_overrides_slot_status`,
		`ALTER TABLE model_slot_overrides RENAME TO model_slot_overrides_v57`,
		`CREATE TABLE model_slot_overrides (
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
		`INSERT INTO model_slot_overrides (
			id, slot, config_json, previous_config_json, status, created_by, reason, created_at, updated_at
		)
		SELECT
			id,
			slot,
			config_json,
			previous_config_json,
			CASE
				WHEN status IN ('expired', 'rolled_back') THEN 'cleared'
				WHEN status = 'active' AND TRIM(COALESCE(expires_at, '')) != '' THEN 'cleared'
				ELSE status
			END,
			created_by,
			reason,
			created_at,
			updated_at
		FROM model_slot_overrides_v57`,
		`DROP TABLE model_slot_overrides_v57`,
		`CREATE INDEX IF NOT EXISTS idx_model_slot_overrides_slot_status ON model_slot_overrides(slot, status, id DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("migrate schema v57 to v58 model slot overrides: %w", err)
		}
	}
	return nil
}

func migrateSchemaV58ToV59(tx *sql.Tx) error {
	if err := ensureTelegramAgentMessageTables(tx); err != nil {
		return fmt.Errorf("migrate schema v58 to v59 ensure telegram agent message ledger: %w", err)
	}
	return nil
}

func migrateSchemaV59ToV60(tx *sql.Tx) error {
	if err := ensureMissionLedgerTables(tx); err != nil {
		return fmt.Errorf("migrate schema v59 to v60 ensure mission ask prompts: %w", err)
	}
	return nil
}

func migrateSchemaV60ToV61(tx *sql.Tx) error {
	if err := ensureApprovalWindowOfferOpenedColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v60 to v61 ensure approval window offer opened columns: %w", err)
	}
	return nil
}

func migrateSchemaV61ToV62(tx *sql.Tx) error {
	if err := ensureTelegramThreadReminderTables(tx); err != nil {
		return fmt.Errorf("migrate schema v61 to v62 ensure telegram thread reminders: %w", err)
	}
	return nil
}

func migrateSchemaV62ToV63(tx *sql.Tx) error {
	return ensureReviewEventDeliveryMessageID(tx)
}

func ensureReviewEventDeliveryMessageID(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "review_events")
	if err != nil {
		return fmt.Errorf("inspect review_events delivery_message_id column: %w", err)
	}
	if !exists {
		return nil
	}
	return addSchemaColumnIfMissing(tx, schemaColumnMigration{
		table:     "review_events",
		column:    "delivery_message_id",
		statement: `ALTER TABLE review_events ADD COLUMN delivery_message_id INTEGER NOT NULL DEFAULT 0`,
	})
}

func migrateSchemaV63ToV64(tx *sql.Tx) error {
	if err := ensureTurnRunAccountingColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v63 to v64 ensure turn run accounting columns: %w", err)
	}
	return nil
}

func migrateSchemaV64ToV65(tx *sql.Tx) error {
	if err := ensureReentryRecommendationTables(tx); err != nil {
		return fmt.Errorf("migrate schema v64 to v65 ensure reentry recommendations: %w", err)
	}
	return nil
}

func migrateSchemaV65ToV66(tx *sql.Tx) error {
	if err := ensureInteriorSignalTables(tx); err != nil {
		return fmt.Errorf("migrate schema v65 to v66 ensure interior signals: %w", err)
	}
	return nil
}

func migrateSchemaV66ToV67(tx *sql.Tx) error {
	if err := ensureCuriosityTables(tx); err != nil {
		return fmt.Errorf("migrate schema v66 to v67 ensure curiosity tables: %w", err)
	}
	return nil
}

func migrateSchemaV67ToV68(tx *sql.Tx) error {
	if err := ensureTelegramMediaPickerSourceIngressColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v67 to v68 ensure media picker source ingress columns: %w", err)
	}
	return nil
}

func migrateSchemaV70ToV71(tx *sql.Tx) error {
	if err := ensureExecutionRunAuthorityTables(tx); err != nil {
		return fmt.Errorf("migrate schema v70 to v71 ensure execution run authority tables: %w", err)
	}
	return nil
}

func migrateSchemaV71ToV72(tx *sql.Tx) error {
	if err := ensureCapabilityInvocationOutcomeColumns(tx); err != nil {
		return fmt.Errorf("migrate schema v71 to v72 ensure capability invocation outcomes: %w", err)
	}
	return nil
}

func ensureCapabilityInvocationOutcomeColumns(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "capability_invocations")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, column := range []schemaColumnMigration{
		{table: "capability_invocations", column: "outcome_status", statement: `ALTER TABLE capability_invocations ADD COLUMN outcome_status TEXT NOT NULL DEFAULT ''`},
		{table: "capability_invocations", column: "outcome_error_text", statement: `ALTER TABLE capability_invocations ADD COLUMN outcome_error_text TEXT NOT NULL DEFAULT ''`},
		{table: "capability_invocations", column: "completed_at", statement: `ALTER TABLE capability_invocations ADD COLUMN completed_at TEXT`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		UPDATE capability_invocations
		SET
			outcome_status = CASE
				WHEN outcome_status != '' THEN outcome_status
				WHEN status = 'allowed' THEN 'pending'
				WHEN status != '' THEN status
				ELSE 'succeeded'
			END,
			outcome_error_text = CASE
				WHEN outcome_error_text != '' THEN outcome_error_text
				ELSE error_text
			END,
			completed_at = CASE
				WHEN completed_at IS NOT NULL AND completed_at != '' THEN completed_at
				WHEN status = 'allowed' THEN completed_at
				ELSE created_at
			END
		WHERE outcome_status = ''
	`); err != nil {
		return fmt.Errorf("backfill capability invocation outcomes: %w", err)
	}
	return nil
}

func ensureExecutionRunAuthorityTables(tx *sql.Tx) error {
	for _, stmt := range []string{
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
			lease_class TEXT NOT NULL DEFAULT '',
			lease_allowed_actions_json TEXT NOT NULL DEFAULT '[]',
			lease_constraints_json TEXT NOT NULL DEFAULT '{}',
			lease_remaining_turns INTEGER NOT NULL DEFAULT 0,
			lease_expires_at TEXT,
			admitted_at TEXT NOT NULL DEFAULT (datetime('now')),
			FOREIGN KEY (turn_run_id) REFERENCES turn_runs(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_run_authority_session ON execution_run_authority(session_id, admitted_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_run_authority_lease ON execution_run_authority(lease_kind, continuation_lease_id, operation_plan_lease_id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	for _, column := range []schemaColumnMigration{
		{table: "execution_run_authority", column: "lease_class", statement: `ALTER TABLE execution_run_authority ADD COLUMN lease_class TEXT NOT NULL DEFAULT ''`},
		{table: "execution_run_authority", column: "lease_allowed_actions_json", statement: `ALTER TABLE execution_run_authority ADD COLUMN lease_allowed_actions_json TEXT NOT NULL DEFAULT '[]'`},
		{table: "execution_run_authority", column: "lease_constraints_json", statement: `ALTER TABLE execution_run_authority ADD COLUMN lease_constraints_json TEXT NOT NULL DEFAULT '{}'`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}
	return nil
}

func ensureTurnRunAccountingColumns(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "turn_runs")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	for _, column := range []schemaColumnMigration{
		{table: "turn_runs", column: "turn_index", statement: `ALTER TABLE turn_runs ADD COLUMN turn_index INTEGER NOT NULL DEFAULT 0`},
		{table: "turn_runs", column: "total_tool_chars_in", statement: `ALTER TABLE turn_runs ADD COLUMN total_tool_chars_in INTEGER NOT NULL DEFAULT 0`},
		{table: "turn_runs", column: "total_assistant_chars_out", statement: `ALTER TABLE turn_runs ADD COLUMN total_assistant_chars_out INTEGER NOT NULL DEFAULT 0`},
		{table: "turn_runs", column: "provider_input_tokens", statement: `ALTER TABLE turn_runs ADD COLUMN provider_input_tokens INTEGER NOT NULL DEFAULT 0`},
		{table: "turn_runs", column: "provider_output_tokens", statement: `ALTER TABLE turn_runs ADD COLUMN provider_output_tokens INTEGER NOT NULL DEFAULT 0`},
		{table: "turn_runs", column: "provider_cache_read_tokens", statement: `ALTER TABLE turn_runs ADD COLUMN provider_cache_read_tokens INTEGER NOT NULL DEFAULT 0`},
		{table: "turn_runs", column: "provider_cache_write_tokens", statement: `ALTER TABLE turn_runs ADD COLUMN provider_cache_write_tokens INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := addSchemaColumnIfMissing(tx, column); err != nil {
			return err
		}
	}

	messagesExists, err := schemaTableExists(tx, "messages")
	if err != nil {
		return err
	}
	if !messagesExists {
		return nil
	}
	if _, err := tx.Exec(`
		UPDATE turn_runs
		SET
			total_tool_chars_in = COALESCE((
				SELECT SUM(content_chars)
				FROM messages
				WHERE messages.session_id = turn_runs.session_id
					AND messages.turn_index = turn_runs.turn_index
					AND messages.role = 'tool'
			), 0),
			total_assistant_chars_out = COALESCE((
				SELECT SUM(content_chars)
				FROM messages
				WHERE messages.session_id = turn_runs.session_id
					AND messages.turn_index = turn_runs.turn_index
					AND messages.role = 'assistant'
			), 0)
		WHERE total_tool_chars_in = 0
			AND total_assistant_chars_out = 0
			AND turn_index > 0
	`); err != nil {
		return fmt.Errorf("backfill turn run char accounting: %w", err)
	}
	return nil
}

func ensureCurrentSchemaShapeRepairColumns(tx *sql.Tx) error {
	repairs := []struct {
		name string
		fn   func(*sql.Tx) error
	}{
		{name: "durable agent remote tailnet columns", fn: ensureDurableAgentRemoteEnrollmentTailnetColumns},
		{name: "operator auto scope columns", fn: ensureOperatorAutoScopeColumns},
		{name: "scoped decision columns", fn: ensureScopedDecisionColumns},
		{name: "approval window offer opened columns", fn: ensureApprovalWindowOfferOpenedColumns},
		{name: "review event delivery message id", fn: ensureReviewEventDeliveryMessageID},
		{name: "turn run accounting columns", fn: ensureTurnRunAccountingColumns},
		{name: "telegram media picker ingress columns", fn: ensureTelegramMediaPickerSourceIngressColumns},
		{name: "capability invocation outcome columns", fn: ensureCapabilityInvocationOutcomeColumns},
		{name: "next action operation columns", fn: ensureNextActionOperationColumns},
		{name: "child task lease columns", fn: ensureChildTaskLeaseColumns},
		{name: "review event idempotency key", fn: ensureReviewEventIdempotencyKey},
	}
	for _, repair := range repairs {
		if err := repair.fn(tx); err != nil {
			return fmt.Errorf("ensure current schema shape %s: %w", repair.name, err)
		}
	}
	return nil
}
