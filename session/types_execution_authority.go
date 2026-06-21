//go:build linux

package session

import (
	"strings"
	"time"
)

const (
	ExecutionAuthorityLeaseKindContinuation  = "continuation_lease"
	ExecutionAuthorityLeaseKindOperationPlan = "operation_plan_lease"
)

type ExecutionRunAuthority struct {
	TurnRunID            int64     `json:"turn_run_id"`
	SessionID            string    `json:"session_id,omitempty"`
	ChatID               int64     `json:"chat_id,omitempty"`
	UserID               int64     `json:"user_id,omitempty"`
	Scope                ScopeRef  `json:"scope,omitempty"`
	Principal            string    `json:"principal,omitempty"`
	PrincipalRole        string    `json:"principal_role,omitempty"`
	ExecutionSpecies     string    `json:"execution_species,omitempty"`
	LeaseKind            string    `json:"lease_kind,omitempty"`
	ContinuationLeaseID  string    `json:"continuation_lease_id,omitempty"`
	OperationPlanLeaseID string    `json:"operation_plan_lease_id,omitempty"`
	LeaseStatus          string    `json:"lease_status,omitempty"`
	LeaseRemainingTurns  int       `json:"lease_remaining_turns,omitempty"`
	LeaseExpiresAt       time.Time `json:"lease_expires_at,omitempty"`
	AdmittedAt           time.Time `json:"admitted_at,omitempty"`
}

func NormalizeExecutionRunAuthority(record ExecutionRunAuthority) ExecutionRunAuthority {
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.Scope = NormalizeScopeRef(record.Scope)
	record.Principal = strings.TrimSpace(record.Principal)
	record.PrincipalRole = strings.TrimSpace(record.PrincipalRole)
	record.ExecutionSpecies = strings.TrimSpace(record.ExecutionSpecies)
	record.LeaseKind = normalizeExecutionAuthorityLeaseKind(record.LeaseKind)
	record.ContinuationLeaseID = strings.TrimSpace(record.ContinuationLeaseID)
	record.OperationPlanLeaseID = strings.TrimSpace(record.OperationPlanLeaseID)
	record.LeaseStatus = strings.TrimSpace(record.LeaseStatus)
	if record.LeaseExpiresAt.IsZero() {
		record.LeaseExpiresAt = time.Time{}
	} else {
		record.LeaseExpiresAt = record.LeaseExpiresAt.UTC()
	}
	if record.AdmittedAt.IsZero() {
		record.AdmittedAt = time.Now().UTC()
	} else {
		record.AdmittedAt = record.AdmittedAt.UTC()
	}
	if record.SessionID == "" {
		record.SessionID = SessionIDFromParts(record.ChatID, record.UserID, record.Scope)
	}
	return record
}

func normalizeExecutionAuthorityLeaseKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case ExecutionAuthorityLeaseKindContinuation:
		return ExecutionAuthorityLeaseKindContinuation
	case ExecutionAuthorityLeaseKindOperationPlan:
		return ExecutionAuthorityLeaseKindOperationPlan
	default:
		return ""
	}
}
