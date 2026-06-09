//go:build linux

package telegramcommands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/stoplabels"
	"github.com/idolum-ai/aphelion/session"
)

const continuationCallbackPrefix = core.ContinuationCallbackPrefix
const staleContinuationCallbackText = "This continuation prompt is no longer active. Use the newest prompt."
const continuationCallbackFailureText = "Continuation action failed. Check /health diagnose for details."

const (
	continuationActionApproveLease         = "approve_lease"
	continuationActionApproveBundleAll     = "approve_bundle_all"
	continuationActionApproveBundleCurrent = "approve_bundle_current"
	continuationActionContinueOnce         = "continue_once"
	continuationActionAskEdit              = "ask_edit"
	continuationActionStop                 = "stop"
	continuationActionStopPark             = "stop_park"
	continuationActionResumeEdge           = "resume_edge"
	continuationActionAskNextLease         = "ask_next_lease"
	continuationActionStatusOnly           = "status_only"
)

func encodeContinuationCallbackData(decisionID string, action string) string {
	decisionID = strings.TrimSpace(decisionID)
	action = normalizeContinuationCallbackAction(action)
	if action == "" {
		return ""
	}
	return core.EncodeContinuationCallbackData(decisionID, action)
}

func decodeContinuationCallbackData(data string) (decisionID string, action string, ok bool) {
	decisionID, action, ok = core.DecodeContinuationCallbackData(data)
	if !ok {
		return "", "", false
	}
	action = normalizeContinuationCallbackAction(action)
	if action == "" {
		return "", "", false
	}
	return decisionID, action, true
}

func normalizeContinuationCallbackAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case continuationActionApproveLease, "approve-lease":
		return continuationActionApproveLease
	case continuationActionApproveBundleAll, "approve-bundle-all", "approve_all", "approve-all":
		return continuationActionApproveBundleAll
	case continuationActionApproveBundleCurrent, "approve-bundle-current", "approve_current", "approve-current", "approve-subset":
		return continuationActionApproveBundleCurrent
	case continuationActionContinueOnce, "continue-once":
		return continuationActionContinueOnce
	case continuationActionAskEdit, "ask-edit", "edit":
		return continuationActionAskEdit
	case continuationActionStop:
		return continuationActionStop
	case continuationActionStopPark, "stop-park", "park":
		return continuationActionStopPark
	case continuationActionResumeEdge, "resume-edge", "resume":
		return continuationActionResumeEdge
	case continuationActionAskNextLease, "ask-next-lease", "next_lease", "next-lease":
		return continuationActionAskNextLease
	case continuationActionStatusOnly, "status-only", "status":
		return continuationActionStatusOnly
	default:
		return ""
	}
}

func continuationCallbackMatchesState(state session.ContinuationState, decisionID string, action string) bool {
	state = session.NormalizeContinuationState(state)
	decisionID = strings.TrimSpace(decisionID)
	action = normalizeContinuationCallbackAction(action)
	if decisionID == "" || action == "" {
		return false
	}
	if !continuationCallbackIDMatchesState(state, decisionID) {
		return false
	}
	switch action {
	case continuationActionApproveLease, continuationActionApproveBundleAll, continuationActionApproveBundleCurrent, continuationActionContinueOnce:
		return state.Status == session.ContinuationStatusPending && state.RemainingTurns > 0
	case continuationActionAskEdit:
		return state.Status == session.ContinuationStatusPending
	case continuationActionStop, continuationActionStopPark:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved || continuationCallbackStateExpired(state)
	case continuationActionResumeEdge:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved
	case continuationActionAskNextLease, continuationActionStatusOnly:
		return state.Status == session.ContinuationStatusPending || state.Status == session.ContinuationStatusApproved || continuationCallbackStateExpired(state)
	default:
		return false
	}
}

func continuationCallbackStateExpired(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return state.ActionProposal.Status == session.ProposalStatusExpired ||
		state.ContinuationLease.Status == session.ContinuationLeaseStatusExpired
}

