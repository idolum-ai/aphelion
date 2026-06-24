//go:build linux

package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) UpsertExecutionRunAuthority(record ExecutionRunAuthority) (ExecutionRunAuthority, error) {
	record = NormalizeExecutionRunAuthority(record)
	if record.TurnRunID <= 0 {
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority turn_run_id is required")
	}
	if record.SessionID == "" {
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority session_id is required")
	}
	if record.Principal == "" {
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority principal is required")
	}
	if record.ExecutionSpecies == "" {
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority execution_species is required")
	}
	switch record.LeaseKind {
	case ExecutionAuthorityLeaseKindContinuation:
		if record.ContinuationLeaseID == "" || record.OperationPlanLeaseID != "" {
			return ExecutionRunAuthority{}, fmt.Errorf("execution run authority requires exactly one continuation lease")
		}
	case ExecutionAuthorityLeaseKindOperationPlan:
		if record.OperationPlanLeaseID == "" || record.ContinuationLeaseID != "" {
			return ExecutionRunAuthority{}, fmt.Errorf("execution run authority requires exactly one operation plan lease")
		}
	default:
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority lease_kind is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("begin execution run authority tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if existing, ok, err := executionRunAuthorityTx(tx, record.TurnRunID); err != nil {
		return ExecutionRunAuthority{}, err
	} else if ok {
		if executionRunAuthoritySame(existing, record) {
			return existing, nil
		}
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority %d is immutable", record.TurnRunID)
	}
	if holder, ok, err := runningExecutionRunAuthorityForLeaseTx(tx, record); err != nil {
		return ExecutionRunAuthority{}, err
	} else if ok && holder != record.TurnRunID {
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority lease %q is already bound to running turn run %d", executionRunAuthorityLeaseID(record), holder)
	}
	if holder, ok, err := priorExecutionRunAuthorityForSingleTurnLeaseTx(tx, record); err != nil {
		return ExecutionRunAuthority{}, err
	} else if ok && holder != record.TurnRunID {
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority lease %q was already claimed by turn run %d", executionRunAuthorityLeaseID(record), holder)
	}
	leaseExpiresAt := nullableTimeRFC3339(record.LeaseExpiresAt)
	allowedActionsJSON, err := json.Marshal(record.LeaseAllowedActions)
	if err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("marshal execution run authority lease actions: %w", err)
	}
	constraintsJSON, err := json.Marshal(record.LeaseConstraints)
	if err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("marshal execution run authority lease constraints: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO execution_run_authority(
			turn_run_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			principal, principal_role, execution_species, lease_kind,
			continuation_lease_id, operation_plan_lease_id, lease_status, lease_remaining_turns,
			lease_class, lease_allowed_actions_json, lease_constraints_json,
			lease_expires_at, admitted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		record.TurnRunID,
		record.SessionID,
		record.ChatID,
		record.UserID,
		string(record.Scope.Kind),
		record.Scope.ID,
		record.Scope.DurableAgentID,
		record.Principal,
		record.PrincipalRole,
		record.ExecutionSpecies,
		record.LeaseKind,
		record.ContinuationLeaseID,
		record.OperationPlanLeaseID,
		record.LeaseStatus,
		record.LeaseRemainingTurns,
		string(record.LeaseClass),
		string(allowedActionsJSON),
		string(constraintsJSON),
		leaseExpiresAt,
		record.AdmittedAt.Format(time.RFC3339Nano),
	); err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("upsert execution run authority: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("commit execution run authority tx: %w", err)
	}
	stored, ok, err := s.ExecutionRunAuthority(record.TurnRunID)
	if err != nil {
		return ExecutionRunAuthority{}, err
	}
	if !ok {
		return ExecutionRunAuthority{}, fmt.Errorf("execution run authority %d not found after upsert", record.TurnRunID)
	}
	return stored, nil
}

func (s *SQLiteStore) runningExecutionRunAuthorityForLease(record ExecutionRunAuthority) (int64, bool, error) {
	return runningExecutionRunAuthorityForLeaseTx(s.db, record)
}

