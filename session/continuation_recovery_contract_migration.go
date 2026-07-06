//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type legacyContinuationRecoveryHandoffInput struct {
	Action                string                     `json:"action"`
	LeaseClass            string                     `json:"lease_class"`
	Principal             string                     `json:"principal"`
	AllowedActions        []string                   `json:"allowed_actions"`
	Constraints           map[string]string          `json:"constraints"`
	Tool                  string                     `json:"tool"`
	ToolAction            string                     `json:"tool_action"`
	GrantID               string                     `json:"grant_id"`
	GrantTargetResource   string                     `json:"grant_target_resource"`
	Resource              string                     `json:"resource"`
	RequestInstanceID     string                     `json:"request_instance_id"`
	AgentID               string                     `json:"agent_id"`
	RecoveryContract      string                     `json:"recovery_contract"`
	RecoveryOperationKind string                     `json:"recovery_operation_kind"`
	RetryOperation        ContinuationRetryOperation `json:"retry_operation"`
}

func migrateLegacyContinuationRecoveryNextActions(tx *sql.Tx) error {
	exists, err := schemaTableExists(tx, "next_action_records")
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	rows, err := tx.Query(`
		SELECT record_id, session_id, subject_kind, subject_ref, operation_input_json, created_at
		FROM next_action_records
		WHERE resolved_at IS NULL
			AND state = 'blocked_needs_authority'
			AND operation_tool = 'request_approval'
			AND operation_kind = 'continuation_lease_request'
			AND operation_input_json != ''
		ORDER BY created_at ASC, record_id ASC
	`)
	if err != nil {
		return fmt.Errorf("query legacy continuation recovery next actions: %w", err)
	}
	defer rows.Close()
	type rowData struct {
		RecordID     string
		SessionID    string
		SubjectKind  string
		SubjectRef   string
		InputJSON    string
		CreatedAtRaw string
	}
	var rowsToMigrate []rowData
	for rows.Next() {
		var row rowData
		if err := rows.Scan(&row.RecordID, &row.SessionID, &row.SubjectKind, &row.SubjectRef, &row.InputJSON, &row.CreatedAtRaw); err != nil {
			return fmt.Errorf("scan legacy continuation recovery next action: %w", err)
		}
		rowsToMigrate = append(rowsToMigrate, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, row := range rowsToMigrate {
		contract, err := compileLegacyContinuationRecoveryHandoff(row.SessionID, row.SubjectKind, row.SubjectRef, row.InputJSON, row.CreatedAtRaw)
		if err != nil {
			if _, updateErr := tx.Exec(`
				UPDATE next_action_records
				SET resolved_at = ?
				WHERE record_id = ?
					AND resolved_at IS NULL
			`, now, row.RecordID); updateErr != nil {
				return fmt.Errorf("resolve uncompilable legacy continuation recovery row %s: %w", row.RecordID, updateErr)
			}
			continue
		}
		if _, err := upsertContinuationRecoveryContractTx(tx, contract); err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE next_action_records
			SET operation_input_json = ?
			WHERE record_id = ?
				AND resolved_at IS NULL
		`, ContinuationRecoveryContractProjectionInput(contract.ContractID), row.RecordID); err != nil {
			return fmt.Errorf("rewrite legacy continuation recovery row %s: %w", row.RecordID, err)
		}
	}
	return nil
}

func compileLegacyContinuationRecoveryHandoff(sessionID string, subjectKind string, subjectRef string, raw string, createdAtRaw string) (ContinuationRecoveryContract, error) {
	var input legacyContinuationRecoveryHandoffInput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &input); err != nil {
		return ContinuationRecoveryContract{}, err
	}
	if strings.TrimSpace(input.Action) != "request_continuation_lease" {
		return ContinuationRecoveryContract{}, fmt.Errorf("legacy handoff action mismatch")
	}
	if strings.TrimSpace(input.RecoveryContract) != "" && strings.TrimSpace(input.RecoveryContract) != "aphelion.recovery_handoff.v1" {
		return ContinuationRecoveryContract{}, fmt.Errorf("legacy handoff contract mismatch")
	}
	if strings.TrimSpace(input.RecoveryOperationKind) != "" && strings.TrimSpace(input.RecoveryOperationKind) != "continuation_lease_request" {
		return ContinuationRecoveryContract{}, fmt.Errorf("legacy handoff operation mismatch")
	}
	createdAt, _ := parseSQLiteTime(createdAtRaw)
	leaseClass := NormalizeContinuationLeaseClass(ContinuationLeaseClass(input.LeaseClass))
	contractSubjectRef := ContinuationRecoverySubjectRef(leaseClass, input.AgentID, input.GrantID, input.Tool, input.ToolAction, input.Resource)
	if contractSubjectRef == "" {
		contractSubjectRef = subjectRef
	}
	retryOperation := NormalizeContinuationRetryOperation(input.RetryOperation)
	if retryOperation.Active() {
		if retryOperation.SubjectKind == "" {
			retryOperation.SubjectKind = subjectKind
		}
		if retryOperation.SubjectRef == "" || retryOperation.SubjectRef == subjectRef {
			retryOperation.SubjectRef = contractSubjectRef
		}
		if retryOperation.RequestInstanceID == "" {
			retryOperation.RequestInstanceID = input.RequestInstanceID
		}
	}
	return CompileContinuationRecoveryContract(ContinuationRecoveryContractInput{
		RequestInstanceID:   input.RequestInstanceID,
		SessionID:           sessionID,
		SubjectKind:         subjectKind,
		SubjectRef:          contractSubjectRef,
		Principal:           input.Principal,
		LeaseClass:          leaseClass,
		AllowedActions:      input.AllowedActions,
		Constraints:         input.Constraints,
		Tool:                input.Tool,
		ToolAction:          input.ToolAction,
		AgentID:             input.AgentID,
		Resource:            input.Resource,
		GrantID:             input.GrantID,
		GrantTargetResource: input.GrantTargetResource,
		RetryOperation:      retryOperation,
		CreatedAt:           createdAt,
	})
}

func firstNonEmptyStore(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringListEqual(left []string, right []string) bool {
	left = normalizeStringList(left)
	right = normalizeStringList(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func stringMapEqual(left map[string]string, right map[string]string) bool {
	left = normalizeRecoveryStringMap(left)
	right = normalizeRecoveryStringMap(right)
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		if right[key] != leftValue {
			return false
		}
	}
	return true
}
