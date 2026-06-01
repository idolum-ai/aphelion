//go:build linux

package main

import (
	"context"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"time"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/internal/telegramcontrol"
	"github.com/idolum-ai/aphelion/session"
)

func (c telegramCommandControl) controlFacade() telegramcontrol.CommandControl {
	facade := telegramcontrol.CommandControl{
		Store:             c.store,
		DecisionDetacher:  c.decisionDetacher,
		ReinstallTemplate: reinstallTemplateMessage,
	}
	if c.router != nil {
		facade.Router = c.router
	}
	if c.ingress != nil {
		facade.Ingress = c.ingress
	}
	if c.rt != nil {
		facade.Runtime = c.rt
		facade.RevokeContinuation = func(chatID int64) (core.StopResult, error) {
			revoke, err := c.rt.RevokeContinuation(chatID)
			if err != nil {
				return core.StopResult{}, err
			}
			return core.StopResult{ContinuationRevoked: revoke.Revoked, ContinuationLabel: revoke.ContinuationLabel}, nil
		}
		facade.RevokeContinuationForMessage = func(msg core.InboundMessage) (core.StopResult, error) {
			revoke, err := c.rt.RevokeContinuationForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramruntime.CommandMessageScope(msg)})
			if err != nil {
				return core.StopResult{}, err
			}
			return core.StopResult{ContinuationRevoked: revoke.Revoked, ContinuationLabel: revoke.ContinuationLabel}, nil
		}
	}
	if c.resolver != nil {
		facade.Resolver = c.resolver
	}
	return facade
}

