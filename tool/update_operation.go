//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/session"
)

func (r *Registry) updateOperation(_ context.Context, input json.RawMessage, key session.SessionKey) (string, error) {
	if r.store == nil {
		return "", fmt.Errorf("update_operation requires transcript store")
	}
	if key.ChatID == 0 && key.UserID == 0 && key.Scope.IsZero() {
		return "", fmt.Errorf("update_operation requires session context")
	}

	var in updateOperationInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("decode update_operation input: %w", err)
		}
	}

	current, err := r.store.OperationState(key)
	if err != nil {
		return "", err
	}

	if operationInputEmpty(in) {
		return renderOperationState("[OPERATION]", current), nil
	}

	state, err := applyOperationInput(current, in)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if state.Active() {
		if strings.TrimSpace(state.ID) == "" {
			if current.ID != "" {
				state.ID = current.ID
			} else {
				state.ID = generatedOperationID("op")
			}
		}
		state.UpdatedAt = now
		if state.Proposal.Active() {
			if strings.TrimSpace(state.Proposal.ID) == "" {
				if current.Proposal.ID != "" {
					state.Proposal.ID = current.Proposal.ID
				} else {
					state.Proposal.ID = generatedOperationID("proposal")
				}
			}
			state.Proposal.UpdatedAt = now
		}
	}

	if err := r.store.UpdateOperationState(key, state); err != nil {
		return "", err
	}
	return renderOperationState("[OPERATION_UPDATED]", state), nil
}

func operationInputEmpty(in updateOperationInput) bool {
	return strings.TrimSpace(in.ID) == "" &&
		strings.TrimSpace(in.Objective) == "" &&
		strings.TrimSpace(in.Status) == "" &&
		strings.TrimSpace(in.Stage) == "" &&
		strings.TrimSpace(in.Summary) == "" &&
		!in.Merge &&
		in.Proposal == nil &&
		in.Findings == nil &&
		in.Artifacts == nil
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
	}
	if strings.TrimSpace(in.Status) != "" && state.Status == "" {
		return session.OperationState{}, fmt.Errorf("update_operation status must be idle, active, blocked, completed, or failed")
	}

	proposal, err := parseOperationProposalInput(in.Proposal)
	if err != nil {
		return session.OperationState{}, err
	}
	state.Proposal = proposal

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

	return session.NormalizeOperationState(state), nil
}

func parseOperationProposalInput(in *updateOperationProposalInput) (session.OperationProposal, error) {
	if in == nil {
		return session.OperationProposal{}, nil
	}
	proposal := session.OperationProposal{
		ID:            strings.TrimSpace(in.ID),
		Kind:          strings.TrimSpace(in.Kind),
		Summary:       strings.TrimSpace(in.Summary),
		WhyNow:        strings.TrimSpace(in.WhyNow),
		BoundedEffect: strings.TrimSpace(in.BoundedEffect),
	}
	if strings.TrimSpace(in.Status) != "" {
		proposal.Status = session.NormalizeProposalStatus(session.ProposalStatus(in.Status))
		if proposal.Status == "" {
			return session.OperationProposal{}, fmt.Errorf("update_operation proposal status must be pending, approved, denied, expired, or superseded")
		}
	}
	return session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal, nil
}

func mergeOperationProposalInput(current session.OperationProposal, in updateOperationProposalInput) (session.OperationProposal, error) {
	proposal := current
	if id := strings.TrimSpace(in.ID); id != "" {
		proposal.ID = id
	}
	if kind := strings.TrimSpace(in.Kind); kind != "" {
		proposal.Kind = kind
	}
	if summary := strings.TrimSpace(in.Summary); summary != "" {
		proposal.Summary = summary
	}
	if whyNow := strings.TrimSpace(in.WhyNow); whyNow != "" {
		proposal.WhyNow = whyNow
	}
	if bounded := strings.TrimSpace(in.BoundedEffect); bounded != "" {
		proposal.BoundedEffect = bounded
	}
	if strings.TrimSpace(in.Status) != "" {
		status := session.NormalizeProposalStatus(session.ProposalStatus(in.Status))
		if status == "" {
			return session.OperationProposal{}, fmt.Errorf("update_operation proposal status must be pending, approved, denied, expired, or superseded")
		}
		proposal.Status = status
	}
	return session.NormalizeOperationState(session.OperationState{Proposal: proposal}).Proposal, nil
}

