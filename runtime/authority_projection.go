//go:build linux

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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

func (p authorityProjection) snapshot() core.AuthorityStatusSnapshot {
	status := "healthy"
	if len(p.Findings) > 0 || p.TruncatedCapabilitySet {
		status = "needs_attention"
	}
	out := core.AuthorityStatusSnapshot{
		GeneratedAt:            p.GeneratedAt,
		Status:                 status,
		ContinuationRecords:    p.ContinuationRecords,
		OperationRecords:       p.OperationRecords,
		PendingDecisions:       p.PendingDecisions,
		AutoApprovalLeases:     p.AutoApprovalLeases,
		CapabilityGrants:       p.CapabilityGrants,
		ActiveProposals:        p.ActiveProposals,
		ActiveLeases:           p.ActiveLeases,
		ActivePlanLeases:       p.ActivePlanLeases,
		FindingCount:           len(p.Findings),
		Findings:               make([]core.AuthorityFindingSnapshot, 0, len(p.Findings)),
		TruncatedCapabilitySet: p.TruncatedCapabilitySet,
	}
	for _, finding := range p.Findings {
		switch strings.ToLower(strings.TrimSpace(finding.Severity)) {
		case "error":
			out.ErrorCount++
		case "warning":
			out.WarningCount++
		}
		out.Findings = append(out.Findings, core.AuthorityFindingSnapshot{
			FindingID:       finding.FindingID,
			Code:            finding.Code,
			Severity:        finding.Severity,
			SourceKind:      finding.SourceKind,
			SourceID:        finding.SourceID,
			SessionID:       finding.SessionID,
			ChatID:          finding.ChatID,
			Detail:          finding.Detail,
			SuggestedRepair: finding.SuggestedRepair,
			ApplyAction:     finding.ApplyAction,
			ApplyScope:      finding.ApplyScope,
			Applicable:      finding.Applicable,
		})
	}
	return out
}

func (r *Runtime) writeDoctorAuthorityProjection(b *strings.Builder, now time.Time) {
	projection, err := r.authorityProjection(now)
	if err != nil {
		writeDoctorKV(b, "authority_projection_error", err.Error())
		return
	}
	status := "healthy"
	if len(projection.Findings) > 0 || projection.TruncatedCapabilitySet {
		status = "needs_attention"
	}
	writeDoctorKV(b, "authority_projection_status", status)
	writeDoctorKV(b, "authority_projection_generated_at", projection.GeneratedAt.Format(time.RFC3339))
	writeDoctorKV(b, "authority_continuation_records", fmt.Sprintf("%d", projection.ContinuationRecords))
	writeDoctorKV(b, "authority_operation_records", fmt.Sprintf("%d", projection.OperationRecords))
	writeDoctorKV(b, "authority_pending_decisions", fmt.Sprintf("%d", projection.PendingDecisions))
	writeDoctorKV(b, "authority_autoapproval_active_leases", fmt.Sprintf("%d", projection.AutoApprovalLeases))
	writeDoctorKV(b, "authority_capability_active_grants", fmt.Sprintf("%d", projection.CapabilityGrants))
	writeDoctorKV(b, "authority_active_proposals", fmt.Sprintf("%d", projection.ActiveProposals))
	writeDoctorKV(b, "authority_active_leases", fmt.Sprintf("%d", projection.ActiveLeases))
	writeDoctorKV(b, "authority_active_plan_leases", fmt.Sprintf("%d", projection.ActivePlanLeases))
	writeDoctorKV(b, "authority_finding_count", fmt.Sprintf("%d", len(projection.Findings)))
	if projection.TruncatedCapabilitySet {
		writeDoctorKV(b, "authority_capability_projection_truncated", "true")
	}
	writeDoctorLine(b, "authority_findings:")
	if len(projection.Findings) == 0 {
		writeDoctorLine(b, "- none")
		return
	}
	for _, finding := range projection.Findings {
		parts := []string{
			"finding_id=" + quoteDoctorToken(finding.FindingID),
			"code=" + quoteDoctorToken(finding.Code),
			"severity=" + quoteDoctorToken(finding.Severity),
			"source=" + quoteDoctorToken(firstNonEmpty(finding.SourceKind, "unknown")+":"+firstNonEmpty(finding.SourceID, "unknown")),
		}
		if finding.SessionID != "" {
			parts = append(parts, "session_id="+quoteDoctorToken(finding.SessionID))
		}
		if finding.ChatID != 0 {
			parts = append(parts, fmt.Sprintf("chat_id=%d", finding.ChatID))
		}
		if finding.Detail != "" {
			parts = append(parts, "detail="+quoteDoctorToken(finding.Detail))
		}
		if finding.SuggestedRepair != "" {
			parts = append(parts, "suggested_repair="+quoteDoctorToken(finding.SuggestedRepair))
		}
		if finding.ApplyAction != "" {
			parts = append(parts, "apply_action="+quoteDoctorToken(finding.ApplyAction))
		}
		if finding.ApplyScope != "" {
			parts = append(parts, "apply_scope="+quoteDoctorToken(finding.ApplyScope))
		}
		if finding.Applicable {
			parts = append(parts, "applicable=true")
		}
		writeDoctorLine(b, "- "+strings.Join(parts, " "))
	}
}

