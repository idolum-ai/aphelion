//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func parseOperationPlanLeaseInput(in *updateOperationPlanLeaseInput) (session.OperationPlanLease, error) {
	if in == nil {
		return session.OperationPlanLease{}, nil
	}
	lease := session.OperationPlanLease{
		ID:                   strings.TrimSpace(in.ID),
		Summary:              strings.TrimSpace(in.Summary),
		Objective:            strings.TrimSpace(in.Objective),
		MissionID:            strings.TrimSpace(in.MissionID),
		OperationID:          strings.TrimSpace(in.OperationID),
		TurnBudget:           in.TurnBudget,
		RemainingTurns:       in.RemainingTurns,
		CoveredPhaseIDs:      append([]string(nil), in.CoveredPhaseIDs...),
		AllowedActions:       append([]string(nil), in.AllowedActions...),
		ForbiddenActions:     append([]string(nil), in.ForbiddenActions...),
		ValidationGates:      append([]string(nil), in.ValidationGates...),
		ExitConditions:       append([]string(nil), in.ExitConditions...),
		HardInterrupts:       append([]string(nil), in.HardInterrupts...),
		ChildInitiationLanes: append([]string(nil), in.ChildInitiationLanes...),
		ApprovedBy:           in.ApprovedBy,
	}
	if strings.TrimSpace(in.Status) != "" {
		lease.Status = session.NormalizePlanLeaseStatus(session.PlanLeaseStatus(in.Status))
		if lease.Status == "" {
			return session.OperationPlanLease{}, fmt.Errorf("update_operation plan_lease status must be proposed, approved, active, paused, revoked, expired, or completed")
		}
	}
	expiresAt, err := parseOperationTime(in.ExpiresAt, "plan_lease expires_at")
	if err != nil {
		return session.OperationPlanLease{}, err
	}
	lease.ExpiresAt = expiresAt
	approvedAt, err := parseOperationTime(in.ApprovedAt, "plan_lease approved_at")
	if err != nil {
		return session.OperationPlanLease{}, err
	}
	lease.ApprovedAt = approvedAt
	lease.Lanes = parseOperationPlanLeaseLanes(in.Lanes)
	if in.EvidenceDigest != nil {
		lease.EvidenceDigest = parseOperationPlanLeaseEvidenceDigest(*in.EvidenceDigest)
	}
	lease = session.NormalizeOperationPlanLease(lease)
	if err := validateOperationPlanLease(lease); err != nil {
		return session.OperationPlanLease{}, err
	}
	return lease, nil
}

