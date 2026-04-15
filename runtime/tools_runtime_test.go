//go:build linux

package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/face"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	toolpkg "github.com/idolum-ai/aphelion/tool"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

func TestHandleInboundApprovedUserDisablesToolsWithoutIsolationFloor(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &legacyRecordingTools{
		defs: []agent.ToolDef{testExecToolDef()},
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     501,
		SenderID:   1002,
		SenderName: "approved",
		Text:       "run pwd",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if provider.firstToolCount != 0 {
		t.Fatalf("first tool count = %d, want 0", provider.firstToolCount)
	}
	if tools.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", tools.executeCalls)
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].Text != "no tools" {
		t.Fatalf("outbound text = %q, want no tools", sender.sent[0].Text)
	}
}

func TestHandleInboundApprovedUserUsesPrincipalAwareToolsWhenSupported(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{
		defs:              []agent.ToolDef{testExecToolDef()},
		supportsPrincipal: true,
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     502,
		SenderID:   1002,
		SenderName: "approved",
		Text:       "run pwd",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if provider.firstToolCount != 1 {
		t.Fatalf("first tool count = %d, want 1", provider.firstToolCount)
	}
	if tools.executeForPrincipalCalls != 1 {
		t.Fatalf("executeForPrincipal calls = %d, want 1", tools.executeForPrincipalCalls)
	}
	if tools.executeCalls != 0 {
		t.Fatalf("legacy execute calls = %d, want 0", tools.executeCalls)
	}
	if tools.lastPrincipal.Role != principal.RoleApprovedUser {
		t.Fatalf("last principal role = %q, want approved_user", tools.lastPrincipal.Role)
	}
	if tools.lastPrincipal.TelegramUserID != 1002 {
		t.Fatalf("last principal user id = %d, want 1002", tools.lastPrincipal.TelegramUserID)
	}
}

func TestHandleInboundAdminCanManageDurableAgentThroughConversationTool(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &durableAgentToolRequestingProvider{}
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

	agent := core.DurableAgent{
		AgentID:            "family-group",
		ParentScopeKind:    string(session.ScopeKindHeartbeat),
		ParentScopeID:      "admin-house",
		ReviewTargetChatID: 1001,
		ChannelKind:        "telegram_group",
		LivePolicy:         core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues."),
		BootstrapCeiling:   core.DefaultDurableAgentBootstrapCeiling("telegram_group", core.DefaultTelegramGroupLivePolicy("Help the family group while escalating important issues.")),
		BootstrapLLM:       durableGroupTestBootstrapLLM(),
		PolicyVersion:      1,
		LocalStorageRoots:  []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")},
		NetworkPolicy:      "default",
		WakeupMode:         "telegram_update",
		Status:             "active",
	}
	if err := store.UpsertDurableAgent(agent); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     42,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "Set family-group to read only.",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	updated, err := store.DurableAgent("family-group")
	if err != nil {
		t.Fatalf("DurableAgent() err = %v", err)
	}
	if updated.LivePolicy.OutboundMode != "read_only" {
		t.Fatalf("updated outbound_mode = %q, want read_only", updated.LivePolicy.OutboundMode)
	}
	if updated.PolicyVersion != 2 {
		t.Fatalf("updated policy_version = %d, want 2", updated.PolicyVersion)
	}

	provider.mu.Lock()
	if !strings.Contains(provider.lastToolOutput, "action: durable-agent policy apply") {
		t.Fatalf("tool output = %q, want durable-agent policy apply output", provider.lastToolOutput)
	}
	provider.mu.Unlock()

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want progress + final reply", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].Text, "Working on Set family-group to read only") {
		t.Fatalf("progress text = %q, want conversation-derived durable_agent progress entry", sender.sent[0].Text)
	}
	if sender.sent[1].Text != "Policy updated through conversation." {
		t.Fatalf("final reply = %q, want conversational policy update reply", sender.sent[1].Text)
	}
}

func TestHandleInboundShowsToolProgressForActualToolCalls(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &multiToolRequestingProvider{}
	tools := &legacyRecordingTools{
		defs: []agent.ToolDef{testExecToolDef()},
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     503,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "inspect",
		MessageID:  99,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	sender.mu.Lock()
	if len(sender.sent) != 2 {
		t.Fatalf("sent len = %d, want 2 (progress + reply)", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].Text, "Working on it") {
		t.Fatalf("progress text = %q, want tool progress message", sender.sent[0].Text)
	}
	if !strings.Contains(sender.sent[0].Text, "Working on inspect") {
		t.Fatalf("progress text = %q, want task-derived progress label", sender.sent[0].Text)
	}
	if strings.Contains(sender.sent[0].Text, "rg first") {
		t.Fatalf("progress text = %q, want task-derived progress instead of raw command", sender.sent[0].Text)
	}
	if sender.sent[0].ReplyTo == nil || *sender.sent[0].ReplyTo != 99 {
		t.Fatalf("progress reply_to = %#v, want 99", sender.sent[0].ReplyTo)
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edit count = %d, want 1", len(sender.edits))
	}
	if !strings.Contains(sender.edits[0].Text, "Working on inspect (2x)") {
		t.Fatalf("edit text = %q, want aggregated task-derived tool progress", sender.edits[0].Text)
	}
	sender.mu.Unlock()

	run, err := store.LatestTurnRun(session.SessionKey{ChatID: 503, UserID: 0})
	if err != nil {
		t.Fatalf("LatestTurnRun() err = %v", err)
	}
	if run.Status != session.TurnRunStatusCompleted {
		t.Fatalf("turn run status = %q, want completed", run.Status)
	}
	if run.ToolCallsStarted != 2 {
		t.Fatalf("tool_calls_started = %d, want 2", run.ToolCallsStarted)
	}
	if run.ToolCallsFinished != 2 {
		t.Fatalf("tool_calls_finished = %d, want 2", run.ToolCallsFinished)
	}
	if run.LastToolResultPreview == "" {
		t.Fatal("last_tool_result_preview is empty, want persisted tool finish preview")
	}
	if run.ProgressMessageID != 1 {
		t.Fatalf("progress_message_id = %d, want 1", run.ProgressMessageID)
	}
}

func TestHandleInboundAdminFallsBackToLegacyToolsWhenPrincipalAwareNotReady(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &toolRequestingProvider{}
	tools := &principalRecordingTools{
		defs:              []agent.ToolDef{testExecToolDef()},
		supportsPrincipal: false,
	}

	rt, err := New(cfg, store, provider, tools, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	rt.faceBackend = face.BackendFloorFallback

	_, err = rt.HandleInbound(context.Background(), core.InboundMessage{
		ChatID:     503,
		SenderID:   1001,
		SenderName: "admin",
		Text:       "run pwd",
		MessageID:  1,
	})
	if err != nil {
		t.Fatalf("HandleInbound() err = %v", err)
	}

	if provider.firstToolCount != 1 {
		t.Fatalf("first tool count = %d, want 1", provider.firstToolCount)
	}
	if tools.executeCalls != 1 {
		t.Fatalf("legacy execute calls = %d, want 1", tools.executeCalls)
	}
	if tools.executeForPrincipalCalls != 0 {
		t.Fatalf("executeForPrincipal calls = %d, want 0", tools.executeForPrincipalCalls)
	}
}
