//go:build linux

package durableagent

import (
	"path/filepath"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

func DefaultLocalRoots(sessionsDBPath string, agentID string) (workspaceRoot string, memoryRoot string) {
	agentID = strings.TrimSpace(agentID)
	if err := core.ValidateDurableAgentID(agentID); err != nil {
		return "", ""
	}
	stateRoot := filepath.Dir(strings.TrimSpace(sessionsDBPath))
	base := filepath.Join(stateRoot, "durable_agents", agentID)
	return filepath.Join(base, "workspace"), filepath.Join(base, "memory")
}

func LocalRoots(agentID string, configured []string) (workspaceRoot string, memoryRoot string) {
	if err := core.ValidateDurableAgentID(agentID); err != nil {
		return "", ""
	}
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
