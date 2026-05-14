//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestScheduledReviewWakeStagesTranscriptAndQueuesArtifact(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "Worked: concise updates.\nDid not: delayed approvals.\nTomorrow: tighten escalation criteria."
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            "scheduled-review-test",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        scheduledReviewChannelKind,
		ChannelConfig:      testScheduledReviewChannelConfig(),
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

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate daily-review relay", got)
	}
	if sender.inline[0].chatID != 1001 {
		t.Fatalf("inline chat_id = %d, want 1001", sender.inline[0].chatID)
	}
	if !strings.Contains(strings.ToLower(sender.inline[0].text), "daily review") {
		t.Fatalf("inline text = %q, want daily review framing", sender.inline[0].text)
	}
	sender.mu.Unlock()

	events, err := store.PendingReviewEvents(1001, 10)
	if err != nil {
		t.Fatalf("PendingReviewEvents() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("PendingReviewEvents() len = %d, want 0 after immediate relay", len(events))
	}

	scope, err := rt.scopeForDurableAgent(agent)
	if err != nil {
		t.Fatalf("scopeForDurableAgent() err = %v", err)
	}
	transcriptPath := scheduledReviewTranscriptPath(scope.WorkingRoot, ".aphelion/daily-review", now.AddDate(0, 0, -1))
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

func TestScheduledReviewWakeCanUseDurableAgentScopedExec(t *testing.T) {
	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &durableWakeExecRequestingProvider{}
	resolver, err := sandbox.NewResolver(
		sandbox.Roots{
			GlobalRoot:        cfg.Agent.PromptRoot,
			AdminExecRoot:     cfg.Agent.ExecRoot,
			SharedMemoryRoot:  cfg.Agent.SharedMemoryRoot,
			UserWorkspaceRoot: cfg.Agent.UserWorkspaceRoot,
			UserMemoryRoot:    cfg.Agent.UserMemoryRoot,
		},
		sandbox.DefaultProfiles(),
	)
	if err != nil {
		t.Fatalf("NewResolver() err = %v", err)
	}
	tools := toolpkg.NewRegistryWithSandbox(cfg.Agent.ExecRoot, 2*time.Second, resolver).WithSessionStore(store)
	setFakeBubblewrapRunnerForRegistry(t, tools)
	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            "scheduled-review-test",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        scheduledReviewChannelKind,
		ChannelConfig:      testScheduledReviewChannelConfig(),
		LivePolicy: core.NormalizeDurableAgentLivePolicy(core.DurableAgentLivePolicy{
			Charter:            "Review yesterday's logs and propose tomorrow action items.",
			CapabilityEnvelope: []string{"bounded_review_artifact", "session_recall"},
			OutboundMode:       "read_only",
			DriftPolicy:        "admin_review",
		}),
		BootstrapLLM:      durableGroupTestBootstrapLLM(),
		WakeupMode:        "poll",
		Status:            "active",
		LocalStorageRoots: []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:     "restricted",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	key := session.SessionKey{ChatID: 17, UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []session.Message{{Role: "user", Content: "daily-review-exec-path-entry", TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	seedHits, err := store.SearchMessages("daily-review-exec-path-entry", 1, nil)
	if err != nil {
		t.Fatalf("SearchMessages(seed) err = %v", err)
	}
	if len(seedHits) != 1 {
		t.Fatalf("SearchMessages(seed) len = %d, want 1", len(seedHits))
	}
	seedAt := seedHits[0].CreatedAt.UTC()
	now := time.Date(seedAt.Year(), seedAt.Month(), seedAt.Day(), 0, 15, 0, 0, time.UTC).AddDate(0, 0, 1)

	if err := rt.pollDurableWakeAgents(context.Background(), now); err != nil {
		t.Fatalf("pollDurableWakeAgents() err = %v", err)
	}

	provider.mu.Lock()
	firstToolCount := provider.firstToolCount
	calls := provider.callCount
	provider.mu.Unlock()
	if firstToolCount == 0 || calls < 2 {
		t.Fatalf("provider firstToolCount/calls = %d/%d, want durable wake tool execution loop", firstToolCount, calls)
	}
	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if strings.TrimSpace(state.Cursor) == "" {
		t.Fatalf("scheduled review cursor empty, want finalized wake after scoped exec")
	}
}

func testScheduledReviewChannelConfig() core.DurableAgentChannelConfig {
	return core.NormalizeDurableAgentChannelConfig(core.DurableAgentChannelConfig{ScheduledReview: &core.DurableAgentScheduledReviewChannelConfig{
		Title:            "Daily review",
		ScheduleKind:     "daily",
		TimeUTC:          "00:10",
		Window:           "previous_day",
		MaxMessages:      1200,
		ArtifactKind:     "scheduled_check_in",
		TranscriptDir:    ".aphelion/daily-review",
		GuidanceQuestion: "What guidance should I apply before the next daily check-in?",
		PromptTemplate:   "Daily scheduled child-parent check-in for review_date={{review_date}}.\nTranscript file: {{transcript_path}}\nMessage count in staged window: {{message_count}}\nRead the transcript file, then reply as a normal child chat to the parent with:\n1) what worked yesterday\n2) what did not work\n3) concrete action items for tomorrow (max 5)\nKeep the message concise and operational.",
	}})
}

type durableWakeExecRequestingProvider struct {
	mu             sync.Mutex
	callCount      int
	firstToolCount int
	requested      bool
}

func (p *durableWakeExecRequestingProvider) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolDef) (*agent.Response, error) {
	if resp, ok := fakeInterpretationResponse(messages, "", core.TokenUsage{}); ok {
		return resp, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.callCount++
	if len(tools) > 0 && !p.requested {
		p.requested = true
		p.firstToolCount = len(tools)
		return &agent.Response{ToolCalls: []agent.ToolCall{{ID: "durable-wake-exec", Name: "exec", Input: json.RawMessage(`{"command":"echo hi"}`)}}}, nil
	}
	return &agent.Response{Content: "done"}, nil
}

func (p *durableWakeExecRequestingProvider) CompleteWithOptions(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, _ agent.CompleteOptions) (*agent.Response, error) {
	return p.Complete(ctx, messages, tools)
}

func TestScheduledReviewWakeFailureRecordsBackoffAndSuppressesRetry(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.err = context.DeadlineExceeded
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            "scheduled-review-failure-test",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        scheduledReviewChannelKind,
		ChannelConfig:      testScheduledReviewChannelConfig(),
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

	reviewDate, now := seedScheduledReviewMessage(t, store, "scheduled-review-failure-entry")
	if err := rt.pollDurableWakeAgents(context.Background(), now); err == nil {
		t.Fatal("first pollDurableWakeAgents() err = nil, want provider failure")
	}
	provider.mu.Lock()
	firstCalls := provider.callCount
	provider.mu.Unlock()
	if firstCalls == 0 {
		t.Fatal("provider calls = 0, want attempted scheduled review wake")
	}

	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState() err = %v", err)
	}
	if got := strings.TrimSpace(state.Cursor); got == reviewDate {
		t.Fatalf("durable state cursor = %q, want not advanced to failed review date", got)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if continuity.ScheduledReview == nil {
		t.Fatal("ScheduledReview state = nil, want failure lifecycle state")
	}
	if continuity.ScheduledReview.ReviewDate != reviewDate || continuity.ScheduledReview.FailureCount != 1 || continuity.ScheduledReview.BackoffUntil.IsZero() {
		t.Fatalf("ScheduledReview state = %#v, want failed review date with backoff", continuity.ScheduledReview)
	}
	if !now.Before(continuity.ScheduledReview.BackoffUntil) {
		t.Fatalf("backoff_until = %s, want after now %s", continuity.ScheduledReview.BackoffUntil, now)
	}

	if err := rt.pollDurableWakeAgents(context.Background(), now.Add(5*time.Minute)); err != nil {
		t.Fatalf("suppressed pollDurableWakeAgents() err = %v", err)
	}
	provider.mu.Lock()
	afterSuppressed := provider.callCount
	provider.mu.Unlock()
	if afterSuppressed != firstCalls {
		t.Fatalf("provider calls after suppressed retry = %d, want unchanged %d", afterSuppressed, firstCalls)
	}
}

func TestScheduledReviewNewReviewDateAttemptsDespitePriorBackoff(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.err = context.DeadlineExceeded
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            "scheduled-review-new-date-test",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        scheduledReviewChannelKind,
		ChannelConfig:      testScheduledReviewChannelConfig(),
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

	_, now := seedScheduledReviewMessage(t, store, "scheduled-review-new-date-entry")
	if err := rt.pollDurableWakeAgents(context.Background(), now); err == nil {
		t.Fatal("first pollDurableWakeAgents() err = nil, want provider failure")
	}
	provider.mu.Lock()
	firstCalls := provider.callCount
	provider.mu.Unlock()

	later := now.AddDate(0, 0, 1)
	if err := rt.pollDurableWakeAgents(context.Background(), later); err == nil {
		t.Fatal("new-date pollDurableWakeAgents() err = nil, want provider failure after fresh attempt")
	}
	provider.mu.Lock()
	afterNewDate := provider.callCount
	provider.mu.Unlock()
	if afterNewDate <= firstCalls {
		t.Fatalf("provider calls after new review date = %d, want > %d", afterNewDate, firstCalls)
	}
}

func seedScheduledReviewMessage(t *testing.T, store *session.SQLiteStore, content string) (string, time.Time) {
	t.Helper()
	key := session.SessionKey{ChatID: time.Now().UnixNano(), UserID: 0}
	sess, err := store.Load(key)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	sess.TurnCount = 1
	if err := store.Save(sess, []session.Message{{Role: "user", Content: content, TurnIndex: 1}}, core.TokenUsage{}); err != nil {
		t.Fatalf("Save() err = %v", err)
	}
	hits, err := store.SearchMessages(content, 1, nil)
	if err != nil {
		t.Fatalf("SearchMessages(seed) err = %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("SearchMessages(seed) len = %d, want 1", len(hits))
	}
	seedAt := hits[0].CreatedAt.UTC()
	reviewDate := seedAt.Format("2006-01-02")
	now := time.Date(seedAt.Year(), seedAt.Month(), seedAt.Day(), 0, 15, 0, 0, time.UTC).AddDate(0, 0, 1)
	return reviewDate, now
}

func TestScheduledReviewAlreadyAwakeAfterPrepareRecordsBackoff(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	provider.replyText = "should not run"
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.durableWakeChild = nil

	agent := core.DurableAgent{
		AgentID:            "scheduled-review-already-awake-test",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        scheduledReviewChannelKind,
		ChannelConfig:      testScheduledReviewChannelConfig(),
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
	reviewDate, now := seedScheduledReviewMessage(t, store, "scheduled-review-already-awake-entry")
	adapter := newScheduledReviewDurableWakeAdapter()
	plan, err := adapter.Prepare(context.Background(), rt, agent, now)
	if err != nil {
		t.Fatalf("Prepare() err = %v", err)
	}
	if plan == nil {
		t.Fatal("Prepare() plan = nil, want scheduled wake plan")
	}
	state, err := store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState(after prepare) err = %v", err)
	}
	state.Status = "awake"
	state.LastWakeAt = now.UTC()
	if err := store.SaveDurableAgentState(*state); err != nil {
		t.Fatalf("SaveDurableAgentState(awake) err = %v", err)
	}

	if err := rt.runDurableWakeTurn(context.Background(), agent, *plan, now); err != nil {
		t.Fatalf("runDurableWakeTurn() err = %v", err)
	}
	provider.mu.Lock()
	calls := provider.callCount
	provider.mu.Unlock()
	if calls != 0 {
		t.Fatalf("provider calls = %d, want skipped before conversation", calls)
	}
	state, err = store.DurableAgentState(agent.AgentID)
	if err != nil {
		t.Fatalf("DurableAgentState(after run) err = %v", err)
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		t.Fatalf("ParseDurableAgentContinuityState() err = %v", err)
	}
	if continuity.ScheduledReview == nil || continuity.ScheduledReview.ReviewDate != reviewDate || continuity.ScheduledReview.FailureCount != 1 || continuity.ScheduledReview.BackoffUntil.IsZero() {
		t.Fatalf("ScheduledReview state = %#v, want backoff after already-awake skip", continuity.ScheduledReview)
	}
	if continuity.ScheduledReview.LastStatus != "wake_failed" {
		t.Fatalf("LastStatus = %q, want wake_failed", continuity.ScheduledReview.LastStatus)
	}
}