func quoteDoctorToken(value string) string {
	return fmt.Sprintf("%q", strings.TrimSpace(value))
}

func (p *authorityProjection) addFinding(finding authorityProjectionFinding) {
	if p == nil {
		return
	}
	finding.FindingID = strings.TrimSpace(finding.FindingID)
	finding.Code = strings.TrimSpace(finding.Code)
	finding.Severity = strings.TrimSpace(finding.Severity)
	finding.SourceKind = strings.TrimSpace(finding.SourceKind)
	finding.SourceID = strings.TrimSpace(finding.SourceID)
	finding.SessionID = strings.TrimSpace(finding.SessionID)
	finding.Detail = strings.TrimSpace(finding.Detail)
	finding.SuggestedRepair = strings.TrimSpace(finding.SuggestedRepair)
	finding.ApplyAction = strings.TrimSpace(finding.ApplyAction)
	finding.ApplyScope = strings.TrimSpace(finding.ApplyScope)
	if !finding.Applicable {
		finding.ApplyAction = ""
		finding.ApplyScope = ""
	}
	if finding.Code == "" || finding.Severity == "" {
		return
	}
	if finding.FindingID == "" {
		finding.FindingID = authorityFindingID(finding)
	}
	p.Findings = append(p.Findings, finding)
}

func authorityFindingID(finding authorityProjectionFinding) string {
	fields := []string{
		strings.TrimSpace(finding.Code),
		strings.TrimSpace(finding.SourceKind),
		strings.TrimSpace(finding.SourceID),
		strings.TrimSpace(finding.SessionID),
		strconv.FormatInt(finding.ChatID, 10),
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, "\x1f")))
	return "af_" + hex.EncodeToString(sum[:])[:16]
}

