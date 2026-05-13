//go:build linux

package tool

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

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
