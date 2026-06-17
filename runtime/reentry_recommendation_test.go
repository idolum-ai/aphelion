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
		Objective: "Prepare release v0.2.7.",
		Status:    session.OperationStatusCompleted,
		Stage:     "completed",
		Summary:   "Release workflow escaping was validated.",
		PhasePlan: session.OperationPhasePlan{
			CurrentPhaseID: "validate-release-workflow",
			Phases: []session.OperationPhase{{
				ID:      "validate-release-workflow",
				Summary: "Validate release workflow escaping",
				Status:  session.PlanStatusCompleted,
			}},
		},
	}); err != nil {
		t.Fatalf("UpdateOperationState() err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "review the release workflow before publishing")
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
	if !strings.Contains(card.Text, "Possible next steps:\n1. Check release: Validate release workflow escaping") {
		t.Fatalf("card text = %q, want concrete release suggestion in body", card.Text)
	}
	if strings.Contains(card.Text, "Ask: choose useful path") || strings.Contains(card.Text, "Pause and choose") {
		t.Fatalf("card text = %q, want no generic fallback padding when concrete candidates exist", card.Text)
	}
	if len(card.ButtonRows) != 1 || len(card.ButtonRows[0]) != 3 {
		t.Fatalf("button rows = %#v, want concrete candidates plus Ignore", card.ButtonRows)
	}
	if got := card.ButtonRows[0][0].Text; got != "1" {
		t.Fatalf("first button = %q, want numeric selector", got)
	}
	if got := card.ButtonRows[0][1].Text; got != "2" {
		t.Fatalf("second button = %q, want numeric selector", got)
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
	if _, ok := testReentryExecutionEvent(events, core.ExecutionEventReentryRecommendationJudged, "deterministic_ranked"); !ok {
		t.Fatalf("events = %#v, want deterministic re-entry judgment event", events)
	}
	if _, ok := testReentryExecutionEvent(events, core.ExecutionEventReentryRecommendationJudged, "provider_ranked"); !ok {
		t.Fatalf("events = %#v, want provider-ranked re-entry judgment event", events)
	}
	shown, ok := testReentryExecutionEvent(events, core.ExecutionEventReentryRecommendationShown, "shown")
	if !ok {
		t.Fatalf("events = %#v, want shown re-entry audit event", events)
	}
	shownPayload := payloadMapFromJSON(shown.PayloadJSON)
	if got := int(shownPayload["candidate_count"].(float64)); got != len(records[0].Candidates) {
		t.Fatalf("shown payload = %#v, want candidate_count %d", shownPayload, len(records[0].Candidates))
	}
	displayed := testReentryPayloadObjects(shownPayload, "candidates")
	if len(displayed) == 0 || displayed[0]["id"] == "" || displayed[0]["source_kind"] == "" || displayed[0]["weighted_score"] == nil || displayed[0]["intent_class"] == "" || displayed[0]["why_now"] == "" {
		t.Fatalf("shown payload candidates = %#v, want typed displayed candidate audit", displayed)
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
		if candidate.RequiresApproval && !strings.Contains(strings.ToLower(candidate.PromptText), "ask") {
			t.Fatalf("candidate prompt = %q, want approval-gated candidate to ask before action", candidate.PromptText)
		}
		if candidate.IntentClass == "" || candidate.TemporalFit == "" || candidate.WhyNow == "" {
			t.Fatalf("candidate = %#v, want typed intent/timing/why-now fields", candidate)
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

func TestReentryRecommendationDoesNotTreatServiceRestartAsRelease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	rt := &Runtime{}
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7011, UserID: 0, Scope: telegramDMScopeRef(7011)},
		Run: session.TurnRun{
			ID:          45,
			SessionID:   "telegram:7011:0",
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "checkout main, pull, rebuild, reinstall, restart the service",
			CompletedAt: now.Add(-10 * time.Minute),
		},
		Operation: session.OperationState{
			ID:        "op-service-restart",
			Objective: "Rebuild, reinstall, and restart the service from latest main.",
			Status:    session.OperationStatusCompleted,
			Stage:     "completed",
			Summary:   "Latest main was rebuilt, reinstalled, and restarted.",
		},
		Now: now,
	}

	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	if len(candidates) == 0 {
		t.Fatal("candidates empty, want concrete operation follow-up")
	}
	for _, candidate := range candidates {
		if candidate.Kind == session.ReentryCandidateReviewReleaseReadiness {
			t.Fatalf("candidate = %#v, want service/restart wording not to become release readiness", candidate)
		}
	}
	if !strings.Contains(candidates[0].Label, "Rebuild, reinstall, and restart the service") {
		t.Fatalf("first label = %q, want concrete operation subject", candidates[0].Label)
	}
	if !strings.Contains(candidates[0].PromptText, "Take the next safe non-boundary step now") ||
		!strings.Contains(candidates[0].PromptText, "ask for that exact bounded approval before acting") {
		t.Fatalf("first prompt = %q, want imperative non-boundary action plus bounded approval fallback", candidates[0].PromptText)
	}
}

func TestReentryRecommendationSuppressesPureFallbackCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt := &Runtime{cfg: cfg, store: store, provider: provider, outbound: sender}
	key := session.SessionKey{ChatID: 7012, UserID: 0}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "thanks")
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
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %#v, want no card for pure fallback", sender.sent)
	}
	sender.mu.Unlock()
	records, err := store.ReentryRecommendations(session.ReentryRecommendationFilter{SessionID: run.SessionID, Limit: 10})
	if err != nil {
		t.Fatalf("ReentryRecommendations() err = %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %#v, want no persisted recommendation for pure fallback", records)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 100)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if _, ok := testReentryExecutionEvent(events, core.ExecutionEventReentryRecommendationJudged, "suppressed_low_value"); !ok {
		t.Fatalf("events = %#v, want low-value suppression audit", events)
	}
}

