//go:build linux

package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSemanticEngineProposesApprovedImportsToMarkdownInbox(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	engine := NewSemanticEngine(SemanticOptions{
		Enabled:             true,
		DBPath:              filepath.Join(root, "semantic.db"),
		InteractiveTopK:     5,
		HeartbeatTopK:       12,
		InteractiveMaxChars: 4000,
		HeartbeatMaxChars:   12000,
	})

	approvedID, err := engine.ImportDocument(context.Background(), SemanticImportRequest{
		Scope:            "shared",
		SourcePath:       "codex/session-approved.jsonl",
		SourceKind:       "codex_session",
		SourceClass:      "imported_archive",
		ProvenanceSource: "codex_session_import",
		ImportState:      SemanticImportStateApproved,
		Content:          "The operator decided that excellent PDF generation guidelines should be saved as a durable skill.",
		MTime:            time.Date(2026, time.April, 25, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ImportDocument(approved) err = %v", err)
	}
	if _, err := engine.ImportDocument(context.Background(), SemanticImportRequest{
		Scope:            "shared",
		SourcePath:       "codex/session-quarantined.jsonl",
		SourceKind:       "codex_session",
		SourceClass:      "imported_archive",
		ProvenanceSource: "codex_session_import",
		ImportState:      SemanticImportStateQuarantine,
		Content:          "This quarantined material must not be proposed yet.",
		MTime:            time.Date(2026, time.April, 25, 11, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("ImportDocument(quarantine) err = %v", err)
	}

	candidates, err := engine.ProposeApprovedImports(context.Background(), SemanticPromotionRequest{
		Root:     root,
		Scope:    "shared",
		Limit:    10,
		Now:      time.Date(2026, time.April, 26, 12, 0, 0, 0, time.UTC),
		MaxChars: 500,
	})
	if err != nil {
		t.Fatalf("ProposeApprovedImports() err = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates len = %d, want 1", len(candidates))
	}
	if candidates[0].Document.ID != approvedID {
		t.Fatalf("candidate document id = %d, want %d", candidates[0].Document.ID, approvedID)
	}
	if candidates[0].Proposal.Store != StoreKnowledge {
		t.Fatalf("proposal store = %q, want knowledge", candidates[0].Proposal.Store)
	}
	if !strings.Contains(candidates[0].Proposal.Content, "PDF generation guidelines") {
		t.Fatalf("proposal content = %q, want approved import excerpt", candidates[0].Proposal.Content)
	}
	if !strings.HasPrefix(candidates[0].Proposal.SourceRef, "semantic_document:") {
		t.Fatalf("proposal source_ref = %q, want semantic document provenance", candidates[0].Proposal.SourceRef)
	}

	again, err := engine.ProposeApprovedImports(context.Background(), SemanticPromotionRequest{
		Root:  root,
		Scope: "shared",
		Limit: 10,
		Now:   time.Date(2026, time.April, 26, 12, 5, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ProposeApprovedImports(second) err = %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second promotion candidates = %#v, want deduped no-op", again)
	}
}
