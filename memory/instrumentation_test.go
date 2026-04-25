//go:build linux

package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrateMarkdownFileTagsEntriesAndStripsInstrumentation(t *testing.T) {
	raw := "# knowledge.md\n\n- durable fact\n\n## Notes\n\n- second fact"
	migrated, result, err := MigrateMarkdownFile(raw, "memory/knowledge.md", StoreKnowledge, "shared", "migration_test", time.Date(2026, 4, 25, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatalf("MigrateMarkdownFile() err = %v", err)
	}
	if !result.Changed || result.Entries != 2 {
		t.Fatalf("result = %#v, want changed with two entries", result)
	}
	if strings.Count(migrated, memoryEntryMarkerPrefix) != 2 {
		t.Fatalf("migrated = %q, want two entry markers", migrated)
	}
	stripped := StripInstrumentation(migrated)
	if strings.Contains(stripped, "aphelion-memory-entry") || !strings.Contains(stripped, "- durable fact") || !strings.Contains(stripped, "- second fact") {
		t.Fatalf("stripped = %q", stripped)
	}
	again, againResult, err := MigrateMarkdownFile(migrated, "memory/knowledge.md", StoreKnowledge, "shared", "migration_test", time.Date(2026, 4, 25, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatalf("MigrateMarkdownFile(second) err = %v", err)
	}
	if again != migrated || againResult.Changed {
		t.Fatalf("second migration changed output: result=%#v", againResult)
	}
}

func TestProposalApprovePromotesToCanonicalMemory(t *testing.T) {
	root := t.TempDir()
	proposal, err := CreateProposal(ProposalRequest{
		Root:       root,
		Scope:      "shared",
		Store:      StoreDecisions,
		SourceKind: "reflection",
		Reason:     "test",
		Content:    "- Keep reviewable memory proposals.",
		Now:        time.Date(2026, 4, 25, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateProposal() err = %v", err)
	}
	if proposal.Status != ProposalStatusProposed {
		t.Fatalf("proposal status = %q", proposal.Status)
	}
	if _, err := ApproveProposal(root, proposal.ID, "reflection", nil); err != nil {
		t.Fatalf("ApproveProposal() err = %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "memory", "decisions.md"))
	if err != nil {
		t.Fatalf("ReadFile(decisions.md) err = %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, memoryEntryMarkerPrefix) || !strings.Contains(text, "Keep reviewable memory proposals") {
		t.Fatalf("decisions.md = %q, want instrumented canonical entry", text)
	}
	loaded, err := LoadProposal(root, proposal.ID)
	if err != nil {
		t.Fatalf("LoadProposal() err = %v", err)
	}
	if loaded.Status != ProposalStatusApproved {
		t.Fatalf("loaded proposal status = %q, want approved", loaded.Status)
	}
}