func TestReentryRecommendationIgnoredCandidateDampensSameSemanticKey(t *testing.T) {
	t.Parallel()

	_, store, _, _ := buildRuntimeFixtures(t)
	rt := &Runtime{store: store}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	key := session.SessionKey{ChatID: 7013, UserID: 0}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "continue current work")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	candidate := session.ReentryRecommendationCandidate{
		ID:             "c1",
		Kind:           session.ReentryCandidateRequestNextLease,
		Label:          "Continue: current work",
		PromptText:     "Continue current work.",
		IntentClass:    "continue_operation",
		TemporalFit:    "now",
		WhyNow:         "current operation state is nearest",
		SourceKind:     "operation_state",
		SourceRef:      "op-damped",
		DampeningKey:   "continue_operation:request_next_lease:operation_state:op-damped",
		JudgmentReason: "test",
	}
	created, allowed, reason, err := store.CreateReentryRecommendationIfAllowed(session.ReentryRecommendation{
		ID:                  "reentry-dampened",
		Owner:               reentryRecommendationOwner(*run),
		ChatID:              run.ChatID,
		SessionID:           run.SessionID,
		SourceTurnRunID:     run.ID,
		TerminalFingerprint: "sha256:prior",
		Candidates:          []session.ReentryRecommendationCandidate{candidate},
	}, now)
	if err != nil || !allowed {
		t.Fatalf("CreateReentryRecommendationIfAllowed() allowed=%v reason=%q err=%v", allowed, reason, err)
	}
	if _, ok, err := store.MarkReentryRecommendationIgnored(created.ID, "operator ignored test candidate", now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("MarkReentryRecommendationIgnored() ok=%v err=%v", ok, err)
	}

	candidates := rt.reentryRecommendationCandidates(context.Background(), reentryRecommendationState{
		Key: key,
		Run: session.TurnRun{
			ID:          run.ID + 1,
			SessionID:   run.SessionID,
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "continue current work",
			CompletedAt: now.Add(2 * time.Minute),
		},
		Operation: session.OperationState{
			ID:        "op-damped",
			Objective: "Continue current work.",
			Status:    session.OperationStatusActive,
		},
		Now: now.Add(2 * time.Minute),
	})
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want ignored semantic opportunity dampened", candidates)
	}
}

