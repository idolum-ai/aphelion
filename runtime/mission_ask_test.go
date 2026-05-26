//go:build linux

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/turn"
)

func TestMaybeOfferMissionAskCreatesLedgerPromptAndTelegramCard(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	if _, err := store.UpsertMission(session.MissionState{
		ID:        "mission-readme",
		Title:     "README cleanup",
		Objective: "Keep README docs concise and operator-friendly.",
		Owner:     "telegram:1001",
		Status:    session.MissionStatusActive,
		Pinned:    true,
		Tags:      []string{"readme", "docs"},
		Authority: session.DefaultMissionAuthority(),
	}, "telegram:1001", "seed mission"); err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	msg := core.InboundMessage{ChatID: 1001, SenderID: 1001, MessageID: 42, Text: "Please review the README docs."}
	if err := rt.maybeOfferMissionAsk(context.Background(), key, msg, msg.Text, missionAskPersistedResult()); err != nil {
		t.Fatalf("maybeOfferMissionAsk() err = %v", err)
	}

	prompts, err := store.MissionAskPrompts(session.MissionAskPromptFilter{Owner: "telegram:1001", Limit: 10})
	if err != nil {
		t.Fatalf("MissionAskPrompts() err = %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("prompts len = %d, want 1", len(prompts))
	}
	prompt := prompts[0]
	if prompt.MissionID != "mission-readme" || prompt.Status != session.MissionAskStatusPending || prompt.Confidence != session.MissionAskConfidenceHigh {
		t.Fatalf("prompt = %#v, want high-confidence pending mission association", prompt)
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want mission Ask Me card", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Mission Question") || !strings.Contains(sender.inline[0].text, "README cleanup") {
		t.Fatalf("inline text = %q, want question card", sender.inline[0].text)
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 2 {
		t.Fatalf("inline rows = %#v, want Ignore/Ask Me buttons", sender.inline[0].rows)
	}
	if _, action, ok := core.DecodeMissionAskCallbackData(sender.inline[0].rows[0][0].CallbackData); !ok || action != core.MissionAskCallbackIgnore {
		t.Fatalf("Ignore callback = %q, want mission ignore callback", sender.inline[0].rows[0][0].CallbackData)
	}
	if _, action, ok := core.DecodeMissionAskCallbackData(sender.inline[0].rows[0][1].CallbackData); !ok || action != core.MissionAskCallbackAsk {
		t.Fatalf("Ask Me callback = %q, want mission ask callback", sender.inline[0].rows[0][1].CallbackData)
	}
}

func TestMaybeOfferMissionAskSuppressesRepeatedAssociation(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	if _, err := store.UpsertMission(session.MissionState{
		ID:        "mission-status",
		Title:     "Status UI",
		Objective: "Keep status messages useful.",
		Owner:     "telegram:1001",
		Status:    session.MissionStatusActive,
		Pinned:    true,
		Tags:      []string{"status"},
		Authority: session.DefaultMissionAuthority(),
	}, "telegram:1001", "seed mission"); err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}

	msg := core.InboundMessage{ChatID: 1001, SenderID: 1001, MessageID: 42, Text: "Please review the status UI."}
	if err := rt.maybeOfferMissionAsk(context.Background(), key, msg, msg.Text, missionAskPersistedResult()); err != nil {
		t.Fatalf("maybeOfferMissionAsk(first) err = %v", err)
	}
	msg.MessageID = 43
	if err := rt.maybeOfferMissionAsk(context.Background(), key, msg, msg.Text, missionAskPersistedResult()); err != nil {
		t.Fatalf("maybeOfferMissionAsk(second) err = %v", err)
	}
	prompts, err := store.MissionAskPrompts(session.MissionAskPromptFilter{Owner: "telegram:1001", Limit: 10})
	if err != nil {
		t.Fatalf("MissionAskPrompts() err = %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("prompts len = %d, want same association suppressed", len(prompts))
	}
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want no second card", len(sender.inline))
	}
}

func TestMaybeOfferMissionAskSkipsContinuationIntentTurns(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	if _, err := store.UpsertMission(session.MissionState{
		ID:        "mission-continuation",
		Title:     "Continuation checks",
		Objective: "Keep continuation approval checks grounded.",
		Owner:     "telegram:1001",
		Status:    session.MissionStatusActive,
		Pinned:    true,
		Tags:      []string{"continuation"},
		Authority: session.DefaultMissionAuthority(),
	}, "telegram:1001", "seed mission"); err != nil {
		t.Fatalf("UpsertMission() err = %v", err)
	}
	result := missionAskPersistedResult()
	result.PersonaIntent.Decision = session.ContinuationIntentDecisionContinue
	msg := core.InboundMessage{ChatID: 1001, SenderID: 1001, MessageID: 42, Text: "Please review continuation checks."}
	if err := rt.maybeOfferMissionAsk(context.Background(), key, msg, msg.Text, result); err != nil {
		t.Fatalf("maybeOfferMissionAsk() err = %v", err)
	}
	if len(sender.inline) != 0 {
		t.Fatalf("inline len = %d, want no mission prompt for continuation-intent turn", len(sender.inline))
	}
}

func TestPendingMissionAskHiddenInputFollowsAskedPrompt(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	admin, ok := rt.resolver.ResolveTelegramUser(1001)
	if !ok {
		t.Fatal("ResolveTelegramUser(1001) = false, want admin")
	}

	key := session.SessionKey{ChatID: 1001, Scope: telegramDMScopeRef(1001)}
	prompt, allowed, reason, err := store.CreateMissionAskPromptIfAllowed(session.MissionAskPrompt{
		Owner:             "telegram:1001",
		ChatID:            1001,
		SenderID:          1001,
		SessionID:         session.SessionIDForKey(key),
		Scope:             key.Scope,
		MissionID:         "mission-hidden-input",
		Confidence:        session.MissionAskConfidenceHigh,
		QuestionText:      "Should this be tied to a mission?",
		SourceFingerprint: "hidden-input",
	}, time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC))
	if err != nil || !allowed || reason != "" {
		t.Fatalf("CreateMissionAskPromptIfAllowed() allowed=%t reason=%q err=%v, want prompt", allowed, reason, err)
	}
	if _, err := store.UpdateMissionAskPromptStatus(prompt.ID, prompt.Owner, session.MissionAskStatusAsked, "queued", time.Date(2026, 5, 26, 12, 1, 0, 0, time.UTC)); err != nil {
		t.Fatalf("UpdateMissionAskPromptStatus(asked) err = %v", err)
	}

	hidden := rt.pendingMissionAskHiddenInput(admin, key)
	if !strings.Contains(hidden, "prompt_id="+prompt.ID) || !strings.Contains(hidden, "Should this be tied to a mission?") {
		t.Fatalf("hidden input = %q, want pending mission ask context", hidden)
	}
}

