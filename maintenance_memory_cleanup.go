//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	memstore "github.com/idolum-ai/aphelion/memory"
	"github.com/idolum-ai/aphelion/workspace"
)

func clearSharedDynamicMemory(cfg *config.Config) (int, error) {
	root := strings.TrimSpace(cfg.Agent.SharedMemoryRoot)
	if root == "" {
		return 0, nil
	}

	removed := 0
	memoryPath := filepath.Join(root, "MEMORY.md")
	preserved, err := preserveMemoryIdentitySections(memoryPath)
	if err != nil {
		return removed, err
	}
	if preserved {
		removed++
	}

	paths := make([]string, 0, len(cfg.Agent.DynamicFiles)+2)
	for _, name := range cfg.Agent.DynamicFiles {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if strings.EqualFold(trimmed, "MEMORY.md") {
			continue
		}
		paths = append(paths, filepath.Join(root, filepath.FromSlash(trimmed)))
	}
	paths = append(paths, filepath.Join(root, "memory.md"))
	paths = append(paths,
		filepath.Join(root, "memory", "knowledge.md"),
		filepath.Join(root, "memory", "decisions.md"),
		filepath.Join(root, "memory", "questions.md"),
		filepath.Join(root, "memory", "rhizome.md"),
	)
	n, err := removeMany(paths)
	if err != nil {
		return removed, err
	}
	removed += n

	noteRemoved, err := clearDailyNotesUnderRoot(root, cfg.Agent.DailyNotesDir)
	if err != nil {
		return removed, err
	}
	return removed + noteRemoved, nil
}

func cleanupTempTrees(cfg *config.Config) (int, error) {
	paths := []string{
		filepath.Join(cfg.Agent.ExecRoot, ".aphelion", "tmp"),
		filepath.Join(cfg.Agent.SharedMemoryRoot, ".aphelion", "tmp"),
	}

	for _, root := range []string{cfg.Agent.UserWorkspaceRoot, cfg.Agent.UserMemoryRoot} {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, fmt.Errorf("read temp root %s: %w", root, err)
		}
		for _, entry := range entries {
			paths = append(paths, filepath.Join(root, entry.Name(), ".aphelion", "tmp"))
		}
	}
	return removeMany(paths)
}

func archiveColdDailyNotes(cfg *config.Config, now time.Time) (int, error) {
	if cfg == nil || !cfg.Memory.Decay.Enabled || cfg.Memory.Decay.ColdDays <= 0 {
		return 0, nil
	}

	roots := []string{cfg.Agent.SharedMemoryRoot}
	entries, err := os.ReadDir(cfg.Agent.UserMemoryRoot)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("read user memory root %s: %w", cfg.Agent.UserMemoryRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			roots = append(roots, filepath.Join(cfg.Agent.UserMemoryRoot, entry.Name()))
		}
	}

	archived := 0
	cutoff := now.AddDate(0, 0, -cfg.Memory.Decay.ColdDays)
	for _, root := range uniqueStrings(roots) {
		n, err := archiveNotesUnderRoot(root, cfg.Agent.DailyNotesDir, cutoff)
		if err != nil {
			return archived, err
		}
		archived += n
	}
	return archived, nil
}

func archiveNotesUnderRoot(root string, notesDir string, cutoff time.Time) (int, error) {
	root = strings.TrimSpace(root)
	notesDir = strings.TrimSpace(notesDir)
	if root == "" || notesDir == "" {
		return 0, nil
	}

	sourceRoot := filepath.Join(root, filepath.FromSlash(notesDir))
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read daily notes dir %s: %w", sourceRoot, err)
	}

	archiveRoot := notesArchiveRoot(root, notesDir)
	archived := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".md" {
			continue
		}
		ts, err := time.Parse("2006-01-02", strings.TrimSuffix(name, ".md"))
		if err != nil {
			continue
		}
		if !ts.Before(cutoff) {
			continue
		}
		if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
			return archived, fmt.Errorf("create daily archive dir %s: %w", archiveRoot, err)
		}
		src := filepath.Join(sourceRoot, name)
		dst := filepath.Join(archiveRoot, name)
		if err := os.Rename(src, dst); err != nil {
			return archived, fmt.Errorf("archive daily note %s -> %s: %w", src, dst, err)
		}
		archived++
	}
	return archived, nil
}

