package core

import (
	"fmt"
	"strings"
)

type DurableAgentWakeFailureClass string

const (
	DurableAgentWakeFailureClaimedParentBatchMissing DurableAgentWakeFailureClass = "claimed_parent_batch_missing"
)

type DurableAgentWakeFailureError struct {
	Class      DurableAgentWakeFailureClass
	AgentID    string
	MessageIDs []string
}

func NewDurableAgentWakeClaimedParentBatchMissingError(agentID string, messageIDs []string) DurableAgentWakeFailureError {
	ids := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return DurableAgentWakeFailureError{
		Class:      DurableAgentWakeFailureClaimedParentBatchMissing,
		AgentID:    strings.TrimSpace(agentID),
		MessageIDs: ids,
	}
}

func (e DurableAgentWakeFailureError) Error() string {
	switch e.Class {
	case DurableAgentWakeFailureClaimedParentBatchMissing:
		return fmt.Sprintf("%s: claimed parent message batch is no longer pending for durable agent %q", e.Class, e.AgentID)
	default:
		if e.Class != "" {
			return string(e.Class)
		}
		return "durable agent wake failed"
	}
}
