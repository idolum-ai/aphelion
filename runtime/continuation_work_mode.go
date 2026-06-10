//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"time"

	runtimecontinuation "github.com/idolum-ai/aphelion/runtime/continuation"
	"github.com/idolum-ai/aphelion/session"
)

func continuationWorkMode(state session.ContinuationState) WorkMode {
	return runtimecontinuation.WorkModeForState(state)
}

func continuationWorkModeAccessCheck(state session.ContinuationState, mode WorkMode, now time.Time) session.ContinuationLeaseAccessDecision {
	return runtimecontinuation.LeaseAccessCheck(state, mode, now)
}

func continuationAllowedWorkModeRank(state session.ContinuationState) int {
	return runtimecontinuation.AllowedWorkModeRank(state)
}

func continuationWorkModeForbiddenByLease(state session.ContinuationState, mode WorkMode) bool {
	return runtimecontinuation.WorkModeForbiddenByLease(state, mode)
}

func workModeFromStructuredAuthorityList(values []string) WorkMode {
	return runtimecontinuation.WorkModeFromStructuredAuthorityList(values)
}

func workModeFromStructuredAuthority(value string) WorkMode {
	return runtimecontinuation.WorkModeFromStructuredAuthority(value)
}

func strongestWorkMode(modes ...WorkMode) WorkMode {
	return runtimecontinuation.StrongestWorkMode(modes...)
}

func workModeRank(mode WorkMode) int {
	return runtimecontinuation.WorkModeRank(mode)
}

func workPromptForContinuation(state session.ContinuationState, opState session.OperationState) string {
	state = session.NormalizeContinuationState(state)
	opState = session.NormalizeOperationState(opState)
	lines := []string{
		"Role: You are the bounded work executor for a runtime-approved continuation.",
		"",
		"## Goal",
		"Complete only the approved next step and return evidence the parent runtime can store and summarize.",
		"",
		"## Success Criteria",
		"- Stay within the lease, work mode, repository, and sandbox implied by this request.",
		"- Preserve durable operation context and do not collapse a broad objective into a one-step plan.",
		"- Validate meaningful edits, generated artifacts, service actions, or conclusions with the narrowest relevant check available.",
		"- Report changed files, commands, tests, evidence, residual risk, and any blocked validation.",
		"",
		"## Constraints",
		"- Do not expand authority, credentials, network use, deploy, restart, commit, or external effects beyond this approved lease.",
		"- Do not ask for approval to make a plan. If more work remains, propose concrete bounded next phases or lanes.",
		"",
		"## Stop Rules",
		"- Stop before any action outside the lease or any action whose failure could create irreversible, external, privacy, or credential risk.",
		"- If required evidence or validation is unavailable, report that limitation instead of inventing certainty.",
	}
	currentBundlePhase, hasCurrentBundlePhase := currentContinuationBundlePhase(state.ApprovalBundle)
	if objective := firstNonEmptyContinuation(opState.Objective, state.Objective); objective != "" {
		lines = append(lines, "Objective: "+objective)
	}
	if hasCurrentBundlePhase {
		phaseID := firstNonEmptyContinuation(currentBundlePhase.OperationPhaseID, currentBundlePhase.ID)
		if phaseID != "" {
			lines = append(lines, "Approved bundle phase: "+phaseID)
		}
		if authority := strings.TrimSpace(currentBundlePhase.AuthorityClass); authority != "" {
			lines = append(lines, "Phase authority class: "+authority)
		}
	}
	if summary := firstNonEmptyContinuation(currentBundlePhase.Summary, state.ActionProposal.Summary, state.StageSummary); summary != "" {
		lines = append(lines, "Next step: "+summary)
	}
	if effect := firstNonEmptyContinuation(currentBundlePhase.BoundedEffect, state.ActionProposal.BoundedEffect); effect != "" {
		lines = append(lines, "Bounded effect: "+effect)
	}
	if hasCurrentBundlePhase && len(currentBundlePhase.AllowedActions) > 0 {
		lines = append(lines, "Allowed phase actions: "+strings.Join(currentBundlePhase.AllowedActions, ", "))
	}
	if hasCurrentBundlePhase && len(currentBundlePhase.ForbiddenActions) > 0 {
		lines = append(lines, "Forbidden phase actions: "+strings.Join(currentBundlePhase.ForbiddenActions, ", "))
	}
	if opState.PhasePlan.Active() {
		lines = append(lines, "Durable phase plan: "+firstNonEmptyContinuation(opState.PhasePlan.Goal, opState.PhasePlan.ID))
		if current := strings.TrimSpace(opState.PhasePlan.CurrentPhaseID); current != "" {
			lines = append(lines, "Current phase id: "+current)
		}
		for _, phase := range opState.PhasePlan.Phases {
			label := strings.TrimSpace(phase.ID)
			if summary := strings.TrimSpace(phase.Summary); summary != "" {
				if label == "" {
					label = summary
				} else {
					label += ": " + summary
				}
			}
			if label == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("Phase [%s] %s", phase.Status, label))
		}
	}
	lines = append(lines, "Stop after this bounded step and report changed files, commands, tests, evidence, and remaining risk.")
	return strings.Join(lines, "\n")
}
