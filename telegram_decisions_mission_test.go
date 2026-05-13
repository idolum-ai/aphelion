//go:build linux

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
)

func TestMissionControlProposalAddCallbackCreatesCandidateOnly(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	metadata, err := core.MissionControlProposalMetadataJSON(core.MissionControlProposal{
		MissionID:         "mission-runtime-noise",
		Title:             "Runtime recovery and restart noise cleanup",
		Objective:         "Clean shutdown warning noise.",
		WhyProposed:       "Restart now works but shutdown emits database-closed warnings.",
		Owner:             "telegram:1001",
		Scope:             "principal",
		Tags:              []string{"runtime", "recovery"},
		NextAllowedAction: "Inspect shutdown ordering.",
		NotIncluded:       []string{"no execution", "no self-continuation"},
	})
	if err != nil {
		t.Fatalf("MissionControlProposalMetadataJSON() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      1001,
		SourceUserID:      1001,
		SourceRole:        string(principal.RoleAdmin),
		SourceScope:       session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"},
		TargetAdminChatID: 1001,
		TargetScope:       session.ScopeRef{Kind: session.ScopeKindTelegramDM, ID: "1001"},
		Summary:           "Mission Control proposal",
		MetadataJSON:      metadata,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-mission-add",
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionMissionAdd),
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 55, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	mission, ok, err := store.Mission("mission-runtime-noise")
	if err != nil || !ok {
		t.Fatalf("Mission() = %#v ok=%t err=%v", mission, ok, err)
	}
	if mission.Status != session.MissionStatusCandidate || mission.Pinned || mission.Authority.CanSelfContinue {
		t.Fatalf("mission = %#v, want review-only unpinned candidate", mission)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "No execution or self-continuation") {
		t.Fatalf("edits = %#v, want candidate-only confirmation", sender.edits)
	}
}

func TestMissionControlProposalAskEditCallbackDoesNotCreateMission(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	metadata, err := core.MissionControlProposalMetadataJSON(core.MissionControlProposal{
		MissionID: "mission-needs-edit",
		Title:     "Needs edit",
		Objective: "Draft a candidate mission.",
	})
	if err != nil {
		t.Fatalf("MissionControlProposalMetadataJSON() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      1001,
		SourceUserID:      1001,
		SourceRole:        string(principal.RoleAdmin),
		TargetAdminChatID: 1001,
		Summary:           "Mission Control proposal",
		MetadataJSON:      metadata,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-mission-edit",
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionMissionAskEdit),
		From:    &telegram.User{ID: 1001},
		Message: &telegram.Message{MessageID: 56, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery() err = %v", err)
	}
	missions, err := store.Missions(session.MissionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Missions() err = %v", err)
	}
	if len(missions) != 0 {
		t.Fatalf("missions = %#v, want none", missions)
	}
	if len(sender.edits) != 1 || !strings.Contains(sender.edits[0].text, "No mission was created") {
		t.Fatalf("edits = %#v, want ask-edit confirmation", sender.edits)
	}
}

func TestMissionControlProposalDetailCallbackRequiresTargetAdmin(t *testing.T) {
	t.Parallel()

	sender := &decisionTestSender{}
	store, err := session.NewSQLiteStore(t.TempDir() + "/sessions.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer store.Close()
	metadata, err := core.MissionControlProposalMetadataJSON(core.MissionControlProposal{
		MissionID: "mission-detail-denied",
		Title:     "Mission detail denied",
		Objective: "Keep Mission Control review details target-bound.",
	})
	if err != nil {
		t.Fatalf("MissionControlProposalMetadataJSON() err = %v", err)
	}
	eventID, err := store.InsertReviewEvent(session.ReviewEvent{
		SourceChatID:      1001,
		SourceUserID:      1001,
		SourceRole:        string(principal.RoleAdmin),
		TargetAdminChatID: 1001,
		Summary:           "Mission Control proposal",
		MetadataJSON:      metadata,
	})
	if err != nil {
		t.Fatalf("InsertReviewEvent() err = %v", err)
	}
	handler := newTelegramDecisionHandler(sender, &decisionTestRouter{}, decision.NewBroker(nil), store)
	err = handler.HandleCallbackQuery(context.Background(), telegram.CallbackQuery{
		ID:      "cb-mission-expand-denied",
		Data:    core.EncodeReviewEventCallbackData(eventID, core.ReviewEventActionExpand),
		From:    &telegram.User{ID: 2002},
		Message: &telegram.Message{MessageID: 57, Chat: &telegram.Chat{ID: 1001}},
	})
	if err != nil {
		t.Fatalf("HandleCallbackQuery(expand denied) err = %v", err)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits = %#v, want none", sender.edits)
	}
	if len(sender.answers) != 1 || !strings.Contains(sender.answers[0].text, "target admin") {
		t.Fatalf("answers = %#v, want target admin denial", sender.answers)
	}
}
