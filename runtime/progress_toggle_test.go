//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"github.com/idolum-ai/aphelion/telegram"
	"strings"
	"testing"
	"time"
)

func TestRuntimeToggleProgressViewRerendersActiveProgressMessage(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{
		ChatID: 9931,
		UserID: 1002,
		Scope:  session.ScopeRef{Kind: session.ScopeKindDurableAgent, ID: "child-progress", DurableAgentID: "child-progress"},
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "inspect progress")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 77); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{EventType: core.ExecutionEventToolStarted, Stage: "tool", Status: "started", PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"exec","preview":"{\"command\":\"rg progress\"}"}`, run.ID), CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}
	decoyKey := session.SessionKey{ChatID: 9931, UserID: 0, Scope: telegramDMScopeRef(9931)}
	if _, err := store.AppendExecutionEvents(decoyKey, []session.ExecutionEventInput{{EventType: core.ExecutionEventToolStarted, Stage: "tool", Status: "started", PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"exec","preview":"{\"command\":\"wrong scope\"}"}`, run.ID), CreatedAt: time.Now().UTC()}}); err != nil {
		t.Fatalf("AppendExecutionEvents(decoy) err = %v", err)
	}

	updated, text, err := rt.ToggleProgressView(context.Background(), 9931, 1001, run.ID, true)
	if err != nil {
		t.Fatalf("ToggleProgressView() err = %v", err)
	}
	if !updated || !strings.Contains(text, "rg progress") {
		t.Fatalf("ToggleProgressView() updated=%t text=%q, want details render", updated, text)
	}
	if strings.Contains(text, "wrong scope") {
		t.Fatalf("ToggleProgressView() text=%q, used decoy default-scope progress event", text)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.editInline) != 1 || sender.editInline[0].MessageID != 77 {
		t.Fatalf("editInline = %#v, want edit of progress message 77", sender.editInline)
	}
	if got := progressButtonLabels(sender.editInline[0].Rows); !equalStringSlices(got, []string{"Reassess", "Summary", "Stop"}) {
		t.Fatalf("button labels = %#v, want Reassess/Summary/Stop", got)
	}
	events, err := store.ExecutionEventsBySession(key, 0, 20)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession(run key) err = %v", err)
	}
	payload := payloadForEventType(events, core.ExecutionEventDeliveryProgressEdited)
	runID, ok := payloadInt64(payload, "run_id")
	if payload == nil || payloadString(payload, "view") != "details" || !ok || runID != run.ID {
		t.Fatalf("progress edit payload = %#v, want details edit evidence under run scope", payload)
	}
}

