//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

func TestSeedAgentPromptFilesSeedsStructuredMemoryFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			PromptRoot:       filepath.Join(root, "agent"),
			SharedMemoryRoot: filepath.Join(root, "agent"),
			DailyNotes:       true,
			DailyNotesDir:    "memory/daily",
		},
	}

	created, err := seedAgentPromptFiles(cfg)
	if err != nil {
		t.Fatalf("seedAgentPromptFiles() err = %v", err)
	}
	if len(created) == 0 {
		t.Fatal("seedAgentPromptFiles() created no files, want defaults")
	}

	for _, rel := range []string{
		"SOUL.md",
		"IDENTITY.md",
		"USER.md",
		"AGENTS.md",
		"TOOLS.md",
		"IDOLUM.md",
		"QUESTIONS-TO-IDOLUM.md",
		"MEMORY.md",
		"HEARTBEAT.md",
		filepath.Join("memory", "knowledge.md"),
		filepath.Join("memory", "decisions.md"),
		filepath.Join("memory", "questions.md"),
		filepath.Join("memory", "rhizome.md"),
	} {
		path := filepath.Join(cfg.Agent.PromptRoot, rel)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("Stat(%s) err = %v", path, err)
		}
	}
}

func TestClearSharedDynamicMemoryPreservesIdentitySectionInMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			SharedMemoryRoot: root,
			DynamicFiles:     []string{"MEMORY.md", "HEARTBEAT.md"},
			DailyNotes:       true,
			DailyNotesDir:    "memory",
		},
	}

	memoryRaw := `# MEMORY.md — Shared Curated Memory

<!-- APHELION:IDENTITY-BEGIN -->
## Identity-Bearing Continuity

- This should survive resets.
<!-- APHELION:IDENTITY-END -->

## Operational Notes

- This should be cleared.
`
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(memoryRaw), 0o600); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "HEARTBEAT.md"), []byte("temporary"), 0o600); err != nil {
		t.Fatalf("WriteFile(HEARTBEAT.md) err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "knowledge.md"), []byte("clear me"), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge.md) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory", "2026-04-10.md"), []byte("daily note"), 0o600); err != nil {
		t.Fatalf("WriteFile(daily note) err = %v", err)
	}

	removed, err := clearSharedDynamicMemory(cfg)
	if err != nil {
		t.Fatalf("clearSharedDynamicMemory() err = %v", err)
	}
	if removed == 0 {
		t.Fatal("clearSharedDynamicMemory() removed 0 paths, want preserved/cleared entries")
	}

	raw, err := os.ReadFile(filepath.Join(root, "MEMORY.md"))
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "This should survive resets.") {
		t.Fatalf("MEMORY.md = %q, want preserved identity block", text)
	}
	if strings.Contains(text, "This should be cleared.") {
		t.Fatalf("MEMORY.md = %q, want non-identity content removed", text)
	}
	if _, err := os.Stat(filepath.Join(root, "HEARTBEAT.md")); !os.IsNotExist(err) {
		t.Fatalf("HEARTBEAT.md still exists, want removed; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "knowledge.md")); !os.IsNotExist(err) {
		t.Fatalf("knowledge.md still exists, want removed; err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "2026-04-10.md")); !os.IsNotExist(err) {
		t.Fatalf("daily note still exists, want removed; err=%v", err)
	}
}

func TestArchiveColdDailyNotesMovesOldNotesIntoArchive(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			SharedMemoryRoot: root,
			UserMemoryRoot:   filepath.Join(root, "users"),
			DailyNotesDir:    "memory/daily",
		},
		Memory: config.MemoryConfig{
			Decay: config.MemoryDecayConfig{
				Enabled:  true,
				HotDays:  3,
				WarmDays: 7,
				ColdDays: 30,
			},
		},
	}

	noteRoot := filepath.Join(root, "memory", "daily")
	if err := os.MkdirAll(noteRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(noteRoot) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteRoot, "2026-01-01.md"), []byte("old note"), 0o600); err != nil {
		t.Fatalf("WriteFile(old note) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(noteRoot, "2026-04-09.md"), []byte("recent note"), 0o600); err != nil {
		t.Fatalf("WriteFile(recent note) err = %v", err)
	}

	archived, err := archiveColdDailyNotes(cfg, time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("archiveColdDailyNotes() err = %v", err)
	}
	if archived != 1 {
		t.Fatalf("archived = %d, want 1", archived)
	}

	if _, err := os.Stat(filepath.Join(root, "memory", "daily", "2026-01-01.md")); !os.IsNotExist(err) {
		t.Fatalf("old note still active, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "archive", "daily", "2026-01-01.md")); err != nil {
		t.Fatalf("archived note missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "memory", "daily", "2026-04-09.md")); err != nil {
		t.Fatalf("recent note missing: %v", err)
	}
}

func TestArchiveOversizedCuratedMemoryArchivesAndCompacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := &config.Config{
		Agent: config.AgentConfig{
			SharedMemoryRoot: root,
			UserMemoryRoot:   filepath.Join(root, "users"),
		},
		Memory: config.MemoryConfig{
			Decay: config.MemoryDecayConfig{Enabled: true},
		},
	}

	path := filepath.Join(root, "memory", "knowledge.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(path, []byte("# knowledge.md\n\n"+strings.Repeat("- fact worth keeping around for a long time\n\n", 500)), 0o600); err != nil {
		t.Fatalf("WriteFile(knowledge.md) err = %v", err)
	}

	archived, err := archiveOversizedCuratedMemory(cfg, time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("archiveOversizedCuratedMemory() err = %v", err)
	}
	if archived != 1 {
		t.Fatalf("archived = %d, want 1", archived)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(knowledge.md) err = %v", err)
	}
	if !strings.Contains(string(raw), "Excerpted for prompt efficiency") {
		t.Fatalf("knowledge.md = %q, want compacted excerpt", string(raw))
	}

	archiveEntries, err := os.ReadDir(filepath.Join(root, "memory", "archive"))
	if err != nil {
		t.Fatalf("ReadDir(archive) err = %v", err)
	}
	if len(archiveEntries) != 1 {
		t.Fatalf("archive entries = %d, want 1", len(archiveEntries))
	}
}
