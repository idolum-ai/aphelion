//go:build linux

package runtime

import (
	"strings"
	"testing"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/config"
)

func TestShouldCompactUsesKnownFatToolAnticipatoryThreshold(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Governor.Codex.ContextWindow = 5000
	cfg.Sessions.MaxContextRatio = 0.90
	rt := &Runtime{cfg: &cfg, governorBackend: "codex"}

	fatOutput := strings.Repeat("x", anticipatoryCompactionToolChars+1000)
	history := []agent.Message{
		{Role: "user", Content: "inspect this"},
		{Role: "tool", ToolName: "fetch_url", Content: fatOutput},
		{Role: "assistant", Content: "fetched"},
	}
	if !rt.shouldCompact(nil, history, "", "") {
		t.Fatal("shouldCompact() = false, want anticipatory compaction for known fat tool output")
	}

	history[1].ToolName = "custom_dump"
	if rt.shouldCompact(nil, history, "", "") {
		t.Fatal("shouldCompact() = true for unknown tool, want no generic large-output anticipatory compaction")
	}
}
