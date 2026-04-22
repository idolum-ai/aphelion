//go:build linux

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/session"
)

func (r *Runtime) RouterEventHandler() core.RouterEventHandler {
	if r == nil {
		return nil
	}
	return func(ctx context.Context, event core.RouterEvent) {
		r.handleRouterEvent(ctx, event)
	}
}

func (r *Runtime) handleRouterEvent(_ context.Context, event core.RouterEvent) {
	if r == nil || r.store == nil {
		return
	}
	eventType := strings.TrimSpace(event.EventType)
	if eventType == "" {
		return
	}
	key := executionKeyFromRouterEvent(event)
	payload := map[string]any{
		"session_id": strings.TrimSpace(event.SessionID),
	}
	if event.MessageID != 0 {
		payload["message_id"] = event.MessageID
	}
	if event.IngressSeq > 0 {
		payload["ingress_seq"] = event.IngressSeq
	}
	if event.QueueDepth > 0 {
		payload["queue_depth"] = event.QueueDepth
	}
	if event.DrainedCount > 0 {
		payload["drained_count"] = event.DrainedCount
	}
	if chatType := strings.TrimSpace(event.ChatType); chatType != "" {
		payload["chat_type"] = chatType
	}
	if userID := event.UserID; userID != 0 {
		payload["user_id"] = userID
	}
	r.recordExecutionEvent(key, eventType, "ingress", "", payload, event.CreatedAt)
}

func executionKeyFromRouterEvent(event core.RouterEvent) session.SessionKey {
	scope := session.ScopeRef{}
	agentID := strings.TrimSpace(event.DurableAgentID)
	switch {
	case agentID != "":
		scope = session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agentID,
			DurableAgentID: agentID,
		}
	case event.ChatID != 0 && isGroupLikeChatType(event.ChatType):
		scope = telegramGroupScopeRef(event.ChatID)
	case event.ChatID != 0:
		scope = telegramDMScopeRef(event.ChatID)
	}
	return session.SessionKey{
		ChatID: event.ChatID,
		UserID: 0,
		Scope:  scope,
	}
}

func (r *Runtime) appendExecutionEvent(
	key session.SessionKey,
	eventType string,
	stage string,
	status string,
	payload map[string]any,
	createdAt time.Time,
) (session.ExecutionEvent, error) {
	if r == nil || r.store == nil {
		return session.ExecutionEvent{}, fmt.Errorf("runtime store unavailable")
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return session.ExecutionEvent{}, fmt.Errorf("execution event type is required")
	}
	payloadJSON := "{}"
	if len(payload) > 0 {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return session.ExecutionEvent{}, fmt.Errorf("marshal execution event payload: %w", err)
		}
		payloadJSON = string(encoded)
	}
	return r.store.AppendExecutionEvent(key, session.ExecutionEventInput{
		EventType:   eventType,
		Stage:       strings.TrimSpace(stage),
		Status:      strings.TrimSpace(status),
		PayloadJSON: payloadJSON,
		CreatedAt:   createdAt.UTC(),
	})
}

func (r *Runtime) recordExecutionEvent(
	key session.SessionKey,
	eventType string,
	stage string,
	status string,
	payload map[string]any,
	createdAt time.Time,
) {
	if r == nil || r.store == nil {
		return
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if _, err := r.appendExecutionEvent(key, eventType, stage, status, payload, createdAt); err != nil {
		log.Printf(
			"WARN append execution event failed type=%s chat_id=%d scope=%s err=%v",
			strings.TrimSpace(eventType),
			key.ChatID,
			key.Scope.String(),
			err,
		)
	}
}

func (r *Runtime) recordInboundExecutionEvent(
	msg core.InboundMessage,
	eventType string,
	stage string,
	status string,
	payload map[string]any,
) {
	key := executionKeyForInbound(msg)
	r.recordExecutionEvent(key, eventType, stage, status, payload, time.Now().UTC())
}

func executionKeyForInbound(msg core.InboundMessage) session.SessionKey {
	scope := session.ScopeRef{}
	if agentID := strings.TrimSpace(msg.DurableAgentID); agentID != "" {
		scope = session.ScopeRef{
			Kind:           session.ScopeKindDurableAgent,
			ID:             agentID,
			DurableAgentID: agentID,
		}
	} else if msg.ChatID != 0 && isGroupLikeChatType(msg.ChatType) {
		scope = telegramGroupScopeRef(msg.ChatID)
	} else if msg.ChatID != 0 {
		scope = telegramDMScopeRef(msg.ChatID)
	}
	return session.SessionKey{
		ChatID: msg.ChatID,
		UserID: 0,
		Scope:  scope,
	}
}

func isGroupLikeChatType(chatType string) bool {
	switch strings.ToLower(strings.TrimSpace(chatType)) {
	case "group", "supergroup", "channel", "telegram_group":
		return true
	default:
		return false
	}
}

func latestTurnPhaseFromExecutionEvents(events []session.ExecutionEvent) (statusTurnPhase, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if strings.TrimSpace(event.EventType) != core.ExecutionEventTurnStageChanged {
			continue
		}
		summary := ""
		raw := strings.TrimSpace(event.PayloadJSON)
		if raw != "" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(raw), &payload); err == nil {
				if value, ok := payload["summary"]; ok {
					summary = strings.TrimSpace(fmt.Sprint(value))
				}
			}
		}
		phase := strings.TrimSpace(event.Stage)
		if phase == "" {
			phase = strings.TrimSpace(event.Status)
		}
		if phase == "" {
			phase = strings.TrimSpace(firstStringField(event.PayloadJSON, "phase"))
		}
		if phase == "" {
			continue
		}
		return statusTurnPhase{
			Phase:     phase,
			Summary:   summary,
			UpdatedAt: event.CreatedAt,
		}, true
	}
	return statusTurnPhase{}, false
}

func firstStringField(rawJSON string, key string) string {
	rawJSON = strings.TrimSpace(rawJSON)
	key = strings.TrimSpace(key)
	if rawJSON == "" || key == "" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
