//go:build linux

package codex

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteArtifactManifestUsesRestrictiveModes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "agent-x", "artifacts")
	manifest := ArtifactManifest{
		AgentID:   "agent-x",
		UpdatedAt: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
		Artifacts: []ArtifactManifestEntry{
			{Path: "heartbeats/example.json", SHA256: "sha256:abc", UpdatedAt: time.Now().UTC()},
		},
	}

	if err := WriteArtifactManifest(nested, manifest); err != nil {
		t.Fatalf("WriteArtifactManifest err = %v", err)
	}

	manifestPath := filepath.Join(nested, "ARTIFACTS.json")
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat manifest err = %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("manifest mode = %#o, want no group/other bits set", mode)
	}

	dirInfo, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat artifact dir err = %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode&0o077 != 0 {
		t.Fatalf("artifact dir mode = %#o, want no group/other bits set", mode)
	}
}

func TestWriteArtifactManifestTightensExistingPermissiveModes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "agent-x", "artifacts")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll err = %v", err)
	}
	manifestPath := filepath.Join(nested, "ARTIFACTS.json")
	if err := os.WriteFile(manifestPath, []byte(`{"agent_id":"agent-x","artifacts":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile err = %v", err)
	}
	if err := os.Chmod(nested, 0o755); err != nil {
		t.Fatalf("chmod dir err = %v", err)
	}
	if err := os.Chmod(manifestPath, 0o644); err != nil {
		t.Fatalf("chmod manifest err = %v", err)
	}

	manifest := ArtifactManifest{AgentID: "agent-x", UpdatedAt: time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC)}
	if err := WriteArtifactManifest(nested, manifest); err != nil {
		t.Fatalf("WriteArtifactManifest err = %v", err)
	}

	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat manifest err = %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("manifest mode = %#o, want 0600", mode)
	}
	dirInfo, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat artifact dir err = %v", err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("artifact dir mode = %#o, want 0700", mode)
	}
}
