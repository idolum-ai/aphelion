//go:build linux

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
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

func (r *Registry) wakeDurableAgentOnce(ctx context.Context, in durableAgentInput, rawInput json.RawMessage, p principal.Principal, key session.SessionKey) (out string, err error) {
	if r.durableAgentWakeRunner == nil {
		return "", fmt.Errorf("durable_agent wake_once requires durable child wake runtime")
	}
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for wake_once")
	}
	agent, err := r.resolveDurableAgentExact(agentID)
	if err != nil {
		return "", err
	}
	grant, err := r.requireDurableAgentWakeOnceCapabilityGrant(agent.AgentID, rawInput, p)
	if err != nil {
		return "", err
	}
	useRef, err := r.requireDurableAgentWakeOnceAuthority(ctx, p, key, agent.AgentID)
	if err != nil {
		return "", missingContinuationLeaseError{
			requirement: durableAgentWakeOnceLeaseRequirement(agent.AgentID, grant, p),
			cause:       err,
		}
	}
	permit, err := r.recordDurableAgentWakeOnceCapabilityInvocation(grant, p, useRef)
	if err != nil {
		return "", err
	}
	defer func() {
		if recordErr := r.recordAuthorityManagedToolOutcome(permit, wakeOnceCapabilityOutcomeStatus(err), wakeOnceCapabilityOutcomeError(err)); recordErr != nil && err == nil {
			err = recordErr
		}
	}()

	_, beforeContinuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	beforePendingMessages := beforeContinuity.PendingParentConversationMessages(0)
	beforePending := len(beforePendingMessages)
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

	messageIDs := core.DurableAgentConversationMessageIDs(beforePendingMessages)
	now := time.Now().UTC()
	if _, err := r.store.ClaimDurableAgentWakeOnce(session.DurableAgentWakeClaimInput{
		LeaseID:          useRef.ContinuationLeaseID,
		AgentID:          agent.AgentID,
		TurnRunID:        useRef.TurnRunID,
		MessageBatchHash: session.DurableAgentWakeMessageBatchHash(agent.AgentID, messageIDs),
		MessageIDs:       messageIDs,
		CreatedAt:        now,
	}); err != nil {
		return "", err
	}

	wakeErr := r.durableAgentWakeRunner.RunDurableAgentParentConversationWake(ctx, agent.AgentID, messageIDs, now)
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

func (r *Registry) requireDurableAgentWakeOnceCapabilityGrant(agentID string, input json.RawMessage, p principal.Principal) (session.CapabilityGrant, error) {
	contract := durableAgentWakeOnceGrantContract(agentID, p)
	grant, ok, err := r.activeGrantForMissingGrantContract(contract, input)
	if err != nil {
		return session.CapabilityGrant{}, err
	}
	if ok {
		return grant, nil
	}
	cause := fmt.Errorf("durable_agent wake_once does not have an active exact invoke grant for agent_id %q", strings.TrimSpace(agentID))
	return session.CapabilityGrant{}, missingGrantError{
		contract: contract,
		cause:    cause,
	}
}

func durableAgentWakeOnceCapabilityTarget(agentID string) string {
	return "durable_agent:" + strings.TrimSpace(agentID) + ":wake_once"
}

func durableAgentWakeOnceMissingGrantRequirement(agentID string, p principal.Principal) missingGrantRequirement {
	return durableAgentWakeOnceGrantContract(agentID, p).Requirement
}

func durableAgentWakeOnceGrantContract(agentID string, p principal.Principal) missingGrantContract {
	agentID = strings.TrimSpace(agentID)
	grantedTo := toolAuthorityCanonicalPrincipal(p)
	contract := compactJSON(map[string]any{
		"bounded_effect": "Allow invoking durable_agent wake_once for the named child only. The continuation child_wake lease still bounds each wake attempt and supplies the one-turn execution authority.",
		"tool_name":      "durable_agent",
		"tool_action":    "wake_once",
		"agent_id":       agentID,
	})
	constraints := compactJSON(map[string]any{
		"tool_invocation": map[string]any{
			"actions": map[string]any{
				"wake_once": map[string]any{
					"selectors": map[string]any{
						"agent_id": []string{agentID},
					},
					"required_selectors":      []string{"agent_id"},
					"allowed_fields":          []string{"reason"},
					"allow_additional_fields": false,
				},
			},
		},
	})
	requirement := missingGrantRequirement{
		Kind:               session.CapabilityKindGenericDelegation,
		TargetResource:     durableAgentWakeOnceCapabilityTarget(agentID),
		GrantedTo:          grantedTo,
		AllowedActions:     []string{"invoke"},
		Contract:           contract,
		Constraints:        constraints,
		Purpose:            fmt.Sprintf("Allow exactly scoped durable_agent wake_once invocations for child %s; execution still requires a current child_wake continuation lease.", agentID),
		RiskClass:          "authority",
		ReviewSummary:      fmt.Sprintf("Approve durable_agent wake_once for child=%s requested_for=%s", agentID, grantedTo),
		OperatorProjection: fmt.Sprintf("durable_agent wake_once for %s is blocked because %s lacks an exact active invoke grant. Review this request; after approval and grant materialization, retry the one wake attempt under a child_wake lease.", agentID, grantedTo),
		OperationKind:      "capability_grant_review",
		OperationTool:      "capability_authority",
	}
	return missingGrantContract{
		Requirement:        requirement,
		AcceptedPrincipals: toolAuthorityPrincipalIDs(p),
		AcceptedGrantShapes: []missingGrantAcceptedShape{
			{
				Kind:                session.CapabilityKindGenericDelegation,
				TargetResource:      durableAgentWakeOnceCapabilityTarget(agentID),
				Action:              "invoke",
				ToolInvocationScope: missingGrantToolInvocationScopeOptional,
				RequiredConstraints: map[string]string{"agent_id": agentID},
			},
			{
				Kind:                session.CapabilityKindGenericDelegation,
				TargetResource:      "durable_agent:wake_once",
				Action:              "invoke",
				ToolInvocationScope: missingGrantToolInvocationScopeOptional,
				RequiredConstraints: map[string]string{"agent_id": agentID},
			},
			{
				Kind:                session.CapabilityKindTool,
				TargetResource:      "durable_agent",
				Action:              "invoke",
				ToolInvocationScope: missingGrantToolInvocationScopeRequired,
			},
		},
	}
}

