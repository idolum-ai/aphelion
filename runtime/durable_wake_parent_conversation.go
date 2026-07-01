//go:build linux

package runtime

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

const durableParentConversationChatType = "durable_parent_conversation"

var durableParentConversationControlPlaneRedactions = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)\btool\s*[:=]\s*durable_agent\b`), "[parent-control tool consumed]"},
	{regexp.MustCompile(`(?i)\baction\s*[:=]\s*wake_once\b`), "[parent-control action consumed]"},
	{regexp.MustCompile(`(?i)\bdurable_agent\.wake_once\b`), "[parent-control wake consumed]"},
	{regexp.MustCompile(`(?i)\bdurable_agent\s+wake_once\b`), "[parent-control wake consumed]"},
	{regexp.MustCompile("`durable_agent`"), "[parent-control tool consumed]"},
	{regexp.MustCompile("`wake_once`"), "[parent-control action consumed]"},
	{regexp.MustCompile(`(?i)\bchild_wake\b`), "child wake approval"},
}

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
	pending, err := pendingDurableParentConversationForWake(rt, agent, now, force)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}
	return durableParentConversationWakePlanForPending(agent, pending, now), nil
}

func prepareDurableParentConversationWakePlanForMessageIDs(rt *Runtime, agent core.DurableAgent, messageIDs []string, now time.Time, force bool) (*durableWakeTurnPlan, error) {
	pending, err := pendingDurableParentConversationForWake(rt, agent, now, force)
	if err != nil {
		return nil, err
	}
	pending = durableParentConversationFilterPendingByIDs(pending, messageIDs)
	if len(pending) == 0 {
		return nil, nil
	}
	return durableParentConversationWakePlanForPending(agent, pending, now), nil
}

func pendingDurableParentConversationForWake(rt *Runtime, agent core.DurableAgent, now time.Time, force bool) ([]core.DurableAgentConversationMessage, error) {
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
	pending, err := rt.pendingDurableAgentParentConversation(strings.TrimSpace(agent.AgentID), 5)
	if err != nil {
		return nil, fmt.Errorf("load parent conversation queue: %w", err)
	}
	return pending, nil
}

func durableParentConversationFilterPendingByIDs(pending []core.DurableAgentConversationMessage, messageIDs []string) []core.DurableAgentConversationMessage {
	wanted := map[string]struct{}{}
	for _, id := range messageIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	filtered := make([]core.DurableAgentConversationMessage, 0, len(pending))
	for _, message := range pending {
		if _, ok := wanted[strings.TrimSpace(message.MessageID)]; ok {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func durableParentConversationWakePlanForPending(agent core.DurableAgent, pending []core.DurableAgentConversationMessage, now time.Time) *durableWakeTurnPlan {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	key := session.SessionKey{
		ChatID: durableWakeSyntheticChatID(agent.AgentID),
		Scope:  durableAgentScopeRef(agent),
	}
	taskPacketID := durableWakeTaskPacketIDForPending(agent.AgentID, pending, now)
	return &durableWakeTurnPlan{
		Channel:          "durable_parent_conversation",
		AuditChannel:     "durable_parent_conversation",
		Key:              key,
		SessionChatType:  durableParentConversationChatType,
		SessionUserName:  "parent",
		ParentMessageIDs: core.DurableAgentConversationMessageIDs(pending),
		TaskPacketID:     taskPacketID,
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
	}
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

func durableWakeTaskPacketIDForWakeClaim(agentID string, messageIDs []string, wakeClaimID string) string {
	parts := []string{strings.TrimSpace(agentID), strings.TrimSpace(wakeClaimID)}
	seen := map[string]struct{}{}
	for _, id := range messageIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		parts = append(parts, id)
	}
	sort.Strings(parts[2:])
	return "child_task:" + session.EffectAttemptCommandHash(strings.Join(parts, ":"))[7:23]
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
		text := truncateRunes(durableParentConversationChildFacingMessage(message.Text), 500)
		if text == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("Parent message %d: %s", i+1, text))
	}
	return strings.Join(lines, "\n")
}

func durableParentConversationChildFacingMessage(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if !durableParentConversationMentionsControlPlaneWake(text) {
		return text
	}
	redacted := durableParentConversationRedactControlPlaneWakeTokens(text)
	return strings.Join([]string{
		"Parent control-plane approval was already consumed by the parent runtime.",
		"Do not call the parent durable-agent governance tool or wake action from this child turn.",
		"Continue only the child-local task guidance below; if the remaining task is ambiguous, report REVIEW_STATUS: needs_review.",
		"Child-local guidance: " + redacted,
	}, " ")
}

func durableParentConversationMentionsControlPlaneWake(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	return strings.Contains(lower, "child_wake") ||
		strings.Contains(lower, "durable_agent.wake_once") ||
		(strings.Contains(lower, "durable_agent") && strings.Contains(lower, "wake_once"))
}

func durableParentConversationRedactControlPlaneWakeTokens(text string) string {
	out := text
	for _, redaction := range durableParentConversationControlPlaneRedactions {
		out = redaction.pattern.ReplaceAllString(out, redaction.replacement)
	}
	return strings.TrimSpace(out)
}
