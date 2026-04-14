//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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
