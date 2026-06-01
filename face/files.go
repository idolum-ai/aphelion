//go:build linux

package face

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/workspace"
)

var (
	defaultStableFiles = []string{
		"IDOLUM.md",
		"face/persona/telos.md",
		"face/persona/name.md",
		"face/persona/anti-idolatry.md",
		"face/persona/voice.md",
		"face/contracts/material-floor.md",
		"face/contracts/no-new-authority.md",
		"face/contracts/no-new-facts.md",
		"face/contracts/semantic-memory-is-texture.md",
		"face/contracts/usefulness-not-obedience.md",
		"face/scenes/architecture-exploration.md",
		"face/scenes/approval-request.md",
		"face/scenes/blocked-notice.md",
		"face/scenes/completion-report.md",
		"face/scenes/refusal.md",
		"face/models/overlays.md",
		"face/models/openai-gpt-5.5.md",
		"face/models/anthropic-claude-sonnet-4-6.md",
	}
	defaultDynamicFiles = []string{
		"QUESTIONS-TO-IDOLUM.md",
		"memory/dreams.md",
		"memory/telos.md",
		"memory/projects.md",
		"memory/relationships.md",
	}
)

func LoadIdolumPromptFiles(workspaceRoot string) ([]workspace.LoadedFile, []workspace.LoadedFile, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return nil, nil, nil
	}

	stable, err := loadFaceFiles(root, defaultStableFiles, false)
	if err != nil {
		return nil, nil, err
	}
	dynamic, err := loadFaceFiles(root, defaultDynamicFiles, true)
	if err != nil {
		return nil, nil, err
	}
	return stable, dynamic, nil
}

func loadFaceFiles(workspaceRoot string, names []string, dynamic bool) ([]workspace.LoadedFile, error) {
	out := make([]workspace.LoadedFile, 0, len(names))
	for _, name := range names {
		loaded, err := loadFaceFile(workspaceRoot, name, dynamic)
		if err != nil {
			return nil, err
		}
		if loaded != nil {
			out = append(out, *loaded)
		}
	}
	return out, nil
}

func loadFaceFile(workspaceRoot string, rel string, dynamic bool) (*workspace.LoadedFile, error) {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return nil, nil
	}
	if filepath.IsAbs(rel) {
		return nil, fmt.Errorf("face file %q must be relative", rel)
	}

	base, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	target := filepath.Join(base, filepath.FromSlash(rel))
	target, err = filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve face file %q: %w", rel, err)
	}

	checkRel, err := filepath.Rel(base, target)
	if err != nil {
		return nil, fmt.Errorf("check face file %q: %w", rel, err)
	}
	if checkRel == ".." || strings.HasPrefix(checkRel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("face file %q escapes workspace root %q", rel, base)
	}

	raw, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read face file %s: %w", filepath.ToSlash(checkRel), err)
	}

	content := strings.TrimSpace(string(raw))
	if content == "" {
		return nil, nil
	}
	return &workspace.LoadedFile{
		Path:    filepath.ToSlash(checkRel),
		Content: content,
		Dynamic: dynamic,
	}, nil
}