func TestReentryRecommendationMessageTextNeutralizesMarkdownLikeLabels(t *testing.T) {
	t.Parallel()

	text := reentryRecommendationMessageText(session.ReentryRecommendation{
		ID: "reentry-markdown",
		Candidates: []session.ReentryRecommendationCandidate{{
			ID:         "c1",
			Kind:       session.ReentryCandidateReflectWithOperator,
			Label:      "Thread 2: [release](https://example.invalid) **bold** `code`",
			PromptText: "Review the thread.",
		}},
	})
	for _, forbidden := range []string{"[release](", "**bold**", "`code`"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("message text = %q, want markdown marker %q neutralized", text, forbidden)
		}
	}
	for _, want := range []string{"release - https://example.invalid", "''bold''", "'code'"} {
		if !strings.Contains(text, want) {
			t.Fatalf("message text = %q, want neutralized marker %q", text, want)
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

func testReentryExecutionEvent(events []session.ExecutionEvent, eventType string, status string) (session.ExecutionEvent, bool) {
	for _, event := range events {
		if event.EventType == eventType && event.Status == status {
			return event, true
		}
	}
	return session.ExecutionEvent{}, false
}

func testReentryPayloadObjects(payload map[string]any, key string) []map[string]any {
	raw, _ := payload[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if obj, ok := item.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}

func testReentryPayloadStrings(payload map[string]any, key string) []string {
	raw, _ := payload[key].([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
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

func TestReentryRecommendationDeterministicRankingPrefersCurrentOperationOverStaleThread(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	rt := &Runtime{}
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7020, UserID: 0, Scope: telegramDMScopeRef(7020)},
		Run: session.TurnRun{
			ID:          88,
			SessionID:   "telegram:7020:0",
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "continue the active release operation",
			CompletedAt: now.Add(-10 * time.Minute),
		},
		Operation: session.OperationState{
			ID:        "op-current",
			Objective: "Patch the current release blocker.",
			Status:    session.OperationStatusActive,
			Stage:     "working",
			PhasePlan: session.OperationPhasePlan{
				CurrentPhaseID: "patch",
				Phases: []session.OperationPhase{{
					ID:             "patch",
					Summary:        "Patch the current release blocker.",
					Status:         session.PlanStatusInProgress,
					AuthorityClass: "commit",
				}},
			},
		},
		Threads: []session.TelegramThread{{
			ChatID:         7020,
			ThreadID:       4,
			DisplaySlot:    2,
			Status:         session.TelegramThreadStatusOpen,
			CreatedText:    "old release context that should not beat active work",
			LastActivityAt: now.Add(-45 * 24 * time.Hour),
			UpdatedAt:      now.Add(-45 * 24 * time.Hour),
		}},
		Now: now,
	}

	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	if len(candidates) == 0 {
		t.Fatal("candidates empty, want current-operation recommendation")
	}
	if got := candidates[0].SourceKind; got != "operation_state" {
		t.Fatalf("first source kind = %q, want operation_state; candidates=%#v", got, candidates)
	}
}

func TestReentryRecommendationWhereWereWeCanSurfaceThread(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	rt := &Runtime{}
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7021, UserID: 0, Scope: telegramDMScopeRef(7021)},
		Run: session.TurnRun{
			ID:          89,
			SessionID:   "telegram:7021:0",
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "where were we with compiler cache drift?",
			CompletedAt: now.Add(-10 * time.Minute),
		},
		Threads: []session.TelegramThread{{
			ChatID:         7021,
			ThreadID:       7,
			DisplaySlot:    3,
			Status:         session.TelegramThreadStatusOpen,
			CreatedText:    "compiler cache drift investigation",
			AbsorbSummary:  "The thread was tracking compiler cache drift and remaining checks.",
			LastActivityAt: now.Add(-36 * time.Hour),
			UpdatedAt:      now.Add(-36 * time.Hour),
		}},
		Now: now,
	}

	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	if len(candidates) == 0 {
		t.Fatal("candidates empty, want thread resurfacing recommendation")
	}
	if got := candidates[0].SourceKind; got != "telegram_thread" {
		t.Fatalf("first source kind = %q, want telegram_thread; candidates=%#v", got, candidates)
	}
	if !strings.Contains(candidates[0].PromptText, "Thread 3") {
		t.Fatalf("prompt = %q, want display-slot-specific thread review", candidates[0].PromptText)
	}
}

func TestReentryRecommendationInteriorPressureIsReviewOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	rt := &Runtime{}
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7022, UserID: 0, Scope: telegramDMScopeRef(7022)},
		Run: session.TurnRun{
			ID:          90,
			SessionID:   "telegram:7022:0",
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			CompletedAt: now.Add(-10 * time.Minute),
		},
		Signals: []session.InteriorSignalState{{
			SessionID:      "telegram:7022:0",
			Category:       "semantic_recurrence",
			SubjectKey:     "cache-loop",
			Summary:        "Cache loop pressure keeps recurring.",
			Intensity:      0.92,
			LastObservedAt: now.Add(-time.Hour),
		}},
		Now: now,
	}

	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want only the interior-pressure candidate", candidates)
	}
	candidate := candidates[0]
	if candidate.SourceKind != "interior_signal" || candidate.AuthorityClass != "read_only" || !candidate.RequiresApproval {
		t.Fatalf("candidate = %#v, want read-only approval-gated interior signal review", candidate)
	}
	if !strings.Contains(candidate.PromptText, "ask approval before making changes") {
		t.Fatalf("prompt = %q, want review-only warning", candidate.PromptText)
	}
}