func TestRuntimeToggleProgressViewUsesCurrentRunEventsInLongSession(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9935, UserID: 1001, Scope: telegramDMScopeRef(9935)}
	oldEvents := make([]session.ExecutionEventInput, 0, 650)
	now := time.Now().UTC()
	for i := 0; i < 650; i++ {
		oldEvents = append(oldEvents, session.ExecutionEventInput{
			EventType:   core.ExecutionEventToolStarted,
			Stage:       "tool",
			Status:      "started",
			PayloadJSON: fmt.Sprintf(`{"run_id":900001,"tool":"exec","preview":"{\"command\":\"old-%d\"}"}`, i),
			CreatedAt:   now.Add(time.Duration(i) * time.Millisecond),
		})
	}
	if _, err := store.AppendExecutionEvents(key, oldEvents); err != nil {
		t.Fatalf("AppendExecutionEvents(old) err = %v", err)
	}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "inspect long progress")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 177); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventToolStarted,
		Stage:       "tool",
		Status:      "started",
		PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"exec","preview":"{\"command\":\"rg live-progress\"}"}`, run.ID),
		CreatedAt:   now.Add(time.Second),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents(current) err = %v", err)
	}

	updated, text, err := rt.ToggleProgressView(context.Background(), 9935, 1001, run.ID, true)
	if err != nil {
		t.Fatalf("ToggleProgressView(details) err = %v", err)
	}
	if !updated || !strings.Contains(text, "rg live-progress") {
		t.Fatalf("details text = %q, want current-run detail despite long session history", text)
	}
	if strings.Contains(text, "No tool detail recorded yet.") || strings.Contains(text, "old-") {
		t.Fatalf("details text = %q, want current-run projection without stale or empty detail state", text)
	}
	state, ok, err := store.TurnProgressView(run.ID)
	if err != nil || !ok {
		t.Fatalf("TurnProgressView() = ok:%v err:%v, want selected view state", ok, err)
	}
	if state.SelectedView != session.TurnProgressViewDetails || !strings.Contains(state.DetailsText, "rg live-progress") {
		t.Fatalf("progress view state = %#v, want cached details selection", state)
	}
}

func TestRuntimeProgressDetailsViewRerendersWholeWindowAndSticks(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9933, UserID: 0, Scope: telegramDMScopeRef(9933)}
	progress := &toolProgressReporter{
		runtime:        rt,
		executionKey:   key,
		sender:         sender,
		inlineSender:   sender,
		keyboardEditor: sender,
		editor:         sender,
		chatID:         9933,
		mode:           "all",
		style:          "semantic",
		window:         4,
		seenKeys:       make(map[string]struct{}),
		validateText:   rt.filterProgressText,
	}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "inspect progress details", progress, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	defer monitor.Finish(context.Background(), nil)

	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"rg first"}`))
	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"sed -n '1,20p' runtime/progress.go"}`))
	sender.mu.Lock()
	inlineCount := len(sender.inline)
	initialText := ""
	if len(sender.editInline) > 0 {
		initialText = sender.editInline[len(sender.editInline)-1].Text
	} else if inlineCount > 0 {
		initialText = sender.inline[0].text
	}
	sender.mu.Unlock()
	if inlineCount != 1 {
		t.Fatalf("inline len = %d, want initial progress card", inlineCount)
	}
	if got := initialText; !strings.Contains(got, "Searching files") || !strings.Contains(got, "Reading file evidence") || strings.Contains(got, "rg first") {
		t.Fatalf("summary progress = %q, want semantic whole-window summary", got)
	}

	updated, detailsText, err := rt.ToggleProgressView(context.Background(), 9933, 1001, monitor.runID, true)
	if err != nil {
		t.Fatalf("ToggleProgressView(details) err = %v", err)
	}
	if !updated || !strings.Contains(detailsText, "rg first") || !strings.Contains(detailsText, "sed -n") || strings.Contains(detailsText, "Reading file evidence") {
		t.Fatalf("details text = %q, want retained entries rerendered as safe raw detail", detailsText)
	}
	sender.mu.Lock()
	detailsRows := [][]telegram.InlineButton(nil)
	if len(sender.editInline) > 0 {
		detailsRows = sender.editInline[len(sender.editInline)-1].Rows
	}
	sender.mu.Unlock()
	if got := progressButtonLabels(detailsRows); !equalStringSlices(got, []string{"Reassess", "Summary", "Stop"}) {
		t.Fatalf("details buttons = %#v, want Summary toggle", got)
	}

	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"go test ./runtime"}`))
	sender.mu.Lock()
	sticky := messageEditInline{}
	if len(sender.editInline) > 0 {
		sticky = sender.editInline[len(sender.editInline)-1]
	}
	sender.mu.Unlock()
	if !strings.Contains(sticky.Text, "rg first") || !strings.Contains(sticky.Text, "sed -n") || !strings.Contains(sticky.Text, "go test ./runtime") {
		t.Fatalf("sticky details text = %q, want prior and new entries in details mode", sticky.Text)
	}
	if strings.Contains(sticky.Text, "Searching files") || strings.Contains(sticky.Text, "Running tests") {
		t.Fatalf("sticky details text = %q, should not fall back to semantic labels", sticky.Text)
	}
	if got := progressButtonLabels(sticky.Rows); !equalStringSlices(got, []string{"Reassess", "Summary", "Stop"}) {
		t.Fatalf("sticky buttons = %#v, want Summary toggle", got)
	}

	updated, summaryText, err := rt.ToggleProgressView(context.Background(), 9933, 1001, monitor.runID, false)
	if err != nil {
		t.Fatalf("ToggleProgressView(summary) err = %v", err)
	}
	if !updated || !strings.Contains(summaryText, "Searching files") || !strings.Contains(summaryText, "Reading file evidence") || !strings.Contains(summaryText, "Running tests") {
		t.Fatalf("summary text = %q, want retained entries rerendered as semantic summary", summaryText)
	}
	if strings.Contains(summaryText, "rg first") || strings.Contains(summaryText, "sed -n") || strings.Contains(summaryText, "go test ./runtime") {
		t.Fatalf("summary text = %q, should not keep raw detail previews", summaryText)
	}

	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"git status --short"}`))
	sender.mu.Lock()
	restored := messageEditInline{}
	if len(sender.editInline) > 0 {
		restored = sender.editInline[len(sender.editInline)-1]
	}
	sender.mu.Unlock()
	if !strings.Contains(restored.Text, "Checking git status") || strings.Contains(restored.Text, "git status --short") {
		t.Fatalf("restored summary text = %q, want future edits in summary mode", restored.Text)
	}
	if got := progressButtonLabels(restored.Rows); !equalStringSlices(got, []string{"Reassess", "Details", "Stop"}) {
		t.Fatalf("restored buttons = %#v, want Details toggle", got)
	}
}

func TestRuntimeToggleProgressViewUsesCachedDetailsWhenProjectionIsEmpty(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9936, UserID: 1001, Scope: telegramDMScopeRef(9936)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "inspect cached progress")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 199); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}
	if err := store.SaveTurnProgressRender(run.ID, 199, session.TurnProgressViewSummary, "Working...\n- Searching files", "Working...\n- exec rg cached-details"); err != nil {
		t.Fatalf("SaveTurnProgressRender() err = %v", err)
	}

	updated, text, err := rt.ToggleProgressView(context.Background(), 9936, 1001, run.ID, true)
	if err != nil {
		t.Fatalf("ToggleProgressView(details cached) err = %v", err)
	}
	if !updated || !strings.Contains(text, "rg cached-details") {
		t.Fatalf("details text = %q, want cached detail render", text)
	}
	if strings.Contains(text, "No tool detail recorded yet.") {
		t.Fatalf("details text = %q, should not replace cached details with empty state", text)
	}
}

func TestRuntimeToggleProgressViewPrefixesTelegramThreadProgress(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9938, UserID: 1001, Scope: session.TelegramThreadScopeRef(9938, 6)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "inspect thread progress")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 206); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventToolStarted,
		Stage:       "tool",
		Status:      "started",
		PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"exec","preview":"{\"command\":\"rg thread-progress\"}"}`, run.ID),
		CreatedAt:   time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}

	updated, detailsText, err := rt.ToggleProgressView(context.Background(), 9938, 1001, run.ID, true)
	if err != nil {
		t.Fatalf("ToggleProgressView(details) err = %v", err)
	}
	if !updated || !strings.HasPrefix(detailsText, "(thread 6)\n\n") || strings.Count(detailsText, "(thread 6)") != 1 {
		t.Fatalf("details text = %q, want one visible thread prefix", detailsText)
	}
	updated, summaryText, err := rt.ToggleProgressView(context.Background(), 9938, 1001, run.ID, false)
	if err != nil {
		t.Fatalf("ToggleProgressView(summary) err = %v", err)
	}
	if !updated || !strings.HasPrefix(summaryText, "(thread 6)\n\n") || strings.Count(summaryText, "(thread 6)") != 1 {
		t.Fatalf("summary text = %q, want one visible thread prefix", summaryText)
	}
}

