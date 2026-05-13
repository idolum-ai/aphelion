//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

type authorityProjection struct {
	GeneratedAt            time.Time
	ContinuationRecords    int
	OperationRecords       int
	PendingDecisions       int
	AutoApprovalLeases     int
	CapabilityGrants       int
	ActiveProposals        int
	ActiveLeases           int
	ActivePlanLeases       int
	Findings               []authorityProjectionFinding
	TruncatedCapabilitySet bool
}

type authorityProjectionFinding struct {
	FindingID       string
	Code            string
	Severity        string
	SourceKind      string
	SourceID        string
	SessionID       string
	ChatID          int64
	Detail          string
	SuggestedRepair string
	ApplyAction     string
	ApplyScope      string
	Applicable      bool
}

func (r *Runtime) authorityProjection(now time.Time) (authorityProjection, error) {
	if r == nil || r.store == nil {
		return authorityProjection{}, fmt.Errorf("authority projection store is unavailable")
	}
	return authorityProjectionFromStore(r.store, now)
}

func (r *Runtime) AuthorityStatusSnapshot(now time.Time) (core.AuthorityStatusSnapshot, error) {
	projection, err := r.authorityProjection(now)
	if err != nil {
		return core.AuthorityStatusSnapshot{}, err
	}
	return projection.snapshot(), nil
}

func AuthorityStatusSnapshotFromStore(store *session.SQLiteStore, now time.Time) (core.AuthorityStatusSnapshot, error) {
	projection, err := authorityProjectionFromStore(store, now)
	if err != nil {
		return core.AuthorityStatusSnapshot{}, err
	}
	return projection.snapshot(), nil
}

