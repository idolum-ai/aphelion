//go:build linux

package runtime

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestDailyReviewWakeStagesTranscriptAndQueuesScheduledCheckIn(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Worked: concise updates.\nDid not: delayed approvals.\nTomorrow: tighten escalation criteria."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            defaultDailyReviewDurableAgentID,
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        dailyReviewDurableChannelKind,
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review yesterday's logs and propose tomorrow action items.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM: durableGroupTestBootstrapLLM(),
		WakeupMode:   "poll",
		Status:       "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []session.Message{
		{Role: "user", Content: "daily-review-log-entry", TurnIndex: 1},
	}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	seedHits, err := store.SearchMessages("daily-review-log-entry", 1, nil)
	if err != nil {
		t.Fatalf("SearchMessages(seed) err = %v", err)
	}
	if len(seedHits) != 1 {
		t.Fatalf("SearchMessages(seed) len = %d, want 1", len(seedHits))
	}
	seedAt := seedHits[0].CreatedAt.UTC()
	expectedReviewDate := seedAt.Format("2006-01-02")

	now := time.Date(seedAt.Year(), seedAt.Month(), seedAt.Day(), 0, 15, 0, 0, time.UTC).AddDate(0, 0, 1)
	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("PendingReviewEvents() len = %d, want 1", len(events))
	}
	if !strings.Contains(strings.ToLower(events[0].Summary), "scheduled check-in") {
		t.Fatalf("review summary = %q, want scheduled check-in framing", events[0].Summary)
	}

	scope, err := rt.scopeForDurableAgent(agent)
	if err != nil {
		t.Fatalf("scopeForDurableAgent() err = %v", err)
	}
	transcriptPath := dailyReviewTranscriptPath(scope.WorkingRoot, now.AddDate(0, 0, -1))
	raw, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read staged transcript %s err = %v", transcriptPath, err)
	}
	if !strings.Contains(string(raw), "daily-review-log-entry") {
		t.Fatalf("staged transcript = %q, want previous-day log entry", string(raw))
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if got := strings.TrimSpace(state.Cursor); got != expectedReviewDate {
		t.Fatalf("durable state cursor = %q, want %s", got, expectedReviewDate)
	}

	provider.mu.Lock()
	beforeSecond := provider.callCount
	provider.mu.Unlock()
	if err := rt.pollDurableWakeAgents(context.Background(), now.Add(2*time.Hour)); err != nil {
		t.Fatalf("second pollDurableWakeAgents() err = %v", err)
	}
	provider.mu.Lock()
	afterSecond := provider.callCount
	provider.mu.Unlock()
	if afterSecond != beforeSecond {
		t.Fatalf("provider call count after second poll = %d, want unchanged %d", afterSecond, beforeSecond)
	}
}
