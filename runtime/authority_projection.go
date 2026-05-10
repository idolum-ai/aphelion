//go:build linux

package runtime

import (
	"fmt"
	"sort"
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
	Code             string
	Severity         string
	SourceKind       string
	SourceID         string
	SessionID        string
	ChatID           int64
	Detail           string
	NextRepairAction string
}

func (r *Runtime) authorityProjection(now time.Time) (authorityProjection, error) {
	if r == nil || r.store == nil {
		return authorityProjection{}, fmt.Errorf("authority projection store is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	continuations, err := r.store.ContinuationStates()
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load continuation states: %w", err)
	}
	operations, err := r.store.OperationStates()
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load operation states: %w", err)
	}
	pendingDecisions, err := r.store.PendingDecisions()
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load pending decisions: %w", err)
	}
	autoApprovalLeases, err := r.store.OperatorAutoApprovalLeases(200, now, true)
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load operator auto-approval leases: %w", err)
	}
	const capabilityProjectionLimit = 1000
	capabilityGrants, err := r.store.CapabilityGrants(capabilityProjectionLimit, session.CapabilityGrantStatusActive, "", "")
	if err != nil {
		return authorityProjection{}, fmt.Errorf("load active capability grants: %w", err)
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
		if authorityPendingContinuationMissingDecision(state, decisionByID) {
			projection.addFinding(authorityProjectionFinding{
				Code:             "pending_proposal_missing_decision",
				Severity:         "warning",
				SourceKind:       "continuation",
				SourceID:         firstNonEmpty(state.ActionProposal.ID, state.DecisionID, sessionID),
				SessionID:        sessionID,
				ChatID:           record.Key.ChatID,
				Detail:           "pending continuation references a decision that is not in the pending decision store",
				NextRepairAction: "re-offer or revoke the pending continuation before executing more work",
			})
		}
		if authorityContinuationLeaseExpiredButConsumable(state.ContinuationLease, now) {
			projection.addFinding(authorityProjectionFinding{
				Code:             "expired_continuation_lease",
				Severity:         "error",
				SourceKind:       "continuation_lease",
				SourceID:         firstNonEmpty(state.ContinuationLease.ID, sessionID),
				SessionID:        sessionID,
				ChatID:           record.Key.ChatID,
				Detail:           "continuation lease still has turn budget after its expiry time",
				NextRepairAction: "expire, refresh, or revoke the lease before continuing",
			})
		}
		if authorityContinuationLeaseProposalMismatch(state) {
			projection.addFinding(authorityProjectionFinding{
				Code:             "continuation_lease_proposal_mismatch",
				Severity:         "warning",
				SourceKind:       "continuation_lease",
				SourceID:         firstNonEmpty(state.ContinuationLease.ID, sessionID),
				SessionID:        sessionID,
				ChatID:           record.Key.ChatID,
				Detail:           "continuation lease points at a different proposal than the current action proposal",
				NextRepairAction: "resynchronize the continuation authority record or re-offer the approval",
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
				Code:             "expired_operation_plan_lease",
				Severity:         "error",
				SourceKind:       "operation_plan_lease",
				SourceID:         firstNonEmpty(lease.ID, state.ID, sessionID),
				SessionID:        sessionID,
				ChatID:           record.Key.ChatID,
				Detail:           "operation plan lease still has turn budget after its expiry time",
				NextRepairAction: "expire, refresh, or revoke the operation plan lease before continuing",
			})
		}
	}

	for _, grant := range capabilityGrants {
		grant = session.NormalizeCapabilityGrant(grant)
		if authorityCapabilityGrantExpired(grant, now) {
			projection.addFinding(authorityProjectionFinding{
				Code:             "active_capability_grant_expired",
				Severity:         "error",
				SourceKind:       "capability_grant",
				SourceID:         grant.GrantID,
				Detail:           "capability grant is marked active after its expiry time",
				NextRepairAction: "expire, refresh, or revoke the capability grant before the next child/tool wake",
			})
		}
		if !grant.RevokedAt.IsZero() {
			projection.addFinding(authorityProjectionFinding{
				Code:             "active_capability_grant_revoked",
				Severity:         "error",
				SourceKind:       "capability_grant",
				SourceID:         grant.GrantID,
				Detail:           "capability grant is marked active while also carrying revoked_at",
				NextRepairAction: "move the grant to revoked or issue a fresh grant with a clean lifecycle",
			})
		}
		if strings.TrimSpace(grant.StaleReason) != "" {
			projection.addFinding(authorityProjectionFinding{
				Code:             "active_capability_grant_stale",
				Severity:         "warning",
				SourceKind:       "capability_grant",
				SourceID:         grant.GrantID,
				Detail:           "capability grant is active while also carrying stale reason: " + strings.TrimSpace(grant.StaleReason),
				NextRepairAction: "review the drift reason and refresh or revoke the grant",
			})
		}
		if authorityGrantRequiresChildRuntime(grant) {
			_, ok, materialErr := core.ExtractChildRuntimeContract(grant.Contract, grant.Constraints)
			if materialErr != nil {
				projection.addFinding(authorityProjectionFinding{
					Code:             "child_runtime_contract_invalid",
					Severity:         "error",
					SourceKind:       "capability_grant",
					SourceID:         grant.GrantID,
					Detail:           "capability grant child_runtime contract is invalid: " + materialErr.Error(),
					NextRepairAction: "replace the grant with validated child runtime material",
				})
			} else if !ok {
				projection.addFinding(authorityProjectionFinding{
					Code:             "child_runtime_contract_missing",
					Severity:         "warning",
					SourceKind:       "capability_grant",
					SourceID:         grant.GrantID,
					Detail:           "durable-agent capability grant has no child_runtime material",
					NextRepairAction: "issue a grant with explicit child runtime material or narrow the grant so it does not require materialization",
				})
			}
		}
	}

	projection.sortFindings()
	return projection, nil
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
		if finding.NextRepairAction != "" {
			parts = append(parts, "next_repair="+quoteDoctorToken(finding.NextRepairAction))
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
	finding.Code = strings.TrimSpace(finding.Code)
	finding.Severity = strings.TrimSpace(finding.Severity)
	finding.SourceKind = strings.TrimSpace(finding.SourceKind)
	finding.SourceID = strings.TrimSpace(finding.SourceID)
	finding.SessionID = strings.TrimSpace(finding.SessionID)
	finding.Detail = strings.TrimSpace(finding.Detail)
	finding.NextRepairAction = strings.TrimSpace(finding.NextRepairAction)
	if finding.Code == "" || finding.Severity == "" {
		return
	}
	p.Findings = append(p.Findings, finding)
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
		return left.SessionID < right.SessionID
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

func authorityCapabilityGrantExpired(grant session.CapabilityGrant, now time.Time) bool {
	grant = session.NormalizeCapabilityGrant(grant)
	return grant.Status == session.CapabilityGrantStatusActive && !grant.ExpiresAt.IsZero() && !grant.ExpiresAt.After(now.UTC())
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
