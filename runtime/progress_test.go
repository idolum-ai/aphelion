//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"strings"
	"testing"
	"time"
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

func TestToolProgressReporterSemanticWindowAggregatesToolIntent(t *testing.T) {
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

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg first"}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg second"}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg third"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.edits) == 0 {
		t.Fatal("expected edited progress message")
	}
	got := sender.edits[len(sender.edits)-1].Text
	if strings.Contains(got, "review the deployment changes") {
		t.Fatalf("progress = %q, should prefer tool intent over task summary", got)
	}
	if !strings.Contains(got, "Searching files (3x)") {
		t.Fatalf("progress = %q, want aggregated evidence-surface summary", got)
	}
}

func TestSemanticToolProgressLabel(t *testing.T) {
	t.Parallel()

	got := semanticToolProgressEntry("semantic_search", json.RawMessage(`{"query":"operator preference"}`), "review the runtime", "")
	if got.Text != "Searching memory" {
		t.Fatalf("semanticToolProgressEntry() = %q, want tool-intent label", got.Text)
	}
	unknown := semanticToolProgressEntry("custom_tool", json.RawMessage(`{"query":"operator preference"}`), "review the runtime", "")
	if unknown.Text != "Working on review the runtime" {
		t.Fatalf("unknown semanticToolProgressEntry() = %q, want plan-step fallback", unknown.Text)
	}
}

func TestSemanticToolProgressEvidenceSummaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input json.RawMessage
		want  string
	}{
		{name: "exec", input: json.RawMessage(`{"command":"rg provider runtime"}`), want: "Searching files"},
		{name: "exec", input: json.RawMessage(`{"command":"grep -R token ."}`), want: "Searching files"},
		{name: "exec", input: json.RawMessage(`{"command":"sed -n '1,80p' runtime/tool_progress_render.go"}`), want: "Reading file evidence"},
		{name: "exec", input: json.RawMessage(`{"command":"sqlite3 state.db 'select * from events'"}`), want: "Inspecting database"},
		{name: "exec", input: json.RawMessage(`{"command":"go test ./runtime"}`), want: "Running tests"},
		{name: "exec", input: json.RawMessage(`{"command":"git status --short --branch"}`), want: "Checking git status"},
		{name: "exec", input: json.RawMessage(`{"command":"git diff --check"}`), want: "Checking diff hygiene"},
		{name: "exec", input: json.RawMessage(`{"command":"git diff --stat"}`), want: "Reviewing git diff"},
		{name: "exec", input: json.RawMessage(`{"command":"journalctl --user -u aphelion.service"}`), want: "Reading service logs"},
		{name: "exec", input: json.RawMessage(`{"command":"systemctl --user status aphelion.service"}`), want: "Checking service status"},
		{name: "exec", input: json.RawMessage(`{"command":"ls -la runtime"}`), want: "Listing directory"},
		{name: "search", input: json.RawMessage(`{"query":"secret-token-value","path":"runtime"}`), want: "Searching files"},
		{name: "read_file", input: json.RawMessage(`{"path":"/tmp/secret-token-value"}`), want: "Reading file"},
		{name: "list_dir", input: json.RawMessage(`{"path":"/tmp/secret-token-value"}`), want: "Listing directory"},
		{name: "session_search", input: json.RawMessage(`{"query":"secret-token-value"}`), want: "Searching transcript history"},
		{name: "semantic_search", input: json.RawMessage(`{"query":"secret-token-value"}`), want: "Searching memory"},
	}

	for _, tc := range cases {
		got := semanticToolProgressEntry(tc.name, tc.input, "", "")
		if got.Text != tc.want {
			t.Fatalf("semanticToolProgressEntry(%s, %s) = %q, want %q", tc.name, string(tc.input), got.Text, tc.want)
		}
		if strings.Contains(got.Text, "secret-token-value") || strings.Contains(got.Text, "state.db") || strings.Contains(got.Text, "runtime/tool_progress_render.go") {
			t.Fatalf("semanticToolProgressEntry(%s) leaked sensitive/raw input in %q", tc.name, got.Text)
		}
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
	if !strings.Contains(got, "Searching files") {
		t.Fatalf("progress = %q, want evidence-surface progress label", got)
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

func TestToolProgressReporterPreservesDistinctToolIntentAcrossTools(t *testing.T) {
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
	if !strings.Contains(got, "Searching files") || !strings.Contains(got, "Searching memory") {
		t.Fatalf("progress = %q, want distinct evidence-surface lines", got)
	}
	if strings.Contains(got, "Working through the request") {
		t.Fatalf("progress = %q, should not collapse known tools into generic request label", got)
	}
}

func TestToolProgressReporterAggregatesRepeatedEvidenceCategoriesAcrossInterleaving(t *testing.T) {
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
		taskSummary: "inspect sensitive paths",
	}

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg sk-secret-do-not-leak /tmp/private-token-path"}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"sed -n '1,80p' /tmp/private-token-path"}`))
	reporter.ToolStarted(context.Background(), "semantic_search", json.RawMessage(`{"query":"private memory text"}`))
	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"grep -R another-secret /tmp/private-token-path"}`))
	reporter.ToolStarted(context.Background(), "search", json.RawMessage(`{"query":"third-secret","path":"/tmp/private-token-path"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.edits) == 0 {
		t.Fatal("expected progress edits")
	}
	got := sender.edits[len(sender.edits)-1].Text
	if strings.Count(got, "Searching files") != 1 || !strings.Contains(got, "Searching files (3x)") {
		t.Fatalf("progress = %q, want one aggregated Searching files category", got)
	}
	for _, leaked := range []string{"sk-secret-do-not-leak", "another-secret", "third-secret", "/tmp/private-token-path", "private memory text"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("progress leaked %q in %q", leaked, got)
		}
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
	if !strings.Contains(got, "Searching files") {
		t.Fatalf("progress = %q, want current evidence-surface label", got)
	}
}

