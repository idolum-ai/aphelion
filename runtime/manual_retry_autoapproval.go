//go:build linux

package runtime

import (
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type manualRetryBarrierResult struct {
	RevokedAutoApprovalLeases int
	RevokedAutonomyOverrides  int
}

func (result manualRetryBarrierResult) Revoked() bool {
	return result.RevokedAutoApprovalLeases > 0 || result.RevokedAutonomyOverrides > 0
}

func (r *Runtime) clearApprovalWindowForManualRetryBarrier(key session.SessionKey, state session.ContinuationState, source string, now time.Time) (manualRetryBarrierResult, error) {
	result := manualRetryBarrierResult{}
	if r == nil || r.store == nil || key.ChatID == 0 {
		return result, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	scopeKind, scopeID := operatorAutoTargetScopeForKey(key)
	leases, err := r.store.ActiveOperatorAutoApprovalLeasesForScope(key.ChatID, scopeKind, scopeID, now)
	if err != nil {
		return result, err
	}
	overrides, err := r.store.ActiveOperatorAutonomyOverridesForScope(key.ChatID, scopeKind, scopeID, now)
	if err != nil {
		return result, err
	}
	adminIDs := manualRetryBarrierAdminIDs(leases, overrides)
	for _, adminID := range adminIDs {
		revokedLeases, err := r.store.RevokeOperatorAutoApprovalLeasesForScope(key.ChatID, adminID, scopeKind, scopeID, now)
		if err != nil {
			return result, err
		}
		if len(revokedLeases) > 0 {
			result.RevokedAutoApprovalLeases += len(revokedLeases)
			payload := operatorAutoApprovalRevokedEventPayload(revokedLeases, now)
			addManualRetryBarrierPayload(payload, state, source)
			r.recordOperatorAutoApprovalEvent(
				key.ChatID,
				core.ExecutionEventAutoApprovalRevoked,
				"revoked",
				operatorAutoApprovalPrimaryLeaseForScope(revokedLeases, key.ChatID, scopeKind, scopeID, adminID),
				payload,
			)
		}
		revokedOverrides, err := r.store.RevokeOperatorAutonomyOverridesForScope(key.ChatID, adminID, scopeKind, scopeID, now)
		if err != nil {
			return result, err
		}
		if len(revokedOverrides) > 0 {
			result.RevokedAutonomyOverrides += len(revokedOverrides)
			payload := operatorAutoModeRevokedEventPayload(revokedOverrides, now)
			addManualRetryBarrierPayload(payload, state, source)
			r.recordOperatorAutoModeEvent(
				key.ChatID,
				core.ExecutionEventAutoModeRevoked,
				"revoked",
				operatorAutoModePrimaryOverrideForScope(revokedOverrides, key.ChatID, scopeKind, scopeID, adminID),
				payload,
			)
		}
	}
	return result, nil
}

func manualRetryBarrierAdminIDs(leases []session.OperatorAutoApprovalLease, overrides []session.OperatorAutonomyOverride) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(leases)+len(overrides))
	for _, lease := range leases {
		lease = session.NormalizeOperatorAutoApprovalLease(lease)
		if lease.AdminUserID <= 0 {
			continue
		}
		if _, ok := seen[lease.AdminUserID]; ok {
			continue
		}
		seen[lease.AdminUserID] = struct{}{}
		out = append(out, lease.AdminUserID)
	}
	for _, override := range overrides {
		override = session.NormalizeOperatorAutonomyOverride(override)
		if override.AdminUserID <= 0 {
			continue
		}
		if _, ok := seen[override.AdminUserID]; ok {
			continue
		}
		seen[override.AdminUserID] = struct{}{}
		out = append(out, override.AdminUserID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func addManualRetryBarrierPayload(payload map[string]any, state session.ContinuationState, source string) {
	if payload == nil {
		return
	}
	state = session.NormalizeContinuationState(state)
	payload["revocation_reason"] = "manual_retry_barrier"
	payload["manual_retry_source"] = strings.TrimSpace(source)
	payload["decision_id"] = strings.TrimSpace(state.DecisionID)
	payload["proposal_id"] = strings.TrimSpace(state.ActionProposal.ID)
	payload["lease_id"] = strings.TrimSpace(state.ContinuationLease.ID)
}
