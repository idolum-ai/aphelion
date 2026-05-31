//go:build linux

package session

import (
	"strings"
	"time"
)

func (s PlanState) Active() bool {
	return len(NormalizePlanState(s).Steps) > 0
}

func (p OperationPhasePlan) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.Goal) != "" ||
		strings.TrimSpace(p.CurrentPhaseID) != "" ||
		len(p.Phases) > 0
}

func (p OperationPhase) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(string(p.Status)) != "" ||
		strings.TrimSpace(p.AuthorityClass) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		len(p.AllowedActions) > 0 ||
		len(p.ForbiddenActions) > 0 ||
		len(p.ValidationPlan) > 0 ||
		len(p.RequiredCapabilityGrants) > 0 ||
		strings.TrimSpace(p.GateLevel) != "" ||
		strings.TrimSpace(p.GateReasonCode) != "" ||
		strings.TrimSpace(p.ApprovalSubject) != "" ||
		p.AutoApproveEligible != nil ||
		strings.TrimSpace(p.BlockedReasonCode) != "" ||
		p.RequiresConsent ||
		p.RequiresOptIn ||
		len(p.SupersedesPhaseIDs) > 0 ||
		p.StaleAuthority ||
		strings.TrimSpace(p.LeaseID) != "" ||
		!p.CompletedAt.IsZero()
}

func (l OperationPlanLease) Active() bool {
	return strings.TrimSpace(l.ID) != "" ||
		strings.TrimSpace(l.OperatorTitle) != "" ||
		strings.TrimSpace(l.PlanTitle) != "" ||
		strings.TrimSpace(l.Summary) != "" ||
		strings.TrimSpace(l.Objective) != "" ||
		strings.TrimSpace(l.MissionID) != "" ||
		strings.TrimSpace(l.OperationID) != "" ||
		strings.TrimSpace(string(l.Status)) != "" ||
		l.TurnBudget > 0 ||
		l.RemainingTurns > 0 ||
		len(l.CoveredPhaseIDs) > 0 ||
		!l.ExpiresAt.IsZero() ||
		len(l.Lanes) > 0 ||
		len(l.AllowedActions) > 0 ||
		len(l.ForbiddenActions) > 0 ||
		len(l.ValidationGates) > 0 ||
		len(l.ExitConditions) > 0 ||
		len(l.HardInterrupts) > 0 ||
		len(l.ChildInitiationLanes) > 0 ||
		l.EvidenceDigest.Active() ||
		l.ApprovedBy > 0 ||
		!l.ApprovedAt.IsZero()
}

func (l OperationPlanLeaseLane) Active() bool {
	return strings.TrimSpace(l.ID) != "" ||
		strings.TrimSpace(l.OperatorTitle) != "" ||
		strings.TrimSpace(l.PlanTitle) != "" ||
		strings.TrimSpace(l.Summary) != "" ||
		strings.TrimSpace(l.AuthorityClass) != "" ||
		l.ExpectedTurns > 0 ||
		len(l.AllowedActions) > 0 ||
		len(l.ForbiddenActions) > 0
}

func (s OperationPlanLeaseEvidenceDigest) Active() bool {
	return s.TurnsSpent > 0 ||
		len(s.LanesUsed) > 0 ||
		len(s.Completed) > 0 ||
		len(s.Blocked) > 0 ||
		len(s.InterruptsRaised) > 0 ||
		len(s.EvidenceRefs) > 0 ||
		len(s.ChangesMade) > 0 ||
		strings.TrimSpace(s.ResidualRisk) != "" ||
		strings.TrimSpace(s.SuggestedNextLease) != "" ||
		!s.UpdatedAt.IsZero()
}

func (s OperationState) Active() bool {
	normalized := s
	return strings.TrimSpace(normalized.ID) != "" ||
		strings.TrimSpace(normalized.Objective) != "" ||
		strings.TrimSpace(string(normalized.Status)) != "" ||
		strings.TrimSpace(normalized.Stage) != "" ||
		strings.TrimSpace(normalized.Summary) != "" ||
		normalized.Proposal.Active() ||
		normalized.PhasePlan.Active() ||
		normalized.PlanLease.Active() ||
		len(normalized.Findings) > 0 ||
		len(normalized.Artifacts) > 0
}

