//go:build linux

package face

import (
	"fmt"
	"strings"
	"sync"

	"github.com/idolum-ai/aphelion/workspace"
)

type promptFileCache struct {
	mu      sync.Mutex
	entries map[string]promptFileCacheEntry
}

type promptFileCacheEntry struct {
	fingerprint string
	files       []workspace.LoadedFile
}

func newPromptFileCache() *promptFileCache {
	return &promptFileCache{entries: make(map[string]promptFileCacheEntry)}
}

func (c *promptFileCache) loadStable(workspaceRoot string) ([]workspace.LoadedFile, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return nil, nil
	}
	if c == nil {
		return loadFaceFiles(root, defaultStableFiles, false)
	}
	fingerprint, err := faceStableFingerprint(root)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if entry, ok := c.entries[root]; ok && entry.fingerprint == fingerprint {
		files := cloneLoadedFiles(entry.files)
		c.mu.Unlock()
		return files, nil
	}
	c.mu.Unlock()

	files, err := loadFaceFiles(root, defaultStableFiles, false)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.entries[root] = promptFileCacheEntry{
		fingerprint: fingerprint,
		files:       cloneLoadedFiles(files),
	}
	c.mu.Unlock()
	return files, nil
}

func (c *promptFileCache) invalidate(workspaceRoot string) {
	if c == nil {
		return
	}
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return
	}
	c.mu.Lock()
	delete(c.entries, root)
	c.mu.Unlock()
}

func faceStableFingerprint(workspaceRoot string) (string, error) {
	fingerprints, err := workspace.PromptFileFingerprints(workspaceRoot, defaultStableFiles)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(fingerprints)+1)
	parts = append(parts, strings.TrimSpace(workspaceRoot))
	for _, fp := range fingerprints {
		parts = append(parts, fmt.Sprintf("%s:%d:%d", fp.Path, fp.Size, fp.ModTimeUnixNano))
	}
	return strings.Join(parts, "\x00"), nil
}

func cloneLoadedFiles(files []workspace.LoadedFile) []workspace.LoadedFile {
	if len(files) == 0 {
		return nil
	}
	out := make([]workspace.LoadedFile, len(files))
	copy(out, files)
	return out
}
