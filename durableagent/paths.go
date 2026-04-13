//go:build linux

package durableagent

import (
	"path/filepath"
	"strings"
)

func DefaultLocalRoots(sessionsDBPath string, agentID string) (workspaceRoot string, memoryRoot string) {
	stateRoot := filepath.Dir(strings.TrimSpace(sessionsDBPath))
	base := filepath.Join(stateRoot, "durable_agents", strings.TrimSpace(agentID))
	return filepath.Join(base, "workspace"), filepath.Join(base, "memory")
}

func LocalRoots(agentID string, configured []string) (workspaceRoot string, memoryRoot string) {
	if len(configured) >= 2 {
		return strings.TrimSpace(configured[0]), strings.TrimSpace(configured[1])
	}
	if len(configured) == 1 {
		base := strings.TrimSpace(configured[0])
		if base != "" {
			return filepath.Join(base, "workspace"), filepath.Join(base, "memory")
		}
	}
	return "", ""
}
