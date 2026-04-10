//go:build linux

package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSemanticEngineSearchFindsCuratedMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSemanticFile(t, filepath.Join(root, "MEMORY.md"), "# MEMORY.md\n\nOperator prefers concise progress updates during long tasks.")
	writeSemanticFile(t, filepath.Join(root, "memory", "knowledge.md"), "# knowledge.md\n\n- Prefers concise progress updates [observed, confidence: 0.90]")
	writeSemanticFile(t, filepath.Join(root, "memory", "decisions.md"), "# decisions.md\n\n- Use heartbeat reflection to preserve durable memory.")

	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		Sources:             []string{"MEMORY.md", "memory/knowledge.md", "memory/decisions.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "brief progress updates",
		Mode:  SemanticModeInteractive,
		Now:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned no hits, want curated-memory result")
	}
	if hits[0].Kind != "knowledge" && hits[0].Kind != "memory" {
		t.Fatalf("top hit = %#v, want knowledge or memory", hits[0])
	}
	if !strings.Contains(strings.ToLower(hits[0].Excerpt), "concise progress updates") {
		t.Fatalf("top hit excerpt = %q, want progress-updates content", hits[0].Excerpt)
	}
}

func TestSemanticEngineHeartbeatIncludesRecentDailyNotes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeSemanticFile(t, filepath.Join(root, "memory", "daily", "2026-04-10.md"), "Need to preserve the recurring preference for concise updates.")
	writeSemanticFile(t, filepath.Join(root, "memory", "knowledge.md"), "# knowledge.md\n\n- Prefers concise updates [observed]")

	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		Sources:             []string{"memory/knowledge.md"},
		IncludeDailyNotes:   true,
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "recurring concise updates",
		Mode:  SemanticModeHeartbeat,
		Now:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned no hits, want heartbeat semantic recall")
	}
	foundDaily := false
	for _, hit := range hits {
		if hit.Kind == "daily_note" {
			foundDaily = true
			break
		}
	}
	if !foundDaily {
		t.Fatalf("hits = %#v, want daily_note hit", hits)
	}
}

func writeSemanticFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) err = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) err = %v", path, err)
	}
}
