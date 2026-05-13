//go:build linux

package session

import (
	"strings"
	"time"
)

type OperatorAutoApprovalLease struct {
	ID          string
	AdminUserID int64
	ChatID      int64
	Scope       string
	Reason      string
	MaxUses     int
	UsedCount   int
	CreatedAt   time.Time
	ExpiresAt   time.Time
	RevokedAt   time.Time
	UpdatedAt   time.Time
}

func NormalizeOperatorAutoApprovalScope(scope string) string {
	switch normalizeEnumValue(scope) {
	case OperatorAutoApprovalScopeWorkspace:
		return OperatorAutoApprovalScopeWorkspace
	case OperatorAutoApprovalScopeDeploy:
		return OperatorAutoApprovalScopeDeploy
	default:
		return OperatorAutoApprovalScopeAll
	}
}

func NormalizeOperatorAutoApprovalLease(lease OperatorAutoApprovalLease) OperatorAutoApprovalLease {
	lease.ID = strings.TrimSpace(lease.ID)
	lease.Scope = NormalizeOperatorAutoApprovalScope(lease.Scope)
	lease.Reason = strings.TrimSpace(lease.Reason)
	if lease.MaxUses < 0 {
		lease.MaxUses = 0
	}
	if lease.UsedCount < 0 {
		lease.UsedCount = 0
	}
	if !lease.CreatedAt.IsZero() {
		lease.CreatedAt = lease.CreatedAt.UTC()
	}
	if !lease.ExpiresAt.IsZero() {
		lease.ExpiresAt = lease.ExpiresAt.UTC()
	}
	if !lease.RevokedAt.IsZero() {
		lease.RevokedAt = lease.RevokedAt.UTC()
	}
	if !lease.UpdatedAt.IsZero() {
		lease.UpdatedAt = lease.UpdatedAt.UTC()
	}
	return lease
}
