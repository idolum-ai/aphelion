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

	ExecutionEventTurnStarted              = "turn.started"
	ExecutionEventTurnStageChanged         = "turn.stage.changed"
	ExecutionEventTurnSidecarsCaptured     = "turn.sidecars.captured"
	ExecutionEventTurnCompleted            = "turn.completed"
	ExecutionEventTurnFailed               = "turn.failed"
	ExecutionEventTurnInterrupted          = "turn.interrupted"
	ExecutionEventProviderAttemptStarted   = "provider.attempt.started"
	ExecutionEventProviderAttemptRetried   = "provider.attempt.retried"
	ExecutionEventProviderAttemptFailed    = "provider.attempt.failed"
	ExecutionEventProviderAttemptSucceeded = "provider.attempt.succeeded"
	ExecutionEventProviderFailoverEngaged  = "provider.failover.engaged"

	ExecutionEventToolStarted              = "tool.started"
	ExecutionEventToolSucceeded            = "tool.succeeded"
	ExecutionEventToolFailed               = "tool.failed"
	ExecutionEventToolRegistered           = "tool.registered"
	ExecutionEventToolInstallUpdated       = "tool.install.updated"
	ExecutionEventToolAuditUpdated         = "tool.audit.updated"
	ExecutionEventCapabilityRequestCreated = "capability.request.created"
	ExecutionEventCapabilityReviewed       = "capability.reviewed"
	ExecutionEventCapabilityGrantChanged   = "capability.grant.changed"
	ExecutionEventCapabilityUpdateApplied  = "capability.update_plan.applied"
	ExecutionEventCapabilityInvocation     = "capability.invocation"

	ExecutionEventDeliveryProgressSent   = "delivery.progress.sent"
	ExecutionEventDeliveryProgressEdited = "delivery.progress.edited"
	ExecutionEventDeliveryProgressFailed = "delivery.progress.failed"
	ExecutionEventDeliveryFinalSent      = "delivery.final.sent"
	ExecutionEventDeliveryFinalFailed    = "delivery.final.failed"
	ExecutionEventProgressSurface        = "progress.surface"

	ExecutionEventContinuationOffered  = "continuation.offered"
	ExecutionEventContinuationApproved = "continuation.approved"
	ExecutionEventContinuationRevoked  = "continuation.revoked"
	ExecutionEventContinuationConsumed = "continuation.consumed"
	ExecutionEventContinuationBlocked  = "continuation.blocked"

	ExecutionEventDecisionOpened   = "decision.opened"
	ExecutionEventDecisionResolved = "decision.resolved"
	ExecutionEventDecisionExpired  = "decision.expired"
	ExecutionEventDecisionDetached = "decision.detached"

	ExecutionEventRecoveryDetected  = "recovery.detected"
	ExecutionEventRecoveryIssued    = "recovery.issued"
	ExecutionEventRecoveryCompleted = "recovery.completed"
	ExecutionEventRecoveryFailed    = "recovery.failed"

	ExecutionEventDurableWakeStarted       = "durable.wake.started"
	ExecutionEventDurableWakeSkipped       = "durable.wake.skipped"
	ExecutionEventDurableWakeCompleted     = "durable.wake.completed"
	ExecutionEventDurableWakeFailed        = "durable.wake.failed"
	ExecutionEventDurableStateAwake        = "durable.state.awake"
	ExecutionEventDurableStateDormant      = "durable.state.dormant"
	ExecutionEventDurablePolicyApplied     = "durable.policy.applied"
	ExecutionEventDurablePolicyApplyFailed = "durable.policy.failed"
	ExecutionEventDurableParentAck         = "durable.parent.acknowledged"
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
