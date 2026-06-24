//go:build linux

package tool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

const durableAgentWakeOnceAction = "wake_named_child"

type durableAgentWakeOnceResult struct {
	AgentID                string
	WakeStatus             string
	PendingParentBefore    int
	PendingParentAfter     int
	ThreadStateBefore      string
	ThreadStateAfter       string
	LastParentMessageAt    time.Time
	LastChildMessageAt     time.Time
	LastParentAcknowledged time.Time
	AuthoritySource        string
	ContinuationLeaseID    string
	ErrorText              string
}

func (r *Registry) wakeDurableAgentOnce(ctx context.Context, in durableAgentInput, p principal.Principal, key session.SessionKey) (string, error) {
	if r.durableAgentWakeRunner == nil {
		return "", fmt.Errorf("durable_agent wake_once requires durable child wake runtime")
	}
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wake_once")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	useRef, err := r.requireDurableAgentWakeOnceAuthority(ctx, p, key)
	if err != nil {
		return "", err
	}

	_, beforeContinuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	beforePending := len(beforeContinuity.PendingParentConversationMessages(0))
	beforeState, lastParentAt, _, _, _ := durableAgentConversationState(beforeContinuity)
	result := durableAgentWakeOnceResult{
		AgentID:             agent.AgentID,
		PendingParentBefore: beforePending,
		ThreadStateBefore:   beforeState,
		LastParentMessageAt: lastParentAt,
		AuthoritySource:     useRef.AuthoritySource,
		ContinuationLeaseID: useRef.ContinuationLeaseID,
	}
	if beforePending == 0 {
		result.WakeStatus = "skipped_no_pending_parent_message"
		result.PendingParentAfter = beforePending
		result.ThreadStateAfter = beforeState
		return renderDurableAgentWakeOnce(result), nil
	}

	wakeErr := r.durableAgentWakeRunner.RunDurableAgentChildWake(ctx, agent.AgentID, time.Now().UTC())
	_, afterContinuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	afterState, _, lastChildAt, lastAckAt, _ := durableAgentConversationState(afterContinuity)
	result.PendingParentAfter = len(afterContinuity.PendingParentConversationMessages(0))
	result.ThreadStateAfter = afterState
	result.LastChildMessageAt = lastChildAt
	result.LastParentAcknowledged = lastAckAt
	if wakeErr != nil {
		result.WakeStatus = "failed"
		result.ErrorText = wakeErr.Error()
	} else if result.PendingParentAfter > 0 {
		result.WakeStatus = "awaiting_child_pickup"
	} else {
		result.WakeStatus = "completed"
	}
	return renderDurableAgentWakeOnce(result), nil
}

func (r *Registry) requireDurableAgentWakeOnceAuthority(ctx context.Context, p principal.Principal, key session.SessionKey) (session.AuthorityUseRef, error) {
	useRef, err := r.authorityUseRefForGrant(ctx, "durable_agent wake_once", key, p)
	if err != nil {
		return session.AuthorityUseRef{}, err
	}
	if useRef.AuthoritySource != session.ExecutionAuthorityLeaseKindContinuation {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires continuation child_wake authority")
	}
	state, ok, err := r.store.ContinuationStateIfExists(key)
	if err != nil {
		return session.AuthorityUseRef{}, err
	}
	if !ok {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires active continuation child_wake lease")
	}
	lease := session.NormalizeContinuationLease(state.ContinuationLease)
	if strings.TrimSpace(lease.ID) == "" || lease.ID != useRef.ContinuationLeaseID {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once authority lease mismatch")
	}
	if lease.LeaseClass != session.ContinuationLeaseClassChildWake {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires child_wake lease class")
	}
	now := time.Now().UTC()
	decision := session.CheckContinuationLeaseAction(lease, durableAgentWakeOnceAction, now)
	if !decision.Allowed {
		fallback := session.CheckContinuationLeaseAction(lease, "request_child_wake", now)
		if !fallback.Allowed {
			return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires child wake action authority")
		}
	}
	return useRef, nil
}

func renderDurableAgentWakeOnce(result durableAgentWakeOnceResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "action: durable-agent wake_once\n")
	fmt.Fprintf(&b, "agent_id: %s\n", strings.TrimSpace(result.AgentID))
	fmt.Fprintf(&b, "wake_status: %s\n", strings.TrimSpace(result.WakeStatus))
	fmt.Fprintf(&b, "pending_parent_before: %d\n", result.PendingParentBefore)
	fmt.Fprintf(&b, "pending_parent_after: %d\n", result.PendingParentAfter)
	fmt.Fprintf(&b, "thread_state_before: %s\n", strings.TrimSpace(result.ThreadStateBefore))
	fmt.Fprintf(&b, "thread_state_after: %s\n", strings.TrimSpace(result.ThreadStateAfter))
	if !result.LastParentMessageAt.IsZero() {
		fmt.Fprintf(&b, "last_parent_message_at: %s\n", result.LastParentMessageAt.UTC().Format(time.RFC3339))
	}
	if !result.LastChildMessageAt.IsZero() {
		fmt.Fprintf(&b, "last_child_message_at: %s\n", result.LastChildMessageAt.UTC().Format(time.RFC3339))
	}
	if !result.LastParentAcknowledged.IsZero() {
		fmt.Fprintf(&b, "last_parent_acknowledged_at: %s\n", result.LastParentAcknowledged.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "authority_source: %s\n", strings.TrimSpace(result.AuthoritySource))
	if strings.TrimSpace(result.ContinuationLeaseID) != "" {
		fmt.Fprintf(&b, "continuation_lease_id: %s\n", strings.TrimSpace(result.ContinuationLeaseID))
	}
	if strings.TrimSpace(result.ErrorText) != "" {
		fmt.Fprintf(&b, "error: %s\n", truncateCompact(result.ErrorText, 220))
	}
	switch result.WakeStatus {
	case "skipped_no_pending_parent_message":
		b.WriteString("next: conversation_send\n")
	case "completed":
		b.WriteString("next: conversation_show\n")
	case "awaiting_child_pickup":
		b.WriteString("next: wait_for_child_result\n")
	case "failed":
		b.WriteString("next: inspect_child_runtime\n")
	default:
		b.WriteString("next: conversation_show\n")
	}
	return b.String()
}
