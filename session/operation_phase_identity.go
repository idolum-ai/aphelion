//go:build linux

package session

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func OperationPhaseProposalID(opState OperationState, phase OperationPhase) string {
	opState = NormalizeOperationState(opState)
	phase = normalizeOperationPhaseForIdentity(phase)
	base := firstNonEmptyOperationPhaseIdentity(opState.ID, opState.PhasePlan.ID, "operation")
	phaseID := firstNonEmptyOperationPhaseIdentity(phase.ID, phase.Summary, "phase")
	id := sanitizeOperationPhaseProposalID("phase-" + base + "-" + phaseID)
	if len(id) <= 128 {
		return id
	}
	return strings.TrimRight(id[:96], "-_") + "-" + core.ContinuationCallbackAlias(id)
}

func OperationPhaseRequiresWorkEvidence(phase OperationPhase) bool {
	phase = normalizeOperationPhaseForIdentity(phase)
	contract, ok := AuthorityContractFor(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect)
	if !ok {
		return false
	}
	if contract.ExternalEffectsAllowed {
		return true
	}
	switch strings.TrimSpace(contract.WorkAction) {
	case AuthorityWorkActionWorkspaceWrite, AuthorityWorkActionCommit, AuthorityWorkActionDeploy:
		return true
	default:
		return false
	}
}

func OperationPhaseWorkAction(phase OperationPhase) string {
	phase = normalizeOperationPhaseForIdentity(phase)
	contract, ok := AuthorityContractFor(phase.AuthorityClass, phase.AllowedActions, phase.BoundedEffect)
	if !ok {
		return ""
	}
	return strings.TrimSpace(contract.WorkAction)
}

func normalizeOperationPhaseForIdentity(phase OperationPhase) OperationPhase {
	plan := NormalizeOperationState(OperationState{PhasePlan: OperationPhasePlan{Phases: []OperationPhase{phase}}}).PhasePlan
	if len(plan.Phases) == 0 {
		return OperationPhase{}
	}
	return plan.Phases[0]
}

func firstNonEmptyOperationPhaseIdentity(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func sanitizeOperationPhaseProposalID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
