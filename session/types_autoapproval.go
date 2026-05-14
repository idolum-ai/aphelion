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

type OperatorAutonomyOverride struct {
	ID          string
	AdminUserID int64
	ChatID      int64
	Mode        string
	Scope       string
	Reason      string
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

func NormalizeOperatorAutonomyMode(mode string) string {
	switch normalizeEnumValue(mode) {
	case "off":
		return "off"
	case "review_only":
		return "review_only"
	case "ask_first":
		return "ask_first"
	case "mission":
		return "mission"
	case "leased":
		return "leased"
	default:
		return strings.ToLower(strings.TrimSpace(mode))
	}
}

func NormalizeOperatorAutonomyOverride(override OperatorAutonomyOverride) OperatorAutonomyOverride {
	override.ID = strings.TrimSpace(override.ID)
	override.Mode = NormalizeOperatorAutonomyMode(override.Mode)
	override.Scope = NormalizeOperatorAutoApprovalScope(override.Scope)
	override.Reason = strings.TrimSpace(override.Reason)
	if !override.CreatedAt.IsZero() {
		override.CreatedAt = override.CreatedAt.UTC()
	}
	if !override.ExpiresAt.IsZero() {
		override.ExpiresAt = override.ExpiresAt.UTC()
	}
	if !override.RevokedAt.IsZero() {
		override.RevokedAt = override.RevokedAt.UTC()
	}
	if !override.UpdatedAt.IsZero() {
		override.UpdatedAt = override.UpdatedAt.UTC()
	}
	return override
}

func (o OperatorAutonomyOverride) ActiveAt(now time.Time) bool {
	override := NormalizeOperatorAutonomyOverride(o)
	if override.ID == "" || override.AdminUserID <= 0 || override.ChatID == 0 {
		return false
	}
	if override.Mode != "leased" {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	if !override.RevokedAt.IsZero() {
		return false
	}
	return !override.ExpiresAt.IsZero() && override.ExpiresAt.After(now)
}
