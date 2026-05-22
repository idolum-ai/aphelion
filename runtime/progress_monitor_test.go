//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
	"testing"
	"time"
)

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
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "long provider request", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
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

func TestTurnMonitorToolAndTurnDurationsAreLedgered(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9912, UserID: 0, Scope: telegramDMScopeRef(9912)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "duration test", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	monitor.ToolStarted(context.Background(), "exec", json.RawMessage(`{"command":"true"}`))
	monitor.ToolFinished(context.Background(), "exec", json.RawMessage(`{"command":"true"}`), "ok", nil)
	monitor.Finish(context.Background(), nil)

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	assertPayloadNonNegativeInt64(t, payloadForEventType(events, core.ExecutionEventToolSucceeded), "tool_duration_ms")
	assertPayloadNonNegativeInt64(t, payloadForEventType(events, core.ExecutionEventTurnCompleted), "turn_duration_ms")
}

func TestTurnMonitorRecordsModelAndToolBatchEvents(t *testing.T) {
	cfg, store, provider, sender := buildRuntimeFixtures(t)
	rt, err := New(cfg, store, provider, nil, sender)
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	key := session.SessionKey{ChatID: 9914, UserID: 0, Scope: telegramDMScopeRef(9914)}
	monitor, err := rt.startTurnMonitor(context.Background(), key, session.TurnRunKindInteractive, "batch evidence", nil, nil, core.InboundMessage{})
	if err != nil {
		t.Fatalf("startTurnMonitor() err = %v", err)
	}
	modelEvent := agent.ModelRequestEvent{
		Attempt:       2,
		HistoryCount:  7,
		ToolCount:     3,
		Duration:      2 * time.Millisecond,
		ToolCallCount: 2,
		OutputChars:   11,
		TokenUsage:    core.TokenUsage{InputTokens: 13, OutputTokens: 5, TotalTokens: 18},
	}
	monitor.ModelRequestStarted(context.Background(), modelEvent)
	monitor.ModelRequestFinished(context.Background(), modelEvent)
	batchEvent := agent.ToolBatchEvent{
		Mode:        "parallel",
		BatchSize:   2,
		ToolNames:   []string{"read_file", "search"},
		Duration:    3 * time.Millisecond,
		FailedCount: 1,
	}
	monitor.ToolBatchStarted(context.Background(), batchEvent)
	monitor.ToolBatchFinished(context.Background(), batchEvent)
	monitor.Finish(context.Background(), nil)

	events, err := store.ExecutionEventsBySession(key, 0, 50)
	if err != nil {
		t.Fatalf("ExecutionEventsBySession() err = %v", err)
	}
	modelPayload := payloadForEventType(events, core.ExecutionEventModelRequestSucceeded)
	if got, ok := payloadInt64(modelPayload, "attempt"); !ok || got != 2 {
		t.Fatalf("model attempt payload = %#v, want attempt 2", modelPayload)
	}
	if got, ok := payloadInt64(modelPayload, "total_tokens"); !ok || got != 18 {
		t.Fatalf("model token payload = %#v, want total_tokens 18", modelPayload)
	}
	batchPayload := payloadForEventType(events, core.ExecutionEventToolBatchCompleted)
	if payloadString(batchPayload, "mode") != "parallel" {
		t.Fatalf("tool batch payload = %#v, want parallel mode", batchPayload)
	}
	if got, ok := payloadInt64(batchPayload, "failed_count"); !ok || got != 1 {
		t.Fatalf("tool batch failed_count payload = %#v, want 1", batchPayload)
	}
	if got := payloadStringSlice(batchPayload, "tools"); len(got) != 2 || got[0] != "read_file" || got[1] != "search" {
		t.Fatalf("tool batch tools payload = %#v, want read_file/search", batchPayload)
	}
}
