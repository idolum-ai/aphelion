//go:build linux

package telegramdecision

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/session"
)

func (h *Handler) HandleBusyMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	if h == nil || h.sender == nil || h.router == nil || h.broker == nil {
		return false, nil
	}
	status := h.router.Status(msg.ChatID)
	if scoped, ok := h.router.(MessageStatusRouter); ok {
		status = scoped.StatusForMessage(msg)
	}
	if !status.Active {
		return false, nil
	}

	ownerKey := telegramruntime.SessionOwnerKey(msg)
	if ownerKey == "" {
		return false, fmt.Errorf("busy decision owner key is required")
	}
	req := h.busyDecisionRequest(msg, ownerKey)
	if h.store == nil {
		result, err := h.broker.Request(ctx, req)
		if err != nil {
			return true, err
		}
		if err := h.applyBusyDecisionResult(ctx, msg, result, false); err != nil {
			return true, err
		}
		return true, nil
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return true, fmt.Errorf("marshal pending busy message: %w", err)
	}
	if err := h.store.UpsertPendingBusyDecision(session.PendingBusyDecisionRecord{
		OwnerKey:           ownerKey,
		ChatID:             msg.ChatID,
		SenderID:           msg.SenderID,
		SessionID:          req.SessionID,
		ScopeKind:          req.ScopeKind,
		ScopeID:            req.ScopeID,
		DurableAgentID:     req.DurableAgentID,
		MessageID:          msg.MessageID,
		InboundMessageJSON: string(raw),
	}); err != nil {
		return true, err
	}
	go h.awaitBusyDecision(context.Background(), ownerKey, req)
	return true, nil
}

func (h *Handler) awaitBusyDecision(ctx context.Context, ownerKey string, req decision.Request) {
	result, err := h.broker.Request(ctx, req)
	if err != nil {
		logTelegramDecisionResumeError("busy_request", ownerKey, err)
		return
	}
	if err := h.ResumePendingBusyDecision(ctx, ownerKey, result); err != nil {
		if h.store != nil {
			logTelegramDecisionResumeError("busy", ownerKey, err)
		}
	}
}

func (h *Handler) ResumePendingBusyDecision(ctx context.Context, ownerKey string, result decision.Result) error {
	if h == nil || h.router == nil || h.store == nil {
		return nil
	}
	record, err := h.store.PendingBusyDecision(ownerKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(record.InboundMessageJSON), &msg); err != nil {
		return fmt.Errorf("decode pending busy message: %w", err)
	}
	if err := h.applyBusyDecisionResult(ctx, msg, result, true); err != nil {
		return err
	}
	if err := h.store.DeletePendingBusyDecision(ownerKey); err != nil {
		return err
	}
	return nil
}

func (h *Handler) applyBusyDecisionResult(ctx context.Context, msg core.InboundMessage, result decision.Result, deferred bool) error {
	switch result.Choice {
	case "stop":
		if result.Delivery.MessageID != 0 && h.sender != nil {
			_ = h.sender.DeleteMessage(ctx, msg.ChatID, result.Delivery.MessageID)
		}
		if scoped, ok := h.router.(MessageStopRouter); ok {
			scoped.StopForMessage(msg)
		} else {
			h.router.Stop(msg.ChatID)
		}
		if !IsOnlyStopWord(msg.Text) {
			return h.routeAfterBusyDecision(ctx, msg, deferred)
		}
	case "queue":
		if result.Delivery.MessageID != 0 && h.sender != nil {
			text := h.busyQueueAcknowledgementText(msg, result.TimedOut)
			_ = EditDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, result.Delivery.MessageID, text)
		}
		return h.routeAfterBusyDecision(ctx, msg, deferred)
	}
	return nil
}

func (h *Handler) busyQueueAcknowledgementText(msg core.InboundMessage, timedOut bool) string {
	text := "Got it — I'll process your message next. ⏳"
	if timedOut {
		text = "Queued your message — processing after current task."
	}
	if msg.ChatID == 0 || msg.TelegramThreadID <= 0 {
		return text
	}
	var resolver DecisionThreadResolver
	if h != nil && h.store != nil {
		resolver = h.store
	}
	return prefixDecisionTextForThread(msg.ChatID, msg.TelegramThreadID, resolver, text)
}

func (h *Handler) routeAfterBusyDecision(ctx context.Context, msg core.InboundMessage, deferred bool) error {
	if handled, err := h.handleArtifactRetentionMessage(ctx, msg, deferred); err != nil || handled {
		return err
	}
	if deferred {
		return h.routeDeferredDecisionMessage(ctx, msg, telegramruntime.BusyDecisionResumeIngressSurface, "decision_resume_busy")
	}
	return h.routeDecisionMessage(ctx, msg)
}

var stopPatterns = []string{
	"wait",
	"stop",
	"cancel",
	"nevermind",
	"nvm",
	"hold on",
	"abort",
	"halt",
}

func IsStopWord(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range stopPatterns {
		if lower == p || strings.HasPrefix(lower, p+" ") || strings.HasPrefix(lower, p+",") {
			return true
		}
	}
	return false
}

func IsOnlyStopWord(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range stopPatterns {
		if lower == p {
			return true
		}
	}
	return false
}

func StopChoiceLabel(text string) string {
	if IsStopWord(text) {
		return "Yes, stop"
	}
	return "Stop"
}

func QueueChoiceLabel(text string) string {
	if IsStopWord(text) {
		return "Keep going"
	}
	return "Finish"
}
