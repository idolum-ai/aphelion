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
	tools := &directRecordingTools{
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
		t.Fatalf("direct execute calls = %d, want 0", tools.executeCalls)
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
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want 1 progress card", len(sender.inline))
	}
	if !strings.HasPrefix(sender.inline[0].text, "Working...") || strings.Contains(sender.inline[0].text, "Thinking") {
		t.Fatalf("progress text = %q, want non-reasoning progress header", sender.inline[0].text)
	}
	if strings.Contains(sender.inline[0].text, "Set family-group to read only") {
		t.Fatalf("progress text = %q, should not echo inbound task summary", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Working through the request") {
		t.Fatalf("progress text = %q, want generic fallback without user echo", sender.inline[0].text)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want final reply only", len(sender.sent))
	}
	if sender.sent[0].Text != "Policy updated through conversation." {
		t.Fatalf("final reply = %q, want conversational policy update reply", sender.sent[0].Text)
	}
}

func TestHandleInboundShowsToolProgressForActualToolCalls(t *testing.T) {
	t.Parallel()

	cfg, store, _, sender := buildRuntimeFixtures(t)
	provider := &multiToolRequestingProvider{}
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
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want 1 progress card", len(sender.inline))
	}
	if !strings.HasPrefix(sender.inline[0].text, "Working...") || strings.Contains(sender.inline[0].text, "Thinking") {
		t.Fatalf("progress text = %q, want non-reasoning progress header", sender.inline[0].text)
	}
	if !strings.Contains(sender.inline[0].text, "Exploring files") {
		t.Fatalf("progress text = %q, want evidence-surface progress label", sender.inline[0].text)
	}
	if strings.Contains(sender.inline[0].text, "rg first") {
		t.Fatalf("progress text = %q, want task-derived progress instead of raw command", sender.inline[0].text)
	}
	if sender.inline[0].replyTo == nil || *sender.inline[0].replyTo != 99 {
		t.Fatalf("progress reply_to = %#v, want 99", sender.inline[0].replyTo)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline count = %d, want 1", len(sender.editInline))
	}
	if !strings.Contains(sender.editInline[0].Text, "Exploring files (2x)") {
		t.Fatalf("edit text = %q, want aggregated evidence-surface progress", sender.editInline[0].Text)
	}
	if len(sender.edits) != 0 {
		t.Fatalf("final edit count = %d, want 0 plain completion edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("final clear edit count = %d, want 1 completion edit clearing controls", len(sender.editClear))
	}
	if !strings.HasPrefix(sender.editClear[0].Text, "Done.") {
		t.Fatalf("completion text = %q, want Done heading", sender.editClear[0].Text)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want final reply only", len(sender.sent))
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

func TestHandleInboundAdminDisablesToolsWhenPrincipalAwareNotReady(t *testing.T) {
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

	if provider.firstToolCount != 0 {
		t.Fatalf("first tool count = %d, want 0", provider.firstToolCount)
	}
	if tools.executeCalls != 0 {
		t.Fatalf("execute calls = %d, want 0", tools.executeCalls)
	}
	if tools.executeForPrincipalCalls != 0 {
		t.Fatalf("executeForPrincipal calls = %d, want 0", tools.executeForPrincipalCalls)
	}
}
