//go:build linux

package telegramcontrol

import (
	"context"
	"fmt"
	"github.com/idolum-ai/aphelion/config"
	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/internal/telegramruntime"
	"github.com/idolum-ai/aphelion/principal"
	"log"
	"time"
)

func (c CommandControl) Status(chatID int64) core.SessionStatus {
	status := core.SessionStatus{}
	if c.Ingress != nil {
		status = mergeSessionStatus(status, c.Ingress.Status(chatID))
	}
	if c.Router != nil {
		status = mergeSessionStatus(status, c.Router.Status(chatID))
	}
	if c.Runtime == nil {
		return status
	}
	diagnostics, err := c.Runtime.StatusDiagnostics(chatID)
	if err != nil {
		log.Printf("WARN telegram status diagnostics failed chat_id=%d err=%v", chatID, err)
		status.Diagnostics = append(status.Diagnostics, "Runtime diagnostics are temporarily unavailable.")
		return status
	}
	status.Diagnostics = append(status.Diagnostics, diagnostics...)
	return status
}

func (c CommandControl) StatusForMessage(msg core.InboundMessage) core.SessionStatus {
	status := core.SessionStatus{}
	if c.Ingress != nil {
		status = mergeSessionStatus(status, c.Ingress.StatusForMessage(msg))
	}
	if c.Router != nil {
		status = mergeSessionStatus(status, c.Router.StatusForMessage(msg))
	}
	return status
}

func (c CommandControl) StatusChat(chatID int64) (core.ChatStatusSnapshot, error) {
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.Router != nil {
		routerSnapshot = c.Router.Snapshot()
	}
	if c.Ingress != nil {
		routerSnapshot = mergeRouterStatusSnapshots(routerSnapshot, c.Ingress.Snapshot())
	}
	if c.Runtime == nil {
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
	return c.Runtime.ChatStatusSnapshot(chatID, routerSnapshot)
}

func (c CommandControl) StatusChatForMessage(msg core.InboundMessage) (core.ChatStatusSnapshot, error) {
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.Router != nil {
		routerSnapshot = c.Router.Snapshot()
	}
	if c.Ingress != nil {
		routerSnapshot = mergeRouterStatusSnapshots(routerSnapshot, c.Ingress.Snapshot())
	}
	if c.Runtime == nil {
		chat := core.ChatStatusSnapshot{
			GeneratedAt:   time.Now().UTC(),
			ChatID:        msg.ChatID,
			RestartHealth: core.RestartHealthSnapshot{},
			SessionID:     telegramruntime.SessionTargetForMessage(msg).SessionID,
			ScopeKind:     string(telegramruntime.SessionTargetForMessage(msg).Scope.Kind),
			ScopeID:       telegramruntime.SessionTargetForMessage(msg).Scope.ID,
		}
		return chat, nil
	}
	return c.Runtime.ChatStatusSnapshotForKey(SessionKeyForMessage(msg), routerSnapshot)
}

func (c CommandControl) StatusSystem(senderID int64) (core.SystemStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.SystemStatusSnapshot{}, fmt.Errorf("status view denied")
	}
	routerSnapshot := core.RouterStatusSnapshot{}
	if c.Router != nil {
		routerSnapshot = c.Router.Snapshot()
	}
	if c.Ingress != nil {
		routerSnapshot = mergeRouterStatusSnapshots(routerSnapshot, c.Ingress.Snapshot())
	}
	if c.Runtime == nil {
		return core.SystemStatusSnapshot{
			GeneratedAt:       time.Now().UTC(),
			ActiveTurnsByChat: routerSnapshot.ActiveTurnsByChat,
			QueueDepthByChat:  routerSnapshot.QueueDepthByChat,
		}, nil
	}
	return c.Runtime.SystemStatusSnapshot(routerSnapshot)
}

func (c CommandControl) AutonomyStatus(chatID int64, senderID int64) (core.AutonomyStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.AutonomyStatusSnapshot{}, fmt.Errorf("auto mode view denied")
	}
	if c.Runtime == nil {
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
	return c.Runtime.ChatAutonomyStatusSnapshot(chatID, senderID)
}

