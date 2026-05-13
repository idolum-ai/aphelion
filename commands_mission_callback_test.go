//go:build linux

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestMissionProposeCommandSendsActionProposalButtons(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionActionProposal: session.ActionProposal{
			ID:               "aprop-mission-action-ui",
			MissionID:        "mission-action-ui",
			Summary:          "Implement the generic approval UI.",
			WhyNow:           "Mission Control surfaced this candidate.",
			BoundedEffect:    "Mark active only; do not self-continue.",
			RiskClass:        "mission_control",
			AllowedActions:   []string{"mark_mission_active"},
			ForbiddenActions: []string{"self_continue_without_lease"},
			ValidationPlan:   []string{"record approval"},
			Status:           session.ProposalStatusPending,
		},
	}
	msg := core.InboundMessage{ChatID: 7, SenderID: 1001, MessageID: 42, Text: "/mission propose mission-action-ui"}

	handled, err := handleTelegramCommand(context.Background(), sender, router, msg)
	if err != nil {
		t.Fatalf("handleTelegramCommand() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.missionActionProposalID != "mission-action-ui" || router.missionCommandArgs != "" {
		t.Fatalf("mission proposal id = %q mission args = %q, want proposal path only", router.missionActionProposalID, router.missionCommandArgs)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want 1", len(sender.inline))
	}
	text := sender.inline[0].text
	for _, needle := range []string{"ActionProposal", "Implement the generic approval UI.", "Bounded effect:", "do not self-continue", "Forbidden:"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("inline text = %q, want substring %q", text, needle)
		}
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 3 {
		t.Fatalf("rows = %#v, want one Deny/Ask edit/Approve row", sender.inline[0].rows)
	}
	row := sender.inline[0].rows[0]
	if row[0].Text != "Deny" || row[1].Text != "Ask edit" || row[2].Text != "Approve" {
		t.Fatalf("button row = %#v, want Deny / Ask edit / Approve", row)
	}
}

func TestActionProposalApproveCallbackAppliesMissionDecision(t *testing.T) {
	t.Parallel()

	sender := &stubCommandSender{}
	router := &stubCommandRouter{
		missionActionProposal: session.ActionProposal{
			ID:            "aprop-mission-action-ui",
			MissionID:     "mission-action-ui",
			Summary:       "Implement the generic approval UI.",
			BoundedEffect: "Mark active only.",
			Status:        session.ProposalStatusPending,
		},
		applyMissionProposalMission: session.MissionState{ID: "mission-action-ui", Title: "Generic ActionProposal approval UI", Status: session.MissionStatusActive},
		applyMissionProposalChanged: true,
	}
	cb := telegram.CallbackQuery{
		ID:      "cb-approve-action-proposal",
		Data:    encodeActionProposalCallbackData("aprop-mission-action-ui", "approve"),
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 77, Chat: &telegram.Chat{ID: 7}},
	}

	handled, err := handleTelegramCommandCallback(context.Background(), sender, router, cb)
	if err != nil {
		t.Fatalf("handleTelegramCommandCallback() err = %v", err)
	}
	if !handled {
		t.Fatal("handled = false, want true")
	}
	if router.applyMissionProposalID != "mission-action-ui" || router.applyMissionProposalChoice != "approve" || router.applyMissionProposalSender != 1001 {
		t.Fatalf("applied id=%q choice=%q sender=%d, want mission-action-ui approve 1001", router.applyMissionProposalID, router.applyMissionProposalChoice, router.applyMissionProposalSender)
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear len = %d, want approval message edit", len(sender.editClear))
	}
	if !strings.Contains(sender.editClear[0].text, "ActionProposal approved") || !strings.Contains(sender.editClear[0].text, "No self-continuation") {
		t.Fatalf("edit text = %q, want approval and authority boundary", sender.editClear[0].text)
	}
}