func parseOperationFindingInputs(inputs []updateOperationFindingInput) ([]session.OperationFinding, error) {
	findings := make([]session.OperationFinding, 0, len(inputs))
	for _, item := range inputs {
		claim := strings.TrimSpace(item.Claim)
		if claim == "" {
			return nil, fmt.Errorf("update_operation finding claim is required")
		}
		confidence := session.NormalizeFindingConfidence(session.FindingConfidence(item.Confidence))
		if strings.TrimSpace(item.Confidence) != "" && confidence == "" {
			return nil, fmt.Errorf("update_operation finding confidence must be low, medium, or high")
		}
		findings = append(findings, session.OperationFinding{
			Claim:      claim,
			Confidence: confidence,
			Basis:      strings.TrimSpace(item.Basis),
		})
	}
	return findings, nil
}

func parseOperationArtifactInputs(inputs []updateOperationArtifactInput) ([]session.OperationArtifact, error) {
	artifacts := make([]session.OperationArtifact, 0, len(inputs))
	for _, item := range inputs {
		ref := strings.TrimSpace(item.Ref)
		if ref == "" {
			return nil, fmt.Errorf("update_operation artifact ref is required")
		}
		artifacts = append(artifacts, session.OperationArtifact{
			Label: strings.TrimSpace(item.Label),
			Ref:   ref,
		})
	}
	return artifacts, nil
}

func appendDedupedFindings(existing []session.OperationFinding, added []session.OperationFinding) []session.OperationFinding {
	out := append([]session.OperationFinding(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, item := range out {
		seen[item.Claim+"\x00"+string(item.Confidence)+"\x00"+item.Basis] = struct{}{}
	}
	for _, item := range added {
		key := item.Claim + "\x00" + string(item.Confidence) + "\x00" + item.Basis
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func appendDedupedArtifacts(existing []session.OperationArtifact, added []session.OperationArtifact) []session.OperationArtifact {
	out := append([]session.OperationArtifact(nil), existing...)
	seen := make(map[string]struct{}, len(out))
	for _, item := range out {
		seen[item.Label+"\x00"+item.Ref] = struct{}{}
	}
	for _, item := range added {
		key := item.Label + "\x00" + item.Ref
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func generatedOperationID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "op"
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}

func renderOperationState(header string, state session.OperationState) string {
	state = session.NormalizeOperationState(state)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(header))
	b.WriteString("\n")
	fmt.Fprintf(&b, "active: %t\n", state.Active())
	if state.ID != "" {
		fmt.Fprintf(&b, "id: %s\n", state.ID)
	}
	if state.Objective != "" {
		fmt.Fprintf(&b, "objective: %s\n", state.Objective)
	}
	if state.Status != "" {
		fmt.Fprintf(&b, "status: %s\n", state.Status)
	}
	if state.Stage != "" {
		fmt.Fprintf(&b, "stage: %s\n", state.Stage)
	}
	if state.Summary != "" {
		fmt.Fprintf(&b, "summary: %s\n", state.Summary)
	}
	if state.Proposal.Active() {
		b.WriteString("proposal:\n")
		if state.Proposal.ID != "" {
			fmt.Fprintf(&b, "- id: %s\n", state.Proposal.ID)
		}
		if state.Proposal.Kind != "" {
			fmt.Fprintf(&b, "- kind: %s\n", state.Proposal.Kind)
		}
		if state.Proposal.Status != "" {
			fmt.Fprintf(&b, "- status: %s\n", state.Proposal.Status)
		}
		if state.Proposal.Summary != "" {
			fmt.Fprintf(&b, "- summary: %s\n", state.Proposal.Summary)
		}
		if state.Proposal.WhyNow != "" {
			fmt.Fprintf(&b, "- why_now: %s\n", state.Proposal.WhyNow)
		}
		if state.Proposal.BoundedEffect != "" {
			fmt.Fprintf(&b, "- bounded_effect: %s\n", state.Proposal.BoundedEffect)
		}
	} else {
		b.WriteString("proposal: none\n")
	}
	if len(state.Findings) == 0 {
		b.WriteString("findings: none\n")
	} else {
		b.WriteString("findings:\n")
		for _, finding := range state.Findings {
			fmt.Fprintf(&b, "- [%s] %s", finding.Confidence, finding.Claim)
			if finding.Basis != "" {
				fmt.Fprintf(&b, " (basis: %s)", finding.Basis)
			}
			b.WriteString("\n")
		}
	}
	if len(state.Artifacts) == 0 {
		b.WriteString("artifacts: none\n")
	} else {
		b.WriteString("artifacts:\n")
		for _, artifact := range state.Artifacts {
			if artifact.Label != "" {
				fmt.Fprintf(&b, "- %s: %s\n", artifact.Label, artifact.Ref)
				continue
			}
			fmt.Fprintf(&b, "- %s\n", artifact.Ref)
		}
	}
	return strings.TrimSpace(b.String())
}
