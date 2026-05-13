//go:build linux

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/session"
)

func (h *telegramDecisionHandler) HandleBusyMessage(ctx context.Context, msg core.InboundMessage) (bool, error) {
	if h == nil || h.sender == nil || h.router == nil || h.broker == nil {
		return false, nil
	}
	status := h.router.Status(msg.ChatID)
	if scoped, ok := h.router.(telegramDecisionMessageStatusRouter); ok {
		status = scoped.StatusForMessage(msg)
	}
	if !status.Active {
		return false, nil
	}

	req := decision.Request{
		ChatID:        msg.ChatID,
		SenderID:      msg.SenderID,
		MessageID:     msg.MessageID,
		Choices:       []decision.Choice{{ID: "stop", Label: stopChoiceLabel(msg.Text)}, {ID: "queue", Label: queueChoiceLabel(msg.Text)}},
		DefaultChoice: "queue",
	}
	if isStopWord(msg.Text) {
		req.Kind = decision.KindStopWord
		req.Prompt = "Stop the current task?"
		req.Timeout = h.stopWordTimeout
	} else {
		req.Kind = decision.KindInterrupt
		req.Prompt = "I'm still working on the previous request. What would you like to do?"
		req.Timeout = h.interruptTimeout
	}
	ownerKey := decision.OwnerKey(msg.ChatID, msg.SenderID)
	if ownerKey == "" {
		return false, fmt.Errorf("busy decision owner key is required")
	}
	if h.store == nil {
		result, err := h.broker.Request(ctx, req)
		if err != nil {
			return true, err
		}
		h.applyBusyDecisionResult(ctx, msg, result)
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
		InboundMessageJSON: string(raw),
	}); err != nil {
		return true, err
	}
	go h.awaitBusyDecision(context.Background(), ownerKey, req)
	return true, nil
}

func (h *telegramDecisionHandler) awaitBusyDecision(ctx context.Context, ownerKey string, req decision.Request) {
	result, err := h.broker.Request(ctx, req)
	if err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingBusyDecision(ownerKey)
		}
		return
	}
	if err := h.resumePendingBusyDecision(ctx, ownerKey, result); err != nil {
		if h.store != nil {
			_ = h.store.DeletePendingBusyDecision(ownerKey)
		}
	}
}

func (h *telegramDecisionHandler) resumePendingBusyDecision(ctx context.Context, ownerKey string, result decision.Result) error {
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
	defer func() { _ = h.store.DeletePendingBusyDecision(ownerKey) }()
	var msg core.InboundMessage
	if err := json.Unmarshal([]byte(record.InboundMessageJSON), &msg); err != nil {
		return fmt.Errorf("decode pending busy message: %w", err)
	}
	h.applyBusyDecisionResult(ctx, msg, result)
	return nil
}

func (h *telegramDecisionHandler) applyBusyDecisionResult(ctx context.Context, msg core.InboundMessage, result decision.Result) {
	switch result.Choice {
	case "stop":
		if result.Delivery.MessageID != 0 && h.sender != nil {
			_ = h.sender.DeleteMessage(ctx, msg.ChatID, result.Delivery.MessageID)
		}
		if scoped, ok := h.router.(telegramDecisionMessageStopRouter); ok {
			scoped.StopForMessage(msg)
		} else {
			h.router.Stop(msg.ChatID)
		}
		if !isOnlyStopWord(msg.Text) {
			h.router.Route(ctx, msg)
		}
	case "queue":
		if result.Delivery.MessageID != 0 && h.sender != nil {
			text := "Got it — I'll process your message next. ⏳"
			if result.TimedOut {
				text = "Queued your message — processing after current task."
			}
			_ = editDecisionMessageClearingInlineKeyboard(ctx, h.sender, msg.ChatID, result.Delivery.MessageID, text)
		}
		h.router.Route(ctx, msg)
	}
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

func isStopWord(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range stopPatterns {
		if lower == p || strings.HasPrefix(lower, p+" ") || strings.HasPrefix(lower, p+",") {
			return true
		}
	}
	return false
}

func isOnlyStopWord(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, p := range stopPatterns {
		if lower == p {
			return true
		}
	}
	return false
}

func stopChoiceLabel(text string) string {
	if isStopWord(text) {
		return "Yes, stop"
	}
	return "Stop"
}

func queueChoiceLabel(text string) string {
	if isStopWord(text) {
		return "Keep going"
	}
	return "Finish"
}
