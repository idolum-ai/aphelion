//go:build linux

package prompt

import (
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/agent"
	"github.com/idolum-ai/aphelion/workspace"
)

func markLastStableCacheBreakpoint(blocks []agent.SystemBlock) {
	for i := len(blocks) - 1; i >= 0; i-- {
		if strings.TrimSpace(blocks[i].Text) == "" {
			continue
		}
		blocks[i].CacheBreakpoint = true
		return
	}
}

func shapeDynamicFilesForPromptCache(files []workspace.LoadedFile, strategy string, lookback int) ([]workspace.LoadedFile, []string) {
	if len(files) == 0 || !cacheAwarePromptLookbackEnabled(strategy) {
		return files, nil
	}
	if lookback <= 0 {
		lookback = defaultCacheAwareDynamicLookback
	}
	if len(files) <= lookback {
		return files, nil
	}

	required := make([]workspace.LoadedFile, 0, 3)
	candidates := make([]workspace.LoadedFile, 0, len(files))
	for _, file := range files {
		if cacheLookbackAlwaysKeep(file.Path) {
			required = append(required, file)
			continue
		}
		candidates = append(candidates, file)
	}
	if len(candidates) <= lookback {
		return files, nil
	}

	keepStart := len(candidates) - lookback
	keepByPath := make(map[string]struct{}, len(required)+lookback)
	for _, file := range required {
		keepByPath[normalizePromptCachePath(file.Path)] = struct{}{}
	}
	for _, file := range candidates[keepStart:] {
		keepByPath[normalizePromptCachePath(file.Path)] = struct{}{}
	}

	kept := make([]workspace.LoadedFile, 0, len(keepByPath))
	omitted := make([]string, 0, len(candidates)-lookback)
	for _, file := range files {
		path := normalizePromptCachePath(file.Path)
		if _, ok := keepByPath[path]; ok {
			kept = append(kept, file)
			continue
		}
		omitted = append(omitted, strings.TrimSpace(file.Path))
	}
	return kept, omitted
}

func cacheAwarePromptLookbackEnabled(strategy string) bool {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "auto", "hybrid":
		return true
	default:
		return false
	}
}

func cacheLookbackAlwaysKeep(path string) bool {
	switch normalizePromptCachePath(path) {
	case "memory.md", "heartbeat.md", "skills.md":
		return true
	default:
		return false
	}
}

func normalizePromptCachePath(path string) string {
	return filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
}

func renderCacheLookbackOmissions(omitted []string) string {
	if len(omitted) == 0 {
		return ""
	}
	paths := make([]string, 0, len(omitted))
	for _, path := range omitted {
		path = strings.TrimSpace(path)
		if path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return ""
	}
	return "Cache-aware lookback omitted older dynamic files this turn: " + strings.Join(paths, ", ")
}
