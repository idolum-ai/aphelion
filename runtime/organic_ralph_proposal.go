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

const organicRalphProposalSchemaVersion = "1"

type organicRalphProposalCandidate struct {
	ID            string
	Kind          string
	Summary       string
	WhyNow        string
	BoundedEffect string
	Confidence    string
}

func (r *Runtime) maybeInferOrganicOperationProposal(ctx context.Context, key session.SessionKey, msg core.InboundMessage, promptInput string, result *turn.Result) (bool, error) {
	_ = ctx
	if r == nil || r.store == nil || msg.ChatID == 0 || msg.Origin == core.InboundOriginTurnAuthorization {
		return false, nil
	}
	if strings.HasPrefix(strings.TrimSpace(msg.Text), "/") {
		return false, nil
	}
	priorState, priorExists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return false, fmt.Errorf("read prior continuation state: %w", err)
	}
	if priorExists && session.NormalizeContinuationState(priorState).Active() {
		return false, nil
	}
	opState, err := r.store.OperationState(key)
	if err != nil {
		return false, nil
	}
	opState = session.NormalizeOperationState(opState)
	if pendingOperationProposalNeedsButton(opState.Proposal) || opState.Proposal.Status == session.ProposalStatusApproved {
		return false, nil
	}
	candidate, basis, ok := organicRalphProposalCandidateFromResult(result)
	if !ok {
		var inferErr error
		candidate, basis, ok, inferErr = r.inferOrganicRalphProposalCandidateFromState(key, msg, promptInput, result, opState)
		if inferErr != nil {
			return false, inferErr
		}
	}
	if !ok || !candidate.ready() {
		return false, nil
	}
	if candidate.requiresSeparateCapability() {
		return false, nil
	}
	now := time.Now().UTC()
	proposalID := candidate.ID
	if proposalID == "" {
		proposalID = organicRalphProposalID(candidate, msg)
	}
	proposal := session.OperationProposal{
		ID:            proposalID,
		Kind:          firstNonEmptyContinuation(candidate.Kind, "organic_lease"),
		Summary:       candidate.Summary,
		WhyNow:        candidate.WhyNow,
		BoundedEffect: candidate.BoundedEffect,
		Status:        session.ProposalStatusPending,
		UpdatedAt:     now,
	}
	state := session.OperationState{
		ID:        "organic-ralph-" + strings.TrimPrefix(proposalID, "organic-ralph-"),
		Objective: candidate.Summary,
		Status:    session.OperationStatusBlocked,
		Stage:     "organic_proposal",
		Summary:   "Organic Ralph inferred one bounded next-step proposal from ordinary conversation.",
		Proposal:  proposal,
		Findings: []session.OperationFinding{{
			Claim:      "Organic Ralph inferred exactly one high-confidence bounded next lease from ordinary conversation.",
			Confidence: session.FindingConfidenceHigh,
			Basis:      basis,
		}},
		Artifacts: []session.OperationArtifact{{
			Label: "source_message",
			Ref:   fmt.Sprintf("telegram:%d:%d", msg.ChatID, msg.MessageID),
		}},
		UpdatedAt: now,
	}
	if err := r.store.UpdateOperationState(key, state); err != nil {
		return false, fmt.Errorf("persist organic ralph operation proposal: %w", err)
	}
	return true, nil
}

func organicRalphProposalCandidateFromResult(result *turn.Result) (organicRalphProposalCandidate, string, bool) {
	candidate, ok := parseOrganicRalphProposalContract(resultProposalNote(result))
	if !ok {
		return organicRalphProposalCandidate{}, "", false
	}
	return candidate, "Face proposal contract carried ORGANIC_RALPH_PROPOSAL=yes, confidence=high, summary, why_now, and bounded_effect.", true
}