func (p *authorityProjection) sortFindings() {
	if p == nil {
		return
	}
	sort.SliceStable(p.Findings, func(i, j int) bool {
		left, right := p.Findings[i], p.Findings[j]
		if authoritySeverityRank(left.Severity) != authoritySeverityRank(right.Severity) {
			return authoritySeverityRank(left.Severity) < authoritySeverityRank(right.Severity)
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.SourceKind != right.SourceKind {
			return left.SourceKind < right.SourceKind
		}
		if left.SourceID != right.SourceID {
			return left.SourceID < right.SourceID
		}
		if left.SessionID != right.SessionID {
			return left.SessionID < right.SessionID
		}
		return left.FindingID < right.FindingID
	})
}

func authoritySeverityRank(severity string) int {
	switch strings.TrimSpace(strings.ToLower(severity)) {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func authorityProposalOpen(proposal session.ActionProposal, now time.Time) bool {
	proposal = session.NormalizeActionProposal(proposal)
	if !proposal.Active() {
		return false
	}
	if proposal.Status != "" && proposal.Status != session.ProposalStatusPending && proposal.Status != session.ProposalStatusApproved {
		return false
	}
	return proposal.ExpiresAt.IsZero() || proposal.ExpiresAt.After(now.UTC())
}

func authorityContinuationLeaseOpen(lease session.ContinuationLease, now time.Time) bool {
	lease = session.NormalizeContinuationLease(lease)
	switch lease.Status {
	case session.ContinuationLeaseStatusPending, session.ContinuationLeaseStatusActive:
	default:
		return false
	}
	if strings.TrimSpace(lease.ID) == "" && strings.TrimSpace(lease.ProposalID) == "" {
		return false
	}
	if lease.Status == session.ContinuationLeaseStatusActive && lease.RemainingTurns <= 0 {
		return false
	}
	return lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC())
}

func authorityPlanLeaseOpen(lease session.OperationPlanLease, now time.Time) bool {
	lease = session.NormalizeOperationPlanLease(lease)
	if !lease.Active() {
		return false
	}
	switch lease.Status {
	case session.PlanLeaseStatusProposed, session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive, session.PlanLeaseStatusPaused:
	default:
		return false
	}
	if lease.Status == session.PlanLeaseStatusActive && lease.RemainingTurns <= 0 {
		return false
	}
	return lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC())
}

func authorityPendingContinuationMissingDecision(state session.ContinuationState, decisions map[string]session.PendingDecisionRecord) bool {
	state = session.NormalizeContinuationState(state)
	if state.Status != session.ContinuationStatusPending {
		return false
	}
	if state.ActionProposal.Status != "" && state.ActionProposal.Status != session.ProposalStatusPending {
		return false
	}
	decisionID := strings.TrimSpace(state.DecisionID)
	if decisionID == "" {
		return false
	}
	_, ok := decisions[decisionID]
	return !ok
}

func authorityContinuationLeaseExpiredButConsumable(lease session.ContinuationLease, now time.Time) bool {
	lease = session.NormalizeContinuationLease(lease)
	if lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC()) {
		return false
	}
	switch lease.Status {
	case session.ContinuationLeaseStatusPending, session.ContinuationLeaseStatusActive:
	default:
		return false
	}
	return lease.RemainingTurns > 0
}

func authorityContinuationLeaseProposalMismatch(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	proposalID := strings.TrimSpace(state.ActionProposal.ID)
	leaseProposalID := strings.TrimSpace(state.ContinuationLease.ProposalID)
	if proposalID == "" || leaseProposalID == "" {
		return false
	}
	return proposalID != leaseProposalID
}

func authorityParkedContinuationNeedsRecoveryReview(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	if state.ParkedAt.IsZero() {
		return false
	}
	if state.Status == session.ContinuationStatusRevoked || state.ContinuationLease.Status == session.ContinuationLeaseStatusRevoked {
		return false
	}
	return state.RemainingTurns > 0 || state.ContinuationLease.RemainingTurns > 0
}

func authorityPlanLeaseExpiredButConsumable(lease session.OperationPlanLease, now time.Time) bool {
	lease = session.NormalizeOperationPlanLease(lease)
	if lease.ExpiresAt.IsZero() || lease.ExpiresAt.After(now.UTC()) {
		return false
	}
	switch lease.Status {
	case session.PlanLeaseStatusProposed, session.PlanLeaseStatusApproved, session.PlanLeaseStatusActive, session.PlanLeaseStatusPaused:
	default:
		return false
	}
	return lease.RemainingTurns > 0
}

func authorityOperationBlockedWithoutEscalation(state session.OperationState, lease session.OperationPlanLease, now time.Time) bool {
	state = session.NormalizeOperationState(state)
	if state.Status != session.OperationStatusBlocked && !authorityPhasePlanHasBlockedPhase(state.PhasePlan) {
		return false
	}
	if state.Proposal.Active() && (state.Proposal.Status == "" || state.Proposal.Status == session.ProposalStatusPending) {
		return false
	}
	return !authorityPlanLeaseOpen(lease, now)
}