func TestToolProgressReporterTreatsPlanUpdatesAsMetadata(t *testing.T) {
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
	reporter.ToolStarted(context.Background(), "update_operation", json.RawMessage(`{"stage":"implementation"}`))

	sender.mu.Lock()
	if len(sender.sent) != 0 || len(sender.edits) != 0 {
		t.Fatalf("progress sends=%d edits=%d, want no visible card for metadata-only updates", len(sender.sent), len(sender.edits))
	}
	sender.mu.Unlock()

	reporter.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg newToolProgressReporter"}`))

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.sent) != 1 {
		t.Fatalf("sent len = %d, want concrete tool to start progress card", len(sender.sent))
	}
	got := sender.sent[0].Text
	if strings.Contains(got, "Inspect progress rendering path") || strings.Contains(got, "Refining the plan") || strings.Contains(got, "Updating the operation") {
		t.Fatalf("progress = %q, should not surface metadata or sticky plan step", got)
	}
	if !strings.Contains(got, "Searching files") {
		t.Fatalf("progress = %q, want concrete evidence-surface label", got)
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
	if len(sender.inline[0].rows) != 1 || len(sender.inline[0].rows[0]) != 3 {
		t.Fatalf("inline rows = %#v, want reassess/details/stop controls", sender.inline[0].rows)
	}
	if got := progressButtonLabels(sender.inline[0].rows); !equalStringSlices(got, []string{"Reassess", "Details", "Stop"}) {
		t.Fatalf("inline button labels = %#v, want Reassess/Details/Stop", got)
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

func TestToolProgressReporterHeartbeatCanStartProgressCard(t *testing.T) {
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
		t.Fatalf("inline len = %d, want 1 heartbeat-started progress card", len(sender.inline))
	}
	if !strings.HasPrefix(sender.inline[0].text, "Working...") || strings.Contains(sender.inline[0].text, "Thinking") {
		t.Fatalf("inline text = %q, want non-reasoning progress heading", sender.inline[0].text)
	}
}

func TestToolProgressReporterSurfaceStartsProgressCard(t *testing.T) {
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
		t.Fatalf("inline len = %d, want 1 surfaced progress card", len(sender.inline))
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

func TestToolProgressReporterSemanticProjectionAggregatesAndRedactsToolEvents(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9922, UserID: 0, Scope: telegramDMScopeRef(9922)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"run_id":11,"tool":"exec","preview":"{\"command\":\"rg sk-secret /tmp/private-token-path\"}"}`,
			CreatedAt:   now,
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"run_id":11,"tool":"exec","preview":"{\"command\":\"sed -n '1,20p' /tmp/private-token-path\"}"}`,
			CreatedAt:   now.Add(time.Second),
		},
		{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: `{"run_id":11,"tool":"exec","preview":"{\"command\":\"grep -R bearer-secret /tmp/private-token-path\"}"}`,
			CreatedAt:   now.Add(2 * time.Second),
		},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	reporter := &toolProgressReporter{
		runtime:      rt,
		executionKey: key,
		chatID:       9922,
		mode:         "all",
		style:        "semantic",
		window:       4,
		runID:        11,
		seenKeys:     make(map[string]struct{}),
	}

	got := reporter.renderLocked(false)
	if strings.Count(got, "Exploring files") != 1 || !strings.Contains(got, "Exploring files (3x)") {
		t.Fatalf("rendered progress = %q, want compact file-exploration projection", got)
	}
	for _, leaked := range []string{"sk-secret", "bearer-secret", "/tmp/private-token-path"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("rendered progress leaked %q in %q", leaked, got)
		}
	}
}

func TestToolProgressReporterDetailsProjectionUsesSafeRawFromTES(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9930, UserID: 0, Scope: telegramDMScopeRef(9930)}
	now := time.Now().UTC()
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{
		{EventType: core.ExecutionEventToolStarted, Stage: "tool", Status: "started", PayloadJSON: `{"run_id":31,"tool":"exec","preview":"{\"command\":\"rg progress sk-secret /tmp/private-token-path\"}"}`, CreatedAt: now},
		{EventType: core.ExecutionEventToolStarted, Stage: "tool", Status: "started", PayloadJSON: `{"run_id":31,"tool":"search","preview":"{\"query\":\"secret-token-value\",\"path\":\"/tmp/private-token-path\"}"}`, CreatedAt: now.Add(time.Second)},
	}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}
	reporter := &toolProgressReporter{runtime: rt, executionKey: key, chatID: 9930, mode: "all", style: "semantic", window: 4, runID: 31, seenKeys: make(map[string]struct{})}

	summary := reporter.renderLockedWithDetails(false, false)
	if !strings.Contains(summary, "Exploring files") || strings.Contains(summary, "rg progress") || strings.Contains(summary, "private-token-path") {
		t.Fatalf("summary = %q, want semantic redacted summary", summary)
	}
	details := reporter.renderLockedWithDetails(false, true)
	if !strings.Contains(details, "exec") || !strings.Contains(details, "rg") {
		t.Fatalf("details = %q, want safe raw exec preview", details)
	}
	for _, leaked := range []string{"sk-secret", "secret-token-value", "/tmp/private-token-path"} {
		if strings.Contains(details, leaked) {
			t.Fatalf("details leaked %q in %q", leaked, details)
		}
	}
}