func clearDailyNotesUnderRoot(root string, notesDir string) (int, error) {
	root = strings.TrimSpace(root)
	notesDir = strings.TrimSpace(notesDir)
	if root == "" || notesDir == "" {
		return 0, nil
	}

	sourceRoot := filepath.Join(root, filepath.FromSlash(notesDir))
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read daily notes dir %s: %w", sourceRoot, err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isDailyNoteFilename(name) {
			continue
		}
		if err := os.Remove(filepath.Join(sourceRoot, name)); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("remove daily note %s: %w", filepath.Join(sourceRoot, name), err)
		}
		removed++
	}
	return removed, nil
}

func notesArchiveRoot(root string, notesDir string) string {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(notesDir)))
	dir := filepath.Dir(clean)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) || strings.TrimSpace(base) == "" {
		base = "daily"
	}
	if dir == "." {
		return filepath.Join(root, "archive", base)
	}
	return filepath.Join(root, dir, "archive", base)
}

func isDailyNoteFilename(name string) bool {
	if filepath.Ext(name) != ".md" {
		return false
	}
	_, err := time.Parse("2006-01-02", strings.TrimSuffix(name, ".md"))
	return err == nil
}

func removeContents(root string) (int, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read directory %s: %w", root, err)
	}

	removed := 0
	for _, entry := range entries {
		ok, err := removeAllIfExists(filepath.Join(root, entry.Name()))
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

func removeMany(paths []string) (int, error) {
	removed := 0
	for _, path := range uniqueStrings(paths) {
		ok, err := removeAllIfExists(path)
		if err != nil {
			return removed, err
		}
		if ok {
			removed++
		}
	}
	return removed, nil
}

func removeAllIfExists(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return false, fmt.Errorf("remove %s: %w", path, err)
	}
	return true, nil
}

func preserveMemoryIdentitySections(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	content, ok := memstore.PreserveMemoryIdentity(string(raw))
	if !ok {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("remove %s: %w", path, err)
		}
		return true, nil
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return false, fmt.Errorf("rewrite %s: %w", path, err)
	}
	return true, nil
}

func archiveOversizedCuratedMemory(cfg *config.Config, now time.Time) (int, error) {
	if cfg == nil || !cfg.Memory.Decay.Enabled {
		return 0, nil
	}

	roots := []string{cfg.Agent.SharedMemoryRoot}
	entries, err := os.ReadDir(cfg.Agent.UserMemoryRoot)
	if err != nil && !os.IsNotExist(err) {
		return 0, fmt.Errorf("read user memory root %s: %w", cfg.Agent.UserMemoryRoot, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			roots = append(roots, filepath.Join(cfg.Agent.UserMemoryRoot, entry.Name()))
		}
	}

	archived := 0
	for _, root := range uniqueStrings(roots) {
		n, err := archiveOversizedCuratedUnderRoot(root, now)
		if err != nil {
			return archived, err
		}
		archived += n
	}
	return archived, nil
}

func archiveOversizedCuratedUnderRoot(root string, now time.Time) (int, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return 0, nil
	}

	type limit struct {
		store string
		chars int
	}
	limits := []limit{
		{store: memstore.StoreKnowledge, chars: 12000},
		{store: memstore.StoreDecisions, chars: 12000},
		{store: memstore.StoreQuestions, chars: 8000},
		{store: memstore.StoreRhizome, chars: 8000},
	}

	archived := 0
	for _, item := range limits {
		path, _, err := memstore.ResolveStorePath(root, item.store)
		if err != nil {
			return archived, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return archived, fmt.Errorf("read curated memory %s: %w", path, err)
		}
		content := strings.TrimSpace(string(raw))
		if len(content) <= item.chars {
			continue
		}

		archiveDir := filepath.Join(filepath.Dir(path), "archive")
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			return archived, fmt.Errorf("create curated archive dir %s: %w", archiveDir, err)
		}
		archivePath := filepath.Join(archiveDir, fmt.Sprintf("%s-%s.md", item.store, now.UTC().Format("20060102T150405")))
		if err := os.WriteFile(archivePath, raw, 0o600); err != nil {
			return archived, fmt.Errorf("write curated archive %s: %w", archivePath, err)
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return archived, fmt.Errorf("derive curated path %s: %w", path, err)
		}
		compacted := workspace.CompactStructuredMemoryForPrompt(filepath.ToSlash(relPath), content, item.chars)
		if strings.TrimSpace(compacted) == "" {
			compacted = content[:min(item.chars, len(content))]
		}
		if err := os.WriteFile(path, []byte(strings.TrimSpace(compacted)+"\n"), 0o600); err != nil {
			return archived, fmt.Errorf("rewrite curated memory %s: %w", path, err)
		}
		archived++
	}
	return archived, nil
}