func TestRuntimeToggleProgressViewDoesNotPersistSelectionWhenEditFails(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9937, UserID: 1001, Scope: telegramDMScopeRef(9937)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "inspect failed progress edit")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 200); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}
	if err := store.SetTurnProgressSelectedView(run.ID, 200, session.TurnProgressViewSummary); err != nil {
		t.Fatalf("SetTurnProgressSelectedView(summary) err = %v", err)
	}
	if _, err := store.AppendExecutionEvents(key, []session.ExecutionEventInput{{
		EventType:   core.ExecutionEventToolStarted,
		Stage:       "tool",
		Status:      "started",
		PayloadJSON: fmt.Sprintf(`{"run_id":%d,"tool":"exec","preview":"{\"command\":\"rg failed-edit\"}"}`, run.ID),
		CreatedAt:   time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("AppendExecutionEvents() err = %v", err)
	}
	sender.editErr = errors.New("telegram edit failed")

	updated, _, err := rt.ToggleProgressView(context.Background(), 9937, 1001, run.ID, true)
	if err == nil {
		t.Fatal("ToggleProgressView(details) err = nil, want edit failure")
	}
	if updated {
		t.Fatal("ToggleProgressView(details) updated=true, want false on edit failure")
	}
	state, ok, err := store.TurnProgressView(run.ID)
	if err != nil || !ok {
		t.Fatalf("TurnProgressView() = ok:%v err:%v, want existing summary selection", ok, err)
	}
	if state.SelectedView != session.TurnProgressViewSummary {
		t.Fatalf("selected view = %q, want summary after failed details edit", state.SelectedView)
	}
}

func TestRuntimeToggleProgressViewDetailsShowsEmptyDetailState(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9934, UserID: 1001, Scope: telegramDMScopeRef(9934)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "wait for provider")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 99); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	updated, text, err := rt.ToggleProgressView(context.Background(), 9934, 1001, run.ID, true)
	if err != nil {
		t.Fatalf("ToggleProgressView(details empty) err = %v", err)
	}
	if !updated || !strings.Contains(text, "No tool detail recorded yet.") {
		t.Fatalf("details text = %q, want explicit empty detail state", text)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.editInline) != 1 {
		t.Fatalf("editInline len = %d, want details edit", len(sender.editInline))
	}
	if got := progressButtonLabels(sender.editInline[0].Rows); !equalStringSlices(got, []string{"Reassess", "Summary", "Stop"}) {
		t.Fatalf("details buttons = %#v, want Summary toggle", got)
	}
}

func TestRuntimeToggleProgressViewDetailsRequiresAdmin(t *testing.T) {
	t.Parallel()

	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}
	key := session.SessionKey{ChatID: 9932, UserID: 1002, Scope: telegramDMScopeRef(9932)}
	run, err := store.BeginTurnRun(key, session.TurnRunKindInteractive, "inspect progress")
	if err != nil {
		t.Fatalf("BeginTurnRun() err = %v", err)
	}
	if err := store.UpdateTurnRunProgressMessage(run.ID, 88); err != nil {
		t.Fatalf("UpdateTurnRunProgressMessage() err = %v", err)
	}

	updated, text, err := rt.ToggleProgressView(context.Background(), 9932, 1002, run.ID, true)
	if err != nil {
		t.Fatalf("ToggleProgressView(non-admin details) err = %v", err)
	}
	if updated || text != "" {
		t.Fatalf("ToggleProgressView(non-admin details) updated=%t text=%q, want denied no-op", updated, text)
	}
	sender.mu.Lock()
	editCount := len(sender.editInline)
	sender.mu.Unlock()
	if editCount != 0 {
		t.Fatalf("editInline count = %d, want no edit for non-admin details", editCount)
	}
}
