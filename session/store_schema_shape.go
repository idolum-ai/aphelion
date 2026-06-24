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
		{"next_action_records", "operation_kind"},
		{"next_action_records", "operation_tool"},
		{"next_action_records", "operation_input_json"},
		{"review_events", "idempotency_key"},
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
