//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertContinuationRecoveryContract(input ContinuationRecoveryContract) (ContinuationRecoveryContract, error) {
	if s == nil || s.db == nil {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract store unavailable")
	}
	input = NormalizeContinuationRecoveryContract(input)
	if input.ContractID == "" {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract_id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ContinuationRecoveryContract{}, fmt.Errorf("begin continuation recovery contract tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	contract, err := upsertContinuationRecoveryContractTx(tx, input)
	if err != nil {
		return ContinuationRecoveryContract{}, err
	}
	if err := tx.Commit(); err != nil {
		return ContinuationRecoveryContract{}, fmt.Errorf("commit continuation recovery contract tx: %w", err)
	}
	return contract, nil
}

func (s *SQLiteStore) ContinuationRecoveryContract(contractID string) (ContinuationRecoveryContract, bool, error) {
	if s == nil || s.db == nil {
		return ContinuationRecoveryContract{}, false, fmt.Errorf("continuation recovery contract store unavailable")
	}
	return continuationRecoveryContractByID(s.db, contractID)
}

func upsertContinuationRecoveryContractTx(tx *sql.Tx, input ContinuationRecoveryContract) (ContinuationRecoveryContract, error) {
	input = NormalizeContinuationRecoveryContract(input)
	if input.ContractID == "" {
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract_id is required")
	}
	existing, ok, err := continuationRecoveryContractByID(tx, input.ContractID)
	if err != nil {
		return ContinuationRecoveryContract{}, err
	}
	if ok {
		if continuationRecoveryContractEquivalent(existing, input) {
			return existing, nil
		}
		return ContinuationRecoveryContract{}, fmt.Errorf("continuation recovery contract %s idempotency conflict", input.ContractID)
	}
	allowedActions := encodeStringList(input.AllowedActions)
	constraintsRaw, _ := json.Marshal(input.Constraints)
	retryRaw, _ := json.Marshal(input.RetryOperation)
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := input.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	if _, err := tx.Exec(`
		INSERT INTO continuation_recovery_contracts(
			contract_id, contract_version, request_instance_id, contract_hash,
			session_id, subject_kind, subject_ref, status, principal, lease_class,
			allowed_actions_json, constraints_json, tool, tool_action, agent_id,
			resource, grant_id, grant_target_resource, retry_operation_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ContractID, input.ContractVersion, input.RequestInstanceID, input.ContractHash,
		input.SessionID, input.SubjectKind, input.SubjectRef, string(input.Status), input.Principal, string(input.LeaseClass),
		allowedActions, string(constraintsRaw), input.Tool, input.ToolAction, input.AgentID,
		input.Resource, input.GrantID, input.GrantTargetResource, string(retryRaw),
		createdAt.Format(time.RFC3339Nano), updatedAt.Format(time.RFC3339Nano)); err != nil {
		return ContinuationRecoveryContract{}, fmt.Errorf("insert continuation recovery contract %s: %w", input.ContractID, err)
	}
	return input, nil
}

type continuationRecoveryContractScanner interface {
	QueryRow(query string, args ...any) *sql.Row
}

func continuationRecoveryContractByID(db continuationRecoveryContractScanner, contractID string) (ContinuationRecoveryContract, bool, error) {
	contractID = strings.TrimSpace(contractID)
	if contractID == "" {
		return ContinuationRecoveryContract{}, false, nil
	}
	row := db.QueryRow(`
		SELECT contract_id, contract_version, request_instance_id, contract_hash,
			session_id, subject_kind, subject_ref, status, principal, lease_class,
			allowed_actions_json, constraints_json, tool, tool_action, agent_id,
			resource, grant_id, grant_target_resource, retry_operation_json,
			created_at, updated_at
		FROM continuation_recovery_contracts
		WHERE contract_id = ?
	`, contractID)
	return scanContinuationRecoveryContract(row)
}

func scanContinuationRecoveryContract(scanner interface{ Scan(dest ...any) error }) (ContinuationRecoveryContract, bool, error) {
	var (
		contract          ContinuationRecoveryContract
		statusRaw         string
		leaseClassRaw     string
		allowedActionsRaw string
		constraintsRaw    string
		retryRaw          string
		createdAtRaw      string
		updatedAtRaw      string
	)
	if err := scanner.Scan(
		&contract.ContractID, &contract.ContractVersion, &contract.RequestInstanceID, &contract.ContractHash,
		&contract.SessionID, &contract.SubjectKind, &contract.SubjectRef, &statusRaw, &contract.Principal, &leaseClassRaw,
		&allowedActionsRaw, &constraintsRaw, &contract.Tool, &contract.ToolAction, &contract.AgentID,
		&contract.Resource, &contract.GrantID, &contract.GrantTargetResource, &retryRaw,
		&createdAtRaw, &updatedAtRaw,
	); err != nil {
		if err == sql.ErrNoRows {
			return ContinuationRecoveryContract{}, false, nil
		}
		return ContinuationRecoveryContract{}, false, fmt.Errorf("scan continuation recovery contract: %w", err)
	}
	contract.Status = NormalizeContinuationRecoveryContractStatus(ContinuationRecoveryContractStatus(statusRaw))
	contract.LeaseClass = NormalizeContinuationLeaseClass(ContinuationLeaseClass(leaseClassRaw))
	contract.AllowedActions = decodeStringList(allowedActionsRaw)
	if err := json.Unmarshal([]byte(firstNonEmptyStore(constraintsRaw, "{}")), &contract.Constraints); err != nil {
		return ContinuationRecoveryContract{}, false, fmt.Errorf("decode continuation recovery constraints: %w", err)
	}
	if err := json.Unmarshal([]byte(firstNonEmptyStore(retryRaw, "{}")), &contract.RetryOperation); err != nil {
		return ContinuationRecoveryContract{}, false, fmt.Errorf("decode continuation recovery retry: %w", err)
	}
	createdAt, err := parseSQLiteTime(createdAtRaw)
	if err != nil {
		return ContinuationRecoveryContract{}, false, fmt.Errorf("parse continuation recovery created_at: %w", err)
	}
	updatedAt, err := parseSQLiteTime(updatedAtRaw)
	if err != nil {
		return ContinuationRecoveryContract{}, false, fmt.Errorf("parse continuation recovery updated_at: %w", err)
	}
	contract.CreatedAt = createdAt
	contract.UpdatedAt = updatedAt
	return NormalizeContinuationRecoveryContract(contract), true, nil
}

func continuationRecoveryContractEquivalent(left ContinuationRecoveryContract, right ContinuationRecoveryContract) bool {
	left = NormalizeContinuationRecoveryContract(left)
	right = NormalizeContinuationRecoveryContract(right)
	return left.ContractID == right.ContractID &&
		left.ContractVersion == right.ContractVersion &&
		left.RequestInstanceID == right.RequestInstanceID &&
		left.ContractHash == right.ContractHash &&
		left.SessionID == right.SessionID &&
		left.SubjectKind == right.SubjectKind &&
		left.SubjectRef == right.SubjectRef &&
		left.Principal == right.Principal &&
		left.LeaseClass == right.LeaseClass &&
		stringListEqual(left.AllowedActions, right.AllowedActions) &&
		stringMapEqual(left.Constraints, right.Constraints) &&
		left.Tool == right.Tool &&
		left.ToolAction == right.ToolAction &&
		left.AgentID == right.AgentID &&
		left.Resource == right.Resource &&
		left.GrantID == right.GrantID &&
		left.GrantTargetResource == right.GrantTargetResource &&
		continuationRetryOperationEqual(left.RetryOperation, right.RetryOperation)
}

func continuationRetryOperationEqual(left ContinuationRetryOperation, right ContinuationRetryOperation) bool {
	left = NormalizeContinuationRetryOperation(left)
	right = NormalizeContinuationRetryOperation(right)
	return left.Contract == right.Contract &&
		left.OperationKind == right.OperationKind &&
		left.Tool == right.Tool &&
		left.InputJSON == right.InputJSON &&
		left.SubjectKind == right.SubjectKind &&
		left.SubjectRef == right.SubjectRef &&
		left.RequestInstanceID == right.RequestInstanceID
}
