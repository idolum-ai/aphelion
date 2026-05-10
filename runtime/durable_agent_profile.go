//go:build linux

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/tool/sandbox"
)

const durableAgentProfileMaxChars = 12000

var durableAgentProfileFiles = []string{
	"charter.md",
	"persona.md",
	"policy.md",
	"runtime.md",
	"surface-rules.md",
	"capabilities.md",
	"capability-ledger.md",
	"growth.md",
	"scorecard.md",
	"skills.md",
	"notes.md",
}

func durableAgentProfileContext(scope sandbox.Scope, agent core.DurableAgent) string {
	root := durableAgentProfileRoot(scope)
	if root == "" {
		return ""
	}
	files := make([]string, 0, len(durableAgentProfileFiles))
	for _, name := range durableAgentProfileFiles {
		path := filepath.Join(root, filepath.FromSlash(name))
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return ""
	}

	remaining := durableAgentProfileMaxChars
	sections := []string{
		"External durable child profile files.",
		"These files are parent/child-managed runtime material, not parent harness source.",
		"Durable agent id: " + strings.TrimSpace(agent.AgentID),
	}
	for _, path := range files {
		if remaining <= 0 {
			break
		}
		content, err := readDurableAgentProfileFile(root, path, remaining)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		sections = append(sections, fmt.Sprintf("### profile/%s\n%s", filepath.ToSlash(rel), content))
		remaining -= len(content)
	}
	if len(sections) <= 3 {
		return ""
	}
	return strings.Join(sections, "\n\n")
}

func durableAgentProfileRoot(scope sandbox.Scope) string {
	root := strings.TrimSpace(scope.SharedMemoryRoot)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "profile")
}

func readDurableAgentProfileFile(root string, path string, limit int) (string, error) {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == "." || path == "." {
		return "", nil
	}
	if rel, err := filepath.Rel(root, path); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("profile path escapes root")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(raw))
	if content == "" {
		return "", nil
	}
	if limit > 0 && len(content) > limit {
		return strings.TrimSpace(content[:limit]) + "\n...[truncated]...", nil
	}
	return content, nil
}
