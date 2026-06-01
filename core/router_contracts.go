//go:build linux

package core

import (
	"context"
	"fmt"
	"strings"
)

// AgentFunc executes one agent turn for a session.
type AgentFunc func(ctx context.Context, session *SessionState, msg InboundMessage) (*TurnResult, error)

// SessionState is the in-memory state for a chat session.
type SessionState struct {
	ChatID       int64
	Messages     []map[string]interface{}
	SystemPrompt string
}

type SessionStatus struct {
	Active bool
	Queued bool
	// QueueDepth is the count of queued follow-up messages for this chat.
	QueueDepth int
	// Diagnostics includes optional status details from higher-level runtime layers.
	Diagnostics []string
}

type StopResult struct {
	ActiveCanceled      bool
	QueuedDropped       bool
	ContinuationRevoked bool
	ContinuationLabel   string
}

type DetachResult struct {
	ActiveCanceled           bool
	QueuedDropped            bool
	ContinuationRevoked      bool
	PendingDecisionsDetached int
}

type NewSessionResult struct {
	ActiveCanceled           bool
	QueuedDropped            bool
	ContinuationRevoked      bool
	PendingDecisionsDetached int
	ContextCleared           bool
}

func SessionIDForInboundMessage(msg InboundMessage) string {
	if agentID := strings.TrimSpace(msg.DurableAgentID); agentID != "" {
		return "durable_agent:" + agentID
	}
	if msg.ChatID != 0 && msg.TelegramThreadID > 0 {
		return fmt.Sprintf("telegram_thread:%d:%d", msg.ChatID, msg.TelegramThreadID)
	}
	switch strings.ToLower(strings.TrimSpace(msg.ChatType)) {
	case "group", "supergroup", "channel":
		if msg.ChatID != 0 {
			return fmt.Sprintf("telegram_group:%d", msg.ChatID)
		}
	default:
		if msg.ChatID != 0 {
			return fmt.Sprintf("telegram_dm:%d", msg.ChatID)
		}
	}
	return fmt.Sprintf("transport:%d", msg.ChatID)
}
