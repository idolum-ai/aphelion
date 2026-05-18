//go:build linux

package main

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/principal"
	"github.com/idolum-ai/aphelion/session"
	"log"
	"time"
)

func (c telegramCommandControl) Status(chatID int64) core.SessionStatus {
	status := core.SessionStatus{}
	if c.ingress != nil {
		status = mergeSessionStatus(status, c.ingress.Status(chatID))
	}
	if c.router != nil {
		status = mergeSessionStatus(status, c.router.Status(chatID))
	}
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

func (c telegramCommandControl) StatusForMessage(msg core.InboundMessage) core.SessionStatus {
	status := core.SessionStatus{}
	if c.ingress != nil {
		status = mergeSessionStatus(status, c.ingress.StatusForMessage(msg))
	}
	if c.router != nil {
		status = mergeSessionStatus(status, c.router.StatusForMessage(msg))
	}
	return status
}

func (c telegramCommandControl) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.router != nil {
		routerSnapshot = c.router.Snapshot()
	}
	if c.ingress != nil {
		routerSnapshot = mergeRouterStatusSnapshots(routerSnapshot, c.ingress.Snapshot())
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

func (c telegramCommandControl) StatusChatForMessage(msg core.InboundMessage) (core.ChatStatusSnapshot, error) {
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.router != nil {
		routerSnapshot = c.router.Snapshot()
	}
	if c.ingress != nil {
		routerSnapshot = mergeRouterStatusSnapshots(routerSnapshot, c.ingress.Snapshot())
	}
	if c.rt == nil {
		chat := core.ChatStatusSnapshot{
			GeneratedAt:   time.Now().UTC(),
			ChatID:        msg.ChatID,
			RestartHealth: core.RestartHealthSnapshot{},
			SessionID:     telegramSessionTargetForMessage(msg).SessionID,
			ScopeKind:     string(telegramSessionTargetForMessage(msg).Scope.Kind),
			ScopeID:       telegramSessionTargetForMessage(msg).Scope.ID,
		}
		return chat, nil
	}
	return c.rt.ChatStatusSnapshotForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)}, routerSnapshot)
}

func (c telegramCommandControl) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.SystemStatusSnapshot{}, fmt.Errorf("status view denied")
	}
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.router != nil {
		routerSnapshot = c.router.Snapshot()
	}
	if c.ingress != nil {
		routerSnapshot = mergeRouterStatusSnapshots(routerSnapshot, c.ingress.Snapshot())
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

func (c telegramCommandControl) AutonomyStatusForMessage(msg core.InboundMessage) (core.AutonomyStatusSnapshot, error) {
	if !c.CanRestart(msg.SenderID) {
		return core.AutonomyStatusSnapshot{}, fmt.Errorf("auto mode view denied")
	}
	if c.rt == nil {
		return c.AutonomyStatus(msg.ChatID, msg.SenderID)
	}
	return c.rt.ChatAutonomyStatusSnapshotForKey(session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)}, msg.SenderID)
}

func (c telegramCommandControl) ConfigureAutonomy(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.rt == nil {
		return "Autonomy controls are unavailable.", nil
	}
	return c.rt.ConfigureAutonomy(ctx, chatID, senderID, args)
}

func (c telegramCommandControl) ConfigureAutonomyForMessage(ctx context.Context, msg core.InboundMessage, args string) (string, error) {
	if c.rt == nil {
		return "Autonomy controls are unavailable.", nil
	}
	return c.rt.ConfigureAutonomyForKey(ctx, session.SessionKey{ChatID: msg.ChatID, UserID: 0, Scope: telegramCommandMessageScope(msg)}, msg.SenderID, args)
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

func (c telegramCommandControl) CurrentEfforts() (string, string) {
	return c.rt.CurrentEfforts()
}
