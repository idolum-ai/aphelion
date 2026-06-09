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
