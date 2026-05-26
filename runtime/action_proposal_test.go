//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/session"
)

func TestMissionActionProposalBuildsReviewOnlyMissionApproval(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	mission, err := store.UpsertMission(session.MissionState{
		ID:                "mission-action-ui",
		Title:             "Generic Mission Proposal UI",
		Objective:         "Make approval a real control surface.",
		Scope:             "principal",
		Owner:             "telegram:1001",
		Status:            session.MissionStatusCandidate,
		NextAllowedAction: "Implement the generic Mission Proposal UI slice.",
	}, "telegram:1001", "candidate")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	proposal, err := rt.MissionActionProposal(context.Background(), 1001, 1001, mission.ID)
	if err != nil {
		t.Fatalf("MissionActionProposal() err = %v", err)
	}
	if proposal.ID != "aprop-"+mission.ID || proposal.MissionID != mission.ID {
		t.Fatalf("proposal IDs = %q/%q, want mission-linked proposal", proposal.ID, proposal.MissionID)
	}
	if proposal.Status != session.ProposalStatusPending {
		t.Fatalf("proposal status = %q, want pending", proposal.Status)
	}
	if !strings.Contains(proposal.BoundedEffect, "does not self-continue") {
		t.Fatalf("bounded effect = %q, want no self-continuation boundary", proposal.BoundedEffect)
	}
	if len(proposal.ForbiddenActions) == 0 {
		t.Fatalf("ForbiddenActions empty, want explicit authority boundaries")
	}
}

func TestApplyMissionActionProposalApproveActivatesWithoutSelfContinue(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	mission, err := store.UpsertMission(session.MissionState{
		ID:        "mission-activate-via-proposal",
		Title:     "Mission proposal activation",
		Objective: "Approve a review-only mission activation.",
		Scope:     "principal",
		Owner:     "telegram:1001",
		Status:    session.MissionStatusCandidate,
	}, "telegram:1001", "candidate")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	updated, changed, err := rt.ApplyMissionActionProposalDecision(context.Background(), 1001, 1001, mission.ID, "approve")
	if err != nil {
		t.Fatalf("ApplyMissionActionProposalDecision() err = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if updated.Status != session.MissionStatusActive {
		t.Fatalf("mission status = %q, want active", updated.Status)
	}
	if updated.Authority.CanSelfContinue {
		t.Fatalf("mission authority = %#v, want no self-continuation grant", updated.Authority)
	}
}

func TestApplyMissionActionProposalAskEditKeepsCandidateWaitingForEdit(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	mission, err := store.UpsertMission(session.MissionState{
		ID:        "mission-edit-via-proposal",
		Title:     "Mission proposal edit",
		Objective: "Ask for an edited proposal.",
		Scope:     "principal",
		Owner:     "telegram:1001",
		Status:    session.MissionStatusCandidate,
	}, "telegram:1001", "candidate")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	updated, changed, err := rt.ApplyMissionActionProposalDecision(context.Background(), 1001, 1001, mission.ID, "ask_edit")
	if err != nil {
		t.Fatalf("ApplyMissionActionProposalDecision(ask_edit) err = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if updated.Status != session.MissionStatusCandidate {
		t.Fatalf("mission status = %q, want candidate", updated.Status)
	}
	if updated.WaitingFor != "proposal_edit" {
		t.Fatalf("WaitingFor = %q, want proposal_edit", updated.WaitingFor)
	}
}
