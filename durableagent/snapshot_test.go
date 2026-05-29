//go:build linux

package durableagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	baseDir, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		t.Fatalf("SnapshotBaseDir() err = %v", err)
	}
	if strings.HasPrefix(filepath.Clean(baseDir), filepath.Clean(memoryRoot)+string(filepath.Separator)) {
		t.Fatalf("SnapshotBaseDir() = %q, want parent-owned path outside child memory root %q", baseDir, memoryRoot)
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

func TestLoadSnapshotRejectsInvalidSnapshotIDs(t *testing.T) {
	t.Parallel()

	agent := core.DurableAgent{AgentID: "child-a"}
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	for _, snapshotID := range []string{
		"../crafted",
		"/tmp/crafted",
		`child\crafted`,
		"not-generated",
		"20260421T120000.000000000Z-",
	} {
		if _, _, err := LoadSnapshot(agent, dbPath, snapshotID); err == nil {
			t.Fatalf("LoadSnapshot(%q) err = nil, want validation error", snapshotID)
		}
	}
}

func TestLoadSnapshotRejectsManifestIDMismatch(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "child-a",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
	}
	snapshotID := durableSnapshotID(time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC))
	otherID := durableSnapshotID(time.Date(2026, time.April, 21, 12, 0, 1, 0, time.UTC))
	baseDir, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		t.Fatalf("SnapshotBaseDir() err = %v", err)
	}
	writeSnapshotFixture(t, filepath.Join(baseDir, snapshotID), SnapshotManifest{
		SchemaVersion: durableAgentSnapshotSchemaVersion,
		SnapshotID:    otherID,
		AgentID:       agent.AgentID,
		CreatedAt:     time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC),
		Agent:         agent,
	})

	if _, _, err := LoadSnapshot(agent, dbPath, snapshotID); err == nil {
		t.Fatal("LoadSnapshot() err = nil, want manifest snapshot_id mismatch")
	}
}

func TestLoadSnapshotRejectsManifestAgentMismatch(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "child-a",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
	}
	snapshotID := durableSnapshotID(time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC))
	baseDir, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		t.Fatalf("SnapshotBaseDir() err = %v", err)
	}
	writeSnapshotFixture(t, filepath.Join(baseDir, snapshotID), SnapshotManifest{
		SchemaVersion: durableAgentSnapshotSchemaVersion,
		SnapshotID:    snapshotID,
		AgentID:       "other-child",
		CreatedAt:     time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC),
		Agent:         agent,
	})

	if _, _, err := LoadSnapshot(agent, dbPath, snapshotID); err == nil {
		t.Fatal("LoadSnapshot() err = nil, want manifest agent_id mismatch")
	}
}

func TestMigrateChildMemorySnapshotsMovesValidEntriesAndRemovesSource(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{
		AgentID:           "child-a",
		LocalStorageRoots: []string{workspaceRoot, memoryRoot},
	}
	snapshotID := durableSnapshotID(time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC))
	sourceBase, err := childMemorySnapshotBaseDir(agent, dbPath)
	if err != nil {
		t.Fatalf("childMemorySnapshotBaseDir() err = %v", err)
	}
	writeSnapshotFixture(t, filepath.Join(sourceBase, snapshotID), SnapshotManifest{
		SchemaVersion: durableAgentSnapshotSchemaVersion,
		SnapshotID:    snapshotID,
		AgentID:       agent.AgentID,
		CreatedAt:     time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC),
		Agent:         agent,
		State:         &core.DurableAgentState{AgentID: agent.AgentID, StateJSON: `{"ok":true}`},
	})
	writeSnapshotFixture(t, filepath.Join(sourceBase, "bad-id"), SnapshotManifest{
		SchemaVersion: durableAgentSnapshotSchemaVersion,
		SnapshotID:    "bad-id",
		AgentID:       agent.AgentID,
		CreatedAt:     time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC),
		Agent:         agent,
	})

	result, err := MigrateChildMemorySnapshots(agent, dbPath)
	if err != nil {
		t.Fatalf("MigrateChildMemorySnapshots() err = %v", err)
	}
	if result.Scanned != 2 || result.Migrated != 1 || result.Rejected != 1 || !result.SourceRemoved {
		t.Fatalf("migration result = %#v, want one migrated, one rejected, source removed", result)
	}
	if _, err := os.Stat(sourceBase); !os.IsNotExist(err) {
		t.Fatalf("source base stat err = %v, want removed source", err)
	}
	manifest, _, err := LoadSnapshot(agent, dbPath, snapshotID)
	if err != nil {
		t.Fatalf("LoadSnapshot(migrated) err = %v", err)
	}
	if manifest.SnapshotID != snapshotID || manifest.State == nil || !strings.Contains(manifest.State.StateJSON, `"ok"`) {
		t.Fatalf("migrated manifest = %#v, want original validated snapshot", manifest)
	}
	records, err := ListSnapshots(agent, dbPath, 10)
	if err != nil {
		t.Fatalf("ListSnapshots() err = %v", err)
	}
	if len(records) != 1 || records[0].SnapshotID != snapshotID {
		t.Fatalf("records = %#v, want only migrated valid snapshot", records)
	}
}

