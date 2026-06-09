//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

const goalContinuationIDPrefix = "goal-continuation-"

type goalContinuationCandidate struct {
	ID            string
	Kind          string
	Objective     string
	Summary       string
	StateSummary  string
	WhyNow        string
	BoundedEffect string
	Basis         string
	FindingClaim  string
}

func (r *Runtime) maybeInferGoalContinuationProposal(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, result *turn.Result) (bool, error) {
	_ = ctx
	if r == nil || r.store == nil || msg.ChatID == 0 {
		return false, nil
	}
	if strings.HasPrefix(strings.TrimSpace(msg.Text), "/") {
		return false, nil
	}
	priorContinuation, priorExists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, err
	}
	priorContinuation = session.NormalizeContinuationState(priorContinuation)
	if priorExists && priorContinuation.Active() {
		return false, nil
	}

	opState, err := r.store.OperationState(key)
	if err != nil {
		return false, err
	}
	opState = session.NormalizeOperationState(opState)
	persistedOpState := opState
	if result != nil && result.OperationState.Active() {
		resultOp := session.NormalizeOperationState(result.OperationState)
		if !pendingOperationProposalNeedsButton(resultOp.Proposal) {
			if !resultOp.PhasePlan.Active() && persistedOpState.PhasePlan.Active() {
				resultOp.PhasePlan = persistedOpState.PhasePlan
			}
			opState = resultOp
		}
	}
	if operationPhasePlanOwnsContinuation(opState.PhasePlan) {
		return false, nil
	}
	if pendingOperationProposalNeedsButton(opState.Proposal) {
		return false, nil
	}
	if opState.Proposal.Status == session.ProposalStatusPending {
		return false, nil
	}
	if opState.Status == session.OperationStatusFailed {
		return false, nil
	}

	planState, _ := r.store.PlanState(key)
	planState = session.NormalizePlanState(planState)
	if result != nil && len(result.PlanState.Steps) > 0 {
		planState = session.NormalizePlanState(result.PlanState)
	}

	candidate, ok := goalContinuationCandidateFromState(msg, promptInput, opState, planState, priorContinuation, priorExists, result)
	if !ok {
		return false, nil
	}
	now := time.Now().UTC()
	proposalID := candidate.ID
	if proposalID == "" {
		proposalID = goalContinuationProposalID(candidate, msg)
	}
	state := opState
	if strings.TrimSpace(state.ID) == "" {
		state.ID = goalContinuationIDPrefix + strings.TrimPrefix(proposalID, goalContinuationIDPrefix)
	}
	state.Objective = firstNonEmptyContinuation(candidate.Objective, state.Objective, summarizeContinuationFallback(promptInput))
	state.Status = session.OperationStatusBlocked
	state.Stage = "next_phase_proposal"
	state.Summary = firstNonEmptyContinuation(candidate.StateSummary, "A prior lease completed only an initial phase; the broader goal still needs an explicit next bounded lease.")
	state.Proposal = session.OperationProposal{
		ID:            proposalID,
		Kind:          firstNonEmptyContinuation(candidate.Kind, "read_only_review"),
		OperatorTitle: continuationPlanTitleFromText(candidate.Summary),
		PlanTitle:     continuationPlanTitleFromText(candidate.Objective),
		Summary:       candidate.Summary,
		WhyNow:        candidate.WhyNow,
		BoundedEffect: candidate.BoundedEffect,
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	state.Findings = append(state.Findings, session.OperationFinding{
		Claim:      firstNonEmptyContinuation(candidate.FindingClaim, "Goal continuation inference found a broader objective after a phase-one lease was consumed."),
		Confidence: session.FindingConfidenceHigh,
		Basis:      candidate.Basis,
	})
	if msg.MessageID != 0 {
		state.Artifacts = append(state.Artifacts, session.OperationArtifact{
			Label: "source_message",
			Ref:   fmt.Sprintf("telegram:%d:%d", msg.ChatID, msg.MessageID),
		})
	}
	if strings.TrimSpace(priorContinuation.ActionProposal.ID) != "" {
		state.Artifacts = append(state.Artifacts, session.OperationArtifact{
			Label: "prior_action_proposal",
			Ref:   priorContinuation.ActionProposal.ID,
		})
	}
	state.UpdatedAt = now
	if err := r.store.UpdateOperationState(key, state); err != nil {
		return false, fmt.Errorf("persist goal continuation proposal: %w", err)
	}
	return true, nil
}

