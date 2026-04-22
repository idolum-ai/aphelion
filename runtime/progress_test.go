//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func TestToolProgressReporterRawStyleKeepsPreview(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:   sender,
		chatID:   42,
		mode:     "all",
		style:    "raw",
		window:   4,
		seenKeys: make(map[string]struct{}),
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg second"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	if !strings.Contains(sender.sent[0].Text, "rg second") {
		t.Fatalf("raw progress = %q, want command preview", sender.sent[0].Text)
	}
}

func TestToolProgressReporterSemanticWindowOmitsEarlierSteps(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:      sender,
		editor:      sender,
		chatID:      42,
		mode:        "all",
		style:       "semantic",
		window:      2,
		seenKeys:    make(map[string]struct{}),
		taskSummary: "review the deployment changes",
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"cat /tmp/config.toml"}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"go test ./..."}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"systemctl --user restart aphelion"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.edits) == 0 {
		t.Fatal("expected edited progress message")
	}
	got := sender.edits[len(sender.edits)-1].Text
	if !strings.Contains(got, "Working on review the deployment changes (3x)") {
		t.Fatalf("progress = %q, want aggregated task-derived semantic steps", got)
	}
}

func TestSemanticToolProgressLabel(t *testing.T) {
	t.Parallel()

	got := semanticToolProgressEntry("semantic_search", json.RawMessage(`{"query":"operator preference"}`), "review the runtime", "")
	if got.Text != "Working on review the runtime" {
		t.Fatalf("semanticToolProgressEntry() = %q, want task-derived label", got.Text)
	}
}

func TestToolProgressReporterDeliberationControlsLifecycle(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:         sender,
		inlineSender:   sender,
		editor:         sender,
		keyboardEditor: sender,
		chatID:         42,
		mode:           "all",
		style:          "semantic",
		window:         4,
		seenKeys:       make(map[string]struct{}),
		taskSummary:    "investigate stuck turn",
	}
	reporter.BindTurnRun(91)

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg first"}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg second"}`))
	reporter.Finish(context.Background())

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want 1 initial progress message with controls", len(sender.inline))
	}
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 2 {
		t.Fatalf("inline rows = %#v, want detach/stop controls", sender.inline[0].rows)
	}
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline len = %d, want 1 in-progress update retaining controls", len(sender.editInline))
	}
	if len(sender.edits) != 1 {
		t.Fatalf("edits len = %d, want 1 completion edit without controls", len(sender.edits))
	}
	if !strings.HasPrefix(sender.edits[0].Text, "Done.") {
		t.Fatalf("completion text = %q, want Done heading", sender.edits[0].Text)
	}
}

func TestToolProgressReporterHeartbeatCanStartThinkingCard(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:       sender,
		inlineSender: sender,
		chatID:       42,
		mode:         "all",
		style:        "semantic",
		window:       4,
		seenKeys:     make(map[string]struct{}),
	}
	reporter.BindTurnRun(109)
	reporter.Heartbeat(context.Background())

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want 1 heartbeat-started thinking card", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Thinking") {
		t.Fatalf("inline text = %q, want thinking heading", sender.inline[0].text)
	}
}

func TestToolProgressReporterSurfaceStartsThinkingCard(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:       sender,
		inlineSender: sender,
		chatID:       42,
		mode:         "all",
		style:        "semantic",
		window:       4,
		seenKeys:     make(map[string]struct{}),
	}
	reporter.BindTurnRun(204)
	reporter.Surface(context.Background(), "Starting the commit scan now.")

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 1 {
		t.Fatalf("inline len = %d, want 1 surfaced thinking card", len(sender.inline))
	}
	if !strings.Contains(sender.inline[0].text, "Starting the commit scan now.") {
		t.Fatalf("inline text = %q, want surfaced prose", sender.inline[0].text)
	}
}

func TestStartTurnMonitorRunActivityHeartbeatUpdatesLastActivity(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	previousInterval := turnRunActivityHeartbeatInterval
	turnRunActivityHeartbeatInterval = 10 * time.Millisecond
	defer func() {
		turnRunActivityHeartbeatInterval = previousInterval
	}()

	key := session.SessionKey{ChatID: 9911, UserID: 0, Scope: telegramDMScopeRef(9911)}
	monitor := rt.startTurnMonitor(key, session.TurnRunKindInteractive, "long provider request", nil, nil)
	if monitor.runID == 0 {
		t.Fatal("startTurnMonitor() did not create a turn run")
	}
	before, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(before) err = %v", err)
	}

	time.Sleep(35 * time.Millisecond)
	after, err := store.LatestTurnRun(key)
	if err != nil {
		t.Fatalf("LatestTurnRun(after) err = %v", err)
	}
	if !after.LastActivityAt.After(before.LastActivityAt) {
		t.Fatalf("last_activity_at = %s, want > %s", after.LastActivityAt.Format(time.RFC3339Nano), before.LastActivityAt.Format(time.RFC3339Nano))
	}

	monitor.Finish(context.Background(), nil)
}

func TestNewToolProgressReporterRoutesInternalDurableProgressToAdminChat(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	if err := store.UpsertDurableAgent(core.DurableAgent{
		AgentID:            "idolum-email",
		ReviewTargetChatID: 1001,
		ChannelKind:        "headless",
		BootstrapLLM:       durableGroupTestBootstrapLLM(),
		Status:             "active",
	}); err != nil {
		t.Fatalf("UpsertDurableAgent() err = %v", err)
	}

	reporter := rt.newToolProgressReporter(session.SessionKey{
		ChatID: 1921139064,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             "idolum-email",
			DurableAgentID: "idolum-email",
		},
	}, core.InboundMessage{
		ChatID:         1921139064,
		ChatType:       "durable_parent_conversation",
		MessageID:      55,
		DurableAgentID: "idolum-email",
		Text:           "internal durable wake",
	}, session.PlanState{}, nil)
	if reporter == nil {
		t.Fatal("newToolProgressReporter() = nil, want reporter")
	}
	if reporter.chatID != 1001 {
		t.Fatalf("reporter.chatID = %d, want admin review chat 1001", reporter.chatID)
	}
	if reporter.replyTo != nil {
		t.Fatalf("reporter.replyTo = %#v, want nil for relayed internal progress", reporter.replyTo)
	}
	reporter.BindTurnRun(7)
	if len(reporter.controls) != 0 {
		t.Fatalf("reporter.controls = %#v, want no controls for relayed internal progress", reporter.controls)
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"echo hello"}`))
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sender.sent len = %d, want 1", len(sender.sent))
	}
	if sender.sent[0].ChatID != 1001 {
		t.Fatalf("sender.sent chat_id = %d, want admin review chat 1001", sender.sent[0].ChatID)
	}
}