func (r CapabilityRequest) Active() bool {
	return strings.TrimSpace(r.RequestID) != "" ||
		strings.TrimSpace(r.RequestedBy) != "" ||
		strings.TrimSpace(r.RequestedFor) != "" ||
		strings.TrimSpace(r.ParentPrincipal) != "" ||
		strings.TrimSpace(r.AdminPrincipal) != "" ||
		strings.TrimSpace(string(r.Kind)) != "" ||
		strings.TrimSpace(r.TargetResource) != "" ||
		strings.TrimSpace(r.Purpose) != "" ||
		strings.TrimSpace(r.RiskClass) != "" ||
		strings.TrimSpace(r.Contract) != "" ||
		strings.TrimSpace(r.Constraints) != "" ||
		strings.TrimSpace(string(r.ReviewStatus)) != "" ||
		strings.TrimSpace(r.GrantID) != ""
}

func (s CapabilityGrantSpec) Active() bool {
	return strings.TrimSpace(s.RequestID) != "" ||
		strings.TrimSpace(s.GrantID) != "" ||
		strings.TrimSpace(string(s.Kind)) != "" ||
		strings.TrimSpace(s.TargetResource) != "" ||
		strings.TrimSpace(s.GrantedTo) != "" ||
		len(s.AllowedActions) > 0 ||
		strings.TrimSpace(s.Contract) != "" ||
		strings.TrimSpace(s.Constraints) != "" ||
		!s.ExpiresAt.IsZero()
}

func (r DurableChildAgreement) Active() bool {
	return strings.TrimSpace(r.AgreementID) != "" ||
		strings.TrimSpace(r.AgentID) != "" ||
		strings.TrimSpace(r.ParentPrincipal) != "" ||
		strings.TrimSpace(r.ChildPrincipal) != "" ||
		strings.TrimSpace(r.SourceSurface) != "" ||
		strings.TrimSpace(r.SourceRequestID) != "" ||
		r.SourceReviewEventID != 0 ||
		strings.TrimSpace(r.Summary) != "" ||
		strings.TrimSpace(r.BoundedEffect) != "" ||
		strings.TrimSpace(string(r.Status)) != "" ||
		len(r.ArtifactRefs) > 0
}

func (s TurnAuthorizationState) Active() bool {
	state := NormalizeTurnAuthorizationState(s)
	return state.Status == TurnAuthorizationStatusPending || state.Status == TurnAuthorizationStatusApproved
}

func (p OperationProposal) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.Kind) != "" ||
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		strings.TrimSpace(string(p.Status)) != ""
}

func (p ActionProposal) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.OperationID) != "" ||
		strings.TrimSpace(p.MissionID) != "" ||
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		strings.TrimSpace(p.RiskClass) != "" ||
		len(p.AllowedActions) > 0 ||
		len(p.ForbiddenActions) > 0 ||
		len(p.ValidationPlan) > 0 ||
		p.AutoApproveEligible != nil ||
		!p.ExpiresAt.IsZero() ||
		strings.TrimSpace(p.PlanHash) != "" ||
		strings.TrimSpace(string(p.Status)) != ""
}

func (l ContinuationLease) Active() bool {
	return l.ActiveAt(time.Now().UTC())
}

func (b ContinuationApprovalBundle) Active() bool {
	return strings.TrimSpace(b.ID) != "" ||
		strings.TrimSpace(b.OperationID) != "" ||
		strings.TrimSpace(b.PhasePlanID) != "" ||
		strings.TrimSpace(b.PlanFingerprint) != "" ||
		strings.TrimSpace(string(b.Status)) != "" ||
		strings.TrimSpace(b.CurrentPhaseID) != "" ||
		b.ApprovedBy > 0 ||
		len(b.Phases) > 0 ||
		!b.ExpiresAt.IsZero() ||
		!b.ApprovedAt.IsZero() ||
		!b.ConsumedAt.IsZero() ||
		!b.RevokedAt.IsZero()
}

func (p ContinuationApprovalBundlePhase) Active() bool {
	return strings.TrimSpace(p.ID) != "" ||
		strings.TrimSpace(p.OperationPhaseID) != "" ||
		strings.TrimSpace(p.PhaseFingerprint) != "" ||
		p.Index > 0 ||
		strings.TrimSpace(p.OperatorTitle) != "" ||
		strings.TrimSpace(p.PlanTitle) != "" ||
		strings.TrimSpace(p.Summary) != "" ||
		strings.TrimSpace(p.AuthorityClass) != "" ||
		strings.TrimSpace(p.WhyNow) != "" ||
		strings.TrimSpace(p.BoundedEffect) != "" ||
		len(p.AllowedActions) > 0 ||
		len(p.ForbiddenActions) > 0 ||
		len(p.ValidationPlan) > 0 ||
		len(p.RequiredCapabilityGrants) > 0 ||
		strings.TrimSpace(string(p.Status)) != "" ||
		!p.ApprovedAt.IsZero() ||
		!p.ActivatedAt.IsZero() ||
		!p.ConsumedAt.IsZero() ||
		!p.DeferredAt.IsZero()
}