func goalContinuationCandidateFromState(
	msg core.InboundMessage,
	promptInput string,
	opState session.OperationState,
	planState session.PlanState,
	priorContinuation session.ContinuationState,
	priorExists bool,
	result *turn.Result,
) (goalContinuationCandidate, bool) {
	opState = session.NormalizeOperationState(opState)
	planState = session.NormalizePlanState(planState)
	priorContinuation = session.NormalizeContinuationState(priorContinuation)
	if !goalContinuationPriorPhaseComplete(msg, opState, priorContinuation, priorExists) {
		return goalContinuationCandidate{}, false
	}
	sourceText := goalContinuationSourceText(promptInput, opState, planState, priorContinuation, result)
	if candidate, ok := goalContinuationConcreteFollowUpCandidate(sourceText, opState, planState, priorContinuation); ok {
		return candidate, true
	}
	if candidate, ok := goalContinuationReleaseReadinessCandidate(sourceText, opState, planState, priorContinuation); ok {
		return candidate, true
	}
	if !goalContinuationHasEnoughSignals(sourceText, planState) {
		return goalContinuationCandidate{}, false
	}
	objective := firstNonEmptyContinuation(
		opState.Objective,
		priorContinuation.Objective,
		planState.Explanation,
		summarizeContinuationFallback(promptInput),
	)
	nextStep := goalContinuationNextStep(objective, planState, opState)
	whyNow := "The prior approved lease appears to have completed a contract, probe, read-only review, or first smoke test, but the durable goal still needs phased follow-through."
	boundedEffect := "Review persisted operation, plan, prior lease result, and local evidence; produce the broader phased plan and exactly one next safe live smoke test proposal; do not edit files, use secrets or credentials, touch external accounts, deploy, restart, commit, or push."
	basis := "Persisted operation/plan/continuation state contained both broad-goal language and phase-one completion language; no explicit continuation contract was required."
	if opState.Proposal.ID != "" {
		basis += " Previous proposal: " + strings.TrimSpace(opState.Proposal.ID) + "."
	}
	return goalContinuationCandidate{
		Objective:     objective,
		Kind:          "read_only_review",
		Summary:       clampContinuationText(nextStep, 160),
		StateSummary:  "A prior lease completed only an initial phase; the broader goal still needs an explicit next bounded lease.",
		WhyNow:        clampContinuationText(whyNow, 240),
		BoundedEffect: boundedEffect,
		Basis:         basis,
	}, true
}

func goalContinuationPriorPhaseComplete(msg core.InboundMessage, opState session.OperationState, prior session.ContinuationState, priorExists bool) bool {
	if opState.Status == session.OperationStatusCompleted {
		return true
	}
	if msg.Origin == core.InboundOriginTurnAuthorization && priorExists && prior.ContinuationLease.Status == session.ContinuationLeaseStatusConsumed {
		return true
	}
	return priorExists && prior.ContinuationLease.Status == session.ContinuationLeaseStatusConsumed
}

func goalContinuationSourceText(promptInput string, opState session.OperationState, planState session.PlanState, prior session.ContinuationState, result *turn.Result) string {
	parts := []string{
		promptInput,
		opState.Objective,
		string(opState.Status),
		opState.Stage,
		opState.Summary,
		opState.Proposal.Summary,
		opState.Proposal.WhyNow,
		opState.Proposal.BoundedEffect,
		planState.Explanation,
		prior.Objective,
		prior.StageSummary,
		prior.ActionProposal.Summary,
		prior.ActionProposal.WhyNow,
		prior.ActionProposal.BoundedEffect,
	}
	for _, step := range planState.Steps {
		parts = append(parts, step.Step, string(step.Status))
	}
	if result != nil {
		parts = append(parts,
			result.VisibleReply,
			result.FloorText,
			result.ProposalNote,
		)
		if result.Turn != nil {
			parts = append(parts, result.Turn.Text)
		}
		resultOp := session.NormalizeOperationState(result.OperationState)
		parts = append(parts,
			resultOp.Objective,
			string(resultOp.Status),
			resultOp.Stage,
			resultOp.Summary,
			resultOp.Proposal.Summary,
			resultOp.Proposal.WhyNow,
			resultOp.Proposal.BoundedEffect,
			resultOp.Work.LastSummary,
		)
		for _, phase := range resultOp.PhasePlan.Phases {
			parts = append(parts,
				phase.ID,
				phase.Summary,
				string(phase.Status),
				phase.AuthorityClass,
				phase.WhyNow,
				phase.BoundedEffect,
			)
		}
		resultPlan := session.NormalizePlanState(result.PlanState)
		parts = append(parts, resultPlan.Explanation)
		for _, step := range resultPlan.Steps {
			parts = append(parts, step.Step, string(step.Status))
		}
	}
	return strings.Join(parts, "\n")
}