func TestMissionAskClassifierMessagesUseProductionContract(t *testing.T) {
	t.Parallel()

	observation := missionAskObservation{
		Query:       "Please review the README docs again.",
		MissionID:   "mission-readme",
		MissionName: "README cleanup",
		Question:    "Should this stay with README cleanup?",
		Confidence:  session.MissionAskConfidenceLow,
	}
	messages := missionAskClassifierMessages(observation)
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	if messages[0].Role != "system" || messages[1].Role != "user" {
		t.Fatalf("message roles = %q/%q, want system/user", messages[0].Role, messages[1].Role)
	}
	system := messages[0].Content
	for _, want := range []string{
		"## Role",
		"## Goal",
		"## Success Criteria",
		"## Output",
		"## Confidence",
		"## Stop Rules",
		"Return JSON only",
		"same_objective|new_objective|ignore|unclear",
		"mission_id may be copied from the supplied candidate only",
		"include a non-empty compact question",
		"When a question would feel like pestering, choose ignore.",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("classifier system prompt missing %q: %q", want, system)
		}
	}
	user := messages[1].Content
	for _, want := range []string{
		"candidate_mission_id=mission-readme",
		"candidate_name=README cleanup",
		"local_confidence=low",
		"query=Please review the README docs again.",
		"question=Should this stay with README cleanup?",
	} {
		if !strings.Contains(user, want) {
			t.Fatalf("classifier user prompt missing %q: %q", want, user)
		}
	}
}

func TestExtractMissionAskJSONTrimsWrappedObject(t *testing.T) {
	t.Parallel()

	got := extractMissionAskJSON("```json\n{\"action\":\"ignore\",\"confidence\":\"low\"}\n```")
	if got != "{\"action\":\"ignore\",\"confidence\":\"low\"}" {
		t.Fatalf("extractMissionAskJSON() = %q", got)
	}
}

func missionAskPersistedResult() *turn.Result {
	return &turn.Result{
		Turn:         &core.TurnResult{Text: "Done."},
		VisibleReply: "Done.",
		Commit:       turn.CommitResult{Persisted: true},
	}
}
