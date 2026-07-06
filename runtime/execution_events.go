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
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/decisionprojection"
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

func (r *Runtime) DecisionEventObserver() decision.Observer {
	if r == nil {
		return nil
	}
	return func(ctx context.Context, event decision.Event) {
		r.handleDecisionEvent(ctx, event)
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
	if surface := strings.TrimSpace(event.IngressSurface); surface != "" {
		payload["ingress_surface"] = surface
	}
	if event.IngressUpdateID > 0 {
		payload["ingress_update_id"] = event.IngressUpdateID
	}
	if event.QueueDepth > 0 {
		payload["queue_depth"] = event.QueueDepth
	}
	if event.DrainedCount > 0 {
		payload["drained_count"] = event.DrainedCount
	}
	if event.IngressQueueWaitKnown {
		putDurationMillis(payload, "ingress_queue_wait_ms", event.IngressQueueWait)
	}
	if event.RouterLockWaitKnown {
		putDurationMillis(payload, "router_lock_wait_ms", event.RouterLockWait)
	}
	if chatType := strings.TrimSpace(event.ChatType); chatType != "" {
		payload["chat_type"] = chatType
	}
	if userID := event.UserID; userID != 0 {
		payload["user_id"] = userID
	}
	r.recordExecutionEvent(key, eventType, "ingress", "", payload, event.CreatedAt)
}

func (r *Runtime) handleDecisionEvent(_ context.Context, event decision.Event) {
	if r == nil || r.store == nil {
		return
	}
	eventType := ""
	status := ""
	switch event.Type {
	case decision.EventTypeOpened:
		eventType = core.ExecutionEventDecisionOpened
		status = "pending"
	case decision.EventTypeResolved:
		eventType = core.ExecutionEventDecisionResolved
		status = "resolved"
	case decision.EventTypeExpired:
		eventType = core.ExecutionEventDecisionExpired
		status = "expired"
	case decision.EventTypeDetached:
		eventType = core.ExecutionEventDecisionDetached
		status = "detached"
	default:
		return
	}
	req := event.Decision.Request
	key := session.SessionKey{
		ChatID: req.ChatID,
		UserID: 0,
		Scope:  decisionScopeRef(req.ChatID),
	}
	payload := map[string]any{
		"decision_id":   strings.TrimSpace(event.Decision.ID),
		"decision_kind": strings.TrimSpace(string(req.Kind)),
		"owner_key":     strings.TrimSpace(event.OwnerKey),
		"seq":           event.Seq,
		"choice":        strings.TrimSpace(event.Choice),
		"timed_out":     event.TimedOut,
		"reason":        strings.TrimSpace(event.Reason),
		"default":       strings.TrimSpace(req.DefaultChoice),
		"prompt":        truncatePreview(strings.TrimSpace(req.Prompt), 200),
		"details":       truncatePreview(strings.TrimSpace(req.Details), 220),
	}
	if summary := strings.TrimSpace(decisionprojection.DecisionSummary(string(req.Kind), req.Prompt, req.Details)); summary != "" {
		payload["summary"] = truncatePreview(summary, 220)
	}
	if req.SenderID != 0 {
		payload["sender_id"] = req.SenderID
	}
	if req.MessageID != 0 {
		payload["message_id"] = req.MessageID
	}
	if len(req.Choices) > 0 {
		choices := make([]string, 0, len(req.Choices))
		for _, choice := range req.Choices {
			id := strings.TrimSpace(choice.ID)
			if id != "" {
				choices = append(choices, id)
			}
		}
		if len(choices) > 0 {
			payload["choices"] = strings.Join(choices, ",")
		}
	}
	r.recordExecutionEvent(key, eventType, "decision", status, payload, event.CreatedAt)
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

func decisionScopeRef(chatID int64) session.ScopeRef {
	if chatID == 0 {
		return session.ScopeRef{}
	}
	if chatID < 0 {
		return telegramGroupScopeRef(chatID)
	}
	return telegramDMScopeRef(chatID)
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
	started := time.Now()
	if _, err := r.appendExecutionEvent(key, eventType, stage, status, payload, createdAt); err != nil {
		if r.expectedShutdownNoise(context.Background(), err) {
			log.Printf(
				"INFO suppressing expected shutdown execution event append failure type=%s chat_id=%d scope=%s err=%v",
				strings.TrimSpace(eventType),
				key.ChatID,
				key.Scope.String(),
				err,
			)
			return
		}
		log.Printf(
			"WARN append execution event failed type=%s chat_id=%d scope=%s err=%v",
			strings.TrimSpace(eventType),
			key.ChatID,
			key.Scope.String(),
			err,
		)
		return
	}
	if elapsed := time.Since(started); elapsed >= core.PersistenceLatencySlowThreshold {
		log.Printf("WARN append execution event slow type=%s chat_id=%d scope=%s tes_write_duration_ms=%d", strings.TrimSpace(eventType), key.ChatID, key.Scope.String(), durationMillis(elapsed))
		if strings.TrimSpace(eventType) != core.ExecutionEventPersistenceLatency {
			if err := r.store.RecordPersistenceLatencyClassification(key, "execution_events:"+strings.TrimSpace(eventType), elapsed, time.Now().UTC()); err != nil {
				log.Printf("WARN record persistence latency classification failed type=%s chat_id=%d scope=%s err=%v", strings.TrimSpace(eventType), key.ChatID, key.Scope.String(), err)
			}
		}
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
	activeTurn := false
	latestPhase := statusTurnPhase{}
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case core.ExecutionEventTurnStarted:
			activeTurn = true
			latestPhase = statusTurnPhase{}
		case core.ExecutionEventTurnCompleted, core.ExecutionEventTurnFailed, core.ExecutionEventTurnInterrupted:
			activeTurn = false
			latestPhase = statusTurnPhase{}
		case core.ExecutionEventTurnStageChanged:
			if !activeTurn {
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
			latestPhase = statusTurnPhase{
				Phase:     phase,
				Summary:   summary,
				UpdatedAt: event.CreatedAt,
			}
		}
	}
	if !activeTurn || strings.TrimSpace(latestPhase.Phase) == "" {
		return statusTurnPhase{}, false
	}
	return latestPhase, true
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