func goalContinuationConcreteFollowUpCandidate(text string, opState session.OperationState, planState session.PlanState, prior session.ContinuationState) (goalContinuationCandidate, bool) {
	lower := strings.ToLower(text)
	if !goalContinuationHasBranchPRFollowUpApprovalSegment(lower) {
		return goalContinuationCandidate{}, false
	}
	objective := firstNonEmptyContinuation(
		opState.Objective,
		prior.Objective,
		planState.Explanation,
		"Publish the completed branch for pull request review.",
	)
	basis := "Persisted completion state plus final turn text named a separate bounded branch publication and pull-request follow-up after the prior lease was consumed."
	if opState.Proposal.ID != "" {
		basis += " Previous proposal: " + strings.TrimSpace(opState.Proposal.ID) + "."
	}
	if prior.ActionProposal.ID != "" {
		basis += " Previous action proposal: " + strings.TrimSpace(prior.ActionProposal.ID) + "."
	}
	return goalContinuationCandidate{
		Kind:          "commit_push_pr",
		Objective:     objective,
		Summary:       "Publish the completed branch and open or update one pull request",
		StateSummary:  "A prior approved phase completed inside its boundary; a concrete governed follow-up was named and needs explicit approval.",
		WhyNow:        "The approved local phase completed and the final turn explicitly named branch publication and pull-request review as the next bounded step outside the consumed lease.",
		BoundedEffect: "Use the already-completed local branch or commit evidence to publish the branch if needed, open or update exactly one pull request for review, and report the PR URL and status. Do not edit files, merge, deploy, restart services, change credentials, change policy or permissions, tag or release, or perform unrelated GitHub effects.",
		Basis:         basis,
		FindingClaim:  "Completed phase follow-up inference found a concrete branch publication and pull-request approval that still needs an explicit bounded lease.",
	}, true
}

func goalContinuationHasBranchPRFollowUpApprovalSegment(lower string) bool {
	for _, segment := range strings.FieldsFunc(lower, func(r rune) bool {
		switch r {
		case '\n', '\r', '.', ';':
			return true
		default:
			return false
		}
	}) {
		segment = strings.TrimSpace(segment)
		if goalContinuationHasFollowUpApprovalSignal(segment) && goalContinuationHasBranchPRFollowUpSignal(segment) {
			return true
		}
	}
	return false
}