func (c CommandControl) AutonomyStatusForMessage(msg core.InboundMessage) (core.AutonomyStatusSnapshot, error) {
	if !c.CanRestart(msg.SenderID) {
		return core.AutonomyStatusSnapshot{}, fmt.Errorf("auto mode view denied")
	}
	if c.Runtime == nil {
		return c.AutonomyStatus(msg.ChatID, msg.SenderID)
	}
	return c.Runtime.ChatAutonomyStatusSnapshotForKey(SessionKeyForMessage(msg), msg.SenderID)
}

func (c CommandControl) ConfigureAutonomy(ctx context.Context, chatID int64, senderID int64, args string) (string, error) {
	if c.Runtime == nil {
		return "Autonomy controls are unavailable.", nil
	}
	return c.Runtime.ConfigureAutonomy(ctx, chatID, senderID, args)
}

func (c CommandControl) ConfigureAutonomyForMessage(ctx context.Context, msg core.InboundMessage, args string) (string, error) {
	if c.Runtime == nil {
		return "Autonomy controls are unavailable.", nil
	}
	return c.Runtime.ConfigureAutonomyForKey(ctx, SessionKeyForMessage(msg), msg.SenderID, args)
}

func (c CommandControl) StatusDurables(senderID int64) (core.DurableAgentsStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.DurableAgentsStatusSnapshot{}, fmt.Errorf("status view denied")
	}
	if c.Runtime == nil {
		return core.DurableAgentsStatusSnapshot{
			GeneratedAt: time.Now().UTC(),
		}, nil
	}
	return c.Runtime.DurableAgentsStatusSnapshot()
}

func (c CommandControl) StatusReadableSummary(ctx context.Context, view string, statusText string) string {
	if c.Runtime == nil {
		return ""
	}
	return c.Runtime.StatusReadableSummary(ctx, view, statusText)
}

func (c CommandControl) TailnetStatus(ctx context.Context, senderID int64) (core.TailnetStatusSnapshot, error) {
	if !c.CanRestart(senderID) {
		return core.TailnetStatusSnapshot{}, fmt.Errorf("tailnet status denied")
	}
	if c.Runtime == nil {
		return core.TailnetStatusSnapshot{
			GeneratedAt: time.Now().UTC(),
			Enabled:     false,
			Backend:     "disabled",
			Status:      "disabled",
			Summary:     "Tailscale integration is disabled.",
		}, nil
	}
	return c.Runtime.TailnetStatusSnapshot(ctx)
}

func (c CommandControl) TailnetSurfaces(senderID int64) ([]core.TailnetSurfaceStatus, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("tailnet surfaces denied")
	}
	if c.Runtime == nil {
		return nil, nil
	}
	return c.Runtime.TailnetSurfacesSnapshot()
}

func (c CommandControl) TailnetGrantBindings(senderID int64) ([]core.TailnetGrantBindingStatus, error) {
	if !c.CanRestart(senderID) {
		return nil, fmt.Errorf("tailnet grant bindings denied")
	}
	if c.Runtime == nil {
		return nil, nil
	}
	return c.Runtime.TailnetGrantBindingsSnapshot()
}

func (c CommandControl) RevokeTailnetSurface(ctx context.Context, senderID int64, surfaceID string, reason string) (core.TailnetSurfaceStatus, bool, error) {
	if !c.CanRestart(senderID) {
		return core.TailnetSurfaceStatus{}, false, fmt.Errorf("tailnet surface revoke denied")
	}
	if c.Runtime == nil {
		return core.TailnetSurfaceStatus{}, false, nil
	}
	return c.Runtime.RevokeTailnetSurface(ctx, surfaceID, reason)
}

func (c CommandControl) CanRestart(senderID int64) bool {
	if c.Runtime != nil {
		return c.Runtime.IsTelegramAdmin(senderID)
	}
	if c.Resolver == nil {
		return false
	}
	actor, ok := c.Resolver.ResolveTelegramUser(senderID)
	return ok && actor.Role == principal.RoleAdmin
}

func (c CommandControl) CurrentEfforts() (string, string) {
	return c.Runtime.CurrentEfforts()
}
