//go:build linux

package tool

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

func operationInputEmpty(in updateOperationInput) bool {
	return strings.TrimSpace(in.ID) == "" &&
		strings.TrimSpace(in.Objective) == "" &&
		strings.TrimSpace(in.Status) == "" &&
		strings.TrimSpace(in.Stage) == "" &&
		strings.TrimSpace(in.Summary) == "" &&
		!in.Merge &&
		in.Proposal == nil &&
		in.PhasePlan == nil &&
		in.PlanLease == nil &&
		in.Findings == nil &&
		in.Artifacts == nil &&
		in.RecoveryHandoff == nil
}

func applyOperationInput(current session.OperationState, in updateOperationInput) (session.OperationState, error) {
	current = session.NormalizeOperationState(current)
	if in.Merge {
		return mergeOperationInput(current, in)
	}

	state := session.OperationState{
		ID:        strings.TrimSpace(in.ID),
		Objective: strings.TrimSpace(in.Objective),
		Status:    session.NormalizeOperationStatus(session.OperationStatus(in.Status)),
		Stage:     strings.TrimSpace(in.Stage),
		Summary:   strings.TrimSpace(in.Summary),
		Work:      current.Work,
	}
	if strings.TrimSpace(in.Status) != "" && state.Status == "" {
		return session.OperationState{}, fmt.Errorf("update_operation status must be idle, active, blocked, completed, or failed")
	}

	proposal, err := parseOperationProposalInput(in.Proposal)
	if err != nil {
		return session.OperationState{}, err
	}
	state.Proposal = proposal

	phasePlan, err := parseOperationPhasePlanInput(in.PhasePlan)
	if err != nil {
		return session.OperationState{}, err
	}
	state.PhasePlan = phasePlan

	planLease, err := parseOperationPlanLeaseInput(in.PlanLease)
	if err != nil {
		return session.OperationState{}, err
	}
	state.PlanLease = planLease

	findings, err := parseOperationFindingInputs(in.Findings)
	if err != nil {
		return session.OperationState{}, err
	}
	state.Findings = findings

	artifacts, err := parseOperationArtifactInputs(in.Artifacts)
	if err != nil {
		return session.OperationState{}, err
	}
	state.Artifacts = artifacts

	recoveryHandoff, err := parseOperationRecoveryHandoffInput(in.RecoveryHandoff)
	if err != nil {
		return session.OperationState{}, err
	}
	state.RecoveryHandoff = recoveryHandoff

	return session.NormalizeOperationState(state), nil
}

func mergeOperationInput(current session.OperationState, in updateOperationInput) (session.OperationState, error) {
	state := current

	if id := strings.TrimSpace(in.ID); id != "" {
		state.ID = id
	}
	if objective := strings.TrimSpace(in.Objective); objective != "" {
		state.Objective = objective
	}
	if strings.TrimSpace(in.Status) != "" {
		status := session.NormalizeOperationStatus(session.OperationStatus(in.Status))
		if status == "" {
			return session.OperationState{}, fmt.Errorf("update_operation status must be idle, active, blocked, completed, or failed")
		}
		state.Status = status
	}
	if stage := strings.TrimSpace(in.Stage); stage != "" {
		state.Stage = stage
	}
	if summary := strings.TrimSpace(in.Summary); summary != "" {
		state.Summary = summary
	}

	if in.Proposal != nil {
		proposal, err := mergeOperationProposalInput(state.Proposal, *in.Proposal)
		if err != nil {
			return session.OperationState{}, err
		}
		state.Proposal = proposal
	}

	if in.PhasePlan != nil {
		phasePlan, err := mergeOperationPhasePlanInput(state.PhasePlan, *in.PhasePlan)
		if err != nil {
			return session.OperationState{}, err
		}
		state.PhasePlan = phasePlan
	}

	if in.PlanLease != nil {
		planLease, err := mergeOperationPlanLeaseInput(state.PlanLease, *in.PlanLease)
		if err != nil {
			return session.OperationState{}, err
		}
		state.PlanLease = planLease
	}

	findings, err := parseOperationFindingInputs(in.Findings)
	if err != nil {
		return session.OperationState{}, err
	}
	if in.Findings != nil {
		state.Findings = appendDedupedFindings(state.Findings, findings)
	}

	artifacts, err := parseOperationArtifactInputs(in.Artifacts)
	if err != nil {
		return session.OperationState{}, err
	}
	if in.Artifacts != nil {
		state.Artifacts = appendDedupedArtifacts(state.Artifacts, artifacts)
	}

	if in.RecoveryHandoff != nil {
		recoveryHandoff, err := parseOperationRecoveryHandoffInput(in.RecoveryHandoff)
		if err != nil {
			return session.OperationState{}, err
		}
		state.RecoveryHandoff = recoveryHandoff
	}

	return session.NormalizeOperationState(state), nil
}

func parseOperationRecoveryHandoffInput(in *updateOperationRecoveryHandoffInput) (session.OperationRecoveryHandoff, error) {
	if in == nil {
		return session.OperationRecoveryHandoff{}, nil
	}
	handoff := session.OperationRecoveryHandoff{
		Contract:          strings.TrimSpace(in.Contract),
		OperationKind:     strings.TrimSpace(in.OperationKind),
		OperationTool:     strings.TrimSpace(in.OperationTool),
		RetryPolicy:       strings.TrimSpace(in.RetryPolicy),
		RequiredAuthority: strings.TrimSpace(in.RequiredAuthority),
		ResourceBlocker:   strings.TrimSpace(in.ResourceBlocker),
		DurableAgentID:    strings.TrimSpace(in.DurableAgentID),
		AgentID:           strings.TrimSpace(in.AgentID),
		BlockerKind:       strings.TrimSpace(in.BlockerKind),
		TaskPacketID:      strings.TrimSpace(in.TaskPacketID),
		ChildResultID:     strings.TrimSpace(in.ChildResultID),
		Tool:              strings.TrimSpace(in.Tool),
		Adapter:           strings.TrimSpace(in.Adapter),
	}
	if in.DiagnosticOnly != nil {
		handoff.DiagnosticOnly = *in.DiagnosticOnly
	}
	if in.NoContentProbe != nil {
		handoff.NoContentProbe = *in.NoContentProbe
	}
	handoff = session.NormalizeOperationRecoveryHandoff(handoff)
	if handoff.Contract != "" && handoff.Contract != recoveryHandoffContractVersion {
		return session.OperationRecoveryHandoff{}, fmt.Errorf("update_operation recovery_handoff contract must be %s", recoveryHandoffContractVersion)
	}
	if handoff.Contract != "" && handoff.OperationKind == "" {
		return session.OperationRecoveryHandoff{}, fmt.Errorf("update_operation recovery_handoff operation_kind is required")
	}
	return handoff, nil
}
