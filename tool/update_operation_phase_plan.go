//go:build linux

package tool

import (
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func parseOperationPhasePlanInput(in *updateOperationPhasePlanInput) (session.OperationPhasePlan, error) {
	if in == nil {
		return session.OperationPhasePlan{}, nil
	}
	phases, err := parseOperationPhaseInputs(in.Phases)
	if err != nil {
		return session.OperationPhasePlan{}, err
	}
	plan := session.OperationPhasePlan{
		ID:             strings.TrimSpace(in.ID),
		Goal:           strings.TrimSpace(in.Goal),
		CurrentPhaseID: strings.TrimSpace(in.CurrentPhaseID),
		Phases:         phases,
	}
	return session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan, nil
}

func mergeOperationPhasePlanInput(current session.OperationPhasePlan, in updateOperationPhasePlanInput) (session.OperationPhasePlan, error) {
	plan := current
	if id := strings.TrimSpace(in.ID); id != "" {
		plan.ID = id
	}
	if goal := strings.TrimSpace(in.Goal); goal != "" {
		plan.Goal = goal
	}
	if currentPhaseID := strings.TrimSpace(in.CurrentPhaseID); currentPhaseID != "" {
		plan.CurrentPhaseID = currentPhaseID
	}
	if in.Phases != nil {
		if len(in.Phases) == 0 {
			plan.Phases = nil
			plan.CurrentPhaseID = ""
		} else {
			phases := append([]session.OperationPhase(nil), plan.Phases...)
			for _, item := range in.Phases {
				phase, err := parseOperationPhaseInput(item)
				if err != nil {
					return session.OperationPhasePlan{}, err
				}
				phaseID := strings.TrimSpace(phase.ID)
				if phaseID == "" {
					phases = append(phases, phase)
					continue
				}
				replaced := false
				for i := range phases {
					if strings.TrimSpace(phases[i].ID) != phaseID {
						continue
					}
					merged, err := mergeOperationPhaseInput(phases[i], item)
					if err != nil {
						return session.OperationPhasePlan{}, err
					}
					phases[i] = merged
					replaced = true
					break
				}
				if !replaced {
					phases = append(phases, phase)
				}
			}
			plan.Phases = phases
		}
	}
	return session.NormalizeOperationState(session.OperationState{PhasePlan: plan}).PhasePlan, nil
}

func parseOperationPhaseInputs(inputs []updateOperationPhaseInput) ([]session.OperationPhase, error) {
	phases := make([]session.OperationPhase, 0, len(inputs))
	for _, item := range inputs {
		phase, err := parseOperationPhaseInput(item)
		if err != nil {
			return nil, err
		}
		if !phase.Active() {
			continue
		}
		phases = append(phases, phase)
	}
	return phases, nil
}

func parseOperationPhaseInput(in updateOperationPhaseInput) (session.OperationPhase, error) {
	inputID := strings.TrimSpace(in.ID)
	if strings.TrimSpace(in.LeaseID) != "" {
		return session.OperationPhase{}, fmt.Errorf("update_operation phase lease_id is runtime-owned and cannot be set by tool input")
	}
	phase := session.OperationPhase{
		ID:                       inputID,
		Summary:                  strings.TrimSpace(in.Summary),
		AuthorityClass:           strings.TrimSpace(in.AuthorityClass),
		WhyNow:                   strings.TrimSpace(in.WhyNow),
		BoundedEffect:            strings.TrimSpace(in.BoundedEffect),
		AllowedActions:           append([]string(nil), in.AllowedActions...),
		ForbiddenActions:         append([]string(nil), in.ForbiddenActions...),
		ValidationPlan:           append([]string(nil), in.ValidationPlan...),
		RequiredCapabilityGrants: parseCapabilityGrantSpecInputs(in.RequiredCapabilityGrants),
		GateLevel:                strings.TrimSpace(in.GateLevel),
		GateReasonCode:           strings.TrimSpace(in.GateReasonCode),
		ApprovalSubject:          strings.TrimSpace(in.ApprovalSubject),
		BlockedReasonCode:        strings.TrimSpace(in.BlockedReasonCode),
		SupersedesPhaseIDs:       append([]string(nil), in.SupersedesPhaseIDs...),
	}
	if in.AutoApproveEligible != nil {
		value := *in.AutoApproveEligible
		phase.AutoApproveEligible = &value
	}
	if in.RequiresConsent != nil {
		phase.RequiresConsent = *in.RequiresConsent
	}
	if in.RequiresOptIn != nil {
		phase.RequiresOptIn = *in.RequiresOptIn
	}
	if in.StaleAuthority != nil {
		phase.StaleAuthority = *in.StaleAuthority
	}
	if strings.TrimSpace(in.Status) != "" {
		phase.Status = session.NormalizePlanStatus(session.PlanStatus(in.Status))
		if phase.Status == "" {
			return session.OperationPhase{}, fmt.Errorf("update_operation phase status must be pending, in_progress, or completed")
		}
	}
	if len(in.RequiredCapabilityGrants) > 0 {
		phase.RequiredCapabilityGrants = parseCapabilityGrantSpecInputs(in.RequiredCapabilityGrants)
	}
	if in.RequiresApproval != nil {
		phase.RequiresApproval = *in.RequiresApproval
	} else if phase.Status != session.PlanStatusCompleted {
		phase.RequiresApproval = true
	}
	plan := session.NormalizeOperationState(session.OperationState{PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{phase}}}).PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}, nil
	}
	phase = plan.Phases[0]
	if inputID == "" {
		phase.ID = ""
	}
	return phase, nil
}

