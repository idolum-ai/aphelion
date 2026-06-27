package core

import (
	"fmt"
	"strings"
)

type DurableAgentWakeFailureClass string

const (
	DurableAgentWakeFailureClaimedParentBatchMissing DurableAgentWakeFailureClass = "claimed_parent_batch_missing"
	DurableAgentWakeFailureParentConversationPrepare DurableAgentWakeFailureClass = "parent_conversation_prepare_failed"
	DurableAgentWakeFailureTaskPacketAdmission       DurableAgentWakeFailureClass = "child_task_packet_admission_failed"
	DurableAgentWakeFailureTaskAttemptClaim          DurableAgentWakeFailureClass = "child_task_attempt_claim_failed"
	DurableAgentWakeFailureScopeSetup                DurableAgentWakeFailureClass = "child_scope_setup_failed"
)

type DurableAgentWakeFailureError struct {
	Class      DurableAgentWakeFailureClass
	AgentID    string
	MessageIDs []string
	Cause      error
}

func NewDurableAgentWakeClaimedParentBatchMissingError(agentID string, messageIDs []string) DurableAgentWakeFailureError {
	return NewDurableAgentWakeFailureError(DurableAgentWakeFailureClaimedParentBatchMissing, agentID, messageIDs, nil)
}

func NewDurableAgentWakeFailureError(class DurableAgentWakeFailureClass, agentID string, messageIDs []string, cause error) DurableAgentWakeFailureError {
	ids := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	return DurableAgentWakeFailureError{
		Class:      class,
		AgentID:    strings.TrimSpace(agentID),
		MessageIDs: ids,
		Cause:      cause,
	}
}

func (e DurableAgentWakeFailureError) Unwrap() error {
	return e.Cause
}

func (e DurableAgentWakeFailureError) Error() string {
	switch e.Class {
	case DurableAgentWakeFailureClaimedParentBatchMissing:
		return fmt.Sprintf("%s: claimed parent message batch is no longer pending for durable agent %q", e.Class, e.AgentID)
	case DurableAgentWakeFailureParentConversationPrepare:
		return fmt.Sprintf("%s: durable parent conversation wake could not prepare the claimed batch for durable agent %q", e.Class, e.AgentID)
	case DurableAgentWakeFailureTaskPacketAdmission:
		return fmt.Sprintf("%s: durable parent conversation wake could not admit a child task packet for durable agent %q", e.Class, e.AgentID)
	case DurableAgentWakeFailureTaskAttemptClaim:
		return fmt.Sprintf("%s: durable parent conversation wake could not claim child task ownership for durable agent %q", e.Class, e.AgentID)
	case DurableAgentWakeFailureScopeSetup:
		return fmt.Sprintf("%s: durable parent conversation wake could not prepare child runtime scope for durable agent %q", e.Class, e.AgentID)
	default:
		if e.Class != "" {
			return string(e.Class)
		}
		return "durable agent wake failed"
	}
}
