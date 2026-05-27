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
