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
const defaultApprovalWindowEventSource = "default_approval_window"

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
		return r.openDefaultApprovalWindowForScope(req.ChatID, adminUserID, scopeKind, scopeID, strings.TrimSpace(req.Kind), policy.Duration, now)
	}
	if !policy.Always {
		if _, ok, err := r.store.LatestOperatorAutoApprovalLeaseForScopeAndReason(req.ChatID, adminUserID, scopeKind, scopeID, defaultApprovalWindowReason); err != nil {
			return err
		} else if ok {
			return nil
		}
	}
	return r.openDefaultApprovalWindowForScope(req.ChatID, adminUserID, scopeKind, scopeID, strings.TrimSpace(req.Kind), policy.Duration, now)
}

func (r *Runtime) suppressInitialDefaultApprovalWindowOffer(chatID int64, adminUserID int64, scopeKind string, scopeID string, sourceKind string, now time.Time) (bool, error) {
	if r == nil || r.store == nil || r.cfg == nil || chatID == 0 {
		return false, nil
	}
	policy := config.EffectiveAutonomyPolicy(r.cfg).DefaultApprovalWindow
	if !policy.Enabled {
		return false, nil
	}
	if adminUserID <= 0 || !r.IsTelegramAdmin(adminUserID) {
		return false, nil
	}
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	if scopeKind == "" || scopeID == "" {
		scopeKind, scopeID = operatorAutoDefaultScope(chatID)
	}
	if err := r.validateApprovalWindowDuration(policy.Duration); err != nil {
		return false, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease, leaseOK, err := r.activeOperatorAutoApprovalLeaseForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return false, err
	}
	override, overrideOK, err := r.activeOperatorAutonomyOverrideForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return false, err
	}
	if leaseOK && overrideOK {
		return strings.TrimSpace(lease.Reason) == defaultApprovalWindowReason && strings.TrimSpace(override.Reason) == defaultApprovalWindowReason, nil
	}
	if leaseOK || overrideOK {
		if (leaseOK && strings.TrimSpace(lease.Reason) == defaultApprovalWindowReason) ||
			(overrideOK && strings.TrimSpace(override.Reason) == defaultApprovalWindowReason) {
			return true, r.openDefaultApprovalWindowForScope(chatID, adminUserID, scopeKind, scopeID, strings.TrimSpace(sourceKind), policy.Duration, now)
		}
		return false, nil
	}
	if _, ok, err := r.store.LatestOperatorAutoApprovalLeaseForScopeAndReason(chatID, adminUserID, scopeKind, scopeID, defaultApprovalWindowReason); err != nil {
		return false, err
	} else if ok && !policy.Always {
		return false, nil
	}
	return true, r.openDefaultApprovalWindowForScope(chatID, adminUserID, scopeKind, scopeID, strings.TrimSpace(sourceKind), policy.Duration, now)
}

func (r *Runtime) SuppressPostApprovalDefaultWindowOfferForKey(ctx context.Context, key session.SessionKey, adminUserID int64, sourceKind string, sourceID string, sourceDecisionKind string) (bool, error) {
	_ = ctx
	_ = sourceID
	_ = sourceDecisionKind
	if r == nil || r.store == nil || r.cfg == nil || key.ChatID == 0 {
		return false, nil
	}
	policy := config.EffectiveAutonomyPolicy(r.cfg).DefaultApprovalWindow
	if !policy.Enabled {
		return false, nil
	}
	if adminUserID <= 0 || !r.IsTelegramAdmin(adminUserID) {
		return false, nil
	}
	scope := session.NormalizeScopeRef(key.Scope)
	if scope.IsZero() {
		scope = telegramDMScopeRef(key.ChatID)
	}
	scopeKind, scopeID := session.OperatorAutoScopeForRef(scope)
	if strings.TrimSpace(scopeKind) == "" || strings.TrimSpace(scopeID) == "" {
		scopeKind, scopeID = operatorAutoDefaultScope(key.ChatID)
	}
	if err := r.validateApprovalWindowDuration(policy.Duration); err != nil {
		return false, err
	}
	now := time.Now().UTC()
	lease, leaseOK, err := r.activeOperatorAutoApprovalLeaseForAdminAndScope(key.ChatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return false, err
	}
	override, overrideOK, err := r.activeOperatorAutonomyOverrideForAdminAndScope(key.ChatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return false, err
	}
	if leaseOK && overrideOK {
		return true, nil
	}
	if leaseOK || overrideOK {
		if (leaseOK && strings.TrimSpace(lease.Reason) != defaultApprovalWindowReason) ||
			(overrideOK && strings.TrimSpace(override.Reason) != defaultApprovalWindowReason) {
			return true, nil
		}
		return true, r.openDefaultApprovalWindowForScope(key.ChatID, adminUserID, scopeKind, scopeID, strings.TrimSpace(sourceKind), policy.Duration, now)
	}
	return true, r.openDefaultApprovalWindowForScope(key.ChatID, adminUserID, scopeKind, scopeID, strings.TrimSpace(sourceKind), policy.Duration, now)
}

func (r *Runtime) openDefaultApprovalWindowForScope(chatID int64, adminUserID int64, scopeKind string, scopeID string, requestKind string, duration time.Duration, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	if duration <= 0 {
		duration = approvalWindowDefaultDuration
	}
	if err := r.validateApprovalWindowDuration(duration); err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	expiresAt := now.Add(duration)
	createdOverrideInput := session.OperatorAutonomyOverride{
		ID:          newOperatorAutonomyOverrideID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
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
		ID:          newOperatorAutoApprovalLeaseID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      defaultApprovalWindowReason,
		MaxUses:     0,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		UpdatedAt:   now,
	}
	if _, err := r.store.RevokeOperatorAutonomyOverridesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return err
	}
	createdOverride, err := r.store.CreateOperatorAutonomyOverride(createdOverrideInput)
	if err != nil {
		return err
	}
	r.recordOperatorAutoModeEvent(chatID, core.ExecutionEventAutoModeEnabled, "active", createdOverride, map[string]any{
		"source":           defaultApprovalWindowEventSource,
		"request_kind":     strings.TrimSpace(requestKind),
		"duration_seconds": int64(duration / time.Second),
	})
	if _, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return err
	}
	createdLease, err := r.store.CreateOperatorAutoApprovalLease(createdLeaseInput)
	if err != nil {
		return err
	}
	r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", createdLease, map[string]any{
		"source":           defaultApprovalWindowEventSource,
		"request_kind":     strings.TrimSpace(requestKind),
		"duration_seconds": int64(duration / time.Second),
	})
	return nil
}
