//go:build linux

package session

import (
	"strings"
	"testing"
	"time"
)

func TestMissionLedgerUpsertListEventsAndHealth(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()

	mission, err := store.UpsertMission(MissionState{
		ID:                "mission-ledger-v1",
		Title:             "Mission Ledger v1",
		Objective:         "Implement ledger-not-hunger mission tracking.",
		Origin:            "user_explicit",
		Scope:             "principal",
		Owner:             "telegram:1001",
		Status:            MissionStatusCandidate,
		Pinned:            true,
		Tags:              []string{"mission", "ledger", "mission"},
		Authority:         MissionAuthorityContract{CanSelfSummon: true, RequiresUserReview: true},
		EvidenceChecklist: []MissionEvidenceItem{{Claim: "storage exists", Required: true, Status: "pending"}},
	}, "telegram:1001", "created from /mission create")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}
	if mission.ID != "mission-ledger-v1" || !mission.Pinned || mission.Status != MissionStatusCandidate {
		t.Fatalf("mission = %#v, want pinned candidate", mission)
	}
	if len(mission.Tags) != 2 {
		t.Fatalf("mission tags = %#v, want normalized/deduped", mission.Tags)
	}
	if mission.Authority.CanSelfContinue {
		t.Fatalf("mission authority = %#v, want no self-continuation by default", mission.Authority)
	}

	mission, err = store.UpdateMissionStatus(mission.ID, MissionStatusActive, "telegram:1001", "approved active work")
	if err != nil {
		t.Fatalf("UpdateMissionStatus() err = %v", err)
	}
	if mission.Status != MissionStatusActive || !mission.Pinned {
		t.Fatalf("updated mission = %#v, want active pinned", mission)
	}

	listed, err := store.Missions(MissionFilter{Scope: "principal", Owner: "telegram:1001", Limit: 10})
	if err != nil {
		t.Fatalf("Missions() err = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != mission.ID {
		t.Fatalf("listed missions = %#v, want stored mission", listed)
	}

	health, err := store.MissionLedgerHealth(time.Now().UTC())
	if err != nil {
		t.Fatalf("MissionLedgerHealth() err = %v", err)
	}
	if health.ActiveCount != 1 || health.PinnedCount != 1 {
		t.Fatalf("health = %#v, want active=1 pinned=1", health)
	}
	if health.SelfContinuationEnabledCount != 0 {
		t.Fatalf("health = %#v, want no self-continuation", health)
	}

	events, err := store.MissionEvents(mission.ID, 10)
	if err != nil {
		t.Fatalf("MissionEvents() err = %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("events len = %d, want create and activation events", len(events))
	}
	joined := ""
	for _, event := range events {
		joined += event.EventType + "\n"
	}
	if !strings.Contains(joined, "mission.created") || !strings.Contains(joined, "mission.activated") {
		t.Fatalf("events = %s, want created and activated", joined)
	}
}

func TestWorkingObjectivePersistsWithoutCreatingMission(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	key := SessionKey{ChatID: 4242, Scope: ScopeRef{Kind: ScopeKindTelegramDM, ID: "4242"}}

	if err := store.UpdateWorkingObjective(key, WorkingObjective{Objective: "Answer the current question", Source: "inferred", Confidence: "medium"}); err != nil {
		t.Fatalf("UpdateWorkingObjective() err = %v", err)
	}
	objective, err := store.WorkingObjective(key)
	if err != nil {
		t.Fatalf("WorkingObjective() err = %v", err)
	}
	if objective.Objective != "Answer the current question" || objective.Source != "inferred" {
		t.Fatalf("working objective = %#v, want persisted inferred objective", objective)
	}
	missions, err := store.Missions(MissionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Missions() err = %v", err)
	}
	if len(missions) != 0 {
		t.Fatalf("missions len = %d, want working objective not promoted", len(missions))
	}
}

func TestMissionSummonIsReviewOnlyAndLogsSummonedEvent(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	mission, err := store.UpsertMission(MissionState{
		ID:        "lighthouse-rename",
		Title:     "Lighthouse rename",
		Objective: "Rename console surfaces to Lighthouse.",
		Scope:     "principal",
		Owner:     "telegram:1001",
		Status:    MissionStatusCandidate,
		Pinned:    true,
		Tags:      []string{"lighthouse", "console"},
	}, "telegram:1001", "candidate")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	summoned, err := store.SummonMissions(MissionFilter{Owner: "telegram:1001"}, "Should we revisit Lighthouse naming?", 5)
	if err != nil {
		t.Fatalf("SummonMissions() err = %v", err)
	}
	if len(summoned) != 1 || summoned[0].ID != mission.ID {
		t.Fatalf("summoned = %#v, want lighthouse mission", summoned)
	}
	if summoned[0].Authority.CanSelfContinue {
		t.Fatalf("summoned authority = %#v, want review-only", summoned[0].Authority)
	}
	if summoned[0].LastSummonedAt.IsZero() {
		t.Fatalf("summoned mission LastSummonedAt is zero")
	}

	events, err := store.MissionEvents(mission.ID, 5)
	if err != nil {
		t.Fatalf("MissionEvents() err = %v", err)
	}
	for _, event := range events {
		if event.EventType == "mission.summoned" {
			return
		}
	}
	t.Fatalf("events = %#v, want mission.summoned", events)
}

func TestMissionHandoffAndResultUpdateHealth(t *testing.T) {
	t.Parallel()

	store := newTestSQLiteStore(t)
	defer store.Close()
	mission, err := store.UpsertMission(MissionState{ID: "restart-proof", Objective: "Record restart evidence", Scope: "system", Owner: "aphelion"}, "test", "create")
	if err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}
	handoff, err := store.CreateMissionHandoff(MissionHandoff{ID: "handoff-1", MissionID: mission.ID, PlannedAction: "restart service", RecoveryQuestion: "did restart finish?"})
	if err != nil {
		t.Fatalf("CreateMissionHandoff() err = %v", err)
	}
	handoffs, err := store.MissionHandoffs(MissionHandoffFilter{Status: "pending", Limit: 5})
	if err != nil {
		t.Fatalf("MissionHandoffs() err = %v", err)
	}
	if len(handoffs) != 1 || handoffs[0].ID != handoff.ID || handoffs[0].PlannedAction != "restart service" {
		t.Fatalf("MissionHandoffs() = %#v, want pending restart handoff", handoffs)
	}
	health, err := store.MissionLedgerHealth(time.Now().UTC())
	if err != nil {
		t.Fatalf("MissionLedgerHealth() err = %v", err)
	}
	if health.PendingHandoffCount != 1 {
		t.Fatalf("health = %#v, want pending handoff", health)
	}
	if _, err := store.RecordMissionResult(MissionResult{HandoffID: handoff.ID, MissionID: mission.ID, Status: "completed", Summary: "restart verified", EvidenceRefsJSON: `["tes:restart"]`}); err != nil {
		t.Fatalf("RecordMissionResult() err = %v", err)
	}
	results, err := store.MissionResults(5)
	if err != nil {
		t.Fatalf("MissionResults() err = %v", err)
	}
	if len(results) != 1 || results[0].HandoffID != handoff.ID || results[0].EvidenceRefsJSON != `["tes:restart"]` {
		t.Fatalf("MissionResults() = %#v, want recorded restart result", results)
	}
	health, err = store.MissionLedgerHealth(time.Now().UTC())
	if err != nil {
		t.Fatalf("MissionLedgerHealth(after result) err = %v", err)
	}
	if health.PendingHandoffCount != 0 {
		t.Fatalf("health = %#v, want handoff closed", health)
	}
}
