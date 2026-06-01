//go:build linux

package face

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIdolumPromptFilesLoadsRoutedPersonaContracts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	files := map[string]string{
		"IDOLUM.md":             "core identity",
		"face/persona/telos.md": "Represent Idolum and help the user achieve goals",
		"face/contracts/semantic-memory-is-texture.md": "Route beats retrieval",
		"face/scenes/approval-request.md":              "Ask for bounded authority",
		"QUESTIONS-TO-IDOLUM.md":                       "dynamic questions",
		"memory/dreams.md":                             "dream texture",
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	stable, dynamic, err := LoadIdolumPromptFiles(root)
	if err != nil {
		t.Fatalf("LoadIdolumPromptFiles err = %v", err)
	}
	stableByPath := map[string]string{}
	for _, file := range stable {
		stableByPath[file.Path] = file.Content
		if file.Dynamic {
			t.Fatalf("stable file %s marked dynamic", file.Path)
		}
	}
	for _, want := range []string{
		"IDOLUM.md",
		"face/persona/telos.md",
		"face/contracts/semantic-memory-is-texture.md",
		"face/scenes/approval-request.md",
	} {
		if stableByPath[want] == "" {
			t.Fatalf("stable face files missing %s in %#v", want, stableByPath)
		}
	}
	if len(dynamic) == 0 || dynamic[0].Path != "QUESTIONS-TO-IDOLUM.md" || !dynamic[0].Dynamic {
		t.Fatalf("dynamic files not loaded as dynamic: %#v", dynamic)
	}
}