func continuationCallbackIDMatchesState(state session.ContinuationState, decisionID string) bool {
	state = session.NormalizeContinuationState(state)
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" {
		return false
	}
	ids := []string{
		strings.TrimSpace(state.DecisionID),
		strings.TrimSpace(state.ActionProposal.ID),
		strings.TrimSpace(state.ContinuationLease.ID),
		strings.TrimSpace(state.ContinuationLease.ProposalID),
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if decisionID == id || decisionID == core.ContinuationCallbackAlias(id) {
			return true
		}
	}
	return false
}

func renderContinuationDecision(state session.ContinuationState, action string) string {
	state = session.NormalizeContinuationState(state)
	switch normalizeContinuationCallbackAction(action) {
	case continuationActionApproveLease:
		return renderContinuationApprovedDecision(state, "Approved.")
	case continuationActionApproveBundleAll:
		return renderContinuationApprovedDecision(state, "Approved. I will use each approved step only when it becomes current.")
	case continuationActionApproveBundleCurrent:
		return renderContinuationApprovedDecision(state, "Current step approved. Later steps will ask again before I use them.")
	case continuationActionContinueOnce:
		return renderContinuationApprovedDecision(state, "Approved for one step.")
	case continuationActionAskEdit:
		return "I parked this request for edits; nothing was approved or started."
	case continuationActionAskNextLease:
		return renderContinuationEdgeStatus(state, "Next step needs approval.")
	case continuationActionStatusOnly:
		if state.Status == session.ContinuationStatusPending {
			return renderContinuationScopeDetails(state, "Scope details.")
		}
		return renderContinuationEdgeStatus(state, "Current request status.")
	case continuationActionResumeEdge:
		if state.Status == session.ContinuationStatusApproved && state.RemainingTurns > 0 {
			return renderContinuationApprovedDecision(state, "Resuming the approved step.")
		}
		return renderContinuationEdgeStatus(state, "This step needs your approval first.")
	default:
		return renderContinuationEdgeStatus(state, "Decision recorded.")
	}
}

func continuationCallbackErrorText(err error) string {
	switch {
	case errors.Is(err, core.ErrContinuationExpired):
		return "That continuation request expired before you could approve it."
	case errors.Is(err, core.ErrContinuationNotPending), errors.Is(err, core.ErrContinuationNoTurns), errors.Is(err, core.ErrContinuationStale):
		return staleContinuationCallbackText
	default:
		return continuationCallbackFailureText
	}
}

func renderContinuationCallbackError(state session.ContinuationState, err error) string {
	switch {
	case errors.Is(err, core.ErrContinuationExpired):
		return renderContinuationEdgeStatus(state, "Continuation request expired before approval.")
	case errors.Is(err, core.ErrContinuationStale):
		return renderContinuationEdgeStatus(state, "Continuation prompt is stale. Use the newest prompt; old buttons cannot approve a changed plan.")
	case errors.Is(err, core.ErrContinuationNotPending), errors.Is(err, core.ErrContinuationNoTurns):
		return renderContinuationEdgeStatus(state, "Continuation prompt is no longer active.")
	default:
		return renderContinuationEdgeStatus(state, "Continuation action failed.")
	}
}

func renderContinuationRefreshedDecision(state session.ContinuationState) string {
	return renderContinuationEdgeStatus(state, "Continuation request expired before approval. I sent a fresh approval prompt.")
}

func renderContinuationRefreshAlreadyActiveDecision(state session.ContinuationState) string {
	return renderContinuationEdgeStatus(state, "Continuation request expired before approval. A fresh approval prompt is already active.")
}

func renderContinuationApprovedDecision(state session.ContinuationState, prefix string) string {
	text := strings.TrimSpace(prefix)
	if text == "" {
		text = "Approved."
	}
	if state.StageSummary != "" {
		text += " Next: " + state.StageSummary
	}
	return text
}

