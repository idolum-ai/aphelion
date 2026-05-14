//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/decision"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/runtime"
	"github.com/idolum-ai/aphelion/session"
)

type telegramCommandControl struct {
	router                 *core.Router
	ingress                *ingressSequencer
	rt                     *runtime.Runtime
	resolver               *principal.Resolver
	decisionDetacher       pendingDecisionDetacher
	detachPendingOnRestart bool
	durableTools           durableWizardToolExecutor
}

type pendingDecisionDetacher interface {
	DetachByOwner(ctx context.Context, ownerKey string) (int, error)
	DetachAll(ctx context.Context) (int, error)
}

type durableWizardToolExecutor interface {
	ExecuteForSessionPrincipal(ctx context.Context, p principal.Principal, key session.SessionKey, name string, input json.RawMessage) (string, error)
}

func (c telegramCommandControl) Stop(chatID int64) core.StopResult {
	result := core.StopResult{}
	if c.router != nil {
		result = c.router.Stop(chatID)
	}
	if c.rt != nil {
		revoke, err := c.rt.RevokeContinuation(chatID)
		if err == nil {
			result.ContinuationRevoked = revoke.Revoked
			result.ContinuationLabel = revoke.ContinuationLabel
		}
	}
	c.maybeFlushMemory(chatID, "stop")
	return result
}

func (c telegramCommandControl) MarkStreamControlStopping(streamID string, chatID int64) bool {
	if c.rt == nil {
		return false
	}
	return c.rt.MarkStreamControlStopping(streamID, chatID)
}

func (c telegramCommandControl) New(chatID int64, senderID int64) (core.NewSessionResult, error) {
	stopped := c.Stop(chatID)
	result := core.NewSessionResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.decisionDetacher != nil {
		ownerKey := decision.OwnerKey(chatID, senderID)
		removed, err := c.decisionDetacher.DetachByOwner(context.Background(), ownerKey)
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.PendingDecisionsDetached = removed
	}
	if c.rt != nil {
		cleared, err := c.rt.ClearChatSessionContext(chatID)
		if err != nil {
			return core.NewSessionResult{}, err
		}
		result.ContextCleared = cleared
	}
	return result, nil
}

func (c telegramCommandControl) Detach(chatID int64, senderID int64) (core.DetachResult, error) {
	stopped := c.Stop(chatID)
	result := core.DetachResult{
		ActiveCanceled:      stopped.ActiveCanceled,
		QueuedDropped:       stopped.QueuedDropped,
		ContinuationRevoked: stopped.ContinuationRevoked,
	}
	if c.decisionDetacher == nil {
		return result, nil
	}
	ownerKey := decision.OwnerKey(chatID, senderID)
	removed, err := c.decisionDetacher.DetachByOwner(context.Background(), ownerKey)
	if err != nil {
		return core.DetachResult{}, err
	}
	result.PendingDecisionsDetached = removed
	return result, nil
}

func (c telegramCommandControl) Restart(chatID int64) error {
	if c.detachPendingOnRestart && c.decisionDetacher != nil {
		removed, err := c.decisionDetacher.DetachAll(context.Background())
		if err != nil {
			log.Printf("WARN restart detach pending decisions failed err=%v", err)
		} else if removed > 0 {
			log.Printf("WARN restart detached %d pending decision(s) before exit", removed)
		}
	}
	log.Printf("WARN restart requested via telegram chat_id=%d", chatID)
	if c.rt != nil {
		c.rt.BeginShutdown()
	}
	go func() {
		time.Sleep(restartExitWait)
		processExit(exitCodeFailure)
	}()
	return nil
}

func (c telegramCommandControl) maybeFlushMemory(chatID int64, reason string) {
	if c.rt == nil {
		return
	}
	if err := c.rt.FlushChatMemory(context.Background(), chatID, reason); err != nil {
		log.Printf("WARN memory flush skipped chat_id=%d reason=%s err=%v", chatID, strings.TrimSpace(reason), err)
		c.rt.ReportOperationalIssue(context.Background(), "memory_flush", fmt.Errorf("chat_id=%d reason=%s: %w", chatID, strings.TrimSpace(reason), err))
	}
}

