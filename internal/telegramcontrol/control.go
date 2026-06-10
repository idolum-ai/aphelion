//go:build linux

package telegramcontrol

import (
	"context"
	"time"

	"github.com/idolum-ai/aphelion/internal/telegramruntime"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
)

type Runtime interface {
	ContinuationState(chatID int64) (session.ContinuationState, error)
	ContinuationStateForKey(key session.SessionKey) (session.ContinuationState, error)
	ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error)
	ApproveContinuationBundle(chatID int64, approverID int64, phaseIDs []string) (session.ContinuationState, error)
	ApproveContinuationForKey(key session.SessionKey, approverID int64) (session.ContinuationState, error)
	ApproveContinuationBundleForKey(key session.SessionKey, approverID int64, phaseIDs []string) (session.ContinuationState, error)
	TriggerContinuation(ctx context.Context, chatID int64) error
	TriggerContinuationForKey(ctx context.Context, key session.SessionKey) error
	RecordTelegramCallbackError(chatID int64, callbackKind string, err error)
	ToggleProgressView(ctx context.Context, chatID int64, senderID int64, runID int64, details bool) (bool, string, error)
	ConfigureAutoApproval(ctx context.Context, chatID int64, senderID int64, args string) (string, error)
	ConfigureAutoApprovalForKey(ctx context.Context, key session.SessionKey, senderID int64, args string) (string, error)
	AutoApprovalStatus(ctx context.Context, chatID int64, senderID int64) (string, error)
	AutoApprovalStatusForKey(ctx context.Context, key session.SessionKey, senderID int64) (string, error)
	CreateApprovalWindowOfferForKey(ctx context.Context, key session.SessionKey, senderID int64, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error)
	EnableApprovalWindowForKey(ctx context.Context, key session.SessionKey, senderID int64, duration time.Duration) (string, error)
	EnableApprovalWindowForKeyResult(ctx context.Context, key session.SessionKey, senderID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error)
	DoubleApprovalWindowForKey(ctx context.Context, key session.SessionKey, senderID int64) (string, error)
	CancelApprovalWindowForKey(ctx context.Context, key session.SessionKey, senderID int64) (string, error)
	CancelApprovalWindowForKeyResult(ctx context.Context, key session.SessionKey, senderID int64) (core.ApprovalWindowCancelResult, error)
	EnableApprovalWindowOffer(ctx context.Context, offerID string, senderID int64, duration time.Duration) (string, error)
	EnableApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error)
	DoubleApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error)
	CancelApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error)
	CancelApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64) (core.ApprovalWindowCancelResult, error)
	CloseApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) error
	ApprovalWindowOfferByID(offerID string) (session.ApprovalWindowOffer, bool, error)
	RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error)
	RefreshContinuationProposalForKey(ctx context.Context, key session.SessionKey, reason string) (session.ContinuationState, bool, error)

	StartDoctor(ctx context.Context, msg core.InboundMessage) error
	LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error)

	MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error)
	MemoryReviewSnapshotForKey(ctx context.Context, key session.SessionKey, senderID int64, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error)
	MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error)
	MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error)
	MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error)
	SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error)
	UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error)
	MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error)
	MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error)
	ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error)
	MissionAskPrompt(ctx context.Context, senderID int64, promptID string) (session.MissionAskPrompt, bool, error)
	ResolveMissionAskPrompt(ctx context.Context, senderID int64, promptID string, status session.MissionAskStatus, summary string) (session.MissionAskPrompt, error)
	ReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, bool, error)
	IgnoreReentryRecommendation(ctx context.Context, senderID int64, recommendationID string) (session.ReentryRecommendation, error)
	PrepareReentryRecommendationSelection(ctx context.Context, senderID int64, recommendationID string, candidateID string) (session.ReentryRecommendation, session.ReentryRecommendationCandidate, bool, error)
	ConfirmReentryRecommendationSelection(ctx context.Context, senderID int64, recommendationID string, candidateID string) (session.ReentryRecommendation, session.ReentryRecommendationCandidate, bool, error)

	ModelSlotStatuses() ([]core.ModelSlotStatus, error)
	ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation
	SetModelSlotOverride(cfg core.ModelSlotConfig, actor string, reason string) (core.ModelSlotStatus, error)
	ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error)
	ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error)

	StatusDiagnostics(chatID int64) ([]string, error)
	ChatStatusSnapshot(chatID int64, routerSnapshot core.RouterStatusSnapshot) (core.ChatStatusSnapshot, error)
	ChatStatusSnapshotForKey(key session.SessionKey, routerSnapshot core.RouterStatusSnapshot) (core.ChatStatusSnapshot, error)
	SystemStatusSnapshot(routerSnapshot core.RouterStatusSnapshot) (core.SystemStatusSnapshot, error)
	ChatAutonomyStatusSnapshot(chatID int64, senderID int64) (core.AutonomyStatusSnapshot, error)
	ChatAutonomyStatusSnapshotForKey(key session.SessionKey, senderID int64) (core.AutonomyStatusSnapshot, error)
	ConfigureAutonomy(ctx context.Context, chatID int64, senderID int64, args string) (string, error)
	ConfigureAutonomyForKey(ctx context.Context, key session.SessionKey, senderID int64, args string) (string, error)
	DurableAgentsStatusSnapshot() (core.DurableAgentsStatusSnapshot, error)
	StatusReadableSummary(ctx context.Context, view string, statusText string) string
	TailnetStatusSnapshot(ctx context.Context) (core.TailnetStatusSnapshot, error)
	TailnetSurfacesSnapshot() ([]core.TailnetSurfaceStatus, error)
	TailnetGrantBindingsSnapshot() ([]core.TailnetGrantBindingStatus, error)
	RevokeTailnetSurface(ctx context.Context, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error)
	IsTelegramAdmin(senderID int64) bool
	CurrentEfforts() (string, string)

	MarkStreamControlStopping(streamID string, chatID int64) bool
	CancelActiveTurnRun(runID int64) bool
	ClearChatSessionContext(chatID int64) (bool, error)
	ClearSessionContextForKey(key session.SessionKey) (bool, error)
	FlushChatMemory(ctx context.Context, chatID int64, reason string) error
	ReportOperationalIssue(ctx context.Context, component string, err error)
}

