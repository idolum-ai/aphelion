//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
)

const (
	approvalWindowDefaultDuration = 15 * time.Minute
	approvalWindowReason          = "inline approval window"
)

func (r *Runtime) EnableApprovalWindow(ctx context.Context, chatID int64, adminUserID int64, duration time.Duration) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.enableApprovalWindowForScope(ctx, chatID, scopeKind, scopeID, adminUserID, duration)
}

func (r *Runtime) EnableApprovalWindowForKey(ctx context.Context, key session.SessionKey, adminUserID int64, duration time.Duration) (string, error) {
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.enableApprovalWindowForScope(ctx, key.ChatID, scopeKind, scopeID, adminUserID, duration)
}

func (r *Runtime) DoubleApprovalWindow(ctx context.Context, chatID int64, adminUserID int64) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.doubleApprovalWindowForScope(ctx, chatID, scopeKind, scopeID, adminUserID)
}

func (r *Runtime) DoubleApprovalWindowForKey(ctx context.Context, key session.SessionKey, adminUserID int64) (string, error) {
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.doubleApprovalWindowForScope(ctx, key.ChatID, scopeKind, scopeID, adminUserID)
}

func (r *Runtime) CancelApprovalWindow(ctx context.Context, chatID int64, adminUserID int64) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	return r.cancelApprovalWindowForScope(ctx, chatID, scopeKind, scopeID, adminUserID)
}

func (r *Runtime) CancelApprovalWindowForKey(ctx context.Context, key session.SessionKey, adminUserID int64) (string, error) {
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.cancelApprovalWindowForScope(ctx, key.ChatID, scopeKind, scopeID, adminUserID)
}

func (r *Runtime) enableApprovalWindowForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64, duration time.Duration) (string, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return "Approval windows are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Approval windows are admin only.", nil
	}
	if duration <= 0 {
		duration = approvalWindowDefaultDuration
	}
	if err := r.validateApprovalWindowDuration(duration); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)

	override := session.OperatorAutonomyOverride{
		ID:          newOperatorAutonomyOverrideID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Mode:        "leased",
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      approvalWindowReason,
		CreatedAt:   now,
		ExpiresAt:   now.Add(duration),
		UpdatedAt:   now,
	}
	lease := session.OperatorAutoApprovalLease{
		ID:          newOperatorAutoApprovalLeaseID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      approvalWindowReason,
		MaxUses:     0,
		CreatedAt:   now,
		ExpiresAt:   now.Add(duration),
		UpdatedAt:   now,
	}
	if _, err := r.store.RevokeOperatorAutonomyOverridesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return "", err
	}
	createdOverride, err := r.store.CreateOperatorAutonomyOverride(override)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoModeEvent(chatID, core.ExecutionEventAutoModeEnabled, "active", createdOverride, map[string]any{
		"source": "approval_window",
	})

	if _, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return "", err
	}
	createdLease, err := r.store.CreateOperatorAutoApprovalLease(lease)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", createdLease, map[string]any{
		"source": "approval_window",
	})
	return renderApprovalWindowEnabled(createdLease, createdOverride, now), nil
}

func (r *Runtime) doubleApprovalWindowForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64) (string, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return "Approval windows are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Approval windows are admin only.", nil
	}
	now := time.Now().UTC()
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)

	lease, ok, err := r.activeOperatorAutoApprovalLeaseForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no active approval window to double")
	}
	override, ok, err := r.activeOperatorAutonomyOverrideForAdminAndScope(chatID, scopeKind, scopeID, adminUserID, now)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("approval window has no active auto mode gate to double")
	}
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	override = session.NormalizeOperatorAutonomyOverride(override)
	previousDuration, doubledDuration := doubledOperatorWindowDuration(lease.CreatedAt, lease.ExpiresAt, now, operatorAutoApprovalMinDuration, r.approvalWindowMaxDuration())
	if err := r.validateApprovalWindowDuration(doubledDuration); err != nil {
		return "", err
	}
	maxUses := lease.MaxUses
	if lease.MaxUses > 0 {
		remainingUses := lease.MaxUses - lease.UsedCount
		if remainingUses <= 0 {
			return "", fmt.Errorf("active approval window has no remaining uses to double")
		}
		maxUses = remainingUses
	}
	createdOverride := session.OperatorAutonomyOverride{
		ID:          newOperatorAutonomyOverrideID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Mode:        override.Mode,
		Scope:       override.Scope,
		Reason:      approvalWindowReason,
		CreatedAt:   now,
		ExpiresAt:   now.Add(doubledDuration),
		UpdatedAt:   now,
	}
	createdLease := session.OperatorAutoApprovalLease{
		ID:          newOperatorAutoApprovalLeaseID(chatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      chatID,
		ScopeKind:   scopeKind,
		ScopeID:     scopeID,
		Scope:       lease.Scope,
		Reason:      approvalWindowReason,
		MaxUses:     maxUses,
		CreatedAt:   now,
		ExpiresAt:   now.Add(doubledDuration),
		UpdatedAt:   now,
	}
	if _, err := r.store.RevokeOperatorAutonomyOverridesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return "", err
	}
	storedOverride, err := r.store.CreateOperatorAutonomyOverride(createdOverride)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoModeEvent(chatID, core.ExecutionEventAutoModeEnabled, "active", storedOverride, map[string]any{
		"source":                    "approval_window",
		"doubled_from_override_id":  strings.TrimSpace(override.ID),
		"previous_duration_seconds": int64(previousDuration / time.Second),
		"new_duration_seconds":      int64(doubledDuration / time.Second),
	})

	if _, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return "", err
	}
	storedLease, err := r.store.CreateOperatorAutoApprovalLease(createdLease)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", storedLease, map[string]any{
		"source":                    "approval_window",
		"doubled_from_lease_id":     strings.TrimSpace(lease.ID),
		"previous_duration_seconds": int64(previousDuration / time.Second),
		"new_duration_seconds":      int64(doubledDuration / time.Second),
	})
	return renderApprovalWindowDoubled(storedLease, storedOverride, now, previousDuration, doubledDuration), nil
}

