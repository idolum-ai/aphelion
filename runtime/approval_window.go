//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/decisionprojection"
	"github.com/idolum-ai/aphelion/session"
)

const (
	approvalWindowDefaultDuration = 15 * time.Minute
	approvalWindowReason          = "inline approval window"
)

func (r *Runtime) EnableApprovalWindow(ctx context.Context, chatID int64, adminUserID int64, duration time.Duration) (string, error) {
	scopeKind, scopeID := operatorAutoDefaultScope(chatID)
	result, err := r.enableApprovalWindowForScope(ctx, chatID, scopeKind, scopeID, adminUserID, duration)
	return result.Text, err
}

func (r *Runtime) EnableApprovalWindowForKey(ctx context.Context, key session.SessionKey, adminUserID int64, duration time.Duration) (string, error) {
	result, err := r.EnableApprovalWindowForKeyResult(ctx, key, adminUserID, duration)
	return result.Text, err
}

func (r *Runtime) EnableApprovalWindowForKeyResult(ctx context.Context, key session.SessionKey, adminUserID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
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
	result, err := r.cancelApprovalWindowForScope(ctx, chatID, scopeKind, scopeID, adminUserID)
	return result.Text, err
}

func (r *Runtime) CancelApprovalWindowForKey(ctx context.Context, key session.SessionKey, adminUserID int64) (string, error) {
	result, err := r.CancelApprovalWindowForKeyResult(ctx, key, adminUserID)
	return result.Text, err
}

func (r *Runtime) CancelApprovalWindowForKeyResult(ctx context.Context, key session.SessionKey, adminUserID int64) (core.ApprovalWindowCancelResult, error) {
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	return r.cancelApprovalWindowForScope(ctx, key.ChatID, scopeKind, scopeID, adminUserID)
}

func (r *Runtime) enableApprovalWindowForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return core.ApprovalWindowEnableResult{Text: "Approval windows are unavailable."}, nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return core.ApprovalWindowEnableResult{Text: "Approval windows are admin only."}, nil
	}
	if duration <= 0 {
		duration = approvalWindowDefaultDuration
	}
	if err := r.validateApprovalWindowDuration(duration); err != nil {
		return core.ApprovalWindowEnableResult{}, err
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
		return core.ApprovalWindowEnableResult{}, err
	}
	createdOverride, err := r.store.CreateOperatorAutonomyOverride(override)
	if err != nil {
		return core.ApprovalWindowEnableResult{}, err
	}
	r.recordOperatorAutoModeEvent(chatID, core.ExecutionEventAutoModeEnabled, "active", createdOverride, map[string]any{
		"source": "approval_window",
	})

	if _, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now); err != nil {
		return core.ApprovalWindowEnableResult{}, err
	}
	createdLease, err := r.store.CreateOperatorAutoApprovalLease(lease)
	if err != nil {
		return core.ApprovalWindowEnableResult{}, err
	}
	r.recordOperatorAutoApprovalEvent(chatID, core.ExecutionEventAutoApprovalGranted, "active", createdLease, map[string]any{
		"source": "approval_window",
	})
	return core.ApprovalWindowEnableResult{Text: r.renderApprovalWindowEnabled(createdLease, createdOverride, now), Active: true, LeaseID: createdLease.ID, OverrideID: createdOverride.ID}, nil
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
	return r.renderApprovalWindowDoubled(storedLease, storedOverride, now, previousDuration, doubledDuration), nil
}

