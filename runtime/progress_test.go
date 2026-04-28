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

func TestSummarizeProgressTaskDropsConversationalContinuation(t *testing.T) {
	t.Parallel()

	if got := summarizeProgressTask("then what happened?"); got != "" {
		t.Fatalf("summarizeProgressTask() = %q, want empty conversational continuation", got)
	}
	if got := summarizeProgressTask("inspect"); got != "inspect" {
		t.Fatalf("summarizeProgressTask(inspect) = %q, want inspect", got)
	}
}

func TestToolProgressReporterUsesGenericLabelForConversationalContinuation(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:      sender,
		editor:      sender,
		chatID:      42,
		mode:        "all",
		style:       "semantic",
		window:      4,
		seenKeys:    make(map[string]struct{}),
		taskSummary: summarizeProgressTask("then what happened?"),
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg first"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	got := sender.sent[0].Text
	if strings.Contains(got, "then what happened") {
		t.Fatalf("progress = %q, want no conversational prompt echo", got)
	}
	if !strings.Contains(got, "Working through the request") {
		t.Fatalf("progress = %q, want generic progress label", got)
	}
}

func TestToolProgressReporterSkipsDuplicateHeartbeatEdit(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:      sender,
		editor:      sender,
		chatID:      42,
		mode:        "all",
		style:       "semantic",
		window:      4,
		seenKeys:    make(map[string]struct{}),
		taskSummary: "inspect deploy logs",
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"journalctl --user -u aphelion"}`))
	reporter.Heartbeat(context.Background())

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want initial progress send", len(sender.sent))
	}
	if len(sender.edits) != 0 {
		t.Fatalf("edits len = %d, want duplicate heartbeat suppressed", len(sender.edits))
	}
}

func TestToolProgressReporterAggregatesSameSemanticTextAcrossTools(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:      sender,
		editor:      sender,
		chatID:      42,
		mode:        "all",
		style:       "semantic",
		window:      4,
		seenKeys:    make(map[string]struct{}),
		taskSummary: summarizeProgressTask("then what happened?"),
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg first"}`))
	reporter.ToolStarted(context.Background(), "semantic_search", json.RawMessage(`{"query":"first"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.edits) != 1 {
		t.Fatalf("edits len = %d, want 1", len(sender.edits))
	}
	got := sender.edits[0].Text
	if strings.Count(got, "Working through the request") != 1 || !strings.Contains(got, "Working through the request (2x)") {
		t.Fatalf("progress = %q, want one aggregated generic line", got)
	}
}

func TestNewToolProgressReporterDoesNotUsePersistedPlanForInitialLabel(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 6119, UserID: 0, Scope: telegramDMScopeRef(6119)}
	if err := store.UpdatePlanState(key, session.PlanState{
		Steps: []session.PlanStep{{
			Step:   "Restart aphelion.service through a durable logged runner and run verify-deploy",
			Status: session.PlanStatusInProgress,
		}},
	}); err != nil {
		t.Fatalf("UpdatePlanState() err = %v", err)
	}

	reporter := rt.newToolProgressReporter(key, core.InboundMessage{
		ChatID:    6119,
		ChatType:  "private",
		MessageID: 19,
		Text:      "Investigate progress rendering labels.",
	}, nil)
	if reporter == nil {
		t.Fatal("newToolProgressReporter() = nil, want reporter")
	}
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg progress"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want 1", len(sender.sent))
	}
	got := sender.sent[0].Text
	if strings.Contains(got, "Restart aphelion.service") {
		t.Fatalf("progress = %q, should not use persisted stale plan step", got)
	}
	if !strings.Contains(got, "Working on Investigate progress rendering labels") {
		t.Fatalf("progress = %q, want current request summary label", got)
	}
}

func TestToolProgressReporterAdoptsCurrentTurnPlanStepFromUpdatePlan(t *testing.T) {
	t.Parallel()

	sender := &fakeSender{}
	reporter := &toolProgressReporter{
		sender:      sender,
		editor:      sender,
		chatID:      42,
		mode:        "all",
		style:       "semantic",
		window:      4,
		seenKeys:    make(map[string]struct{}),
		taskSummary: "investigate progress rendering",
	}

	reporter.ToolStarted(context.Background(), "update_plan", json.RawMessage(`{
		"plan":[
			{"step":"Inspect progress rendering path","status":"in_progress"},
			{"step":"Patch reporter label source","status":"pending"}
		]
	}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg newToolProgressReporter"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.edits) != 1 {
		t.Fatalf("edits len = %d, want 1 progress edit", len(sender.edits))
	}
	got := sender.edits[0].Text
	if !strings.Contains(got, "Refining the plan for Inspect progress rendering path") {
		t.Fatalf("progress = %q, want update_plan to use current turn plan step", got)
	}
	if !strings.Contains(got, "Working on Inspect progress rendering path") {
		t.Fatalf("progress = %q, want later tool to use current turn plan step", got)
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
	if len(sender.edits) != 0 {
		t.Fatalf("edits len = %d, want 0 plain completion edits", len(sender.edits))
	}
	if len(sender.editClear) != 1 {
		t.Fatalf("editClear len = %d, want 1 completion edit clearing controls", len(sender.editClear))
	}
	if !strings.HasPrefix(sender.editClear[0].Text, "Done.") {
		t.Fatalf("completion text = %q, want Done heading", sender.editClear[0].Text)
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

func TestToolProgressReporterDropsInternalDeliberationSurface(t *testing.T) {
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
	reporter.BindTurnRun(205)
	reporter.Surface(context.Background(), "Center the next turn on curiosity without overbuilding: answer the user directly.")

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.inline) != 0 {
		t.Fatalf("inline len = %d, want no progress card for internal deliberation surface", len(sender.inline))
	}
}

func TestToolProgressReporterRenderUsesExecutionEventProjectionWhenAvailable(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9921, UserID: 0, Scope: telegramDMScopeRef(9921)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventTurnStarted,
			Stage:       "turn",
			Status:      "running",
			PayloadJSON: `{"run_id":7,"run_kind":"interactive"}`,
			CreatedAt:   now,
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"run_id":7,"tool":"exec","preview":"{\"command\":\"echo from-event\"}"}`,
			CreatedAt:   now.Add(time.Second),
		},
		{
			EventType:   core.ExecutionEventProgressSurface,
			Stage:       "progress",
			Status:      "active",
			PayloadJSON: `{"run_id":7,"text":"Surface from TES"}`,
			CreatedAt:   now.Add(2 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reporter := &toolProgressReporter{
		runtime:      rt,
		executionKey: key,
		chatID:       9921,
		mode:         "all",
		style:        "raw",
		window:       4,
		runID:        7,
		entries: []toolProgressEntry{
			{Key: "surface:local", Text: "Local-only stale entry", Count: 1},
		},
		seenKeys: make(map[string]struct{}),
	}

	got := reporter.renderLocked(false)
	if strings.Contains(got, "Local-only stale entry") {
		t.Fatalf("rendered progress = %q, do not want local stale entries when TES projection is available", got)
	}
	if !strings.Contains(got, "echo from-event") {
		t.Fatalf("rendered progress = %q, want tool preview from TES tool.started event", got)
	}
	if !strings.Contains(got, "Surface from TES") {
		t.Fatalf("rendered progress = %q, want surfaced text from TES progress.surface event", got)
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
		AgentID:            "child-alpha",
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
			ID:             "child-alpha",
			DurableAgentID: "child-alpha",
		},
	}, core.InboundMessage{
		ChatID:         1921139064,
		ChatType:       "durable_parent_conversation",
		MessageID:      55,
		DurableAgentID: "child-alpha",
		Text:           "internal durable wake",
	}, nil)
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
	}, nil)
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

func TestToolProgressReporterSuppressesDurableChildNoopOutboundErrors(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7722, UserID: 0, Scope: session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "child"}}
	noOpSender := &fakeSender{sendErr: errors.New("outbound delivery is unavailable in durable child mode")}
	var reported []string
	reporter := &toolProgressReporter{
		runtime:      rt,
		executionKey: key,
		sender:       noOpSender,
		reportIssue: func(_ context.Context, err error) {
			reported = append(reported, err.Error())
		},
		chatID:   42,
		mode:     "all",
		style:    "semantic",
		window:   4,
		seenKeys: make(map[string]struct{}),
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"echo child"}`))

	if len(reported) != 0 {
		t.Fatalf("reported = %#v, want suppressed expected durable child outbound error", reported)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if hasExecutionEvent(events, core.ExecutionEventDeliveryProgressFailed) {
		t.Fatalf("events = %#v, want no delivery.progress.failed for expected durable child outbound", events)
	}
}

