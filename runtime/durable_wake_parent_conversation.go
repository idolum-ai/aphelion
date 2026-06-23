//go:build linux

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const durableParentConversationChatType = "durable_parent_conversation"

type durableParentConversationWakeAdapter struct{}

func newDurableParentConversationWakeAdapter() durableWakeIngressAdapter {
	return durableParentConversationWakeAdapter{}
}

func (durableParentConversationWakeAdapter) Name() string {
	return "parent_conversation"
}

func (durableParentConversationWakeAdapter) Supports(agent core.DurableAgent) bool {
	channelKind := strings.TrimSpace(agent.ChannelKind)
	return !strings.EqualFold(channelKind, scheduledReviewChannelKind)
}

func (durableParentConversationWakeAdapter) Prepare(_ context.Context, rt *Runtime, agent core.DurableAgent, now time.Time) (*durableWakeTurnPlan, error) {
	return prepareDurableParentConversationWakePlan(rt, agent, now, false)
}

func prepareDurableParentConversationWakePlan(rt *Runtime, agent core.DurableAgent, now time.Time, force bool) (*durableWakeTurnPlan, error) {
	if rt == nil || rt.store == nil {
		return nil, fmt.Errorf("parent conversation wake runtime is unavailable")
	}
	if strings.ToLower(strings.TrimSpace(agent.Status)) != "active" {
		return nil, nil
	}
	if mode := strings.TrimSpace(agent.WakeupMode); !force && mode != "" && !strings.EqualFold(mode, "poll") {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	pending, err := rt.pendingDurableAgentParentConversation(strings.TrimSpace(agent.AgentID), 5)
	if err != nil {
		return nil, fmt.Errorf("load parent conversation queue: %w", err)
	}
	if len(pending) == 0 {
		return nil, nil
	}

	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	taskPacketID := durableWakeTaskPacketIDForPending(agent.AgentID, pending, now)
	return &durableWakeTurnPlan{
		Channel:         "durable_parent_conversation",
		AuditChannel:    "durable_parent_conversation",
		Key:             key,
		SessionChatType: durableParentConversationChatType,
		SessionUserName: "parent",
		TaskPacketID:    taskPacketID,
		Inbound: core.InboundMessage{
			ChatID:         key.ChatID,
			ChatType:       durableParentConversationChatType,
			ChatTitle:      "durable-parent-conversation",
			SenderName:     "parent",
			Text:           durableParentConversationWakePrompt(agent, pending),
			MessageID:      durableWakeMessageID(now),
			DurableAgentID: strings.TrimSpace(agent.AgentID),
			Timestamp:      now,
		},
		PromptContextErrHint: "load durable parent conversation prompt context",
		PolicyReason:         "mapped from interactive face policy for durable parent conversation wakes",
		PersistenceErrCtx: turnCommitErrorContext{
			ConvertMessages: "convert durable parent conversation wake messages",
			LoadPlanState:   "load durable parent conversation wake plan state before save",
			LoadOperation:   "load durable parent conversation wake operation state before save",
			SaveSession:     "save durable parent conversation wake session",
			RecordOutbound:  "record durable parent conversation wake outbound reply",
		},
		SendErrCtx:   "send durable parent conversation wake reply",
		RecordErrCtx: "record durable parent conversation wake outbound reply",
		GovernorContext: func(agent core.DurableAgent, policy core.DurableAgentLivePolicy, _ core.InboundMessage, pending []core.DurableAgentConversationMessage) string {
			lines := []string{
				"You are handling a durable-agent parent conversation wake.",
				"No external channel ingress is included in this wake; focus on pending parent guidance.",
				"Respond with the concrete work completed and the next bounded step, within current charter limits.",
				"Do not claim channel actions that were not actually executed in this turn.",
			}
			if charter := strings.TrimSpace(policy.Charter); charter != "" {
				lines = append(lines, "Charter: "+charter)
			}
			lines = append(lines, "Durable agent id: "+strings.TrimSpace(agent.AgentID))
			lines = append(lines, "Channel kind: "+strings.TrimSpace(agent.ChannelKind))
			lines = append(lines, durableParentConversationGovernorLines(pending)...)
			return strings.Join(lines, "\n")
		},
	}, nil
}

func durableWakeTaskPacketIDForPending(agentID string, pending []core.DurableAgentConversationMessage, now time.Time) string {
	ids := core.DurableAgentConversationMessageIDs(pending)
	if len(ids) == 1 && strings.TrimSpace(ids[0]) != "" {
		return strings.TrimSpace(ids[0])
	}
	parts := []string{strings.TrimSpace(agentID)}
	for _, id := range ids {
		parts = append(parts, strings.TrimSpace(id))
	}
	if len(parts) > 1 {
		return "child_task:" + session.EffectAttemptCommandHash(strings.Join(parts, ":"))[7:23]
	}
	return durableWakeTaskPacketID(agentID, durableWakeMessageID(now), now)
}

func durableParentConversationWakePrompt(agent core.DurableAgent, messages []core.DurableAgentConversationMessage) string {
	lines := []string{
		"Durable parent conversation wake.",
		"Agent: " + strings.TrimSpace(agent.AgentID),
		"Channel: " + strings.TrimSpace(agent.ChannelKind),
		fmt.Sprintf("Pending parent messages: %d", len(messages)),
		"Process pending parent guidance and report a concise, truthful status update.",
		"Finish with REVIEW_STATUS: completed, blocked, failed, needs_review, or update.",
	}
	for i, message := range messages {
		text := truncateRunes(strings.TrimSpace(message.Text), 300)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("Parent message %d: %s", i+1, text))
	}
	return strings.Join(lines, "\n")
}