func (r *Runtime) cancelApprovalWindowForScope(ctx context.Context, chatID int64, scopeKind string, scopeID string, adminUserID int64) (core.ApprovalWindowCancelResult, error) {
	_ = ctx
	if r == nil || r.store == nil {
		return core.ApprovalWindowCancelResult{Text: "Approval windows are unavailable."}, nil
	}
	if !r.IsTelegramAdmin(adminUserID) {
		return core.ApprovalWindowCancelResult{Text: "Approval windows are admin only."}, nil
	}
	now := time.Now().UTC()
	scopeKind = strings.TrimSpace(scopeKind)
	scopeID = strings.TrimSpace(scopeID)
	revokedLeases, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(chatID, adminUserID, scopeKind, scopeID, now)
	if err != nil {
		return core.ApprovalWindowCancelResult{}, err
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
		return core.ApprovalWindowCancelResult{}, err
	}
	r.recordOperatorAutoModeEvent(
		chatID,
		core.ExecutionEventAutoModeRevoked,
		"revoked",
		operatorAutoModePrimaryOverrideForScope(revokedOverrides, chatID, scopeKind, scopeID, adminUserID),
		operatorAutoModeRevokedEventPayload(revokedOverrides, now),
	)
	return core.ApprovalWindowCancelResult{Text: renderApprovalWindowCanceled(revokedLeases, revokedOverrides, now), Canceled: true}, nil
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

func (r *Runtime) renderApprovalWindowEnabled(lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time) string {
	return renderApprovalWindowActiveLine("active", lease, override, now, r.approvalWindowDisplayLocation(), "matching requests", "")
}

func (r *Runtime) renderApprovalWindowEnabledForOffer(offer session.ApprovalWindowOffer, lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time) string {
	return renderApprovalWindowActiveLine("active", lease, override, now, r.approvalWindowDisplayLocation(), r.approvalWindowOfferSubject(offer), "")
}

func (r *Runtime) renderApprovalWindowDoubled(lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time, previousDuration time.Duration, doubledDuration time.Duration) string {
	detail := "extended from " + roundDuration(previousDuration) + " to " + roundDuration(doubledDuration)
	return renderApprovalWindowActiveLine("extended", lease, override, now, r.approvalWindowDisplayLocation(), "matching requests", detail)
}

func (r *Runtime) renderApprovalWindowDoubledForOffer(offer session.ApprovalWindowOffer, lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time, previousDuration time.Duration, doubledDuration time.Duration) string {
	detail := "extended from " + roundDuration(previousDuration) + " to " + roundDuration(doubledDuration)
	return renderApprovalWindowActiveLine("extended", lease, override, now, r.approvalWindowDisplayLocation(), r.approvalWindowOfferSubject(offer), detail)
}

func renderApprovalWindowActiveLine(state string, lease session.OperatorAutoApprovalLease, override session.OperatorAutonomyOverride, now time.Time, loc *time.Location, subject string, detail string) string {
	lease = session.NormalizeOperatorAutoApprovalLease(lease)
	override = session.NormalizeOperatorAutonomyOverride(override)
	expiresAt := lease.ExpiresAt
	if !override.ExpiresAt.IsZero() && (expiresAt.IsZero() || override.ExpiresAt.Before(expiresAt)) {
		expiresAt = override.ExpiresAt
	}
	if loc == nil {
		loc = time.UTC
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		subject = "matching requests"
	}
	verb := "is active"
	if state == "extended" {
		verb = "was extended"
	}
	tail := " until " + formatApprovalWindowExpiry(expiresAt, now, loc) + "."
	if d := strings.TrimSpace(detail); d != "" {
		tail = " (" + d + ")" + tail
	}
	if strings.EqualFold(subject, "matching requests") {
		return "Approval window " + verb + " for matching requests in this chat or thread" + tail
	}
	return "Approval window " + verb + " for “" + subject + "” and matching requests in this chat or thread" + tail
}

func (r *Runtime) approvalWindowDisplayLocation() *time.Location {
	zone := ""
	if r != nil && r.cfg != nil {
		zone = strings.TrimSpace(r.cfg.Operator.DisplayTimezone)
	}
	if zone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func formatApprovalWindowExpiry(expiresAt time.Time, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	local := expiresAt.In(loc)
	current := now.In(loc)
	if sameLocalDate(local, current) {
		return local.Format("3:04 PM")
	}
	if sameLocalDate(local, current.AddDate(0, 0, 1)) {
		return "tomorrow at " + local.Format("3:04 PM")
	}
	if local.Year() == current.Year() {
		return local.Format("Jan 2 at 3:04 PM")
	}
	return local.Format("Jan 2, 2006 at 3:04 PM")
}

func sameLocalDate(a time.Time, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func (r *Runtime) approvalWindowOfferSubject(offer session.ApprovalWindowOffer) string {
	offer = session.NormalizeApprovalWindowOffer(offer)
	if r == nil || r.store == nil {
		return "matching requests"
	}
	if offer.SourceKind == session.ApprovalWindowOfferSourceContinuation {
		if states, err := r.store.ContinuationStates(); err == nil {
			for _, rec := range states {
				state := session.NormalizeContinuationState(rec.State)
				if approvalWindowContinuationMatchesOffer(state, offer) {
					if subject := approvalWindowContinuationSubject(state); subject != "" {
						return subject
					}
				}
			}
		}
	}
	if offer.SourceKind == session.ApprovalWindowOfferSourceDecision {
		if decisions, err := r.store.PendingDecisions(); err == nil {
			for _, rec := range decisions {
				if strings.TrimSpace(rec.ID) != offer.SourceID {
					continue
				}
				if subject := approvalWindowDecisionSubject(rec); subject != "" {
					return subject
				}
			}
		}
	}
	return "matching requests"
}

func approvalWindowContinuationMatchesOffer(state session.ContinuationState, offer session.ApprovalWindowOffer) bool {
	sourceID := strings.TrimSpace(offer.SourceID)
	if sourceID == "" {
		return false
	}
	return sourceID == strings.TrimSpace(state.DecisionID) ||
		sourceID == strings.TrimSpace(state.ActionProposal.ID) ||
		sourceID == strings.TrimSpace(state.ApprovalBundle.ID) ||
		sourceID == strings.TrimSpace(state.ActionProposal.OperationID)
}

func approvalWindowContinuationSubject(state session.ContinuationState) string {
	title := continuationUserFacingPlanTitle(state)
	phase := continuationUserFacingPhaseLabel(state)
	if phase != "" && title != "" {
		return strings.ToLower(phase) + " for " + title
	}
	if phase != "" {
		return strings.ToLower(phase)
	}
	return title
}

func approvalWindowDecisionSubject(rec session.PendingDecisionRecord) string {
	summary := strings.TrimSpace(decisionprojection.DecisionSummary(rec.Kind, rec.Prompt, rec.Details))
	summary = strings.TrimPrefix(summary, "I’d like to ")
	summary = strings.TrimPrefix(summary, "I'd like to ")
	summary = strings.TrimSpace(strings.TrimRight(summary, "."))
	if summary == "" {
		summary = strings.TrimSpace(rec.Prompt)
	}
	runes := []rune(summary)
	if len(runes) > 72 {
		summary = strings.TrimSpace(string(runes[:72])) + "..."
	}
	return summary
}

func renderApprovalWindowCanceled(leases []session.OperatorAutoApprovalLease, overrides []session.OperatorAutonomyOverride, now time.Time) string {
	leaseActive := len(operatorAutoApprovalActiveLeases(leases, now))
	overrideActive := len(operatorAutoModeActiveOverrides(overrides, now))
	if leaseActive == 0 && overrideActive == 0 {
		return "Approval window is off; no active window was open. Matching requests need manual approval again."
	}
	return fmt.Sprintf("Approval window is off; cleared %d approval grant(s) and %d auto-mode gate(s). Matching requests need manual approval again.", len(leases), len(overrides))
}