func TestNewToolProgressReporterKeepsTelegramChatTarget(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	reporter := rt.newToolProgressReporter(session.SessionKey{
		ChatID: 6313146,
		UserID: 0,
		Scope:  telegramDMScopeRef(6313146),
	}, core.InboundMessage{
		ChatID:    6313146,
		ChatType:  "private",
		MessageID: 77,
		Text:      "normal telegram chat",
	}, session.PlanState{}, nil)
	if reporter == nil {
		t.Fatal("newToolProgressReporter() = nil, want reporter")
	}
	if reporter.chatID != 6313146 {
		t.Fatalf("reporter.chatID = %d, want 6313146", reporter.chatID)
	}
	if reporter.replyTo == nil || *reporter.replyTo != 77 {
		t.Fatalf("reporter.replyTo = %#v, want pointer to 77", reporter.replyTo)
	}
}

func TestToolProgressReporterReportsSendErrors(t *testing.T) {
	sender := &fakeSender{sendErr: errors.New("telegram sendMessage failed: chat not found")}
	var reported []string
	reporter := &toolProgressReporter{
		sender: sender,
		reportIssue: func(_ context.Context, err error) {
			reported = append(reported, err.Error())
		},
		chatID:   42,
		mode:     "all",
		style:    "semantic",
		window:   4,
		seenKeys: make(map[string]struct{}),
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"echo fail"}`))

	if len(reported) != 1 {
		t.Fatalf("reported len = %d, want 1", len(reported))
	}
	if !strings.Contains(reported[0], "send tool progress chat_id=42") {
		t.Fatalf("reported[0] = %q, want send tool progress context", reported[0])
	}
}
