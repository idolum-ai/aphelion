//go:build linux

package sandbox

import (
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/principal"
)

func DurableAgentScope(agentID string, globalRoot string, workingRoot string, memoryRoot string, networkPolicy string) (Scope, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return Scope{}, fmt.Errorf("durable agent id is required")
	}

	resolvedGlobalRoot, err := resolveRootPath("global_root", globalRoot)
	if err != nil {
		return Scope{}, err
	}
	resolvedWorkingRoot, err := resolveRootPath("working_root", workingRoot)
	if err != nil {
		return Scope{}, err
	}
	resolvedMemoryRoot, err := resolveRootPath("shared_memory_root", memoryRoot)
	if err != nil {
		return Scope{}, err
	}

	profile, err := DefaultProfiles().ForRole(principal.RoleDurableAgent)
	if err != nil {
		return Scope{}, err
	}
	profile.Network = durableAgentNetworkPolicy(networkPolicy)

	return Scope{
		Principal: principal.Principal{
			Role:           principal.RoleDurableAgent,
			DurableAgentID: agentID,
		},
		Profile:          profile,
		GlobalRoot:       resolvedGlobalRoot,
		SharedMemoryRoot: resolvedMemoryRoot,
		UserWorkspace:    resolvedWorkingRoot,
		UserMemory:       resolvedMemoryRoot,
		WorkingRoot:      resolvedWorkingRoot,
	}, nil
}

func durableAgentNetworkPolicy(value string) NetworkPolicy {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "deny", "restricted", "disabled":
		return NetworkDeny
	case "allowlist":
		return NetworkAllowlist
	default:
		return NetworkDeny
	}
}
