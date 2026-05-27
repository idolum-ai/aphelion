//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const (
	approvalWindowOfferTTL          = 24 * time.Hour
	approvalWindowOfferOpeningGrace = 2 * time.Minute
)

func (r *Runtime) CreateApprovalWindowOfferForKey(ctx context.Context, key session.SessionKey, adminUserID int64, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return session.ApprovalWindowOffer{}, false, nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return session.ApprovalWindowOffer{}, false, nil
	}
	sourceKind = strings.TrimSpace(sourceKind)
	sourceID = strings.TrimSpace(sourceID)
	if sourceKind == "" || sourceID == "" || key.ChatID == 0 {
		return session.ApprovalWindowOffer{}, false, nil
	}
	scope := session.NormalizeScopeRef(key.Scope)
	if scope.IsZero() && key.ChatID != 0 {
		scope = telegramDMScopeRef(key.ChatID)
	}
	scopeKind, scopeID := session.OperatorAutoScopeForRef(scope)
	if strings.TrimSpace(scopeKind) == "" || strings.TrimSpace(scopeID) == "" {
		return session.ApprovalWindowOffer{}, false, nil
	}
	now := time.Now().UTC()
	if existing, ok, err := r.store.ActiveApprovalWindowOfferForSource(key.ChatID, sourceKind, sourceID, now); err != nil {
		return session.ApprovalWindowOffer{}, false, err
	} else if ok {
		if !existing.UsedAt.IsZero() {
			if existing.OpenedLeaseID == "" && existing.OpenedOverrideID == "" {
				if approvalWindowOfferClaimStillOpening(existing, now) {
					return existing, true, nil
				}
				_, _, _ = r.closeOfferIfStillBound(existing, now)
			} else if _, _, live, liveErr := r.liveApprovalWindowForOffer(existing, existing.AdminUserID, now); liveErr != nil {
				return session.ApprovalWindowOffer{}, false, liveErr
			} else if !live {
				_, _, _ = r.closeOfferIfStillBound(existing, now)
			} else {
				return existing, true, nil
			}
		} else {
			return existing, true, nil
		}
	}
	offer := session.ApprovalWindowOffer{
		ID:                 newApprovalWindowOfferID(key.ChatID, now),
		ChatID:             key.ChatID,
		AdminUserID:        adminUserID,
		SessionID:          session.SessionIDForKey(session.SessionKey{ChatID: key.ChatID, UserID: key.UserID, Scope: scope}),
		ScopeKind:          scopeKind,
		ScopeID:            scopeID,
		DurableAgentID:     scope.DurableAgentID,
		SourceKind:         sourceKind,
		SourceID:           sourceID,
		SourceDecisionKind: strings.TrimSpace(sourceDecisionKind),
		CreatedAt:          now,
		ExpiresAt:          now.Add(approvalWindowOfferTTL),
		UpdatedAt:          now,
	}
	created, err := r.store.CreateApprovalWindowOffer(offer)
	if err != nil {
		return session.ApprovalWindowOffer{}, false, err
	}
	return created, true, nil
}

func (r *Runtime) EnableApprovalWindowOffer(ctx context.Context, offerID string, adminUserID int64, duration time.Duration) (string, error) {
	result, err := r.EnableApprovalWindowOfferResult(ctx, offerID, adminUserID, duration)
	return result.Text, err
}

