//go:build linux

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
)

const (
	memoryFileName    = "MEMORY.md"
	memoryAltFileName = "memory.md"
	truncationMarker  = "\n...[truncated]..."
)

var autoStructuredDynamicFiles = []string{
	"SKILLS.md",
	"memory/knowledge.md",
	"memory/decisions.md",
	"memory/questions.md",
	"memory/rhizome.md",
	"memory/dreams.md",
}

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
	autoDynamic, err := loadConfiguredFiles(cfg.Workspace, autoStructuredDynamicFiles, true, cfg.BootstrapMaxChars, &remaining, seen)
	if err != nil {
		return nil, err
	}
	dynamic = append(dynamic, autoDynamic...)
	practices, err := loadPracticeFilesFromSkills(cfg.Workspace, dynamic, cfg.BootstrapMaxChars, &remaining, seen)
	if err != nil {
		return nil, err
	}
	dynamic = append(dynamic, practices...)

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
	effectiveLimit := perFileLimit
	if remaining != nil && (effectiveLimit <= 0 || *remaining < effectiveLimit) {
		effectiveLimit = *remaining
	}
	content, truncated := selectPromptContent(displayPath, memstore.StripInstrumentation(string(raw)), effectiveLimit)
	if remaining != nil {
		*remaining -= len(content)
		if *remaining < 0 {
			*remaining = 0
		}
	}
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

func selectPromptContent(displayPath string, raw string, limit int) (string, bool) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", false
	}
	if limit <= 0 {
		return "", len(content) > 0
	}

	switch normalizePromptPath(displayPath) {
	case "memory.md":
		return compactMemoryForPrompt(content, limit)
	case "memory/knowledge.md", "memory/decisions.md", "memory/questions.md", "memory/rhizome.md", "memory/dreams.md":
		compacted := CompactStructuredMemoryForPrompt(displayPath, content, limit)
		return compacted, len(compacted) < len(content)
	default:
		return truncateString(content, limit)
	}
}

func loadPracticeFilesFromSkills(
	workspaceRoot string,
	loaded []LoadedFile,
	perFileLimit int,
	remaining *int,
	seen map[string]struct{},
) ([]LoadedFile, error) {
	paths := skillPracticeLinks(loaded)
	if len(paths) == 0 {
		return nil, nil
	}
	return loadConfiguredFiles(workspaceRoot, paths, true, perFileLimit, remaining, seen)
}

func skillPracticeLinks(loaded []LoadedFile) []string {
	var paths []string
	for _, file := range loaded {
		if normalizePromptPath(file.Path) != "skills.md" {
			continue
		}
		paths = append(paths, markdownLinksUnder(file.Content, "practices/")...)
	}
	return uniqueOrderedStrings(paths)
}

func markdownLinksUnder(content string, prefix string) []string {
	prefix = filepath.ToSlash(strings.TrimSpace(prefix))
	if prefix == "" {
		return nil
	}
	var out []string
	rest := content
	for {
		open := strings.Index(rest, "](")
		if open < 0 {
			break
		}
		rest = rest[open+2:]
		close := strings.Index(rest, ")")
		if close < 0 {
			break
		}
		target := strings.TrimSpace(rest[:close])
		rest = rest[close+1:]
		if hash := strings.Index(target, "#"); hash >= 0 {
			target = target[:hash]
		}
		if query := strings.Index(target, "?"); query >= 0 {
			target = target[:query]
		}
		target = filepath.ToSlash(strings.TrimSpace(target))
		if strings.HasPrefix(target, prefix) && strings.HasSuffix(strings.ToLower(target), ".md") {
			out = append(out, target)
		}
	}
	return out
}

func CompactStructuredMemoryForPrompt(displayPath string, raw string, limit int) string {
	content := strings.TrimSpace(raw)
	if content == "" || limit <= 0 {
		return ""
	}
	if len(content) <= limit {
		return content
	}

	paragraphs := splitMarkdownParagraphs(content)
	if len(paragraphs) == 0 {
		compacted, _ := truncateString(content, limit)
		return compacted
	}

	headCount := 1
	if normalizePromptPath(displayPath) == "memory/knowledge.md" || normalizePromptPath(displayPath) == "memory/decisions.md" {
		headCount = 2
	}

	keep := make([]string, 0, len(paragraphs))
	keep = append(keep, paragraphs[:min(headCount, len(paragraphs))]...)
	keep = append(keep, "_Excerpted for prompt efficiency; recent entries prioritized._")

	tailCount := 6
	if normalizePromptPath(displayPath) == "memory/questions.md" || normalizePromptPath(displayPath) == "memory/rhizome.md" {
		tailCount = 4
	}
	if len(paragraphs) > headCount {
		start := max(headCount, len(paragraphs)-tailCount)
		keep = append(keep, paragraphs[start:]...)
	}

	compacted := strings.TrimSpace(strings.Join(uniqueOrderedStrings(keep), "\n\n"))
	compacted, _ = truncateString(compacted, limit)
	return compacted
}

func compactMemoryForPrompt(raw string, limit int) (string, bool) {
	if len(raw) <= limit {
		return raw, false
	}

	paragraphs := splitMarkdownParagraphs(raw)
	keep := make([]string, 0, 6)
	if len(paragraphs) > 0 {
		keep = append(keep, paragraphs[0])
	}
	if len(paragraphs) > 1 {
		keep = append(keep, paragraphs[1])
	}
	if identity := memstore.ExtractIdentityBlock(raw); strings.TrimSpace(identity) != "" {
		keep = append(keep, identity)
	}
	keep = append(keep, "_Excerpted for prompt efficiency; identity-bearing continuity preserved._")
	if len(paragraphs) > 2 {
		start := max(2, len(paragraphs)-3)
		keep = append(keep, paragraphs[start:]...)
	}

	compacted := strings.TrimSpace(strings.Join(uniqueOrderedStrings(keep), "\n\n"))
	compacted, truncated := truncateString(compacted, limit)
	return compacted, truncated || len(compacted) < len(raw)
}

func normalizePromptPath(path string) string {
	return filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
}

func splitMarkdownParagraphs(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	chunks := strings.Split(raw, "\n\n")
	out := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		out = append(out, chunk)
	}
	return out
}

func uniqueOrderedStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func truncateString(content string, limit int) (string, bool) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	if limit <= 0 {
		return "", len(content) > 0
	}
	if len(content) <= limit {
		return content, false
	}

	truncated := content[:limit]
	if limit > len(truncationMarker) {
		truncated = strings.TrimRight(truncated[:limit-len(truncationMarker)], " \n\r\t") + truncationMarker
	}
	return truncated, true
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

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