func mergeOperationPlanLeaseInput(current session.OperationPlanLease, in updateOperationPlanLeaseInput) (session.OperationPlanLease, error) {
	lease := current
	if id := strings.TrimSpace(in.ID); id != "" {
		lease.ID = id
	}
	if summary := strings.TrimSpace(in.Summary); summary != "" {
		lease.Summary = summary
	}
	if objective := strings.TrimSpace(in.Objective); objective != "" {
		lease.Objective = objective
	}
	if missionID := strings.TrimSpace(in.MissionID); missionID != "" {
		lease.MissionID = missionID
	}
	if operationID := strings.TrimSpace(in.OperationID); operationID != "" {
		lease.OperationID = operationID
	}
	if strings.TrimSpace(in.Status) != "" {
		status := session.NormalizePlanLeaseStatus(session.PlanLeaseStatus(in.Status))
		if status == "" {
			return session.OperationPlanLease{}, fmt.Errorf("update_operation plan_lease status must be proposed, approved, active, paused, revoked, expired, or completed")
		}
		lease.Status = status
	}
	if in.TurnBudget != 0 {
		lease.TurnBudget = in.TurnBudget
	}
	if in.RemainingTurns != 0 {
		lease.RemainingTurns = in.RemainingTurns
	}
	if in.CoveredPhaseIDs != nil {
		lease.CoveredPhaseIDs = append([]string(nil), in.CoveredPhaseIDs...)
	}
	if expiresAt, err := parseOperationTime(in.ExpiresAt, "plan_lease expires_at"); err != nil {
		return session.OperationPlanLease{}, err
	} else if !expiresAt.IsZero() {
		lease.ExpiresAt = expiresAt
	}
	if in.Lanes != nil {
		lease.Lanes = parseOperationPlanLeaseLanes(in.Lanes)
	}
	if in.AllowedActions != nil {
		lease.AllowedActions = append([]string(nil), in.AllowedActions...)
	}
	if in.ForbiddenActions != nil {
		lease.ForbiddenActions = append([]string(nil), in.ForbiddenActions...)
	}
	if in.ValidationGates != nil {
		lease.ValidationGates = append([]string(nil), in.ValidationGates...)
	}
	if in.ExitConditions != nil {
		lease.ExitConditions = append([]string(nil), in.ExitConditions...)
	}
	if in.HardInterrupts != nil {
		lease.HardInterrupts = append([]string(nil), in.HardInterrupts...)
	}
	if in.ChildInitiationLanes != nil {
		lease.ChildInitiationLanes = append([]string(nil), in.ChildInitiationLanes...)
	}
	if in.EvidenceDigest != nil {
		lease.EvidenceDigest = parseOperationPlanLeaseEvidenceDigest(*in.EvidenceDigest)
	}
	if in.ApprovedBy > 0 {
		lease.ApprovedBy = in.ApprovedBy
	}
	if approvedAt, err := parseOperationTime(in.ApprovedAt, "plan_lease approved_at"); err != nil {
		return session.OperationPlanLease{}, err
	} else if !approvedAt.IsZero() {
		lease.ApprovedAt = approvedAt
	}
	lease = session.NormalizeOperationPlanLease(lease)
	if err := validateOperationPlanLease(lease); err != nil {
		return session.OperationPlanLease{}, err
	}
	return lease, nil
}

func parseOperationPlanLeaseLanes(inputs []updateOperationPlanLeaseLaneInput) []session.OperationPlanLeaseLane {
	lanes := make([]session.OperationPlanLeaseLane, 0, len(inputs))
	for _, in := range inputs {
		lanes = append(lanes, session.OperationPlanLeaseLane{
			ID:               strings.TrimSpace(in.ID),
			Summary:          strings.TrimSpace(in.Summary),
			AuthorityClass:   strings.TrimSpace(in.AuthorityClass),
			ExpectedTurns:    in.ExpectedTurns,
			AllowedActions:   append([]string(nil), in.AllowedActions...),
			ForbiddenActions: append([]string(nil), in.ForbiddenActions...),
		})
	}
	return session.NormalizeOperationPlanLease(session.OperationPlanLease{Lanes: lanes}).Lanes
}

func parseOperationPlanLeaseEvidenceDigest(in updateOperationPlanLeaseEvidenceInput) session.OperationPlanLeaseEvidenceDigest {
	return session.OperationPlanLeaseEvidenceDigest{
		TurnsSpent:         in.TurnsSpent,
		LanesUsed:          append([]string(nil), in.LanesUsed...),
		Completed:          append([]string(nil), in.Completed...),
		Blocked:            append([]string(nil), in.Blocked...),
		InterruptsRaised:   append([]string(nil), in.InterruptsRaised...),
		EvidenceRefs:       append([]string(nil), in.EvidenceRefs...),
		ChangesMade:        append([]string(nil), in.ChangesMade...),
		ResidualRisk:       strings.TrimSpace(in.ResidualRisk),
		SuggestedNextLease: strings.TrimSpace(in.SuggestedNextLease),
		UpdatedAt:          time.Now().UTC(),
	}
}

func validateOperationPlanLease(lease session.OperationPlanLease) error {
	lease = session.NormalizeOperationPlanLease(lease)
	if !lease.Active() {
		return nil
	}
	for _, lane := range lease.Lanes {
		if strings.TrimSpace(lane.AuthorityClass) == "" {
			return fmt.Errorf("update_operation plan_lease lane %q requires authority_class", lane.ID)
		}
		if lane.ExpectedTurns <= 0 {
			return fmt.Errorf("update_operation plan_lease lane %q requires expected_turns > 0", lane.ID)
		}
	}
	return nil
}