func (c telegramCommandControl) Status(chatID int64) core.SessionStatus {
	status := c.router.Status(chatID)
	if c.rt == nil {
		return status
	}
	diagnostics, err := c.rt.StatusDiagnostics(chatID)
	if err != nil {
		log.Printf("WARN telegram status diagnostics failed chat_id=%d err=%v", chatID, err)
		status.Diagnostics = append(status.Diagnostics, "Runtime diagnostics are temporarily unavailable.")
		return status
	}
	status.Diagnostics = append(status.Diagnostics, diagnostics...)
	return status
}

func (c telegramCommandControl) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.router != nil {
		routerSnapshot = c.router.Snapshot()
	}
	if c.rt == nil {
		chat := core.ChatStatusSnapshot{
			GeneratedAt:   time.Now().UTC(),
			ChatID:        chatID,
			RestartHealth: core.RestartHealthSnapshot{},
		}
		if ids := routerSnapshot.ActiveTurnsByChat[chatID]; len(ids) > 0 {
			chat.ActiveTurnIDs = append(chat.ActiveTurnIDs, ids...)
		}
		chat.QueueDepth = routerSnapshot.QueueDepthByChat[chatID]
		return chat, nil
	}
	return c.rt.ChatStatusSnapshot(chatID, routerSnapshot)
}

func (c telegramCommandControl) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.SystemStatusSnapshot{}, fmt.Errorf("status view denied")
	}
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.router != nil {
		routerSnapshot = c.router.Snapshot()
	}
	if c.rt == nil {
		return core.SystemStatusSnapshot{
			GeneratedAt:       time.Now().UTC(),
			ActiveTurnsByChat: routerSnapshot.ActiveTurnsByChat,
			QueueDepthByChat:  routerSnapshot.QueueDepthByChat,
		}, nil
	}
	return c.rt.SystemStatusSnapshot(routerSnapshot)
}

func (c telegramCommandControl) AutonomyStatus(chatID int64, senderID int64) (core.AutonomyStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.AutonomyStatusSnapshot{}, fmt.Errorf("auto mode view denied")
	}
	if c.rt == nil {
		policy := config.EffectiveAutonomyPolicy(nil)
		return core.AutonomyStatusSnapshot{
			GeneratedAt:         time.Now().UTC(),
			DefaultMode:         policy.DefaultMode,
			Ceiling:             policy.Ceiling,
			AllowLiveOverrides:  policy.AllowLiveOverrides,
			MaxOverrideDuration: policy.MaxOverrideDuration,
			Source:              "default",
			AuthorityBehavior:   "approval grants require an open auto mode gate",
		}, nil
	}
	return c.rt.ChatAutonomyStatusSnapshot(chatID, senderID)
}

func (c telegramCommandControl) ConfigureAutonomy(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.rt == nil {
		return "Autonomy controls are unavailable.", nil
	}
	return c.rt.ConfigureAutonomy(ctx, chatID, senderID, args)
}

func (c telegramCommandControl) StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.DurableAgentsStatusSnapshot{}, fmt.Errorf("status view denied")
	}
	if c.rt == nil {
		return core.DurableAgentsStatusSnapshot{
			GeneratedAt: time.Now().UTC(),
		}, nil
	}
	return c.rt.DurableAgentsStatusSnapshot()
}

func (c telegramCommandControl) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	if c.rt == nil {
		return ""
	}
	return c.rt.StatusReadableSummary(ctx, view, statusText)
}

func (c telegramCommandControl) TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.TailnetStatusSnapshot{}, fmt.Errorf("tailnet status denied")
	}
	if c.rt == nil {
		return core.TailnetStatusSnapshot{
			GeneratedAt: time.Now().UTC(),
			Enabled:     false,
			Backend:     "disabled",
			Status:      "disabled",
			Summary:     "Tailscale integration is disabled.",
		}, nil
	}
	return c.rt.TailnetStatusSnapshot(ctx)
}

func (c telegramCommandControl) TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("tailnet surfaces denied")
	}
	if c.rt == nil {
		return nil, nil
	}
	return c.rt.TailnetSurfacesSnapshot()
}

func (c telegramCommandControl) TailnetGrantBindings(senderID int64) ([]core.TailnetGrantBindingStatus, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("tailnet grant bindings denied")
	}
	if c.rt == nil {
		return nil, nil
	}
	return c.rt.TailnetGrantBindingsSnapshot()
}

