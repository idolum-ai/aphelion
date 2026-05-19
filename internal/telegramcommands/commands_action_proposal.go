//go:build linux

package telegramcommands

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const actionProposalCallbackPrefix = "action_proposal:"
const staleActionProposalCallbackText = "This action proposal is no longer active. Use the newest prompt."

func missionProposalCommandMissionID(args string) (string, bool) {
	action, rest := nextCommandToken(args)
	switch action {
	case "propose", "proposal", "approve":
		missionID, _ := nextCommandToken(rest)
		return strings.TrimSpace(missionID), strings.TrimSpace(missionID) != ""
	default:
		return "", false
	}
}

func nextCommandToken(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if idx := strings.IndexAny(raw, " \n\t"); idx >= 0 {
		return strings.ToLower(strings.TrimSpace(raw[:idx])), strings.TrimSpace(raw[idx+1:])
	}
	return strings.ToLower(raw), ""
}

func renderActionProposalPrompt(proposal session.ActionProposal) string {
	proposal = session.NormalizeActionProposal(proposal)
	lines := []string{"ActionProposal"}
	if id := strings.TrimSpace(proposal.ID); id != "" {
		lines = append(lines, "id: "+id)
	}
	if missionID := strings.TrimSpace(proposal.MissionID); missionID != "" {
		lines = append(lines, "mission: "+missionID)
	}
	if summary := strings.TrimSpace(proposal.Summary); summary != "" {
		lines = append(lines, "", "Proposal:", summary)
	}
	if whyNow := strings.TrimSpace(proposal.WhyNow); whyNow != "" {
		lines = append(lines, "", "Why now:", whyNow)
	}
	if effect := strings.TrimSpace(proposal.BoundedEffect); effect != "" {
		lines = append(lines, "", "Bounded effect:", effect)
	}
	if risk := strings.TrimSpace(proposal.RiskClass); risk != "" {
		lines = append(lines, "", "Risk:", risk)
	}
	if len(proposal.AllowedActions) > 0 {
		lines = append(lines, "", "Allowed:", strings.Join(proposal.AllowedActions, ", "))
	}
	if len(proposal.ForbiddenActions) > 0 {
		lines = append(lines, "", "Forbidden:", strings.Join(proposal.ForbiddenActions, ", "))
	}
	if len(proposal.ValidationPlan) > 0 {
		lines = append(lines, "", "Validation:", strings.Join(proposal.ValidationPlan, "; "))
	}
	lines = append(lines, "", "Choose: Deny, Ask edit, or Approve.")
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func actionProposalButtonRows(proposalID string) [][]telegram.InlineButton {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: "Deny", CallbackData: encodeActionProposalCallbackData(proposalID, "deny")},
		{Text: "Ask edit", CallbackData: encodeActionProposalCallbackData(proposalID, "ask_edit")},
		{Text: "Approve", CallbackData: encodeActionProposalCallbackData(proposalID, "approve")},
	}}
}

func encodeActionProposalCallbackData(proposalID string, action string) string {
	return actionProposalCallbackPrefix + strings.TrimSpace(proposalID) + ":" + strings.TrimSpace(action)
}

func decodeActionProposalCallbackData(data string) (proposalID string, action string, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, actionProposalCallbackPrefix) {
		return "", "", false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, actionProposalCallbackPrefix))
	parts := strings.SplitN(payload, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	proposalID = strings.TrimSpace(parts[0])
	action = strings.TrimSpace(parts[1])
	if proposalID == "" || action == "" {
		return "", "", false
	}
	switch action {
	case "approve", "deny", "ask_edit":
		return proposalID, action, true
	default:
		return "", "", false
	}
}

func missionIDFromActionProposalID(proposalID string) string {
	proposalID = strings.TrimSpace(proposalID)
	if strings.HasPrefix(proposalID, "aprop-") {
		return strings.TrimSpace(strings.TrimPrefix(proposalID, "aprop-"))
	}
	return ""
}

func renderActionProposalDecision(proposal session.ActionProposal, mission session.MissionState, action string, changed bool) string {
	proposal = session.NormalizeActionProposal(proposal)
	action = strings.TrimSpace(action)
	title := strings.TrimSpace(mission.Title)
	if title == "" {
		title = strings.TrimSpace(mission.ID)
	}
	status := strings.TrimSpace(string(mission.Status))
	switch action {
	case "approve":
		line := "ActionProposal approved."
		if title != "" {
			line += " Mission: " + title + "."
		}
		if status != "" {
			line += " Status: " + status + "."
		}
		line += " No self-continuation authority was granted."
		return line
	case "ask_edit":
		line := "ActionProposal needs edits."
		if title != "" {
			line += " Mission: " + title + "."
		}
		line += " I will revise the proposal before asking again."
		return line
	case "deny":
		line := "ActionProposal denied."
		if title != "" {
			line += " Mission: " + title + "."
		}
		if !changed {
			line += " No mission state changed."
		}
		return line
	default:
		return fmt.Sprintf("ActionProposal %s recorded.", action)
	}
}
