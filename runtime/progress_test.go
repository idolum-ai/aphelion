//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
