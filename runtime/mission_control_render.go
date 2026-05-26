//go:build linux

package runtime

import (
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
)

func FormatMissionControlProposalMessage(proposal core.MissionControlProposal) string {
	proposal = core.NormalizeMissionControlProposal(proposal)
	details := make([]string, 0, 6)
	if title := strings.TrimSpace(proposal.Title); title != "" {
		details = append(details, "Title: "+title)
	}
	if objective := strings.TrimSpace(proposal.Objective); objective != "" {
		details = append(details, "Objective: "+objective)
	}
	if scope := strings.TrimSpace(proposal.Scope); scope != "" {
		details = append(details, "Scope: "+scope)
	}
	if next := strings.TrimSpace(proposal.NextAllowedAction); next != "" {
		details = append(details, "Next allowed action: "+next)
	}
	if len(proposal.NotIncluded) > 0 {
		details = append(details, "Not included: "+strings.Join(proposal.NotIncluded, "; "))
	}
	why := strings.TrimSpace(proposal.WhyProposed)
	if why == "" {
		why = "Adding this only creates a candidate mission; it does not start execution or grant self-continuation."
	}
	evidence := []string{"authority: no self-continuation"}
	if missionID := strings.TrimSpace(proposal.MissionID); missionID != "" {
		evidence = append([]string{"mission: " + missionID}, evidence...)
	}
	return face.RenderCompactOperatorPanel(face.OperatorPanel{
		Title:    "Mission Proposal",
		State:    "review-only candidate",
		Why:      why,
		Next:     "Add the mission, ask for a change, park the proposal, or reject it.",
		Details:  details,
		Evidence: evidence,
	}, face.OperatorPanelCompactOptions{DetailLimit: 6, EvidenceLimit: 2})
}