func (c telegramCommandControl) RevokeTailnetSurface(ctx context.Context, senderID int64, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error) {
	if !c.CanRestart(senderID) {
		return core.TailnetSurfaceStatus{}, false, fmt.Errorf("tailnet surface revoke denied")
	}
	if c.rt == nil {
		return core.TailnetSurfaceStatus{}, false, nil
	}
	return c.rt.RevokeTailnetSurface(ctx, surfaceID, reason)
}

func (c telegramCommandControl) ContinuationState(chatID int64) (session.ContinuationState, error) {
	if c.rt == nil {
		return session.ContinuationState{}, nil
	}
	return c.rt.ContinuationState(chatID)
}

func (c telegramCommandControl) CanRestart(senderID int64) bool {
	if c.rt != nil {
		return c.rt.IsTelegramAdmin(senderID)
	}
	if c.resolver == nil {
		return false
	}
	actor, ok := c.resolver.ResolveTelegramUser(senderID)
	return ok && actor.Role == principal.RoleAdmin
}

func (c telegramCommandControl) ApproveContinuation(chatID int64, approverID int64) (session.ContinuationState, error) {
	return c.rt.ApproveContinuation(chatID, approverID)
}

func (c telegramCommandControl) StopContinuation(chatID int64) (core.StopResult, error) {
	revoke, err := c.rt.RevokeContinuation(chatID)
	if err != nil {
		return core.StopResult{}, err
	}
	return core.StopResult{ContinuationRevoked: revoke.Revoked, ContinuationLabel: revoke.ContinuationLabel}, nil
}

func (c telegramCommandControl) TriggerContinuation(ctx context.Context, chatID int64) error {
	if c.rt == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return c.rt.TriggerContinuation(ctx, chatID)
}

func (c telegramCommandControl) RecordTelegramCallbackError(chatID int64, callbackKind string, err error) {
	if c.rt == nil || err == nil {
		return
	}
	c.rt.RecordTelegramCallbackError(chatID, callbackKind, err)
}

func (c telegramCommandControl) ConfigureAutoApproval(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.rt == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.rt.ConfigureAutoApproval(ctx, chatID, senderID, args)
}

func (c telegramCommandControl) AutoApprovalStatus(ctx context.Context, chatID int64, senderID int64) (string, error) {
	if c.rt == nil {
		return "Auto approvals are unavailable.", nil
	}
	return c.rt.AutoApprovalStatus(ctx, chatID, senderID)
}

func (c telegramCommandControl) RefreshContinuationProposal(ctx context.Context, chatID int64, reason string) (session.ContinuationState, bool, error) {
	if c.rt == nil {
		return session.ContinuationState{}, false, fmt.Errorf("runtime is unavailable")
	}
	return c.rt.RefreshContinuationProposal(ctx, chatID, reason)
}

func (c telegramCommandControl) QueueReinstall(ctx context.Context, msg core.InboundMessage) error {
	if c.router == nil {
		return fmt.Errorf("router is not configured")
	}
	queued := msg
	queued.Text = reinstallTemplateMessage
	queued.Raw = nil
	if c.ingress != nil {
		c.ingress.Enqueue(ctx, queued)
		return nil
	}
	turnCtx, cancel := newTurnContext(ctx, turnTimeout)
	c.router.Route(turnCtx, queued)
	cancel()
	return nil
}

func (c telegramCommandControl) QueueDoctor(ctx context.Context, msg core.InboundMessage) error {
	if c.rt == nil {
		return fmt.Errorf("runtime is not configured")
	}
	return c.rt.StartDoctor(ctx, msg)
}

func (c telegramCommandControl) CurrentEfforts() (string, string) {
	return c.rt.CurrentEfforts()
}

func (c telegramCommandControl) ModelSlotStatuses() ([]core.ModelSlotStatus, error) {
	if c.rt == nil {
		return nil, fmt.Errorf("runtime is not configured")
	}
	return c.rt.ModelSlotStatuses()
}

func (c telegramCommandControl) ValidateModelSlotConfig(cfg core.ModelSlotConfig) core.ModelValidation {
	if c.rt == nil {
		validation := core.ValidateModelSlotConfig(cfg, core.ModelSlotUsesTools(cfg.Slot))
		validation.Valid = false
		validation.Error = "runtime is not configured"
		return validation
	}
	return c.rt.ValidateModelSlotConfig(cfg)
}

