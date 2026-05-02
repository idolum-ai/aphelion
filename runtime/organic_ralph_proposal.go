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
	priorState, priorExists, _ := r.store.ContinuationStateIfExists(key)
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
	candidate, ok := parseOrganicRalphProposalContract(resultProposalNote(result))
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
			Basis:      "Face proposal contract carried ORGANIC_RALPH_PROPOSAL=yes, confidence=high, summary, why_now, and bounded_effect.",
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