func (r *Runtime) EnableApprovalWindowOfferResult(ctx context.Context, offerID string, adminUserID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	offer, ok, err := r.activeApprovalWindowOffer(offerID, true)
	if err != nil || !ok {
		return core.ApprovalWindowEnableResult{}, err
	}
	if r == nil || r.store == nil {
		return core.ApprovalWindowEnableResult{Text: "Approval windows are unavailable."}, nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return core.ApprovalWindowEnableResult{Text: "Approval windows are admin only."}, nil
	}
	if !offer.UsedAt.IsZero() {
		return r.repairClaimedApprovalWindowOffer(ctx, offer, adminUserID)
	}
	if duration <= 0 {
		duration = approvalWindowDefaultDuration
	}
	if err := r.validateApprovalWindowDuration(duration); err != nil {
		return core.ApprovalWindowEnableResult{}, err
	}
	now := time.Now().UTC()
	createdLease, createdOverride := approvalWindowOfferAuthorityRows(offer, adminUserID, duration, now)
	_, storedLease, storedOverride, opened, err := r.store.OpenApprovalWindowOfferWithAuthority(offer.ID, createdLease, createdOverride, now)
	if err != nil {
		return core.ApprovalWindowEnableResult{}, err
	}
	if !opened {
		return core.ApprovalWindowEnableResult{}, fmt.Errorf("approval window offer is no longer active")
	}
	r.recordOperatorAutoModeEvent(offer.ChatID, core.ExecutionEventAutoModeEnabled, "active", storedOverride, map[string]any{
		"source": "approval_window",
	})
	r.recordOperatorAutoApprovalEvent(offer.ChatID, core.ExecutionEventAutoApprovalGranted, "active", storedLease, map[string]any{
		"source": "approval_window",
	})
	return core.ApprovalWindowEnableResult{Text: renderApprovalWindowEnabled(storedLease, storedOverride, now), Active: true, LeaseID: storedLease.ID, OverrideID: storedOverride.ID}, nil
}

