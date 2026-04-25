//go:build linux

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func TestLoadPromptContextLoadsConfiguredFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "agent rules")
	writeFile(t, filepath.Join(root, "memory", "knowledge.md"), "# knowledge.md\n\n- durable fact")
	writeFile(t, filepath.Join(root, "memory", "decisions.md"), "# decisions.md\n\n- durable decision")
	writeFile(t, filepath.Join(root, "HEARTBEAT.md"), "check inbox")
	writeFile(t, filepath.Join(root, "memory", "daily", "2026-04-08.md"), "today note")

	ctx, err := LoadPromptContext(config.AgentConfig{
		Workspace:              root,
		BootstrapFiles:         []string{"AGENTS.md"},
		DynamicFiles:           []string{"MEMORY.md", "HEARTBEAT.md"},
		BootstrapMaxChars:      1000,
		BootstrapTotalMaxChars: 1000,
		DailyNotes:             true,
		DailyNotesDir:          "memory/daily",
	}, time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadPromptContext() err = %v", err)
	}

	if len(ctx.Stable) != 1 || ctx.Stable[0].Path != "AGENTS.md" {
		t.Fatalf("stable = %#v", ctx.Stable)
	}
	if len(ctx.Dynamic) != 4 {
		t.Fatalf("dynamic len = %d, want 4", len(ctx.Dynamic))
	}
	prompt := ctx.Render("base instruction")
	if !strings.Contains(prompt, "AGENTS.md") || !strings.Contains(prompt, "HEARTBEAT.md") || !strings.Contains(prompt, "memory/daily/2026-04-08.md") {
		t.Fatalf("prompt = %q, want injected files", prompt)
	}
	if !strings.Contains(prompt, "memory/knowledge.md") || !strings.Contains(prompt, "memory/decisions.md") {
		t.Fatalf("prompt = %q, want structured memory auto-loaded", prompt)
	}
}

func TestLoadPromptContextFallsBackToLowercaseMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "memory.md"), "lowercase memory")

	ctx, err := LoadPromptContext(config.AgentConfig{
		Workspace:              root,
		DynamicFiles:           []string{"MEMORY.md"},
		BootstrapMaxChars:      1000,
		BootstrapTotalMaxChars: 1000,
		DailyNotes:             false,
		DailyNotesDir:          "memory/daily",
	}, time.Now())
	if err != nil {
		t.Fatalf("LoadPromptContext() err = %v", err)
	}

	if len(ctx.Dynamic) != 1 || ctx.Dynamic[0].Path != "memory.md" {
		t.Fatalf("dynamic = %#v, want lowercase fallback", ctx.Dynamic)
	}
}

func TestLoadPromptContextAppliesSizeCaps(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("a", 40))
	writeFile(t, filepath.Join(root, "SOUL.md"), strings.Repeat("b", 40))

	ctx, err := LoadPromptContext(config.AgentConfig{
		Workspace:              root,
		BootstrapFiles:         []string{"AGENTS.md", "SOUL.md"},
		BootstrapMaxChars:      25,
		BootstrapTotalMaxChars: 30,
		DailyNotes:             false,
		DailyNotesDir:          "memory/daily",
	}, time.Now())
	if err != nil {
		t.Fatalf("LoadPromptContext() err = %v", err)
	}

	if len(ctx.Stable) != 2 {
		t.Fatalf("stable len = %d, want 2", len(ctx.Stable))
	}
	if !ctx.Stable[0].Truncated {
		t.Fatal("first file should be truncated")
	}
	if ctx.Stable[1].Content == "" || len(ctx.Stable[1].Content) > 30 {
		t.Fatalf("second file content = %q, want truncated by remaining budget", ctx.Stable[1].Content)
	}
}

func TestLoadPromptContextRejectsEscapes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, err := LoadPromptContext(config.AgentConfig{
		Workspace:              root,
		BootstrapFiles:         []string{"../outside.md"},
		BootstrapMaxChars:      1000,
		BootstrapTotalMaxChars: 1000,
		DailyNotes:             false,
		DailyNotesDir:          "memory/daily",
	}, time.Now())
	if err == nil {
		t.Fatal("LoadPromptContext() err = nil, want path escape rejection")
	}
}