func parseCapabilityGrantSpecInputs(inputs []capabilityGrantSpecInput) []session.CapabilityGrantSpec {
	specs := make([]session.CapabilityGrantSpec, 0, len(inputs))
	for _, in := range inputs {
		spec := session.CapabilityGrantSpec{
			RequestID:      strings.TrimSpace(in.RequestID),
			GrantID:        strings.TrimSpace(in.GrantID),
			Kind:           session.CapabilityKind(in.Kind),
			TargetResource: strings.TrimSpace(in.TargetResource),
			GrantedTo:      strings.TrimSpace(in.GrantedTo),
			AllowedActions: append([]string(nil), in.AllowedActions...),
		}
		if len(in.Contract) > 0 {
			spec.Contract = strings.TrimSpace(string(in.Contract))
		}
		if len(in.Constraints) > 0 {
			spec.Constraints = strings.TrimSpace(string(in.Constraints))
		}
		if raw := strings.TrimSpace(in.ExpiresAt); raw != "" {
			if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				spec.ExpiresAt = t.UTC()
			}
		}
		specs = append(specs, spec)
	}
	return session.NormalizeCapabilityGrantSpecs(specs)
}

func mergeOperationPhaseInput(current session.OperationPhase, in updateOperationPhaseInput) (session.OperationPhase, error) {
	phase := current
	if strings.TrimSpace(in.LeaseID) != "" {
		return session.OperationPhase{}, fmt.Errorf("update_operation phase lease_id is runtime-owned and cannot be set by tool input")
	}
	if id := strings.TrimSpace(in.ID); id != "" {
		phase.ID = id
	}
	if summary := strings.TrimSpace(in.Summary); summary != "" {
		phase.Summary = summary
	}
	if strings.TrimSpace(in.Status) != "" {
		status := session.NormalizePlanStatus(session.PlanStatus(in.Status))
		if status == "" {
			return session.OperationPhase{}, fmt.Errorf("update_operation phase status must be pending, in_progress, or completed")
		}
		phase.Status = status
	}
	if authorityClass := strings.TrimSpace(in.AuthorityClass); authorityClass != "" {
		phase.AuthorityClass = authorityClass
	}
	if whyNow := strings.TrimSpace(in.WhyNow); whyNow != "" {
		phase.WhyNow = whyNow
	}
	if boundedEffect := strings.TrimSpace(in.BoundedEffect); boundedEffect != "" {
		phase.BoundedEffect = boundedEffect
	}
	if in.AllowedActions != nil {
		phase.AllowedActions = append([]string(nil), in.AllowedActions...)
	}
	if in.ForbiddenActions != nil {
		phase.ForbiddenActions = append([]string(nil), in.ForbiddenActions...)
	}
	if in.ValidationPlan != nil {
		phase.ValidationPlan = append([]string(nil), in.ValidationPlan...)
	}
	if gateLevel := strings.TrimSpace(in.GateLevel); gateLevel != "" {
		phase.GateLevel = gateLevel
	}
	if gateReasonCode := strings.TrimSpace(in.GateReasonCode); gateReasonCode != "" {
		phase.GateReasonCode = gateReasonCode
	}
	if approvalSubject := strings.TrimSpace(in.ApprovalSubject); approvalSubject != "" {
		phase.ApprovalSubject = approvalSubject
	}
	if in.AutoApproveEligible != nil {
		value := *in.AutoApproveEligible
		phase.AutoApproveEligible = &value
	}
	if blockedReasonCode := strings.TrimSpace(in.BlockedReasonCode); blockedReasonCode != "" {
		phase.BlockedReasonCode = blockedReasonCode
	}
	if in.RequiresConsent != nil {
		phase.RequiresConsent = *in.RequiresConsent
	}
	if in.RequiresOptIn != nil {
		phase.RequiresOptIn = *in.RequiresOptIn
	}
	if in.SupersedesPhaseIDs != nil {
		phase.SupersedesPhaseIDs = append([]string(nil), in.SupersedesPhaseIDs...)
	}
	if in.StaleAuthority != nil {
		phase.StaleAuthority = *in.StaleAuthority
	}
	if len(in.RequiredCapabilityGrants) > 0 {
		phase.RequiredCapabilityGrants = parseCapabilityGrantSpecInputs(in.RequiredCapabilityGrants)
	}
	if in.RequiresApproval != nil {
		phase.RequiresApproval = *in.RequiresApproval
	}
	plan := session.NormalizeOperationState(session.OperationState{PhasePlan: session.OperationPhasePlan{Phases: []session.OperationPhase{phase}}}).PhasePlan
	if len(plan.Phases) == 0 {
		return session.OperationPhase{}, nil
	}
	return plan.Phases[0], nil
}