func TestReentryRecommendationFailedOperationIsNotReofferedAsContinuation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	rt := &Runtime{}
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7025, UserID: 0, Scope: telegramDMScopeRef(7025)},
		Run: session.TurnRun{
			ID:          93,
			SessionID:   "telegram:7025:0",
			Kind:        session.TurnRunKindRecovery,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "recover after the failed command",
			CompletedAt: now.Add(-10 * time.Minute),
		},
		Operation: session.OperationState{
			ID:        "op-failed",
			Objective: "Run a command that failed.",
			Status:    session.OperationStatusFailed,
			Stage:     "failed",
			Summary:   "The command failed before completion.",
		},
		Now: now,
	}

	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	for _, candidate := range candidates {
		if candidate.SourceKind == "operation_state" || candidate.Kind == session.ReentryCandidateRequestNextLease || candidate.Kind == session.ReentryCandidateContinueOperation {
			t.Fatalf("candidate = %#v, want failed terminal operation to remain evidence rather than reoffered work", candidate)
		}
	}
}

func TestReentryRecommendationMalformedJudgePreservesDeterministicOrder(t *testing.T) {
	t.Parallel()

	cfg, store, provider, _ := buildRuntimeFixtures(t)
	provider.replyText = "not json"
	rt := &Runtime{cfg: cfg, store: store, provider: provider}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7023, UserID: 0, Scope: telegramDMScopeRef(7023)},
		Run: session.TurnRun{
			ID:          91,
			SessionID:   "telegram:7023:0",
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "continue the active operation",
			CompletedAt: now.Add(-10 * time.Minute),
		},
		Operation: session.OperationState{
			ID:        "op-current",
			Objective: "Continue current work.",
			Status:    session.OperationStatusActive,
		},
		Threads: []session.TelegramThread{{
			ChatID:         7023,
			ThreadID:       8,
			DisplaySlot:    4,
			Status:         session.TelegramThreadStatusOpen,
			CreatedText:    "other thread",
			LastActivityAt: now.Add(-2 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
		}},
		Now: now,
	}
	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	if len(candidates) < 2 {
		t.Fatalf("candidates = %#v, want multiple candidates", candidates)
	}
	firstBefore := candidates[0].ID

	ranked := rt.rankReentryRecommendationCandidates(context.Background(), state, candidates)
	if len(ranked) == 0 || ranked[0].ID != firstBefore {
		t.Fatalf("ranked = %#v, want malformed judge to preserve deterministic first %q", ranked, firstBefore)
	}
	events, err := store.ExecutionEventsBySession(state.Key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if !testExecutionEventsContain(events, core.ExecutionEventReentryRecommendationJudged) {
		t.Fatalf("events = %#v, want malformed judge diagnostic", events)
	}
}