func approvalWindowOfferAuthorityRows(offer session.ApprovalWindowOffer, adminUserID int64, duration time.Duration, now time.Time) (session.OperatorAutoApprovalLease, session.OperatorAutonomyOverride) {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	override := session.OperatorAutonomyOverride{
		ID:          newOperatorAutonomyOverrideID(offer.ChatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      offer.ChatID,
		ScopeKind:   offer.ScopeKind,
		ScopeID:     offer.ScopeID,
		Mode:        "leased",
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      approvalWindowReason,
		CreatedAt:   now,
		ExpiresAt:   now.Add(duration),
		UpdatedAt:   now,
	}
	lease := session.OperatorAutoApprovalLease{
		ID:          newOperatorAutoApprovalLeaseID(offer.ChatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      offer.ChatID,
		ScopeKind:   offer.ScopeKind,
		ScopeID:     offer.ScopeID,
		Scope:       session.OperatorAutoApprovalScopeAll,
		Reason:      approvalWindowReason,
		MaxUses:     0,
		CreatedAt:   now,
		ExpiresAt:   now.Add(duration),
		UpdatedAt:   now,
	}
	return lease, override
}

func (r *Runtime) repairClaimedApprovalWindowOffer(_ context.Context, offer session.ApprovalWindowOffer, adminUserID int64) (core.ApprovalWindowEnableResult, error) {
	now := time.Now().UTC()
	lease, override, live, err := r.liveApprovalWindowForOffer(offer, adminUserID, now)
	if err != nil {
		return core.ApprovalWindowEnableResult{}, err
	}
	if live {
		return core.ApprovalWindowEnableResult{Text: renderApprovalWindowEnabled(lease, override, now), Active: true, LeaseID: lease.ID, OverrideID: override.ID}, nil
	}
	if offer.OpenedLeaseID != "" || offer.OpenedOverrideID != "" {
		_, _, _ = r.closeOfferIfStillBound(offer, now)
		return core.ApprovalWindowEnableResult{}, fmt.Errorf("approval window offer was opened but no matching live approval window exists; offer closed")
	}
	if approvalWindowOfferClaimStillOpening(offer, now) {
		return core.ApprovalWindowEnableResult{}, fmt.Errorf("approval window offer is already being opened")
	}
	_, _, _ = r.closeOfferIfStillBound(offer, now)
	return core.ApprovalWindowEnableResult{}, fmt.Errorf("approval window offer was claimed but no matching live approval window exists; offer closed")
}

func approvalWindowOfferClaimStillOpening(offer session.ApprovalWindowOffer, now time.Time) bool {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.UsedAt.IsZero() || offer.OpenedLeaseID != "" || offer.OpenedOverrideID != "" {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.UTC().Before(offer.UsedAt.UTC().Add(approvalWindowOfferOpeningGrace))
}

func (r *Runtime) liveApprovalWindowForOffer(offer session.ApprovalWindowOffer, adminUserID int64, now time.Time) (session.OperatorAutoApprovalLease, session.OperatorAutonomyOverride, bool, error) {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if offer.OpenedLeaseID == "" || offer.OpenedOverrideID == "" || r == nil || r.store == nil {
		return session.OperatorAutoApprovalLease{}, session.OperatorAutonomyOverride{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease, leaseOK, err := r.store.OperatorAutoApprovalLease(offer.OpenedLeaseID)
	if err != nil {
		return session.OperatorAutoApprovalLease{}, session.OperatorAutonomyOverride{}, false, err
	}
	override, overrideOK, err := r.store.OperatorAutonomyOverride(offer.OpenedOverrideID)
	if err != nil {
		return session.OperatorAutoApprovalLease{}, session.OperatorAutonomyOverride{}, false, err
	}
	if !leaseOK || !overrideOK {
		return session.OperatorAutoApprovalLease{}, session.OperatorAutonomyOverride{}, false, nil
	}
	scopeKind, scopeID := session.OperatorAutoScopeForRef(offer.ScopeRef())
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	override = session.NormalizeOperatorAutonomyOverride(override)
	if lease.ChatID != offer.ChatID || lease.AdminUserID != adminUserID || lease.ScopeKind != scopeKind || lease.ScopeID != scopeID || !lease.ActiveAt(now) {
		return session.OperatorAutoApprovalLease{}, session.OperatorAutonomyOverride{}, false, nil
	}
	if override.ChatID != offer.ChatID || override.AdminUserID != adminUserID || override.ScopeKind != scopeKind || override.ScopeID != scopeID || !override.ActiveAt(now) {
		return session.OperatorAutoApprovalLease{}, session.OperatorAutonomyOverride{}, false, nil
	}
	return lease, override, true, nil
}

func (r *Runtime) DoubleApprovalWindowOffer(ctx context.Context, offerID string, adminUserID int64) (string, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return "Approval windows are unavailable.", nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return "Approval windows are admin only.", nil
	}
	offer, ok, err := r.activeApprovalWindowOffer(offerID, true)
	if err != nil || !ok {
		return "", err
	}
	now := time.Now().UTC()
	lease, override, live, err := r.liveApprovalWindowForOffer(offer, adminUserID, now)
	if err != nil {
		return "", err
	}
	if !live {
		_, _, _ = r.closeOfferIfStillBound(offer, now)
		return "", fmt.Errorf("approval window offer is no longer active")
	}
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
		ID:          newOperatorAutonomyOverrideID(offer.ChatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      offer.ChatID,
		ScopeKind:   lease.ScopeKind,
		ScopeID:     lease.ScopeID,
		Mode:        override.Mode,
		Scope:       override.Scope,
		Reason:      approvalWindowReason,
		CreatedAt:   now,
		ExpiresAt:   now.Add(doubledDuration),
		UpdatedAt:   now,
	}
	createdLease := session.OperatorAutoApprovalLease{
		ID:          newOperatorAutoApprovalLeaseID(offer.ChatID, adminUserID, now),
		AdminUserID: adminUserID,
		ChatID:      offer.ChatID,
		ScopeKind:   lease.ScopeKind,
		ScopeID:     lease.ScopeID,
		Scope:       lease.Scope,
		Reason:      approvalWindowReason,
		MaxUses:     maxUses,
		CreatedAt:   now,
		ExpiresAt:   now.Add(doubledDuration),
		UpdatedAt:   now,
	}
	_, oldLease, oldOverride, storedLease, storedOverride, replaced, err := r.store.ReplaceApprovalWindowOfferAuthorityByIDs(offer.ID, offer.ChatID, adminUserID, lease.ScopeKind, lease.ScopeID, offer.OpenedLeaseID, offer.OpenedOverrideID, createdLease, createdOverride, now)
	if err != nil {
		return "", err
	}
	if !replaced {
		_, _, _ = r.closeOfferIfStillBound(offer, now)
		return "", fmt.Errorf("approval window offer is no longer active")
	}
	r.recordOperatorAutoModeEvent(offer.ChatID, core.ExecutionEventAutoModeEnabled, "active", storedOverride, map[string]any{
		"source":                    "approval_window",
		"doubled_from_override_id":  strings.TrimSpace(oldOverride.ID),
		"previous_duration_seconds": int64(previousDuration / time.Second),
		"new_duration_seconds":      int64(doubledDuration / time.Second),
	})
	r.recordOperatorAutoApprovalEvent(offer.ChatID, core.ExecutionEventAutoApprovalGranted, "active", storedLease, map[string]any{
		"source":                    "approval_window",
		"doubled_from_lease_id":     strings.TrimSpace(oldLease.ID),
		"previous_duration_seconds": int64(previousDuration / time.Second),
		"new_duration_seconds":      int64(doubledDuration / time.Second),
	})
	return renderApprovalWindowDoubled(storedLease, storedOverride, now, previousDuration, doubledDuration), nil
}

func (r *Runtime) CancelApprovalWindowOffer(ctx context.Context, offerID string, adminUserID int64) (string, error) {
	result, err := r.CancelApprovalWindowOfferResult(ctx, offerID, adminUserID)
	return result.Text, err
}

func (r *Runtime) CancelApprovalWindowOfferResult(ctx context.Context, offerID string, adminUserID int64) (core.ApprovalWindowCancelResult, error) {
	if r == nil || r.store == nil {
		return core.ApprovalWindowCancelResult{Text: "Approval windows are unavailable."}, nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return core.ApprovalWindowCancelResult{Text: "Approval windows are admin only."}, nil
	}
	offer, ok, err := r.activeApprovalWindowOffer(offerID, true)
	if err != nil || !ok {
		return core.ApprovalWindowCancelResult{}, err
	}
	now := time.Now().UTC()
	lease, _, live, err := r.liveApprovalWindowForOffer(offer, adminUserID, now)
	if err != nil {
		return core.ApprovalWindowCancelResult{}, err
	}
	if !live {
		_, _, _ = r.closeOfferIfStillBound(offer, now)
		return core.ApprovalWindowCancelResult{}, fmt.Errorf("approval window offer is no longer active")
	}
	revokedLeases, revokedOverrides, canceled, err := r.store.RevokeOperatorApprovalWindowByIDs(offer.ChatID, adminUserID, lease.ScopeKind, lease.ScopeID, offer.OpenedLeaseID, offer.OpenedOverrideID, now)
	if err != nil {
		return core.ApprovalWindowCancelResult{}, err
	}
	if !canceled {
		_, _, _ = r.closeOfferIfStillBound(offer, now)
		return core.ApprovalWindowCancelResult{}, fmt.Errorf("approval window offer is no longer active")
	}
	r.recordOperatorAutoApprovalEvent(offer.ChatID, core.ExecutionEventAutoApprovalRevoked, "revoked", operatorAutoApprovalPrimaryLeaseForScope(revokedLeases, offer.ChatID, lease.ScopeKind, lease.ScopeID, adminUserID), operatorAutoApprovalRevokedEventPayload(revokedLeases, now))
	r.recordOperatorAutoModeEvent(offer.ChatID, core.ExecutionEventAutoModeRevoked, "revoked", operatorAutoModePrimaryOverrideForScope(revokedOverrides, offer.ChatID, lease.ScopeKind, lease.ScopeID, adminUserID), operatorAutoModeRevokedEventPayload(revokedOverrides, now))
	if _, ok, err := r.store.CloseApprovalWindowOffer(offer.ID, now); err != nil {
		return core.ApprovalWindowCancelResult{}, err
	} else if !ok {
		return core.ApprovalWindowCancelResult{}, fmt.Errorf("approval window offer is no longer active")
	}
	return core.ApprovalWindowCancelResult{Text: renderApprovalWindowCanceled(revokedLeases, revokedOverrides, now), Canceled: true}, nil
}

func (r *Runtime) closeOfferIfStillBound(offer session.ApprovalWindowOffer, now time.Time) (session.ApprovalWindowOffer, bool, error) {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if r == nil || r.store == nil || offer.ID == "" {
		return session.ApprovalWindowOffer{}, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if offer.OpenedLeaseID != "" || offer.OpenedOverrideID != "" {
		return r.store.CloseApprovalWindowOfferIfOpened(offer.ID, offer.OpenedLeaseID, offer.OpenedOverrideID, now)
	}
	if !offer.UsedAt.IsZero() {
		if approvalWindowOfferClaimStillOpening(offer, now) {
			return session.ApprovalWindowOffer{}, false, nil
		}
		return r.store.CloseClaimedUnopenedApprovalWindowOffer(offer.ID, now)
	}
	return r.store.CloseUnusedApprovalWindowOffer(offer.ID, now)
}

func (r *Runtime) CloseApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) error {
	_ = ctx
	if r == nil || r.store == nil {
		return nil
	}
	offer, ok, err := r.store.ApprovalWindowOffer(strings.TrimSpace(offerID))
	if err != nil || !ok {
		return err
	}
	offer = session.NormalizeApprovalWindowOffer(offer)
	now := time.Now().UTC()
	if senderID != 0 && !r.IsTelegramAdmin(senderID) {
		return fmt.Errorf("approval windows are admin only")
	}
	if offer.OpenedLeaseID == "" && offer.OpenedOverrideID == "" {
		if !offer.UsedAt.IsZero() && approvalWindowOfferClaimStillOpening(offer, now) {
			return fmt.Errorf("approval window offer is already being opened")
		}
		_, _, err := r.closeOfferIfStillBound(offer, now)
		return err
	}
	lease, _, live, err := r.liveApprovalWindowForOffer(offer, offer.AdminUserID, now)
	if err != nil {
		return err
	}
	if !live {
		_, _, err := r.closeOfferIfStillBound(offer, now)
		return err
	}
	if !r.IsTelegramAdmin(senderID) {
		return fmt.Errorf("approval windows are admin only")
	}
	revokedLeases, revokedOverrides, canceled, err := r.store.RevokeOperatorApprovalWindowByIDs(offer.ChatID, offer.AdminUserID, lease.ScopeKind, lease.ScopeID, offer.OpenedLeaseID, offer.OpenedOverrideID, now)
	if err != nil {
		return err
	}
	if canceled {
		r.recordOperatorAutoApprovalEvent(offer.ChatID, core.ExecutionEventAutoApprovalRevoked, "revoked", operatorAutoApprovalPrimaryLeaseForScope(revokedLeases, offer.ChatID, lease.ScopeKind, lease.ScopeID, offer.AdminUserID), operatorAutoApprovalRevokedEventPayload(revokedLeases, now))
		r.recordOperatorAutoModeEvent(offer.ChatID, core.ExecutionEventAutoModeRevoked, "revoked", operatorAutoModePrimaryOverrideForScope(revokedOverrides, offer.ChatID, lease.ScopeKind, lease.ScopeID, offer.AdminUserID), operatorAutoModeRevokedEventPayload(revokedOverrides, now))
	}
	_, _, err = r.closeOfferIfStillBound(offer, now)
	return err
}

func (r *Runtime) ApprovalWindowOfferByID(offerID string) (session.ApprovalWindowOffer, bool, error) {
	if r == nil || r.store == nil {
		return session.ApprovalWindowOffer{}, false, nil
	}
	offer, ok, err := r.store.ApprovalWindowOffer(strings.TrimSpace(offerID))
	if err != nil || !ok {
		return session.ApprovalWindowOffer{}, ok, err
	}
	return session.NormalizeApprovalWindowOffer(offer), true, nil
}

func (r *Runtime) activeApprovalWindowOffer(offerID string, allowUsed bool) (session.ApprovalWindowOffer, bool, error) {
	if r == nil || r.store == nil {
		return session.ApprovalWindowOffer{}, false, fmt.Errorf("approval windows are unavailable")
	}
	offer, ok, err := r.store.ApprovalWindowOffer(offerID)
	if err != nil {
		return session.ApprovalWindowOffer{}, false, err
	}
	if !ok || !offer.ActiveAt(time.Now().UTC()) {
		return session.ApprovalWindowOffer{}, false, fmt.Errorf("approval window offer is no longer active")
	}
	if !allowUsed && !offer.UsedAt.IsZero() {
		return session.ApprovalWindowOffer{}, false, fmt.Errorf("approval window offer was already used")
	}
	return offer, true, nil
}

func approvalWindowOfferSessionKey(offer session.ApprovalWindowOffer) session.SessionKey {
	offer = session.NormalizeApprovalWindowOffer(offer)
	return session.SessionKey{ChatID: offer.ChatID, Scope: offer.ScopeRef()}
}

func newApprovalWindowOfferID(chatID int64, now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return "awo-" + strconv.FormatInt(chatID, 36) + "-" + strconv.FormatInt(now.UnixNano(), 36)
}
