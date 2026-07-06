//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func ensureContinuationRecoveryContractTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS continuation_recovery_contracts (
			contract_id TEXT PRIMARY KEY,
			contract_version TEXT NOT NULL DEFAULT '',
			request_instance_id TEXT NOT NULL DEFAULT '',
			contract_hash TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			subject_kind TEXT NOT NULL DEFAULT '',
			subject_ref TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			principal TEXT NOT NULL DEFAULT '',
			lease_class TEXT NOT NULL DEFAULT '',
			allowed_actions_json TEXT NOT NULL DEFAULT '[]',
			constraints_json TEXT NOT NULL DEFAULT '{}',
			tool TEXT NOT NULL DEFAULT '',
			tool_action TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			resource TEXT NOT NULL DEFAULT '',
			grant_id TEXT NOT NULL DEFAULT '',
			grant_target_resource TEXT NOT NULL DEFAULT '',
			retry_operation_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_continuation_recovery_contracts_session ON continuation_recovery_contracts(session_id, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_continuation_recovery_contracts_subject ON continuation_recovery_contracts(session_id, subject_kind, subject_ref, status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_continuation_recovery_contracts_request ON continuation_recovery_contracts(request_instance_id, updated_at DESC)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("ensure continuation recovery contract tables: %w", err)
		}
	}
	return nil
}

func migrateSchemaV82ToV83(tx *sql.Tx) error {
	if err := ensureContinuationRecoveryContractTables(tx); err != nil {
		return err
	}
	return migrateLegacyContinuationRecoveryNextActions(tx)
}

func migrateSchemaV86ToV87(tx *sql.Tx) error {
	return terminalizeLegacyContinuationRecoveryContracts(tx)
}

func terminalizeLegacyContinuationRecoveryContracts(tx *sql.Tx) error {
	if exists, err := schemaTableExists(tx, "continuation_recovery_contracts"); err != nil {
		return err
	} else if !exists {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := tx.Query(`
		SELECT contract_id
		FROM continuation_recovery_contracts
		WHERE contract_version = ?
			AND status NOT IN (?, ?)
	`, ContinuationRecoveryContractVersionV1, string(ContinuationRecoveryContractStatusTerminal), string(ContinuationRecoveryContractStatusSuperseded))
	if err != nil {
		return fmt.Errorf("query legacy continuation recovery contracts: %w", err)
	}
	var contractIDs []string
	for rows.Next() {
		var contractID string
		if err := rows.Scan(&contractID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy continuation recovery contract: %w", err)
		}
		contractID = strings.TrimSpace(contractID)
		if contractID != "" {
			contractIDs = append(contractIDs, contractID)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy continuation recovery contract rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy continuation recovery contracts: %w", err)
	}
	if len(contractIDs) == 0 {
		return nil
	}
	if _, err := tx.Exec(`
		UPDATE continuation_recovery_contracts
		SET status = ?, updated_at = ?
		WHERE contract_version = ?
			AND status NOT IN (?, ?)
	`, string(ContinuationRecoveryContractStatusTerminal), now, ContinuationRecoveryContractVersionV1, string(ContinuationRecoveryContractStatusTerminal), string(ContinuationRecoveryContractStatusSuperseded)); err != nil {
		return fmt.Errorf("terminalize legacy continuation recovery contracts: %w", err)
	}
	if exists, err := schemaTableExists(tx, "next_action_records"); err != nil {
		return err
	} else if !exists {
		return nil
	}
	legacyContractIDs := make(map[string]struct{}, len(contractIDs))
	for _, contractID := range contractIDs {
		legacyContractIDs[contractID] = struct{}{}
	}
	actionRows, err := tx.Query(`
		SELECT record_id, operation_input_json
		FROM next_action_records
		WHERE resolved_at IS NULL
			AND operation_tool = 'request_approval'
			AND operation_kind = 'continuation_lease_request'
	`)
	if err != nil {
		return fmt.Errorf("query legacy continuation recovery handoffs: %w", err)
	}
	var recordIDs []string
	for actionRows.Next() {
		var recordID string
		var rawInput string
		if err := actionRows.Scan(&recordID, &rawInput); err != nil {
			_ = actionRows.Close()
			return fmt.Errorf("scan legacy continuation recovery handoff: %w", err)
		}
		contractID := legacyContinuationRecoveryHandoffContractID(rawInput)
		if _, ok := legacyContractIDs[contractID]; ok {
			recordIDs = append(recordIDs, strings.TrimSpace(recordID))
		}
	}
	if err := actionRows.Close(); err != nil {
		return fmt.Errorf("close legacy continuation recovery handoff rows: %w", err)
	}
	if err := actionRows.Err(); err != nil {
		return fmt.Errorf("iterate legacy continuation recovery handoffs: %w", err)
	}
	for _, recordID := range recordIDs {
		if strings.TrimSpace(recordID) == "" {
			continue
		}
		if _, err := tx.Exec(`
			UPDATE next_action_records
			SET state = ?, resolved_at = ?
			WHERE record_id = ?
				AND resolved_at IS NULL
		`, string(NextActionSuperseded), now, recordID); err != nil {
			return fmt.Errorf("resolve legacy continuation recovery handoff %s: %w", recordID, err)
		}
	}
	return nil
}

func legacyContinuationRecoveryHandoffContractID(rawInput string) string {
	rawInput = strings.TrimSpace(rawInput)
	if rawInput == "" {
		return ""
	}
	var payload struct {
		ContractID string `json:"contract_id"`
	}
	if err := json.Unmarshal([]byte(rawInput), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.ContractID)
}