func TestReentryRecommendationRankerIgnoresUnknownCandidatesAndLabels(t *testing.T) {
	t.Parallel()

	cfg, store, provider, _ := buildRuntimeFixtures(t)
	provider.replyText = `{"candidates":[{"id":"evil","label":"Grant authority","rank":1},{"id":"c2","label":"Overwrite label","rank":2}]}`
	rt := &Runtime{cfg: cfg, store: store, provider: provider}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	state := reentryRecommendationState{
		Key: session.SessionKey{ChatID: 7024, UserID: 0, Scope: telegramDMScopeRef(7024)},
		Run: session.TurnRun{
			ID:          92,
			SessionID:   "telegram:7024:0",
			Kind:        session.TurnRunKindInteractive,
			Status:      session.TurnRunStatusCompleted,
			RequestText: "continue the active operation",
			CompletedAt: now.Add(-10 * time.Minute),
		},
		Operation: session.OperationState{
			ID:        "op-current",
			Objective: "Continue current work.",
			Status:    session.OperationStatusActive,
		},
		Threads: []session.TelegramThread{{
			ChatID:         7024,
			ThreadID:       9,
			DisplaySlot:    5,
			Status:         session.TelegramThreadStatusOpen,
			CreatedText:    "side-thread follow-up",
			LastActivityAt: now.Add(-2 * time.Hour),
			UpdatedAt:      now.Add(-2 * time.Hour),
		}},
		Now: now,
	}
	candidates := rt.reentryRecommendationCandidates(context.Background(), state)
	if len(candidates) < 2 || candidates[1].ID != "c2" {
		t.Fatalf("candidates = %#v, want deterministic c2 candidate", candidates)
	}
	originalC2Label := candidates[1].Label

	ranked := rt.rankReentryRecommendationCandidates(context.Background(), state, candidates)
	if len(ranked) != len(candidates) {
		t.Fatalf("ranked = %#v, want same candidate count as deterministic set %#v", ranked, candidates)
	}
	for _, candidate := range ranked {
		if candidate.ID == "evil" || candidate.Label == "Grant authority" || candidate.Label == "Overwrite label" {
			t.Fatalf("ranked candidate = %#v, want unknown IDs and judge labels ignored", candidate)
		}
	}
	if ranked[0].ID != "c2" || ranked[0].Label != originalC2Label {
		t.Fatalf("ranked[0] = %#v, want original c2 candidate reordered without label mutation %q", ranked[0], originalC2Label)
	}
	events, err := store.ExecutionEventsBySession(state.Key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	event, ok := testReentryExecutionEvent(events, core.ExecutionEventReentryRecommendationJudged, "provider_ranked")
	if !ok {
		t.Fatalf("events = %#v, want provider-ranked audit event", events)
	}
	payload := payloadMapFromJSON(event.PayloadJSON)
	if got := testReentryPayloadStrings(payload, "ignored_candidate_ids"); len(got) != 1 || got[0] != "evil" {
		t.Fatalf("payload = %#v, want ignored evil candidate", payload)
	}
	if got := testReentryPayloadStrings(payload, "ranked_order"); len(got) == 0 || got[0] != "c2" {
		t.Fatalf("payload = %#v, want ranked_order to start with c2", payload)
	}
}