type StatusRouter interface {
	Status(chatID int64) core.SessionStatus
	StatusForMessage(msg core.InboundMessage) core.SessionStatus
	Snapshot() core.RouterStatusSnapshot
	Stop(chatID int64) core.StopResult
	StopForMessage(msg core.InboundMessage) core.StopResult
	Route(ctx context.Context, msg core.InboundMessage)
}

type IngressRouter interface {
	Status(chatID int64) core.SessionStatus
	StatusForMessage(msg core.InboundMessage) core.SessionStatus
	Snapshot() core.RouterStatusSnapshot
	Stop(chatID int64) core.StopResult
	StopForMessage(msg core.InboundMessage) core.StopResult
	Enqueue(ctx context.Context, msg core.InboundMessage) error
}

type DecisionDetacher interface {
	DetachByOwner(ctx context.Context, ownerKey string) (int, error)
	DetachAll(ctx context.Context) (int, error)
}

type DecisionChatSenderDetacher interface {
	DetachByChatSender(ctx context.Context, chatID int64, senderID int64) (int, error)
}

type TelegramUserResolver interface {
	ResolveTelegramUser(userID int64) (principal.Principal, bool)
}

type CommandControl struct {
	Router                       StatusRouter
	Ingress                      IngressRouter
	Runtime                      Runtime
	Store                        *session.SQLiteStore
	Resolver                     TelegramUserResolver
	DecisionDetacher             DecisionDetacher
	RevokeContinuation           func(chatID int64) (core.StopResult, error)
	RevokeContinuationForMessage func(core.InboundMessage) (core.StopResult, error)
	ReinstallTemplate            string
}

func SessionKeyForMessage(msg core.InboundMessage) session.SessionKey {
	return session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramruntime.CommandMessageScope(msg)}
}

func mergeSessionStatus(a core.SessionStatus, b core.SessionStatus) core.SessionStatus {
	return core.SessionStatus{
		Active:      a.Active || b.Active,
		Queued:      a.Queued || b.Queued || a.QueueDepth+b.QueueDepth > 0,
		QueueDepth:  a.QueueDepth + b.QueueDepth,
		Diagnostics: append(append([]string(nil), a.Diagnostics...), b.Diagnostics...),
	}
}

func mergeRouterStatusSnapshots(a core.RouterStatusSnapshot, b core.RouterStatusSnapshot) core.RouterStatusSnapshot {
	if a.ActiveTurnsByChat == nil {
		a.ActiveTurnsByChat = make(map[int64][]uint64)
	}
	if a.QueueDepthByChat == nil {
		a.QueueDepthByChat = make(map[int64]int)
	}
	for chatID, ids := range b.ActiveTurnsByChat {
		a.ActiveTurnsByChat[chatID] = append(a.ActiveTurnsByChat[chatID], ids...)
		a.TotalActiveTurns += len(ids)
	}
	for chatID, depth := range b.QueueDepthByChat {
		a.QueueDepthByChat[chatID] += depth
		a.TotalQueuedMessages += depth
		if mergedDepth := a.QueueDepthByChat[chatID]; mergedDepth > a.MaxQueueDepth {
			a.MaxQueueDepth = mergedDepth
			a.MaxQueueDepthChatID = chatID
		}
	}
	if !b.OldestQueuedAt.IsZero() && (a.OldestQueuedAt.IsZero() || b.OldestQueuedAt.Before(a.OldestQueuedAt)) {
		a.OldestQueuedAt = b.OldestQueuedAt
		a.OldestQueuedAge = b.OldestQueuedAge
		a.OldestQueuedChatID = b.OldestQueuedChatID
	}
	return a
}
