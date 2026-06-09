//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestReentryRecommendationSweepSurfacesBoundedChoicesAfterTerminalQuietWindow(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = `{"candidates":[{"id":"c1","label":"Release check","rank":1},{"id":"c2","label":"Next lease","rank":2}]}`
	rt := &Runtime{cfg: cfg, store: store, provider: provider, outbound: sender}
	key := session.SessionKey{ChatID: 7001, UserID: 0}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-release",
		Objective: "Rebuild, reinstall, and restart the service from latest main.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		Summary:   "Latest main was rebuilt, reinstalled, and restarted.",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "checkout main, pull, rebuild, reinstall, restart the service")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}
	completed, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}

	if err := rt.runReentryRecommendationSweepOnce(context.Background(), completed.CompletedAt.Add(6*time.Minute)); err != nil {
		t.Fatalf("runReentryRecommendationSweepOnce() err = %v", err)
	}
	sender.mu.Lock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent = %#v, want one re-entry card", sender.sent)
	}
	card := sender.sent[0]
	sender.mu.Unlock()
	if strings.TrimSpace(card.Text) != "Possible next steps:" {
		t.Fatalf("card text = %q, want compact prompt", card.Text)
	}
	if len(card.ButtonRows) != 1 || len(card.ButtonRows[0]) < 2 || len(card.ButtonRows[0]) > 4 {
		t.Fatalf("button rows = %#v, want up to three candidates plus Ignore", card.ButtonRows)
	}
	if got := card.ButtonRows[0][len(card.ButtonRows[0])-1].Text; got != "Ignore" {
		t.Fatalf("last button = %q, want Ignore", got)
	}
	records, err := store.ReentryRecommendations(session.ReentryRecommendationFilter{SessionID: run.SessionID, Limit: 10})
	if err != nil {
		t.Fatalf("ReentryRecommendations() err = %v", err)
	}
	if len(records) != 1 || records[0].Status != session.ReentryRecommendationStatusShown {
		t.Fatalf("records = %#v, want one shown recommendation", records)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !testExecutionEventsContain(events, core.ExecutionEventReentryRecommendationShown) {
		t.Fatalf("events = %#v, want reentry recommendation shown event", events)
	}

	if err := rt.runReentryRecommendationSweepOnce(context.Background(), completed.CompletedAt.Add(7*time.Minute)); err != nil {
		t.Fatalf("second runReentryRecommendationSweepOnce() err = %v", err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent after duplicate sweep = %#v, want deduped single card", sender.sent)
	}
}

func TestReentryRecommendationRankingPayloadAvoidsPrivateContext(t *testing.T) {
	t.Parallel()

	cfg, _, provider, _ := buildRuntimeFixtures(t)
	provider.replyText = `{"candidates":[{"id":"c1","label":"Release check","rank":1},{"id":"c2","label":"Mission","rank":2}]}`
	rt := &Runtime{cfg: cfg, provider: provider}
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7010, UserID: 0},
		Run: session.TurnRun{
			ID:          44,
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "release Secret Project Night Orchard using fake credential marker for test",
			ErrorText:   "failed while reading /home/alice/.aphelion/secrets/app.env for Secret Project Night Orchard",
		},
		Operation: session.OperationState{
			ID:        "op-private",
			Objective: "Ship Secret Project Night Orchard",
			Status:    session.OperationStatusCompleted,
			Summary:   "Secret Project Night Orchard release is probably ready.",
			PhasePlan: session.OperationPhasePlan{
				CurrentPhaseID: "phase-private",
				Phases: []session.OperationPhase{
					{ID: "phase-private", Status: session.PlanStatusCompleted, AuthorityClass: "commit"},
				},
			},
		},
		Plan: session.PlanState{
			Explanation: "Private release checklist for Secret Project Night Orchard.",
			Steps:       []session.PlanStep{{Step: "Push Secret Project Night Orchard", Status: session.PlanStatusCompleted}},
		},
		Missions: []session.MissionState{
			{ID: "mission-secret", Title: "Secret Project Night Orchard", Objective: "Keep the private codename out of prompts.", Status: session.MissionStatusActive},
		},
		MemoryNotes: []string{"Daily private note: Secret Project Night Orchard depends on Alice."},
	}

	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	if len(candidates) < 2 {
		t.Fatalf("candidates = %#v, want multiple candidates for ranking", candidates)
	}
	for _, candidate := range candidates {
		for _, forbidden := range []string{"grant authority", "consumed lease", "fresh bounded approval", "fresh bounded lease"} {
			if strings.Contains(candidate.PromptText, forbidden) {
				t.Fatalf("candidate prompt leaked internal copy %q: %#v", forbidden, candidate)
			}
		}
		if !strings.Contains(candidate.PromptText, "ask before doing it") {
			t.Fatalf("candidate prompt = %q, want ask-before-action warning", candidate.PromptText)
		}
		if candidate.Kind == session.ReentryCandidateResumeMission &&
			(strings.Contains(candidate.Summary, "Secret Project Night Orchard") || strings.Contains(candidate.Summary, "private codename")) {
			t.Fatalf("mission candidate summary leaked private mission content: %#v", candidate)
		}
	}
	_ = rt.rankReentryRecommendationCandidates(context.Background(), state, candidates)

	var joined []string
	for _, msg := range provider.lastGovernorMsgs {
		joined = append(joined, msg.Content)
	}
	payload := strings.Join(joined, "\n")
	for _, forbidden := range []string{
		"Secret Project Night Orchard",
		"fake credential marker",
		"/home/alice/.aphelion/secrets/app.env",
		"Daily private note",
		"Push Secret Project",
		"Keep the private codename",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("ranking payload leaked %q:\n%s", forbidden, payload)
		}
	}
	for _, want := range []string{"mission_counts", "memory_note_count", "signals", "phase_status_counts"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("ranking payload = %q, want structural field %q", payload, want)
		}
	}
}

func testExecutionEventsContain(events []session.ExecutionEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func TestReentryRecommendationSweepSkipsActiveContinuation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt := &Runtime{cfg: cfg, store: store, provider: provider, outbound: sender}
	key := session.SessionKey{ChatID: 7002, UserID: 0}
	if err := store.UpdateOperationState(key, session.OperationState{
		ID:        "op-active-lease",
		Objective: "Continue already approved work.",
		Status:    session.OperationStatusActive,
		Stage:     "working",
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	if err := store.UpdateContinuationState(key, session.ContinuationState{
		Status:         session.ContinuationStatusApproved,
		DecisionID:     "decision-active",
		RemainingTurns: 1,
	}); err != nil {
		t.Fatalf("UpdateContinuationState() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "continue the approved work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.CompleteTurnRun(run.ID, session.TurnRunStatusCompleted, ""); err != nil {
		t.Fatalf("CompleteTurnRun() err = %v", err)
	}
	completed, err := store.TurnRun(run.ID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}

	if err := rt.runReentryRecommendationSweepOnce(context.Background(), completed.CompletedAt.Add(6*time.Minute)); err != nil {
		t.Fatalf("runReentryRecommendationSweepOnce() err = %v", err)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %#v, want no idle recommendation while continuation is approved", sender.sent)
	}
}
