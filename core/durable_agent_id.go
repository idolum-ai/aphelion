//go:build linux

package core

import (
	"fmt"
	"strings"
)

const maxDurableAgentIDLength = 96

func ValidateDurableAgentID(agentID string) error {
	agentID = strings.TrimSpace(agentID)
	switch {
	case agentID == "":
		return fmt.Errorf("durable agent id is required")
	case len(agentID) > maxDurableAgentIDLength:
		return fmt.Errorf("durable agent id %q is too long", agentID)
	case agentID == "." || agentID == "..":
		return fmt.Errorf("durable agent id %q is not allowed", agentID)
	case strings.ContainsAny(agentID, "/\\\x00"):
		return fmt.Errorf("durable agent id %q must not contain path separators", agentID)
	}
	for _, r := range agentID {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("durable agent id %q must contain only letters, digits, hyphen, or underscore", agentID)
		}
	}
	return nil
}
