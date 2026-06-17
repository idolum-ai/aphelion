//go:build linux

package runtime

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/workspace"
)

type promptStableContextCache struct {
	mu      sync.Mutex
	entries map[string]promptStableContextCacheEntry
	hits    int
	misses  int
}

type promptStableContextCacheEntry struct {
	fingerprint string
	stable      []workspace.LoadedFile
}

func newPromptStableContextCache() *promptStableContextCache {
	return &promptStableContextCache{entries: make(map[string]promptStableContextCacheEntry)}
}

func (c *promptStableContextCache) load(cfg config.AgentConfig, now time.Time) (*workspace.PromptContext, error) {
	if c == nil {
		return workspace.LoadPromptContext(cfg, now)
	}
	key := promptStableContextCacheKey(cfg)
	fingerprint, err := promptStableContextFingerprint(cfg)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && entry.fingerprint == fingerprint {
		c.hits++
		stable := cloneLoadedFiles(entry.stable)
		c.mu.Unlock()
		return &workspace.PromptContext{Workspace: cfg.Workspace, Stable: stable}, nil
	}
	c.mu.Unlock()

	loaded, err := workspace.LoadPromptContext(cfg, now)
	if err != nil {
		return nil, err
	}
	stable := cloneLoadedFiles(loaded.Stable)

	c.mu.Lock()
	c.entries[key] = promptStableContextCacheEntry{
		fingerprint: fingerprint,
		stable:      cloneLoadedFiles(stable),
	}
	c.misses++
	c.mu.Unlock()

	return &workspace.PromptContext{Workspace: loaded.Workspace, Stable: stable}, nil
}

func (c *promptStableContextCache) invalidateWorkspace(workspaceRoot string) {
	if c == nil {
		return
	}
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return
	}
	c.mu.Lock()
	for key := range c.entries {
		if strings.HasPrefix(key, root+"\x00") {
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
}

func (c *promptStableContextCache) stats() (hits int, misses int) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses
}

func promptStableContextCacheKey(cfg config.AgentConfig) string {
	return strings.Join([]string{
		strings.TrimSpace(cfg.Workspace),
		strings.Join(trimmedPromptFileNames(cfg.BootstrapFiles), "\x1f"),
		fmt.Sprintf("%d", cfg.BootstrapMaxChars),
		fmt.Sprintf("%d", cfg.BootstrapTotalMaxChars),
	}, "\x00")
}

func promptStableContextFingerprint(cfg config.AgentConfig) (string, error) {
	fingerprints, err := workspace.PromptFileFingerprints(cfg.Workspace, cfg.BootstrapFiles)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(fingerprints)+1)
	parts = append(parts, promptStableContextCacheKey(cfg))
	for _, fp := range fingerprints {
		parts = append(parts, fmt.Sprintf("%s:%d:%d", fp.Path, fp.Size, fp.ModTimeUnixNano))
	}
	return strings.Join(parts, "\x00"), nil
}

func trimmedPromptFileNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func cloneLoadedFiles(files []workspace.LoadedFile) []workspace.LoadedFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]workspace.LoadedFile, len(files))
	copy(out, files)
	return out
}