func renderContinuationEdgeStatus(state session.ContinuationState, prefix string) string {
	state = session.NormalizeContinuationState(state)
	lines := []string{strings.TrimSpace(prefix)}
	if lines[0] == "" {
		lines[0] = "Current request."
	}
	if state.Status != "" {
		lines = append(lines, "Status: "+string(state.Status))
	}
	if state.Objective != "" {
		lines = append(lines, "Objective: "+state.Objective)
	}
	if state.StageSummary != "" {
		lines = append(lines, "Next: "+state.StageSummary)
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Approval window: %d step(s)", state.RemainingTurns))
	}
	if state.HandshakeBlockedReason != "" {
		lines = append(lines, "Why paused: "+continuationBlockedReasonLabel(state.HandshakeBlockedReason))
	}
	return strings.Join(lines, "\n")
}

func renderContinuationScopeDetails(state session.ContinuationState, prefix string) string {
	state = session.NormalizeContinuationState(state)
	if continuationStateHasApprovalBundle(state) {
		return renderContinuationApprovalBundleDetails(state, prefix)
	}
	if continuationStateIsPlanBudget(state) {
		return renderContinuationPlanBudgetDetails(state, prefix)
	}
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	lines := []string{strings.TrimSpace(prefix)}
	if lines[0] == "" {
		lines[0] = "Scope details."
	}
	if state.Status != "" {
		lines = append(lines, "Status: "+string(state.Status))
	}
	if state.Objective != "" {
		lines = append(lines, "Objective: "+state.Objective)
	}
	if state.StageSummary != "" {
		lines = append(lines, "Next: "+state.StageSummary)
	}
	if proposal.Summary != "" {
		lines = append(lines, "Step: "+proposal.Summary)
	}
	if proposal.WhyNow != "" {
		lines = append(lines, "Why now: "+proposal.WhyNow)
	}
	if proposal.BoundedEffect != "" {
		lines = append(lines, "Scope: "+proposal.BoundedEffect)
	}
	if allowed := firstNonEmptyContinuationCommandList(proposal.AllowedActions, lease.AllowedActions); len(allowed) > 0 {
		lines = append(lines, "Can do: "+strings.Join(allowed, ", "))
	}
	if forbidden := firstNonEmptyContinuationCommandList(proposal.ForbiddenActions, lease.ForbiddenActions); len(forbidden) > 0 {
		lines = append(lines, "Stops before: "+strings.Join(forbidden, ", "))
	}
	if validation := firstNonEmptyContinuationCommandList(proposal.ValidationPlan, lease.ValidationPlan); len(validation) > 0 {
		lines = append(lines, "Checks: "+strings.Join(validation, "; "))
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Approval window: %d step(s)", state.RemainingTurns))
	}
	return strings.Join(lines, "\n")
}

func continuationStateIsPlanBudget(state session.ContinuationState) bool {
	state = session.NormalizeContinuationState(state)
	return strings.TrimSpace(state.ActionProposal.RiskClass) == "plan_lease" ||
		actionListContainsMain(state.ActionProposal.AllowedActions, "approve_operation_plan_lease") ||
		actionListContainsMain(state.ContinuationLease.AllowedActions, "approve_operation_plan_lease")
}

func renderContinuationApprovalBundleDetails(state session.ContinuationState, prefix string) string {
	lines := continuationScopeDetailLines(state, prefix)
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) > 0 {
		lines = append(lines, "Grouped approval: each step is still used only when it becomes current.")
		lines = append(lines, "Approve all: approve the listed steps now; I will spend them one at a time.")
		lines = append(lines, "Approve current: approve only the current step and ask again for later steps.")
		lines = append(lines, "Changed plan rule: old buttons cannot approve a changed plan.")
	}
	return strings.Join(lines, "\n")
}