func (c telegramCommandControl) SetModelSlotConfig(cfg core.ModelSlotConfig, actor string, reason string, ttl time.Duration) (core.ModelSlotStatus, error) {
	if c.rt == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.rt.SetModelSlotOverride(cfg, actor, reason, ttl)
}

func (c telegramCommandControl) RollbackModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	if c.rt == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.rt.RollbackModelSlot(slot, actor, reason)
}

func (c telegramCommandControl) ClearModelSlot(slot string, actor string, reason string) (core.ModelSlotStatus, error) {
	if c.rt == nil {
		return core.ModelSlotStatus{}, fmt.Errorf("runtime is not configured")
	}
	return c.rt.ClearModelSlot(slot, actor, reason)
}

func (c telegramCommandControl) ModelSlotHistory(slot string, limit int) ([]session.ModelSlotOverrideRecord, error) {
	if c.rt == nil {
		return nil, fmt.Errorf("runtime is not configured")
	}
	return c.rt.ModelSlotHistory(slot, limit)
}

func (c telegramCommandControl) RunDurableWizard(ctx context.Context, chatID int64, senderID int64, action string, agentID string, wizardAnswers map[string]any) (string, error) {
	if c.durableTools == nil {
		return "", fmt.Errorf("durable wizard controls are unavailable")
	}
	if !c.CanRestart(senderID) {
		return "", fmt.Errorf("durable wizard controls are admin only")
	}

	actor := principal.Principal{
		Role:           principal.RoleAdmin,
		TelegramUserID: senderID,
	}
	if c.resolver != nil {
		resolved, ok := c.resolver.ResolveTelegramUser(senderID)
		if !ok || resolved.Role != principal.RoleAdmin {
			return "", fmt.Errorf("durable wizard controls are admin only")
		}
		actor = resolved
	}

	request := map[string]any{
		"action":   strings.TrimSpace(action),
		"agent_id": strings.TrimSpace(agentID),
	}
	if len(wizardAnswers) > 0 {
		request["wizard_answers"] = wizardAnswers
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	key := session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(chatID, 10),
		},
	}
	return c.durableTools.ExecuteForSessionPrincipal(ctx, actor, key, "durable_agent", payload)
}

func (c telegramCommandControl) DurableAgentsList(senderID int64) ([]core.DurableAgentStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("durable-agent controls are admin only")
	}
	if c.rt == nil {
		return nil, nil
	}
	snapshot, err := c.rt.DurableAgentsStatusSnapshot()
	if err != nil {
		return nil, err
	}
	return append([]core.DurableAgentStatusSnapshot(nil), snapshot.Agents...), nil
}

func (c telegramCommandControl) StartDurableAgentConversation(ctx context.Context, chatID int64, senderID int64, agentID string) (string, error) {
	if c.durableTools == nil {
		return "", fmt.Errorf("durable-agent controls are unavailable")
	}
	if !c.CanRestart(senderID) {
		return "", fmt.Errorf("durable-agent controls are admin only")
	}

	actor := principal.Principal{
		Role:           principal.RoleAdmin,
		TelegramUserID: senderID,
	}
	if c.resolver != nil {
		resolved, ok := c.resolver.ResolveTelegramUser(senderID)
		if !ok || resolved.Role != principal.RoleAdmin {
			return "", fmt.Errorf("durable-agent controls are admin only")
		}
		actor = resolved
	}

	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("durable agent id is required")
	}
	request := map[string]any{
		"action":   "conversation_send",
		"agent_id": agentID,
		"message":  "Scheduled parent-child check-in from /agents. Share current status, blockers, and concrete next actions.",
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	key := session.SessionKey{
		ChatID: chatID,
		UserID: 0,
		Scope: session.ScopeRef{
			Kind: session.ScopeKindTelegramDM,
			ID:   strconv.FormatInt(chatID, 10),
		},
	}
	if _, err := c.durableTools.ExecuteForSessionPrincipal(ctx, actor, key, "durable_agent", payload); err != nil {
		return "", err
	}

	wakeStatus := "queued for next child wake"
	if c.rt != nil {
		go func(agent string) {
			wakeCtx, cancel := newTurnContext(context.Background(), turnTimeout)
			defer cancel()
			if err := c.rt.RunDurableAgentChildWake(wakeCtx, agent, time.Now().UTC()); err != nil {
				log.Printf("WARN durable-agent background wake failed agent_id=%s err=%v", strings.TrimSpace(agent), err)
			}
		}(agentID)
		wakeStatus = "wake attempt started"
	}
	return fmt.Sprintf("Started background conversation with durable agent %s (%s).", agentID, wakeStatus), nil
}