func (r *Runtime) inferOrganicRalphProposalCandidateFromState(
	key session.SessionKey,
	msg core.InboundMessage,
	promptInput string,
	result *turn.Result,
	opState session.OperationState,
) (organicRalphProposalCandidate, string, bool, error) {
	if r == nil || r.store == nil {
		return organicRalphProposalCandidate{}, "", false, nil
	}
	opState = session.NormalizeOperationState(opState)
	if terminalOperationProposalBlocksStateInference(opState.Proposal) {
		return organicRalphProposalCandidate{}, "", false, nil
	}
	planState, _ := r.store.PlanState(key)
	planState = session.NormalizePlanState(planState)
	if result != nil && organicRalphPlanStateHasConcreteStep(result.PlanState) {
		planState = session.NormalizePlanState(result.PlanState)
	}
	if result != nil && result.OperationState.Active() && !pendingOperationProposalNeedsButton(result.OperationState.Proposal) {
		resultOp := session.NormalizeOperationState(result.OperationState)
		if !terminalOperationProposalBlocksStateInference(resultOp.Proposal) {
			opState = resultOp
		}
	}
	priorContinuation, priorContinuationExists, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return organicRalphProposalCandidate{}, "", false, err
	}
	priorContinuation = session.NormalizeContinuationState(priorContinuation)

	nextStep, source := organicRalphStateNextStep(planState, opState, priorContinuation, priorContinuationExists)
	if nextStep == "" {
		return organicRalphProposalCandidate{}, "", false, nil
	}
	objective := firstNonEmptyContinuation(
		opState.Objective,
		opState.Summary,
		planState.Explanation,
		priorContinuation.Objective,
		summarizeContinuationFallback(promptInput),
	)
	whyNow := firstNonEmptyContinuation(
		opState.Summary,
		planState.Explanation,
		priorContinuation.StageSummary,
		"Persisted operation, plan, or continuation state names one bounded next step that needs explicit approval.",
	)
	summary := clampContinuationText(nextStep, 120)
	boundedEffect := firstNonEmptyContinuation(
		organicRalphProposalBoundedEffectForStateInference(opState.Proposal),
		organicRalphBoundedEffectFromState(nextStep),
	)
	kind := organicRalphKindFromStateText(strings.Join([]string{summary, objective, boundedEffect}, "\n"))
	candidate := organicRalphProposalCandidate{
		Kind:          kind,
		Summary:       summary,
		WhyNow:        clampContinuationText(whyNow, 220),
		BoundedEffect: boundedEffect,
		Confidence:    "high",
	}
	basis := "Persisted " + source + " carried a concrete next step; no explicit face contract was required."
	return candidate, basis, true, nil
}

func terminalOperationProposalBlocksStateInference(proposal session.OperationProposal) bool {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	if !proposal.Active() {
		return false
	}
	switch proposal.Status {
	case session.ProposalStatusApproved, session.ProposalStatusDenied, session.ProposalStatusSuperseded:
		return true
	default:
		return false
	}
}

func organicRalphProposalFieldsForStateInference(proposal session.OperationProposal) (summary string, boundedEffect string) {
	proposal = session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal
	if !proposal.Active() {
		return "", ""
	}
	switch proposal.Status {
	case session.ProposalStatusExpired, session.ProposalStatusDenied, session.ProposalStatusSuperseded, session.ProposalStatusApproved:
		return "", ""
	default:
		return strings.TrimSpace(proposal.Summary), strings.TrimSpace(proposal.BoundedEffect)
	}
}

func organicRalphProposalBoundedEffectForStateInference(proposal session.OperationProposal) string {
	_, boundedEffect := organicRalphProposalFieldsForStateInference(proposal)
	return boundedEffect
}

func organicRalphStateNextStep(planState session.PlanState, opState session.OperationState, priorContinuation session.ContinuationState, priorContinuationExists bool) (string, string) {
	planState = session.NormalizePlanState(planState)
	opState = session.NormalizeOperationState(opState)
	priorContinuation = session.NormalizeContinuationState(priorContinuation)
	for _, step := range planState.Steps {
		if (step.Status == session.PlanStatusInProgress || step.Status == session.PlanStatusPending) && organicRalphConcreteStateStep(step.Step) {
			return step.Step, "plan state"
		}
	}
	if opState.Status == session.OperationStatusBlocked || opState.Status == session.OperationStatusActive {
		proposalSummary, proposalBoundedEffect := organicRalphProposalFieldsForStateInference(opState.Proposal)
		for _, text := range []string{
			proposalSummary,
			proposalBoundedEffect,
			opState.Summary,
			opState.Objective,
			opState.Stage,
		} {
			if organicRalphConcreteStateStep(text) {
				return strings.TrimSpace(text), "operation state"
			}
		}
	}
	if priorContinuationExists && !priorContinuation.Active() {
		for _, text := range []string{
			priorContinuation.StageSummary,
			priorContinuation.Objective,
			priorContinuation.ActionProposal.Summary,
			priorContinuation.ActionProposal.BoundedEffect,
		} {
			if organicRalphConcreteStateStep(text) {
				return strings.TrimSpace(text), "continuation state"
			}
		}
	}
	return "", ""
}

func organicRalphConcreteStateStep(step string) bool {
	trimmed := strings.TrimSpace(step)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	normalized := strings.Trim(strings.ReplaceAll(strings.ReplaceAll(lower, "-", "_"), " ", "_"), "._ ")
	for _, generic := range []string{
		"continue with the next bounded step",
		"resume the next bounded step",
		"resume the next bounded step from this thread",
		"continue the current thread",
		"take the next bounded step",
	} {
		if strings.Trim(lower, ". ") == generic {
			return false
		}
	}
	for _, internal := range []string{
		"awaiting_ordinary_prompt",
		"button_press_verification",
		"delivery",
		"local_patch_validated",
		"awaiting_button_approval",
		"awaiting_live_test_button",
		"no_button_reported",
		"recovery_assessment",
		"implementation",
		"diagnosis",
		"intake",
	} {
		if normalized == internal {
			return false
		}
	}
	return true
}