func continuationScopeDetailLines(state session.ContinuationState, prefix string) []string {
	state = session.NormalizeContinuationState(state)
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	lines := []string{strings.TrimSpace(prefix)}
	if lines[0] == "" {
		lines[0] = "Scope details."
	}
	if state.Status != "" {
		lines = append(lines, "Status: "+string(state.Status))
	}
	if state.Objective != "" {
		lines = append(lines, "Objective: "+state.Objective)
	}
	if state.StageSummary != "" {
		lines = append(lines, "Next: "+state.StageSummary)
	}
	if proposal.Summary != "" {
		lines = append(lines, "Step: "+proposal.Summary)
	}
	if proposal.WhyNow != "" {
		lines = append(lines, "Why now: "+proposal.WhyNow)
	}
	if proposal.BoundedEffect != "" {
		lines = append(lines, "Scope: "+proposal.BoundedEffect)
	}
	if allowed := firstNonEmptyContinuationCommandList(proposal.AllowedActions, lease.AllowedActions); len(allowed) > 0 {
		lines = append(lines, "Can do: "+strings.Join(allowed, ", "))
	}
	if forbidden := firstNonEmptyContinuationCommandList(proposal.ForbiddenActions, lease.ForbiddenActions); len(forbidden) > 0 {
		lines = append(lines, "Stops before: "+strings.Join(forbidden, ", "))
	}
	if validations := firstNonEmptyContinuationCommandList(proposal.ValidationPlan, lease.ValidationPlan); len(validations) > 0 {
		lines = append(lines, "Checks: "+strings.Join(validations, "; "))
	}
	return lines
}
func renderContinuationPlanBudgetDetails(state session.ContinuationState, prefix string) string {
	state = session.NormalizeContinuationState(state)
	lines := []string{strings.TrimSpace(prefix)}
	if lines[0] == "" {
		lines[0] = "Plan scope details."
	}
	if state.Objective != "" {
		lines = append(lines, "Goal: "+state.Objective)
	}
	if state.RemainingTurns > 0 {
		lines = append(lines, fmt.Sprintf("Approved steps remaining: %d", state.RemainingTurns))
	}
	bundle := session.NormalizeContinuationApprovalBundle(state.ApprovalBundle)
	if len(bundle.Phases) > 0 {
		lines = append(lines, "Included:")
		for _, phase := range bundle.Phases {
			label := fmt.Sprintf("- phase %d", phase.Index)
			if summary := strings.TrimSpace(phase.Summary); summary != "" {
				label += ": " + summary
			}
			if authority := strings.TrimSpace(phase.AuthorityClass); authority != "" {
				label += " [" + authority + "]"
			}
			lines = append(lines, label)
		}
	}
	stops := compactContinuationStops(state)
	if len(stops) > 0 {
		lines = append(lines, "Stops for: "+strings.Join(stops, ", "))
	}
	return strings.Join(lines, "\n")
}

func compactContinuationStops(state session.ContinuationState) []string {
	proposal := session.NormalizeActionProposal(state.ActionProposal)
	values := firstNonEmptyContinuationCommandList(proposal.ForbiddenActions, state.ContinuationLease.ForbiddenActions)
	return stoplabels.LabelsForContinuationState(state, values, stoplabels.Options{
		Defaults: []string{"anything outside scope", "hard gates", "deploy/restart", "policy or permission changes", "mailbox access or mutation"},
		Limit:    5,
	})
}

func continuationBlockedReasonLabel(reason string) string {
	switch strings.TrimSpace(reason) {
	case "persona_intent_missing", "persona_rationale_missing":
		return "the next step needs a clearer request"
	case "persona_not_willing":
		return "I chose to stop here instead of continuing automatically"
	case "governor_intent_missing", "governor_rationale_missing", "governor_not_ratified", "governor_not_willing":
		return "the next step needs a safer approval path"
	default:
		if trimmed := strings.TrimSpace(reason); trimmed != "" {
			return strings.ReplaceAll(trimmed, "_", " ")
		}
		return "the next step is not ready"
	}
}

func actionListContainsMain(values []string, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == want {
			return true
		}
	}
	return false
}

func firstNonEmptyContinuationCommandList(values ...[]string) []string {
	for _, list := range values {
		out := make([]string, 0, len(list))
		for _, value := range list {
			value = strings.TrimSpace(value)
			if value != "" {
				out = append(out, value)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