func (c telegramCommandControl) MemoryReviewSnapshot(ctx context.Context, chatID int64, senderID int64, source memoryReviewSource) (memoryReviewSnapshot, error) {
	if c.rt == nil {
		return memoryReviewSnapshot{
			GeneratedAt: time.Now().UTC(),
			Source:      core.NormalizeMemoryReviewSource(string(source)),
			Query:       "",
		}, nil
	}
	return c.rt.MemoryReviewSnapshot(ctx, chatID, senderID, core.NormalizeMemoryReviewSource(string(source)))
}

func (c telegramCommandControl) MissionCommand(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.rt == nil {
		return "Mission Ledger is unavailable.", nil
	}
	return c.rt.MissionCommand(ctx, chatID, senderID, args)
}

func (c telegramCommandControl) MissionHome(ctx context.Context, chatID int64, senderID int64) ([]session.MissionState, session.WorkingObjective, bool, error) {
	if c.rt == nil {
		return nil, session.WorkingObjective{}, false, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.rt.MissionHome(ctx, chatID, senderID)
}

func (c telegramCommandControl) MissionDetails(ctx context.Context, chatID int64, senderID int64, missionID string) (session.MissionState, []session.MissionEvent, error) {
	if c.rt == nil {
		return session.MissionState{}, nil, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.rt.MissionDetails(ctx, chatID, senderID, missionID)
}

func (c telegramCommandControl) SetMissionPinned(ctx context.Context, chatID int64, senderID int64, missionID string, pinned bool) (session.MissionState, error) {
	if c.rt == nil {
		return session.MissionState{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.rt.SetMissionPinned(ctx, chatID, senderID, missionID, pinned)
}

func (c telegramCommandControl) UpdateMissionStatus(ctx context.Context, chatID int64, senderID int64, missionID string, status session.MissionStatus) (session.MissionState, error) {
	if c.rt == nil {
		return session.MissionState{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.rt.UpdateMissionStatus(ctx, chatID, senderID, missionID, status)
}

func (c telegramCommandControl) MissionLedgerHealth(ctx context.Context, senderID int64) (session.MissionLedgerHealth, error) {
	if c.rt == nil {
		return session.MissionLedgerHealth{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.rt.MissionLedgerHealth(ctx, senderID)
}

func (c telegramCommandControl) LatestDoctorReport(ctx context.Context, chatID int64, senderID int64) (session.DoctorReportRecord, bool, error) {
	if c.rt == nil {
		return session.DoctorReportRecord{}, false, fmt.Errorf("Doctor report storage is unavailable.")
	}
	return c.rt.LatestDoctorReport(ctx, chatID, senderID)
}

func (c telegramCommandControl) MissionActionProposal(ctx context.Context, chatID int64, senderID int64, missionID string) (session.ActionProposal, error) {
	if c.rt == nil {
		return session.ActionProposal{}, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.rt.MissionActionProposal(ctx, chatID, senderID, missionID)
}

func (c telegramCommandControl) ApplyMissionActionProposalDecision(ctx context.Context, chatID int64, senderID int64, missionID string, choice string) (session.MissionState, bool, error) {
	if c.rt == nil {
		return session.MissionState{}, false, fmt.Errorf("Mission Ledger is unavailable.")
	}
	return c.rt.ApplyMissionActionProposalDecision(ctx, chatID, senderID, missionID, choice)
}

func (c telegramCommandControl) MemoryFocus(chatID int64) (core.MemoryFocus, bool) {
	if c.rt == nil {
		return core.MemoryFocus{}, false
	}
	return c.rt.MemoryFocus(chatID)
}

func (c telegramCommandControl) SetMemoryFocus(chatID int64, focus core.MemoryFocus) {
	if c.rt == nil {
		return
	}
	c.rt.SetMemoryFocus(chatID, focus)
}

func (c telegramCommandControl) ClearMemoryFocus(chatID int64) bool {
	if c.rt == nil {
		return false
	}
	return c.rt.ClearMemoryFocus(chatID)
}