func TestToolProgressReporterRecordsTransportLedgerSemantics(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7711, UserID: 0, Scope: telegramDMScopeRef(7711)}
	reporter := &toolProgressReporter{
		runtime:      rt,
		executionKey: key,
		sender:       sender,
		editor:       sender,
		chatID:       7711,
		mode:         "all",
		style:        "raw",
		window:       4,
		seenKeys:     make(map[string]struct{}),
	}
	reporter.BindTurnRun(17)
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"echo semantic-tags"}`))
	reporter.Finish(context.Background())

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	sentPayload := payloadForEventType(events, core.ExecutionEventDeliveryProgressSent)
	if sentPayload == nil {
		t.Fatalf("missing %s event", core.ExecutionEventDeliveryProgressSent)
	}
	if got := strings.TrimSpace(payloadString(sentPayload, "source_class")); got != "canonical" {
		t.Fatalf("source_class = %q, want canonical", got)
	}
	if got := strings.TrimSpace(payloadString(sentPayload, "source_surface")); got != "outbound_transport_ledger" {
		t.Fatalf("source_surface = %q, want outbound_transport_ledger", got)
	}
	if got := strings.TrimSpace(payloadString(sentPayload, "visibility")); got != "human_render_unknown" {
		t.Fatalf("visibility = %q, want human_render_unknown", got)
	}
}

func payloadForEventType(events []session.ExecutionEvent, eventType string) map[string]any {
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != strings.TrimSpace(eventType) {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			return nil
		}
		return payload
	}
	return nil
}