func (c telegramCommandControl) ContinuationState(chatID int64) (session.ContinuationState, error) {
	return c.controlFacade().ContinuationState(chatID)
}
func (c telegramCommandControl) ContinuationStateForMessage(msg core.InboundMessage) (session.ContinuationState, error) {
	return c.controlFacade().ContinuationStateForMessage(msg)
}
func (c telegramCommandControl) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	return c.controlFacade().ApproveContinuation(chatID, approverID)
}
func (c telegramCommandControl) ApproveContinuationBundle(chatID int64, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	return c.controlFacade().ApproveContinuationBundle(chatID, approverID, phaseIDs)
}
func (c telegramCommandControl) ApproveContinuationForMessage(msg core.InboundMessage, approverID int64) (session.ContinuationState, error) {
	return c.controlFacade().ApproveContinuationForMessage(msg, approverID)
}
func (c telegramCommandControl) ApproveContinuationBundleForMessage(msg core.InboundMessage, approverID int64, phaseIDs []string) (session.ContinuationState, error) {
	return c.controlFacade().ApproveContinuationBundleForMessage(msg, approverID, phaseIDs)
}
func (c telegramCommandControl) StopContinuation(chatID int64) (core.StopResult, error) {
	return c.controlFacade().StopContinuation(chatID)
}
func (c telegramCommandControl) StopContinuationForMessage(msg core.InboundMessage) (core.StopResult, error) {
	return c.controlFacade().StopContinuationForMessage(msg)
}
func (c telegramCommandControl) TriggerContinuation(ctx context.Context, chatID int64) error {
	return c.controlFacade().TriggerContinuation(ctx, chatID)
}
func (c telegramCommandControl) TriggerContinuationForMessage(ctx context.Context, msg core.InboundMessage) error {
	return c.controlFacade().TriggerContinuationForMessage(ctx, msg)
}
func (c telegramCommandControl) RecordTelegramCallbackError(chatID int64, callbackKind string, err error) {
	c.controlFacade().RecordTelegramCallbackError(chatID, callbackKind, err)
}
func (c telegramCommandControl) ToggleProgressView(ctx context.Context, chatID int64, senderID int64, runID int64, details bool) (bool, string, error) {
	return c.controlFacade().ToggleProgressView(ctx, chatID, senderID, runID, details)
}
func (c telegramCommandControl) ConfigureAutoApproval(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	return c.controlFacade().ConfigureAutoApproval(ctx, chatID, senderID, args)
}
func (c telegramCommandControl) ConfigureAutoApprovalForMessage(ctx context.Context, msg core.InboundMessage, args string) (string, error) {
	return c.controlFacade().ConfigureAutoApprovalForMessage(ctx, msg, args)
}
func (c telegramCommandControl) AutoApprovalStatus(ctx context.Context, chatID int64, senderID int64) (string, error) {
	return c.controlFacade().AutoApprovalStatus(ctx, chatID, senderID)
}
func (c telegramCommandControl) AutoApprovalStatusForMessage(ctx context.Context, msg core.InboundMessage) (string, error) {
	return c.controlFacade().AutoApprovalStatusForMessage(ctx, msg)
}
func (c telegramCommandControl) CreateApprovalWindowOfferForMessage(ctx context.Context, msg core.InboundMessage, sourceKind string, sourceID string, sourceDecisionKind string) (session.ApprovalWindowOffer, bool, error) {
	return c.controlFacade().CreateApprovalWindowOfferForMessage(ctx, msg, sourceKind, sourceID, sourceDecisionKind)
}
func (c telegramCommandControl) EnableApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage, duration time.Duration) (string, error) {
	return c.controlFacade().EnableApprovalWindowForMessage(ctx, msg, duration)
}
func (c telegramCommandControl) EnableApprovalWindowForMessageResult(ctx context.Context, msg core.InboundMessage, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	return c.controlFacade().EnableApprovalWindowForMessageResult(ctx, msg, duration)
}
func (c telegramCommandControl) DoubleApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error) {
	return c.controlFacade().DoubleApprovalWindowForMessage(ctx, msg)
}
func (c telegramCommandControl) CancelApprovalWindowForMessage(ctx context.Context, msg core.InboundMessage) (string, error) {
	return c.controlFacade().CancelApprovalWindowForMessage(ctx, msg)
}
func (c telegramCommandControl) CancelApprovalWindowForMessageResult(ctx context.Context, msg core.InboundMessage) (core.ApprovalWindowCancelResult, error) {
	return c.controlFacade().CancelApprovalWindowForMessageResult(ctx, msg)
}
func (c telegramCommandControl) EnableApprovalWindowOffer(ctx context.Context, offerID string, senderID int64, duration time.Duration) (string, error) {
	return c.controlFacade().EnableApprovalWindowOffer(ctx, offerID, senderID, duration)
}
func (c telegramCommandControl) EnableApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64, duration time.Duration) (core.ApprovalWindowEnableResult, error) {
	return c.controlFacade().EnableApprovalWindowOfferResult(ctx, offerID, senderID, duration)
}
func (c telegramCommandControl) DoubleApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error) {
	return c.controlFacade().DoubleApprovalWindowOffer(ctx, offerID, senderID)
}
func (c telegramCommandControl) CancelApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) (string, error) {
	return c.controlFacade().CancelApprovalWindowOffer(ctx, offerID, senderID)
}
func (c telegramCommandControl) CancelApprovalWindowOfferResult(ctx context.Context, offerID string, senderID int64) (core.ApprovalWindowCancelResult, error) {
	return c.controlFacade().CancelApprovalWindowOfferResult(ctx, offerID, senderID)
}
func (c telegramCommandControl) CloseApprovalWindowOffer(ctx context.Context, offerID string, senderID int64) error {
	return c.controlFacade().CloseApprovalWindowOffer(ctx, offerID, senderID)
}
func (c telegramCommandControl) ApprovalWindowOfferByID(offerID string) (session.ApprovalWindowOffer, bool, error) {
	return c.controlFacade().ApprovalWindowOfferByID(offerID)
}
func (c telegramCommandControl) PeekDecisionCallback(decisionID string, actor decision.CallbackActor) (decision.PendingDecision, bool) {
	return c.controlFacade().PeekDecisionCallback(decisionID, actor)
}
func (c telegramCommandControl) ResolveDecisionCallback(decisionID string, choice string, actor decision.CallbackActor) decision.ResolveResult {
	return c.controlFacade().ResolveDecisionCallback(decisionID, choice, actor)
}
func (c telegramCommandControl) RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error) {
	return c.controlFacade().RefreshContinuationProposal(ctx, chatID, reason)
}
func (c telegramCommandControl) RefreshContinuationProposalForMessage(ctx context.Context, msg core.InboundMessage, reason string) (session.ContinuationState, bool, error) {
	return c.controlFacade().RefreshContinuationProposalForMessage(ctx, msg, reason)
}

func (c telegramCommandControl) QueueReinstall(ctx context.Context, msg core.InboundMessage) error {
	return c.controlFacade().QueueReinstall(ctx, msg)
}
func (c telegramCommandControl) QueueDoctor(ctx context.Context, msg core.InboundMessage) error {
	return c.controlFacade().QueueDoctor(ctx, msg)
}

func (c telegramCommandControl) ensureDoctorIngressQueued(msg core.InboundMessage) (bool, error) {
	return c.controlFacade().EnsureDoctorIngressQueued(msg)
}

func (c telegramCommandControl) LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error) {
	return c.controlFacade().LatestDoctorReport(ctx, chatID, senderID)
}

