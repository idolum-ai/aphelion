//go:build linux

package session

import "fmt"

func (s *SQLiteStore) VerifyCriticalSchemaShape() (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("schema shape check requires an open store")
	}
	checks := []struct {
		table  string
		column string
	}{
		{"approval_window_offers", "opened_lease_id"},
		{"approval_window_offers", "opened_override_id"},
		{"capability_invocations", "outcome_status"},
		{"capability_invocations", "outcome_error_text"},
		{"capability_invocations", "completed_at"},
		{"durable_agent_control_receipts", "signature"},
		{"durable_agent_control_receipts", "response_status"},
		{"durable_agent_control_receipts", "response_json"},
		{"durable_agent_remote_enrollments", "tailnet_stable_node_id"},
		{"durable_agent_remote_enrollments", "tailnet_node_name"},
		{"durable_agent_remote_enrollments", "tailnet_computed_name"},
		{"durable_agent_remote_enrollments", "tailnet_login_name"},
		{"durable_agent_remote_enrollments", "tailnet_tags_json"},
		{"next_action_records", "operation_kind"},
		{"next_action_records", "operation_tool"},
		{"next_action_records", "operation_input_json"},
		{"operator_auto_approvals", "scope_kind"},
		{"operator_auto_approvals", "scope_id"},
		{"operator_autonomy_overrides", "scope_kind"},
		{"operator_autonomy_overrides", "scope_id"},
		{"pending_artifact_retention", "session_id"},
		{"pending_artifact_retention", "scope_kind"},
		{"pending_artifact_retention", "scope_id"},
		{"pending_artifact_retention", "durable_agent_id"},
		{"pending_artifact_retention", "message_id"},
		{"pending_busy_decisions", "session_id"},
		{"pending_busy_decisions", "scope_kind"},
		{"pending_busy_decisions", "scope_id"},
		{"pending_busy_decisions", "durable_agent_id"},
		{"pending_busy_decisions", "message_id"},
		{"pending_decisions", "session_id"},
		{"pending_decisions", "scope_kind"},
		{"pending_decisions", "scope_id"},
		{"pending_decisions", "durable_agent_id"},
		{"review_events", "delivery_message_id"},
		{"review_events", "idempotency_key"},
		{"telegram_media_thread_pickers", "source_ingress_surface"},
		{"telegram_media_thread_pickers", "source_ingress_update_id"},
		{"telegram_threads", "display_slot"},
		{"telegram_threads", "archived_display_name"},
		{"telegram_thread_promotion_handoffs", "proposed_child_json"},
		{"telegram_thread_promotion_handoffs", "first_task"},
		{"telegram_thread_promotion_handoffs", "validation_json"},
		{"turn_runs", "turn_index"},
		{"turn_runs", "total_tool_chars_in"},
		{"turn_runs", "total_assistant_chars_out"},
		{"turn_runs", "provider_input_tokens"},
		{"turn_runs", "provider_output_tokens"},
		{"turn_runs", "provider_cache_read_tokens"},
		{"turn_runs", "provider_cache_write_tokens"},
		{"child_task_packets", "active_attempt_id"},
		{"child_task_packets", "lease_owner"},
		{"child_task_packets", "lease_generation"},
		{"child_task_packets", "fencing_token"},
		{"child_task_packets", "lease_expires_at"},
		{"child_task_packets", "lease_heartbeat_at"},
		{"child_task_packets", "lease_released_at"},
		{"child_task_results", "attempt_id"},
		{"child_task_results", "lease_owner"},
		{"child_task_results", "lease_generation"},
		{"child_task_results", "fencing_token"},
		{"child_task_results", "intent_set_fingerprint"},
		{"child_task_outcome_intents", "sequence"},
		{"child_task_outcome_intents", "idempotency_key"},
		{"child_task_outcome_intents", "lease_owner"},
		{"child_task_outcome_intents", "lease_generation"},
		{"child_task_outcome_intents", "fencing_token"},
		{"child_task_outcome_intents", "lease_expires_at"},
		{"child_task_outcome_intents", "next_attempt_at"},
		{"child_task_outcome_intents", "dead_letter_at"},
	}
	for _, check := range checks {
		var count int
		query := "SELECT COUNT(1) FROM pragma_table_info(" + sqliteStringLiteral(check.table) + ") WHERE name = ?"
		if err := s.db.QueryRow(query, check.column).Scan(&count); err != nil {
			return "", fmt.Errorf("inspect critical schema column %s.%s: %w", check.table, check.column, err)
		}
		if count == 0 {
			return "", fmt.Errorf("critical schema column missing: %s.%s", check.table, check.column)
		}
	}
	var version int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version); err != nil {
		return "", fmt.Errorf("inspect schema version: %w", err)
	}
	return fmt.Sprintf("schema_version=%d critical_columns=%d", version, len(checks)), nil
}
