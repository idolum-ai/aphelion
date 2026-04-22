//go:build linux

package core

import (
	"context"
	"time"
)

const (
	ExecutionEventIngressAccepted  = "ingress.accepted"
	ExecutionEventIngressQueued    = "ingress.queued"
	ExecutionEventIngressCompacted = "ingress.compacted"
	ExecutionEventIngressSelected  = "ingress.selected"

	ExecutionEventTurnStarted      = "turn.started"
	ExecutionEventTurnStageChanged = "turn.stage.changed"
	ExecutionEventTurnCompleted    = "turn.completed"
	ExecutionEventTurnFailed       = "turn.failed"
	ExecutionEventTurnInterrupted  = "turn.interrupted"

	ExecutionEventToolStarted   = "tool.started"
	ExecutionEventToolSucceeded = "tool.succeeded"
	ExecutionEventToolFailed    = "tool.failed"

	ExecutionEventDeliveryProgressSent   = "delivery.progress.sent"
	ExecutionEventDeliveryProgressEdited = "delivery.progress.edited"
	ExecutionEventDeliveryProgressFailed = "delivery.progress.failed"
	ExecutionEventDeliveryFinalSent      = "delivery.final.sent"
	ExecutionEventDeliveryFinalFailed    = "delivery.final.failed"

	ExecutionEventContinuationOffered  = "continuation.offered"
	ExecutionEventContinuationApproved = "continuation.approved"
	ExecutionEventContinuationRevoked  = "continuation.revoked"
	ExecutionEventContinuationConsumed = "continuation.consumed"
	ExecutionEventContinuationBlocked  = "continuation.blocked"
)

type RouterEvent struct {
	EventType      string
	SessionID      string
	ChatID         int64
	UserID         int64
	ChatType       string
	DurableAgentID string
	MessageID      int64
	IngressSeq     int64
	QueueDepth     int
	DrainedCount   int
	CreatedAt      time.Time
}

type RouterEventHandler func(ctx context.Context, event RouterEvent)