func (c telegramCommandControl) MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	return c.controlFacade().MemoryReviewSnapshot(ctx, chatID, senderID, core.MemoryReviewSource(source))
}
func (c telegramCommandControl) MemoryReviewSnapshotForMessage(ctx context.Context, msg core.InboundMessage, source core.MemoryReviewSource) (core.MemoryReviewSnapshot, error) {
	return c.controlFacade().MemoryReviewSnapshotForMessage(ctx, msg, core.MemoryReviewSource(source))
}
func (c telegramCommandControl) MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	return c.controlFacade().MissionCommand(ctx, chatID, senderID, args)
}
func (c telegramCommandControl) MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error) {
	return c.controlFacade().MissionHome(ctx, chatID, senderID)
}
func (c telegramCommandControl) MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error) {
	return c.controlFacade().MissionDetails(ctx, chatID, senderID, missionID)
}
func (c telegramCommandControl) SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error) {
	return c.controlFacade().SetMissionPinned(ctx, chatID, senderID, missionID, pinned)
}
func (c telegramCommandControl) UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error) {
	return c.controlFacade().UpdateMissionStatus(ctx, chatID, senderID, missionID, status)
}
func (c telegramCommandControl) MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error) {
	return c.controlFacade().MissionLedgerHealth(ctx, senderID)
}
func (c telegramCommandControl) MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error) {
	return c.controlFacade().MissionActionProposal(ctx, chatID, senderID, missionID)
}
func (c telegramCommandControl) ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error) {
	return c.controlFacade().ApplyMissionActionProposalDecision(ctx, chatID, senderID, missionID, choice)
}
func (c telegramCommandControl) MissionAskPrompt(ctx context.Context, senderID int64, promptID string) (session.MissionAskPrompt, bool, error) {
	return c.controlFacade().MissionAskPrompt(ctx, senderID, promptID)
}
func (c telegramCommandControl) ResolveMissionAskPrompt(ctx context.Context, senderID int64, promptID string, status session.MissionAskStatus, summary string) (session.MissionAskPrompt, error) {
	return c.controlFacade().ResolveMissionAskPrompt(ctx, senderID, promptID, status, summary)
}

func (c telegramCommandControl) ModelSlotStatuses() ([]core.ModelSlotStatus, error) {
	return c.controlFacade().ModelSlotStatuses()
}
func (c telegramCommandControl) ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation {
	return c.controlFacade().ValidateModelSlotConfig(cfg)
}
func (c telegramCommandControl) SetModelSlotConfig(cfg core.ModelSlotConfig, actor string, reason string) (core.ModelSlotStatus, error) {
	return c.controlFacade().SetModelSlotConfig(cfg, actor, reason)
}
func (c telegramCommandControl) ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	return c.controlFacade().ClearModelSlot(slot, actor, reason)
}
func (c telegramCommandControl) ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error) {
	return c.controlFacade().ModelSlotHistory(slot, limit)
}

func (c telegramCommandControl) Status(chatID int64) core.SessionStatus {
	return c.controlFacade().Status(chatID)
}
func (c telegramCommandControl) StatusForMessage(msg core.InboundMessage) core.SessionStatus {
	return c.controlFacade().StatusForMessage(msg)
}
func (c telegramCommandControl) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	return c.controlFacade().StatusChat(chatID)
}
func (c telegramCommandControl) StatusChatForMessage(msg core.InboundMessage) (core.ChatStatusSnapshot, error) {
	return c.controlFacade().StatusChatForMessage(msg)
}
func (c telegramCommandControl) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	return c.controlFacade().StatusSystem(senderID)
}
func (c telegramCommandControl) AutonomyStatus(chatID int64, senderID int64) (core.AutonomyStatusSnapshot, error) {
	return c.controlFacade().AutonomyStatus(chatID, senderID)
}
func (c telegramCommandControl) AutonomyStatusForMessage(msg core.InboundMessage) (core.AutonomyStatusSnapshot, error) {
	return c.controlFacade().AutonomyStatusForMessage(msg)
}
func (c telegramCommandControl) ConfigureAutonomy(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	return c.controlFacade().ConfigureAutonomy(ctx, chatID, senderID, args)
}
func (c telegramCommandControl) ConfigureAutonomyForMessage(ctx context.Context, msg core.InboundMessage, args string) (string, error) {
	return c.controlFacade().ConfigureAutonomyForMessage(ctx, msg, args)
}
func (c telegramCommandControl) StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error) {
	return c.controlFacade().StatusDurables(senderID)
}
func (c telegramCommandControl) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	return c.controlFacade().StatusReadableSummary(ctx, view, statusText)
}
func (c telegramCommandControl) TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error) {
	return c.controlFacade().TailnetStatus(ctx, senderID)
}
func (c telegramCommandControl) TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error) {
	return c.controlFacade().TailnetSurfaces(senderID)
}
func (c telegramCommandControl) TailnetGrantBindings(senderID int64) ([]core.TailnetGrantBindingStatus, error) {
	return c.controlFacade().TailnetGrantBindings(senderID)
}
func (c telegramCommandControl) RevokeTailnetSurface(ctx context.Context, senderID int64, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error) {
	return c.controlFacade().RevokeTailnetSurface(ctx, senderID, surfaceID, reason)
}
func (c telegramCommandControl) CanRestart(senderID int64) bool {
	return c.controlFacade().CanRestart(senderID)
}
func (c telegramCommandControl) CurrentEfforts() (string, string) {
	return c.controlFacade().CurrentEfforts()
}