func organicRalphPlanStateHasConcreteStep(state session.PlanState) bool {
	state = session.NormalizePlanState(state)
	for _, step := range state.Steps {
		if (step.Status == session.PlanStatusInProgress || step.Status == session.PlanStatusPending) && organicRalphConcreteStateStep(step.Step) {
			return true
		}
	}
	return false
}

func organicRalphBoundedEffectFromState(nextStep string) string {
	nextStep = strings.TrimSpace(nextStep)
	if nextStep == "" {
		nextStep = "the current bounded next step"
	}
	return "Work only on: " + nextStep + "; use existing authority only; report evidence and stop."
}

func organicRalphKindFromStateText(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(lower, "status") || strings.Contains(lower, "doctor") || strings.Contains(lower, "health"):
		return "status_check"
	case strings.Contains(lower, "patch") || strings.Contains(lower, "implement") || strings.Contains(lower, "edit") ||
		strings.Contains(lower, "commit") || strings.Contains(lower, "deploy") || strings.Contains(lower, "restart") ||
		strings.Contains(lower, "write") || strings.Contains(lower, "change"):
		return "system_change"
	default:
		return "read_only_review"
	}
}

func resultProposalNote(result *turn.Result) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(result.ProposalNote)
}

func parseOrganicRalphProposalContract(raw string) (organicRalphProposalCandidate, bool) {
	candidate := organicRalphProposalCandidate{}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return candidate, false
	}
	schemaOK := false
	proposalOK := false
	for _, line := range strings.Split(trimmed, "\n") {
		key, value, ok := splitContinuationDirective(strings.TrimSpace(line))
		if !ok {
			continue
		}
		switch key {
		case "ORGANIC_RALPH_SCHEMA_VERSION", "ORGANIC_RALPH_SCHEMA":
			schemaOK = strings.TrimSpace(value) == organicRalphProposalSchemaVersion
		case "ORGANIC_RALPH_PROPOSAL":
			proposalOK = parseBoolish(value)
		case "ORGANIC_RALPH_ID":
			candidate.ID = sanitizeOrganicRalphID(value)
		case "ORGANIC_RALPH_KIND":
			candidate.Kind = strings.TrimSpace(value)
		case "ORGANIC_RALPH_SUMMARY":
			candidate.Summary = strings.TrimSpace(value)
		case "ORGANIC_RALPH_WHY_NOW":
			candidate.WhyNow = strings.TrimSpace(value)
		case "ORGANIC_RALPH_BOUNDED_EFFECT":
			candidate.BoundedEffect = strings.TrimSpace(value)
		case "ORGANIC_RALPH_CONFIDENCE":
			candidate.Confidence = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if !schemaOK || !proposalOK {
		return organicRalphProposalCandidate{}, false
	}
	return candidate, true
}

func (c organicRalphProposalCandidate) ready() bool {
	if strings.ToLower(strings.TrimSpace(c.Confidence)) != "high" {
		return false
	}
	if strings.TrimSpace(c.Summary) == "" || strings.TrimSpace(c.WhyNow) == "" || strings.TrimSpace(c.BoundedEffect) == "" {
		return false
	}
	return organicRalphHasStopCondition(c.BoundedEffect)
}

func organicRalphHasStopCondition(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, needle := range []string{"stop", "report", "no ", "only", "bounded", "without", "do not"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func (c organicRalphProposalCandidate) requiresSeparateCapability() bool {
	kind := strings.ToLower(strings.TrimSpace(c.Kind))
	if kind == "" || kind == "read_only_review" || kind == "status_check" || kind == "system_change" || kind == "organic_lease" {
		// system_change is allowed as a proposal only; execution still needs the button-backed lease.
	} else {
		return true
	}
	combined := strings.ToLower(strings.Join([]string{c.Summary, c.WhyNow, c.BoundedEffect}, "\n"))
	for _, risky := range []string{"api key", "credential", "secret", "purchase", "external account", "send email", "public contact"} {
		if strings.Contains(combined, risky) && !strings.Contains(combined, "no "+risky) && !strings.Contains(combined, "without "+risky) {
			return true
		}
	}
	return false
}

func organicRalphProposalID(candidate organicRalphProposalCandidate, msg core.InboundMessage) string {
	raw := strings.Join([]string{candidate.Summary, candidate.WhyNow, candidate.BoundedEffect, fmt.Sprintf("%d", msg.MessageID)}, "\n")
	sum := sha256.Sum256([]byte(raw))
	return "organic-ralph-" + hex.EncodeToString(sum[:6])
}

func sanitizeOrganicRalphID(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range trimmed {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 72 {
		out = strings.Trim(out[:72], "-")
	}
	return out
}

func parseBoolish(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "y", "true", "1", "on":
		return true
	default:
		return false
	}
}
