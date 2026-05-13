//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func FormatMissionControlProposalMessage(proposal core.MissionControlProposal) string {
	proposal = core.NormalizeMissionControlProposal(proposal)
	lines := []string{"Mission Control Proposal"}
	if title := strings.TrimSpace(proposal.Title); title != "" {
		lines = append(lines, "", "Title:", title)
	}
	if objective := strings.TrimSpace(proposal.Objective); objective != "" {
		lines = append(lines, "", "Objective:", objective)
	}
	if why := strings.TrimSpace(proposal.WhyProposed); why != "" {
		lines = append(lines, "", "Why I’m proposing it:", why)
	}
	if scope := strings.TrimSpace(proposal.Scope); scope != "" {
		lines = append(lines, "", "Suggested state:", "candidate mission, review-only", "scope: "+scope)
	}
	if next := strings.TrimSpace(proposal.NextAllowedAction); next != "" {
		lines = append(lines, "", "Next allowed action:", next)
	}
	if len(proposal.NotIncluded) > 0 {
		lines = append(lines, "", "Not included:")
		for _, item := range proposal.NotIncluded {
			lines = append(lines, "- "+item)
		}
	}
	lines = append(lines, "", "Adding this only creates a candidate. It does not start execution or grant self-continuation.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