func TestLoadPromptContextCompactsLargeMemoryAndStructuredFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "MEMORY.md"), strings.Join([]string{
		"# MEMORY.md",
		"",
		"Intro paragraph that should stay visible.",
		"",
		"<!-- APHELION:IDENTITY-BEGIN -->",
		"## Identity",
		"- keep this continuity",
		"<!-- APHELION:IDENTITY-END -->",
		"",
		"## Operational Notes",
		strings.Repeat("long operational note\n", 40),
	}, "\n"))
	writeFile(t, filepath.Join(root, "memory", "knowledge.md"), "# knowledge.md\n\n"+strings.Repeat("- fact worth recalling\n\n", 24))

	ctx, err := LoadPromptContext(config.AgentConfig{
		Workspace:              root,
		DynamicFiles:           []string{"MEMORY.md"},
		BootstrapMaxChars:      400,
		BootstrapTotalMaxChars: 1200,
		DailyNotes:             false,
		DailyNotesDir:          "memory/daily",
	}, time.Now())
	if err != nil {
		t.Fatalf("LoadPromptContext() err = %v", err)
	}

	rendered := ctx.Render("")
	if !strings.Contains(rendered, "keep this continuity") {
		t.Fatalf("prompt = %q, want identity-bearing continuity preserved", rendered)
	}
	if !strings.Contains(rendered, "Excerpted for prompt efficiency") {
		t.Fatalf("prompt = %q, want excerpt marker", rendered)
	}
	if !strings.Contains(rendered, "memory/knowledge.md") {
		t.Fatalf("prompt = %q, want structured memory included", rendered)
	}
}

func TestLoadPromptContextStripsMemoryInstrumentation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "memory", "knowledge.md"), strings.Join([]string{
		"<!-- aphelion-memory-file:v1",
		"scope: shared",
		"store: knowledge",
		"-->",
		"",
		"<!-- aphelion-memory-entry:v1",
		"id: mem_test",
		"scope: shared",
		"store: knowledge",
		"-->",
		"",
		"- durable fact",
	}, "\n"))

	ctx, err := LoadPromptContext(config.AgentConfig{
		Workspace:              root,
		DynamicFiles:           []string{"memory/knowledge.md"},
		BootstrapMaxChars:      1000,
		BootstrapTotalMaxChars: 1000,
		DailyNotes:             false,
		DailyNotesDir:          "memory/daily",
	}, time.Now())
	if err != nil {
		t.Fatalf("LoadPromptContext() err = %v", err)
	}
	rendered := ctx.Render("")
	if strings.Contains(rendered, "aphelion-memory-entry") || !strings.Contains(rendered, "- durable fact") {
		t.Fatalf("prompt = %q, want stripped instrumentation and retained content", rendered)
	}
}

func TestLoadPromptContextAutoLoadsSkillsAndIndexedPractices(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "SKILLS.md"), strings.Join([]string{
		"# Skills",
		"",
		"- [Commit Archaeology](practices/commit-archeology.md) - reconstruct commit intent.",
	}, "\n"))
	writeFile(t, filepath.Join(root, "practices", "commit-archeology.md"), "# Commit Archaeology\n\nUse Typst and evidence-first pages.")

	ctx, err := LoadPromptContext(config.AgentConfig{
		Workspace:              root,
		BootstrapMaxChars:      1000,
		BootstrapTotalMaxChars: 3000,
		DailyNotes:             false,
		DailyNotesDir:          "memory/daily",
	}, time.Now())
	if err != nil {
		t.Fatalf("LoadPromptContext() err = %v", err)
	}

	rendered := ctx.Render("")
	if !strings.Contains(rendered, "### SKILLS.md") {
		t.Fatalf("prompt = %q, want auto-loaded skills index", rendered)
	}
	if !strings.Contains(rendered, "### practices/commit-archeology.md") {
		t.Fatalf("prompt = %q, want practice referenced from SKILLS.md", rendered)
	}
	if !strings.Contains(rendered, "Use Typst and evidence-first pages.") {
		t.Fatalf("prompt = %q, want indexed practice content", rendered)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
