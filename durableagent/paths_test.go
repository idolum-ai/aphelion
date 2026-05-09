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
