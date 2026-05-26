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
	ExecutionEventProviderPartial          = "provider.partial"
	ExecutionEventProviderAttemptSucceeded = "provider.attempt.succeeded"
	ExecutionEventProviderFailoverEngaged  = "provider.failover.engaged"
	ExecutionEventModelRequestStarted      = "model.request.started"
	ExecutionEventModelRequestSucceeded    = "model.request.succeeded"
	ExecutionEventModelRequestFailed       = "model.request.failed"
	ExecutionEventModelConfigValidated     = "model.config.validated"
	ExecutionEventModelConfigChanged       = "model.config.changed"
	ExecutionEventModelConfigRejected      = "model.config.rejected"

	ExecutionEventToolStarted               = "tool.started"
	ExecutionEventToolSucceeded             = "tool.succeeded"
	ExecutionEventToolFailed                = "tool.failed"
	ExecutionEventToolBatchStarted          = "tool.batch.started"
	ExecutionEventToolBatchCompleted        = "tool.batch.completed"
	ExecutionEventToolRegistered            = "tool.registered"
	ExecutionEventToolInstallUpdated        = "tool.install.updated"
	ExecutionEventToolAuditUpdated          = "tool.audit.updated"
	ExecutionEventToolRollbackApplied       = "tool.rollback.applied"
	ExecutionEventToolRemovalApplied        = "tool.removal.applied"
	ExecutionEventCapabilityRequestCreated  = "capability.request.created"
	ExecutionEventCapabilityReviewed        = "capability.reviewed"
	ExecutionEventCapabilityGrantChanged    = "capability.grant.changed"
	ExecutionEventCapabilityGrantWakeQueued = "capability.grant.wake_queued"
	ExecutionEventCapabilityGrantWakeFailed = "capability.grant.wake_failed"
	ExecutionEventCapabilityUpdateApplied   = "capability.update_plan.applied"
	ExecutionEventCapabilityInvocation      = "capability.invocation"

	ExecutionEventDeliveryProgressSent   = "delivery.progress.sent"
	ExecutionEventDeliveryProgressEdited = "delivery.progress.edited"
	ExecutionEventDeliveryProgressFailed = "delivery.progress.failed"
	ExecutionEventDeliveryFinalSent      = "delivery.final.sent"
	ExecutionEventDeliveryFinalFailed    = "delivery.final.failed"
	ExecutionEventTelegramCallbackFailed = "telegram.callback.failed"
	ExecutionEventProgressSurface        = "progress.surface"
	ExecutionEventReplyClaimAdjudicated  = "reply.claim.adjudicated"

	ExecutionEventContinuationOffered     = "continuation.offered"
	ExecutionEventContinuationAdjudicated = "continuation.approval.adjudicated"
	ExecutionEventContinuationApproved    = "continuation.approved"
	ExecutionEventContinuationRevoked     = "continuation.revoked"
	ExecutionEventContinuationConsumed    = "continuation.consumed"
	ExecutionEventContinuationBlocked     = "continuation.blocked"
	ExecutionEventContinuationParked      = "continuation.parked"
	ExecutionEventContinuationResumed     = "continuation.resumed"

	ExecutionEventMissionAskOffered    = "mission_ask.offered"
	ExecutionEventMissionAskSuppressed = "mission_ask.suppressed"

	ExecutionEventWorkExecutorSelected  = "work.executor.selected"
	ExecutionEventWorkExecutorFallback  = "work.executor.fallback"
	ExecutionEventWorkExecutorStarted   = "work.executor.started"
	ExecutionEventWorkExecutorSucceeded = "work.executor.succeeded"
	ExecutionEventWorkExecutorFailed    = "work.executor.failed"

	ExecutionEventDecisionOpened   = "decision.opened"
	ExecutionEventDecisionResolved = "decision.resolved"
	ExecutionEventDecisionExpired  = "decision.expired"
	ExecutionEventDecisionDetached = "decision.detached"

	ExecutionEventAutoApprovalGranted = "auto_approval.granted"
	ExecutionEventAutoApprovalUsed    = "auto_approval.used"
	ExecutionEventAutoApprovalRevoked = "auto_approval.revoked"
	ExecutionEventAutoModeEnabled     = "auto_mode.enabled"
	ExecutionEventAutoModeRevoked     = "auto_mode.revoked"

	ExecutionEventAuthorityFindingReviewed = "authority.finding.reviewed"

	ExecutionEventRecoveryAwake     = "recovery.awake"
	ExecutionEventRecoveryDetected  = "recovery.detected"
	ExecutionEventRecoveryIssued    = "recovery.issued"
	ExecutionEventRecoveryCompleted = "recovery.completed"
	ExecutionEventRecoveryFailed    = "recovery.failed"
	ExecutionEventRecoveryResume    = "recovery.resume"

	ExecutionEventWatchdogObserved           = "watchdog.observed"
	ExecutionEventWatchdogRecovered          = "watchdog.recovered"
	ExecutionEventWatchdogRecoverySuppressed = "watchdog.recovery_suppressed"
	ExecutionEventWatchdogFailed             = "watchdog.failed"

	ExecutionEventTailnetSurfaceChanged = "tailnet.surface.changed"
	ExecutionEventTailnetGrantChanged   = "tailnet.grant.changed"

	ExecutionEventGitHubAppTokenMinted = "github_app.token.minted"

	ExecutionEventDurableWakeStarted        = "durable.wake.started"
	ExecutionEventDurableWakeSkipped        = "durable.wake.skipped"
	ExecutionEventDurableWakeCompleted      = "durable.wake.completed"
	ExecutionEventDurableWakeFailed         = "durable.wake.failed"
	ExecutionEventDurableStateAwake         = "durable.state.awake"
	ExecutionEventDurableStateDormant       = "durable.state.dormant"
	ExecutionEventDurablePolicyApplied      = "durable.policy.applied"
	ExecutionEventDurablePolicyApplyFailed  = "durable.policy.failed"
	ExecutionEventDurableParentAck          = "durable.parent.acknowledged"
	ExecutionEventDurableLifecycleChanged   = "durable.lifecycle.changed"
	ExecutionEventDurableProvisionStarted   = "durable.provision.started"
	ExecutionEventDurableProvisionCompleted = "durable.provision.completed"
	ExecutionEventDurableProvisionFailed    = "durable.provision.failed"
)

type RouterEvent struct {
	EventType             string
	SessionID             string
	ChatID                int64
	UserID                int64
	ChatType              string
	DurableAgentID        string
	MessageID             int64
	IngressSeq            int64
	IngressSurface        string
	IngressUpdateID       int64
	QueueDepth            int
	DrainedCount          int
	IngressQueueWait      time.Duration
	IngressQueueWaitKnown bool
	RouterLockWait        time.Duration
	RouterLockWaitKnown   bool
	CreatedAt             time.Time
}

type RouterEventHandler func(ctx context.Context, event RouterEvent)
