//go:build linux

package session

import (
	"database/sql"
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
	leaseExpiresAt := nullableTimeRFC3339(record.LeaseExpiresAt)
	if _, err := s.db.Exec(`
		INSERT INTO execution_run_authority(
			turn_run_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			principal, principal_role, execution_species, lease_kind,
			continuation_lease_id, operation_plan_lease_id, lease_status, lease_remaining_turns,
			lease_expires_at, admitted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(turn_run_id) DO UPDATE SET
			session_id = excluded.session_id,
			chat_id = excluded.chat_id,
			user_id = excluded.user_id,
			scope_kind = excluded.scope_kind,
			scope_id = excluded.scope_id,
			durable_agent_id = excluded.durable_agent_id,
			principal = excluded.principal,
			principal_role = excluded.principal_role,
			execution_species = excluded.execution_species,
			lease_kind = excluded.lease_kind,
			continuation_lease_id = excluded.continuation_lease_id,
			operation_plan_lease_id = excluded.operation_plan_lease_id,
			lease_status = excluded.lease_status,
			lease_remaining_turns = excluded.lease_remaining_turns,
			lease_expires_at = excluded.lease_expires_at,
			admitted_at = excluded.admitted_at
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
		leaseExpiresAt,
		record.AdmittedAt.Format(time.RFC3339Nano),
	); err != nil {
		return ExecutionRunAuthority{}, fmt.Errorf("upsert execution run authority: %w", err)
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

func (s *SQLiteStore) ExecutionRunAuthority(turnRunID int64) (ExecutionRunAuthority, bool, error) {
	if turnRunID <= 0 {
		return ExecutionRunAuthority{}, false, nil
	}
	row := s.db.QueryRow(`
		SELECT
			turn_run_id, session_id, chat_id, user_id, scope_kind, scope_id, durable_agent_id,
			principal, principal_role, execution_species, lease_kind,
			continuation_lease_id, operation_plan_lease_id, lease_status, lease_remaining_turns,
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
