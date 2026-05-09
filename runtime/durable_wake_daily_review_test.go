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

	sender.mu.Lock()
	if got := len(sender.inline); got != 1 {
		t.Fatalf("inline len = %d, want 1 immediate daily-review relay", got)
	}
	if sender.inline[0].chatID != 1001 {
		t.Fatalf("inline chat_id = %d, want 1001", sender.inline[0].chatID)
	}
	if !strings.Contains(strings.ToLower(sender.inline[0].text), "scheduled check-in") {
		t.Fatalf("inline text = %q, want scheduled check-in framing", sender.inline[0].text)
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

func TestDailyReviewWakeCanUseDurableAgentScopedExec(t *testing.T) {
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
		t.Fatalf("daily review cursor empty, want finalized wake after scoped exec")
	}
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