func authorityPhasePlanHasBlockedPhase(plan session.OperationPhasePlan) bool {
	for _, phase := range plan.Phases {
		if strings.TrimSpace(phase.BlockedReasonCode) != "" || phase.StaleAuthority {
			return true
		}
	}
	return false
}

func authorityCapabilityGrantExpired(grant session.CapabilityGrant, now time.Time) bool {
	grant = session.NormalizeCapabilityGrant(grant)
	return grant.Status == session.CapabilityGrantStatusActive && !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now.UTC())
}

func authorityCapabilityGrantUsedWithoutTurnLeaseEvidence(grant session.CapabilityGrant, invocations []session.CapabilityInvocation) bool {
	grant = session.NormalizeCapabilityGrant(grant)
	if grant.InvocationCount <= 0 {
		return false
	}
	if !authorityGrantRequiresChildRuntime(grant) {
		return false
	}
	if len(invocations) == 0 {
		return true
	}
	for _, invocation := range invocations {
		invocation = session.NormalizeCapabilityInvocation(invocation)
		if strings.TrimSpace(invocation.ContinuationLeaseID) == "" && strings.TrimSpace(invocation.OperationPlanLeaseID) == "" {
			return true
		}
	}
	return false
}

func authorityGrantRequiresChildRuntime(grant session.CapabilityGrant) bool {
	grant = session.NormalizeCapabilityGrant(grant)
	if grant.Status != session.CapabilityGrantStatusActive {
		return false
	}
	if _, ok := core.DurableAgentIDFromPrincipal(grant.GrantedTo); !ok {
		return false
	}
	switch grant.Kind {
	case session.CapabilityKindTool,
		session.CapabilityKindExternalAccount,
		session.CapabilityKindLocalDevice,
		session.CapabilityKindFileAccess,
		session.CapabilityKindNetworkAccess:
		return true
	default:
		return false
	}
}

func authorityAutoApprovalUsedOutsideScopeFinding(event session.ExecutionEvent) (authorityProjectionFinding, bool) {
	if event.EventType != core.ExecutionEventAutoApprovalUsed {
		return authorityProjectionFinding{}, false
	}
	var payload struct {
		LeaseID     string `json:"lease_id"`
		Scope       string `json:"scope"`
		RequestKind string `json:"request_kind"`
		DecisionID  string `json:"decision_id"`
		ProposalID  string `json:"proposal_id"`
		WorkMode    string `json:"work_mode"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(event.PayloadJSON)), &payload); err != nil {
		return authorityProjectionFinding{}, false
	}
	if !authorityAutoApprovalScopeAllowsWorkMode(payload.Scope, payload.WorkMode) {
		return authorityProjectionFinding{
			Code:            "auto_approval_used_outside_scope",
			Severity:        "error",
			SourceKind:      "auto_approval_lease",
			SourceID:        strings.TrimSpace(payload.LeaseID),
			SessionID:       strings.TrimSpace(event.SessionID),
			ChatID:          event.ChatID,
			Detail:          "auto-approval use event records work_mode outside the lease scope",
			SuggestedRepair: "revoke the lease and inspect the linked decision or proposal before continuing",
		}, true
	}
	return authorityProjectionFinding{}, false
}

func authorityTailnetBindingInactive(binding session.TailnetGrantBinding) bool {
	switch strings.TrimSpace(binding.Status) {
	case "", session.TailnetGrantBindingStatusRevoked:
		return true
	default:
		return false
	}
}

func authorityAutoApprovalScopeAllowsWorkMode(scope string, workMode string) bool {
	scope = session.NormalizeOperatorAutoApprovalScope(scope)
	workMode = strings.TrimSpace(workMode)
	if workMode == "" {
		return true
	}
	switch scope {
	case session.OperatorAutoApprovalScopeAll:
		return true
	case session.OperatorAutoApprovalScopeWorkspace:
		return workMode == string(WorkModeReadOnly) || workMode == string(WorkModeWorkspaceWrite)
	case session.OperatorAutoApprovalScopeDeploy:
		return workMode == string(WorkModeDeploy) || workMode == string(WorkModeCommit)
	default:
		return false
	}
}
