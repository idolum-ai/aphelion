//go:build linux

package durableagent

import (
	"path/filepath"
	"testing"
)

func TestDefaultLocalRootsRejectInvalidAgentID(t *testing.T) {
	t.Parallel()

	workspaceRoot, memoryRoot := DefaultLocalRoots(filepath.Join(t.TempDir(), "sessions.db"), "../escape")
	if workspaceRoot != "" || memoryRoot != "" {
		t.Fatalf("DefaultLocalRoots() = %q, %q; want empty roots for invalid agent id", workspaceRoot, memoryRoot)
	}
}

func TestLocalRootsRejectInvalidAgentID(t *testing.T) {
	t.Parallel()

	workspaceRoot, memoryRoot := LocalRoots("../escape", []string{filepath.Join(t.TempDir(), "workspace"), filepath.Join(t.TempDir(), "memory")})
	if workspaceRoot != "" || memoryRoot != "" {
		t.Fatalf("LocalRoots() = %q, %q; want empty roots for invalid agent id", workspaceRoot, memoryRoot)
	}
}

func TestDefaultLocalRootsUsesSessionsDBDirectoryAndAgentID(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "state", "sessions.db")
	workspaceRoot, memoryRoot := DefaultLocalRoots(dbPath, "child-a")
	wantBase := filepath.Join(filepath.Dir(dbPath), "durable_agents", "child-a")
	if workspaceRoot != filepath.Join(wantBase, "workspace") || memoryRoot != filepath.Join(wantBase, "memory") {
		t.Fatalf("DefaultLocalRoots() = %q, %q; want workspace/memory under %q", workspaceRoot, memoryRoot, wantBase)
	}
}

func TestLocalRootsConfiguredForms(t *testing.T) {
	t.Parallel()

	base := filepath.Join(t.TempDir(), "child")
	workspaceRoot, memoryRoot := LocalRoots("child-a", []string{base})
	if workspaceRoot != filepath.Join(base, "workspace") || memoryRoot != filepath.Join(base, "memory") {
		t.Fatalf("LocalRoots(one root) = %q, %q; want derived workspace/memory", workspaceRoot, memoryRoot)
	}

	workspace := filepath.Join(t.TempDir(), "workspace")
	memory := filepath.Join(t.TempDir(), "memory")
	workspaceRoot, memoryRoot = LocalRoots("child-a", []string{" " + workspace + " ", " " + memory + " "})
	if workspaceRoot != workspace || memoryRoot != memory {
		t.Fatalf("LocalRoots(two roots) = %q, %q; want trimmed configured roots", workspaceRoot, memoryRoot)
	}
}
