//go:build linux

package telegramcommands

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

const actionProposalCallbackPrefix = "action_proposal:"
const staleActionProposalCallbackText = "This mission proposal is no longer active. Use the newest prompt."

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
	details := make([]string, 0, 8)
	evidence := make([]string, 0, 2)
	if id := strings.TrimSpace(proposal.ID); id != "" {
		evidence = append(evidence, "proposal: "+id)
	}
	if missionID := strings.TrimSpace(proposal.MissionID); missionID != "" {
		evidence = append(evidence, "mission: "+missionID)
	}
	if summary := strings.TrimSpace(proposal.Summary); summary != "" {
		details = append(details, "Proposal: "+truncateOperatorLine(summary, 220))
	}
	if effect := strings.TrimSpace(proposal.BoundedEffect); effect != "" {
		details = append(details, "Effect: "+truncateOperatorLine(effect, 220))
	}
	if risk := strings.TrimSpace(proposal.RiskClass); risk != "" {
		details = append(details, "Risk: "+risk)
	}
	if len(proposal.AllowedActions) > 0 {
		details = append(details, "Allowed: "+truncateOperatorLine(strings.Join(proposal.AllowedActions, ", "), 220))
	}
	if len(proposal.ForbiddenActions) > 0 {
		details = append(details, "Forbidden: "+truncateOperatorLine(strings.Join(proposal.ForbiddenActions, ", "), 220))
	}
	if len(proposal.ValidationPlan) > 0 {
		details = append(details, "Validation: "+truncateOperatorLine(strings.Join(proposal.ValidationPlan, "; "), 220))
	}
	why := strings.TrimSpace(proposal.WhyNow)
	if why == "" {
		why = "This mission action is review-only until you approve it."
	}
	return renderTelegramCompactPanelWithLimits(face.OperatorPanel{
		Title:    "Mission Proposal",
		State:    "waiting for approval",
		Why:      truncateOperatorLine(why, 220),
		Next:     "Reject it, ask for a change, or approve the bounded mission action.",
		Details:  details,
		Evidence: evidence,
	}, 8, 2)
}

func actionProposalButtonRows(proposalID string) [][]telegram.InlineButton {
	proposalID = strings.TrimSpace(proposalID)
	if proposalID == "" {
		return nil
	}
	return [][]telegram.InlineButton{{
		{Text: "Reject", CallbackData: encodeActionProposalCallbackData(proposalID, "deny")},
		{Text: "Change", CallbackData: encodeActionProposalCallbackData(proposalID, "ask_edit")},
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
		line := "Mission proposal approved."
		if title != "" {
			line += " Mission: " + title + "."
		}
		if status != "" {
			line += " Status: " + status + "."
		}
		line += " No self-continuation authority was granted."
		return line
	case "ask_edit":
		line := "Mission proposal needs changes."
		if title != "" {
			line += " Mission: " + title + "."
		}
		line += " I will revise the proposal before asking again."
		return line
	case "deny":
		line := "Mission proposal rejected."
		if title != "" {
			line += " Mission: " + title + "."
		}
		if !changed {
			line += " No mission state changed."
		}
		return line
	default:
		return fmt.Sprintf("Mission proposal %s recorded.", action)
	}
}
