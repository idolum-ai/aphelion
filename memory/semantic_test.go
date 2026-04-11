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
		DBPath:              filepath.Join(root, "semantic.db"),
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
		DBPath:              filepath.Join(root, "semantic.db"),
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

func TestSemanticEngineExcludesQuarantinedImportsFromSearch(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	if _, err := engine.ImportDocument(context.Background(), SemanticImportRequest{
		Scope:            "shared",
		SourcePath:       "imports/openclaw/knowledge.md",
		SourceKind:       "knowledge",
		SourceClass:      "imported_archive",
		ProvenanceSource: "openclaw_import",
		ImportState:      SemanticImportStateQuarantine,
		Content:          "- Secret imported preference",
		MTime:            time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ImportDocument() err = %v", err)
	}

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:  root,
		Scope: "shared",
		Query: "secret imported preference",
		Mode:  SemanticModeInteractive,
		Now:   time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search() hits = %#v, want quarantined imports excluded", hits)
	}
}

func TestSemanticImportAuditApproveMakesDocumentSearchable(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		Sources:             []string{"memory/knowledge.md"},
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
		DailyNotesDir:       "memory/daily",
	})

	docID, err := engine.ImportDocument(context.Background(), SemanticImportRequest{
		Scope:            "principal",
		PrincipalID:      "42",
		SourcePath:       "imports/openclaw/preferences.md",
		SourceKind:       "knowledge",
		SourceClass:      "imported_archive",
		ProvenanceSource: "openclaw_import",
		ImportState:      SemanticImportStateQuarantine,
		Content:          "- Prefers concise updates from imported archive",
		MTime:            time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ImportDocument() err = %v", err)
	}

	docs, err := engine.ListImportAudit(context.Background(), SemanticAuditFilter{
		State:       SemanticImportStateQuarantine,
		Scope:       "principal",
		PrincipalID: "42",
	})
	if err != nil {
		t.Fatalf("ListImportAudit() err = %v", err)
	}
	if len(docs) != 1 || docs[0].ID != docID {
		t.Fatalf("ListImportAudit() = %#v, want imported document %d", docs, docID)
	}

	if err := engine.SetImportState(context.Background(), docID, SemanticImportStateApproved); err != nil {
		t.Fatalf("SetImportState() err = %v", err)
	}

	hits, err := engine.Search(context.Background(), SemanticSearchRequest{
		Root:        root,
		Scope:       "principal",
		PrincipalID: "42",
		Query:       "concise updates imported archive",
		Mode:        SemanticModeInteractive,
		Now:         time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Search() err = %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Search() returned no hits, want approved imported document")
	}
	if hits[0].PrincipalID != "42" || hits[0].Provenance != "openclaw_import" {
		t.Fatalf("top hit = %#v, want principal discriminator and provenance", hits[0])
	}

	if _, err := engine.ReviewImportDocument(context.Background(), docID, 4, 2000); err == nil {
		t.Fatal("ReviewImportDocument() err = nil after approval, want quarantine-only review")
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
