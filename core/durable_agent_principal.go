//go:build linux

package core

import "strings"

const DurableAgentPrincipalPrefix = "durable_agent:"

func DurableAgentPrincipal(agentID string) string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return ""
	}
	if strings.HasPrefix(agentID, DurableAgentPrincipalPrefix) {
		agentID = strings.TrimSpace(strings.TrimPrefix(agentID, DurableAgentPrincipalPrefix))
	}
	if agentID == "" {
		return ""
	}
	return DurableAgentPrincipalPrefix + agentID
}

func DurableAgentIDFromPrincipal(principal string) (string, bool) {
	principal = strings.TrimSpace(principal)
	if !strings.HasPrefix(principal, DurableAgentPrincipalPrefix) {
		return "", false
	}
	agentID := strings.TrimSpace(strings.TrimPrefix(principal, DurableAgentPrincipalPrefix))
	if agentID == "" {
		return "", false
	}
	return agentID, true
}