func goalContinuationHasFollowUpApprovalSignal(lower string) bool {
	for _, needle := range []string{
		"separate bounded approval",
		"bounded approval for",
		"separate approval",
		"follow-up approval",
		"follow up approval",
		"approval for push",
		"approval for branch",
		"approval for pr",
		"approval for pull request",
		"next useful step",
		"next step would be",
		"next useful action",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func goalContinuationHasBranchPRFollowUpSignal(lower string) bool {
	if strings.Contains(lower, "push/pr") || strings.Contains(lower, "push + pr") || strings.Contains(lower, "push and pr") {
		return true
	}
	hasPR := strings.Contains(lower, "pull request") ||
		strings.Contains(lower, "draft pr") ||
		strings.Contains(lower, "open pr") ||
		strings.Contains(lower, "update pr") ||
		strings.Contains(lower, " pr ")
	if !hasPR {
		return false
	}
	return strings.Contains(lower, "push") ||
		strings.Contains(lower, "publish") ||
		strings.Contains(lower, "branch") ||
		strings.Contains(lower, "open or update") ||
		strings.Contains(lower, "open/update")
}

func goalContinuationReleaseReadinessCandidate(text string, opState session.OperationState, planState session.PlanState, prior session.ContinuationState) (goalContinuationCandidate, bool) {
	lower := strings.ToLower(text)
	if !goalContinuationHasOperatorContinueSignal(lower) || !strings.Contains(lower, "release") {
		return goalContinuationCandidate{}, false
	}
	objective := firstNonEmptyContinuation(
		opState.Objective,
		prior.Objective,
		planState.Explanation,
		"Prepare Aphelion for release.",
	)
	basis := "Persisted operation state shows a completed release-related phase, and the operator asked to continue after completion; no active continuation lease remains."
	if opState.Proposal.ID != "" {
		basis += " Previous proposal: " + strings.TrimSpace(opState.Proposal.ID) + "."
	}
	return goalContinuationCandidate{
		Kind:          "read_only_review",
		Objective:     objective,
		Summary:       "Run a read-only release readiness check",
		StateSummary:  "A completed release-related phase needs a fresh bounded read-only follow-up before Aphelion continues.",
		WhyNow:        "The release-process phase completed and the operator asked to continue; the next bounded readiness pass requires explicit approval instead of reusing consumed authority.",
		BoundedEffect: "Inspect persisted operation state, local repository status, latest main alignment, tags or release checklist evidence, and recent validation signals; report release readiness and blockers only. Do not edit files, commit, push, tag, publish a release, deploy, restart services, use credentials, or change policy or permissions.",
		Basis:         basis,
		FindingClaim:  "Completed release-process follow-up inference found a read-only release readiness pass that needs a fresh bounded approval.",
	}, true
}

func goalContinuationHasOperatorContinueSignal(lower string) bool {
	for _, needle := range []string{
		"continue",
		"let's continue",
		"lets continue",
		"proceed",
		"next step",
		"next phase",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func goalContinuationHasEnoughSignals(text string, planState session.PlanState) bool {
	signals := 0
	if goalContinuationLooksBroad(text) {
		signals++
	}
	if goalContinuationLooksLikePhaseOne(text) {
		signals++
	}
	if goalContinuationHasRemainingWork(text, planState) {
		signals++
	}
	return signals >= 2
}

func goalContinuationLooksBroad(text string) bool {
	lower := strings.ToLower(text)
	for _, needle := range []string{
		"goal", "make ", "build ", "enable ", "agent", "integration", "bridge", "inbox",
		"external account", "proton", "lighthouse", "live feature", "workflow",
		"tailnet", "tailscale", "durable", "sandbox", "production",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func goalContinuationLooksLikePhaseOne(text string) bool {
	lower := strings.ToLower(text)
	for _, needle := range []string{
		"contract", "architecture", "read-only", "readonly", "minimal", "first",
		"phase one", "phase-one", "phase 1", "probe", "smoke", "initial", "one simple",
		"first slice", "first pass", "first version", "v0",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func goalContinuationHasRemainingWork(text string, planState session.PlanState) bool {
	planState = session.NormalizePlanState(planState)
	for _, step := range planState.Steps {
		if step.Status == session.PlanStatusPending || step.Status == session.PlanStatusInProgress {
			return true
		}
	}
	lower := strings.ToLower(text)
	for _, needle := range []string{
		"broader goal remains", "still needs", "next phase", "next bounded", "follow-through",
		"remaining", "not complete", "not done", "needs phased", "later explicit lease",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func goalContinuationNextStep(objective string, planState session.PlanState, opState session.OperationState) string {
	for _, step := range planState.Steps {
		if step.Status == session.PlanStatusPending || step.Status == session.PlanStatusInProgress {
			return "Plan the next bounded phase: " + strings.TrimSpace(step.Step)
		}
	}
	objective = firstNonEmptyContinuation(objective, opState.Objective, opState.Summary, "the broader goal")
	return "Plan the next bounded phase for " + objective
}

func goalContinuationProposalID(candidate goalContinuationCandidate, msg core.InboundMessage) string {
	raw := strings.Join([]string{candidate.Objective, candidate.Summary, candidate.WhyNow, candidate.BoundedEffect, fmt.Sprintf("%d", msg.MessageID)}, "\n")
	sum := sha256.Sum256([]byte(raw))
	return goalContinuationIDPrefix + hex.EncodeToString(sum[:6])
}