func (r *Registry) recordDurableAgentWakeOnceCapabilityInvocation(grant session.CapabilityGrant, p principal.Principal, useRef session.AuthorityUseRef) (*authorityInvocationPermit, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("durable_agent wake_once capability invocation requires transcript store")
	}
	principalID := toolAuthorityPrincipalDisplay(p)
	invocation, err := r.store.RecordCapabilityInvocation(capabilityInvocationWithAuthorityUseRef(session.CapabilityInvocation{
		GrantID:   grant.GrantID,
		Principal: principalID,
		Action:    "invoke",
		Status:    "allowed",
	}, useRef))
	if err != nil {
		return nil, err
	}
	return &authorityInvocationPermit{
		InvocationID: invocation.InvocationID,
		Grant:        grant,
		Principal:    principalID,
		Action:       "invoke",
		UseRef:       useRef,
	}, nil
}

func wakeOnceCapabilityOutcomeStatus(err error) string {
	if err != nil {
		return "failed"
	}
	return "completed"
}

func wakeOnceCapabilityOutcomeError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (r *Registry) requireDurableAgentWakeOnceAuthority(ctx context.Context, p principal.Principal, key session.SessionKey, agentID string) (session.AuthorityUseRef, error) {
	useRef, err := r.authorityUseRefForGrant(ctx, "durable_agent wake_once", key, p)
	if err != nil {
		return session.AuthorityUseRef{}, err
	}
	if useRef.AuthoritySource != session.ExecutionAuthorityLeaseKindContinuation {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires continuation child_wake authority")
	}
	authority, ok, err := r.store.ExecutionRunAuthority(useRef.TurnRunID)
	if err != nil {
		return session.AuthorityUseRef{}, fmt.Errorf("load durable_agent wake_once run authority: %w", err)
	}
	if !ok {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires durable execution authority")
	}
	if authority.LeaseStatus != string(session.ContinuationLeaseStatusActive) || authority.LeaseRemainingTurns <= 0 {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires a run admitted with active child_wake authority")
	}
	now := time.Now().UTC()
	if !authority.LeaseExpiresAt.IsZero() && !authority.LeaseExpiresAt.After(now) {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once continuation lease expired")
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
	if !lease.ExpiresAt.IsZero() && !lease.ExpiresAt.After(now) {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once continuation lease expired")
	}
	if authority.LeaseClass != session.ContinuationLeaseClassChildWake {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires child_wake lease class")
	}
	if !durableAgentWakeActionsAllow(authority.LeaseAllowedActions, lease.ForbiddenActions, durableAgentWakeOnceAction) && !durableAgentWakeActionsAllow(authority.LeaseAllowedActions, lease.ForbiddenActions, "request_child_wake") {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once requires child wake action authority")
	}
	if !durableAgentWakeConstraintsAllowAgent(authority.LeaseConstraints, agentID) {
		return session.AuthorityUseRef{}, fmt.Errorf("durable_agent wake_once lease is not bound to agent_id %q", strings.TrimSpace(agentID))
	}
	return useRef, nil
}

func durableAgentWakeActionsAllow(allowed []string, forbidden []string, action string) bool {
	action = durableAgentWakeActionToken(action)
	if action == "" {
		return false
	}
	for _, value := range forbidden {
		if durableAgentWakeActionToken(value) == action {
			return false
		}
	}
	for _, value := range allowed {
		if durableAgentWakeActionToken(value) == action {
			return true
		}
	}
	return false
}

func durableAgentWakeActionToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func durableAgentWakeConstraintsAllowAgent(constraints map[string]string, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false
	}
	for _, key := range []string{"agent_id", "durable_agent_id", "child_agent_id", "target_agent_id"} {
		if strings.TrimSpace(constraints[key]) == agentID {
			return true
		}
	}
	return false
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
