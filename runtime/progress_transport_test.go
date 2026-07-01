//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"strings"
	"testing"
)

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

func TestToolProgressReporterBackfillsEarlyProgressMessageWhenRunBinds(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7741, UserID: 0, Scope: telegramDMScopeRef(7741)}
	msg := core.InboundMessage{ChatID: 7741, ChatType: "private", MessageID: 11, Text: "wake child"}
	reporter := rt.newToolProgressReporter(key, msg, nil)
	if reporter == nil {
		t.Fatal("newToolProgressReporter() = nil")
	}
	reporter.Surface(context.Background(), "Starting child wake")

	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "wake child", reporter, nil, msg)
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	defer monitor.Finish(context.Background(), nil)
	run, err := store.TurnRun(monitor.runID)
	if err != nil {
		t.Fatalf("TurnRun() err = %v", err)
	}
	if run.ProgressMessageID != 1 {
		t.Fatalf("progress_message_id = %d, want previously sent message 1", run.ProgressMessageID)
	}
	sender.mu.Lock()
	sent := len(sender.sent)
	sender.mu.Unlock()
	if sent != 1 {
		t.Fatalf("sent progress messages = %d, want one early progress message", sent)
	}
}

func TestToolProgressReporterRecordsPreTurnAndBoundProgressPhases(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7742, UserID: 0, Scope: telegramDMScopeRef(7742)}
	msg := core.InboundMessage{ChatID: 7742, ChatType: "private", MessageID: 12, Text: "wake child"}
	reporter := rt.newToolProgressReporter(key, msg, nil)
	if reporter == nil {
		t.Fatal("newToolProgressReporter() = nil")
	}
	reporter.Surface(context.Background(), "Starting child wake")
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "wake child", reporter, nil, msg)
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	defer monitor.Finish(context.Background(), nil)
	reporter.Surface(context.Background(), "Child is running")

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	preTurn := progressSurfacePayloadForText(events, "Starting child wake")
	if preTurn == nil {
		t.Fatalf("events = %#v, missing pre-turn progress surface", events)
	}
	if got := payloadString(preTurn, "progress_phase"); got != "pre_turn" {
		t.Fatalf("pre-turn progress_phase = %q, want pre_turn", got)
	}
	if runID, ok := payloadInt64(preTurn, "run_id"); !ok || runID != 0 {
		t.Fatalf("pre-turn run_id = %d ok=%t, want 0", runID, ok)
	}
	turnBound := progressSurfacePayloadForText(events, "Child is running")
	if turnBound == nil {
		t.Fatalf("events = %#v, missing bound progress surface", events)
	}
	if got := payloadString(turnBound, "progress_phase"); got != "turn_bound" {
		t.Fatalf("bound progress_phase = %q, want turn_bound", got)
	}
	if runID, ok := payloadInt64(turnBound, "run_id"); !ok || runID != monitor.runID {
		t.Fatalf("bound run_id = %d ok=%t, want %d", runID, ok, monitor.runID)
	}
}

func TestToolProgressReporterRecordsDeliveryFailureDiagnostics(t *testing.T) {
	cfg, store, provider, _ := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, &fakeSender{})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7743, UserID: 0, Scope: telegramDMScopeRef(7743)}
	sender := &fakeSender{sendErr: errors.New("telegram sendMessage failed: chat unavailable")}
	reporter := &toolProgressReporter{
		runtime:      rt,
		executionKey: key,
		sender:       sender,
		chatID:       7743,
		mode:         "all",
		style:        "semantic",
		window:       4,
		seenKeys:     make(map[string]struct{}),
	}
	reporter.BindTurnRun(91)
	reporter.Surface(context.Background(), "Starting child wake")

	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	payload := payloadForEventType(events, core.ExecutionEventDeliveryProgressFailed)
	if payload == nil {
		t.Fatalf("events = %#v, want %s", events, core.ExecutionEventDeliveryProgressFailed)
	}
	if got := payloadString(payload, "method"); got != "send" {
		t.Fatalf("method = %q, want send", got)
	}
	if got := payloadString(payload, "progress_phase"); got != "turn_bound" {
		t.Fatalf("progress_phase = %q, want turn_bound", got)
	}
	if runID, ok := payloadInt64(payload, "run_id"); !ok || runID != 91 {
		t.Fatalf("run_id = %d ok=%t, want 91", runID, ok)
	}
	if got := payloadString(payload, "source_surface"); got != "outbound_transport_ledger" {
		t.Fatalf("source_surface = %q, want outbound_transport_ledger", got)
	}
}

func TestToolProgressReporterRecordsEditFailureDiagnostics(t *testing.T) {
	cfg, store, provider, _ := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, &fakeSender{})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7744, UserID: 0, Scope: telegramDMScopeRef(7744)}
	sender := &fakeSender{editErr: errors.New("telegram editMessageText failed: message is not modified")}
	reporter := &toolProgressReporter{
		runtime:      rt,
		executionKey: key,
		sender:       sender,
		editor:       sender,
		chatID:       7744,
		messageID:    44,
		mode:         "all",
		style:        "semantic",
		window:       4,
		seenKeys:     make(map[string]struct{}),
	}
	reporter.BindTurnRun(92)
	reporter.Surface(context.Background(), "Child is running")

	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	payload := payloadForEventType(events, core.ExecutionEventDeliveryProgressFailed)
	if payload == nil {
		t.Fatalf("events = %#v, want %s", events, core.ExecutionEventDeliveryProgressFailed)
	}
	if got := payloadString(payload, "method"); got != "edit_text" {
		t.Fatalf("method = %q, want edit_text", got)
	}
	if got := payloadString(payload, "progress_phase"); got != "turn_bound" {
		t.Fatalf("progress_phase = %q, want turn_bound", got)
	}
	if runID, ok := payloadInt64(payload, "run_id"); !ok || runID != 92 {
		t.Fatalf("run_id = %d ok=%t, want 92", runID, ok)
	}
	if chatID, ok := payloadInt64(payload, "chat_id"); !ok || chatID != 7744 {
		t.Fatalf("chat_id = %d ok=%t, want 7744", chatID, ok)
	}
}