func runningExecutionRunAuthorityForLeaseTx(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, record ExecutionRunAuthority) (int64, bool, error) {
	leaseID := executionRunAuthorityLeaseID(record)
	if leaseID == "" {
		return 0, false, nil
	}
	query := `
		SELECT era.turn_run_id
		FROM execution_run_authority era
		JOIN turn_runs tr ON tr.id = era.turn_run_id
		WHERE era.lease_kind = ?
			AND tr.status = ?
			AND `
	args := []any{record.LeaseKind, string(TurnRunStatusRunning)}
	switch record.LeaseKind {
	case ExecutionAuthorityLeaseKindContinuation:
		query += `era.continuation_lease_id = ?`
	case ExecutionAuthorityLeaseKindOperationPlan:
		query += `era.operation_plan_lease_id = ?`
	default:
		return 0, false, nil
	}
	query += ` LIMIT 1`
	args = append(args, leaseID)
	var turnRunID int64
	if err := queryer.QueryRow(query, args...).Scan(&turnRunID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return turnRunID, true, nil
}

func priorExecutionRunAuthorityForSingleTurnLeaseTx(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, record ExecutionRunAuthority) (int64, bool, error) {
	if record.LeaseRemainingTurns > 1 {
		return 0, false, nil
	}
	leaseID := executionRunAuthorityLeaseID(record)
	if leaseID == "" {
		return 0, false, nil
	}
	query := `
		SELECT turn_run_id
		FROM execution_run_authority
		WHERE lease_kind = ?
			AND `
	args := []any{record.LeaseKind}
	switch record.LeaseKind {
	case ExecutionAuthorityLeaseKindContinuation:
		query += `continuation_lease_id = ?`
	case ExecutionAuthorityLeaseKindOperationPlan:
		query += `operation_plan_lease_id = ?`
	default:
		return 0, false, nil
	}
	query += ` LIMIT 1`
	args = append(args, leaseID)
	var turnRunID int64
	if err := queryer.QueryRow(query, args...).Scan(&turnRunID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return turnRunID, true, nil
}

func executionRunAuthorityLeaseID(record ExecutionRunAuthority) string {
	switch record.LeaseKind {
	case ExecutionAuthorityLeaseKindContinuation:
		return strings.TrimSpace(record.ContinuationLeaseID)
	case ExecutionAuthorityLeaseKindOperationPlan:
		return strings.TrimSpace(record.OperationPlanLeaseID)
	default:
		return ""
	}
}

func executionRunAuthoritySame(left ExecutionRunAuthority, right ExecutionRunAuthority) bool {
	left = NormalizeExecutionRunAuthority(left)
	right = NormalizeExecutionRunAuthority(right)
	return left.TurnRunID == right.TurnRunID &&
		left.SessionID == right.SessionID &&
		left.ChatID == right.ChatID &&
		left.UserID == right.UserID &&
		left.Scope == right.Scope &&
		left.Principal == right.Principal &&
		left.PrincipalRole == right.PrincipalRole &&
		left.ExecutionSpecies == right.ExecutionSpecies &&
		left.LeaseKind == right.LeaseKind &&
		left.ContinuationLeaseID == right.ContinuationLeaseID &&
		left.OperationPlanLeaseID == right.OperationPlanLeaseID &&
		left.LeaseStatus == right.LeaseStatus &&
		left.LeaseClass == right.LeaseClass &&
		stringSlicesEqual(left.LeaseAllowedActions, right.LeaseAllowedActions) &&
		stringMapsEqual(left.LeaseConstraints, right.LeaseConstraints) &&
		left.LeaseRemainingTurns == right.LeaseRemainingTurns &&
		sameOptionalTime(left.LeaseExpiresAt, right.LeaseExpiresAt) &&
		sameOptionalTime(left.AdmittedAt, right.AdmittedAt)
}

func stringSlicesEqual(left []string, right []string) bool {
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

func stringMapsEqual(left map[string]string, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func sameOptionalTime(left time.Time, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return left.IsZero() && right.IsZero()
	}
	return left.UTC().Equal(right.UTC())
}

func (s *SQLiteStore) ExecutionRunAuthority(turnRunID int64) (ExecutionRunAuthority, bool, error) {
	if turnRunID <= 0 {
		return ExecutionRunAuthority{}, false, nil
	}
	return executionRunAuthorityTx(s.db, turnRunID)
}

func executionRunAuthorityTx(queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}, turnRunID int64) (ExecutionRunAuthority, bool, error) {
	if turnRunID <= 0 {
		return ExecutionRunAuthority{}, false, nil
	}
	row := queryer.QueryRow(`
		SELECT
			turn_run_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			principal, principal_role, execution_species, lease_kind,
			continuation_lease_id, operation_plan_lease_id, lease_status, lease_remaining_turns,
			lease_class, lease_allowed_actions_json, lease_constraints_json,
			lease_expires_at, admitted_at
		FROM execution_run_authority
		WHERE turn_run_id = ?
	`, turnRunID)
	record, err := scanExecutionRunAuthority(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecutionRunAuthority{}, false, nil
	}
	if err != nil {
		return ExecutionRunAuthority{}, false, err
	}
	return record, true, nil
}

func scanExecutionRunAuthority(scanner interface{ Scan(dest ...any) error }) (ExecutionRunAuthority, error) {
	var (
		record            ExecutionRunAuthority
		scopeKindRaw      string
		scopeIDRaw        string
		durableAgentIDRaw string
		leaseClassRaw     string
		actionsRaw        string
		constraintsRaw    string
		leaseExpiresAtRaw sql.NullString
		admittedAtRaw     string
	)
	if err := scanner.Scan(
		&record.TurnRunID,
		&record.SessionID,
		&record.ChatID,
		&record.UserID,
		&scopeKindRaw,
		&scopeIDRaw,
		&durableAgentIDRaw,
		&record.Principal,
		&record.PrincipalRole,
		&record.ExecutionSpecies,
		&record.LeaseKind,
		&record.ContinuationLeaseID,
		&record.OperationPlanLeaseID,
		&record.LeaseStatus,
		&record.LeaseRemainingTurns,
		&leaseClassRaw,
		&actionsRaw,
		&constraintsRaw,
		&leaseExpiresAtRaw,
		&admittedAtRaw,
	); err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("scan execution run authority: %w", err)
	}
	record.Scope = NormalizeScopeRef(ScopeRef{
		Kind:           ScopeKind(strings.TrimSpace(scopeKindRaw)),
		ID:             strings.TrimSpace(scopeIDRaw),
		DurableAgentID: strings.TrimSpace(durableAgentIDRaw),
	})
	record.LeaseClass = ContinuationLeaseClass(strings.TrimSpace(leaseClassRaw))
	if strings.TrimSpace(actionsRaw) != "" {
		if err := json.Unmarshal([]byte(actionsRaw), &record.LeaseAllowedActions); err != nil {
			return ExecutionRunAuthority{}, fmt.Errorf("parse execution run authority lease actions: %w", err)
		}
	}
	if strings.TrimSpace(constraintsRaw) != "" {
		if err := json.Unmarshal([]byte(constraintsRaw), &record.LeaseConstraints); err != nil {
			return ExecutionRunAuthority{}, fmt.Errorf("parse execution run authority lease constraints: %w", err)
		}
	}
	if leaseExpiresAtRaw.Valid && strings.TrimSpace(leaseExpiresAtRaw.String) != "" {
		parsed, err := parseSQLiteTime(leaseExpiresAtRaw.String)
		if err != nil {
			return ExecutionRunAuthority{}, fmt.Errorf("parse execution run authority lease_expires_at: %w", err)
		}
		record.LeaseExpiresAt = parsed
	}
	admittedAt, err := parseSQLiteTime(admittedAtRaw)
	if err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("parse execution run authority admitted_at: %w", err)
	}
	record.AdmittedAt = admittedAt
	return NormalizeExecutionRunAuthority(record), nil
}
