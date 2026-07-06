//go:build linux

package session

import (
	"database/sql"
	"fmt"
)

func ensureSessionIdentityIndexes(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_transport_scope ON sessions(chat_id, user_id, scope_kind, scope_id, durable_agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_active ON messages(session_id, compacted, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_outbound_session ON outbound_messages(session_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_review_events_target ON review_events(target_chat_id, status, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_review_events_target_session ON review_events(target_session_id, status, created_at, id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_review_events_idempotency_key ON review_events(idempotency_key) WHERE idempotency_key != ''`,
		`CREATE INDEX IF NOT EXISTS idx_plan_events_session ON plan_events(session_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_registered_tools_state ON registered_tools(registered, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_install_records_status ON tool_install_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_probe_records_status ON tool_probe_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_audit_records_status ON tool_audit_records(status, updated_at, tool_name)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_session ON turn_runs(session_id, started_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_runs_recovery ON turn_runs(status, recovery_logged_at, started_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_turn_progress_views_message ON turn_progress_views(message_id, updated_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_events_session_seq ON execution_events(session_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_chat_created ON execution_events(chat_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_type_created ON execution_events(event_type, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_events_durable_created ON execution_events(durable_agent_id, created_at, id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure session identity index: %w", err)
		}
	}
	if err := ensureTelegramReplyRoutingIndexes(tx); err != nil {
		return err
	}
	return nil
}
