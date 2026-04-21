//go:build linux

package durableagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func TestSnapshotLifecycleCreateListRestore(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(workspaceRoot) err = %v", err)
	}
	if err := os.MkdirAll(memoryRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot) err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(memoryRoot, "memory"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memoryRoot/memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "task.txt"), []byte("before"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "memory", "knowledge.md"), []byte("- before knowledge"), 0o600); err != nil {
		t.Fatalf("WriteFile(memory) err = %v", err)
	}

	agent := core.DurableAgent{
		AgentID:           "child-a",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
	}
	state := &core.DurableAgentState{
		AgentID:   agent.AgentID,
		StateJSON: `{"conversation":{"messages":[{"role":"child","text":"before"}]}}`,
	}
	now := time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC)
	manifest, err := CreateSnapshot(agent, state, dbPath, "before-change", now)
	if err != nil {
		t.Fatalf("CreateSnapshot() err = %v", err)
	}
	if strings.TrimSpace(manifest.SnapshotID) == "" {
		t.Fatalf("SnapshotID = %q, want non-empty", manifest.SnapshotID)
	}
	if manifest.State == nil || !strings.Contains(manifest.State.StateJSON, `"before"`) {
		t.Fatalf("manifest state = %#v, want saved state", manifest.State)
	}

	records, err := ListSnapshots(agent, dbPath, 10)
	if err != nil {
		t.Fatalf("ListSnapshots() err = %v", err)
	}
	if len(records) != 1 || records[0].SnapshotID != manifest.SnapshotID {
		t.Fatalf("records = %#v, want one created snapshot", records)
	}

	if err := os.WriteFile(filepath.Join(workspaceRoot, "task.txt"), []byte("after"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace after) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "memory", "knowledge.md"), []byte("- after knowledge"), 0o600); err != nil {
		t.Fatalf("WriteFile(memory after) err = %v", err)
	}

	restored, err := RestoreSnapshot(agent, dbPath, manifest.SnapshotID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RestoreSnapshot() err = %v", err)
	}
	if restored.SnapshotID != manifest.SnapshotID {
		t.Fatalf("restored snapshot id = %q, want %q", restored.SnapshotID, manifest.SnapshotID)
	}

	workspaceRaw, err := os.ReadFile(filepath.Join(workspaceRoot, "task.txt"))
	if err != nil {
		t.Fatalf("ReadFile(workspace restored) err = %v", err)
	}
	if strings.TrimSpace(string(workspaceRaw)) != "before" {
		t.Fatalf("workspace content = %q, want before", string(workspaceRaw))
	}
	memoryRaw, err := os.ReadFile(filepath.Join(memoryRoot, "memory", "knowledge.md"))
	if err != nil {
		t.Fatalf("ReadFile(memory restored) err = %v", err)
	}
	if !strings.Contains(string(memoryRaw), "before knowledge") {
		t.Fatalf("memory content = %q, want before snapshot content", string(memoryRaw))
	}
}
