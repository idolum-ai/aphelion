//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

func TestMissionLedgerToolCreatesCandidateWithoutSelfContinuation(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, missionLedgerToolName, json.RawMessage(`{
		"action":"create_candidate",
		"mission_id":"mission-ledger-tool",
		"title":"Mission Ledger tool",
		"objective":"Expose Mission Ledger read/write surfaces safely.",
		"tags":["mission","ledger"],
		"evidence":[{"claim":"tool creates candidates","required":true,"status":"pending"}]
	}`))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(mission_ledger create_candidate) err = %v", err)
	}
	if !strings.Contains(out, "[MISSION]") || !strings.Contains(out, "authority: review,self_summon,requires_user_review") {
		t.Fatalf("out = %q, want review-only mission", out)
	}
	mission, ok, err := store.Mission("mission-ledger-tool")
	if err != nil || !ok {
		t.Fatalf("Mission() = %#v ok=%t err=%v", mission, ok, err)
	}
	if mission.Pinned || mission.Authority.CanSelfContinue || mission.Status != session.MissionStatusCandidate {
		t.Fatalf("mission = %#v, want unpinned review-only candidate", mission)
	}
}

func TestMissionLedgerToolRejectsPinnedCandidateCreation(t *testing.T) {
	t.Parallel()

	registry, _ := newDurableAgentToolRegistry(t)
	_, err := registry.ExecuteForSessionPrincipal(context.Background(), principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}, adminSessionKey(), missionLedgerToolName, json.RawMessage(`{
		"action":"create_candidate",
		"objective":"Should not be pinned silently.",
		"pinned":true
	}`))
	if err == nil {
		t.Fatal("ExecuteForSessionPrincipal(create_candidate pinned) err = nil, want denial")
	}
	if !strings.Contains(err.Error(), "cannot pin") {
		t.Fatalf("err = %v, want cannot pin", err)
	}
}

func TestMissionLedgerToolWorkingObjectiveDoesNotPromoteMission(t *testing.T) {
	t.Parallel()

	registry, store := newDurableAgentToolRegistry(t)
	key := adminSessionKey()
	actor := principal.Principal{Role: principal.RoleAdmin, TelegramUserID: 1001}

	out, err := registry.ExecuteForSessionPrincipal(context.Background(), actor, key, missionLedgerToolName, json.RawMessage(`{
		"action":"working_objective_set",
		"working_objective":"Finish the current turn cleanly.",
		"working_source":"inferred",
		"working_confidence":"high"
	}`))
	if err != nil {
		t.Fatalf("ExecuteForSessionPrincipal(working_objective_set) err = %v", err)
	}
	if !strings.Contains(out, "[WORKING_OBJECTIVE_UPDATED]") || !strings.Contains(out, "Finish the current turn cleanly") {
		t.Fatalf("out = %q, want working objective", out)
	}
	missions, err := store.Missions(session.MissionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Missions() err = %v", err)
	}
	if len(missions) != 0 {
		t.Fatalf("missions len = %d, want no durable promotion", len(missions))
	}
}