func TestToolProgressReporterInlineEditPayloadCarriesRunID(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7712, UserID: 0, Scope: telegramDMScopeRef(7712)}
	reporter := &toolProgressReporter{
		runtime:        rt,
		executionKey:   key,
		sender:         sender,
		keyboardEditor: sender,
		chatID:         7712,
		messageID:      91,
		mode:           "all",
		style:          "raw",
		window:         4,
		controls:       deliberationControlRows(23, false),
		seenKeys:       make(map[string]struct{}),
	}
	reporter.BindTurnRun(23)
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"echo inline"}`))

	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	editPayload := payloadForEventType(events, core.ExecutionEventDeliveryProgressEdited)
	if editPayload == nil {
		t.Fatalf("missing %s event", core.ExecutionEventDeliveryProgressEdited)
	}
	if payloadString(editPayload, "method") != "edit_inline" {
		t.Fatalf("method = %q, want edit_inline", payloadString(editPayload, "method"))
	}
	runID, ok := payloadInt64(editPayload, "run_id")
	if !ok || runID != 23 {
		t.Fatalf("run_id = %d ok=%t payload=%#v, want 23", runID, ok, editPayload)
	}
	assertPayloadNonNegativeInt64(t, editPayload, "progress_delivery_duration_ms")
}

func TestToolProgressRecordsProgressSourceBranches(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7731, UserID: 0, Scope: telegramDMScopeRef(7731)}
	reporter := &toolProgressReporter{runtime: rt, executionKey: key, sender: sender, chatID: 7731, mode: "all", style: "semantic", window: 8, seenKeys: make(map[string]struct{}), currentPlanStep: "instrument progress source"}
	reporter.BindTurnRun(42)
	reporter.ToolStarted(context.Background(), "read_file", json.RawMessage(`{"path":"runtime/tool_progress_render.go"}`))
	reporter.ToolStarted(context.Background(), "update_plan", json.RawMessage(`{"plan":[{"step":"instrument progress source"}]}`))
	reporter.ToolStarted(context.Background(), "unknown_tool_with_step", json.RawMessage(`{"query":"secret-token-value"}`))
	reporter.currentPlanStep = ""
	reporter.ToolStarted(context.Background(), "unknown_tool_without_step", json.RawMessage(`{"query":"secret-token-value"}`))

	events, err := store.ExecutionEventsBySession(key, 0, 80)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	sources := progressSourcePayloads(events)
	for _, want := range []string{progressSourceTypedTool, progressSourceMetadataTool, progressSourcePlanStep, progressSourceGenericFallback} {
		if sources[want] == nil {
			t.Fatalf("progress sources = %#v, missing %s", sources, want)
		}
	}
	if label := payloadString(sources[progressSourceGenericFallback], "progress_label"); label != "Working through the request" {
		t.Fatalf("generic fallback label = %q, want Working through the request", label)
	}
	for source, payload := range sources {
		for _, leaked := range []string{"secret-token-value"} {
			if strings.Contains(payloadString(payload, "progress_label"), leaked) || strings.Contains(payloadString(payload, "progress_key"), leaked) {
				t.Fatalf("source %s leaked %q in payload %#v", source, leaked, payload)
			}
		}
	}
}

func progressSourcePayloads(events []session.ExecutionEvent) map[string]map[string]any {
	out := make(map[string]map[string]any)
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventProgressSurface {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		source := strings.TrimSpace(payloadString(payload, "progress_source"))
		if source != "" {
			out[source] = payload
		}
	}
	return out
}

func TestStatusSummarizesProgressSourceEvents(t *testing.T) {
	payload := map[string]any{"progress_source": "generic_fallback", "progress_label": "Working through the request", "tool": "unknown_tool"}
	got := summarizeExecutionEventPayload(core.ExecutionEventProgressSurface, "source", payload)
	for _, want := range []string{"source=generic_fallback", "label=Working through the request", "tool=unknown_tool"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, missing %q", got, want)
		}
	}
}

func TestRuntimeLockSessionDoesNotWriteExecutionEvent(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 7713, UserID: 0, Scope: telegramDMScopeRef(7713)}
	unlock := rt.lockSession(key)
	unlock()

	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events after lockSession = %#v, want no optional lock-path ledger write", events)
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

func progressSurfacePayloadForText(events []session.ExecutionEvent, text string) map[string]any {
	for _, event := range events {
		if strings.TrimSpace(event.EventType) != core.ExecutionEventProgressSurface {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			continue
		}
		if payloadString(payload, "text") == text {
			return payload
		}
	}
	return nil
}

func progressButtonLabels(rows [][]telegram.InlineButton) []string {
	out := make([]string, 0)
	for _, row := range rows {
		for _, button := range row {
			out = append(out, button.Text)
		}
	}
	return out
}
