//go:build linux

package releaseinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewerReleaseNoticeUsesLocalMetadataOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(path, []byte(`{"latest_version":"v0.2.2","checked_at":"2026-06-04T00:00:00Z","source":"test"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() err = %v", err)
	}
	notice, err := NewerReleaseNotice(Current{Version: "v0.2.1"}, path)
	if err != nil {
		t.Fatalf("NewerReleaseNotice() err = %v", err)
	}
	if !notice.Available || notice.LatestVersion != "v0.2.2" {
		t.Fatalf("notice = %#v, want newer release available", notice)
	}
}

func TestNewerReleaseNoticeIgnoresMissingMetadata(t *testing.T) {
	notice, err := NewerReleaseNotice(Current{Version: "v0.2.1"}, filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("NewerReleaseNotice() err = %v", err)
	}
	if notice.Available {
		t.Fatalf("notice.Available = true, want false for missing metadata")
	}
}