func writeSnapshotFixture(t *testing.T, snapshotDir string, manifest SnapshotManifest) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(snapshotDir, "workspace"), 0o700); err != nil {
		t.Fatalf("MkdirAll(workspace) err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(snapshotDir, "memory"), 0o700); err != nil {
		t.Fatalf("MkdirAll(memory) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "workspace", "task.txt"), []byte("snapshot task"), 0o600); err != nil {
		t.Fatalf("WriteFile(workspace) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "memory", "note.md"), []byte("snapshot memory"), 0o600); err != nil {
		t.Fatalf("WriteFile(memory) err = %v", err)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(manifest) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "manifest.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) err = %v", err)
	}
}

func TestSnapshotSkipsNestedSnapshotStorageAndListsNewestFirst(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	if err := os.MkdirAll(filepath.Join(memoryRoot, ".snapshots", "legacy"), 0o755); err != nil {
		t.Fatalf("MkdirAll(memory .snapshots) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, ".snapshots", "legacy", "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(old snapshot marker) err = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(memoryRoot, "notes"), 0o755); err != nil {
		t.Fatalf("MkdirAll(notes) err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(memoryRoot, "notes", "keep.md"), []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile(keep) err = %v", err)
	}
	agent := core.DurableAgent{AgentID: "child-a", LocalStorageRoots: []string{workspaceRoot, memoryRoot}}
	firstTime := time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Minute)
	first, err := CreateSnapshot(agent, nil, dbPath, "first", firstTime)
	if err != nil {
		t.Fatalf("CreateSnapshot(first) err = %v", err)
	}
	second, err := CreateSnapshot(agent, nil, dbPath, "second", secondTime)
	if err != nil {
		t.Fatalf("CreateSnapshot(second) err = %v", err)
	}
	_, firstDir, err := LoadSnapshot(agent, dbPath, first.SnapshotID)
	if err != nil {
		t.Fatalf("LoadSnapshot(first) err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(firstDir, "memory", ".snapshots", "legacy", "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("legacy .snapshots copied into snapshot, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(firstDir, "memory", "notes", "keep.md")); err != nil {
		t.Fatalf("kept memory file stat err = %v", err)
	}
	records, err := ListSnapshots(agent, dbPath, 1)
	if err != nil {
		t.Fatalf("ListSnapshots(limit) err = %v", err)
	}
	if len(records) != 1 || records[0].SnapshotID != second.SnapshotID || records[0].Reason != "second" {
		t.Fatalf("records = %#v, want newest second snapshot only", records)
	}
}

func TestRestoreSnapshotLeavesBackupOnOverwrite(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(workspaceRoot, "task.txt"), []byte("snapshot value"), 0o600); err != nil {
		t.Fatalf("WriteFile(snapshot workspace) err = %v", err)
	}
	agent := core.DurableAgent{AgentID: "child-a", LocalStorageRoots: []string{workspaceRoot, memoryRoot}}
	now := time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC)
	manifest, err := CreateSnapshot(agent, nil, dbPath, "before-overwrite", now)
	if err != nil {
		t.Fatalf("CreateSnapshot() err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "task.txt"), []byte("current value"), 0o600); err != nil {
		t.Fatalf("WriteFile(current workspace) err = %v", err)
	}
	restoreTime := now.Add(time.Minute)
	if _, err := RestoreSnapshot(agent, dbPath, manifest.SnapshotID, restoreTime); err != nil {
		t.Fatalf("RestoreSnapshot() err = %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(workspaceRoot, "task.txt"))
	if err != nil {
		t.Fatalf("ReadFile(restored) err = %v", err)
	}
	if string(restored) != "snapshot value" {
		t.Fatalf("restored workspace = %q, want snapshot value", string(restored))
	}
	backupPath := workspaceRoot + ".restore-bak-" + strings.ToLower(strconv.FormatInt(restoreTime.UnixNano(), 36))
	backup, err := os.ReadFile(filepath.Join(backupPath, "task.txt"))
	if err != nil {
		t.Fatalf("ReadFile(backup) err = %v", err)
	}
	if string(backup) != "current value" {
		t.Fatalf("backup workspace = %q, want current value", string(backup))
	}
}

func TestMigrateChildMemorySnapshotsKeepsExistingValidDestination(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	workspaceRoot := filepath.Join(t.TempDir(), "child", "workspace")
	memoryRoot := filepath.Join(t.TempDir(), "child", "memory")
	agent := core.DurableAgent{AgentID: "child-a", LocalStorageRoots: []string{workspaceRoot, memoryRoot}}
	snapshotID := durableSnapshotID(time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC))
	sourceBase, err := childMemorySnapshotBaseDir(agent, dbPath)
	if err != nil {
		t.Fatalf("childMemorySnapshotBaseDir() err = %v", err)
	}
	targetBase, err := SnapshotBaseDir(agent, dbPath)
	if err != nil {
		t.Fatalf("SnapshotBaseDir() err = %v", err)
	}
	manifest := SnapshotManifest{SchemaVersion: durableAgentSnapshotSchemaVersion, SnapshotID: snapshotID, AgentID: agent.AgentID, CreatedAt: time.Date(2026, time.April, 21, 12, 0, 0, 0, time.UTC), Agent: agent}
	writeSnapshotFixture(t, filepath.Join(sourceBase, snapshotID), manifest)
	writeSnapshotFixture(t, filepath.Join(targetBase, snapshotID), manifest)

	result, err := MigrateChildMemorySnapshots(agent, dbPath)
	if err != nil {
		t.Fatalf("MigrateChildMemorySnapshots() err = %v", err)
	}
	if result.Scanned != 1 || result.Migrated != 0 || result.AlreadyPresent != 1 || result.Rejected != 0 || !result.SourceRemoved {
		t.Fatalf("migration result = %#v, want already-present accounting and source removal", result)
	}
}
