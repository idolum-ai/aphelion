//go:build linux

package durableagent

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var requiredArchetypeFiles = []string{
	"AGENT.md",
	"profile/charter.md",
	"profile/policy.md",
	"profile/capabilities.md",
	"profile/runtime.md",
}

var disallowedArchetypePathParts = map[string]struct{}{
	".aphelion":      {},
	"artifacts":      {},
	"grants":         {},
	"memory":         {},
	"snapshots":      {},
	"state":          {},
	"workspace":      {},
	"ARTIFACTS.json": {},
}

type Archetype struct {
	Name     string
	Root     string
	Files    map[string]string
	Profile  map[string]string
	Examples []string
}

type ArchetypeSummary struct {
	Name          string
	Root          string
	RequiredFiles []string
	Examples      []string
}

func ListArchetypes(root string) ([]ArchetypeSummary, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("archetype root is required")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read archetype root: %w", err)
	}
	out := make([]ArchetypeSummary, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		archetype, err := LoadArchetype(root, entry.Name())
		if err != nil {
			continue
		}
		out = append(out, archetype.Summary())
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func LoadArchetype(root string, name string) (Archetype, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return Archetype{}, fmt.Errorf("archetype root is required")
	}
	cleanName, err := cleanArchetypeName(name)
	if err != nil {
		return Archetype{}, err
	}
	archetypeRoot := filepath.Join(root, cleanName)
	info, err := os.Stat(archetypeRoot)
	if err != nil {
		return Archetype{}, fmt.Errorf("stat archetype %q: %w", cleanName, err)
	}
	if !info.IsDir() {
		return Archetype{}, fmt.Errorf("archetype %q is not a directory", cleanName)
	}
	for _, rel := range requiredArchetypeFiles {
		if err := validateArchetypeRelPath(rel); err != nil {
			return Archetype{}, err
		}
		info, err := os.Stat(filepath.Join(archetypeRoot, filepath.FromSlash(rel)))
		if err != nil {
			return Archetype{}, fmt.Errorf("archetype %q missing required file %s: %w", cleanName, rel, err)
		}
		if info.IsDir() {
			return Archetype{}, fmt.Errorf("archetype %q required file %s is a directory", cleanName, rel)
		}
	}

	files := make(map[string]string)
	profile := make(map[string]string)
	examples := make([]string, 0)
	err = filepath.WalkDir(archetypeRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(archetypeRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if err := validateArchetypeRelPath(rel); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archetype %q path %s must not be a symlink", cleanName, rel)
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read archetype %q file %s: %w", cleanName, rel, err)
		}
		files[rel] = string(raw)
		if strings.HasPrefix(rel, "profile/") {
			profile[strings.TrimPrefix(rel, "profile/")] = string(raw)
		}
		if strings.HasPrefix(rel, "examples/") {
			examples = append(examples, rel)
		}
		return nil
	})
	if err != nil {
		return Archetype{}, err
	}
	sort.Strings(examples)
	return Archetype{
		Name:     cleanName,
		Root:     archetypeRoot,
		Files:    files,
		Profile:  profile,
		Examples: examples,
	}, nil
}

func (a Archetype) Summary() ArchetypeSummary {
	return ArchetypeSummary{
		Name:          strings.TrimSpace(a.Name),
		Root:          strings.TrimSpace(a.Root),
		RequiredFiles: append([]string(nil), requiredArchetypeFiles...),
		Examples:      append([]string(nil), a.Examples...),
	}
}

func cleanArchetypeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("archetype name is required")
	}
	if filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("archetype name %q is not allowed", name)
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean == ".." || strings.Contains(clean, "/") || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archetype name %q is not allowed", name)
	}
	return clean, nil
}

func validateArchetypeRelPath(rel string) error {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" || rel == "." {
		return nil
	}
	if filepath.IsAbs(rel) || strings.Contains(rel, "\\") || strings.Contains(rel, "\x00") {
		return fmt.Errorf("archetype path %q is not allowed", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("archetype path %q escapes archetype root", rel)
	}
	for _, part := range strings.Split(clean, "/") {
		if _, disallowed := disallowedArchetypePathParts[part]; disallowed {
			return fmt.Errorf("archetype path %q contains live-state path %q", rel, part)
		}
	}
	return nil
}
