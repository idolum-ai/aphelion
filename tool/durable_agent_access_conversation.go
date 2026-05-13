//go:build linux

package tool

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
)

func (r *Registry) loadDurableAgentContinuity(agentID string) (*core.DurableAgentState, core.DurableAgentContinuityState, error) {
	state, err := r.store.DurableAgentState(agentID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, core.DurableAgentContinuityState{}, err
		}
		state = &core.DurableAgentState{AgentID: strings.TrimSpace(agentID)}
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, core.DurableAgentContinuityState{}, err
	}
	return state, continuity, nil
}

func (r *Registry) saveDurableAgentContinuity(state *core.DurableAgentState, continuity core.DurableAgentContinuityState) error {
	if state == nil {
		return fmt.Errorf("durable agent continuity state is nil")
	}
	raw, err := continuity.Marshal()
	if err != nil {
		return err
	}
	state.StateJSON = raw
	return r.store.SaveDurableAgentState(*state)
}

func (r *Registry) showDurableAgentAccess(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for access_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	return renderDurableAgentAccess("show", *agent, nil, false), nil
}

func (r *Registry) grantDurableAgentAccess(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for access_grant")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	requested, err := durableAgentAccessUserIDs(in)
	if err != nil {
		return "", err
	}
	combined := append(append([]int64(nil), agent.AllowedTelegramUserIDs...), requested...)
	next := core.NormalizeDurableAgentAllowedTelegramUserIDs(combined)
	changed := !equalInt64Slices(agent.AllowedTelegramUserIDs, next)
	agent.AllowedTelegramUserIDs = next
	if changed {
		if err := r.store.UpsertDurableAgent(*agent); err != nil {
			return "", err
		}
	}
	return renderDurableAgentAccess("grant", *agent, requested, changed), nil
}

func (r *Registry) revokeDurableAgentAccess(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for access_revoke")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	requested, err := durableAgentAccessUserIDs(in)
	if err != nil {
		return "", err
	}
	remove := make(map[int64]struct{}, len(requested))
	for _, userID := range requested {
		remove[userID] = struct{}{}
	}
	next := make([]int64, 0, len(agent.AllowedTelegramUserIDs))
	for _, userID := range core.NormalizeDurableAgentAllowedTelegramUserIDs(agent.AllowedTelegramUserIDs) {
		if _, drop := remove[userID]; drop {
			continue
		}
		next = append(next, userID)
	}
	next = core.NormalizeDurableAgentAllowedTelegramUserIDs(next)
	changed := !equalInt64Slices(agent.AllowedTelegramUserIDs, next)
	agent.AllowedTelegramUserIDs = next
	if changed {
		if err := r.store.UpsertDurableAgent(*agent); err != nil {
			return "", err
		}
	}
	return renderDurableAgentAccess("revoke", *agent, requested, changed), nil
}

func (r *Registry) showDurableAgentConversation(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for conversation_show")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	_, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	return renderDurableAgentConversation("show", *agent, continuity, in.History), nil
}

func (r *Registry) sendDurableAgentConversation(in durableAgentInput) (string, error) {
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		return "", fmt.Errorf("durable_agent agent_id is required for conversation_send")
	}
	message := strings.TrimSpace(in.Message)
	if message == "" {
		return "", fmt.Errorf("durable_agent message is required for conversation_send")
	}
	agent, err := r.resolveDurableAgent(agentID)
	if err != nil {
		return "", err
	}
	state, continuity, err := r.loadDurableAgentContinuity(agent.AgentID)
	if err != nil {
		return "", err
	}
	continuity = continuity.WithConversationMessage("parent", message, time.Now().UTC())
	if err := r.saveDurableAgentContinuity(state, continuity); err != nil {
		return "", err
	}
	return renderDurableAgentConversation("send", *agent, continuity, in.History), nil
}

func durableAgentAccessUserIDs(in durableAgentInput) ([]int64, error) {
	values := make([]int64, 0, len(in.TelegramUserIDs)+1)
	if in.TelegramUserID != 0 {
		values = append(values, in.TelegramUserID)
	}
	values = append(values, in.TelegramUserIDs...)
	values = core.NormalizeDurableAgentAllowedTelegramUserIDs(values)
	if len(values) == 0 {
		return nil, fmt.Errorf("durable_agent telegram_user_id or telegram_user_ids is required")
	}
	return values, nil
}

func equalInt64Slices(left []int64, right []int64) bool {
	left = core.NormalizeDurableAgentAllowedTelegramUserIDs(left)
	right = core.NormalizeDurableAgentAllowedTelegramUserIDs(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func durableAgentConversationWindow(continuity core.DurableAgentContinuityState, history int) []core.DurableAgentConversationMessage {
	continuity = core.NormalizeDurableAgentContinuityState(continuity)
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) == 0 {
		return nil
	}
	limit := history
	if limit <= 0 {
		limit = 8
	}
	if limit > 20 {
		limit = 20
	}
	if limit > len(continuity.Conversation.Messages) {
		limit = len(continuity.Conversation.Messages)
	}
	out := make([]core.DurableAgentConversationMessage, 0, limit)
	out = append(out, continuity.Conversation.Messages[:limit]...)
	return out
}

func durableAgentConversationState(continuity core.DurableAgentContinuityState) (state string, lastParentAt, lastChildAt, lastParentAckAt time.Time, lastChildError string) {
	continuity = core.NormalizeDurableAgentContinuityState(continuity)
	pending := len(continuity.PendingParentConversationMessages(0))
	if continuity.Conversation == nil || len(continuity.Conversation.Messages) == 0 {
		if pending > 0 {
			return "awaiting_child_pickup", time.Time{}, time.Time{}, time.Time{}, ""
		}
		return "idle", time.Time{}, time.Time{}, time.Time{}, ""
	}

	for _, message := range continuity.Conversation.Messages {
		switch strings.TrimSpace(message.Role) {
		case "parent":
			if lastParentAt.IsZero() {
				lastParentAt = message.CreatedAt.UTC()
			}
			if !message.AcknowledgedAt.IsZero() && lastParentAckAt.IsZero() {
				lastParentAckAt = message.AcknowledgedAt.UTC()
			}
		case "child":
			if lastChildAt.IsZero() {
				lastChildAt = message.CreatedAt.UTC()
			}
			if lastChildError == "" && durableAgentMessageIsInferenceUnavailable(message.Text) {
				lastChildError = strings.TrimSpace(message.Text)
			}
		}
	}

	switch {
	case pending > 0 && lastChildError != "":
		state = "retrying_after_inference_failure"
	case pending > 0:
		state = "awaiting_child_pickup"
	case !lastChildAt.IsZero() && lastChildError != "":
		state = "child_blocked_inference"
	case !lastChildAt.IsZero():
		state = "awaiting_parent_guidance"
	default:
		state = "conversation_open"
	}
	return state, lastParentAt, lastChildAt, lastParentAckAt, lastChildError
}

func durableAgentMessageIsInferenceUnavailable(text string) bool {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	return strings.Contains(text, "Inference backend is unavailable.") ||
		strings.Contains(text, "Inference backends are unavailable after retries and fallback.") ||
		strings.Contains(text, "Inference backends are unavailable after provider fallback attempts.") ||
		strings.Contains(lower, "provider_failure:") ||
		strings.Contains(lower, "provider failure:")
}
