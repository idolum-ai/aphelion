//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClassifyExecCommandSemanticLabels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "memory write", command: "cat > /tmp/memory/knowledge.md <<'EOF'", want: "Writing memory files"},
		{name: "config read", command: "cat /home/user/.aphelion/aphelion.toml", want: "Inspecting config"},
		{name: "service restart", command: "systemctl --user restart aphelion", want: "Restarting service"},
		{name: "tests", command: "go test ./...", want: "Running tests"},
		{name: "git inspect", command: "git status --short", want: "Inspecting git state"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyExecCommand(tc.command)
			if got.Text != tc.want {
				t.Fatalf("classifyExecCommand(%q) = %q, want %q", tc.command, got.Text, tc.want)
			}
		})
	}
}

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
		sender:   sender,
		editor:   sender,
		chatID:   42,
		mode:     "all",
		style:    "semantic",
		window:   2,
		seenKeys: make(map[string]struct{}),
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
	if !strings.Contains(got, "1 earlier steps omitted.") {
		t.Fatalf("progress = %q, want omission line", got)
	}
	if !strings.Contains(got, "Running tests") || !strings.Contains(got, "Restarting service") {
		t.Fatalf("progress = %q, want last two semantic steps", got)
	}
}