func (r *Runtime) cancelApprovalWindowForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64) (string, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return "Approval windows are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Approval windows are admin only.", nil
	}
	now := time.Now().UTC()
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	revokedLeases, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoApprovalEvent(
		chatID,
		core.ExecutionEventAutoApprovalRevoked,
		"revoked",
		operatorAutoApprovalPrimaryLeaseForScope(revokedLeases, chatID, scopeKind, scopeID, adminUserID),
		operatorAutoApprovalRevokedEventPayload(revokedLeases, now),
	)
	revokedOverrides, err := r.store.RevokeOperatorAutonomyOverridesForScope(chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return "", err
	}
	r.recordOperatorAutoModeEvent(
		chatID,
		core.ExecutionEventAutoModeRevoked,
		"revoked",
		operatorAutoModePrimaryOverrideForScope(revokedOverrides, chatID, scopeKind, scopeID, adminUserID),
		operatorAutoModeRevokedEventPayload(revokedOverrides, now),
	)
	return renderApprovalWindowCanceled(revokedLeases, revokedOverrides, now), nil
}

func (r *Runtime) validateApprovalWindowDuration(duration time.Duration) error {
	if duration < operatorAutoApprovalMinDuration {
		return fmt.Errorf("approval window duration must be at least %s", operatorAutoApprovalMinDuration)
	}
	if duration > operatorAutoApprovalMaxDuration {
		return fmt.Errorf("approval window duration is capped at %s", operatorAutoApprovalMaxDuration)
	}
	return r.validateAutonomyLiveOverride("leased", duration)
}

func (r *Runtime) approvalWindowMaxDuration() time.Duration {
	policy := config.EffectiveAutonomyPolicy(nil)
	if r != nil && r.cfg != nil {
		policy = config.EffectiveAutonomyPolicy(r.cfg)
	}
	maxDuration := operatorAutoApprovalMaxDuration
	if policy.MaxOverrideDuration > 0 && policy.MaxOverrideDuration < maxDuration {
		maxDuration = policy.MaxOverrideDuration
	}
	return maxDuration
}

func renderApprovalWindowEnabled(lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	override = session.NormalizeOperatorAutonomyOverride(override)
	return renderApprovalWindowActivePanel("active", "Opened a bounded approval window for matching requests.", lease, override, now, "")
}

func renderApprovalWindowDoubled(lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time, previousDuration time.Duration, doubledDuration time.Duration) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	override = session.NormalizeOperatorAutonomyOverride(override)
	detail := "Doubled: " + roundDuration(previousDuration) + " -> " + roundDuration(doubledDuration) + "."
	return renderApprovalWindowActivePanel("extended", "Expanded the current approval window by doubling its full time window.", lease, override, now, detail)
}

func renderApprovalWindowActivePanel(state string, why string, lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time, extraDetail string) string {
	expiresAt := lease.ExpiresAt
	if !override.ExpiresAt.IsZero() && (expiresAt.IsZero() || override.ExpiresAt.Before(expiresAt)) {
		expiresAt = override.ExpiresAt
	}
	details := []string{
		"Scope: all approval requests in this chat or thread.",
		"Expires: " + expiresAt.UTC().Format(time.RFC3339) + " (" + roundDuration(expiresAt.Sub(now)) + ").",
	}
	if strings.TrimSpace(extraDetail) != "" {
		details = append(details, strings.TrimSpace(extraDetail))
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Approval window",
		State:   state,
		Why:     why,
		Next:    "Use Double time to extend it, or cancel approvals to stop it.",
		Details: details,
	})
}

func renderApprovalWindowCanceled(leases []session.OperatorAutoApprovalLease, overrides []session.OperatorAutonomyOverride, now time.Time) string {
	leaseActive := len(operatorAutoApprovalActiveLeases(leases, now))
	overrideActive := len(operatorAutoModeActiveOverrides(overrides, now))
	details := []string{fmt.Sprintf("Revoked approval grants: %d.", len(leases))}
	details = append(details, fmt.Sprintf("Revoked auto mode gates: %d.", len(overrides)))
	if leaseActive == 0 && overrideActive == 0 {
		details = append(details, "No active approval window was open.")
	}
	return renderRuntimeCompactPanel(face.OperatorPanel{
		Title:   "Approval window",
		State:   "off",
		Why:     "Matching requests require explicit approval again.",
		Details: details,
	})
}
