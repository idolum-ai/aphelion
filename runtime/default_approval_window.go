//go:build linux

package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const defaultApprovalWindowReason = "default approval window"

func (r *Runtime) DefaultApprovalWindowDuration() time.Duration {
	policy := config.EffectiveAutonomyPolicy(nil).DefaultApprovalWindow
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg).DefaultApprovalWindow
	}
	if policy.Enabled && policy.Duration > 0 {
		return policy.Duration
	}
	return approvalWindowDefaultDuration
}

func (r *Runtime) ensureDefaultApprovalWindowForRequest(ctx context.Context, req operatorAutoApprovalRequest, now time.Time) error {
	_ = ctx
	if r == nil || r.store == nil || r.cfg == nil || req.ChatID == 0 {
		return nil
	}
	policy := config.EffectiveAutonomyPolicy(r.cfg).DefaultApprovalWindow
	if !policy.Enabled {
		return nil
	}
	adminUserID := req.AdminUserID
	if adminUserID <= 0 || !r.IsTelegramAdmin(adminUserID) {
		return nil
	}
	scopeKind := strings.TrimSpace(req.TargetScopeKind)
	scopeID := strings.TrimSpace(req.TargetScopeID)
	if scopeKind == "" || scopeID == "" {
		scopeKind, scopeID = operatorAutoDefaultScope(req.ChatID)
	}
	if err := r.validateApprovalWindowDuration(policy.Duration); err != nil {
		return err
	}
	lease, leaseOK, err := r.activeOperatorAutoApprovalLeaseForAdminAndScope(req.ChatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return err
	}
	override, overrideOK, err := r.activeOperatorAutonomyOverrideForAdminAndScope(req.ChatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return err
	}
	if leaseOK && overrideOK {
		return nil
	}
	if leaseOK || overrideOK {
		if (leaseOK && strings.TrimSpace(lease.Reason) != defaultApprovalWindowReason) ||
			(overrideOK && strings.TrimSpace(override.Reason) != defaultApprovalWindowReason) {
			return nil
		}
	}
	expiresAt := now.Add(policy.Duration)
	createdOverrideInput := session.OperatorAutonomyOverride{
		ID:          newOperatorAutonomyOverrideID(req.ChatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      req.ChatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Mode:        "leased",
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      defaultApprovalWindowReason,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		UpdatedAt:   now,
	}
	createdLeaseInput := session.OperatorAutoApprovalLease{
		ID:          newOperatorAutoApprovalLeaseID(req.ChatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      req.ChatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      defaultApprovalWindowReason,
		MaxUses:     0,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		UpdatedAt:   now,
	}
	if _, err := r.store.RevokeOperatorAutonomyOverridesForScope(req.ChatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return err
	}
	createdOverride, err := r.store.CreateOperatorAutonomyOverride(createdOverrideInput)
	if err != nil {
		return err
	}
	r.recordOperatorAutoModeEvent(req.ChatID, core.ExecutionEventAutoModeEnabled, "active", createdOverride, map[string]any{
		"source":           "default_approval_window",
		"request_kind":     strings.TrimSpace(req.Kind),
		"duration_seconds": int64(policy.Duration / time.Second),
	})
	if _, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(req.ChatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return err
	}
	createdLease, err := r.store.CreateOperatorAutoApprovalLease(createdLeaseInput)
	if err != nil {
		return err
	}
	r.recordOperatorAutoApprovalEvent(req.ChatID, core.ExecutionEventAutoApprovalGranted, "active", createdLease, map[string]any{
		"source":           "default_approval_window",
		"request_kind":     strings.TrimSpace(req.Kind),
		"duration_seconds": int64(policy.Duration / time.Second),
	})
	return nil
}
