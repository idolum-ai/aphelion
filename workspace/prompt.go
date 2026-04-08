//go:build linux

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
)

const (
	memoryFileName    = "MEMORY.md"
	memoryAltFileName = "memory.md"
	truncationMarker  = "\n...[truncated]..."
)

type LoadedFile struct {
	Path      string
	Content   string
	Dynamic   bool
	Truncated bool
}

type PromptContext struct {
	Workspace string
	Stable    []LoadedFile
	Dynamic   []LoadedFile
}

func LoadPromptContext(cfg config.AgentConfig, now time.Time) (*PromptContext, error) {
	ctx := &PromptContext{Workspace: cfg.Workspace}
	remaining := cfg.BootstrapTotalMaxChars
	seen := make(map[string]struct{})

	stable, err := loadConfiguredFiles(cfg.Workspace, cfg.BootstrapFiles, false, cfg.BootstrapMaxChars, &remaining, seen)
	if err != nil {
		return nil, err
	}
	dynamic, err := loadConfiguredFiles(cfg.Workspace, cfg.DynamicFiles, true, cfg.BootstrapMaxChars, &remaining, seen)
	if err != nil {
		return nil, err
	}

	if cfg.DailyNotes {
		notes, err := loadDailyNotes(cfg.Workspace, cfg.DailyNotesDir, now, cfg.BootstrapMaxChars, &remaining, seen)
		if err != nil {
			return nil, err
		}
		dynamic = append(dynamic, notes...)
	}

	ctx.Stable = stable
	ctx.Dynamic = dynamic
	return ctx, nil
}

func (c *PromptContext) Render(baseInstruction string) string {
	parts := make([]string, 0, 4+len(c.Stable)+len(c.Dynamic))
	if strings.TrimSpace(baseInstruction) != "" {
		parts = append(parts, strings.TrimSpace(baseInstruction))
	}

	if len(c.Stable) > 0 {
		parts = append(parts, "## Workspace Bootstrap Files")
		for _, file := range c.Stable {
			parts = append(parts, renderFile(file))
		}
	}

	if len(c.Dynamic) > 0 {
		parts = append(parts, "## Dynamic Workspace Files")
		parts = append(parts, "These files may change between turns and are reloaded for every request.")
		for _, file := range c.Dynamic {
			parts = append(parts, renderFile(file))
		}
	}

	return strings.Join(parts, "\n\n")
}

func renderFile(file LoadedFile) string {
	return fmt.Sprintf("### %s\n%s", file.Path, file.Content)
}

func loadConfiguredFiles(
	workspaceRoot string,
	names []string,
	dynamic bool,
	perFileLimit int,
	remaining *int,
	seen map[string]struct{},
) ([]LoadedFile, error) {
	out := make([]LoadedFile, 0, len(names))
	for _, name := range names {
		file, err := loadOne(workspaceRoot, name, dynamic, perFileLimit, remaining, seen)
		if err != nil {
			return nil, err
		}
		if file != nil {
			out = append(out, *file)
		}
		if remaining != nil && *remaining <= 0 {
			break
		}
	}
	return out, nil
}

func loadDailyNotes(
	workspaceRoot string,
	notesDir string,
	now time.Time,
	perFileLimit int,
	remaining *int,
	seen map[string]struct{},
) ([]LoadedFile, error) {
	paths := []string{
		filepath.ToSlash(filepath.Join(notesDir, now.Format("2006-01-02")+".md")),
		filepath.ToSlash(filepath.Join(notesDir, now.AddDate(0, 0, -1).Format("2006-01-02")+".md")),
	}
	return loadConfiguredFiles(workspaceRoot, paths, true, perFileLimit, remaining, seen)
}

func loadOne(
	workspaceRoot string,
	name string,
	dynamic bool,
	perFileLimit int,
	remaining *int,
	seen map[string]struct{},
) (*LoadedFile, error) {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" {
		return nil, nil
	}

	path, displayPath, err := resolveWorkspacePath(workspaceRoot, name)
	if err != nil {
		return nil, err
	}

	if _, ok := seen[path]; ok {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && strings.EqualFold(name, memoryFileName) {
			altPath, altDisplay, altErr := resolveWorkspacePath(workspaceRoot, memoryAltFileName)
			if altErr != nil {
				return nil, altErr
			}
			raw, err = os.ReadFile(altPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil, nil
				}
				return nil, fmt.Errorf("read workspace file %s: %w", altDisplay, err)
			}
			path = altPath
			displayPath = altDisplay
		} else if os.IsNotExist(err) {
			return nil, nil
		} else {
			return nil, fmt.Errorf("read workspace file %s: %w", displayPath, err)
		}
	}

	seen[path] = struct{}{}
	content, truncated := truncateContent(string(raw), perFileLimit, remaining)
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}

	return &LoadedFile{
		Path:      displayPath,
		Content:   content,
		Dynamic:   dynamic,
		Truncated: truncated,
	}, nil
}

func resolveWorkspacePath(workspaceRoot string, rel string) (string, string, error) {
	if filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("workspace file %q must be relative to the workspace root", rel)
	}

	base, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	target := filepath.Join(base, filepath.FromSlash(rel))
	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace file %q: %w", rel, err)
	}

	checkRel, err := filepath.Rel(base, target)
	if err != nil {
		return "", "", fmt.Errorf("check workspace path %q: %w", rel, err)
	}
	if checkRel == ".." || strings.HasPrefix(checkRel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("workspace file %q escapes workspace root %q", rel, base)
	}

	return target, filepath.ToSlash(checkRel), nil
}

func truncateContent(raw string, perFileLimit int, remaining *int) (string, bool) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", false
	}

	limit := len(content)
	if perFileLimit > 0 && limit > perFileLimit {
		limit = perFileLimit
	}
	if remaining != nil && *remaining < limit {
		limit = *remaining
	}
	if limit <= 0 {
		return "", len(content) > 0
	}

	truncated := limit < len(content)
	content = content[:limit]
	if truncated && limit > len(truncationMarker) {
		content = strings.TrimRight(content[:limit-len(truncationMarker)], " \n\r\t") + truncationMarker
	}

	if remaining != nil {
		*remaining -= len(content)
		if *remaining < 0 {
			*remaining = 0
		}
	}

	return content, truncated
}