func authorityProjectionFromStore(store *session.SQLiteStore, now time.Time) (authorityProjection, error) {
	if store == nil {
		return authorityProjection{}, fmt.Errorf("authority projection store is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	continuations, err := store.ContinuationStates()
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load continuation states: %w", err)
	}
	operations, err := store.OperationStates()
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load operation states: %w", err)
	}
	pendingDecisions, err := store.PendingDecisions()
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load pending decisions: %w", err)
	}
	autoApprovalLeases, err := store.OperatorAutoApprovalLeases(200, now, true)
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load operator auto-approval leases: %w", err)
	}
	const capabilityProjectionLimit = 1000
	capabilityGrants, err := store.CapabilityGrants(capabilityProjectionLimit, session.CapabilityGrantStatusActive, "", "")
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load active capability grants: %w", err)
	}
	tailnetBindings, err := store.TailnetGrantBindings(session.TailnetGrantBindingFilter{Limit: capabilityProjectionLimit})
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load tailnet grant bindings: %w", err)
	}
	tailnetSurfaces, err := store.TailnetSurfaces(session.TailnetSurfaceFilter{Limit: capabilityProjectionLimit})
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load tailnet surfaces: %w", err)
	}
	autoApprovalEvents, err := store.ExecutionEventsByTypes([]string{core.ExecutionEventAutoApprovalUsed}, now.Add(-30*24*time.Hour), 1000)
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load auto-approval use events: %w", err)
	}
	projection := authorityProjection{
		GeneratedAt:            now,
		ContinuationRecords:    len(continuations),
		OperationRecords:       len(operations),
		PendingDecisions:       len(pendingDecisions),
		AutoApprovalLeases:     len(autoApprovalLeases),
		CapabilityGrants:       len(capabilityGrants),
		TruncatedCapabilitySet: len(capabilityGrants) >= capabilityProjectionLimit,
	}
	decisionByID := make(map[string]session.PendingDecisionRecord, len(pendingDecisions))
	for _, decision := range pendingDecisions {
		id := strings.TrimSpace(decision.ID)
		if id == "" {
			continue
		}
		decisionByID[id] = decision
	}

	for _, record := range continuations {
		state := session.NormalizeContinuationState(record.State)
		sessionID := session.SessionIDForKey(record.Key)
		if authorityProposalOpen(state.ActionProposal, now) {
			projection.ActiveProposals++
		}
		if authorityContinuationLeaseOpen(state.ContinuationLease, now) {
			projection.ActiveLeases++
		}
		if authorityContinuationLeaseOpen(state.ContinuationLease, now) && !authorityProposalOpen(state.ActionProposal, now) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "active_continuation_lease_missing_proposal",
				Severity:        "error",
				SourceKind:      "continuation_lease",
				SourceID:        firstNonEmpty(state.ContinuationLease.ID, sessionID),
				SessionID:       sessionID,
				ChatID:          record.Key.ChatID,
				Detail:          "open continuation lease has no active action proposal projection",
				SuggestedRepair: "re-offer the continuation or revoke the orphaned lease before executing more work",
			})
		}
		if authorityPendingContinuationMissingDecision(state, decisionByID) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "pending_proposal_missing_decision",
				Severity:        "warning",
				SourceKind:      "continuation",
				SourceID:        firstNonEmpty(state.ActionProposal.ID, state.DecisionID, sessionID),
				SessionID:       sessionID,
				ChatID:          record.Key.ChatID,
				Detail:          "pending continuation references a decision that is not in the pending decision store",
				SuggestedRepair: "re-offer or revoke the pending continuation before executing more work",
			})
		}
		if authorityContinuationLeaseExpiredButConsumable(state.ContinuationLease, now) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "expired_continuation_lease",
				Severity:        "error",
				SourceKind:      "continuation_lease",
				SourceID:        firstNonEmpty(state.ContinuationLease.ID, sessionID),
				SessionID:       sessionID,
				ChatID:          record.Key.ChatID,
				Detail:          "continuation lease still has turn budget after its expiry time",
				SuggestedRepair: "expire, refresh, or revoke the lease before continuing",
				ApplyAction:     "expire_continuation_lease",
				ApplyScope:      "continuation_lease",
				Applicable:      true,
			})
		}
		if authorityContinuationLeaseProposalMismatch(state) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "continuation_lease_proposal_mismatch",
				Severity:        "warning",
				SourceKind:      "continuation_lease",
				SourceID:        firstNonEmpty(state.ContinuationLease.ID, sessionID),
				SessionID:       sessionID,
				ChatID:          record.Key.ChatID,
				Detail:          "continuation lease points at a different proposal than the current action proposal",
				SuggestedRepair: "resynchronize the continuation authority record or re-offer the approval",
			})
		}
		if authorityParkedContinuationNeedsRecoveryReview(state) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "parked_lease_needs_recovery_review",
				Severity:        "warning",
				SourceKind:      "continuation_lease",
				SourceID:        firstNonEmpty(state.ContinuationLease.ID, sessionID),
				SessionID:       sessionID,
				ChatID:          record.Key.ChatID,
				Detail:          "parked continuation still has authority budget and needs explicit recovery review",
				SuggestedRepair: "recover, re-offer, or revoke the parked lease before startup recovery continues it",
			})
		}
	}

	for _, record := range operations {
		state := session.NormalizeOperationState(record.State)
		lease := session.NormalizeOperationPlanLease(state.PlanLease)
		sessionID := session.SessionIDForKey(record.Key)
		if authorityPlanLeaseOpen(lease, now) {
			projection.ActivePlanLeases++
		}
		if authorityPlanLeaseExpiredButConsumable(lease, now) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "expired_operation_plan_lease",
				Severity:        "error",
				SourceKind:      "operation_plan_lease",
				SourceID:        firstNonEmpty(lease.ID, state.ID, sessionID),
				SessionID:       sessionID,
				ChatID:          record.Key.ChatID,
				Detail:          "operation plan lease still has turn budget after its expiry time",
				SuggestedRepair: "expire, refresh, or revoke the operation plan lease before continuing",
				ApplyAction:     "expire_operation_plan_lease",
				ApplyScope:      "operation_plan_lease",
				Applicable:      true,
			})
		}
		if authorityOperationBlockedWithoutEscalation(state, lease, now) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "blocked_phase_missing_escalation",
				Severity:        "warning",
				SourceKind:      "operation",
				SourceID:        firstNonEmpty(state.ID, sessionID),
				SessionID:       sessionID,
				ChatID:          record.Key.ChatID,
				Detail:          "blocked operation phase has no pending proposal or active plan lease to resolve it",
				SuggestedRepair: "create a bounded escalation proposal or mark the phase stopped with evidence",
			})
		}
	}

	for _, grant := range capabilityGrants {
		grant = session.NormalizeCapabilityGrant(grant)
		invocations, err := store.CapabilityInvocationsByGrant(grant.GrantID, 20)
		if err != nil {
			return authorityProjection{}, fmt.Errorf("load capability invocations for grant %q: %w", grant.GrantID, err)
		}
		if authorityCapabilityGrantExpired(grant, now) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "active_capability_grant_expired",
				Severity:        "error",
				SourceKind:      "capability_grant",
				SourceID:        grant.GrantID,
				Detail:          "capability grant is marked active after its expiry time",
				SuggestedRepair: "expire, refresh, or revoke the capability grant before the next child/tool wake",
				ApplyAction:     "expire_capability_grant",
				ApplyScope:      "capability_grant",
				Applicable:      true,
			})
		}
		if !grant.RevokedAt.IsZero() {
			projection.addFinding(authorityProjectionFinding{
				Code:            "active_capability_grant_revoked",
				Severity:        "error",
				SourceKind:      "capability_grant",
				SourceID:        grant.GrantID,
				Detail:          "capability grant is marked active while also carrying revoked_at",
				SuggestedRepair: "move the grant to revoked or issue a fresh grant with a clean lifecycle",
				ApplyAction:     "revoke_capability_grant",
				ApplyScope:      "capability_grant",
				Applicable:      true,
			})
		}
		if strings.TrimSpace(grant.StaleReason) != "" {
			projection.addFinding(authorityProjectionFinding{
				Code:            "active_capability_grant_stale",
				Severity:        "warning",
				SourceKind:      "capability_grant",
				SourceID:        grant.GrantID,
				Detail:          "capability grant is active while also carrying stale reason: " + strings.TrimSpace(grant.StaleReason),
				SuggestedRepair: "review the drift reason and refresh or revoke the grant",
			})
		}
		if authorityCapabilityGrantUsedWithoutTurnLeaseEvidence(grant, invocations) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "capability_grant_invocation_missing_turn_lease_evidence",
				Severity:        "warning",
				SourceKind:      "capability_grant",
				SourceID:        grant.GrantID,
				Detail:          "capability grant has invocation evidence without continuation or operation plan lease reference",
				SuggestedRepair: "inspect capability invocations and ensure future grant use records the consuming turn lease",
			})
		}
		if authorityGrantRequiresChildRuntime(grant) {
			_, ok, materialErr := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
			if materialErr != nil {
				projection.addFinding(authorityProjectionFinding{
					Code:            "child_runtime_contract_invalid",
					Severity:        "error",
					SourceKind:      "capability_grant",
					SourceID:        grant.GrantID,
					Detail:          "capability grant child_runtime contract is invalid: " + materialErr.Error(),
					SuggestedRepair: "replace the grant with validated child runtime material",
				})
			} else if !ok {
				projection.addFinding(authorityProjectionFinding{
					Code:            "child_runtime_contract_missing",
					Severity:        "warning",
					SourceKind:      "capability_grant",
					SourceID:        grant.GrantID,
					Detail:          "durable-agent capability grant has no child_runtime material",
					SuggestedRepair: "issue a grant with explicit child runtime material or narrow the grant so it does not require materialization",
				})
			}
		}
	}

	for _, event := range autoApprovalEvents {
		if finding, ok := authorityAutoApprovalUsedOutsideScopeFinding(event); ok {
			projection.addFinding(finding)
		}
	}
	grantByID := make(map[string]session.CapabilityGrant, len(capabilityGrants))
	for _, grant := range capabilityGrants {
		grantByID[strings.TrimSpace(grant.GrantID)] = grant
	}
	surfaceByID := make(map[string]session.TailnetSurfaceRecord, len(tailnetSurfaces))
	for _, surface := range tailnetSurfaces {
		surfaceByID[strings.TrimSpace(surface.SurfaceID)] = surface
	}
	for _, binding := range tailnetBindings {
		binding = session.NormalizeTailnetGrantBinding(binding)
		if authorityTailnetBindingInactive(binding) {
			continue
		}
		if _, ok := surfaceByID[strings.TrimSpace(binding.SurfaceID)]; !ok {
			projection.addFinding(authorityProjectionFinding{
				Code:            "tailnet_binding_surface_missing",
				Severity:        "error",
				SourceKind:      "tailnet_grant_binding",
				SourceID:        binding.BindingID,
				Detail:          "tailnet grant binding references a surface that is not declared or observed",
				SuggestedRepair: "declare the surface, correct the binding, or revoke the network grant binding",
				ApplyAction:     "revoke_tailnet_grant_binding",
				ApplyScope:      "tailnet_grant_binding",
				Applicable:      true,
			})
		}
		if _, ok := grantByID[strings.TrimSpace(binding.GrantID)]; !ok && binding.Status == session.TailnetGrantBindingStatusApplied {
			projection.addFinding(authorityProjectionFinding{
				Code:            "tailnet_binding_active_grant_missing",
				Severity:        "error",
				SourceKind:      "tailnet_grant_binding",
				SourceID:        binding.BindingID,
				Detail:          "applied tailnet grant binding has no matching active Aphelion capability grant",
				SuggestedRepair: "roll back the Tailnet binding or restore a fresh approved capability grant",
				ApplyAction:     "revoke_tailnet_grant_binding",
				ApplyScope:      "tailnet_grant_binding",
				Applicable:      true,
			})
		}
		if binding.Status == session.TailnetGrantBindingStatusDrifted {
			projection.addFinding(authorityProjectionFinding{
				Code:            "tailnet_binding_drifted",
				Severity:        "warning",
				SourceKind:      "tailnet_grant_binding",
				SourceID:        binding.BindingID,
				Detail:          "tailnet grant binding is drifted: " + firstNonEmpty(binding.DriftReason, "policy evidence diverged"),
				SuggestedRepair: "review the drift reason and either re-apply the approved projection or revoke the binding",
			})
		}
		if strings.TrimSpace(binding.AppliedPolicyHash) != "" &&
			strings.TrimSpace(binding.ObservedPolicyHash) != "" &&
			strings.TrimSpace(binding.AppliedPolicyHash) != strings.TrimSpace(binding.ObservedPolicyHash) {
			projection.addFinding(authorityProjectionFinding{
				Code:            "tailnet_binding_policy_hash_mismatch",
				Severity:        "warning",
				SourceKind:      "tailnet_grant_binding",
				SourceID:        binding.BindingID,
				Detail:          "tailnet observed policy hash differs from the policy hash recorded at apply time",
				SuggestedRepair: "refresh observed policy evidence and mark the binding drifted or applied",
			})
		}
	}

	projection.sortFindings()
	return projection, nil
}
