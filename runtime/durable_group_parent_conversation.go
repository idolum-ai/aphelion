//go:build linux

package runtime

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/durableagent"
)

func (r *Runtime) pendingDurableAgentParentConversation(agentID string, limit int) ([]core.DurableAgentConversationMessage, error) {
	state, err := r.store.DurableAgentState(strings.TrimSpace(agentID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	continuity, err := core.ParseDurableAgentContinuityState(state.StateJSON)
	if err != nil {
		return nil, err
	}
	return continuity.PendingParentConversationMessages(limit), nil
}

func (r *Runtime) pendingDurableAgentParentConversationByIDs(agentID string, messageIDs []string) ([]core.DurableAgentConversationMessage, error) {
	wanted := map[string]struct{}{}
	for _, id := range messageIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			wanted[id] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	pending, err := r.pendingDurableAgentParentConversation(agentID, 0)
	if err != nil {
		return nil, err
	}
	filtered := make([]core.DurableAgentConversationMessage, 0, len(pending))
	for _, message := range pending {
		if _, ok := wanted[strings.TrimSpace(message.MessageID)]; ok {
			filtered = append(filtered, message)
		}
	}
	return filtered, nil
}

func (r *Runtime) acknowledgeDurableAgentParentConversation(agentID string, messages []core.DurableAgentConversationMessage, at time.Time) error {
	messageIDs := core.DurableAgentConversationMessageIDs(messages)
	_, _, err := r.store.UpdateDurableAgentContinuity(strings.TrimSpace(agentID), func(continuity core.DurableAgentContinuityState) (core.DurableAgentContinuityState, error) {
		return continuity.AcknowledgeParentConversationMessageIDs(messageIDs, at)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (r *Runtime) queueDurableAgentParentConversationAck(agent core.DurableAgent, messages []core.DurableAgentConversationMessage, localReply string, at time.Time) error {
	if len(messages) == 0 || agent.ReviewTargetChatID == 0 {
		return nil
	}
	summary := durableAgentParentConversationAckSummary(messages, localReply)
	if summary == "" {
		return nil
	}
	status, statusSource := durableAgentParentConversationReviewStatus(localReply)
	metadata := map[string]string{
		"durable_agent_id":    strings.TrimSpace(agent.AgentID),
		"channel_kind":        firstNonEmpty(durableTelegramChannel(agent.ChannelKind), strings.TrimSpace(agent.ChannelKind)),
		"trigger_kinds":       "parent_conversation",
		"parent_note_count":   strconv.Itoa(len(messages)),
		"parent_note_excerpt": truncateRunes(strings.TrimSpace(messages[0].Text), 240),
		"acknowledged_at":     at.UTC().Format(time.RFC3339),
		"child_local_subject": "false",
		"status":              status,
		"status_source":       statusSource,
	}
	if trimmedReply := strings.TrimSpace(localReply); trimmedReply != "" {
		metadata["local_response"] = truncateRunes(trimmedReply, 240)
	}
	artifact := core.DurableReviewArtifact{
		AgentID:       strings.TrimSpace(agent.AgentID),
		Summary:       summary,
		IntervalLabel: at.UTC().Format(time.RFC3339),
		LocalActions: []string{
			"Processed pending parent guidance during this durable child turn.",
		},
		RiskFlags:    []string{"parent_conversation_sync"},
		ArtifactRefs: []string{fmt.Sprintf("conversation://durable-agent/%s", strings.TrimSpace(agent.AgentID))},
		Metadata:     metadata,
	}
	_, err := r.queueDurableReviewArtifactPending(agent, artifact)
	if err == nil {
		key := r.durableAgentExecutionKey(strings.TrimSpace(agent.AgentID))
		r.recordExecutionEvent(key, core.ExecutionEventDurableParentAck, "durable", "acknowledged", map[string]any{
			"agent_id":          strings.TrimSpace(agent.AgentID),
			"parent_note_count": len(messages),
			"acknowledged_at":   at.UTC().Format(time.RFC3339),
		}, at.UTC())
	}
	return err
}

func (r *Runtime) queueDurableReviewArtifactPending(agent core.DurableAgent, artifact core.DurableReviewArtifact) (int64, error) {
	if r == nil || r.store == nil {
		return 0, fmt.Errorf("queue durable review artifact: runtime store unavailable")
	}
	// QueueReviewArtifact writes review_events(status='pending') as the operational queue;
	// delivery later transitions those rows to status='delivered'.
	return durableagent.NewRuntime(r.store).QueueReviewArtifact(agent, artifact)
}

func durableAgentParentConversationReviewStatus(localReply string) (string, string) {
	for _, key := range []string{"REVIEW_STATUS", "CHILD_REVIEW_STATUS"} {
		if status := strings.TrimSpace(extractGenericExternalChannelStatusLine(localReply, key)); status != "" {
			return status, strings.ToLower(strings.ReplaceAll(key, "_", "-"))
		}
	}
	return "update", "parent_conversation_ack_default"
}

func durableAgentParentConversationAckSummary(messages []core.DurableAgentConversationMessage, localReply string) string {
	if len(messages) == 0 {
		return ""
	}
	head := truncateRunes(strings.TrimSpace(messages[0].Text), 200)
	if head == "" {
		head = "parent guidance received"
	}
	summary := ""
	if len(messages) == 1 {
		summary = fmt.Sprintf("Processed pending parent guidance: %q.", head)
	} else {
		summary = fmt.Sprintf("Processed %d pending parent guidance notes; latest: %q.", len(messages), head)
	}
	if trimmedReply := strings.TrimSpace(localReply); trimmedReply != "" {
		summary = summary + " Local response: " + truncateRunes(trimmedReply, 220)
	}
	return strings.TrimSpace(summary)
}
