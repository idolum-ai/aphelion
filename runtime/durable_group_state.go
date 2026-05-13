//go:build linux

package runtime

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) markDurableAgentAwake(agentID string, cursorMessageID int64) error {
	state, err := r.store.DurableAgentState(agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agentID}
	}
	now := time.Now().UTC()
	state.Status = "awake"
	state.Cursor = strconv.FormatInt(cursorMessageID, 10)
	state.LastWakeAt = now
	state.DormantAt = time.Time{}
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	key := r.durableAgentExecutionKey(strings.TrimSpace(agentID))
	r.recordExecutionEvent(key, core.ExecutionEventDurableStateAwake, "durable", "awake", map[string]any{
		"agent_id":          strings.TrimSpace(agentID),
		"cursor_message_id": cursorMessageID,
	}, now)
	return nil
}

func (r *Runtime) tryMarkDurableAgentWakeAwake(agentID string, cursorMessageID int64) (bool, error) {
	now := time.Now().UTC()
	acquired, err := r.store.TryMarkDurableAgentAwake(
		strings.TrimSpace(agentID),
		strconv.FormatInt(cursorMessageID, 10),
		now,
		durableWakeAwakeLockStaleAfter,
	)
	if err != nil || !acquired {
		return acquired, err
	}
	key := r.durableAgentExecutionKey(strings.TrimSpace(agentID))
	r.recordExecutionEvent(key, core.ExecutionEventDurableStateAwake, "durable", "awake", map[string]any{
		"agent_id":          strings.TrimSpace(agentID),
		"cursor_message_id": cursorMessageID,
	}, now)
	return true, nil
}

func (r *Runtime) markDurableAgentDormant(agentID string) error {
	state, err := r.store.DurableAgentState(agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agentID}
	}
	now := time.Now().UTC()
	state.Status = "dormant"
	state.DormantAt = now
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	key := r.durableAgentExecutionKey(strings.TrimSpace(agentID))
	r.recordExecutionEvent(key, core.ExecutionEventDurableStateDormant, "durable", "dormant", map[string]any{
		"agent_id": strings.TrimSpace(agentID),
	}, now)
	return nil
}

func (r *Runtime) ensureDurableAgentPolicyOffered(agent core.DurableAgent) error {
	state, err := r.store.DurableAgentState(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agent.AgentID}
	}
	if state.LastOfferedPolicyVersion == agent.PolicyVersion && strings.TrimSpace(state.LastOfferedPolicyHash) == strings.TrimSpace(agent.PolicyHash) {
		return nil
	}
	state.LastOfferedPolicyVersion = agent.PolicyVersion
	state.LastOfferedPolicyHash = strings.TrimSpace(agent.PolicyHash)
	state.LastOfferedPolicyAt = nonZeroPolicyTime(agent.PolicyIssuedAt)
	if strings.TrimSpace(state.LastApplyStatus) == "" {
		state.LastApplyStatus = "pending"
	}
	state.LastApplyError = ""
	return r.store.SaveDurableAgentState(*state)
}

func (r *Runtime) markDurableAgentPolicyApplied(agent core.DurableAgent) error {
	state, err := r.store.DurableAgentState(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agent.AgentID}
	}
	now := time.Now().UTC()
	state.LastOfferedPolicyVersion = agent.PolicyVersion
	state.LastOfferedPolicyHash = strings.TrimSpace(agent.PolicyHash)
	if state.LastOfferedPolicyAt.IsZero() {
		state.LastOfferedPolicyAt = nonZeroPolicyTime(agent.PolicyIssuedAt)
	}
	state.LastAcknowledgedPolicyVersion = agent.PolicyVersion
	state.LastAcknowledgedPolicyHash = strings.TrimSpace(agent.PolicyHash)
	state.LastAcknowledgedPolicyAt = now
	state.LastAppliedPolicyVersion = agent.PolicyVersion
	state.LastAppliedPolicyHash = strings.TrimSpace(agent.PolicyHash)
	state.LastAppliedPolicyAt = now
	state.LastApplyStatus = "applied"
	state.LastApplyError = ""
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	key := r.durableAgentExecutionKey(strings.TrimSpace(agent.AgentID))
	r.recordExecutionEvent(key, core.ExecutionEventDurablePolicyApplied, "durable", "applied", map[string]any{
		"agent_id":       strings.TrimSpace(agent.AgentID),
		"policy_version": agent.PolicyVersion,
		"policy_hash":    strings.TrimSpace(agent.PolicyHash),
	}, now)
	return nil
}

func (r *Runtime) markDurableAgentPolicyApplyFailure(agent core.DurableAgent, cause error) error {
	state, err := r.store.DurableAgentState(agent.AgentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == nil {
		state = &core.DurableAgentState{AgentID: agent.AgentID}
	}
	state.LastOfferedPolicyVersion = agent.PolicyVersion
	state.LastOfferedPolicyHash = strings.TrimSpace(agent.PolicyHash)
	if state.LastOfferedPolicyAt.IsZero() {
		state.LastOfferedPolicyAt = nonZeroPolicyTime(agent.PolicyIssuedAt)
	}
	state.LastApplyStatus = "failed"
	state.LastApplyError = strings.TrimSpace(cause.Error())
	if err := r.store.SaveDurableAgentState(*state); err != nil {
		return err
	}
	now := time.Now().UTC()
	key := r.durableAgentExecutionKey(strings.TrimSpace(agent.AgentID))
	r.recordExecutionEvent(key, core.ExecutionEventDurablePolicyApplyFailed, "durable", "failed", map[string]any{
		"agent_id":       strings.TrimSpace(agent.AgentID),
		"policy_version": agent.PolicyVersion,
		"policy_hash":    strings.TrimSpace(agent.PolicyHash),
		"error":          trimError(cause.Error()),
	}, now)
	return nil
}

func (r *Runtime) durableAgentExecutionKey(agentID string) session.SessionKey {
	agentID = strings.TrimSpace(agentID)
	if r == nil || r.store == nil || agentID == "" {
		return session.SessionKey{Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agentID,
			DurableAgentID: agentID,
		}}
	}
	agent, err := r.store.DurableAgent(agentID)
	if err != nil || agent == nil {
		return session.SessionKey{Scope: session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agentID,
			DurableAgentID: agentID,
		}}
	}
	key := session.SessionKey{
		ChatID: agent.ReviewTargetChatID,
		Scope:  durableAgentScopeRef(*agent),
	}
	if key.Scope.IsZero() {
		key.Scope = session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agentID,
			DurableAgentID: agentID,
		}
	}
	return key
}

func nonZeroPolicyTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}
